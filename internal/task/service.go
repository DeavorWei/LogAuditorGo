package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/summary"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

var taskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)

// 导入与导出的规模上限。缺乏这类硬上限时，单机工具很容易被一次大导入/大导出直接打爆内存。
const (
	// exportHTMLMaxRecords 单任务 HTML 报告最多包含的日志明细条数 (DEV-03)。
	// 超过时按"严重级别优先 + 时间"截断，并在报告头部显式标注总数，绝不静默丢弃。
	exportHTMLMaxRecords = 5000

	// rcaSeverityThreshold / maxRCALogs 控制回灌给 RCA 引擎的样本规模 (TASK-03)。
	// RCA 只关心命中知识库或严重级别较高的日志，无需把百万行全部驻留内存。
	rcaSeverityThreshold = 4      // Severity <= 4（error 及以上）纳入 RCA 样本
	maxRCALogs           = 100000 // RCA 单次分析样本硬上限

	// MaxPageSize 单次分页查询返回的最大行数 (TASK-13)。
	// 深翻页时 offset 越大越慢，这里同时约束单页规模，避免一次拉取过多行。
	MaxPageSize = 10000

	// defaultTimelineSize 多设备时间线的默认返回条数 (DEV-15)
	defaultTimelineSize = 500

	// clusterWindow 时序关联事件簇的滑动窗口时长 (DEV-15)
	clusterWindow = 60 * time.Second

	// maxExportRows 导出行数硬上限 (ARCH-07 / TASK-09 / DEV-09)。
	// 导出接口原先 `PageSize: -1` 一次性把整表读进内存再全量富化，
	// 大任务必然 OOM；超过此上限时改为按页分批富化并明确报错。
	maxExportRows = 100000

	// inQueryChunkSize IN 查询的分片大小 (TASK-15)。
	// SQLite 的 SQLITE_MAX_VARIABLE_NUMBER 限制了单条语句的绑定变量数量，
	// 大 IN 既可能报错，也无法有效利用索引。
	inQueryChunkSize = 500
)

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
	taskLocks   sync.Map
}

func NewService(globalDB *gorm.DB, taskDir string, matchEngine *matcher.MatchEngine, rcaEngine *rootcause.Engine) *Service {
	return &Service{
		globalDB:    globalDB,
		taskDir:     taskDir,
		matchEngine: matchEngine,
		rcaEngine:   rcaEngine,
	}
}

func (s *Service) getTaskLock(taskID string) *sync.Mutex {
	actual, _ := s.taskLocks.LoadOrStore(taskID, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// GetTaskLockForTest 用于单元测试获取任务互斥锁
func (s *Service) GetTaskLockForTest(taskID string) *sync.Mutex {
	return s.getTaskLock(taskID)
}

// CreateEmptyTask 创建初始状态为 PENDING 的空审计任务。
//
// PARSE-04: 新增可选参数 deviceVersion，用于把设备软件版本透传进匹配引擎，
// 让"同型号同版本优先 / 偏好不高于目标版本的最新版本"这套版本分档真正生效。
// 采用可变参数是为了兼容既有调用点（它们大多只有型号没有版本）。
func (s *Service) CreateEmptyTask(taskName string, deviceType string, deviceVersion ...string) (*model.TaskInfo, error) {
	version := ""
	for _, v := range deviceVersion {
		if strings.TrimSpace(v) != "" {
			version = strings.TrimSpace(v)
			break
		}
	}
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
	// KB-06: 归还连接引用；失败补偿路径需要先归还再删文件，用 released 做幂等保护
	released := false
	releaseTaskDB := func() {
		if !released {
			storage.ReleaseTaskDB(taskID)
			released = true
		}
	}
	defer releaseTaskDB()

	taskInfo := &model.TaskInfo{
		TaskID:        taskID,
		TaskName:      taskName,
		DeviceType:    deviceType,
		DeviceVersion: version,
		DBPath:        dbPath,
		Status:        model.TaskStatusPending,
		StartTime:     time.Now(),
	}

	// 写入全局库与任务库
	//
	// TASK-10: 双写必须补偿。原实现在第二个 Create 失败时直接返回错误，
	// 全局库留下永远打不开的"幽灵任务"，且 task_<id>.db 物理文件已生成却无人回收。
	if err := s.globalDB.Create(taskInfo).Error; err != nil {
		releaseTaskDB()
		if delErr := storage.DeleteTaskDB(s.taskDir, taskID); delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete task db %s failed: %v", taskID, delErr)
		}
		return nil, fmt.Errorf("save task info to global db failed: %w", err)
	}
	if err := taskDB.Create(taskInfo).Error; err != nil {
		if delErr := s.globalDB.Where("task_id = ?", taskID).Delete(&model.TaskInfo{}).Error; delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete global task %s failed: %v", taskID, delErr)
		}
		releaseTaskDB()
		if delErr := storage.DeleteTaskDB(s.taskDir, taskID); delErr != nil {
			logger.Log.Warnf("[Task Service] compensate delete task db %s failed: %v", taskID, delErr)
		}
		return nil, fmt.Errorf("save task info to task db failed: %w", err)
	}

	logger.Log.Infof("[Task Service] Created empty task '%s' (ID: %s, Device: %s)", taskName, taskID, deviceType)
	return taskInfo, nil
}

// CreateAndRunTask 创建并执行日志分析任务（兼容已有单步创建调用）
func (s *Service) CreateAndRunTask(taskName string, deviceType string, logContent string, deviceVersion ...string) (*model.TaskInfo, error) {
	taskInfo, err := s.CreateEmptyTask(taskName, deviceType, deviceVersion...)
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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
//
// TASK-01: 返回值改为命名返回值 (ret, err)。
// 原实现使用匿名返回值，panic 被 recover 后函数返回 (nil, nil)，
// 而 api/task_handler.go 只判断 err == nil 就向前端返回成功，
// 导致"任务实际已 FAILED，用户却看到导入成功"的严重误报。
func (s *Service) ImportLogsWithDevice(taskID string, deviceID uint, items []FileUploadItem, conflictMode string, tracker ...*progress.JobTracker) (ret *model.TaskInfo, err error) {
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

	// 任务并发互斥锁：防止同一任务被并发多次导入导致数据重复与竞态 (H-06)
	taskLock := s.getTaskLock(taskID)
	if !taskLock.TryLock() {
		err := fmt.Errorf("task %s is already processing another import job", taskID)
		if tr != nil {
			tr.Fail(err, "当前任务正在执行其他导入任务，请等待其完成后再试")
		}
		return nil, err
	}
	defer taskLock.Unlock()

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
	defer storage.ReleaseTaskDB(taskID)

	var taskInfo model.TaskInfo
	if err := taskDB.First(&taskInfo, "task_id = ?", taskID).Error; err != nil {
		if tr != nil {
			tr.Fail(err, "任务未找到")
		}
		return nil, fmt.Errorf("task not found: %w", err)
	}

	// PARSE-04: 设备软件版本必须在此处取出并透传给匹配引擎。
	// 闭包内直接读 taskInfo 也可以，但显式取值能避免后续把 taskInfo 改成指针/副本时踩坑。
	deviceVersion := strings.TrimSpace(taskInfo.DeviceVersion)

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

	// TASK-01: panic 时除了把任务置为 FAILED，还必须给命名返回值 err 赋值，
	// 否则调用方会收到 (nil, nil) 并误判为成功。
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("Panic in task processing: %v", r)
			logger.Log.Errorf("[Task Service] %s", errStr)
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = errStr
			if saveErr := s.globalDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to global db failed: %v", saveErr)
			}
			if saveErr := taskDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to task db failed: %v", saveErr)
			}
			if tr != nil {
				tr.Fail(fmt.Errorf("%s", errStr), "日志处理异常中断")
			}
			ret = nil
			err = fmt.Errorf("%s", errStr)
		}
	}()

	// 获取已存在的文件记录。
	// TASK-14 / 12.1: 旧实现用 Warnf 吞掉查询错误。一旦读取失败，
	// skip / overwrite 两种冲突策略都会基于"空白的历史清单"做判断，直接导致重复入库。
	var existingFileList []model.TaskFile
	if err := taskDB.Find(&existingFileList).Error; err != nil {
		return s.failTask(taskDB, &taskInfo, tr, fmt.Errorf("load existing task files failed: %w", err), "读取任务文件清单失败")
	}
	dbFilesMap := make(map[string]model.TaskFile, len(existingFileList))
	for _, f := range existingFileList {
		dbFilesMap[f.FileName] = f
	}
	// 记录当前导入批次中已分配占用的文件名，防止同批次文件之间相互覆盖或命名冲突
	batchAssignedNames := make(map[string]bool)

	// ---------- 阶段 RECEIVE：逐文件预处理 ----------
	bundles, totalValidLines, failedFiles := s.prepareBundles(
		taskID, deviceID, items, conflictMode, taskDB, dbFilesMap, batchAssignedNames, tr)

	if len(bundles) == 0 {
		if len(failedFiles) == 0 {
			return s.failTask(taskDB, &taskInfo, tr,
				fmt.Errorf("no valid log lines found in the given files"), "未从给定文件中解析到任何有效日志行")
		}
		return s.failTask(taskDB, &taskInfo, tr,
			fmt.Errorf("all %d file(s) failed to import: %s", len(failedFiles), strings.Join(failedFiles, "; ")),
			"全部文件均导入失败")
	}

	if tr != nil {
		tr.AddLog("info", "待处理有效日志文件 %d 个，总计 %d 行日志", len(bundles), totalValidLines)
		for _, ff := range failedFiles {
			tr.AddLog("error", "文件处理失败: %s", ff)
		}
		tr.SetStage("PARSE_NORM", fmt.Sprintf("正在并发分词与标准化解析 %d 行日志...", totalValidLines))
		tr.UpdateProgress(0, totalValidLines, fmt.Sprintf("已解析 0 / %d 行", totalValidLines))
	}

	// ---------- 阶段 PARSE_NORM / MATCH_KB / SAVE_DB：逐文件流式落库 ----------
	//
	// TASK-04: 不再预分配一个装下全部日志的大切片，改为逐文件按批次解析并落库，
	// 常驻内存与单批大小（5000 行）同阶，与导入文件总量无关；
	// 每个批次一个独立小事务，避免 SQLite 长时间持有 WAL 锁。
	var processedTotal int64 = 0
	for _, bundle := range bundles {
		base := processedTotal
		if tr != nil {
			tr.SetStage("SAVE_DB", fmt.Sprintf("正在写入文件 %s 的解析结果...", bundle.cleanName))
		}
		ingestErr := s.persistFileBundle(taskID, taskDB, bundle, taskInfo.DeviceType, deviceVersion, func(processed int) {
			cur := base + int64(processed)
			processedTotal = cur
			if tr != nil {
				tr.UpdateProgress(cur, totalValidLines,
					fmt.Sprintf("正在解析与入库: %d / %d 行 (%s)", cur, totalValidLines, bundle.cleanName))
			}
		})
		if ingestErr != nil {
			// TASK-07: 统一错误出口，置 FAILED 并回写 ErrorMessage，绝不让任务卡在 PROCESSING
			return s.failTask(taskDB, &taskInfo, tr, ingestErr, fmt.Sprintf("文件 %s 导入失败", bundle.cleanName))
		}
		processedTotal = base + int64(bundle.totalLines)
		if tr != nil {
			tr.AddLog("info", "文件 %s 导入完成 (%d 行)", bundle.cleanName, bundle.totalLines)
		}
	}

	// ---------- 阶段 RCA_ANALYSIS ----------
	if tr != nil {
		tr.SetStage("RCA_ANALYSIS", "正在基于时序关联与故障传播模型执行 RCA 根因拓扑分析...")
		tr.AddLog("info", "开始 RCA 根因分析计算...")
	}
	rcaEvents, totalLogCount, matchedCount, rcaErr := s.runRCAPipeline(taskDB)
	if rcaErr != nil {
		// RCA 失败不阻断任务（日志已完整入库），但必须显式记录，
		// 绝不能像旧实现那样"打一行日志就当没发生"
		logger.Log.Errorf("[Task Service] Rebuild RCA events failed: %v", rcaErr)
		if tr != nil {
			tr.AddLog("error", "RCA 根因事件重建失败: %v", rcaErr)
		}
		totalLogCount, matchedCount = recountTaskLogs(taskDB)
	}

	// ---------- 设备归属与统计刷新 ----------
	if _, assignErr := s.AutoAssignDevices(taskID); assignErr != nil {
		logger.Log.Errorf("[Task Service] Auto assign devices failed: %v", assignErr)
		if tr != nil {
			tr.AddLog("warning", "设备自动归集失败: %v", assignErr)
		}
	}
	if statsErr := s.refreshAllDeviceStats(taskDB); statsErr != nil {
		logger.Log.Errorf("[Task Service] Refresh device stats failed: %v", statsErr)
	}
	s.syncTaskDeviceCount(taskID, taskDB)

	// ---------- 阶段 COMPLETE ----------
	errSummary := ""
	if len(failedFiles) > 0 {
		errSummary = fmt.Sprintf("部分文件导入失败: %s", strings.Join(failedFiles, "; "))
	}
	s.finalizeTaskInfo(taskDB, &taskInfo, int(totalLogCount), int(matchedCount), len(rcaEvents),
		model.TaskStatusCompleted, errSummary)

	logger.Log.Infof("[Task Service] Task %s updated: %d files, %d total logs, %d matched, %d rca events",
		taskID, taskInfo.FileCount, taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)

	if tr != nil {
		tr.AddLog("info", "RCA 拓扑分析就绪: 发现 %d 个根因故障事件", len(rcaEvents))
		if len(failedFiles) > 0 {
			tr.AddLog("warning", "本次导入有 %d 个文件处理失败，详见上方错误日志", len(failedFiles))
		}
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

// SetTaskDeviceVersion 更新任务的设备软件版本 (PARSE-04)。
// 补充导入时若用户在请求里携带了版本，需要先回写到任务元信息，
// 否则本次导入的匹配仍会沿用旧版本（或空版本）。
func (s *Service) SetTaskDeviceVersion(taskID string, deviceVersion string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	version := strings.TrimSpace(deviceVersion)
	if version == "" {
		return nil
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

	if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).
		Update("device_version", version).Error; err != nil {
		return fmt.Errorf("update device_version in global db failed: %w", err)
	}
	if err := taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).
		Update("device_version", version).Error; err != nil {
		return fmt.Errorf("update device_version in task db failed: %w", err)
	}
	return nil
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
	defer storage.ReleaseTaskDB(taskID)

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
		// TASK-19: 原模式的 `_` 未转义，在 LIKE 里等价于"任意一个字符"，
		// 会让 `]` 后跟任意字符的文件名都被误匹配进来。
		// escapeLikePattern 已经把 `_` 处理成 `\_`，这里的前缀分隔符同样必须转义。
		escaped := escapeLikePattern(filter.SourceFile)
		query = query.Where("(source_file = ? OR source_file LIKE ? ESCAPE '\\')", filter.SourceFile, "%]\\_"+escaped)
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

	// 导出模式 (PageSize <= 0)
	//
	// TASK-09: 原实现 `PageSize: -1` 一次性把整表（含 raw_log / message_body 大文本）
	// 读进内存，大任务导出必然 OOM。这里加上硬上限并在超限时明确报错，
	// 让调用方改用分批拉取，而不是"静默返回残缺结果"。
	if filter.PageSize <= 0 {
		if total > maxExportRows {
			return nil, total, fmt.Errorf("export size limit exceeded: task has %d logs, hard limit is %d; please narrow the filter or page through the data",
				total, maxExportRows)
		}
		var records []model.LogRecord
		if err := query.Order("id asc").Limit(maxExportRows).Find(&records).Error; err != nil {
			return nil, total, err
		}
		return records, total, nil
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	} else if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	var records []model.LogRecord
	// TASK-13: 提供了游标锚点时改走 keyset 分页，避免深翻页的 offset 线性开销。
	// 排序固定为 id asc，与 offset 分支一致，保证两种模式结果可拼接。
	if filter.AfterID > 0 {
		err = query.Where("id > ?", filter.AfterID).
			Order("id asc").Limit(pageSize).Find(&records).Error
		return records, total, err
	}

	offset := (page - 1) * pageSize
	err = query.Order("id asc").Offset(offset).Limit(pageSize).Find(&records).Error
	return records, total, err
}

// StreamTaskLogs 以数据库游标逐行流式读取日志并回调给调用方 (ARCH-07 / TASK-09)。
//
// 旧导出链路是 `QueryTaskLogs(PageSize:-1)` + 全量富化，
// 一次性把整表读进内存再序列化，大任务必然 OOM。
// 这里用 Rows() 游标 + 逐行回调，内存占用恒定为 O(1)，
// 配合 http.Flusher 即可实现边查边下发的真流式导出。
func (s *Service) StreamTaskLogs(taskID string, filter model.LogQueryFilter, emit func(rec model.LogRecord) error) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	if emit == nil {
		return fmt.Errorf("emit callback is nil")
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

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
		// TASK-19: 分隔符写作 `\_`，避免 `]` 后的任意字符被 LIKE 的通配符误匹配
		escaped := escapeLikePattern(filter.SourceFile)
		query = query.Where("(source_file = ? OR source_file LIKE ? ESCAPE '\\')", filter.SourceFile, "%]\\_"+escaped)
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

	rows, err := query.Order("id asc").Rows()
	if err != nil {
		return fmt.Errorf("open log rows for stream export failed: %w", err)
	}
	defer rows.Close()

	var rec model.LogRecord
	for rows.Next() {
		if err := taskDB.ScanRows(rows, &rec); err != nil {
			return fmt.Errorf("scan log row failed: %w", err)
		}
		if err := emit(rec); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate log rows failed: %w", err)
	}
	return nil
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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
	// TASK-15: 单次 IN 查询的 ID 数量必须分片。
	// 旧实现把全部关联日志 ID 拼进一条 `IN (...)`，
	// 超过 SQLITE_MAX_VARIABLE_NUMBER 会直接报错，且大 IN 无法有效利用索引。
	// 这里按 500 一个分片，与知识库查重分片保持一致的口径。
	for start := 0; start < len(allLogIDs); start += inQueryChunkSize {
		end := start + inQueryChunkSize
		if end > len(allLogIDs) {
			end = len(allLogIDs)
		}
		var records []model.LogRecord
		if err := taskDB.Where("id IN ?", allLogIDs[start:end]).Find(&records).Error; err != nil {
			return nil, fmt.Errorf("find log records for rca failed: %w", err)
		}
		for _, r := range records {
			logRecordMap[r.ID] = r
		}
	}

	// 批量加载设备映射
	var devList []model.Device
	if err := taskDB.Find(&devList).Error; err != nil {
		return nil, fmt.Errorf("find devices for rca failed: %w", err)
	}
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
	defer storage.ReleaseTaskDB(taskID)

	// DEV-03: 原实现硬编码 Limit(100)，且 exporter 内部还会再截一次 100 条。
	// 用户以为导出的是"完整报告"，实际只有前 100 条，RCA 引用的日志可能根本不在其中，
	// 对审计留痕是致命的。改为按可配置上限导出，并把总数回填进报告头部显式说明。
	var totalLogs int64
	if err := taskDB.Model(&model.LogRecord{}).Count(&totalLogs).Error; err != nil {
		return "", fmt.Errorf("count log records for html export failed: %w", err)
	}

	var records []model.LogRecord
	// 按严重级别升序（数值越小越紧急）+ 时间排序，确保截断时保留最有诊断价值的日志
	q := taskDB.Order("severity asc, timestamp asc, id asc")
	if totalLogs > int64(exportHTMLMaxRecords) {
		q = q.Limit(exportHTMLMaxRecords)
	}
	if err := q.Find(&records).Error; err != nil {
		return "", fmt.Errorf("load log records for html export failed: %w", err)
	}

	var rcas []model.RCAEvent
	if err := taskDB.Find(&rcas).Error; err != nil {
		return "", fmt.Errorf("load rca events for html export failed: %w", err)
	}

	// REANA-13: 补充设备维度与命中知识的官方释义。
	// 原报告只有一张日志明细表，看不出"涉及哪些设备""命中了什么知识、该怎么处理"，
	// 作为离线留痕文档价值有限。
	var devices []model.Device
	if err := taskDB.Order("id asc").Find(&devices).Error; err != nil {
		logger.Log.Warnf("[Task Service] load devices for html export failed: %v", err)
	}

	opts := make([]ReportOption, 0, 2)
	if len(devices) > 0 {
		opts = append(opts, WithDevices(devices))
	}
	if kbMap := s.loadKnowledgeBriefs(records); len(kbMap) > 0 {
		opts = append(opts, WithKnowledgeMap(kbMap))
	}

	html := GenerateHTMLReport(task, records, rcas, int(totalLogs), opts...)
	return html, nil
}

// loadKnowledgeBriefs 批量加载报告中命中知识的简要信息 (REANA-13)。
// 一次 IN 查询完成，绝不在模板渲染循环里逐条查库。
func (s *Service) loadKnowledgeBriefs(records []model.LogRecord) map[uint]model.Knowledge {
	if s.globalDB == nil || len(records) == 0 {
		return nil
	}
	kidSet := make(map[uint]bool, 8)
	kids := make([]uint, 0, 8)
	for _, r := range records {
		if r.KnowledgeID > 0 && !kidSet[r.KnowledgeID] {
			kidSet[r.KnowledgeID] = true
			kids = append(kids, r.KnowledgeID)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	var list []model.Knowledge
	if err := s.globalDB.Where("id IN ?", kids).Find(&list).Error; err != nil {
		logger.Log.Warnf("[Task Service] load knowledge briefs for html export failed: %v", err)
		return nil
	}
	out := make(map[uint]model.Knowledge, len(list))
	for _, k := range list {
		out[k.ID] = k
	}
	return out
}

// DeleteTask 删除任务及物理数据库
//
// DEV-04 / TASK-11: 原实现"先删全局元数据、再删物理库文件"，顺序完全颠倒：
// Windows 上 DB 句柄/WAL 占用会让 os.Remove 必然失败，
// 结果元数据已消失、任务不可见，而 .db/-wal/-shm 成为永久孤儿；
// 且整个删除过程不持任务锁，可以在导入进行中删库导致半截状态。
//
// 修正后的时序：取任务锁 → 驱逐并关闭连接 → 删物理文件 → 最后删全局元数据。
// 任一步失败都保留全局记录，返回可重试的错误，绝不产生无主残留。
func (s *Service) DeleteTask(taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}

	taskLock := s.getTaskLock(taskID)
	if !taskLock.TryLock() {
		return fmt.Errorf("task %s is busy: another job is running, please retry later", taskID)
	}
	defer taskLock.Unlock()

	// 1. 先从连接池强制驱逐并关闭底层句柄（等待在途引用归零，超时强制关闭）
	if err := storage.GlobalPool.EvictTaskDB(taskID); err != nil {
		logger.Log.Errorf("[Task Service] evict task db %s failed: %v", taskID, err)
		return fmt.Errorf("evict task db failed: %w", err)
	}

	// 2. 删除物理磁盘文件 (.db / -wal / -shm)
	if err := storage.DeleteTaskDBFiles(s.taskDir, taskID); err != nil {
		logger.Log.Errorf("[Task Service] delete task db files for %s failed: %v", taskID, err)
		return fmt.Errorf("delete task db files failed: %w", err)
	}

	// 3. 最后删除全局元数据。
	//    注意列名是 task_id（业务主键），绝非自增主键 id——
	//    用 id 过滤时 task_id 是字符串，SQLite 会静默匹配 0 行，导致删不掉任何数据。
	res := s.globalDB.Where("task_id = ?", taskID).Delete(&model.TaskInfo{})
	if res.Error != nil {
		logger.Log.Errorf("[Task Service] delete task %s from global db failed: %v", taskID, res.Error)
		return fmt.Errorf("delete task from global db failed: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		logger.Log.Warnf("[Task Service] task %s not found in global db during delete (physical files already removed)", taskID)
	}

	logger.Log.Infof("[Task Service] Task %s deleted (files + metadata)", taskID)
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

	device.TaskID = taskID
	if device.DeviceName == "" {
		// DEV-13: 原默认名 `Device-<unix%10000>` 在同一秒内创建多个设备时必然重名，
		// 而 AutoAssignDevices 与导入链路都按 `device_name OR hostname` 匹配，重名会误绑。
		// 改为"时间戳 + 随机短串"，碰撞概率可忽略。
		device.DeviceName = fmt.Sprintf("Device-%s", strings.ToUpper(uuid.New().String()[:6]))
	}
	if device.DeviceType == "" {
		device.DeviceType = "Router"
	}
	if device.Color == "" {
		device.Color = nextDeviceColor(taskDB)
	}
	device.CreatedAt = time.Now()
	device.UpdatedAt = time.Now()

	// DEV-13: 创建前查重，避免同一任务下出现同名设备导致归属错乱
	var dup model.Device
	if err := taskDB.Where("task_id = ? AND device_name = ?", taskID, device.DeviceName).
		First(&dup).Error; err == nil {
		return nil, fmt.Errorf("device name %q already exists in task %s", device.DeviceName, taskID)
	}

	if err := taskDB.Create(device).Error; err != nil {
		return nil, fmt.Errorf("create device in task db failed: %w", err)
	}

	s.syncTaskDeviceCount(taskID, taskDB)
	logger.Log.Infof("[Task Service] Created device '%s' (ID: %d, Type: %s) for task %s", device.DeviceName, device.ID, device.DeviceType, taskID)
	return device, nil
}

// nextDeviceColor 依据当前设备数为新设备挑选配色
func nextDeviceColor(taskDB *gorm.DB) string {
	var count int64
	if err := taskDB.Model(&model.Device{}).Count(&count).Error; err != nil {
		logger.Log.Warnf("[Task Service] count devices for color picking failed: %v", err)
		count = 0
	}
	return DeviceDefaultColors[int(count)%len(DeviceDefaultColors)]
}

// getOrCreateDevice 按名称或主机名查找设备，不存在则创建。
//
// DEV-16: 这段"查不到就造一个 Router + 配色 + 时间戳"的逻辑原本在三处各写一遍
// （AutoAssignDevices、ImportLogsWithDevice、CreateDevice），
// 任何一处的行为漂移都会让设备归属出现不一致。
func getOrCreateDevice(taskDB *gorm.DB, taskID, name, hostname string) (model.Device, error) {
	var dev model.Device
	err := taskDB.Where("task_id = ? AND (device_name = ? OR hostname = ?)", taskID, name, name).First(&dev).Error
	if err == nil {
		// 已存在：若 hostname 为空且本次嗅探到了，则补齐
		if dev.Hostname == "" && hostname != "" {
			dev.Hostname = hostname
			if saveErr := taskDB.Model(&model.Device{}).Where("id = ?", dev.ID).
				Update("hostname", hostname).Error; saveErr != nil {
				logger.Log.Warnf("[Task Service] backfill hostname for device %d failed: %v", dev.ID, saveErr)
			} else {
				dev.UpdatedAt = time.Now()
			}
		}
		return dev, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Device{}, err
	}

	dev = model.Device{
		TaskID:     taskID,
		DeviceName: name,
		DeviceType: "Router",
		Hostname:   hostname,
		Color:      nextDeviceColor(taskDB),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if createErr := taskDB.Create(&dev).Error; createErr != nil {
		return model.Device{}, createErr
	}
	return dev, nil
}

// syncTaskDeviceCount 同步任务的设备数量到全局库与任务库。
//
// DEV-14: 原实现两次 Update 的 error 全部丢弃。DB 故障时设备数会静默停留在旧值，
// 前端"设备 N 台"与实际列表不一致，且没有任何告警。
func (s *Service) syncTaskDeviceCount(taskID string, taskDB *gorm.DB) {
	var count int64
	if err := taskDB.Model(&model.Device{}).Count(&count).Error; err != nil {
		logger.Log.Errorf("[Task Service] count devices of task %s failed: %v", taskID, err)
		return
	}
	if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).
		Update("device_count", int(count)).Error; err != nil {
		logger.Log.Errorf("[Task Service] sync device_count to global db (task %s) failed: %v", taskID, err)
	}
	if err := taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).
		Update("device_count", int(count)).Error; err != nil {
		logger.Log.Errorf("[Task Service] sync device_count to task db (task %s) failed: %v", taskID, err)
	}
}

// deviceStatsRow 设备日志量与匹配量的批量聚合结果
type deviceStatsRow struct {
	DeviceID uint  `gorm:"column:device_id"`
	LogCount int64 `gorm:"column:log_count"`
	Matched  int64 `gorm:"column:matched_count"`
}

// loadDeviceStatsBatch 一次性按 device_id 聚合出各设备的日志数与匹配数。
//
// DEV-05 / DEV-07: 原实现在循环里对每个设备各执行 2~5 次 COUNT，
// 设备数一多查询次数就线性爆炸（2N+1）。这里改为一条 GROUP BY 聚合后回填。
func loadDeviceStatsBatch(taskDB *gorm.DB) (map[uint]deviceStatsRow, error) {
	rows := make([]deviceStatsRow, 0)
	err := taskDB.Model(&model.LogRecord{}).
		Select("device_id, COUNT(*) AS log_count, COALESCE(SUM(CASE WHEN knowledge_id > 0 THEN 1 ELSE 0 END), 0) AS matched_count").
		Group("device_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]deviceStatsRow, len(rows))
	for _, r := range rows {
		out[r.DeviceID] = r
	}
	return out, nil
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

	var devices []model.Device
	if err := taskDB.Order("id asc").Find(&devices).Error; err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return devices, nil
	}

	// DEV-05: 原实现循环内每台设备 2 次 COUNT（2N+1 次查询），
	// 设备数增长时列表接口线性劣化。改为一条 GROUP BY 批量聚合。
	stats, err := loadDeviceStatsBatch(taskDB)
	if err != nil {
		return nil, fmt.Errorf("aggregate device stats failed: %w", err)
	}
	for i := range devices {
		if st, ok := stats[devices[i].ID]; ok {
			devices[i].LogCount = int(st.LogCount)
			devices[i].MatchedCount = int(st.Matched)
		}
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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

// DeviceUpdatableFields 设备允许被外部更新的字段白名单 (DEV-01)。
//
// API 层（ARCH-03）早已有 DTO 白名单，但 service 层仍接受裸 map[string]interface{} 直写 GORM，
// 任何新增的内部调用点都能重新引入"改主键 / 跨任务划转 / 伪造统计"的越权写入。
// 纵深防御要求这里也必须过滤——不在白名单内的键一律丢弃并记录告警。
var DeviceUpdatableFields = map[string]bool{
	"device_name":   true,
	"device_type":   true,
	"management_ip": true,
	"hostname":      true,
	"description":   true,
	"color":         true,
}

// ErrNoUpdatableField 表示请求中没有任何可更新的白名单字段
var ErrNoUpdatableField = fmt.Errorf("no updatable device fields provided")

// UpdateDevice 更新设备属性
//
// DEV-01 / DEV-11:
//  1. 入参经白名单过滤，杜绝 id / task_id / log_count / created_at 等敏感列被写入；
//  2. 先 First 校验存在性并判 RowsAffected，避免"更新 0 行却返回成功"，
//     随后 GetDevice 才不会抛 ErrRecordNotFound 变成 500；
//  3. 复制 map 后再注入 updated_at，绝不就地修改调用方传入的 map（原实现的隐藏副作用）。
func (s *Service) UpdateDevice(taskID string, deviceID uint, updates map[string]interface{}) (*model.Device, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

	// 1. 字段白名单过滤
	filtered := make(map[string]interface{}, len(updates)+1)
	for k, v := range updates {
		if DeviceUpdatableFields[k] {
			filtered[k] = v
			continue
		}
		logger.Log.Warnf("[Task Service] device update rejected non-whitelisted field %q (task %s, device %d)", k, taskID, deviceID)
	}
	if len(filtered) == 0 {
		return nil, ErrNoUpdatableField
	}

	// 2. 先校验存在性
	var existing model.Device
	if err := taskDB.First(&existing, "id = ?", deviceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("device %d not found in task %s", deviceID, taskID)
		}
		return nil, err
	}

	// 3. 注入时间戳（写的是副本，不污染调用方传入的 map）
	filtered["updated_at"] = time.Now()

	res := taskDB.Model(&model.Device{}).Where("id = ?", deviceID).Updates(filtered)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, fmt.Errorf("device %d not found in task %s", deviceID, taskID)
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
	defer storage.ReleaseTaskDB(taskID)

	// DEV-08: 原实现"删设备"与"解绑日志"是两个独立操作，
	// 解绑失败只打一行 Warn——设备已删、日志仍指向旧 device_id，
	// 后续按设备查询与统计会出现幽灵数据。这里纳入同一事务，失败整体回滚。
	//
	// 同时删除不存在的设备不再返回 nil（原实现静默成功），改为显式报错。
	err = taskDB.Transaction(func(tx *gorm.DB) error {
		res := tx.Delete(&model.Device{}, "id = ?", deviceID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("device %d not found in task %s", deviceID, taskID)
		}
		if err := tx.Model(&model.LogRecord{}).
			Where("device_id = ?", deviceID).
			Update("device_id", 0).Error; err != nil {
			return fmt.Errorf("unbind log records of device %d failed: %w", deviceID, err)
		}
		return nil
	})
	if err != nil {
		return err
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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

	// DEV-06: 原实现完全没有事务，中途失败会留下"部分设备已绑、部分没绑"的半截状态；
	// 且回绑条件只有 `hostname = ?`，用户手工归属到 A 设备的日志
	// 会因 hostname 恰好等于 B 设备名而被强行改绑到 B——手工归属被静默覆盖。
	// 产品决策：自动归集只认领"尚未归属"的日志（device_id = 0）。
	//
	// DEV-16: 设备创建逻辑收敛到 getOrCreateDevice，与导入链路保持一致。
	type assignPlan struct {
		hostname string
		deviceID uint
	}
	plans := make([]assignPlan, 0, len(hostnames))

	err = taskDB.Transaction(func(tx *gorm.DB) error {
		for _, raw := range hostnames {
			h := strings.TrimSpace(raw)
			if h == "" {
				continue
			}
			dev, devErr := getOrCreateDevice(tx, taskID, h, h)
			if devErr != nil {
				return fmt.Errorf("prepare device for hostname %q failed: %w", h, devErr)
			}

			// 仅绑定尚未归属的日志，保护用户的手工归属结果
			if updErr := tx.Model(&model.LogRecord{}).
				Where("hostname = ? AND (device_id = 0 OR device_id IS NULL)", h).
				Update("device_id", dev.ID).Error; updErr != nil {
				return fmt.Errorf("assign logs of hostname %q to device %d failed: %w", h, dev.ID, updErr)
			}
			plans = append(plans, assignPlan{hostname: h, deviceID: dev.ID})
		}
		return nil
	})
	if err != nil {
		logger.Log.Errorf("[Task Service] AutoAssignDevices failed for task %s: %v", taskID, err)
		return nil, err
	}

	// 事务外统一刷新设备统计（一次 GROUP BY 聚合，避免在事务内做 N 次 COUNT 拉长持锁时间）
	stats, statsErr := loadDeviceStatsBatch(taskDB)
	if statsErr != nil {
		logger.Log.Errorf("[Task Service] aggregate device stats after auto assign failed: %v", statsErr)
	} else {
		for _, p := range plans {
			st := stats[p.deviceID]
			if err := taskDB.Model(&model.Device{}).Where("id = ?", p.deviceID).Updates(map[string]interface{}{
				"log_count":     int(st.LogCount),
				"matched_count": int(st.Matched),
				"updated_at":    time.Now(),
			}).Error; err != nil {
				logger.Log.Warnf("[Task Service] refresh stats of device %d failed: %v", p.deviceID, err)
			}
		}
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

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
	defer storage.ReleaseTaskDB(taskID)

	var devList []model.Device
	if err := taskDB.Find(&devList).Error; err != nil {
		return nil, 0, err
	}
	devMap := make(map[uint]model.Device)
	for _, d := range devList {
		devMap[d.ID] = d
	}

	query := taskDB.Model(&model.LogRecord{})

	if len(filter.DeviceIDs) > 0 {
		query = query.Where("device_id IN ?", filter.DeviceIDs)
	} else if filter.IncludeUnassigned != nil && !*filter.IncludeUnassigned {
		// DEV-10: 调用方明确要求排除"未指定设备"的日志。
		// 默认情况下保留——产品决策要求未归属日志在多设备视图中仍然可见，
		// 运维正是靠它发现归属遗漏。
		query = query.Where("device_id > 0")
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
	if filter.PageSize <= 0 {
		// DEV-09: 与 QueryTaskLogs 保持一致的导出上限。
		// 原实现 `PageSize < 0` 一次性载入全表并全量 json.Unmarshal，百万级任务必然 OOM。
		if total > maxExportRows {
			return nil, total, fmt.Errorf("export size limit exceeded: query matched %d logs, hard limit is %d; please narrow the filter or page through the data",
				total, maxExportRows)
		}
		err = query.Order(orderClause).Limit(maxExportRows).Find(&records).Error
	} else {
		page := filter.Page
		if page <= 0 {
			page = 1
		}
		pageSize := filter.PageSize
		if pageSize <= 0 {
			pageSize = 100
		} else if pageSize > MaxPageSize {
			pageSize = MaxPageSize
		}
		offset := (page - 1) * pageSize
		err = query.Order(orderClause).Offset(offset).Limit(pageSize).Find(&records).Error
	}

	if err != nil {
		return nil, 0, err
	}

	// 批量收集当前页命中的 KnowledgeID，查出官方知识库实体映射
	var kidList []uint
	kidSet := make(map[uint]bool)
	for _, r := range records {
		if r.KnowledgeID > 0 && !kidSet[r.KnowledgeID] {
			kidSet[r.KnowledgeID] = true
			kidList = append(kidList, r.KnowledgeID)
		}
	}
	kbMap := s.getKnowledgeMap(kidList)

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
			EventSummary:    summary.GenerateSummary(r.Module, r.Brief, r.Severity, r.MessageBody, params, kbMap[r.KnowledgeID]),
		}
	}

	return events, total, nil
}

// getKnowledgeMap 批量从全局数据库查询 Knowledge 实体映射
func (s *Service) getKnowledgeMap(kids []uint) map[uint]*model.Knowledge {
	res := make(map[uint]*model.Knowledge)
	if len(kids) == 0 || s.globalDB == nil {
		return res
	}
	var list []model.Knowledge
	if err := s.globalDB.Where("id IN ?", kids).Find(&list).Error; err == nil {
		for i := range list {
			res[list[i].ID] = &list[i]
		}
	}
	return res
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
	// KB-06: 连接池已改为引用计数模型，必须显式归还引用，
	// 否则该任务库连接永远无法被 LRU 淘汰，也无法在删除任务时安全关闭句柄。
	defer storage.ReleaseTaskDB(taskID)

	var devices []model.Device
	if len(deviceIDs) > 0 {
		if err := taskDB.Where("id IN ?", deviceIDs).Order("id asc").Find(&devices).Error; err != nil {
			return nil, fmt.Errorf("load selected devices failed: %w", err)
		}
	} else {
		if err := taskDB.Order("id asc").Find(&devices).Error; err != nil {
			return nil, fmt.Errorf("load devices failed: %w", err)
		}
	}

	selectedDevIDs := make([]uint, len(devices))
	for i, d := range devices {
		selectedDevIDs[i] = d.ID
	}

	// DEV-07: 批量聚合取代"每设备 4~5 次查询"的 N+1 模式
	aggMap, sevMap, modMap, aggErr := aggregateDeviceReports(taskDB)
	if aggErr != nil {
		return nil, aggErr
	}

	// REANA-05: 时间跨度与总条数走 SQL 聚合，不再基于被截断的时间线切片
	span, spanErr := aggregateTimelineSpan(taskDB, selectedDevIDs)
	if spanErr != nil {
		return nil, spanErr
	}

	var devStatsList []model.DeviceStats
	totalLogs := 0
	totalMatched := 0

	for _, dev := range devices {
		agg := aggMap[dev.ID]
		devCopy := dev
		devCopy.LogCount = int(agg.LogCount)
		devCopy.MatchedCount = int(agg.Matched)

		firstSeen := parseSQLTime(agg.MinTime)
		lastSeen := parseSQLTime(agg.MaxTime)

		devStatsList = append(devStatsList, model.DeviceStats{
			Device:       devCopy,
			LogCount:     int(agg.LogCount),
			MatchedCount: int(agg.Matched),
			TopModules:   modMap[dev.ID],
			SeverityDist: sevMap[dev.ID],
			FirstSeen:    firstSeen,
			LastSeen:     lastSeen,
		})

		totalLogs += int(agg.LogCount)
		totalMatched += int(agg.Matched)
	}

	timelineFilter := model.MultiDeviceLogFilter{
		DeviceIDs: selectedDevIDs,
		PageSize:  defaultTimelineSize,
		AscOrder:  true,
	}
	// DEV-07: 时间线查询的错误不再被丢弃。
	// 旧实现 `timelineEvents, _, _ := ...` 静默产出"有统计无时间线"的残缺报告，
	// 用户看到的簇与结论都基于空数据，却没有任何提示。
	timelineEvents, _, err := s.QueryMultiDeviceLogs(taskID, timelineFilter)
	if err != nil {
		return nil, fmt.Errorf("load multi-device timeline failed: %w", err)
	}

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

	clusters := buildCorrelatedClusters(timelineEvents, clusterWindow)
	conclusion := generateMultiDeviceConclusion(devStatsList, timelineEvents, clusters, commonEvents, span)

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

	// DEV-02: 原实现以"簇首事件"的时间为基准，且命中分支内 curStartTime 从不前移。
	// 设备日志的间隔通常远小于 60s，这会让簇无限膨胀，最终把整段日志并成一个巨型簇，
	// 完全丧失"关联事件簇"的诊断意义；若入参乱序，Sub() 为负更会把全部事件并成一簇。
	// 这里改为：入参先稳定排序，簇的时间基准跟随"最后一条入簇事件"（真正的滑动窗口）。
	sorted := make([]model.DeviceTimelineEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	var clusters []model.CorrelatedTimelineCluster
	var curEvents []model.DeviceTimelineEvent
	var curLastTime time.Time

	flush := func() {
		if len(curEvents) == 0 {
			return
		}
		if c := evaluateCluster(curEvents); c != nil {
			clusters = append(clusters, *c)
		}
		curEvents = nil
		curLastTime = time.Time{}
	}

	for _, ev := range sorted {
		if len(curEvents) == 0 {
			curEvents = []model.DeviceTimelineEvent{ev}
			curLastTime = ev.Timestamp
			continue
		}

		// 与"簇内最后一条事件"比较，窗口随事件推进，避免单一巨型簇
		if !ev.Timestamp.Before(curLastTime) && ev.Timestamp.Sub(curLastTime) <= window {
			curEvents = append(curEvents, ev)
			curLastTime = ev.Timestamp
			continue
		}
		flush()
		curEvents = []model.DeviceTimelineEvent{ev}
		curLastTime = ev.Timestamp
	}
	flush()

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

// ExportMultiDeviceHTML 导出多设备对比 HTML 报告
func (s *Service) ExportMultiDeviceHTML(taskID string, deviceIDs []uint) (string, error) {
	report, err := s.GetMultiDeviceReport(taskID, deviceIDs)
	if err != nil {
		return "", err
	}
	return GenerateMultiDeviceHTMLReport(report), nil
}

// ReanalyzeTask 按照导入日志全流程标准，对任务内所有日志执行全量重新分词解析、参数提取、知识库匹配、入库持久化、设备同步及 RCA 根因拓扑分析
//
// REANA-01: 补上与 ImportLogsWithDevice 一致的任务互斥锁。
// 原实现完全没有加锁，重分析可与同任务的另一次导入/重分析并发执行，
// 交错改写同一批 LogRecord 并重复统计 matchedCount，H-06 的互斥保证在此处完全失效。
//
// REANA-02: 补上与导入路径一致的 panic 恢复，避免任务永久卡在 PROCESSING。
//
// 返回值同样改为命名返回值，确保 panic / 失败路径不会向调用方返回 (nil, nil)。
func (s *Service) ReanalyzeTask(taskID string, tr *progress.JobTracker) (ret *model.TaskInfo, err error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}

	taskLock := s.getTaskLock(taskID)
	if !taskLock.TryLock() {
		lockErr := fmt.Errorf("task %s is already processing another job", taskID)
		if tr != nil {
			tr.Fail(lockErr, "当前任务正在执行其他作业，请等待其完成后再试")
		}
		return nil, lockErr
	}
	defer taskLock.Unlock()

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to open task database: %w", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	var taskInfo model.TaskInfo
	if err := s.globalDB.Where("task_id = ?", taskID).First(&taskInfo).Error; err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	taskInfo.Status = model.TaskStatusProcessing
	taskInfo.ErrorMessage = ""
	_ = s.globalDB.Save(&taskInfo)
	_ = taskDB.Save(&taskInfo)

	// REANA-02: panic 时置 FAILED 并回填 err，绝不静默返回 (nil, nil)
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("Panic in task reanalysis: %v", r)
			logger.Log.Errorf("[Task Service] %s", errStr)
			taskInfo.Status = model.TaskStatusFailed
			taskInfo.ErrorMessage = errStr
			if saveErr := s.globalDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to global db failed: %v", saveErr)
			}
			if saveErr := taskDB.Save(&taskInfo).Error; saveErr != nil {
				logger.Log.Errorf("save task info to task db failed: %v", saveErr)
			}
			if tr != nil {
				tr.Fail(fmt.Errorf("%s", errStr), "重新分析异常中断")
			}
			ret = nil
			err = fmt.Errorf("%s", errStr)
		}
	}()

	deviceVersion := strings.TrimSpace(taskInfo.DeviceVersion)

	var totalLogCount int64
	if err := taskDB.Model(&model.LogRecord{}).Count(&totalLogCount).Error; err != nil {
		return s.failTask(taskDB, &taskInfo, tr, fmt.Errorf("count task log records failed: %w", err), "统计任务日志失败")
	}

	if tr != nil {
		tr.AddLog("info", "启动任务【%s】全流程重新分析，将严格按照导入全流程重新执行解析、匹配与根因诊断 (共 %d 条原始日志)", taskInfo.TaskName, totalLogCount)
		tr.SetStage("PARSE_NORM", fmt.Sprintf("正在对 %d 条日志重新执行原始文本分行解析与标准化...", totalLogCount))
		tr.UpdateProgress(0, totalLogCount, "准备执行全量日志解析与匹配...")
	}

	// ---------- 阶段一：流式批量重新解析与匹配 ----------
	//
	// 9.1: 解析与匹配复用导入链路的 parseLogLine，两条链路的行为不可能再漂移。
	// REANA-09: 落库从"逐行 UPDATE"改为"每 500 行一个 CASE WHEN 批量 UPDATE"，
	//           百万级重分析的 SQL 往返次数从百万级降到千级。
	matchedCount := 0
	var processedCount int64 = 0
	var lastID uint = 0

	for {
		var records []model.LogRecord
		if err := taskDB.Where("id > ?", lastID).Order("id asc").Limit(reanalyzeBatchSize).Find(&records).Error; err != nil {
			// REANA-02: 原实现 break 后继续走完流程，把只处理了一部分的任务标记为 COMPLETED。
			return s.failTask(taskDB, &taskInfo, tr,
				fmt.Errorf("query batch log records failed: %w", err), "查询任务日志失败，重新分析已中止")
		}
		if len(records) == 0 {
			break
		}

		for i := range records {
			rec := &records[i]
			lastID = rec.ID
			parsed := s.parseLogLine(rec.RawLog, rec.SourceFile, rec.DeviceID, taskInfo.DeviceType, deviceVersion)

			if parsed.KnowledgeID > 0 {
				matchedCount++
			}
			// 保留主键与归属信息，只覆盖解析产出的字段
			parsed.ID = rec.ID
			parsed.DeviceID = rec.DeviceID
			parsed.SourceFile = rec.SourceFile
			records[i] = parsed
		}

		if err := batchUpdateLogRecords(taskDB, records); err != nil {
			return s.failTask(taskDB, &taskInfo, tr,
				fmt.Errorf("batch update log records failed: %w", err), "批量更新重新解析数据失败")
		}

		processedCount += int64(len(records))
		if tr != nil {
			tr.UpdateProgress(processedCount, totalLogCount,
				fmt.Sprintf("已全量重新解析与匹配: %d / %d 行 (命中知识库 %d 条)", processedCount, totalLogCount, matchedCount))
		}
	}

	// ---------- 阶段二：设备归属与统计刷新 ----------
	if tr != nil {
		tr.SetStage("AUTO_ASSIGN", "正在根据重新解析的设备主机名同步设备归属与统计指标...")
		tr.AddLog("info", "同步设备归属与统计指标...")
	}
	if _, assignErr := s.AutoAssignDevices(taskID); assignErr != nil {
		// REANA-08: 旧实现 `_, _ = s.AutoAssignDevices(taskID)` 把失败完全吞掉，
		// 设备归属与统计指标停留在旧值，报告数字与明细对不上。
		logger.Log.Errorf("[Task Service] Auto assign devices during reanalyze failed: %v", assignErr)
		if tr != nil {
			tr.AddLog("warning", "设备自动归集失败: %v", assignErr)
		}
	} else if statsErr := s.refreshAllDeviceStats(taskDB); statsErr != nil {
		logger.Log.Errorf("[Task Service] Refresh device stats failed: %v", statsErr)
	}
	s.syncTaskDeviceCount(taskID, taskDB)

	// ---------- 阶段三：RCA 根因拓扑重建 ----------
	if tr != nil {
		tr.SetStage("RCA_ANALYSIS", "正在基于全量重新解析后的时序日志执行 RCA 根因拓扑分析...")
		tr.AddLog("info", "重新计算拓扑与传播链并整体替换历史 RCA 事件...")
	}
	rcaEvents, finalLogCount, finalMatched, rcaErr := s.runRCAPipeline(taskDB)
	if rcaErr != nil {
		logger.Log.Errorf("[Task Service] Rebuild RCA events failed: %v", rcaErr)
		if tr != nil {
			tr.AddLog("error", "RCA 根因事件重建失败: %v", rcaErr)
		}
		finalLogCount, finalMatched = recountTaskLogs(taskDB)
	}

	// ---------- 阶段四：收尾 ----------
	s.finalizeTaskInfo(taskDB, &taskInfo, int(finalLogCount), int(finalMatched), len(rcaEvents), model.TaskStatusCompleted, "")

	logger.Log.Infof("[Task Service] Reanalyze completed for task %s: %d total logs, %d matched, %d rca events",
		taskID, taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)

	if tr != nil {
		tr.AddLog("info", "全流程重新分析完成: 共 %d 行日志，最新命中知识库 %d 条，识别出 %d 个 RCA 根因事件",
			taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount)
		tr.SetStage("COMPLETE", "日志全流程重新解析与审计分析已完成")
		tr.Complete(&taskInfo, fmt.Sprintf("全流程重新分析就绪！共重新解析 %d 行日志，命中知识库 %d 条，发现 %d 个 RCA 根因事件",
			taskInfo.LogCount, taskInfo.MatchedCount, taskInfo.RcaCount))
	}

	return &taskInfo, nil
}
