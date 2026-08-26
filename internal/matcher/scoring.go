package matcher

import (
	"strings"

	"logauditorgo/internal/model"
)

// MatchTier 匹配层级常量
const (
	TierExact    = "EXACT"     // 1.0: 模块与简名精确匹配
	TierMnemonic = "MNEMONIC"  // 0.9: 助记符/别名不区分大小写模糊匹配
	TierTemplate = "TEMPLATE"  // 0.8: 日志消息模板反向正则匹配
	TierBleve    = "BLEVE"     // 0.5 ~ 0.75: 全文语义关键词检索召回
	TierUnmatch  = "UNMATCHED" // 0.0: 未匹配
)

// 评分体系与置信度常量定义 (Centralized Scoring & Confidence Constants)
const (
	// 版本匹配评分体系 (用于 FindBestKnowledgeMatchPtr)
	ScoreExactProductExactVersion = 150  // 同型号同版本精准命中
	ScoreExactProductNewerVersion = 120  // 同型号不同版本
	ScoreSameFamily               = 50   // 同产品族相近系列匹配 (如 CloudEngine 或 USG 系列)
	ScoreCrossProductGeneral      = 10   // 跨产品全局通用知识
	ScoreNoVersionFallback        = 5    // 无版本信息兜底候选

	// Bleve 全文检索匹配门限
	BleveMinScoreThreshold        = 0.25 // Bleve 搜索候选最低相关度分数阈值

	// 各层级匹配置信度 (Confidence Level)
	ConfidenceExact               = 1.0  // Tier 1 (EXACT) 精确匹配置信度
	ConfidenceMnemonic            = 0.90 // Tier 2 (MNEMONIC) 助记符别名匹配置信度
	ConfidenceTemplate            = 0.80 // Tier 3 (TEMPLATE) 消息模板反向匹配置信度
	ConfidenceBleveMin            = 0.50 // Tier 4 (BLEVE) 全文语义检索最小置信度
	ConfidenceBleveMax            = 0.75 // Tier 4 (BLEVE) 全文语义检索最大置信度
)

// CalculateConfidence 根据匹配层级与相关度计算置信度
func CalculateConfidence(tier string, rawBleveScore float64) float64 {
	switch tier {
	case TierExact:
		return ConfidenceExact
	case TierMnemonic:
		return ConfidenceMnemonic
	case TierTemplate:
		return ConfidenceTemplate
	case TierBleve:
		// 将 Bleve Score 归一化为 ConfidenceBleveMin ~ ConfidenceBleveMax 之间
		score := ConfidenceBleveMin + (rawBleveScore / 10.0)
		if score > ConfidenceBleveMax {
			score = ConfidenceBleveMax
		}
		if score < ConfidenceBleveMin {
			score = ConfidenceBleveMin
		}
		return score
	default:
		return 0.0
	}
}

// FindBestKnowledgeMatch 实现了基于产品型号与版本的智能回退（Fallback）策略
func FindBestKnowledgeMatch(candidates []model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	if len(candidates) == 0 {
		return nil
	}
	ptrs := make([]*model.Knowledge, len(candidates))
	for i := range candidates {
		ptrs[i] = &candidates[i]
	}
	return FindBestKnowledgeMatchPtr(ptrs, targetProduct, targetVersion)
}

// FindBestKnowledgeMatchPtr 针对指针切片执行高效零拷贝版本匹配打分
func FindBestKnowledgeMatchPtr(candidates []*model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	if len(candidates) == 0 {
		return nil
	}

	targetProductTrim := strings.TrimSpace(targetProduct)
	targetVersionTrim := strings.TrimSpace(targetVersion)
	targetUpper := strings.ToUpper(targetProductTrim)

	var bestMatch *model.Knowledge
	var highestScore int = -1

	for _, k := range candidates {
		if k == nil {
			continue
		}

		if len(k.Versions) == 0 {
			if highestScore < ScoreNoVersionFallback {
				highestScore = ScoreNoVersionFallback
				bestMatch = k
			}
			continue
		}

		for _, v := range k.Versions {
			currentScore := 0
			vProductTrim := strings.TrimSpace(v.ProductType)
			vVersionTrim := strings.TrimSpace(v.ProductVersion)

			// 1. 同型号精确匹配 (要求非空)
			if targetProductTrim != "" && strings.EqualFold(vProductTrim, targetProductTrim) {
				if targetVersionTrim != "" && strings.EqualFold(vVersionTrim, targetVersionTrim) {
					currentScore = ScoreExactProductExactVersion // 完全精准命中: 150
				} else {
					// 同型号不同版本，偏好较新版本
					currentScore = ScoreExactProductNewerVersion // 120
				}
			} else if targetProductTrim != "" && vProductTrim != "" {
				// 2. 同产品族相近系列匹配 (如 CloudEngine 系列或 USG 系列)
				vUpper := strings.ToUpper(vProductTrim)
				if (strings.Contains(targetUpper, "CLOUDENGINE") && strings.Contains(vUpper, "CLOUDENGINE")) ||
					(strings.Contains(targetUpper, "USG") && strings.Contains(vUpper, "USG")) ||
					(strings.Contains(targetUpper, "HISECENGINE") && strings.Contains(vUpper, "HISECENGINE")) ||
					(strings.Contains(targetUpper, "NETENGINE") && strings.Contains(vUpper, "NETENGINE")) ||
					(strings.Contains(targetUpper, "CAMPUS") && strings.Contains(vUpper, "CAMPUS")) {
					currentScore = ScoreSameFamily // 50
				} else {
					// 3. 跨产品全局通用知识
					currentScore = ScoreCrossProductGeneral // 10
				}
			} else {
				currentScore = ScoreCrossProductGeneral // 10
			}

			if currentScore > highestScore {
				highestScore = currentScore
				bestMatch = k
			}
		}
	}

	return bestMatch
}

