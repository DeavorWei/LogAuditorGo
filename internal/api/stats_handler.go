package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"logauditorgo/internal/model"
)

type StatsHandler struct {
	db *gorm.DB
}

func NewStatsHandler(db *gorm.DB) *StatsHandler {
	return &StatsHandler{db: db}
}

// GetSystemStats 获取全局系统概览与仪表盘统计
func (h *StatsHandler) GetSystemStats(c *gin.Context) {
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
	type ModuleStat struct {
		Module string `json:"module"`
		Count  int64  `json:"count"`
	}
	var topModules []ModuleStat
	h.db.Model(&model.Knowledge{}).
		Select("module, count(*) as count").
		Group("module").
		Order("count desc").
		Limit(10).
		Scan(&topModules)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total_knowledge":     totalKnowledge,
			"total_documents":     totalDocs,
			"total_tasks":         totalTasks,
			"total_logs_analyzed": totalLogsAnalyzed,
			"top_modules":         topModules,
		},
	})
}
