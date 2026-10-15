package enrich

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"logauditorgo/internal/matcher"
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

		mergedParamsJSON := rec.ParametersJSON
		if kb != nil && kb.Message != "" {
			captured := summary.CaptureTemplateParams(kb.Message, rec.MessageBody)
			if len(captured) > 0 {
				if rawParams == nil {
					rawParams = make(map[string]string, len(captured))
				}

				isWireless := matcher.IsWirelessLog(rec.MessageBody)
				existingNormKeys := make(map[string]struct{}, len(rawParams))
				for k := range rawParams {
					existingNormKeys[summary.NormalizeKey(k)] = struct{}{}
				}

				for ck, cv := range captured {
					// NormalizeKey 已做小写 + 去下划线/短横线/空格处理，后续前缀/正则均按小写口径
					normCK := summary.NormalizeKey(ck)

					// 1. 无线日志下阻断 USER_INFO_* 有线前导伪键（归一化后前缀为小写 userinfo）
					if isWireless && strings.HasPrefix(normCK, "userinfo") {
						continue
					}

					// 2. 占位符黑名单抑制：仅过滤模板泛化占位符（CHAR/STRING/INTEGER/OCTET），
					//    绝不覆盖官方字典真实字段（如 IPADDRESS / IPV6ADDRESS）
					if isPlaceholderPseudoKey(normCK) {
						continue
					}

					// 3. 保护现场原生解析结果不被模板捕获覆盖；同时借助规范化索引杜绝大小写重入
					if _, hasRaw := existingNormKeys[normCK]; !hasRaw {
						rawParams[ck] = cv
						existingNormKeys[normCK] = struct{}{}
					}
				}
				if sJSON, err := marshalSortedParamsJSON(rawParams); err == nil {
					mergedParamsJSON = sJSON
				}
			}
		}

		er.EventSummary = summaryFor(rec, rawParams, kb)

		if kb != nil {
			er.Knowledge = kb
			er.EnrichedParameters = EnrichParameters(mergedParamsJSON, kb)
			er.ContextualizedKB = ContextualizeKnowledge(kb, mergedParamsJSON)
			if kb.Message != "" {
				er.RenderedMessage = RenderMessageTemplate(kb.Message, rawParams)
			}
		} else if mergedParamsJSON != "" {
			er.EnrichedParameters = EnrichParameters(mergedParamsJSON, nil)
		}
		enrichedList = append(enrichedList, er)
	}
	return enrichedList
}

// marshalSortedParamsJSON 将参数 map 按键名升序排序后序列化为 JSON，杜绝键序随机造成的 diff 噪音 (Audit B13)
func marshalSortedParamsJSON(params map[string]string) (string, error) {
	if len(params) == 0 {
		return "{}", nil
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kB, _ := json.Marshal(k)
		vB, _ := json.Marshal(params[k])
		b.Write(kB)
		b.WriteByte(':')
		b.Write(vB)
	}
	b.WriteByte('}')
	return b.String(), nil
}

func summaryFor(rec model.LogRecord, rawParams map[string]string, kb *model.Knowledge) string {
	return summary.GenerateSummary(rec.Module, rec.Brief, rec.Severity, rec.MessageBody, rawParams, kb)
}

// placeholderPseudoKeyRegex 预编译占位符伪键正则（小写口径，匹配 NormalizeKey 之后的键名）
var placeholderPseudoKeyRegex = regexp.MustCompile(`^(char|string|integer|octet)\d*$`)

func isPlaceholderPseudoKey(normKey string) bool {
	return placeholderPseudoKeyRegex.MatchString(normKey)
}
