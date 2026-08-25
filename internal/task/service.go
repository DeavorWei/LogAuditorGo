package task

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"logauditorgo/internal/logparser"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// LogAuditStages 日志导入与审计分析全流程预设阶段
var LogAuditStages = []progress.StageDef{
	{Key: "RECEIVE", Name: "日志文件预处理"},
	{Key: "PARSE_NORM", Name: "分行解析与参数提取"},
	{Key: "MATCH_KB", Name: "知识库多级智能匹配"},
	{Key: "SAVE_DB", Name: "任务独立数据库持久化"},
	{Key: "RCA_ANALYSIS", Name: "RCA 根因拓扑与传播链分析"},
	{Key: "COMPLETE", Name: "审计分析完成"},
}

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

// ImportLogs 导入/补充导入日志文件，支持全流程阶段进度实时追踪
func (s *Service) ImportLogs(taskID string, items []FileUploadItem, conflictMode string, tracker ...*progress.JobTracker) (*model.TaskInfo, error) {
	var tr *progress.JobTracker
	if len(tracker) > 0 && tracker[0] != nil {
		tr = tracker[0]
	}

	if tr != nil {
		tr.SetStage("RECEIVE", "正在加载并预处理待导入日志文件...")
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		if tr != nil {
			tr.Fail(err, "无法打开任务独立数据库")
		}
		return nil, fmt.Errorf("open task db failed: %w", err)
	}

	var taskInfo model.TaskInfo
	if err := taskDB.First(&taskInfo, "task_id = ?", taskID).Error; err != nil {
		if tr != nil {
			tr.Fail(err, "任务未找到")
		}
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
			if tr != nil {
				tr.Fail(fmt.Errorf("%s", errStr), "日志处理异常中断")
			}
		}
	}()

	// 获取已存在的文件记录
	var existingFileList []model.TaskFile
	taskDB.Find(&existingFileList)
	existingMap := make(map[string]model.TaskFile)
	for _, f := range existingFileList {
		existingMap[f.FileName] = f
	}

	// 预估所有文件的有效行数
	type fileLineBundle struct {
		item      FileUploadItem
		cleanName string
		lines     []string
	}

	var bundles []fileLineBundle
	var totalValidLines int64 = 0

	for _, item := range items {
		cleanName := filepath.Base(strings.TrimSpace(item.FileName))
		if cleanName == "" {
			cleanName = "uploaded_log.txt"
		}

		if _, exists := existingMap[cleanName]; exists {
			if conflictMode == "skip" {
				logger.Log.Infof("[Task Service] Skipping existing file %s for task %s", cleanName, taskID)
				if tr != nil {
					tr.AddLog("warning", "跳过已存在的同名文件: %s", cleanName)
				}
				continue
			} else if conflictMode == "overwrite" {
				logger.Log.Infof("[Task Service] Overwriting existing file %s for task %s", cleanName, taskID)
				if tr != nil {
					tr.AddLog("info", "清理覆盖旧同名文件数据: %s", cleanName)
				}
				taskDB.Where("source_file = ?", cleanName).Delete(&model.LogRecord{})
				taskDB.Where("task_id = ? AND file_name = ?", taskID, cleanName).Delete(&model.TaskFile{})
			}
		}

		rawLines := strings.Split(strings.ReplaceAll(item.Content, "\r\n", "\n"), "\n")
		var validLines []string
		for _, l := range rawLines {
			l = strings.TrimSpace(l)
			if l != "" {
				validLines = append(validLines, l)
			}
		}

		totalValidLines += int64(len(validLines))
		bundles = append(bundles, fileLineBundle{
			item:      item,
			cleanName: cleanName,
			lines:     validLines,
		})
	}

	if tr != nil {
		tr.AddLog("info", "待处理有效日志文件 %d 个，总计 %d 行日志", len(bundles), totalValidLines)
		tr.SetStage("PARSE_NORM", fmt.Sprintf("正在并发分词与标准化解析 %d 行日志...", totalValidLines))
		tr.UpdateProgress(0, totalValidLines, fmt.Sprintf("已解析 0 / %d 行", totalValidLines))
	}

	// 多协程并发流水线设计
	type logParseJob struct {
		index     int
		cleanName string
		line      string
	}

	type logParseResult struct {
		index   int
		record  model.LogRecord
		matched bool
	}

	allNewLogRecords := make([]model.LogRecord, totalValidLines)
	var processedCount int64 = 0
	var matchedCountSoFar int64 = 0

	if totalValidLines > 0 {
		workerNum := runtime.NumCPU()
		if workerNum < 2 {
			workerNum = 2
		} else if workerNum > 32 {
			workerNum = 32
		}

		jobs := make(chan logParseJob, 1024)
		results := make(chan logParseResult, 1024)

		// 结果收集协程
		collectDone := make(chan struct{})
		go func() {
			defer close(collectDone)
			for res := range results {
				allNewLogRecords[res.index] = res.record
				if res.matched {
					atomic.AddInt64(&matchedCountSoFar, 1)
				}
				cur := atomic.AddInt64(&processedCount, 1)
				if tr != nil && (cur%200 == 0 || cur == totalValidLines) {
					curMatched := atomic.LoadInt64(&matchedCountSoFar)
					tr.UpdateProgress(cur, totalValidLines,
						fmt.Sprintf("正在并发解析与匹配: %d / %d 行 (命中知识库 %d 条)", cur, totalValidLines, curMatched))
				}
			}
		}()

		// 启动并发 Worker 协程池
		var wg sync.WaitGroup
		for i := 0; i < workerNum; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						logger.Log.Errorf("Recovered in log parse worker: %v", r)
					}
				}()

				for job := range jobs {
					norm, err := logparser.ParseLine(job.line)
					if err != nil {
						norm = &model.NormalizedLog{
							RawLog:      job.line,
							Timestamp:   time.Now(),
							MessageBody: job.line,
							Module:      "UNKNOWN",
							Brief:       "UNPARSED",
							Severity:    8, // 解析失败的日志等级设为最低级 (8)
						}
					}
					norm.SourceFile = job.cleanName

					// 知识库多级匹配
					k, tier, conf := s.matchEngine.Match(norm, taskInfo.DeviceType, "")
					matched := false
					if k != nil && k.ID > 0 {
						norm.KnowledgeID = k.ID
						norm.MatchTier = tier
						norm.MatchConfidence = conf
						matched = true
					} else {
						norm.KnowledgeID = 0
						norm.MatchTier = matcher.TierUnmatch
						norm.MatchConfidence = 0.0
						// 无法匹配知识库的日志，等级调整为最低级 (8)
						norm.Severity = 8
					}

					// 兜底保障：若 Severity 缺失或不在 1~8 范围内，默认设为最低级 8
					if norm.Severity < 1 || norm.Severity > 8 {
						norm.Severity = 8
					}

					paramJSON, _ := json.Marshal(norm.Parameters)
					rec := model.LogRecord{
						Timestamp:       norm.Timestamp,
						Hostname:        norm.Hostname,
						Module:          norm.Module,
						Severity:        norm.Severity,
						Brief:           norm.Brief,
						SlotInfo:        norm.SlotInfo,
						SourceFile:      job.cleanName,
						RawLog:          norm.RawLog,
						MessageBody:     norm.MessageBody,
						ParametersJSON:  string(paramJSON),
						KnowledgeID:     norm.KnowledgeID,
						MatchTier:       norm.MatchTier,
						MatchConfidence: norm.MatchConfidence,
					}

					results <- logParseResult{
						index:   job.index,
						record:  rec,
						matched: matched,
					}
				}
			}()
		}

		// 分发待解析任务并保存 TaskFile 记录
		lineIdx := 0
		for _, bundle := range bundles {
			taskFile := model.TaskFile{
				TaskID:    taskID,
				FileName:  bundle.cleanName,
				FileSize:  bundle.item.FileSize,
				LineCount: len(bundle.lines),
				CreatedAt: time.Now(),
			}
			if err := taskDB.Create(&taskFile).Error; err != nil {
				logger.Log.Errorf("create task file record failed: %v", err)
			}

			for _, line := range bundle.lines {
				jobs <- logParseJob{
					index:     lineIdx,
					cleanName: bundle.cleanName,
					line:      line,
				}
				lineIdx++
			}
		}

		close(jobs)
		wg.Wait()
		close(results)
		<-collectDone
	}

	if tr != nil {
		tr.SetStage("SAVE_DB", fmt.Sprintf("正在将 %d 条解析日志批量写入任务隔离数据库...", len(allNewLogRecords)))
		tr.AddLog("info", "并发日志分行与匹配完成，开始批量持久化落盘 (%d 条)...", len(allNewLogRecords))
	}

	// 批量插入新增日志记录
	if len(allNewLogRecords) > 0 {
		logger.Log.Debugf("[Task Service] Inserting %d new log records for task %s...", len(allNewLogRecords), taskID)
		if err := taskDB.CreateInBatches(&allNewLogRecords, 500).Error; err != nil {
			logger.Log.Errorf("batch insert log records failed: %v", err)
		}
	}

	if tr != nil {
		tr.SetStage("RCA_ANALYSIS", "正在基于时序关联与故障传播模型执行全量 RCA 根因拓扑分析...")
		tr.AddLog("info", "开始 RCA 根因分析计算...")
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

	if tr != nil {
		tr.AddLog("info", "RCA 拓扑分析就绪: 发现 %d 个根因故障事件", len(rcaEvents))
		tr.SetStage("COMPLETE", "日志审计分析已全部完成")
		tr.Complete(&taskInfo, fmt.Sprintf("分析就绪！共处理 %d 行日志，命中知识库 %d 条，识别出 %d 个 RCA 根因事件",
			taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount))
	}

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
