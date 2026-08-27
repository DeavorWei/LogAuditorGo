package summary

import (
	"fmt"
	"strings"
	"sync"
)

// Engine 事件摘要生成引擎
type Engine struct {
	mu          sync.RWMutex
	moduleRules map[string][]Rule
	allRules    []Rule
}

// NewEngine 创建并初始化事件摘要生成引擎
func NewEngine() *Engine {
	e := &Engine{
		moduleRules: make(map[string][]Rule),
		allRules:    make([]Rule, 0, len(DefaultRules)),
	}

	for _, rule := range DefaultRules {
		e.RegisterRule(rule)
	}

	return e
}

// RegisterRule 注册规则
func (e *Engine) RegisterRule(rule Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.allRules = append(e.allRules, rule)
	for _, m := range rule.Modules {
		modUpper := strings.ToUpper(strings.TrimSpace(m))
		e.moduleRules[modUpper] = append(e.moduleRules[modUpper], rule)
	}
}

// GenerateSummary 生成单条日志事件的中文语义摘要
func (e *Engine) GenerateSummary(module, brief string, severity int, rawMsg string, params map[string]string) string {
	modUpper := strings.ToUpper(strings.TrimSpace(module))

	// 1. 构建参数规范化索引
	normParams := BuildNormalizedMap(params)

	ctx := &EventContext{
		Module:     module,
		Brief:      brief,
		Severity:   severity,
		RawMsg:     rawMsg,
		RawParams:  params,
		NormParams: normParams,
	}

	// 2. 按模块索引查找匹配规则
	e.mu.RLock()
	rules := e.moduleRules[modUpper]
	e.mu.RUnlock()

	for _, r := range rules {
		if summary := r.Handler(ctx); summary != "" {
			return summary
		}
	}

	// 3. 通用兜底策略
	// 尝试提取动态参数前 4 项
	if topParams := ExtractTopParams(params, 4); topParams != "" {
		if module != "" && brief != "" {
			return fmt.Sprintf("[%s/%s] %s", module, brief, topParams)
		}
		if module != "" {
			return fmt.Sprintf("[%s] %s", module, topParams)
		}
		return topParams
	}

	// 尝试截取 rawMsg
	cleanMsg := strings.TrimSpace(rawMsg)
	if cleanMsg != "" {
		// 移除可能的内部多余换行
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

// 全局默认引擎单例
var DefaultEngine = NewEngine()

// GenerateSummary 便捷全局调用函数
func GenerateSummary(module, brief string, severity int, rawMsg string, params map[string]string) string {
	return DefaultEngine.GenerateSummary(module, brief, severity, rawMsg, params)
}
