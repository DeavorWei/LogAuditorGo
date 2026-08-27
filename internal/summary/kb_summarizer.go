package summary

import (
	"fmt"
	"regexp"
	"strings"

	"logauditorgo/internal/model"
)

// 常见官方文档前缀口癖过滤正则
var kbPrefixRegex = regexp.MustCompile(`^(?:该(?:日志|告警|事件)(?:表示|用于记录|产生于|提示)?|此(?:告警|日志|事件)(?:表示|提示)?|本(?:日志|告警)(?:用于记录|表示)?|当.*?时(?:，|,)?(?:产生此告警|记录此日志)?)\s*`)

// CleanDescriptionTitle 从知识库 Description 中提取干净简明的中文核心含义
func CleanDescriptionTitle(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}

	// 1. 去除 HTML 标签（如果存在）
	if strings.Contains(desc, "<") && strings.Contains(desc, ">") {
		tagRegex := regexp.MustCompile(`<[^>]+>`)
		desc = tagRegex.ReplaceAllString(desc, "")
	}

	// 2. 取第一句（以句号、换行、分号截断）
	idx := strings.IndexAny(desc, "。\n;\r")
	firstSentence := desc
	if idx != -1 {
		firstSentence = strings.TrimSpace(desc[:idx])
	}

	// 如果第一句话过长（>80字符）或者里面包含“，可能”，在逗号处适度截断
	if idxComma := strings.Index(firstSentence, "，可能"); idxComma != -1 {
		firstSentence = firstSentence[:idxComma]
	} else if idxComma := strings.Index(firstSentence, "，由于"); idxComma != -1 {
		firstSentence = firstSentence[:idxComma]
	}

	// 3. 剥离前缀口癖
	firstSentence = kbPrefixRegex.ReplaceAllString(firstSentence, "")
	firstSentence = strings.TrimSpace(firstSentence)

	// 4. 长度限制，防止极端长句
	runes := []rune(firstSentence)
	if len(runes) > 60 {
		firstSentence = string(runes[:60]) + "..."
	}

	return firstSentence
}

// ExtractCoreContextParams 提取最重要的关键上下文参数标签（最多提取3个关键实体）
func ExtractCoreContextParams(normParams map[string]string) string {
	var parts []string

	// 1. 操作用户与命令（管理审计类）
	user := ResolveParam(normParams, "user")
	cmd := ResolveParam(normParams, "command")
	if user != "" && cmd != "" {
		return fmt.Sprintf("用户: %s, 执行: %s", user, cmd)
	}

	// 2. 实例 / VPN
	if inst := ResolveParam(normParams, "evpninstance"); inst != "" {
		parts = append(parts, "实例: "+inst)
	}

	// 3. MAC 地址
	if mac := ResolveParam(normParams, "mac"); mac != "" {
		parts = append(parts, "MAC: "+mac)
	}

	// 4. 接口 / 端口
	if iface := ResolveParam(normParams, "interface"); iface != "" {
		parts = append(parts, "接口: "+iface)
	}

	// 5. 对端 / 邻居 IP
	if peer := ResolveParam(normParams, "peer"); peer != "" {
		parts = append(parts, "对端: "+peer)
	} else if ip := ResolveParam(normParams, "ip"); ip != "" {
		parts = append(parts, "IP: "+ip)
	}

	// 6. 聚合组 / Trunk
	if trunk := ResolveParam(normParams, "trunk"); trunk != "" {
		parts = append(parts, "聚合: "+trunk)
	}

	// 7. 状态
	if state := ResolveParam(normParams, "state"); state != "" {
		parts = append(parts, "状态: "+state)
	}

	// 8. 错误/原因
	if reason := ResolveParam(normParams, "reason"); reason != "" {
		// 截短原因
		if len([]rune(reason)) > 30 {
			reason = string([]rune(reason)[:30]) + "..."
		}
		parts = append(parts, "原因: "+reason)
	}

	if len(parts) == 0 {
		return ""
	}

	// 最多保留前 3 项关键参数，保持摘要精炼
	if len(parts) > 3 {
		parts = parts[:3]
	}

	return strings.Join(parts, ", ")
}

// placeholderRegex 匹配消息模板中的占位符
var placeholderRegex = regexp.MustCompile(`(?:\[([a-zA-Z0-9_\-]+)\]|<([a-zA-Z0-9_\-]+)>|\{([a-zA-Z0-9_\-]+)\}|%([a-zA-Z0-9_\-]+)%|\$([a-zA-Z0-9_\-]+))`)

// RenderTemplateWithParams 将官方文档消息模板占位符替换为提取的真实值
func RenderTemplateWithParams(template string, rawParams map[string]string, normParams map[string]string) string {
	if strings.TrimSpace(template) == "" || len(rawParams) == 0 {
		return template
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

		// 精确匹配
		if val, ok := rawParams[keyName]; ok && val != "" && val != "-" {
			return val
		}
		// 规范化匹配
		normK := NormalizeKey(keyName)
		if val, ok := normParams[normK]; ok && val != "" && val != "-" {
			return val
		}
		// 别名同义词匹配
		for role, aliases := range AliasGroups {
			for _, alias := range aliases {
				if alias == normK {
					if v := ResolveParam(normParams, role); v != "" {
						return v
					}
				}
			}
		}

		return match
	})
}

// BuildKnowledgeSummary 基于官方知识库条目自动生成语义摘要
func BuildKnowledgeSummary(kb *model.Knowledge, normParams map[string]string, rawParams map[string]string) string {
	if kb == nil {
		return ""
	}

	// 1. 优先提取官方 Description 的核心中文含义
	coreDesc := CleanDescriptionTitle(kb.Description)
	coreParams := ExtractCoreContextParams(normParams)

	if coreDesc != "" {
		if coreParams != "" {
			return fmt.Sprintf("%s [%s]", coreDesc, coreParams)
		}
		return coreDesc
	}

	// 2. 若无 Description，使用已有的官方模板实例化
	if strings.TrimSpace(kb.Message) != "" {
		rendered := RenderTemplateWithParams(kb.Message, rawParams, normParams)
		// 截取前 120 字符
		if len([]rune(rendered)) > 120 {
			rendered = string([]rune(rendered)[:120]) + "..."
		}
		return rendered
	}

	return ""
}

// BuildAdaptiveGenericSummary 无知识库时的通用自适应摘要提取（完全不区分协议名）
func BuildAdaptiveGenericSummary(module, brief string, normParams map[string]string, rawParams map[string]string, rawMsg string) string {
	coreParams := ExtractCoreContextParams(normParams)
	if coreParams != "" {
		if module != "" && brief != "" {
			return fmt.Sprintf("[%s/%s] %s", module, brief, coreParams)
		}
		if module != "" {
			return fmt.Sprintf("[%s] %s", module, coreParams)
		}
		return coreParams
	}

	// 提取前 4 个任意键值对
	if topParams := ExtractTopParams(rawParams, 4); topParams != "" {
		if module != "" && brief != "" {
			return fmt.Sprintf("[%s/%s] %s", module, brief, topParams)
		}
		return topParams
	}

	// 截取原始报文
	cleanMsg := strings.TrimSpace(rawMsg)
	if cleanMsg != "" {
		cleanMsg = strings.ReplaceAll(cleanMsg, "\r", " ")
		cleanMsg = strings.ReplaceAll(cleanMsg, "\n", " ")
		if len([]rune(cleanMsg)) > 100 {
			cleanMsg = string([]rune(cleanMsg)[:100]) + "..."
		}
		if module != "" && brief != "" {
			return fmt.Sprintf("[%s/%s] %s", module, brief, cleanMsg)
		}
		return cleanMsg
	}

	if module != "" && brief != "" {
		return fmt.Sprintf("[%s/%s]", module, brief)
	}
	return "—"
}
