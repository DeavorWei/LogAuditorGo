package api

import (
	"archive/zip"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/pkg/logger"
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

// ImportDir 从本地目录导入 HDX 文档
func (h *DocumentHandler) ImportDir(c *gin.Context) {
	var req struct {
		DirPath      string `json:"dir_path" binding:"required"`
		ConflictMode string `json:"conflict_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request: "+err.Error())
		return
	}

	conflictMode := req.ConflictMode
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

	logger.Log.Debugf("[API Documents] Triggered ImportDir from: %s (conflictMode: %s)", req.DirPath, conflictMode)
	stats, err := h.knowledgeSvc.ImportDocumentFromDir(req.DirPath, conflictMode)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import failed: "+err.Error())
		return
	}

	SuccessResponse(c, stats, "Import completed successfully")
}

// UploadHDX 上传 HDX 压缩包或目录文件并自动解压导入（支持批量多文件、目录上传及冲突策略）
func (h *DocumentHandler) UploadHDX(c *gin.Context) {
	conflictMode := c.DefaultPostForm("conflict_mode", "overwrite")
	if conflictMode == "" {
		conflictMode = "overwrite"
	}

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
	defer os.RemoveAll(batchTempDir)

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
		ErrorResponse(c, http.StatusBadRequest, -1, "No valid files saved")
		return
	}

	// 递归解压所有 .hdx 压缩包（已解压的目录保持原样，直接由 FindHDXDocDirs 发现）
	if err := extractAllHDXArchives(batchTempDir); err != nil {
		logger.Log.Warnf("[API Documents] Extract HDX archives warning: %v", err)
	}

	logger.Log.Debugf("[API Documents] Triggered UploadHDX with %d files in %s (conflictMode: %s)", fileCount, batchTempDir, conflictMode)

	stats, err := h.knowledgeSvc.ImportDocumentFromDir(batchTempDir, conflictMode)
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

// extractAllHDXArchives 递归查找并解压目录下所有的 .hdx 压缩文件
func extractAllHDXArchives(dir string) error {
	var hdxFiles []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".hdx" {
			hdxFiles = append(hdxFiles, p)
		}
		return nil
	})

	for _, h := range hdxFiles {
		destDir := filepath.Join(filepath.Dir(h), "extracted_"+strings.TrimSuffix(filepath.Base(h), filepath.Ext(h)))
		if err := unzipFile(h, destDir); err != nil {
			logger.Log.Warnf("[API Documents] Failed to unzip HDX archive %s: %v", h, err)
			continue
		}
		_ = os.Remove(h)
	}
	return nil
}

func unzipFile(src string, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(fpath)+string(os.PathSeparator), filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		err = func() error {
			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			defer outFile.Close()

			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			if _, err := io.Copy(outFile, rc); err != nil {
				logger.Log.Warnf("unzip copy error for %s: %v", f.Name, err)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
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
