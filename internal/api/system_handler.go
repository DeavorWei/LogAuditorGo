package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/config"
	"logauditorgo/pkg/logger"
)

type SystemHandler struct{}

func NewSystemHandler() *SystemHandler {
	return &SystemHandler{}
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
