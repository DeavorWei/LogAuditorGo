package api

import (
	"logauditorgo/internal/enrich"
	"logauditorgo/internal/model"
)

// ARCH-12: 富化业务逻辑已下沉到 internal/enrich 服务层。
// 本文件只保留类型别名与转发函数，保证既有 API 契约与前端字段完全不变，
// 同时让 CLI / 报告导出等其他出口可以直接复用 enrich.Service。
type (
	// EnrichedParameter 富化后的参数实体
	EnrichedParameter = enrich.EnrichedParameter
	// ContextualizedKnowledge 上下文化后的知识库实体
	ContextualizedKnowledge = enrich.ContextualizedKnowledge
	// EnrichedRecord 富化后的完整日志记录
	EnrichedRecord = enrich.Record
)

// GenerateEventSummary 从日志基本字段与 ParametersJSON 生成中文事件摘要
func GenerateEventSummary(module, brief string, severity int, rawMsg string, paramsJSON string, kb ...*model.Knowledge) string {
	return enrich.GenerateEventSummary(module, brief, severity, rawMsg, paramsJSON, kb...)
}

// ParseParametersJSON 解析日志记录中的 ParametersJSON
func ParseParametersJSON(paramsJSON string) map[string]string {
	return enrich.ParseParametersJSON(paramsJSON)
}

// EnrichParameters 将日志提取的动态参数与知识库官方参数定义进行多层级匹配融合
func EnrichParameters(paramsJSON string, kb *model.Knowledge) []EnrichedParameter {
	return enrich.EnrichParameters(paramsJSON, kb)
}

// RenderMessageTemplate 将知识库官方消息模板中的占位符替换为提取的真实值
func RenderMessageTemplate(template string, rawParams map[string]string) string {
	return enrich.RenderMessageTemplate(template, rawParams)
}

// ContextualizeText 将排查指导、可能原因等知识库文本中的参数占位符替换为实际值
func ContextualizeText(text string, rawParams map[string]string) string {
	return enrich.ContextualizeText(text, rawParams)
}

// ContextualizeKnowledge 对官方知识库实体进行全局上下文参数注入
func ContextualizeKnowledge(kb *model.Knowledge, paramsJSON string) *ContextualizedKnowledge {
	return enrich.ContextualizeKnowledge(kb, paramsJSON)
}

// enrichRecords 对日志记录执行富化（委托给 enrich.Service）
func (h *TaskHandler) enrichRecords(records []model.LogRecord) []EnrichedRecord {
	return h.enricher.EnrichLogs(records)
}
