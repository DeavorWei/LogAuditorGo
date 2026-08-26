package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"logauditorgo/internal/model"
)

type ModuleStat struct {
	Module string `json:"module"`
	Count  int64  `json:"count"`
}

type SystemStatsData struct {
	TotalKnowledge    int64        `json:"total_knowledge"`
	TotalDocuments    int64        `json:"total_documents"`
	TotalTasks        int64        `json:"total_tasks"`
	TotalLogsAnalyzed int64        `json:"total_logs_analyzed"`
	TopModules        []ModuleStat `json:"top_modules"`
}

type StatsHandler struct {
	db         *gorm.DB
	cacheMu    sync.RWMutex
	cacheData  *SystemStatsData
	cacheUntil time.Time
	cacheTTL   time.Duration
}

func NewStatsHandler(db *gorm.DB) *StatsHandler {
	return &StatsHandler{
		db:       db,
		cacheTTL: 15 * time.Second,
	}
}

// InvalidateCache 清空统计缓存
func (h *StatsHandler) InvalidateCache() {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	h.cacheData = nil
	h.cacheUntil = time.Time{}
}

// GetSystemStats 获取全局系统概览与仪表盘统计（带 15s 内存缓存优化）
func (h *StatsHandler) GetSystemStats(c *gin.Context) {
	h.cacheMu.RLock()
	if h.cacheData != nil && time.Now().Before(h.cacheUntil) {
		data := *h.cacheData
		h.cacheMu.RUnlock()
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": data,
		})
		return
	}
	h.cacheMu.RUnlock()

	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()

	// 二次检查缓存
	if h.cacheData != nil && time.Now().Before(h.cacheUntil) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": *h.cacheData,
		})
		return
	}

	var totalKnowledge int64
	var totalDocs int64
	var totalTasks int64
	var totalLogsAnalyzed int64

	h.db.Model(&model.Knowledge{}).Count(&totalKnowledge)
	h.db.Model(&model.Document{}).Count(&totalDocs)
	h.db.Model(&model.TaskInfo{}).Count(&totalTasks)

	type LogSum struct {
		Sum int64
	}
	var sumRes LogSum
	h.db.Model(&model.TaskInfo{}).Select("COALESCE(SUM(log_count), 0) as sum").Scan(&sumRes)
	totalLogsAnalyzed = sumRes.Sum

	// 统计各模块知识数量 Top 10
	var topModules []ModuleStat
	h.db.Model(&model.Knowledge{}).
		Select("module, count(*) as count").
		Group("module").
		Order("count desc").
		Limit(10).
		Scan(&topModules)
	if topModules == nil {
		topModules = make([]ModuleStat, 0)
	}

	statsData := &SystemStatsData{
		TotalKnowledge:    totalKnowledge,
		TotalDocuments:    totalDocs,
		TotalTasks:        totalTasks,
		TotalLogsAnalyzed: totalLogsAnalyzed,
		TopModules:        topModules,
	}

	h.cacheData = statsData
	h.cacheUntil = time.Now().Add(h.cacheTTL)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": *statsData,
	})
}
