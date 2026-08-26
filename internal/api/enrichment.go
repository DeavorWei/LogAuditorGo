package api

import (
	"encoding/json"
	"regexp"
	"strings"

	"logauditorgo/internal/model"
)

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

// placeholderRegex 匹配日志模板或文本中的参数占位符，支持 [Param], <Param>, {Param}, %Param%, $Param
var placeholderRegex = regexp.MustCompile(`(?:\[([a-zA-Z0-9_\-]+)\]|<([a-zA-Z0-9_\-]+)>|\{([a-zA-Z0-9_\-]+)\}|%([a-zA-Z0-9_\-]+)%|\$([a-zA-Z0-9_\-]+))`)

// normalizeKey 规范化参数键名（忽略大小写、下划线、短横线与空格）
func normalizeKey(k string) string {
	s := strings.ToLower(k)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
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
				keyName = submatches[i]
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
