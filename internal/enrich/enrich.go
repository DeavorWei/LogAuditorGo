// Package enrich 承载"日志富化"业务编排：把原始日志记录与知识库实体融合，
// 产出可直接渲染的事件摘要、参数字典与上下文化处置指导。
//
// ARCH-12: 这段逻辑原本写在 internal/api/task_handler.go 的 enrichRecords 里，
// 导致 handler 同时依赖 model / summary / knowledge 三方，
// CLI、报告导出等其他出口想复用只能复制一份。
// 现在下沉为独立服务层，HTTP handler 只是它的一层薄薄适配器。
package enrich

import (
	"encoding/json"
	"strings"

	"logauditorgo/internal/model"
	"logauditorgo/internal/summary"
)

// placeholderRegex 引用 summary 包中的统一定义 (M-21)
var placeholderRegex = summary.PlaceholderRegex

// normalizeKey 引用 summary 包中的统一键名规范化 (M-21)
func normalizeKey(k string) string {
	return summary.NormalizeKey(k)
}

// EnrichedParameter 表示变量与其官方文档说明融合后的实体
type EnrichedParameter struct {
	Name        string `json:"name"`        // 参数名 (如 PeerID)
	Value       string `json:"value"`       // 提取的实际值 (如 192.168.1.1)
	Description string `json:"description"` // 官方文档说明 (如 对等体IP地址)
	Matched     bool   `json:"matched"`     // 是否成功关联官方文档说明
}

// ContextualizedKnowledge 上下文化后的知识库实体（将文档模板中的参数占位符替换为提取的真实值）
type ContextualizedKnowledge struct {
	Description string `json:"description"`
	Cause       string `json:"cause"`
	Action      string `json:"action"`
	Impact      string `json:"impact"`
}

// ParseParametersJSON 解析日志记录中的 ParametersJSON
func ParseParametersJSON(paramsJSON string) map[string]string {
	res := make(map[string]string)
	if strings.TrimSpace(paramsJSON) == "" {
		return res
	}
	_ = json.Unmarshal([]byte(paramsJSON), &res)
	return res
}

// GenerateEventSummary 从日志基本字段与 ParametersJSON 生成中文事件摘要
func GenerateEventSummary(module, brief string, severity int, rawMsg string, paramsJSON string, kb ...*model.Knowledge) string {
	params := ParseParametersJSON(paramsJSON)
	var k *model.Knowledge
	if len(kb) > 0 && kb[0] != nil {
		k = kb[0]
	}
	return summary.GenerateSummary(module, brief, severity, rawMsg, params, k)
}

// EnrichParameters 将日志提取的动态参数与知识库官方参数定义进行多层级匹配融合
func EnrichParameters(paramsJSON string, kb *model.Knowledge) []EnrichedParameter {
	rawParams := ParseParametersJSON(paramsJSON)
	if len(rawParams) == 0 {
		return []EnrichedParameter{}
	}

	exactMap := make(map[string]string)
	normMap := make(map[string]string)

	if kb != nil && strings.TrimSpace(kb.Parameters) != "" {
		var defs []model.ParameterItem
		if err := json.Unmarshal([]byte(kb.Parameters), &defs); err == nil {
			for _, d := range defs {
				name := strings.TrimSpace(d.Name)
				desc := strings.TrimSpace(d.Description)
				if name != "" {
					exactMap[name] = desc
					normMap[normalizeKey(name)] = desc
				}
			}
		}
	}

	result := make([]EnrichedParameter, 0, len(rawParams))
	for k, v := range rawParams {
		desc := ""
		matched := false

		if d, ok := exactMap[k]; ok && d != "" {
			desc = d
			matched = true
		} else if d, ok := normMap[normalizeKey(k)]; ok && d != "" {
			desc = d
			matched = true
		}

		result = append(result, EnrichedParameter{
			Name:        k,
			Value:       v,
			Description: desc,
			Matched:     matched,
		})
	}

	return result
}

// RenderMessageTemplate 将知识库官方消息模板中的占位符替换为提取的真实值
func RenderMessageTemplate(template string, rawParams map[string]string) string {
	if strings.TrimSpace(template) == "" || len(rawParams) == 0 {
		return template
	}

	normParams := make(map[string]string, len(rawParams))
	for k, v := range rawParams {
		normParams[normalizeKey(k)] = v
	}

	return placeholderRegex.ReplaceAllStringFunc(template, func(match string) string {
		submatches := placeholderRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		var keyName string
		for i := 1; i < len(submatches); i++ {
			if submatches[i] != "" {
				keyName = strings.TrimSpace(submatches[i])
				break
			}
		}

		if val, ok := rawParams[keyName]; ok {
			return val
		}
		if val, ok := normParams[normalizeKey(keyName)]; ok {
			return val
		}
		return match
	})
}

// ContextualizeText 将排查指导、可能原因等知识库文本中的参数占位符替换为实际值
func ContextualizeText(text string, rawParams map[string]string) string {
	if strings.TrimSpace(text) == "" || len(rawParams) == 0 {
		return text
	}
	return RenderMessageTemplate(text, rawParams)
}

// ContextualizeKnowledge 对官方知识库实体进行全局上下文参数注入
func ContextualizeKnowledge(kb *model.Knowledge, paramsJSON string) *ContextualizedKnowledge {
	if kb == nil {
		return nil
	}
	rawParams := ParseParametersJSON(paramsJSON)
	if len(rawParams) == 0 {
		return &ContextualizedKnowledge{
			Description: kb.Description,
			Cause:       kb.Cause,
			Action:      kb.Action,
			Impact:      kb.Impact,
		}
	}

	return &ContextualizedKnowledge{
		Description: ContextualizeText(kb.Description, rawParams),
		Cause:       ContextualizeText(kb.Cause, rawParams),
		Action:      ContextualizeText(kb.Action, rawParams),
		Impact:      ContextualizeText(kb.Impact, rawParams),
	}
}
