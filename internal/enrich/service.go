package enrich

import (
	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
)

// KnowledgeResolver 知识库批量查询能力的最小抽象。
// 由 internal/knowledge.Service 实现，enrich 层不反向依赖具体实现，
// 便于单测时用内存 map 直接替换。
type KnowledgeResolver interface {
	GetKnowledgeMapByIDs(ids []uint) (map[uint]*model.Knowledge, error)
}

// Record 包含日志、知识库与融合参数的完整记录实体
type Record struct {
	model.LogRecord
	Knowledge          *model.Knowledge         `json:"knowledge,omitempty"`
	ContextualizedKB   *ContextualizedKnowledge `json:"contextualized_knowledge,omitempty"`
	EnrichedParameters []EnrichedParameter      `json:"enriched_parameters,omitempty"`
	RenderedMessage    string                   `json:"rendered_message,omitempty"`
}

// Service 日志富化服务
//
// ARCH-12: 原先这段编排逻辑写在 HTTP handler 里，
// 导致"同一个任务在 Web 上看到的富化结果"与"导出的报告"可能不一致，
// 且任何非 HTTP 出口（CLI、批量导出）都无法复用。
type Service struct {
	resolver KnowledgeResolver
}

// NewService 创建富化服务，resolver 允许为 nil（退化为不带知识库的富化）
func NewService(resolver KnowledgeResolver) *Service {
	return &Service{resolver: resolver}
}

// EnrichLogs 对一批日志记录执行富化：
// 1. 批量解析出命中的知识库实体（一次 IN 查询，绝不在循环内逐条查库）
// 2. 生成中文事件摘要
// 3. 融合参数字典与上下文化处置指导
func (s *Service) EnrichLogs(records []model.LogRecord) []Record {
	uniqueKIDs := make([]uint, 0)
	kidSet := make(map[uint]bool)
	for _, rec := range records {
		if rec.KnowledgeID > 0 && !kidSet[rec.KnowledgeID] {
			kidSet[rec.KnowledgeID] = true
			uniqueKIDs = append(uniqueKIDs, rec.KnowledgeID)
		}
	}

	var knowledgeMap map[uint]*model.Knowledge
	if len(uniqueKIDs) > 0 && s.resolver != nil {
		knowledgeMap, _ = s.resolver.GetKnowledgeMapByIDs(uniqueKIDs)
	}

	enrichedList := make([]Record, 0, len(records))
	for _, rec := range records {
		er := Record{LogRecord: rec}
		rawParams := ParseParametersJSON(rec.ParametersJSON)

		var kb *model.Knowledge
		if rec.KnowledgeID > 0 && knowledgeMap != nil {
			kb = knowledgeMap[rec.KnowledgeID]
		}
		er.EventSummary = summaryFor(rec, rawParams, kb)

		if kb != nil {
			er.Knowledge = kb
			er.EnrichedParameters = EnrichParameters(rec.ParametersJSON, kb)
			er.ContextualizedKB = ContextualizeKnowledge(kb, rec.ParametersJSON)
			if kb.Message != "" {
				er.RenderedMessage = RenderMessageTemplate(kb.Message, rawParams)
			}
		} else if rec.ParametersJSON != "" {
			er.EnrichedParameters = EnrichParameters(rec.ParametersJSON, nil)
		}
		enrichedList = append(enrichedList, er)
	}
	return enrichedList
}

func summaryFor(rec model.LogRecord, rawParams map[string]string, kb *model.Knowledge) string {
	return summary.GenerateSummary(rec.Module, rec.Brief, rec.Severity, rec.MessageBody, rawParams, kb)
}
