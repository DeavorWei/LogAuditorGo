package task

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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

var taskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)

// isValidTaskID 校验任务ID格式是否合法，防止路径遍历与注入攻击
func isValidTaskID(taskID string) bool {
	return taskIDRegex.MatchString(taskID)
}

// escapeLikePattern 转义 LIKE 模糊匹配中的特殊通配符
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

var invalidFileNameChars = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+`)

// sanitizeFileNameComponent 过滤文件名中的非法字符与路径穿越符
func sanitizeFileNameComponent(name string) string {
	s := invalidFileNameChars.ReplaceAllString(strings.TrimSpace(name), "_")
	if s == "" {
		return "Device"
	}
	return s
}

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
	Reader   io.Reader
	FilePath string
	TempFile bool // 标识是否为处理完毕后需清理的临时文件
}

// Open 打开数据流 Reader
func (item *FileUploadItem) Open() (io.ReadCloser, error) {
	if item.Reader != nil {
		if rc, ok := item.Reader.(io.ReadCloser); ok {
			return rc, nil
		}
		return io.NopCloser(item.Reader), nil
	}
	if item.FilePath != "" {
		return os.Open(item.FilePath)
	}
	return io.NopCloser(strings.NewReader(item.Content)), nil
}

// Cleanup 清理临时文件（如果有）
func (item *FileUploadItem) Cleanup() {
	if item.TempFile && item.FilePath != "" {
		_ = os.Remove(item.FilePath)
	}
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
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
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
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
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
	return s.ImportLogsWithDevice(taskID, 0, items, conflictMode, tracker...)
}

// ImportLogsToDevice 向指定设备导入日志
func (s *Service) ImportLogsToDevice(taskID string, deviceID uint, items []FileUploadItem, conflictMode string, tracker ...*progress.JobTracker) (*model.TaskInfo, error) {
	return s.ImportLogsWithDevice(taskID, deviceID, items, conflictMode, tracker...)
}

// ImportLogsWithDevice 导入日志文件并支持指定关联设备ID
func (s *Service) ImportLogsWithDevice(taskID string, deviceID uint, items []FileUploadItem, conflictMode string, tracker ...*progress.JobTracker) (*model.TaskInfo, error) {
	defer func() {
		for i := range items {
			items[i].Cleanup()
		}
	}()

	var tr *progress.JobTracker
	if len(tracker) > 0 && tracker[0] != nil {
		tr = tracker[0]
	}

	if !isValidTaskID(taskID) {
		if tr != nil {
			tr.Fail(fmt.Errorf("invalid task id: %s", taskID), "任务ID非法")
		}
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}

	if tr != nil {
		tr.SetStage("RECEIVE", "正在加载并预处理待导入日志文件...")
	}

	if s.matchEngine != nil {
		s.matchEngine.Reload()
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
	if err := s.globalDB.Save(&taskInfo).Error; err != nil {
		logger.Log.Errorf("save task info to global db failed: %v", err)
	}
	if err := taskDB.Save(&taskInfo).Error; err != nil {
		logger.Log.Errorf("save task info to task db failed: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("Panic in task processing: %v", r)
			logger.Log.Errorf("[Task Service] %s", errStr)
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = errStr
			if err := s.globalDB.Save(&taskInfo).Error; err != nil {
				logger.Log.Errorf("save task info to global db failed: %v", err)
			}
			if err := taskDB.Save(&taskInfo).Error; err != nil {
				logger.Log.Errorf("save task info to task db failed: %v", err)
			}
			if tr != nil {
				tr.Fail(fmt.Errorf("%s", errStr), "日志处理异常中断")
			}
		}
	}()

	// 获取已存在的文件记录
	var existingFileList []model.TaskFile
	if err := taskDB.Find(&existingFileList).Error; err != nil {
		logger.Log.Warnf("[Task Service] Find existing task files warning: %v", err)
	}
	existingMap := make(map[string]model.TaskFile)
	for _, f := range existingFileList {
		existingMap[f.FileName] = f
	}

	// 预估所有文件的有效行数
	type fileLineBundle struct {
		item           FileUploadItem
		cleanName      string
		lines          []string
		targetDeviceID uint
	}

	var bundles []fileLineBundle
	var totalValidLines int64 = 0

	for _, item := range items {
		cleanName := filepath.Base(strings.TrimSpace(item.FileName))
		if cleanName == "" {
			cleanName = "uploaded_log.txt"
		}

		rc, err := item.Open()
		if err != nil {
			logger.Log.Warnf("[Task Service] Failed to open upload item %s: %v", item.FileName, err)
			continue
		}

		var validLines []string
		scanner := bufio.NewScanner(rc)
		scanBuf := make([]byte, 64*1024)
		scanner.Buffer(scanBuf, 1024*1024)
		for scanner.Scan() {
			l := strings.TrimSpace(scanner.Text())
			if l != "" {
				validLines = append(validLines, l)
			}
		}
		_ = rc.Close()

		if len(validLines) == 0 {
			continue
		}

		// 1. 自动触发 Hostname 解析：检查前 100 行日志
		detectedHost := ""
		for idx, l := range validLines {
			if idx > 100 {
				break
			}
			if n, err := logparser.ParseLine(l); err == nil && n.Hostname != "" {
				detectedHost = n.Hostname
				break
			}
		}

		targetDevName := detectedHost
		if targetDevName == "" {
			if deviceID > 0 {
				var dev model.Device
				if err := taskDB.First(&dev, deviceID).Error; err == nil && dev.DeviceName != "" {
					targetDevName = dev.DeviceName
				}
			}
			if targetDevName == "" {
				targetDevName = fmt.Sprintf("Device-%s", strings.ToUpper(uuid.New().String()[:4]))
			}
		}

		// 2. 日志名带上设备名，以区分不同设备相同日志文件名称
		prefix := fmt.Sprintf("[%s]_", sanitizeFileNameComponent(targetDevName))
		if !strings.HasPrefix(cleanName, prefix) && !strings.HasPrefix(cleanName, "[") {
			cleanName = prefix + cleanName
		}

		// 3. 同名文件冲突处理 (支持 skip, overwrite, 以及默认 rename)
		if conflictMode == "skip" {
			if _, exists := existingMap[cleanName]; exists {
				logger.Log.Infof("[Task Service] Skipping existing file %s for task %s", cleanName, taskID)
				if tr != nil {
					tr.AddLog("warning", "跳过已存在的同名文件: %s", cleanName)
				}
				continue
			}
		} else if conflictMode == "overwrite" {
			if _, exists := existingMap[cleanName]; exists {
				logger.Log.Infof("[Task Service] Overwriting existing file %s for task %s", cleanName, taskID)
				if tr != nil {
					tr.AddLog("info", "清理覆盖旧同名文件数据: %s", cleanName)
				}
				_ = taskDB.Where("source_file = ?", cleanName).Delete(&model.LogRecord{}).Error
				_ = taskDB.Where("task_id = ? AND file_name = ?", taskID, cleanName).Delete(&model.TaskFile{}).Error
			}
		} else {
			// 默认 rename 模式：同名文件自动追加序号，支持多文件共存
			if _, exists := existingMap[cleanName]; exists {
				ext := filepath.Ext(cleanName)
				base := strings.TrimSuffix(cleanName, ext)
				for seq := 2; ; seq++ {
					candidate := fmt.Sprintf("%s_%d%s", base, seq, ext)
					if _, dup := existingMap[candidate]; !dup {
						cleanName = candidate
						break
					}
				}
			}
		}
		existingMap[cleanName] = model.TaskFile{FileName: cleanName}

		// 4. 自动关联或创建设备实体
		fileDevID := deviceID
		if fileDevID == 0 && targetDevName != "" {
			var dev model.Device
			err := taskDB.Where("task_id = ? AND (device_name = ? OR hostname = ?)", taskID, targetDevName, targetDevName).First(&dev).Error
			if err != nil {
				dev = model.Device{
					TaskID:     taskID,
					DeviceName: targetDevName,
					DeviceType: "Router",
					Hostname:   detectedHost,
					Color:      DeviceDefaultColors[len(existingMap)%len(DeviceDefaultColors)],
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				}
				if err := taskDB.Create(&dev).Error; err == nil {
					fileDevID = dev.ID
				}
			} else {
				fileDevID = dev.ID
			}
		}

		totalValidLines += int64(len(validLines))
		bundles = append(bundles, fileLineBundle{
			item:           item,
			cleanName:      cleanName,
			lines:          validLines,
			targetDeviceID: fileDevID,
		})
	}

	if tr != nil {
		tr.AddLog("info", "待处理有效日志文件 %d 个，总计 %d 行日志", len(bundles), totalValidLines)
		tr.SetStage("PARSE_NORM", fmt.Sprintf("正在并发分词与标准化解析 %d 行日志...", totalValidLines))
		tr.UpdateProgress(0, totalValidLines, fmt.Sprintf("已解析 0 / %d 行", totalValidLines))
	}

	// 多协程并发流水线设计
	type logParseJob struct {
		index          int
		cleanName      string
		line           string
		targetDeviceID uint
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

		jobs := make(chan logParseJob, 2048)
		results := make(chan logParseResult, 2048)

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
				if tr != nil && (cur%500 == 0 || cur == totalValidLines) {
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
						logger.Log.Errorf("Unexpected panic in log parse worker: %v", r)
					}
				}()

				for job := range jobs {
					func() {
						defer func() {
							if r := recover(); r != nil {
								logger.Log.Errorf("Recovered panic while parsing line (job %d): %v", job.index, r)
								fallbackRec := model.LogRecord{
									DeviceID:        job.targetDeviceID,
									Module:          "UNKNOWN",
									Severity:        8,
									Brief:           "PARSE_ERROR",
									SourceFile:      job.cleanName,
									RawLog:          job.line,
									MessageBody:     job.line,
									ParametersJSON:  "{}",
									MatchTier:       matcher.TierUnmatch,
									MatchConfidence: 0.0,
								}
								results <- logParseResult{
									index:   job.index,
									record:  fallbackRec,
									matched: false,
								}
							}
						}()

						norm, err := logparser.ParseLine(job.line)
						if err != nil {
							norm = &model.NormalizedLog{
								RawLog:      job.line,
								MessageBody: job.line,
								Module:      "UNKNOWN",
								Brief:       "UNPARSED",
								Severity:    8, // 解析失败的日志等级设为最低级 (8)
							}
						}
						norm.SourceFile = job.cleanName

						// 知识库多级匹配（纯内存极速检索 + 负缓存）
						var k *model.Knowledge
						var tier string
						var conf float64
						if s.matchEngine != nil {
							k, tier, conf = s.matchEngine.Match(norm, taskInfo.DeviceType, "")
						}
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

						paramJSONStr := "{}"
						if len(norm.Parameters) > 0 {
							if b, err := json.Marshal(norm.Parameters); err == nil {
								paramJSONStr = string(b)
							}
						}

						rec := model.LogRecord{
							DeviceID:        job.targetDeviceID,
							Timestamp:       norm.Timestamp,
							Hostname:        norm.Hostname,
							Module:          norm.Module,
							Severity:        norm.Severity,
							Brief:           norm.Brief,
							SlotInfo:        norm.SlotInfo,
							SourceFile:      job.cleanName,
							RawLog:          norm.RawLog,
							MessageBody:     norm.MessageBody,
							ParametersJSON:  paramJSONStr,
							KnowledgeID:     norm.KnowledgeID,
							MatchTier:       norm.MatchTier,
							MatchConfidence: norm.MatchConfidence,
						}

						results <- logParseResult{
							index:   job.index,
							record:  rec,
							matched: matched,
						}
					}()
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
					index:          lineIdx,
					cleanName:      bundle.cleanName,
					line:           line,
					targetDeviceID: bundle.targetDeviceID,
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

	// 事务级批量插入新增日志记录
	if len(allNewLogRecords) > 0 {
		logger.Log.Debugf("[Task Service] Inserting %d new log records for task %s...", len(allNewLogRecords), taskID)
		err := taskDB.Transaction(func(tx *gorm.DB) error {
			return tx.CreateInBatches(&allNewLogRecords, 1000).Error
		})
		if err != nil {
			logger.Log.Errorf("batch insert log records failed: %v", err)
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = fmt.Sprintf("batch insert log records failed: %v", err)
			if saveErr := s.globalDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to global db failed: %v", saveErr)
			}
			if saveErr := taskDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to task db failed: %v", saveErr)
			}
			if tr != nil {
				tr.Fail(err, "批量写入日志数据失败")
			}
			return nil, fmt.Errorf("batch insert log records failed: %w", err)
		}
	}

	if tr != nil {
		tr.SetStage("RCA_ANALYSIS", "正在基于时序关联与故障传播模型执行全量 RCA 根因拓扑分析...")
		tr.AddLog("info", "开始 RCA 根因分析计算...")
	}

	// 全量重新聚合并执行 RCA 根因拓扑分析（基于 GORM 游标流式读取，避免百万级日志全量切片内存激增）
	var normLogsForRCA []*model.NormalizedLog
	matchedCount := 0
	totalLogCount := 0

	rows, err := taskDB.Model(&model.LogRecord{}).Order("timestamp asc, id asc").Rows()
	if err != nil {
		logger.Log.Errorf("[Task Service] Failed to query log records for RCA: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var rec model.LogRecord
			if err := taskDB.ScanRows(rows, &rec); err != nil {
				logger.Log.Warnf("[Task Service] Scan row for RCA failed: %v", err)
				continue
			}
			totalLogCount++
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
	}

	// 清理旧 RCA 并重新分析
	if err := taskDB.Where("1 = 1").Delete(&model.RCAEvent{}).Error; err != nil {
		logger.Log.Warnf("[Task Service] Delete old RCA events failed: %v", err)
	}
	var rcaEvents []model.RCAEvent
	if len(normLogsForRCA) > 0 {
		rcaEvents = s.rcaEngine.Analyze(normLogsForRCA, 300)
		if len(rcaEvents) > 0 {
			if err := taskDB.Create(&rcaEvents).Error; err != nil {
				logger.Log.Errorf("[Task Service] Create RCA events failed: %v", err)
			}
		}
	}

	// 若指定了具体设备，同步刷新该设备在任务库中的日志与匹配条数
	if deviceID > 0 {
		var devLogs, devMatched int64
		taskDB.Model(&model.LogRecord{}).Where("device_id = ?", deviceID).Count(&devLogs)
		taskDB.Model(&model.LogRecord{}).Where("device_id = ? AND knowledge_id > 0", deviceID).Count(&devMatched)
		taskDB.Model(&model.Device{}).Where("id = ?", deviceID).Updates(map[string]interface{}{
			"log_count":     int(devLogs),
			"matched_count": int(devMatched),
			"updated_at":    time.Now(),
		})
	}

	// 自动查漏补缺按 Hostname 纳管设备并绑定
	_, _ = s.AutoAssignDevices(taskID)
	s.syncTaskDeviceCount(taskID, taskDB)

	var currentFileCount int64
	if err := taskDB.Model(&model.TaskFile{}).Count(&currentFileCount).Error; err != nil {
		logger.Log.Warnf("[Task Service] Count task files warning: %v", err)
	}

	var currentDeviceCount int64
	if err := taskDB.Model(&model.Device{}).Count(&currentDeviceCount).Error; err != nil {
		logger.Log.Warnf("[Task Service] Count task devices warning: %v", err)
	}

	now := time.Now()
	taskInfo.FileCount = int(currentFileCount)
	taskInfo.DeviceCount = int(currentDeviceCount)
	taskInfo.LogCount = totalLogCount
	taskInfo.MatchedCount = matchedCount
	taskInfo.RcaCount = len(rcaEvents)
	taskInfo.Status = model.TaskStatusCompleted
	taskInfo.FinishTime = &now

	if err := s.globalDB.Save(&taskInfo).Error; err != nil {
		logger.Log.Errorf("save task info to global db failed: %v", err)
	}
	if err := taskDB.Save(&taskInfo).Error; err != nil {
		logger.Log.Errorf("save task info to task db failed: %v", err)
	}

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
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	var task model.TaskInfo
	err := s.globalDB.First(&task, "task_id = ?", taskID).Error
	return &task, err
}

// QueryTaskLogs 分页及多维度过滤查询任务内日志
func (s *Service) QueryTaskLogs(taskID string, filter model.LogQueryFilter) ([]model.LogRecord, int64, error) {
	if !isValidTaskID(taskID) {
		return nil, 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, 0, err
	}

	query := taskDB.Model(&model.LogRecord{})

	if filter.DeviceID != nil {
		query = query.Where("device_id = ?", *filter.DeviceID)
	}
	if filter.Module != "" {
		query = query.Where("UPPER(module) = ?", strings.ToUpper(filter.Module))
	}
	if filter.Severity != nil {
		query = query.Where("severity <= ?", *filter.Severity)
	}
	if filter.Brief != "" {
		query = query.Where("brief LIKE ? ESCAPE '\\'", "%"+escapeLikePattern(filter.Brief)+"%")
	}
	if filter.Hostname != "" {
		query = query.Where("hostname LIKE ? ESCAPE '\\'", "%"+escapeLikePattern(filter.Hostname)+"%")
	}
	if filter.SourceFile != "" {
		escaped := escapeLikePattern(filter.SourceFile)
		query = query.Where("(source_file = ? OR source_file LIKE ? ESCAPE '\\')", filter.SourceFile, "%]_"+escaped)
	}
	if filter.Keyword != "" {
		escaped := escapeLikePattern(filter.Keyword)
		query = query.Where("(raw_log LIKE ? ESCAPE '\\' OR message_body LIKE ? ESCAPE '\\')", "%"+escaped+"%", "%"+escaped+"%")
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
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var events []model.RCAEvent
	err = taskDB.Order("id asc").Find(&events).Error
	return events, err
}

// GetEnrichedRCAEvents 获取包含完整根因与级联时序日志元数据的富化 RCA 事件列表
func (s *Service) GetEnrichedRCAEvents(taskID string) ([]model.EnrichedRCAEvent, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var events []model.RCAEvent
	if err := taskDB.Order("id asc").Find(&events).Error; err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return []model.EnrichedRCAEvent{}, nil
	}

	// 收集所有关联的 LogID 并批量加载 LogRecord
	logIDSet := make(map[uint]bool)
	for _, ev := range events {
		if ev.RootLogID > 0 {
			logIDSet[ev.RootLogID] = true
		}
		var impactList []model.ImpactEvent
		if ev.ImpactEventsJSON != "" {
			_ = json.Unmarshal([]byte(ev.ImpactEventsJSON), &impactList)
			for _, ie := range impactList {
				if ie.LogID > 0 {
					logIDSet[ie.LogID] = true
				}
				if ie.FromLogID > 0 {
					logIDSet[ie.FromLogID] = true
				}
			}
		}
	}

	var allLogIDs []uint
	for id := range logIDSet {
		allLogIDs = append(allLogIDs, id)
	}

	logRecordMap := make(map[uint]model.LogRecord)
	if len(allLogIDs) > 0 {
		var records []model.LogRecord
		taskDB.Where("id IN ?", allLogIDs).Find(&records)
		for _, r := range records {
			logRecordMap[r.ID] = r
		}
	}

	// 批量加载设备映射
	var devList []model.Device
	taskDB.Find(&devList)
	devMap := make(map[uint]model.Device)
	for _, d := range devList {
		devMap[d.ID] = d
	}

	enrichedList := make([]model.EnrichedRCAEvent, len(events))
	for i, ev := range events {
		enriched := model.EnrichedRCAEvent{
			RCAEvent: ev,
		}

		modMap := make(map[string]bool)
		devNameMap := make(map[string]bool)

		if ev.RootModule != "" {
			modMap[ev.RootModule] = true
		}

		// 装配根因日志信息
		if rootRec, ok := logRecordMap[ev.RootLogID]; ok {
			recCopy := rootRec
			enriched.RootLog = &recCopy
			enriched.RootHostname = rootRec.Hostname

			if rootRec.ParametersJSON != "" && rootRec.ParametersJSON != "{}" {
				var p map[string]string
				if err := json.Unmarshal([]byte(rootRec.ParametersJSON), &p); err == nil {
					enriched.RootParameters = p
				}
			}

			if d, exists := devMap[rootRec.DeviceID]; exists {
				enriched.RootDeviceName = d.DeviceName
				enriched.RootDeviceColor = d.Color
			} else if rootRec.Hostname != "" {
				enriched.RootDeviceName = rootRec.Hostname
				enriched.RootDeviceColor = "#3B82F6"
			} else {
				enriched.RootDeviceName = "未指定设备"
				enriched.RootDeviceColor = "#64748B"
			}
			devNameMap[enriched.RootDeviceName] = true
		}

		// 装配衍生事件明细
		var impactList []model.ImpactEvent
		if ev.ImpactEventsJSON != "" {
			_ = json.Unmarshal([]byte(ev.ImpactEventsJSON), &impactList)
		}

		details := make([]model.ImpactLogDetail, len(impactList))
		for j, ie := range impactList {
			detail := model.ImpactLogDetail{
				ImpactEvent: ie,
			}
			if ie.Module != "" {
				modMap[ie.Module] = true
			}

			if rec, ok := logRecordMap[ie.LogID]; ok {
				detail.Hostname = rec.Hostname
				detail.DeviceID = rec.DeviceID
				detail.Severity = rec.Severity
				detail.RawLog = rec.RawLog
				detail.SourceFile = rec.SourceFile
				detail.KnowledgeID = rec.KnowledgeID
				detail.MatchTier = rec.MatchTier
				detail.MatchConfidence = rec.MatchConfidence

				if rec.ParametersJSON != "" && rec.ParametersJSON != "{}" {
					var p map[string]string
					if err := json.Unmarshal([]byte(rec.ParametersJSON), &p); err == nil {
						detail.Parameters = p
					}
				}

				if d, exists := devMap[rec.DeviceID]; exists {
					detail.DeviceName = d.DeviceName
					detail.DeviceColor = d.Color
				} else if rec.Hostname != "" {
					detail.DeviceName = rec.Hostname
					detail.DeviceColor = "#3B82F6"
				} else {
					detail.DeviceName = "未指定设备"
					detail.DeviceColor = "#64748B"
				}
				devNameMap[detail.DeviceName] = true
			}
			details[j] = detail
		}

		enriched.ImpactDetails = details
		enriched.CorrelatedCount = len(details)

		var mods []string
		for m := range modMap {
			mods = append(mods, m)
		}
		sort.Strings(mods)
		enriched.ModulesInvolved = mods

		var devs []string
		for d := range devNameMap {
			devs = append(devs, d)
		}
		sort.Strings(devs)
		enriched.DevicesInvolved = devs

		enrichedList[i] = enriched
	}

	return enrichedList, nil
}

// ExportTaskHTML 导出任务 HTML 报告
func (s *Service) ExportTaskHTML(taskID string) (string, error) {
	if !isValidTaskID(taskID) {
		return "", fmt.Errorf("invalid task id: %s", taskID)
	}
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
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	if err := s.globalDB.Where("task_id = ?", taskID).Delete(&model.TaskInfo{}).Error; err != nil {
		logger.Log.Errorf("delete task from global db failed: %v", err)
		return fmt.Errorf("delete task from global db failed: %w", err)
	}
	if err := storage.DeleteTaskDB(s.taskDir, taskID); err != nil {
		logger.Log.Errorf("delete task db failed: %v", err)
		return fmt.Errorf("delete task db failed: %w", err)
	}
	return nil
}

var DeviceDefaultColors = []string{
	"#3B82F6", // Blue
	"#10B981", // Emerald
	"#F59E0B", // Amber
	"#EF4444", // Red
	"#8B5CF6", // Purple
	"#EC4899", // Pink
	"#14B8A6", // Teal
	"#F97316", // Orange
	"#6366F1", // Indigo
	"#84CC16", // Lime
}

// CreateDevice 在任务中创建新设备
func (s *Service) CreateDevice(taskID string, device *model.Device) (*model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	if device == nil {
		return nil, fmt.Errorf("nil device")
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	device.TaskID = taskID
	if device.DeviceName == "" {
		device.DeviceName = fmt.Sprintf("Device-%d", time.Now().Unix()%10000)
	}
	if device.DeviceType == "" {
		device.DeviceType = "Router"
	}
	if device.Color == "" {
		var count int64
		taskDB.Model(&model.Device{}).Count(&count)
		device.Color = DeviceDefaultColors[int(count)%len(DeviceDefaultColors)]
	}
	device.CreatedAt = time.Now()
	device.UpdatedAt = time.Now()

	if err := taskDB.Create(device).Error; err != nil {
		return nil, fmt.Errorf("create device in task db failed: %w", err)
	}

	s.syncTaskDeviceCount(taskID, taskDB)
	logger.Log.Infof("[Task Service] Created device '%s' (ID: %d, Type: %s) for task %s", device.DeviceName, device.ID, device.DeviceType, taskID)
	return device, nil
}

func (s *Service) syncTaskDeviceCount(taskID string, taskDB *gorm.DB) {
	var count int64
	taskDB.Model(&model.Device{}).Count(&count)
	s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Update("device_count", int(count))
	taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Update("device_count", int(count))
}

// ListDevices 获取指定任务下的所有设备列表并实时汇总其日志数与匹配数
func (s *Service) ListDevices(taskID string) ([]model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var devices []model.Device
	if err := taskDB.Order("id asc").Find(&devices).Error; err != nil {
		return nil, err
	}

	for i := range devices {
		var logCount, matchedCount int64
		taskDB.Model(&model.LogRecord{}).Where("device_id = ?", devices[i].ID).Count(&logCount)
		taskDB.Model(&model.LogRecord{}).Where("device_id = ? AND knowledge_id > 0", devices[i].ID).Count(&matchedCount)
		devices[i].LogCount = int(logCount)
		devices[i].MatchedCount = int(matchedCount)
	}

	return devices, nil
}

// GetDevice 获取单个设备信息
func (s *Service) GetDevice(taskID string, deviceID uint) (*model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var device model.Device
	if err := taskDB.First(&device, "id = ?", deviceID).Error; err != nil {
		return nil, err
	}

	var logCount, matchedCount int64
	taskDB.Model(&model.LogRecord{}).Where("device_id = ?", device.ID).Count(&logCount)
	taskDB.Model(&model.LogRecord{}).Where("device_id = ? AND knowledge_id > 0", device.ID).Count(&matchedCount)
	device.LogCount = int(logCount)
	device.MatchedCount = int(matchedCount)

	return &device, nil
}

// UpdateDevice 更新设备属性
func (s *Service) UpdateDevice(taskID string, deviceID uint, updates map[string]interface{}) (*model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	updates["updated_at"] = time.Now()
	if err := taskDB.Model(&model.Device{}).Where("id = ?", deviceID).Updates(updates).Error; err != nil {
		return nil, err
	}

	return s.GetDevice(taskID, deviceID)
}

// DeleteDevice 删除设备并将关联日志解除绑定 (device_id=0)
func (s *Service) DeleteDevice(taskID string, deviceID uint) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}

	if err := taskDB.Delete(&model.Device{}, "id = ?", deviceID).Error; err != nil {
		return err
	}

	if err := taskDB.Model(&model.LogRecord{}).Where("device_id = ?", deviceID).Update("device_id", 0).Error; err != nil {
		logger.Log.Warnf("reset log records device_id failed: %v", err)
	}

	s.syncTaskDeviceCount(taskID, taskDB)
	return nil
}

// AutoAssignDevices 根据日志中提取的 Hostname 自动识别并创建设备，自动将日志按 Hostname 归属绑定
func (s *Service) AutoAssignDevices(taskID string) ([]model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var hostnames []string
	if err := taskDB.Model(&model.LogRecord{}).
		Where("hostname != '' AND hostname IS NOT NULL").
		Distinct("hostname").
		Pluck("hostname", &hostnames).Error; err != nil {
		return nil, err
	}

	if len(hostnames) == 0 {
		return s.ListDevices(taskID)
	}

	for i, h := range hostnames {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		var dev model.Device
		err := taskDB.Where("task_id = ? AND (hostname = ? OR device_name = ?)", taskID, h, h).First(&dev).Error
		if err != nil {
			dev = model.Device{
				TaskID:     taskID,
				DeviceName: h,
				DeviceType: "Router",
				Hostname:   h,
				Color:      DeviceDefaultColors[i%len(DeviceDefaultColors)],
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}
			if err := taskDB.Create(&dev).Error; err != nil {
				logger.Log.Errorf("create auto device %s failed: %v", h, err)
				continue
			}
		}

		taskDB.Model(&model.LogRecord{}).
			Where("hostname = ? AND (device_id = 0 OR device_id IS NULL)", h).
			Update("device_id", dev.ID)
	}

	s.syncTaskDeviceCount(taskID, taskDB)
	return s.ListDevices(taskID)
}

// GetDistinctModules 获取任务日志中实际出现的所有唯一模块名称 (大写排序)
func (s *Service) GetDistinctModules(taskID string) ([]string, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var modules []string
	err = taskDB.Model(&model.LogRecord{}).
		Distinct("UPPER(module)").
		Where("module IS NOT NULL AND module != ''").
		Order("UPPER(module) asc").
		Pluck("UPPER(module)", &modules).Error
	if err != nil {
		return nil, err
	}
	return modules, nil
}

// QueryMultiDeviceLogs 多设备联合日志查询与时间线构建
func (s *Service) QueryMultiDeviceLogs(taskID string, filter model.MultiDeviceLogFilter) ([]model.DeviceTimelineEvent, int64, error) {
	if !isValidTaskID(taskID) {
		return nil, 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, 0, err
	}

	var devList []model.Device
	taskDB.Find(&devList)
	devMap := make(map[uint]model.Device)
	for _, d := range devList {
		devMap[d.ID] = d
	}

	query := taskDB.Model(&model.LogRecord{})

	if len(filter.DeviceIDs) > 0 {
		query = query.Where("device_id IN ?", filter.DeviceIDs)
	}
	if len(filter.Modules) > 0 {
		var upperMods []string
		for _, m := range filter.Modules {
			if trimmed := strings.TrimSpace(m); trimmed != "" {
				upperMods = append(upperMods, strings.ToUpper(trimmed))
			}
		}
		if len(upperMods) > 0 {
			query = query.Where("UPPER(module) IN ?", upperMods)
		}
	}
	if len(filter.Briefs) > 0 {
		var upperBriefs []string
		for _, b := range filter.Briefs {
			if trimmed := strings.TrimSpace(b); trimmed != "" {
				upperBriefs = append(upperBriefs, strings.ToUpper(trimmed))
			}
		}
		if len(upperBriefs) > 0 {
			query = query.Where("UPPER(brief) IN ?", upperBriefs)
		}
	}
	if filter.Severity != nil {
		query = query.Where("severity <= ?", *filter.Severity)
	}
	if filter.Keyword != "" {
		escaped := escapeLikePattern(filter.Keyword)
		query = query.Where("(raw_log LIKE ? ESCAPE '\\' OR message_body LIKE ? ESCAPE '\\' OR brief LIKE ? ESCAPE '\\')", "%"+escaped+"%", "%"+escaped+"%", "%"+escaped+"%")
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

	orderClause := "timestamp asc, id asc"
	if !filter.AscOrder {
		orderClause = "timestamp desc, id desc"
	}

	var records []model.LogRecord
	if filter.PageSize < 0 {
		err = query.Order(orderClause).Find(&records).Error
	} else {
		page := filter.Page
		if page <= 0 {
			page = 1
		}
		pageSize := filter.PageSize
		if pageSize <= 0 {
			pageSize = 100
		} else if pageSize > 10000 {
			pageSize = 10000
		}
		offset := (page - 1) * pageSize
		err = query.Order(orderClause).Offset(offset).Limit(pageSize).Find(&records).Error
	}

	if err != nil {
		return nil, 0, err
	}

	events := make([]model.DeviceTimelineEvent, len(records))
	for i, r := range records {
		var params map[string]string
		if r.ParametersJSON != "" && r.ParametersJSON != "{}" {
			_ = json.Unmarshal([]byte(r.ParametersJSON), &params)
		}

		devName := "未指定设备"
		devColor := "#64748B"
		if d, ok := devMap[r.DeviceID]; ok {
			devName = d.DeviceName
			if d.Color != "" {
				devColor = d.Color
			}
		} else if r.Hostname != "" {
			devName = r.Hostname
		}

		events[i] = model.DeviceTimelineEvent{
			LogID:           r.ID,
			Timestamp:       r.Timestamp,
			DeviceID:        r.DeviceID,
			DeviceName:      devName,
			DeviceColor:     devColor,
			Hostname:        r.Hostname,
			Module:          r.Module,
			Brief:           r.Brief,
			Severity:        r.Severity,
			RawLog:          r.RawLog,
			MessageBody:     r.MessageBody,
			SourceFile:      r.SourceFile,
			KnowledgeID:     r.KnowledgeID,
			MatchTier:       r.MatchTier,
			MatchConfidence: r.MatchConfidence,
			Parameters:      params,
		}
	}

	return events, total, nil
}

// GetDeviceTimeline 获取多设备联合时间线事件
func (s *Service) GetDeviceTimeline(taskID string, filter model.MultiDeviceLogFilter) ([]model.DeviceTimelineEvent, error) {
	if filter.PageSize == 0 {
		filter.PageSize = 500
	}
	filter.AscOrder = true
	events, _, err := s.QueryMultiDeviceLogs(taskID, filter)
	return events, err
}

// GetMultiDeviceReport 生成多设备对比统计与推断诊断报告
func (s *Service) GetMultiDeviceReport(taskID string, deviceIDs []uint) (*model.MultiDeviceReport, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskInfo, err := s.GetTaskByID(taskID)
	if err != nil {
		return nil, err
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}

	var devices []model.Device
	if len(deviceIDs) > 0 {
		taskDB.Where("id IN ?", deviceIDs).Order("id asc").Find(&devices)
	} else {
		taskDB.Order("id asc").Find(&devices)
	}

	var devStatsList []model.DeviceStats
	totalLogs := 0
	totalMatched := 0

	for _, dev := range devices {
		var logCnt, matchCnt int64
		taskDB.Model(&model.LogRecord{}).Where("device_id = ?", dev.ID).Count(&logCnt)
		taskDB.Model(&model.LogRecord{}).Where("device_id = ? AND knowledge_id > 0", dev.ID).Count(&matchCnt)

		var topMods []model.ModuleCount
		taskDB.Model(&model.LogRecord{}).
			Select("module, count(*) as count").
			Where("device_id = ?", dev.ID).
			Group("module").
			Order("count desc").
			Limit(5).
			Scan(&topMods)

		type SevRow struct {
			Severity int
			Count    int
		}
		var sevRows []SevRow
		taskDB.Model(&model.LogRecord{}).
			Select("severity, count(*) as count").
			Where("device_id = ?", dev.ID).
			Group("severity").
			Scan(&sevRows)
		sevDist := make(map[int]int)
		for _, sr := range sevRows {
			sevDist[sr.Severity] = sr.Count
		}

		type TimeRange struct {
			MinTime *time.Time
			MaxTime *time.Time
		}
		var tr TimeRange
		taskDB.Model(&model.LogRecord{}).
			Select("min(timestamp) as min_time, max(timestamp) as max_time").
			Where("device_id = ?", dev.ID).
			Scan(&tr)

		devCopy := dev
		devCopy.LogCount = int(logCnt)
		devCopy.MatchedCount = int(matchCnt)

		devStatsList = append(devStatsList, model.DeviceStats{
			Device:       devCopy,
			LogCount:     int(logCnt),
			MatchedCount: int(matchCnt),
			TopModules:   topMods,
			SeverityDist: sevDist,
			FirstSeen:    tr.MinTime,
			LastSeen:     tr.MaxTime,
		})

		totalLogs += int(logCnt)
		totalMatched += int(matchCnt)
	}

	selectedDevIDs := make([]uint, len(devices))
	for i, d := range devices {
		selectedDevIDs[i] = d.ID
	}
	timelineFilter := model.MultiDeviceLogFilter{
		DeviceIDs: selectedDevIDs,
		PageSize:  500,
		AscOrder:  true,
	}
	timelineEvents, _, _ := s.QueryMultiDeviceLogs(taskID, timelineFilter)

	eventDevMap := make(map[string]map[uint]bool)
	for _, ev := range timelineEvents {
		if ev.Module == "" || ev.Brief == "" || ev.DeviceID == 0 {
			continue
		}
		key := fmt.Sprintf("%s/%s", strings.ToUpper(ev.Module), ev.Brief)
		if eventDevMap[key] == nil {
			eventDevMap[key] = make(map[uint]bool)
		}
		eventDevMap[key][ev.DeviceID] = true
	}

	var commonEvents []string
	for k, devSet := range eventDevMap {
		if len(devSet) >= 2 || (len(devices) == 1 && len(devSet) >= 1) {
			commonEvents = append(commonEvents, fmt.Sprintf("%s (涉及 %d 台设备)", k, len(devSet)))
		}
	}
	sort.Strings(commonEvents)

	clusters := buildCorrelatedClusters(timelineEvents, 60*time.Second)
	conclusion := generateMultiDeviceConclusion(devStatsList, timelineEvents, clusters, commonEvents)

	report := &model.MultiDeviceReport{
		TaskInfo:     taskInfo,
		Devices:      devStatsList,
		TotalLogs:    totalLogs,
		TotalMatched: totalMatched,
		CommonEvents: commonEvents,
		Clusters:     clusters,
		Timeline:     timelineEvents,
		Conclusion:   conclusion,
		ExportTime:   time.Now().Format("2006-01-02 15:04:05"),
	}

	return report, nil
}

// buildCorrelatedClusters 基于时间窗口将多设备关联事件聚合为时序簇
func buildCorrelatedClusters(events []model.DeviceTimelineEvent, window time.Duration) []model.CorrelatedTimelineCluster {
	if len(events) == 0 {
		return nil
	}

	var clusters []model.CorrelatedTimelineCluster
	var curEvents []model.DeviceTimelineEvent
	var curStartTime time.Time

	for _, ev := range events {
		if curStartTime.IsZero() {
			curStartTime = ev.Timestamp
			curEvents = []model.DeviceTimelineEvent{ev}
			continue
		}

		if ev.Timestamp.Sub(curStartTime) <= window {
			curEvents = append(curEvents, ev)
		} else {
			if c := evaluateCluster(curEvents); c != nil {
				clusters = append(clusters, *c)
			}
			curStartTime = ev.Timestamp
			curEvents = []model.DeviceTimelineEvent{ev}
		}
	}

	if len(curEvents) > 0 {
		if c := evaluateCluster(curEvents); c != nil {
			clusters = append(clusters, *c)
		}
	}

	return clusters
}

func evaluateCluster(events []model.DeviceTimelineEvent) *model.CorrelatedTimelineCluster {
	if len(events) == 0 {
		return nil
	}
	devMap := make(map[string]bool)
	var mods []string
	modMap := make(map[string]bool)

	for _, ev := range events {
		devMap[ev.DeviceName] = true
		if !modMap[ev.Module] {
			modMap[ev.Module] = true
			mods = append(mods, ev.Module)
		}
	}

	if len(devMap) >= 2 || len(events) >= 3 {
		var devList []string
		for d := range devMap {
			devList = append(devList, d)
		}
		sort.Strings(devList)

		first := events[0]
		last := events[len(events)-1]
		summary := fmt.Sprintf("[%s ~ %s] 涉及设备 %s，触发 %d 条事件 (包含模块: %s)",
			first.Timestamp.Format("15:04:05"), last.Timestamp.Format("15:04:05"),
			strings.Join(devList, ", "), len(events), strings.Join(mods, "/"))

		return &model.CorrelatedTimelineCluster{
			StartTime: first.Timestamp,
			EndTime:   last.Timestamp,
			Module:    strings.Join(mods, ","),
			Devices:   devList,
			Events:    events,
			Summary:   summary,
		}
	}
	return nil
}

// generateMultiDeviceConclusion 自动生成多设备协同与时间线分析结论
func generateMultiDeviceConclusion(devices []model.DeviceStats, timeline []model.DeviceTimelineEvent, clusters []model.CorrelatedTimelineCluster, commonEvents []string) string {
	if len(devices) == 0 {
		return "当前任务尚未配置设备，建议添加设备或执行按 Hostname 自动识别以进行多设备协同分析。"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("【多设备协同审计综述】本次分析覆盖 %d 台网络设备，", len(devices)))

	totalCrit := 0
	for _, d := range devices {
		for sev, count := range d.SeverityDist {
			if sev <= 3 {
				totalCrit += count
			}
		}
	}

	if len(timeline) > 0 {
		start := timeline[0].Timestamp.Format("2006-01-02 15:04:05")
		end := timeline[len(timeline)-1].Timestamp.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("时间跨度为 %s 至 %s，共汇聚分析 %d 条时序日志，其中严重告警（级别≤3）共 %d 条。\n\n",
			start, end, len(timeline), totalCrit))
	} else {
		sb.WriteString("暂无时间线日志记录。\n\n")
	}

	if len(commonEvents) > 0 {
		sb.WriteString("【跨设备共性事件】检测到以下在多台设备间协同或相继发生的事件：\n")
		for _, ce := range commonEvents {
			sb.WriteString(fmt.Sprintf(" • %s\n", ce))
		}
		sb.WriteString("\n")
	}

	if len(clusters) > 0 {
		sb.WriteString("【故障传播与时间窗口推断】\n")
		for i, cl := range clusters {
			if i >= 3 {
				sb.WriteString(fmt.Sprintf(" • 另有 %d 个时序关联事件簇...\n", len(clusters)-3))
				break
			}
			if len(cl.Events) > 0 {
				firstEv := cl.Events[0]
				sb.WriteString(fmt.Sprintf(" • [%s] 由设备「%s」率先上报 %s/%s 事件，随后在时间窗口内协同影响设备 (%s)。\n",
					firstEv.Timestamp.Format("15:04:05"), firstEv.DeviceName, firstEv.Module, firstEv.Brief, strings.Join(cl.Devices, ", ")))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("【专家排查建议】\n")
	hasOSPF := false
	hasBGP := false
	hasLink := false
	for _, ev := range timeline {
		m := strings.ToUpper(ev.Module)
		if strings.Contains(m, "OSPF") {
			hasOSPF = true
		}
		if strings.Contains(m, "BGP") {
			hasBGP = true
		}
		if strings.Contains(m, "IFNET") || strings.Contains(m, "PORT") || strings.Contains(m, "ETH") {
			hasLink = true
		}
	}

	if hasOSPF {
		sb.WriteString(" 1. OSPF 邻居震荡排查：请重点检查对端路由器接口 MTU 一致性、Hello/Dead Timer 配置、链路丢包以及 BFD 联动保活状态。\n")
	}
	if hasBGP {
		sb.WriteString(" 2. BGP 状态排查：请检查 TCP 179 端口可达性、Hold Timer 超时原因、以及对等体 Keepalive 报文交互是否被 ACL 或 CPU 防攻击策略丢弃。\n")
	}
	if hasLink {
		sb.WriteString(" 3. 物理链路排查：检查对端光模块收发光功率（optical-power）、接口 CRC 错包统计及物理光纤链路质量。\n")
	}
	if !hasOSPF && !hasBGP && !hasLink {
		sb.WriteString(" 1. 请依据时间线率先产生告警的设备与模块，结合华为官方知识库排查指引依次进行处置。\n")
	}

	return sb.String()
}

// ExportMultiDeviceHTML 导出多设备对比 HTML 报告
func (s *Service) ExportMultiDeviceHTML(taskID string, deviceIDs []uint) (string, error) {
	report, err := s.GetMultiDeviceReport(taskID, deviceIDs)
	if err != nil {
		return "", err
	}
	return GenerateMultiDeviceHTMLReport(report), nil
}

// ReanalyzeTask 基于任务维度对已持久化的日志记录重新执行知识库匹配、设备指标同步与 RCA 根因拓扑分析
func (s *Service) ReanalyzeTask(taskID string, tr *progress.JobTracker) (*model.TaskInfo, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to open task database: %w", err)
	}

	var taskInfo model.TaskInfo
	if err := s.globalDB.Where("task_id = ?", taskID).First(&taskInfo).Error; err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	taskInfo.Status = model.TaskStatusProcessing
	taskInfo.ErrorMessage = ""
	_ = s.globalDB.Save(&taskInfo)
	_ = taskDB.Save(&taskInfo)

	var totalLogCount int64
	if err := taskDB.Model(&model.LogRecord{}).Count(&totalLogCount).Error; err != nil {
		if tr != nil {
			tr.Fail(err, "统计任务日志失败")
		}
		return nil, err
	}

	if tr != nil {
		tr.AddLog("info", "启动任务【%s】重新分析，共 %d 条日志待匹配", taskInfo.TaskName, totalLogCount)
		tr.SetStage("MATCH_NORM", fmt.Sprintf("正在对 %d 条日志重新执行知识库匹配...", totalLogCount))
		tr.UpdateProgress(0, totalLogCount, "准备执行知识库匹配...")
	}

	// 阶段一：流式批量重新匹配 LogRecord
	matchedCount := 0
	const batchSize = 1000
	var lastID uint = 0
	var processedCount int64 = 0

	for {
		var records []model.LogRecord
		if err := taskDB.Where("id > ?", lastID).Order("id asc").Limit(batchSize).Find(&records).Error; err != nil {
			logger.Log.Errorf("[Task Service] Query batch log records failed: %v", err)
			break
		}
		if len(records) == 0 {
			break
		}

		err := taskDB.Transaction(func(tx *gorm.DB) error {
			for i := range records {
				rec := &records[i]
				lastID = rec.ID

				var params map[string]string
				if rec.ParametersJSON != "" {
					_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
				}

				norm := &model.NormalizedLog{
					ID:          rec.ID,
					RawLog:      rec.RawLog,
					Timestamp:   rec.Timestamp,
					Hostname:    rec.Hostname,
					Module:      rec.Module,
					Severity:    rec.Severity,
					Brief:       rec.Brief,
					SlotInfo:    rec.SlotInfo,
					SourceFile:  rec.SourceFile,
					MessageBody: rec.MessageBody,
					Parameters:  params,
				}

				var k *model.Knowledge
				var tier string
				var conf float64
				if s.matchEngine != nil {
					k, tier, conf = s.matchEngine.Match(norm, taskInfo.DeviceType, "")
				}

				if k != nil && k.ID > 0 {
					rec.KnowledgeID = k.ID
					rec.MatchTier = tier
					rec.MatchConfidence = conf
					matchedCount++
				} else {
					rec.KnowledgeID = 0
					rec.MatchTier = matcher.TierUnmatch
					rec.MatchConfidence = 0.0
					rec.Severity = 8
				}

				if err := tx.Model(&model.LogRecord{}).Where("id = ?", rec.ID).Updates(map[string]interface{}{
					"knowledge_id":     rec.KnowledgeID,
					"match_tier":       rec.MatchTier,
					"match_confidence": rec.MatchConfidence,
					"severity":         rec.Severity,
				}).Error; err != nil {
					return err
				}
			}
			return nil
		})

		if err != nil {
			logger.Log.Errorf("[Task Service] Batch update log records failed: %v", err)
			if tr != nil {
				tr.Fail(err, "批量更新日志匹配状态失败")
			}
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = err.Error()
			_ = s.globalDB.Save(&taskInfo)
			_ = taskDB.Save(&taskInfo)
			return nil, err
		}

		processedCount += int64(len(records))
		if tr != nil {
			tr.UpdateProgress(processedCount, totalLogCount, fmt.Sprintf("已重新匹配 %d / %d 条 (命中 %d 条)", processedCount, totalLogCount, matchedCount))
		}
	}

	// 阶段二：设备归属与设备指标刷新
	if tr != nil {
		tr.SetStage("AUTO_ASSIGN", "正在同步设备归属与各设备统计指标...")
		tr.AddLog("info", "同步设备归属与统计指标...")
	}
	_, _ = s.AutoAssignDevices(taskID)
	var devices []model.Device
	if err := taskDB.Find(&devices).Error; err == nil {
		for _, dev := range devices {
			var devLogs, devMatched int64
			taskDB.Model(&model.LogRecord{}).Where("device_id = ?", dev.ID).Count(&devLogs)
			taskDB.Model(&model.LogRecord{}).Where("device_id = ? AND knowledge_id > 0", dev.ID).Count(&devMatched)
			taskDB.Model(&model.Device{}).Where("id = ?", dev.ID).Updates(map[string]interface{}{
				"log_count":     int(devLogs),
				"matched_count": int(devMatched),
				"updated_at":    time.Now(),
			})
		}
	}
	s.syncTaskDeviceCount(taskID, taskDB)

	// 阶段三：重建 RCA 根因拓扑分析
	if tr != nil {
		tr.SetStage("RCA_ANALYSIS", "正在基于最新匹配结果重新执行 RCA 根因拓扑分析...")
		tr.AddLog("info", "清理历史 RCA 事件并重新计算...")
	}
	if err := taskDB.Where("1 = 1").Delete(&model.RCAEvent{}).Error; err != nil {
		logger.Log.Warnf("[Task Service] Delete old RCA events failed: %v", err)
	}

	var normLogsForRCA []*model.NormalizedLog
	rows, err := taskDB.Model(&model.LogRecord{}).Order("timestamp asc, id asc").Rows()
	if err != nil {
		logger.Log.Errorf("[Task Service] Failed to query log records for RCA: %v", err)
	} else {
		defer rows.Close()
		for rows.Next() {
			var rec model.LogRecord
			if err := taskDB.ScanRows(rows, &rec); err != nil {
				continue
			}
			var params map[string]string
			if rec.ParametersJSON != "" {
				_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
			}
			normLogsForRCA = append(normLogsForRCA, &model.NormalizedLog{
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
			})
		}
		_ = rows.Close()
	}

	var rcaEvents []model.RCAEvent
	if len(normLogsForRCA) > 0 && s.rcaEngine != nil {
		rcaEvents = s.rcaEngine.Analyze(normLogsForRCA, 300)
		if len(rcaEvents) > 0 {
			if err := taskDB.Create(&rcaEvents).Error; err != nil {
				logger.Log.Errorf("[Task Service] Create reanalyzed RCA events failed: %v", err)
			}
		}
	}

	// 阶段四：刷新全局指标与持久化
	var currentFileCount int64
	_ = taskDB.Model(&model.TaskFile{}).Count(&currentFileCount)
	var currentDeviceCount int64
	_ = taskDB.Model(&model.Device{}).Count(&currentDeviceCount)

	now := time.Now()
	taskInfo.FileCount = int(currentFileCount)
	taskInfo.DeviceCount = int(currentDeviceCount)
	taskInfo.LogCount = int(totalLogCount)
	taskInfo.MatchedCount = matchedCount
	taskInfo.RcaCount = len(rcaEvents)
	taskInfo.Status = model.TaskStatusCompleted
	taskInfo.FinishTime = &now

	_ = s.globalDB.Save(&taskInfo)
	_ = taskDB.Save(&taskInfo)

	logger.Log.Infof("[Task Service] Reanalyze completed for task %s: %d total logs, %d matched, %d rca events",
		taskID, taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)

	if tr != nil {
		tr.AddLog("info", "重新分析完成: 共 %d 行日志，最新命中知识库 %d 条，识别出 %d 个 RCA 根因事件",
			taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)
		tr.SetStage("COMPLETE", "日志审计重新分析已完成")
		tr.Complete(&taskInfo, fmt.Sprintf("重新分析就绪！共处理 %d 行日志，命中知识库 %d 条，发现 %d 个 RCA 根因事件",
			taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount))
	}

	return &taskInfo, nil
}
