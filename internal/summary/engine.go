package summary

import (
	"strings"

	"logauditorgo/internal/model"
)

// 严重级别前缀阈值。
// RCA-15: GenerateSummary 的 severity 形参此前全程未读，
// 调用方误以为严重级别会影响措辞。现在用它给高严重级事件加定级前缀，
// 让运维在列表页一眼分辨紧急程度。
const (
	severityCriticalMax = 2 // severity <= 2：灾难/严重
	severityErrorMax    = 4 // severity <= 4：错误/警告
)

// Engine 事件摘要生成引擎（知识库驱动自适应）
type Engine struct{}

// NewEngine 创建并初始化事件摘要生成引擎
func NewEngine() *Engine {
	return &Engine{}
}

// GenerateSummary 生成单条日志事件的语义摘要
// 优先使用命中的官方知识库中文释义与模板自适应生成；
// 若未关联官方知识库，自动走通用语义自适应提取，全流程无需任何特定协议规则！
func (e *Engine) GenerateSummary(module, brief string, severity int, rawMsg string, params map[string]string, kb *model.Knowledge) string {
	// 0. 注释性日志：由 # 开头的日志行（文件头、Digest校验、关闭记录等）统一由专用逻辑生成友好提示
	if strings.ToUpper(strings.TrimSpace(module)) == "COMMENT" {
		return BuildCommentSummary(brief, params, rawMsg)
	}

	// 1. 构建参数规范化索引
	normParams := BuildNormalizedMap(params)

	// 2. 第一优先级：官方知识库全自动驱动与参数实例化
	if kb != nil {
		if kbSummary := BuildKnowledgeSummary(kb, normParams, params); kbSummary != "" {
			return withSeverityPrefix(severity, kbSummary)
		}
	}

	// 3. 第二优先级：未关联知识库时的通用语义自适应提取（不限定协议）
	return withSeverityPrefix(severity, BuildAdaptiveGenericSummary(module, brief, normParams, params, rawMsg))
}

// withSeverityPrefix 依据严重级别给摘要加定级前缀 (RCA-15)
func withSeverityPrefix(severity int, summary string) string {
	if summary == "" || severity <= 0 {
		return summary
	}
	switch {
	case severity <= severityCriticalMax:
		return "[紧急] " + summary
	case severity <= severityErrorMax:
		return "[告警] " + summary
	default:
		return summary
	}
}

// 全局默认引擎单例
var DefaultEngine = NewEngine()

// GenerateSummary 便捷全局调用函数
func GenerateSummary(module, brief string, severity int, rawMsg string, params map[string]string, kb ...*model.Knowledge) string {
	var k *model.Knowledge
	if len(kb) > 0 && kb[0] != nil {
		k = kb[0]
	}
	return DefaultEngine.GenerateSummary(module, brief, severity, rawMsg, params, k)
}
