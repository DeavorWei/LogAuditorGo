package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
)

// parseUintParam 解析正整数路径参数。
func parseUintParam(c *gin.Context, name string) (uint, bool) {
	raw := c.Param(name)
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid "+name)
		return 0, false
	}
	return uint(value), true
}

// ListLocalKnowledge 列出本地知识。
func (h *KnowledgeHandler) ListLocalKnowledge(c *gin.Context) {
	items, err := h.knowledgeSvc.ListLocalKnowledge(c.Query("keyword"))
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, items)
}

// CreateLocalKnowledge 新建本地知识。
func (h *KnowledgeHandler) CreateLocalKnowledge(c *gin.Context) {
	var input knowledge.LocalKnowledgeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}
	entity, err := h.knowledgeSvc.UpsertLocalKnowledge(input)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, entity, "本地知识已保存")
}

// UpdateLocalKnowledge 更新指定本地知识。
func (h *KnowledgeHandler) UpdateLocalKnowledge(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	var input knowledge.LocalKnowledgeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}
	entity, err := h.knowledgeSvc.UpdateLocalKnowledge(id, input)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, entity, "本地知识已更新")
}

// DeleteLocalKnowledge 删除本地知识。
func (h *KnowledgeHandler) DeleteLocalKnowledge(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := h.knowledgeSvc.DeleteLocalKnowledge(id); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "本地知识已删除")
}

// ImportLocalCSV 通过 CSV 文本批量导入本地知识。
func (h *KnowledgeHandler) ImportLocalCSV(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		ErrorResponse(c, http.StatusBadRequest, -1, "CSV content is empty")
		return
	}
	result, err := h.knowledgeSvc.ImportLocalCSV(body.Content)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, result, "本地知识导入完成")
}

// ExportLocalCSV 导出本地知识 CSV。
func (h *KnowledgeHandler) ExportLocalCSV(c *gin.Context) {
	content, err := h.knowledgeSvc.ExportLocalCSV()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", attachmentHeader("本地知识", "", "csv"))
	c.String(http.StatusOK, content)
}

// LocalOverrideView 覆盖关系 + 目标知识摘要。
type LocalOverrideView struct {
	model.KnowledgeOverride
	TargetModule      string `json:"target_module"`
	TargetBrief       string `json:"target_brief"`
	TargetDescription string `json:"target_description"`
}

// ListOverrides 列出本地覆盖（带目标知识摘要）。
func (h *KnowledgeHandler) ListOverrides(c *gin.Context) {
	overrides, err := h.knowledgeSvc.ListOverrides()
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	ids := make([]uint, 0, len(overrides))
	for _, item := range overrides {
		ids = append(ids, item.KnowledgeID)
	}
	knowledgeMap, _ := h.knowledgeSvc.GetKnowledgeMapByIDs(ids)

	views := make([]LocalOverrideView, 0, len(overrides))
	for _, item := range overrides {
		view := LocalOverrideView{KnowledgeOverride: item}
		if kb, ok := knowledgeMap[item.KnowledgeID]; ok && kb != nil {
			view.TargetModule = kb.Module
			view.TargetBrief = kb.Brief
			view.TargetDescription = kb.Description
		}
		views = append(views, view)
	}
	SuccessResponse(c, views)
}

// CreateOverride 新建/更新本地覆盖。
func (h *KnowledgeHandler) CreateOverride(c *gin.Context) {
	var body struct {
		Module      string `json:"module"`
		Brief       string `json:"brief"`
		KnowledgeID uint   `json:"knowledge_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}
	override, err := h.knowledgeSvc.CreateOverride(body.Module, body.Brief, body.KnowledgeID)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, override, "匹配覆盖已保存")
}

// UpdateOverride 启用/禁用覆盖。
func (h *KnowledgeHandler) UpdateOverride(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "enabled is required")
		return
	}
	if err := h.knowledgeSvc.SetOverrideEnabled(id, *body.Enabled); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "匹配覆盖已更新")
}

// DeleteOverride 删除覆盖。
func (h *KnowledgeHandler) DeleteOverride(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if err := h.knowledgeSvc.DeleteOverride(id); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, nil, "匹配覆盖已删除")
}

// GlobalUnmatchedHotspots 跨任务未匹配热点聚合。
func (h *KnowledgeHandler) GlobalUnmatchedHotspots(c *gin.Context) {
	if h.taskSvc == nil {
		ErrorResponse(c, http.StatusServiceUnavailable, -1, "task service unavailable")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, digestCount, err := h.taskSvc.GetGlobalUnmatchedHotspots(limit)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, gin.H{
		"items":        items,
		"digest_count": digestCount,
	})
}
