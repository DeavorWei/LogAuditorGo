package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"logauditorgo/internal/enrich"
	"logauditorgo/internal/fsx"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

var taskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)

func isValidTaskID(taskID string) bool {
	return taskIDRegex.MatchString(taskID)
}

type TaskHandler struct {
	taskSvc      *task.Service
	knowledgeSvc *knowledge.Service
	// enricher 富化服务 (ARCH-12)。handler 只做参数绑定与响应封装，
	// 具体的"日志 × 知识库"融合编排由 enrich.Service 承载，供导出等非 HTTP 出口复用。
	enricher *enrich.Service
}

func NewTaskHandler(taskSvc *task.Service, knowledgeSvc *knowledge.Service) *TaskHandler {
	var resolver enrich.KnowledgeResolver
	if knowledgeSvc != nil {
		resolver = knowledgeSvc
	}
	return &TaskHandler{
		taskSvc:      taskSvc,
		knowledgeSvc: knowledgeSvc,
		enricher:     enrich.NewService(resolver),
	}
}

// logImportRequest 日志导入请求的统一入参（JSON 与表单双通道）
type logImportRequest struct {
	TaskName   string `json:"task_name"`
	DeviceType string `json:"device_type"`
	// PARSE-04: 设备软件版本（如 V200R024C00），透传给匹配引擎做版本分档打分
	DeviceVersion string `json:"device_version"`

	// 服务端本地路径模式
	Paths     []string `json:"paths"`
	Exts      []string `json:"exts"`
	Recursive bool     `json:"recursive"`

	// 文本直接提交模式
	Content  string `json:"content"`
	FileName string `json:"file_name"`

	ConflictMode string `json:"conflict_mode"`
	Async        bool   `json:"async"`
}

// bindLogImportRequest 统一解析日志导入请求。
// 使用 ShouldBindBodyWith 绑定 JSON，使请求体可被重复绑定而不会丢失；
// 表单字段作为兜底补充，保证 curl / x-www-form-urlencoded 依旧可用。
func bindLogImportRequest(c *gin.Context) logImportRequest {
	var req logImportRequest
	req.Recursive = true

	_ = c.ShouldBindBodyWith(&req, binding.JSON)

	if v := c.PostForm("task_name"); v != "" {
		req.TaskName = v
	}
	if v := c.PostForm("device_type"); v != "" {
		req.DeviceType = v
	}
	if v := c.PostForm("device_version"); v != "" {
		req.DeviceVersion = v
	}
	if v := c.PostForm("conflict_mode"); v != "" {
		req.ConflictMode = v
	}
	if v := c.PostForm("content"); v != "" {
		req.Content = v
	}
	if v := c.PostForm("file_name"); v != "" {
		req.FileName = v
	}
	if c.PostForm("async") == "true" {
		req.Async = true
	}
	if v := c.PostForm("paths"); v != "" {
		req.Paths = splitMultiValue(v)
	}
	if v := c.PostForm("exts"); v != "" {
		req.Exts = splitMultiValue(v)
	}

	return req
}

// splitMultiValue 拆分逗号、分号或换行分隔的多个值
func splitMultiValue(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})
	values := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			values = append(values, p)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// buildPathItems 将服务端本地路径展开为待导入文件项
// 与上传模式的关键区别：这些文件原本就在用户磁盘上，导入完成后绝不能删除（TempFile=false）
func buildPathItems(paths []string, exts []string, recursive bool) ([]task.FileUploadItem, error) {
	entries, _, truncated := fsx.CollectFiles(paths, recursive, exts, fsx.DefaultMaxFiles)
	if len(entries) == 0 {
		return nil, fmt.Errorf("no matching log files found in the given paths")
	}
	if truncated {
		logger.Log.Warnf("[API Tasks] Path import truncated: reached the limit of %d files", fsx.DefaultMaxFiles)
	}

	items := make([]task.FileUploadItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, task.FileUploadItem{
			FileName: e.Name,
			FileSize: e.Size,
			FilePath: e.Path,
			TempFile: false,
		})
	}
	return items, nil
}

// CreateTask 创建日志审计任务
// 日志来源支持两种：服务端本地路径（推荐，超大目录也不会消耗浏览器资源）与直接粘贴的文本
func (h *TaskHandler) CreateTask(c *gin.Context) {
	taskName := c.PostForm("task_name")
	deviceType := c.PostForm("device_type")
	deviceVersion := c.PostForm("device_version")
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	req := bindLogImportRequest(c)
	if req.TaskName != "" {
		taskName = req.TaskName
	}
	if req.DeviceType != "" {
		deviceType = req.DeviceType
	}
	if req.DeviceVersion != "" {
		deviceVersion = req.DeviceVersion
	}
	if req.Async {
		isAsync = true
	}

	var items []task.FileUploadItem

	// 1. 服务端本地路径模式：由服务端进程直接读取磁盘，浏览器只传递路径字符串
	if len(req.Paths) > 0 {
		// ARCH-02: 根目录白名单校验，必须发生在真正读取磁盘之前
		if !guardPaths(c, req.Paths) {
			return
		}
		pathItems, err := buildPathItems(req.Paths, req.Exts, req.Recursive)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
			return
		}
		items = append(items, pathItems...)
	}

	// 2. 文本直接提交模式
	if req.Content != "" {
		fileName := req.FileName
		if fileName == "" {
			fileName = "manual_input.txt"
		}
		items = append(items, task.FileUploadItem{
			FileName: fileName,
			FileSize: int64(len(req.Content)),
			Content:  req.Content,
		})
	}

	if taskName == "" && len(items) == 1 {
		taskName = items[0].FileName
	}
	if taskName == "" && len(items) > 1 {
		taskName = fmt.Sprintf("%s 等 %d 个日志文件", items[0].FileName, len(items))
	}

	// 1. 如果没有上传文件也没有日志文本，创建空任务 (PENDING)
	if len(items) == 0 {
		taskInfo, err := h.taskSvc.CreateEmptyTask(taskName, deviceType, deviceVersion)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, -1, "Create empty task failed: "+err.Error())
			return
		}
		SuccessResponse(c, taskInfo, "Empty task created successfully")
		return
	}

	// 2. 如果包含日志，先创建任务再启动分析
	taskInfo, err := h.taskSvc.CreateEmptyTask(taskName, deviceType, deviceVersion)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Create task failed: "+err.Error())
		return
	}

	tracker := progress.GetHub().RegisterJob(taskInfo.TaskID, taskInfo.TaskID, "log", task.LogAuditStages)
	tracker.AddLog("info", "任务 '%s' 创建成功 (ID: %s)，已接收 %d 个日志待分析", taskName, taskInfo.TaskID, len(items))

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in task analysis: %v", r))
				}
			}()
			_, _ = h.taskSvc.ImportLogs(taskInfo.TaskID, items, "overwrite", tracker)
		}()

		SuccessResponse(c, gin.H{
			"task_id":  taskInfo.TaskID,
			"task":     taskInfo,
			"job_id":   tracker.JobID(),
			"is_async": true,
		}, "Task created and analysis started")
		return
	}

	// 同步模式
	updatedInfo, err := h.taskSvc.ImportLogs(taskInfo.TaskID, items, "overwrite", tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Run task failed: "+err.Error())
		return
	}

	SuccessResponse(c, updatedInfo, "Task created and analyzed successfully")
}

// ImportLogs 导入/补充导入日志文件（支持服务端本地路径或文本提交，支持覆盖/跳过冲突模式，支持进度追踪及异步模式）
func (h *TaskHandler) ImportLogs(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	conflictMode := c.DefaultPostForm("conflict_mode", "rename")
	if conflictMode == "" {
		conflictMode = "rename"
	}
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	req := bindLogImportRequest(c)
	if req.ConflictMode != "" {
		conflictMode = req.ConflictMode
	}
	if req.Async {
		isAsync = true
	}

	var items []task.FileUploadItem

	// 1. 服务端本地路径模式
	if len(req.Paths) > 0 {
		pathItems, err := buildPathItems(req.Paths, req.Exts, req.Recursive)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
			return
		}
		items = append(items, pathItems...)
	}

	// 2. 文本直接提交模式
	if req.Content != "" {
		fileName := req.FileName
		if fileName == "" {
			fileName = "manual_input.txt"
		}
		items = append(items, task.FileUploadItem{
			FileName: fileName,
			FileSize: int64(len(req.Content)),
			Content:  req.Content,
		})
	}

	if len(items) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "No log paths or content provided")
		return
	}

	// PARSE-04: 补充导入时允许用户补录/修正设备版本，
	// 必须在启动导入前回写，否则本次匹配仍会沿用旧版本分档。
	if req.DeviceVersion != "" {
		if err := h.taskSvc.SetTaskDeviceVersion(taskID, req.DeviceVersion); err != nil {
			logger.Log.Warnf("[API Tasks] Update device_version for task %s failed: %v", taskID, err)
		}
	}

	logger.Log.Debugf("[API Tasks] Importing %d items into task %s (conflictMode: %s)", len(items), taskID, conflictMode)

	tracker := progress.GetHub().NewJob("log", taskID, task.LogAuditStages)
	tracker.AddLog("info", "开始向任务 %s 导入 %d 个日志文件 (策略: %s)", taskID, len(items), conflictMode)

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in import logs: %v", r))
				}
			}()
			_, _ = h.taskSvc.ImportLogs(taskID, items, conflictMode, tracker)
		}()

		SuccessResponse(c, gin.H{
			"task_id":  taskID,
			"job_id":   tracker.JobID(),
			"is_async": true,
		}, "Log import and analysis job started")
		return
	}

	// 同步模式
	taskInfo, err := h.taskSvc.ImportLogs(taskID, items, conflictMode, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import logs failed: "+err.Error())
		return
	}

	SuccessResponse(c, taskInfo, "Logs imported and analyzed successfully")
}

// GetTaskFiles 获取指定任务已上传的文件清单
func (h *TaskHandler) GetTaskFiles(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	files, err := h.taskSvc.GetTaskFiles(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, files)
}

// ListTasks 获取所有任务
func (h *TaskHandler) ListTasks(c *gin.Context) {
	tasks, err := h.taskSvc.GetTaskList()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, tasks)
}

// GetTask 获取任务元信息
func (h *TaskHandler) GetTask(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	t, err := h.taskSvc.GetTaskByID(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Task not found")
		return
	}

	SuccessResponse(c, t)
}

// QueryLogs 查询任务内日志并分页
func (h *TaskHandler) QueryLogs(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	module := c.Query("module")
	brief := c.Query("brief")
	hostname := c.Query("hostname")
	sourceFile := c.Query("source_file")
	keyword := c.Query("keyword")

	var sevPtr *int
	if sStr := c.Query("severity"); sStr != "" {
		if s, err := strconv.Atoi(sStr); err == nil {
			sevPtr = &s
		}
	}

	var matchedPtr *bool
	if mStr := c.Query("matched"); mStr != "" {
		m := (mStr == "true" || mStr == "1")
		matchedPtr = &m
	}

	var timeStart *time.Time
	if tsStr := c.Query("time_start"); tsStr != "" {
		if ts, err := time.Parse("2006-01-02 15:04:05", tsStr); err == nil {
			timeStart = &ts
		}
	}

	var timeEnd *time.Time
	if teStr := c.Query("time_end"); teStr != "" {
		if te, err := time.Parse("2006-01-02 15:04:05", teStr); err == nil {
			timeEnd = &te
		}
	}

	var devIDPtr *uint
	if dStr := c.Query("device_id"); dStr != "" {
		if d, err := strconv.ParseUint(dStr, 10, 32); err == nil {
			ud := uint(d)
			devIDPtr = &ud
		}
	}

	filter := model.LogQueryFilter{
		Page:       page,
		PageSize:   pageSize,
		DeviceID:   devIDPtr,
		Module:     module,
		Severity:   sevPtr,
		Brief:      brief,
		Hostname:   hostname,
		SourceFile: sourceFile,
		Keyword:    keyword,
		Matched:    matchedPtr,
		TimeStart:  timeStart,
		TimeEnd:    timeEnd,
	}

	records, total, err := h.taskSvc.QueryTaskLogs(taskID, filter)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	enrichedList := h.enrichRecords(records)

	SuccessResponse(c, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"records":   enrichedList,
	})
}

// GetRCA 获取任务 RCA 事件 (返回包含根因与级联时序日志元数据的完整实体)
func (h *TaskHandler) GetRCA(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	events, err := h.taskSvc.GetEnrichedRCAEvents(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, events)
}

// ExportReport 导出分析报告
func (h *TaskHandler) ExportReport(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	format := c.DefaultQuery("format", "html")

	if format == "html" {
		htmlContent, err := h.taskSvc.ExportTaskHTML(taskID)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
			return
		}
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Content-Disposition", "attachment; filename=report_"+taskID+".html")
		c.String(http.StatusOK, htmlContent)
		return
	}

	// CSV 导出：真流式逐行下发，内存占用恒定 (ARCH-07 / TASK-09)
	if format == "csv" {
		h.streamCSVExport(c, taskID)
		return
	}

	// JSON format export
	//
	// REANA-12 / ARCH-07: 原实现 `t, _ := ...; records, _, _ := ...; rcas, _ := ...`
	// 三个错误全部丢弃，导出失败时静默返回残缺报告。这里全部判空并返回 500。
	// 同时 RCA 改用 GetEnrichedRCAEvents，与列表接口保持同一字段形态。
	t, err := h.taskSvc.GetTaskByID(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Load task failed: "+err.Error())
		return
	}
	records, _, err := h.taskSvc.QueryTaskLogs(taskID, model.LogQueryFilter{PageSize: 0})
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Load log records failed: "+err.Error())
		return
	}
	rcas, err := h.taskSvc.GetEnrichedRCAEvents(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Load RCA events failed: "+err.Error())
		return
	}
	enrichedRecords := h.enrichRecords(records)

	c.JSON(http.StatusOK, gin.H{
		"task":    t,
		"records": enrichedRecords,
		"rcas":    rcas,
	})
}

// streamCSVExport 以数据库游标逐行生成 CSV 并流式下发 (ARCH-07)。
//
// 与 JSON 导出的区别：JSON 需要完整的数组结构，只能整体序列化；
// CSV 天然是行式格式，配合 http.Flusher 可以做到"查一行、写一行、刷一行"，
// 百万行导出的内存占用与单条日志同阶。
func (h *TaskHandler) streamCSVExport(c *gin.Context, taskID string) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=logs_"+taskID+".csv")

	// UTF-8 BOM：保证 Excel 打开中文不乱码
	if _, err := c.Writer.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return
	}
	if _, err := c.Writer.WriteString("ID,时间,级别,模块,助记符,主机名,来源文件,匹配层级,置信度,原始报文\n"); err != nil {
		return
	}

	err := h.taskSvc.StreamTaskLogs(taskID, model.LogQueryFilter{}, func(rec model.LogRecord) error {
		row := buildCSVRow(rec)
		if _, err := c.Writer.WriteString(row); err != nil {
			return err
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		// 流已开始，无法再改响应头，只能追加一条错误行供调用方识别
		logger.Log.Errorf("[API Tasks] stream CSV export for task %s failed: %v", taskID, err)
		_, _ = c.Writer.WriteString("\n# EXPORT_ABORTED: " + strings.ReplaceAll(err.Error(), "\n", " ") + "\n")
	}
}

// buildCSVRow 把单条日志序列化为 CSV 行。
// 对引号、换行与公式注入前缀做防护，避免导出的 CSV 被表格软件当作公式执行。
func buildCSVRow(rec model.LogRecord) string {
	fields := []string{
		strconv.FormatUint(uint64(rec.ID), 10),
		rec.Timestamp.Format("2006-01-02 15:04:05"),
		strconv.Itoa(rec.Severity),
		rec.Module,
		rec.Brief,
		rec.Hostname,
		rec.SourceFile,
		rec.MatchTier,
		strconv.FormatFloat(rec.MatchConfidence, 'f', 2, 64),
		rec.RawLog,
	}
	var sb strings.Builder
	for i, f := range fields {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(escapeCSVField(f))
	}
	sb.WriteByte('\n')
	return sb.String()
}

// escapeCSVField 转义 CSV 字段：包裹引号 + 双写内部引号，并防御公式注入
func escapeCSVField(v string) string {
	// 以 = + - @ 开头的字段会被 Excel/Sheets 当作公式求值，统一加前导单引号
	if len(v) > 0 && (v[0] == '=' || v[0] == '+' || v[0] == '-' || v[0] == '@') {
		v = "'" + v
	}
	if strings.ContainsAny(v, ",\"\n\r") {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}

// DeleteTask 删除任务
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	if err := h.taskSvc.DeleteTask(taskID); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, nil, "Task deleted successfully")
}

// updateDeviceRequest 设备更新的请求 DTO (ARCH-03)。
// 使用指针字段以区分"未传"与"传了零值"；未在结构体中声明的字段一律不接受，
// 从源头杜绝 id / task_id / log_count 等敏感列被客户端注入。
type updateDeviceRequest struct {
	DeviceName   *string `json:"device_name"`
	DeviceType   *string `json:"device_type"`
	ManagementIP *string `json:"management_ip"`
	Hostname     *string `json:"hostname"`
	Description  *string `json:"description"`
	Color        *string `json:"color"`
}

// toUpdateMap 将 DTO 转换为白名单更新集，只包含本次真正传入的字段
func (r updateDeviceRequest) toUpdateMap() map[string]interface{} {
	updates := make(map[string]interface{})
	if v := r.DeviceName; v != nil {
		updates["device_name"] = *v
	}
	if v := r.DeviceType; v != nil {
		updates["device_type"] = *v
	}
	if v := r.ManagementIP; v != nil {
		updates["management_ip"] = *v
	}
	if v := r.Hostname; v != nil {
		updates["hostname"] = *v
	}
	if v := r.Description; v != nil {
		updates["description"] = *v
	}
	if v := r.Color; v != nil {
		updates["color"] = *v
	}
	return updates
}

// createDeviceRequest 设备创建的请求 DTO (ARCH-03)。
// 原实现直接绑定 model.Device，客户端可预设 id / created_at 等字段。
type createDeviceRequest struct {
	DeviceName   string `json:"device_name"`
	DeviceType   string `json:"device_type"`
	ManagementIP string `json:"management_ip"`
	Hostname     string `json:"hostname"`
	Description  string `json:"description"`
	Color        string `json:"color"`
}

// CreateDevice 在任务中创建新设备
func (h *TaskHandler) CreateDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var req createDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request JSON: "+err.Error())
		return
	}

	// 只取白名单字段构造实体，忽略请求体中可能携带的 id / task_id / 计数类字段
	dev := &model.Device{
		DeviceName:   req.DeviceName,
		DeviceType:   req.DeviceType,
		ManagementIP: req.ManagementIP,
		Hostname:     req.Hostname,
		Description:  req.Description,
		Color:        req.Color,
	}

	created, err := h.taskSvc.CreateDevice(taskID, dev)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, created, "Device created successfully")
}

// ListDevices 获取指定任务下的所有设备列表
func (h *TaskHandler) ListDevices(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	devices, err := h.taskSvc.ListDevices(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, devices)
}

// GetDevice 获取单个设备详情
func (h *TaskHandler) GetDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	deviceID, err := strconv.ParseUint(c.Param("device_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid device ID")
		return
	}

	dev, err := h.taskSvc.GetDevice(taskID, uint(deviceID))
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, err.Error())
		return
	}

	SuccessResponse(c, dev)
}

// UpdateDevice 更新设备属性
func (h *TaskHandler) UpdateDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	deviceID, err := strconv.ParseUint(c.Param("device_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid device ID")
		return
	}

	// ARCH-03: 原实现直接把请求体绑定成 map[string]interface{} 透传给 GORM 的 Updates()，
	// 客户端可以注入 id / task_id / log_count / created_at 等任意列，
	// 实现"改主键、跨任务划转设备、伪造统计数字"的越权写入。
	// 这里在 API 边界做字段白名单，只放行业务可编辑字段。
	var req updateDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request JSON: "+err.Error())
		return
	}
	updates := req.toUpdateMap()
	if len(updates) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "No editable fields provided")
		return
	}

	dev, err := h.taskSvc.UpdateDevice(taskID, uint(deviceID), updates)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, dev, "Device updated successfully")
}

// DeleteDevice 删除设备
func (h *TaskHandler) DeleteDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	deviceID, err := strconv.ParseUint(c.Param("device_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid device ID")
		return
	}

	if err := h.taskSvc.DeleteDevice(taskID, uint(deviceID)); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, nil, "Device deleted successfully")
}

// ImportLogsToDevice 向指定设备导入日志（支持服务端本地路径或文本提交）
func (h *TaskHandler) ImportLogsToDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	deviceID, err := strconv.ParseUint(c.Param("device_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid device ID")
		return
	}

	conflictMode := c.DefaultPostForm("conflict_mode", "rename")
	if conflictMode == "" {
		conflictMode = "rename"
	}
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	req := bindLogImportRequest(c)
	if req.ConflictMode != "" {
		conflictMode = req.ConflictMode
	}
	if req.Async {
		isAsync = true
	}

	var items []task.FileUploadItem

	// 1. 服务端本地路径模式
	if len(req.Paths) > 0 {
		pathItems, err := buildPathItems(req.Paths, req.Exts, req.Recursive)
		if err != nil {
			ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
			return
		}
		items = append(items, pathItems...)
	}

	// 2. 文本直接提交模式
	if req.Content != "" {
		fileName := req.FileName
		if fileName == "" {
			fileName = "manual_input.txt"
		}
		items = append(items, task.FileUploadItem{
			FileName: fileName,
			FileSize: int64(len(req.Content)),
			Content:  req.Content,
		})
	}

	if len(items) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "No log paths or content provided")
		return
	}

	tracker := progress.GetHub().NewJob("log", taskID, task.LogAuditStages)
	tracker.AddLog("info", "开始向设备 (ID: %d) 导入 %d 个日志文件 (策略: %s)", deviceID, len(items), conflictMode)

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in import logs: %v", r))
				}
			}()
			_, _ = h.taskSvc.ImportLogsToDevice(taskID, uint(deviceID), items, conflictMode, tracker)
		}()

		SuccessResponse(c, gin.H{
			"task_id":   taskID,
			"device_id": deviceID,
			"job_id":    tracker.JobID(),
			"is_async":  true,
		}, "Device log import job started")
		return
	}

	taskInfo, err := h.taskSvc.ImportLogsToDevice(taskID, uint(deviceID), items, conflictMode, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import logs to device failed: "+err.Error())
		return
	}

	SuccessResponse(c, taskInfo, "Logs imported to device successfully")
}

// AutoAssignDevices 根据日志 Hostname 自动创建设备并关联绑定
func (h *TaskHandler) AutoAssignDevices(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	devices, err := h.taskSvc.AutoAssignDevices(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Auto assign devices failed: "+err.Error())
		return
	}

	SuccessResponse(c, devices, fmt.Sprintf("Successfully auto-assigned %d devices", len(devices)))
}

// QueryMultiDeviceLogs 多设备联合日志查询与时序筛选
func (h *TaskHandler) QueryMultiDeviceLogs(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var filter model.MultiDeviceLogFilter
	if err := c.ShouldBindJSON(&filter); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid filter JSON: "+err.Error())
		return
	}

	events, total, err := h.taskSvc.QueryMultiDeviceLogs(taskID, filter)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, gin.H{
		"total":     total,
		"page":      filter.Page,
		"page_size": filter.PageSize,
		"events":    events,
	})
}

// GetDeviceTimeline 获取多设备统一时间线数据
func (h *TaskHandler) GetDeviceTimeline(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	// ARCH-14: 原实现 `_ = c.ShouldBindJSON(&filter)` 把解析错误完全吞掉，
	// 非法 JSON 会被静默当作"空筛选条件"返回全量数据，掩盖客户端 bug。
	// 这里与同文件其他接口一致，解析失败返回 400。
	var filter model.MultiDeviceLogFilter
	if err := c.ShouldBindJSON(&filter); err != nil {
		// 时间线接口允许空 body（等价于"不带筛选条件"），只有非空且解析失败才报错
		if c.Request.ContentLength > 0 {
			ErrorResponse(c, http.StatusBadRequest, -1, "Invalid filter JSON: "+err.Error())
			return
		}
	}

	events, err := h.taskSvc.GetDeviceTimeline(taskID, filter)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, events)
}

// GetMultiDeviceReport 获取多设备协同诊断与对比报告数据
func (h *TaskHandler) GetMultiDeviceReport(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var req struct {
		DeviceIDs []uint `json:"device_ids"`
	}
	// ARCH-14: 同上，非空 body 解析失败必须返回 400 而不是静默降级为"全量设备"
	if err := c.ShouldBindJSON(&req); err != nil {
		if c.Request.ContentLength > 0 {
			ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request JSON: "+err.Error())
			return
		}
	}

	report, err := h.taskSvc.GetMultiDeviceReport(taskID, req.DeviceIDs)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, report)
}

// ExportMultiDeviceReport 导出多设备分析 HTML 离线报告
func (h *TaskHandler) ExportMultiDeviceReport(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var deviceIDs []uint
	if devStr := c.Query("device_ids"); devStr != "" {
		for _, s := range strings.Split(devStr, ",") {
			if id, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32); err == nil {
				deviceIDs = append(deviceIDs, uint(id))
			}
		}
	}

	format := c.DefaultQuery("format", "html")
	if format == "json" {
		report, err := h.taskSvc.GetMultiDeviceReport(taskID, deviceIDs)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
			return
		}
		c.JSON(http.StatusOK, report)
		return
	}

	htmlContent, err := h.taskSvc.ExportMultiDeviceHTML(taskID, deviceIDs)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=multi_device_report_%s.html", taskID))
	c.String(http.StatusOK, htmlContent)
}

// GetTaskModules 获取指定任务日志中实际出现的所有模块名称列表
func (h *TaskHandler) GetTaskModules(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	modules, err := h.taskSvc.GetDistinctModules(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, modules)
}

// ReanalyzeTask 基于任务已持久化的日志记录重新执行知识库匹配与 RCA 根因拓扑分析
func (h *TaskHandler) ReanalyzeTask(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"
	var req struct {
		Async bool `json:"async"`
	}
	if err := c.ShouldBindJSON(&req); err == nil && req.Async {
		isAsync = true
	}

	tracker := progress.GetHub().NewJob("log", taskID, task.LogAuditStages)
	tracker.AddLog("info", "启动任务 %s 的重新分析...", taskID)

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in reanalyze task: %v", r))
				}
			}()
			_, _ = h.taskSvc.ReanalyzeTask(taskID, tracker)
		}()

		SuccessResponse(c, gin.H{
			"task_id":  taskID,
			"job_id":   tracker.JobID(),
			"is_async": true,
		}, "Task reanalysis job started")
		return
	}

	taskInfo, err := h.taskSvc.ReanalyzeTask(taskID, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Reanalyze task failed: "+err.Error())
		return
	}

	SuccessResponse(c, taskInfo, "Task reanalyzed successfully")
}
