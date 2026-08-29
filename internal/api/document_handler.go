package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/fsx"
	"logauditorgo/internal/knowledge"
	"logauditorgo/pkg/progress"
)

// maxImportPaths 单次导入允许的最大路径数量
const maxImportPaths = 100

type DocumentHandler struct {
	knowledgeSvc *knowledge.Service
}

func NewDocumentHandler(knowledgeSvc *knowledge.Service) *DocumentHandler {
	return &DocumentHandler{
		knowledgeSvc: knowledgeSvc,
	}
}

// ImportDir 从服务端本地的一个或多个目录导入 HDX 文档
// 支持全流程阶段进度实时追踪及异步模式，浏览器只传递路径字符串，不传输文件内容
func (h *DocumentHandler) ImportDir(c *gin.Context) {
	var req struct {
		DirPath      string   `json:"dir_path"`
		Paths        []string `json:"paths"`
		ConflictMode string   `json:"conflict_mode"`
		Async        bool     `json:"async"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request: "+err.Error())
		return
	}

	conflictMode := req.ConflictMode
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	targets := make([]string, 0, len(req.Paths)+1)
	if p := strings.TrimSpace(req.DirPath); p != "" {
		targets = append(targets, p)
	}
	for _, p := range req.Paths {
		if p = strings.TrimSpace(p); p != "" {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "dir_path or paths is required")
		return
	}
	if len(targets) > maxImportPaths {
		ErrorResponse(c, http.StatusBadRequest, -1, fmt.Sprintf("too many paths, limit is %d", maxImportPaths))
		return
	}

	// 预检路径，避免进入导入流程后才发现路径不可用
	var invalid []string
	for _, e := range fsx.Stat(targets) {
		if !e.Exists {
			invalid = append(invalid, e.Path)
		}
	}
	if len(invalid) > 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "以下路径不存在或无法访问: "+strings.Join(invalid, ", "))
		return
	}

	isAsync := req.Async || c.Query("async") == "true" || c.GetHeader("X-Async") == "true"

	tracker := progress.GetHub().NewJob("hdx", "", knowledge.HDXImportStages)
	tracker.AddLog("info", "接收到本地目录导入请求: 共 %d 个路径 (策略: %s)", len(targets), conflictMode)

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in HDX import: %v", r))
				}
			}()
			_, _ = h.knowledgeSvc.ImportDocumentsFromPaths(targets, conflictMode, tracker)
		}()

		SuccessResponse(c, gin.H{
			"job_id":     tracker.JobID(),
			"path_count": len(targets),
			"is_async":   true,
		}, "HDX 文档导入任务已在后台启动")
		return
	}

	// 同步模式（兼容单测与同步请求）
	stats, err := h.knowledgeSvc.ImportDocumentsFromPaths(targets, conflictMode, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import failed: "+err.Error())
		return
	}

	SuccessResponse(c, stats, "Import completed successfully")
}

// ListDocuments 获取已导入文档列表
func (h *DocumentHandler) ListDocuments(c *gin.Context) {
	docs, err := h.knowledgeSvc.GetDocumentList()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, docs)
}

// DeleteDocument 删除指定文档
func (h *DocumentHandler) DeleteDocument(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid document ID")
		return
	}

	if err := h.knowledgeSvc.DeleteDocument(uint(id)); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Delete document failed: "+err.Error())
		return
	}

	SuccessResponse(c, nil, "Document deleted successfully")
}
