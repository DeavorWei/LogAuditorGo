package task

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"logauditorgo/internal/logparser"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

type FileUploadItem struct {
	FileName string
	FileSize int64
	Content  string
}

type Service struct {
	globalDB    *gorm.DB
	taskDir     string
	matchEngine *matcher.MatchEngine
	rcaEngine   *rootcause.Engine
}

func NewService(globalDB *gorm.DB, taskDir string, matchEngine *matcher.MatchEngine, rcaEngine *rootcause.Engine) *Service {
	return &Service{
		globalDB:    globalDB,
		taskDir:     taskDir,
		matchEngine: matchEngine,
		rcaEngine:   rcaEngine,
	}
}

// CreateEmptyTask 创建初始状态为 PENDING 的空审计任务
func (s *Service) CreateEmptyTask(taskName string, deviceType string) (*model.TaskInfo, error) {
	taskID := strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	if taskName == "" {
		taskName = fmt.Sprintf("Audit-%s", time.Now().Format("20060102-150405"))
	}
	if deviceType == "" {
		deviceType = "CloudEngine"
	}

	taskDB, dbPath, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("create task db failed: %w", err)
	}

	taskInfo := &model.TaskInfo{
		TaskID:     taskID,
		TaskName:   taskName,
		DeviceType: deviceType,
		DBPath:     dbPath,
		Status:     model.TaskStatusPending,
		StartTime:  time.Now(),
	}

	// 写入全局库与任务库
	if err := s.globalDB.Create(taskInfo).Error; err != nil {
		return nil, fmt.Errorf("save task info to global db failed: %w", err)
	}
	if err := taskDB.Create(taskInfo).Error; err != nil {
		return nil, fmt.Errorf("save task info to task db failed: %w", err)
	}

	logger.Log.Infof("[Task Service] Created empty task '%s' (ID: %s, Device: %s)", taskName, taskID, deviceType)
	return taskInfo, nil
}

// CreateAndRunTask 创建并执行日志分析任务（兼容已有单步创建调用）
func (s *Service) CreateAndRunTask(taskName string, deviceType string, logContent string) (*model.TaskInfo, error) {
	taskInfo, err := s.CreateEmptyTask(taskName, deviceType)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(logContent) == "" {
		return taskInfo, nil
	}

	item := FileUploadItem{
		FileName: "manual_input.txt",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}

	return s.ImportLogs(taskInfo.TaskID, []FileUploadItem{item}, "overwrite")
}

// GetTaskFiles 获取任务中已上传的文件列表
func (s *Service) GetTaskFiles(taskID string) ([]model.TaskFile, error) {
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var files []model.TaskFile
	err = taskDB.Order("created_at asc, id asc").Find(&files).Error
	return files, err
}

// ImportLogs 导入/补充导入日志文件，支持同名文件冲突模式（"overwrite" 或 "skip"）
func (s *Service) ImportLogs(taskID string, items []FileUploadItem, conflictMode string) (*model.TaskInfo, error) {
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("open task db failed: %w", err)
	}

	var taskInfo model.TaskInfo
	if err := taskDB.First(&taskInfo, "task_id = ?", taskID).Error; err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	taskInfo.Status = model.TaskStatusProcessing
	s.globalDB.Save(&taskInfo)
	taskDB.Save(&taskInfo)

	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("Panic in task processing: %v", r)
			logger.Log.Errorf("[Task Service] %s", errStr)
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = errStr
			s.globalDB.Save(&taskInfo)
			taskDB.Save(&taskInfo)
		}
	}()

	// 获取已存在的文件记录
	var existingFileList []model.TaskFile
	taskDB.Find(&existingFileList)
	existingMap := make(map[string]model.TaskFile)
	for _, f := range existingFileList {
		existingMap[f.FileName] = f
	}

	var allNewLogRecords []model.LogRecord

	for _, item := range items {
		cleanName := filepath.Base(strings.TrimSpace(item.FileName))
		if cleanName == "" {
			cleanName = "uploaded_log.txt"
		}

		if _, exists := existingMap[cleanName]; exists {
			if conflictMode == "skip" {
				logger.Log.Infof("[Task Service] Skipping existing file %s for task %s", cleanName, taskID)
				continue
			} else if conflictMode == "overwrite" {
				logger.Log.Infof("[Task Service] Overwriting existing file %s for task %s", cleanName, taskID)
				// 删除旧的日志记录与文件记录
				taskDB.Where("source_file = ?", cleanName).Delete(&model.LogRecord{})
				taskDB.Where("task_id = ? AND file_name = ?", taskID, cleanName).Delete(&model.TaskFile{})
			}
		}

		// 按行解析日志
		lines := strings.Split(strings.ReplaceAll(item.Content, "\r\n", "\n"), "\n")
		var fileRecords []model.LogRecord
		validLineCount := 0

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			validLineCount++

			norm, err := logparser.ParseLine(line)
			if err != nil {
				norm = &model.NormalizedLog{
					RawLog:      line,
					Timestamp:   time.Now(),
					MessageBody: line,
				}
			}
			norm.SourceFile = cleanName

			// 知识库多级匹配
			k, tier, conf := s.matchEngine.Match(norm, taskInfo.DeviceType, "")
			if k != nil && k.ID > 0 {
				norm.KnowledgeID = k.ID
				norm.MatchTier = tier
				norm.MatchConfidence = conf
			} else {
				norm.MatchTier = matcher.TierUnmatch
				norm.MatchConfidence = 0.0
			}

			paramJSON, _ := json.Marshal(norm.Parameters)
			rec := model.LogRecord{
				Timestamp:       norm.Timestamp,
				Hostname:        norm.Hostname,
				Module:          norm.Module,
				Severity:        norm.Severity,
				Brief:           norm.Brief,
				SlotInfo:        norm.SlotInfo,
				SourceFile:      cleanName,
				RawLog:          norm.RawLog,
				MessageBody:     norm.MessageBody,
				ParametersJSON:  string(paramJSON),
				KnowledgeID:     norm.KnowledgeID,
				MatchTier:       norm.MatchTier,
				MatchConfidence: norm.MatchConfidence,
			}

			fileRecords = append(fileRecords, rec)
		}

		// 插入或更新 TaskFile 记录
		taskFile := model.TaskFile{
			TaskID:    taskID,
			FileName:  cleanName,
			FileSize:  item.FileSize,
			LineCount: validLineCount,
			CreatedAt: time.Now(),
		}
		if err := taskDB.Create(&taskFile).Error; err != nil {
			logger.Log.Errorf("create task file record failed: %v", err)
		}

		allNewLogRecords = append(allNewLogRecords, fileRecords...)
	}

	// 批量插入新增日志记录
	if len(allNewLogRecords) > 0 {
		logger.Log.Debugf("[Task Service] Inserting %d new log records for task %s...", len(allNewLogRecords), taskID)
		if err := taskDB.CreateInBatches(&allNewLogRecords, 100).Error; err != nil {
			logger.Log.Errorf("batch insert log records failed: %v", err)
		}
	}

	// 全量重新聚合并执行 RCA 根因拓扑分析
	var fullLogRecords []model.LogRecord
	taskDB.Order("timestamp asc, id asc").Find(&fullLogRecords)

	var normLogsForRCA []*model.NormalizedLog
	matchedCount := 0
	for _, rec := range fullLogRecords {
		if rec.KnowledgeID > 0 {
			matchedCount++
		}
		var params map[string]string
		if rec.ParametersJSON != "" {
			_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
		}
		nl := &model.NormalizedLog{
			ID:              rec.ID,
			RawLog:          rec.RawLog,
			Timestamp:       rec.Timestamp,
			Hostname:        rec.Hostname,
			Module:          rec.Module,
			Severity:        rec.Severity,
			Brief:           rec.Brief,
			SlotInfo:        rec.SlotInfo,
			SourceFile:      rec.SourceFile,
			MessageBody:     rec.MessageBody,
			Parameters:      params,
			KnowledgeID:     rec.KnowledgeID,
			MatchTier:       rec.MatchTier,
			MatchConfidence: rec.MatchConfidence,
		}
		normLogsForRCA = append(normLogsForRCA, nl)
	}

	// 清理旧 RCA 并重新分析
	taskDB.Where("1 = 1").Delete(&model.RCAEvent{})
	var rcaEvents []model.RCAEvent
	if len(normLogsForRCA) > 0 {
		rcaEvents = s.rcaEngine.Analyze(normLogsForRCA, 300)
		if len(rcaEvents) > 0 {
			taskDB.Create(&rcaEvents)
		}
	}

	var currentFileCount int64
	taskDB.Model(&model.TaskFile{}).Count(&currentFileCount)

	now := time.Now()
	taskInfo.FileCount = int(currentFileCount)
	taskInfo.LogCount = len(fullLogRecords)
	taskInfo.MatchedCount = matchedCount
	taskInfo.RcaCount = len(rcaEvents)
	taskInfo.Status = model.TaskStatusCompleted
	taskInfo.FinishTime = &now

	s.globalDB.Save(&taskInfo)
	taskDB.Save(&taskInfo)

	logger.Log.Infof("[Task Service] Task %s updated: %d files, %d total logs, %d matched, %d rca events",
		taskID, taskInfo.FileCount, taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)

	return &taskInfo, nil
}

// GetTaskList 获取所有任务列表
func (s *Service) GetTaskList() ([]model.TaskInfo, error) {
	var tasks []model.TaskInfo
	err := s.globalDB.Order("start_time desc").Find(&tasks).Error
	return tasks, err
}

// GetTaskByID 获取单个任务元信息
func (s *Service) GetTaskByID(taskID string) (*model.TaskInfo, error) {
	var task model.TaskInfo
	err := s.globalDB.First(&task, "task_id = ?", taskID).Error
	return &task, err
}

// QueryTaskLogs 分页及多维度过滤查询任务内日志
func (s *Service) QueryTaskLogs(taskID string, filter model.LogQueryFilter) ([]model.LogRecord, int64, error) {
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, 0, err
	}

	query := taskDB.Model(&model.LogRecord{})

	if filter.Module != "" {
		query = query.Where("UPPER(module) = ?", strings.ToUpper(filter.Module))
	}
	if filter.Severity != nil {
		query = query.Where("severity <= ?", *filter.Severity)
	}
	if filter.Brief != "" {
		query = query.Where("brief LIKE ?", "%"+filter.Brief+"%")
	}
	if filter.Hostname != "" {
		query = query.Where("hostname LIKE ?", "%"+filter.Hostname+"%")
	}
	if filter.SourceFile != "" {
		query = query.Where("source_file = ?", filter.SourceFile)
	}
	if filter.Keyword != "" {
		query = query.Where("raw_log LIKE ? OR message_body LIKE ?", "%"+filter.Keyword+"%", "%"+filter.Keyword+"%")
	}
	if filter.Matched != nil {
		if *filter.Matched {
			query = query.Where("knowledge_id > 0")
		} else {
			query = query.Where("knowledge_id = 0 OR knowledge_id IS NULL")
		}
	}
	if filter.TimeStart != nil {
		query = query.Where("timestamp >= ?", *filter.TimeStart)
	}
	if filter.TimeEnd != nil {
		query = query.Where("timestamp <= ?", *filter.TimeEnd)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 支持导出全量查询 (pageSize < 0)
	if filter.PageSize < 0 {
		var records []model.LogRecord
		err = query.Order("id asc").Find(&records).Error
		return records, total, err
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	} else if pageSize > 10000 {
		pageSize = 10000
	}
	offset := (page - 1) * pageSize

	var records []model.LogRecord
	err = query.Order("id asc").Offset(offset).Limit(pageSize).Find(&records).Error
	return records, total, err
}

// GetTaskRCAEvents 获取任务的 RCA 分析事件
func (s *Service) GetTaskRCAEvents(taskID string) ([]model.RCAEvent, error) {
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var events []model.RCAEvent
	err = taskDB.Order("id asc").Find(&events).Error
	return events, err
}

// ExportTaskHTML 导出任务 HTML 报告
func (s *Service) ExportTaskHTML(taskID string) (string, error) {
	task, err := s.GetTaskByID(taskID)
	if err != nil {
		return "", err
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return "", err
	}

	var records []model.LogRecord
	taskDB.Order("id asc").Find(&records)

	var rcas []model.RCAEvent
	taskDB.Find(&rcas)

	html := GenerateHTMLReport(task, records, rcas)
	return html, nil
}

// DeleteTask 删除任务及物理数据库
func (s *Service) DeleteTask(taskID string) error {
	_ = s.globalDB.Where("task_id = ?", taskID).Delete(&model.TaskInfo{})
	return storage.DeleteTaskDB(s.taskDir, taskID)
}

