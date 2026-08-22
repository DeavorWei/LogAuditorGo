package matcher

// MatchTier 匹配层级常量
const (
	TierExact    = "EXACT"     // 1.0: 模块与简名精确匹配
	TierMnemonic = "MNEMONIC"  // 0.9: 助记符/别名不区分大小写模糊匹配
	TierTemplate = "TEMPLATE"  // 0.8: 日志消息模板反向正则匹配
	TierBleve    = "BLEVE"     // 0.5 ~ 0.75: 全文语义关键词检索召回
	TierUnmatch  = "UNMATCHED" // 0.0: 未匹配
)

// CalculateConfidence 根据匹配层级与相关度计算置信度
func CalculateConfidence(tier string, rawBleveScore float64) float64 {
	switch tier {
	case TierExact:
		return 1.0
	case TierMnemonic:
		return 0.90
	case TierTemplate:
		return 0.80
	case TierBleve:
		// 将 Bleve Score 归一化为 0.50 ~ 0.75 之间
		score := 0.50 + (rawBleveScore / 10.0)
		if score > 0.75 {
			score = 0.75
		}
		if score < 0.50 {
			score = 0.50
		}
		return score
	default:
		return 0.0
	}
}
