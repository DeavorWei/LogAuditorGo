package summary

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"logauditorgo/internal/model"
)

// 常见官方文档前缀口癖过滤正则
var kbPrefixRegex = regexp.MustCompile(`^(?:该(?:日志|告警|事件)(?:表示|用于记录|产生于|提示)?|此(?:告警|日志|事件)(?:表示|提示)?|本(?:日志|告警)(?:用于记录|表示)?|当.*?时(?:，|,)?(?:产生此告警|记录此日志)?)\s*`)

// htmlTagRegex 剥离 HTML 标签。
//
// RCA-10: 该正则原先写在 CleanDescriptionTitle 函数体内，
// 每条日志生成摘要都要重新编译一次（百万行日志下是可观的 CPU 与 GC 开销）。
// 提升为包级变量，与同文件的 kbPrefixRegex 保持一致的写法。
var htmlTagRegex = regexp.MustCompile(`<[^>]+>`)

// CleanDescriptionTitle 从知识库 Description 中提取干净简明的中文核心含义
func CleanDescriptionTitle(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}

	// 1. 去除 HTML 标签（如果存在）
	// RCA-10: 复用包级已编译正则，不再每次调用都重新编译
	if strings.Contains(desc, "<") && strings.Contains(desc, ">") {
		desc = htmlTagRegex.ReplaceAllString(desc, "")
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

// coreParamExtractors 关键上下文参数的提取顺序。
//
// RCA-15: 原实现按"实例→MAC→接口→对端→聚合→状态→原因"的**采集顺序**截断前 3 项，
// 而 parts 顺序恰好与诊断价值相反——最有价值的"原因/状态"永远排在最末，
// 一旦参数超过 3 个就会被最先砍掉。
//
// 这里改为显式声明诊断优先级：原因 > 接口 > 对端 > 状态 > 实例/MAC/聚合。
// 截断时先按 priority 升序（数值越小越重要）排序，再取前 N 个，最后按展示顺序输出。
var coreParamExtractors = []struct {
	label    string
	role     string
	priority int // 数值越小诊断价值越高
}{
	{"原因", "reason", 0},
	{"接口", "interface", 1},
	{"对端", "peer", 2},
	{"IP", "ip", 3},
	{"状态", "state", 4},
	{"实例", "evpninstance", 5},
	{"MAC", "mac", 6},
	{"聚合", "trunk", 7},
}

// ExtractCoreContextParams 提取最重要的关键上下文参数标签（最多提取 3 个关键实体）
func ExtractCoreContextParams(normParams map[string]string) string {
	// 1. 管理审计类：用户 + 命令是一个语义整体，命中即完整返回
	user := ResolveParam(normParams, "user")
	cmd := ResolveParam(normParams, "command")
	if user != "" && cmd != "" {
		return fmt.Sprintf("用户: %s, 执行: %s", user, cmd)
	}

	type item struct {
		text     string
		priority int
	}

	items := make([]item, 0, len(coreParamExtractors))
	for _, ex := range coreParamExtractors {
		val := ResolveParam(normParams, ex.role)
		if val == "" {
			continue
		}
		// "对端"与"IP"语义重叠，取其一即可
		if ex.label == "IP" {
			dup := false
			for _, it := range items {
				if it.priority == 2 { // 已存在"对端"
					dup = true
					break
				}
			}
			if dup {
				continue
			}
		}
		if ex.label == "原因" && len([]rune(val)) > 30 {
			val = string([]rune(val)[:30]) + "..."
		}
		items = append(items, item{text: ex.label + ": " + val, priority: ex.priority})
	}

	if len(items) == 0 {
		return ""
	}

	// 2. 按诊断优先级排序后截断：保证"原因/接口/对端"这类高价值字段永不被砍
	sort.SliceStable(items, func(i, j int) bool { return items[i].priority < items[j].priority })
	if len(items) > 3 {
		items = items[:3]
	}
	// 3. 输出时再按采集顺序（即上面声明的顺序）展示，读起来更自然
	sort.SliceStable(items, func(i, j int) bool { return items[i].priority < items[j].priority })

	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.text)
	}
	return strings.Join(parts, ", ")
}

// PlaceholderRegex 匹配消息模板中的占位符
var PlaceholderRegex = regexp.MustCompile(`(?:\[\s*([a-zA-Z0-9_\-\s]+?)\s*\]|<\s*([a-zA-Z0-9_\-\s]+?)\s*>|\{\s*([a-zA-Z0-9_\-\s]+?)\s*\}|%\s*([a-zA-Z0-9_\-\s]+?)\s*%|\$\s*([a-zA-Z0-9_\-]+))`)

// RenderTemplateWithParams 将官方文档消息模板占位符替换为提取的真实值
func RenderTemplateWithParams(template string, rawParams map[string]string, normParams map[string]string) string {
	if strings.TrimSpace(template) == "" || len(rawParams) == 0 {
		return template
	}

	return PlaceholderRegex.ReplaceAllStringFunc(template, func(match string) string {
		submatches := PlaceholderRegex.FindStringSubmatch(match)
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
		//
		// RCA-11: 改用构建期生成的反查表，O(1) 命中且结果确定，
		// 不再依赖 Go map 的随机遍历顺序。
		if role := ResolveAliasRole(normK); role != "" {
			if v := ResolveParam(normParams, role); v != "" {
				return v
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
