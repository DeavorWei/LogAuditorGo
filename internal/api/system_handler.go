package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/internal/version"
	"logauditorgo/pkg/logger"
)

type SystemHandler struct {
	globalDB *gorm.DB
	indexer  *search.Indexer
}

// NewSystemHandler 构造系统处理器。
//
// 参数设计为可变参数：既有的无参调用（测试/工具）保持可用，
// 主程序传入全局库以提供数据量统计。
func NewSystemHandler(globalDB ...*gorm.DB) *SystemHandler {
	h := &SystemHandler{}
	if len(globalDB) > 0 {
		h.globalDB = globalDB[0]
	}
	return h
}

// SetIndexer 注入索引实例，供诊断包输出索引健康状态。
func (h *SystemHandler) SetIndexer(indexer *search.Indexer) {
	h.indexer = indexer
}

// GetConfig 获取当前系统配置与日志状态
func (h *SystemHandler) GetConfig(c *gin.Context) {
	if config.GlobalConfig == nil {
		ErrorResponse(c, http.StatusInternalServerError, 50001, "配置未初始化")
		return
	}

	stats := logger.GetLogStats()
	SuccessResponse(c, gin.H{
		"config":    config.GlobalConfig,
		"log_stats": stats,
	})
}

type UpdateLogConfigRequest struct {
	MaxSizeMB int    `json:"max_size_mb"`
	MaxDays   int    `json:"max_days"`
	Level     string `json:"level"`
	Format    string `json:"format"`
}

// UpdateLogConfig 动态更新日志保留配置并持久化
func (h *SystemHandler) UpdateLogConfig(c *gin.Context) {
	var req UpdateLogConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, 40001, "请求参数解析失败: "+err.Error())
		return
	}

	if req.MaxSizeMB < 1 {
		ErrorResponse(c, http.StatusBadRequest, 40002, "日志最大保留大小不能小于 1MB")
		return
	}
	if req.MaxDays < 1 {
		ErrorResponse(c, http.StatusBadRequest, 40003, "日志最大保留天数不能小于 1 天")
		return
	}

	if req.Level != "" {
		lvl := strings.ToLower(req.Level)
		if lvl != "debug" && lvl != "info" && lvl != "warn" && lvl != "warning" && lvl != "error" {
			ErrorResponse(c, http.StatusBadRequest, 40004, "无效的日志级别 (可选: debug, info, warn, error)")
			return
		}
		req.Level = lvl
	}

	if req.Format != "" {
		fmtStr := strings.ToLower(req.Format)
		if fmtStr != "console" && fmtStr != "json" {
			ErrorResponse(c, http.StatusBadRequest, 40005, "无效的日志格式 (可选: console, json)")
			return
		}
		req.Format = fmtStr
	}

	updated, err := config.UpdateLogConfig(req.MaxSizeMB, req.MaxDays, req.Level, req.Format)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, 50002, "更新日志配置失败: "+err.Error())
		return
	}

	stats := logger.GetLogStats()
	SuccessResponse(c, gin.H{
		"log_config": updated,
		"log_stats":  stats,
	}, "日志配置更新成功")
}

// GetLogs 获取日志文件统计与文件列表
func (h *SystemHandler) GetLogs(c *gin.Context) {
	stats := logger.GetLogStats()
	SuccessResponse(c, stats)
}

// CleanLogs 手动触发一次过期日志清理
func (h *SystemHandler) CleanLogs(c *gin.Context) {
	logger.CleanOldLogs()
	stats := logger.GetLogStats()
	SuccessResponse(c, stats, "日志清理执行完成")
}

// GetFeatures 返回当前功能开关（RCA 实验开关等）。
func (h *SystemHandler) GetFeatures(c *gin.Context) {
	SuccessResponse(c, gin.H{
		"features": config.Features(),
		"version":  version.String,
	})
}

// UpdateFeatures 更新功能开关并持久化。
//
// 仅接受已知且允许的开关；"浏览器上传 / LAN 多用户 / 脱敏"属于产品明确不做项，
// 传入会返回 400 而不是静默忽略，避免造成"已开启"的误解。
func (h *SystemHandler) UpdateFeatures(c *gin.Context) {
	var body map[string]bool
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorResponse(c, http.StatusBadRequest, 40001, "请求参数解析失败: "+err.Error())
		return
	}
	updated, err := config.UpdateFeatures(body)
	if err != nil {
		status := http.StatusInternalServerError
		code := 50003
		if strings.Contains(err.Error(), config.ErrFeatureNotImplemented.Error()) {
			status = http.StatusBadRequest
			code = 40006
		}
		ErrorResponse(c, status, code, err.Error())
		return
	}
	SuccessResponse(c, gin.H{"features": updated}, "功能开关已更新")
}

// storagePathUsage 单个数据路径的占用情况。
type storagePathUsage struct {
	Key   string `json:"key"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// GetStorage 返回存储概览与运行环境信息，供设置页"数据与存储"分区展示。
//
// 统计口径：目录递归求和（跳过符号链接与非普通文件），文件不存在时计 0，
// 不因某个目录缺失而让整个接口失败。
func (h *SystemHandler) GetStorage(c *gin.Context) {
	if config.GlobalConfig == nil {
		ErrorResponse(c, http.StatusInternalServerError, 50001, "配置未初始化")
		return
	}
	cfg := config.GlobalConfig

	dirs := []struct {
		key  string
		path string
	}{
		{"data", cfg.Storage.DataDir},
		{"knowledge_db", cfg.Storage.KnowledgeDB},
		{"bleve_index", cfg.Storage.BleveIndex},
		{"tasks", cfg.Storage.TaskDir},
		{"uploads", cfg.Storage.UploadDir},
		{"logs", cfg.Log.Dir},
	}

	usages := make([]storagePathUsage, 0, len(dirs))
	for _, d := range dirs {
		bytesUsed, files := pathUsage(d.path)
		usages = append(usages, storagePathUsage{Key: d.key, Path: d.path, Bytes: bytesUsed, Files: files})
	}

	counts := gin.H{"knowledge": 0, "documents": 0, "tasks": 0}
	if h.globalDB != nil {
		var knowledgeCount, documentCount, taskCount int64
		_ = h.globalDB.Model(&model.Knowledge{}).Count(&knowledgeCount).Error
		_ = h.globalDB.Model(&model.Document{}).Count(&documentCount).Error
		_ = h.globalDB.Model(&model.TaskInfo{}).Count(&taskCount).Error
		counts = gin.H{
			"knowledge": knowledgeCount,
			"documents": documentCount,
			"tasks":     taskCount,
		}
	}

	SuccessResponse(c, gin.H{
		"version":     version.String,
		"config_file": config.ConfigFileUsed,
		"features":    config.Features(),
		"paths":       usages,
		"counts":      counts,
	})
}

// pathUsage 统计路径占用；路径既可以是文件也可以是目录。
func pathUsage(path string) (int64, int) {
	if strings.TrimSpace(path) == "" {
		return 0, 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	if !info.IsDir() {
		return info.Size(), 1
	}

	var total int64
	files := 0
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			// 单个条目不可读不影响整体统计
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || !info.Mode().IsRegular() {
			return nil
		}
		total += info.Size()
		files++
		return nil
	})
	return total, files
}
