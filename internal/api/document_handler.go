package api

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/knowledge"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

type DocumentHandler struct {
	knowledgeSvc *knowledge.Service
	uploadDir    string
}

func NewDocumentHandler(knowledgeSvc *knowledge.Service, uploadDir string) *DocumentHandler {
	return &DocumentHandler{
		knowledgeSvc: knowledgeSvc,
		uploadDir:    uploadDir,
	}
}

// ImportDir 从本地目录导入 HDX 文档（支持全流程阶段进度实时追踪及异步模式）
func (h *DocumentHandler) ImportDir(c *gin.Context) {
	var req struct {
		DirPath      string `json:"dir_path" binding:"required"`
		ConflictMode string `json:"conflict_mode"`
		Async        bool   `json:"async"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request: "+err.Error())
		return
	}

	conflictMode := req.ConflictMode
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	isAsync := req.Async || c.Query("async") == "true" || c.GetHeader("X-Async") == "true"

	tracker := progress.GetHub().NewJob("hdx", "", knowledge.HDXImportStages)
	tracker.AddLog("info", "接收到本地目录导入请求: %s (策略: %s)", req.DirPath, conflictMode)

	if isAsync {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in HDX import: %v", r))
				}
			}()
			_, _ = h.knowledgeSvc.ImportDocumentFromDir(req.DirPath, conflictMode, tracker)
		}()

		SuccessResponse(c, gin.H{
			"job_id":   tracker.JobID(),
			"is_async": true,
		}, "HDX 文档导入任务已在后台启动")
		return
	}

	// 同步模式（兼容单测与同步请求）
	stats, err := h.knowledgeSvc.ImportDocumentFromDir(req.DirPath, conflictMode, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import failed: "+err.Error())
		return
	}

	SuccessResponse(c, stats, "Import completed successfully")
}

// UploadHDX 上传 HDX 压缩包或目录文件并自动解压导入（支持全流程阶段进度追踪及异步模式）
func (h *DocumentHandler) UploadHDX(c *gin.Context) {
	conflictMode := c.DefaultPostForm("conflict_mode", "overwrite")
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	isAsync := c.Query("async") == "true" || c.GetHeader("X-Async") == "true" || c.PostForm("async") == "true"

	form, err := c.MultipartForm()
	if err != nil || form == nil || len(form.File) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "At least one file is required")
		return
	}

	// 创建本次批量上传的隔离临时目录
	batchTempDir := filepath.Join(h.uploadDir, fmt.Sprintf("batch_upload_%d", time.Now().UnixNano()))
	if err := os.MkdirAll(batchTempDir, 0755); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Create temp dir failed: "+err.Error())
		return
	}

	fileCount := 0
	for _, fileHeaders := range form.File {
		for _, fh := range fileHeaders {
			relName := getOriginalFilename(fh)
			if relName == "" {
				relName = fh.Filename
			}
			if relName == "" {
				continue
			}

			targetPath := filepath.Join(batchTempDir, filepath.FromSlash(relName))
			// 路径安全防护（防越界 traversal）
			cleanDest := filepath.Clean(batchTempDir) + string(os.PathSeparator)
			if !strings.HasPrefix(filepath.Clean(targetPath)+string(os.PathSeparator), cleanDest) && filepath.Clean(targetPath) != filepath.Clean(batchTempDir) {
				targetPath = filepath.Join(batchTempDir, filepath.Base(relName))
			}

			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				continue
			}

			if err := c.SaveUploadedFile(fh, targetPath); err != nil {
				logger.Log.Warnf("[API Documents] Failed to save uploaded file %s: %v", fh.Filename, err)
				continue
			}
			fileCount++
		}
	}

	if fileCount == 0 {
		_ = os.RemoveAll(batchTempDir)
		ErrorResponse(c, http.StatusBadRequest, -1, "No valid files saved")
		return
	}

	tracker := progress.GetHub().NewJob("hdx", "", knowledge.HDXImportStages)
	tracker.SetStage("UPLOAD", fmt.Sprintf("已成功接收并保存 %d 个上传文件，准备解压...", fileCount))
	tracker.AddLog("info", "已接收 %d 个上传文件至临时工作目录", fileCount)

	if isAsync {
		// 异步模式：立即返回 job_id，后台 goroutine 执行
		go func() {
			defer func() {
				if r := recover(); r != nil {
					tracker.Fail(fmt.Errorf("panic in upload HDX process: %v", r))
				}
				time.Sleep(2 * time.Second)
				_ = os.RemoveAll(batchTempDir)
			}()

			tracker.AddLog("info", "开始检查并解压所有 HDX 官方压缩包...")
			if err := hdx.ExtractAllArchivesWithTracker(batchTempDir, tracker); err != nil {
				logger.Log.Warnf("[API Documents] Extract HDX archives warning: %v", err)
				tracker.AddLog("warning", "解压警告: %v", err)
			}

			_, _ = h.knowledgeSvc.ImportDocumentFromDir(batchTempDir, conflictMode, tracker)
		}()

		SuccessResponse(c, gin.H{
			"job_id":     tracker.JobID(),
			"file_count": fileCount,
			"is_async":   true,
		}, "HDX 文档上传成功，后台导入已启动")
		return
	}

	// 同步模式（兼容旧单测和直接调用）
	defer os.RemoveAll(batchTempDir)

	if err := hdx.ExtractAllArchivesWithTracker(batchTempDir, tracker); err != nil {
		logger.Log.Warnf("[API Documents] Extract HDX archives warning: %v", err)
	}

	logger.Log.Debugf("[API Documents] Triggered UploadHDX with %d files in %s (conflictMode: %s)", fileCount, batchTempDir, conflictMode)

	stats, err := h.knowledgeSvc.ImportDocumentFromDir(batchTempDir, conflictMode, tracker)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import extracted documents failed: "+err.Error())
		return
	}

	SuccessResponse(c, stats, "Upload and import completed successfully")
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
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, nil, "Document deleted successfully")
}

// getOriginalFilename 从 Content-Disposition 请求头中提取保留了目录层级的原始相对路径
func getOriginalFilename(fh *multipart.FileHeader) string {
	cd := fh.Header.Get("Content-Disposition")
	if cd != "" {
		if idx := strings.Index(cd, "filename="); idx != -1 {
			rest := strings.TrimSpace(cd[idx+len("filename="):])
			if strings.HasPrefix(rest, `"`) {
				rest = strings.TrimPrefix(rest, `"`)
				if endQuote := strings.Index(rest, `"`); endQuote != -1 {
					return rest[:endQuote]
				}
			} else if endSemi := strings.Index(rest, ";"); endSemi != -1 {
				return strings.TrimSpace(rest[:endSemi])
			} else {
				return strings.TrimSpace(rest)
			}
		}
	}
	return fh.Filename
}
