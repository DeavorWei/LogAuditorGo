package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
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
	uploadDir    string
}

func NewTaskHandler(taskSvc *task.Service, knowledgeSvc *knowledge.Service, uploadDir ...string) *TaskHandler {
	dir := ""
	if len(uploadDir) > 0 {
		dir = uploadDir[0]
	}
	return &TaskHandler{
		taskSvc:      taskSvc,
		knowledgeSvc: knowledgeSvc,
		uploadDir:    dir,
	}
}

func (h *TaskHandler) getUploadDir() string {
	if h.uploadDir != "" {
		return h.uploadDir
	}
	return os.TempDir()
}

// CreateTask 创建日志审计任务（支持空任务创建、多文件上传或文本直接提交，支持全流程阶段进度实时追踪）
func (h *TaskHandler) CreateTask(c *gin.Context) {
	taskName := c.PostForm("task_name")
	deviceType := c.PostForm("device_type")
	logContent := c.PostForm("content")
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	var items []task.FileUploadItem

	// 检查多文件上传（采用流式落盘到 uploadDir，避免全量读入内存导致 OOM）
	form, err := c.MultipartForm()
	if err == nil && form != nil {
		uploadDir := h.getUploadDir()
		_ = os.MkdirAll(uploadDir, 0755)

		for _, fileHeaders := range form.File {
			for _, fh := range fileHeaders {
				cleanBase := filepath.Base(fh.Filename)
				if cleanBase == "" || cleanBase == "." || cleanBase == "/" || cleanBase == "\\" {
					cleanBase = "uploaded_log.txt"
				}
				tempPath := filepath.Join(uploadDir, fmt.Sprintf("task_upload_%d_%s", time.Now().UnixNano(), cleanBase))
				if err := c.SaveUploadedFile(fh, tempPath); err != nil {
					// 降级使用 fh.Open() 直接传递流
					f, err := fh.Open()
					if err != nil {
						continue
					}
					items = append(items, task.FileUploadItem{
						FileName: fh.Filename,
						FileSize: fh.Size,
						Reader:   f,
					})
					continue
				}
				items = append(items, task.FileUploadItem{
					FileName: fh.Filename,
					FileSize: fh.Size,
					FilePath: tempPath,
					TempFile: true,
				})
			}
		}
	}

	if logContent != "" {
		items = append(items, task.FileUploadItem{
			FileName: "manual_input.txt",
			FileSize: int64(len(logContent)),
			Content:  logContent,
		})
	}

	// 支持 JSON 格式提交
	if len(items) == 0 && taskName == "" && deviceType == "" {
		var req struct {
			TaskName   string `json:"task_name"`
			DeviceType string `json:"device_type"`
			Content    string `json:"content"`
			Async      bool   `json:"async"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			taskName = req.TaskName
			deviceType = req.DeviceType
			if req.Async {
				isAsync = true
			}
			if req.Content != "" {
				items = append(items, task.FileUploadItem{
					FileName: "manual_input.txt",
					FileSize: int64(len(req.Content)),
					Content:  req.Content,
				})
			}
		}
	}

	if taskName == "" && len(items) > 0 {
		taskName = items[0].FileName
	}

	// 1. 如果没有上传文件也没有日志文本，创建空任务 (PENDING)
	if len(items) == 0 {
		taskInfo, err := h.taskSvc.CreateEmptyTask(taskName, deviceType)
		if err != nil {
			ErrorResponse(c, http.StatusInternalServerError, -1, "Create empty task failed: "+err.Error())
			return
		}
		SuccessResponse(c, taskInfo, "Empty task created successfully")
		return
	}

	// 2. 如果包含日志，先创建任务再启动分析
	taskInfo, err := h.taskSvc.CreateEmptyTask(taskName, deviceType)
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

// ImportLogs 导入/补充导入日志文件（支持多文件上传或文本提交，支持覆盖/跳过冲突模式，支持进度追踪及异步模式）
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

	var items []task.FileUploadItem

	// 检查多文件上传（采用流式落盘到 uploadDir，避免全量读入内存导致 OOM）
	form, err := c.MultipartForm()
	if err == nil && form != nil {
		uploadDir := h.getUploadDir()
		_ = os.MkdirAll(uploadDir, 0755)

		for _, fileHeaders := range form.File {
			for _, fh := range fileHeaders {
				cleanBase := filepath.Base(fh.Filename)
				if cleanBase == "" || cleanBase == "." || cleanBase == "/" || cleanBase == "\\" {
					cleanBase = "uploaded_log.txt"
				}
				tempPath := filepath.Join(uploadDir, fmt.Sprintf("task_upload_%d_%s", time.Now().UnixNano(), cleanBase))
				if err := c.SaveUploadedFile(fh, tempPath); err != nil {
					// 降级使用 fh.Open() 直接传递流
					f, err := fh.Open()
					if err != nil {
						continue
					}
					items = append(items, task.FileUploadItem{
						FileName: fh.Filename,
						FileSize: fh.Size,
						Reader:   f,
					})
					continue
				}
				items = append(items, task.FileUploadItem{
					FileName: fh.Filename,
					FileSize: fh.Size,
					FilePath: tempPath,
					TempFile: true,
				})
			}
		}
	}

	if textContent := c.PostForm("content"); textContent != "" {
		fileName := c.DefaultPostForm("file_name", "manual_input.txt")
		items = append(items, task.FileUploadItem{
			FileName: fileName,
			FileSize: int64(len(textContent)),
			Content:  textContent,
		})
	}

	if len(items) == 0 {
		var req struct {
			Content      string `json:"content"`
			FileName     string `json:"file_name"`
			ConflictMode string `json:"conflict_mode"`
			Async        bool   `json:"async"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Content != "" {
			fileName := req.FileName
			if fileName == "" {
				fileName = "manual_input.txt"
			}
			if req.ConflictMode != "" {
				conflictMode = req.ConflictMode
			}
			if req.Async {
				isAsync = true
			}
			items = append(items, task.FileUploadItem{
				FileName: fileName,
				FileSize: int64(len(req.Content)),
				Content:  req.Content,
			})
		}
	}

	if len(items) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "No log files or content provided")
		return
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

// EnrichedRecord 包含日志、知识库与融合参数的完整记录实体
type EnrichedRecord struct {
	model.LogRecord
	Knowledge          *model.Knowledge         `json:"knowledge,omitempty"`
	ContextualizedKB   *ContextualizedKnowledge `json:"contextualized_knowledge,omitempty"`
	EnrichedParameters []EnrichedParameter      `json:"enriched_parameters,omitempty"`
	RenderedMessage    string                   `json:"rendered_message,omitempty"`
}

func (h *TaskHandler) enrichRecords(records []model.LogRecord) []EnrichedRecord {
	uniqueKIDs := make([]uint, 0)
	kidSet := make(map[uint]bool)
	for _, rec := range records {
		if rec.KnowledgeID > 0 && !kidSet[rec.KnowledgeID] {
			kidSet[rec.KnowledgeID] = true
			uniqueKIDs = append(uniqueKIDs, rec.KnowledgeID)
		}
	}

	var knowledgeMap map[uint]*model.Knowledge
	if len(uniqueKIDs) > 0 && h.knowledgeSvc != nil {
		knowledgeMap, _ = h.knowledgeSvc.GetKnowledgeMapByIDs(uniqueKIDs)
	}

	enrichedList := make([]EnrichedRecord, 0, len(records))
	for _, rec := range records {
		er := EnrichedRecord{LogRecord: rec}
		rawParams := ParseParametersJSON(rec.ParametersJSON)
		er.EventSummary = summary.GenerateSummary(rec.Module, rec.Brief, rec.Severity, rec.MessageBody, rawParams)

		if rec.KnowledgeID > 0 && knowledgeMap != nil {
			kb := knowledgeMap[rec.KnowledgeID]
			er.Knowledge = kb
			er.EnrichedParameters = EnrichParameters(rec.ParametersJSON, kb)
			er.ContextualizedKB = ContextualizeKnowledge(kb, rec.ParametersJSON)
			if kb != nil && kb.Message != "" {
				er.RenderedMessage = RenderMessageTemplate(kb.Message, rawParams)
			}
		} else if rec.ParametersJSON != "" {
			er.EnrichedParameters = EnrichParameters(rec.ParametersJSON, nil)
		}
		enrichedList = append(enrichedList, er)
	}
	return enrichedList
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

	// JSON format export (PageSize: -1 导出全量日志)
	t, _ := h.taskSvc.GetTaskByID(taskID)
	records, _, _ := h.taskSvc.QueryTaskLogs(taskID, model.LogQueryFilter{PageSize: -1})
	rcas, _ := h.taskSvc.GetTaskRCAEvents(taskID)
	enrichedRecords := h.enrichRecords(records)

	c.JSON(http.StatusOK, gin.H{
		"task":    t,
		"records": enrichedRecords,
		"rcas":    rcas,
	})
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

// CreateDevice 在任务中创建新设备
func (h *TaskHandler) CreateDevice(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var req model.Device
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request JSON: "+err.Error())
		return
	}

	dev, err := h.taskSvc.CreateDevice(taskID, &req)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, dev, "Device created successfully")
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

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request JSON: "+err.Error())
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

// ImportLogsToDevice 向指定设备导入日志文件或文本
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

	var items []task.FileUploadItem

	form, err := c.MultipartForm()
	if err == nil && form != nil {
		uploadDir := h.getUploadDir()
		_ = os.MkdirAll(uploadDir, 0755)

		for _, fileHeaders := range form.File {
			for _, fh := range fileHeaders {
				cleanBase := filepath.Base(fh.Filename)
				if cleanBase == "" || cleanBase == "." || cleanBase == "/" || cleanBase == "\\" {
					cleanBase = "uploaded_log.txt"
				}
				tempPath := filepath.Join(uploadDir, fmt.Sprintf("task_upload_%d_%s", time.Now().UnixNano(), cleanBase))
				if err := c.SaveUploadedFile(fh, tempPath); err != nil {
					f, err := fh.Open()
					if err != nil {
						continue
					}
					items = append(items, task.FileUploadItem{
						FileName: fh.Filename,
						FileSize: fh.Size,
						Reader:   f,
					})
					continue
				}
				items = append(items, task.FileUploadItem{
					FileName: fh.Filename,
					FileSize: fh.Size,
					FilePath: tempPath,
					TempFile: true,
				})
			}
		}
	}

	if textContent := c.PostForm("content"); textContent != "" {
		fileName := c.DefaultPostForm("file_name", "manual_input.txt")
		items = append(items, task.FileUploadItem{
			FileName: fileName,
			FileSize: int64(len(textContent)),
			Content:  textContent,
		})
	}

	if len(items) == 0 {
		var req struct {
			Content      string `json:"content"`
			FileName     string `json:"file_name"`
			ConflictMode string `json:"conflict_mode"`
			Async        bool   `json:"async"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Content != "" {
			fileName := req.FileName
			if fileName == "" {
				fileName = "manual_input.txt"
			}
			if req.ConflictMode != "" {
				conflictMode = req.ConflictMode
			}
			if req.Async {
				isAsync = true
			}
			items = append(items, task.FileUploadItem{
				FileName: fileName,
				FileSize: int64(len(req.Content)),
				Content:  req.Content,
			})
		}
	}

	if len(items) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "No log files or content provided")
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

	var filter model.MultiDeviceLogFilter
	_ = c.ShouldBindJSON(&filter)

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
	_ = c.ShouldBindJSON(&req)

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

