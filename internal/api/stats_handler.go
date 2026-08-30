package api

import (
	"fmt"
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

// globalStatsHandler 全局统计处理器引用。
//
// ARCH-11: 原实现把 handler 只存在 SetupRouter 的局部变量里，
// InvalidateCache() 全项目仅测试调用，导入/删任务后仪表盘最多陈旧 15s。
// 这里暴露一个包级引用与便捷失效函数，让业务层在数据变更后能主动失效缓存。
var globalStatsHandler *StatsHandler

// InvalidateStatsCache 主动失效系统统计缓存（可在任何业务变更后调用）
func InvalidateStatsCache() {
	if globalStatsHandler != nil {
		globalStatsHandler.InvalidateCache()
	}
}

// GetSystemStats 获取全局系统概览与仪表盘统计（带 15s 内存缓存优化）
func (h *StatsHandler) GetSystemStats(c *gin.Context) {
	h.cacheMu.RLock()
	if h.cacheData != nil && time.Now().Before(h.cacheUntil) {
		data := *h.cacheData
		h.cacheMu.RUnlock()
		// ARCH-10: 统一走 SuccessResponse，保证 message 字段始终存在
		SuccessResponse(c, data)
		return
		}
	h.cacheMu.RUnlock()

	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()

	// 二次检查缓存
	if h.cacheData != nil && time.Now().Before(h.cacheUntil) {
		SuccessResponse(c, *h.cacheData)
		return
	}

	// 12.1: 统计类查询的错误不再静默丢弃。
	// 原实现全部 `Count(&x)` 不接 Error，DB 故障时仪表盘会展示一份全是 0 的"正常"数据，
	// 比直接报错更危险——用户会以为知识库是空的。
	var totalKnowledge int64
	var totalDocs int64
	var totalTasks int64
	var totalLogsAnalyzed int64

	var qErr error
	collect := func(err error) {
		if err != nil && qErr == nil {
			qErr = err
		}
	}
	collect(h.db.Model(&model.Knowledge{}).Count(&totalKnowledge).Error)
	collect(h.db.Model(&model.Document{}).Count(&totalDocs).Error)
	collect(h.db.Model(&model.TaskInfo{}).Count(&totalTasks).Error)

	type LogSum struct {
		Sum int64
	}
	var sumRes LogSum
	collect(h.db.Model(&model.TaskInfo{}).Select("COALESCE(SUM(log_count), 0) as sum").Scan(&sumRes).Error)
	totalLogsAnalyzed = sumRes.Sum

	// 统计各模块知识数量 Top 10
	var topModules []ModuleStat
	collect(h.db.Model(&model.Knowledge{}).
		Select("module, count(*) as count").
		Group("module").
		Order("count desc").
		Limit(10).
		Scan(&topModules).Error)
	if topModules == nil {
		topModules = make([]ModuleStat, 0)
	}

	if qErr != nil {
		ErrorResponse(c, 500, -1, fmt.Sprintf("load system stats failed: %v", qErr))
		return
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

	SuccessResponse(c, *statsData)
}
