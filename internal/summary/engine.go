package summary

import (
	"logauditorgo/internal/model"
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
	// 1. 构建参数规范化索引
	normParams := BuildNormalizedMap(params)

	// 2. 第一优先级：官方知识库全自动驱动与参数实例化
	if kb != nil {
		if kbSummary := BuildKnowledgeSummary(kb, normParams, params); kbSummary != "" {
			return kbSummary
		}
	}

	// 3. 第二优先级：未关联知识库时的通用语义自适应提取（不限定协议）
	return BuildAdaptiveGenericSummary(module, brief, normParams, params, rawMsg)
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
