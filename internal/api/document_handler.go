package api

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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
		DirPath string `json:"dir_path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request: "+err.Error())
		return
	}

	logger.Log.Debugf("[API Documents] Triggered ImportDir from: %s", req.DirPath)
	stats, err := h.knowledgeSvc.ImportDocumentFromDir(req.DirPath)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import failed: "+err.Error())
		return
	}

	SuccessResponse(c, stats, "Import completed successfully")
}

// UploadHDX 上传 HDX 压缩包并自动解压导入
func (h *DocumentHandler) UploadHDX(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "File upload required")
		return
	}

	zipPath := filepath.Join(h.uploadDir, file.Filename)
	if err := c.SaveUploadedFile(file, zipPath); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Save uploaded file failed: "+err.Error())
		return
	}
	defer os.Remove(zipPath)

	// 解压到临时目录
	extractDir := filepath.Join(h.uploadDir, "extracted_"+filepath.Base(file.Filename))
	if err := unzipFile(zipPath, extractDir); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Unzip HDX failed: "+err.Error())
		return
	}
	defer os.RemoveAll(extractDir)

	stats, err := h.knowledgeSvc.ImportDocumentFromDir(extractDir)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Import extracted document failed: "+err.Error())
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

	SuccessResponse(c, docs,)
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
		if !filepath.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
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
