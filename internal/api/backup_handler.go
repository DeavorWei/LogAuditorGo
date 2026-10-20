package api

import (
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/backup"
	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/internal/version"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// backupOptions 汇总当前配置中的备份路径。
func (h *SystemHandler) backupOptions() backup.Options {
	cfg := config.GlobalConfig
	if cfg == nil {
		return backup.Options{}
	}
	return backup.Options{
		DataDir:     cfg.Storage.DataDir,
		KnowledgeDB: cfg.Storage.KnowledgeDB,
		BleveIndex:  cfg.Storage.BleveIndex,
		TaskDir:     cfg.Storage.TaskDir,
		LogDir:      cfg.Log.Dir,
		ConfigFile:  config.ConfigFileUsed,
		DB:          h.globalDB,
	}
}

// backupDataDir 返回数据目录；未初始化时返回空串，调用方按 500 处理。
func backupDataDir() string {
	if config.GlobalConfig == nil {
		return ""
	}
	return strings.TrimSpace(config.GlobalConfig.Storage.DataDir)
}

// CreateBackup 创建一键备份；长任务运行期间返回 409。
func (h *SystemHandler) CreateBackup(c *gin.Context) {
	if backupDataDir() == "" {
		ErrorResponse(c, http.StatusInternalServerError, -1, "配置未初始化，无法创建备份")
		return
	}
	if progress.GetHub().RunningCount() > 0 {
		ErrorResponse(c, http.StatusConflict, 40901, "有长任务正在运行，请等待完成后再备份（避免备份不一致）")
		return
	}
	info, err := backup.Create(h.backupOptions())
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "创建备份失败: "+err.Error())
		return
	}
	SuccessResponse(c, info, "备份已完成")
}

// ListBackups 列出备份。
func (h *SystemHandler) ListBackups(c *gin.Context) {
	dataDir := backupDataDir()
	if dataDir == "" {
		ErrorResponse(c, http.StatusInternalServerError, -1, "配置未初始化")
		return
	}
	items, err := backup.List(dataDir)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, items)
}

// DeleteBackup 删除备份。
func (h *SystemHandler) DeleteBackup(c *gin.Context) {
	dataDir := backupDataDir()
	if dataDir == "" {
		ErrorResponse(c, http.StatusInternalServerError, -1, "配置未初始化")
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	if err := backup.Delete(dataDir, name); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "备份已删除")
}

// DownloadBackup 下载备份文件。
func (h *SystemHandler) DownloadBackup(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	archivePath, err := backup.ArchivePath(backupDataDir(), name)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "备份不存在或格式非法")
		return
	}
	// 复用 backup 包的白名单校验：非法名称统一按 404 处理，避免暴露路径
	if err := backup.ValidateArchive(archivePath); err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "备份不存在或格式非法")
		return
	}
	c.FileAttachment(archivePath, name)
}

// RestoreBackup 准备恢复（下次启动生效）。
func (h *SystemHandler) RestoreBackup(c *gin.Context) {
	dataDir := backupDataDir()
	if dataDir == "" {
		ErrorResponse(c, http.StatusInternalServerError, -1, "配置未初始化")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		ErrorResponse(c, http.StatusBadRequest, -1, "name is required")
		return
	}
	if err := backup.PrepareRestore(dataDir, body.Name); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "恢复准备失败: "+err.Error())
		return
	}
	SuccessResponse(c, gin.H{"pending": true}, "恢复已准备，重启 LogAuditorGo 后生效")
}

// DownloadDiagnostics 下载诊断包（配置 + 最近日志 + 运行状态）。
func (h *SystemHandler) DownloadDiagnostics(c *gin.Context) {
	if backupDataDir() == "" {
		ErrorResponse(c, http.StatusInternalServerError, -1, "配置未初始化")
		return
	}
	opts := h.backupOptions()
	meta := gin.H{
		"app":         "LogAuditorGo",
		"version":     version.String,
		"go_version":  runtime.Version(),
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"export_time": time.Now().Format(time.RFC3339),
		"config_file": config.ConfigFileUsed,
	}
	if h.globalDB != nil {
		var knowledgeCount, taskCount, documentCount int64
		_ = h.globalDB.Model(&model.Knowledge{}).Count(&knowledgeCount).Error
		_ = h.globalDB.Model(&model.TaskInfo{}).Count(&taskCount).Error
		_ = h.globalDB.Model(&model.Document{}).Count(&documentCount).Error
		meta["counts"] = gin.H{"knowledge": knowledgeCount, "tasks": taskCount, "documents": documentCount}
	}
	if h.indexer != nil {
		docCount, _ := h.indexer.DocCount()
		meta["index"] = gin.H{
			"mapping_version":   search.IndexMappingVersion,
			"mapping_outdated":  h.indexer.IsMappingOutdated(),
			"indexed_documents": docCount,
		}
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", attachmentHeader("诊断包", "", "zip"))
	c.Header("Cache-Control", "no-store")
	if err := backup.WriteDiagnostics(c.Writer, opts, meta); err != nil {
		logger.Log.Errorf("[API System] write diagnostics failed: %v", err)
	}
}
