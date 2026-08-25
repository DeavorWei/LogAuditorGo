package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

type TaskHandler struct {
	taskSvc      *task.Service
	knowledgeSvc *knowledge.Service
}

func NewTaskHandler(taskSvc *task.Service, knowledgeSvc *knowledge.Service) *TaskHandler {
	return &TaskHandler{
		taskSvc:      taskSvc,
		knowledgeSvc: knowledgeSvc,
	}
}

// CreateTask 创建日志审计任务（支持空任务创建、多文件上传或文本直接提交，支持全流程阶段进度实时追踪）
func (h *TaskHandler) CreateTask(c *gin.Context) {
	taskName := c.PostForm("task_name")
	deviceType := c.PostForm("device_type")
	logContent := c.PostForm("content")
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	var items []task.FileUploadItem

	// 检查多文件上传
	form, err := c.MultipartForm()
	if err == nil && form != nil {
		for _, fileHeaders := range form.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}
				items = append(items, task.FileUploadItem{
					FileName: fh.Filename,
					FileSize: fh.Size,
					Content:  string(data),
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
	conflictMode := c.DefaultPostForm("conflict_mode", "overwrite")
	if conflictMode == "" {
		conflictMode = "overwrite"
	}
	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	var items []task.FileUploadItem

	// 检查多文件上传
	form, err := c.MultipartForm()
	if err == nil && form != nil {
		for _, fileHeaders := range form.File {
			for _, fh := range fileHeaders {
				f, err := fh.Open()
				if err != nil {
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					continue
				}
				items = append(items, task.FileUploadItem{
					FileName: fh.Filename,
					FileSize: fh.Size,
					Content:  string(data),
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

	filter := model.LogQueryFilter{
		Page:       page,
		PageSize:   pageSize,
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

	// 批量补充关联的知识库详情（优化 N+1 查询）
	type EnrichedRecord struct {
		model.LogRecord
		Knowledge *model.Knowledge `json:"knowledge,omitempty"`
	}

	uniqueKIDs := make([]uint, 0)
	kidSet := make(map[uint]bool)
	for _, rec := range records {
		if rec.KnowledgeID > 0 && !kidSet[rec.KnowledgeID] {
			kidSet[rec.KnowledgeID] = true
			uniqueKIDs = append(uniqueKIDs, rec.KnowledgeID)
		}
	}

	knowledgeMap := make(map[uint]*model.Knowledge)
	for _, kid := range uniqueKIDs {
		if k, err := h.knowledgeSvc.GetKnowledgeByID(kid); err == nil && k != nil {
			knowledgeMap[kid] = k
		}
	}

	var enrichedList []EnrichedRecord
	for _, rec := range records {
		er := EnrichedRecord{LogRecord: rec}
		if rec.KnowledgeID > 0 {
			er.Knowledge = knowledgeMap[rec.KnowledgeID]
		}
		enrichedList = append(enrichedList, er)
	}

	SuccessResponse(c, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"records":   enrichedList,
	})
}

// GetRCA 获取任务 RCA 事件
func (h *TaskHandler) GetRCA(c *gin.Context) {
	taskID := c.Param("id")
	events, err := h.taskSvc.GetTaskRCAEvents(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, events)
}

// ExportReport 导出分析报告
func (h *TaskHandler) ExportReport(c *gin.Context) {
	taskID := c.Param("id")
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

	c.JSON(http.StatusOK, gin.H{
		"task":    t,
		"records": records,
		"rcas":    rcas,
	})
}

// DeleteTask 删除任务
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	taskID := c.Param("id")
	if err := h.taskSvc.DeleteTask(taskID); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, nil, "Task deleted successfully")
}
