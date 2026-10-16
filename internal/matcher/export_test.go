package matcher

import "logauditorgo/internal/model"

// 本文件仅供外部测试包 matcher_test 访问包内未导出实现使用（标准 Go export_test 模式）。
// 生产代码不应依赖此处导出的符号。

// MnemonicStemForTest 暴露 mnemonicStem，用于回归 PARSE-13（大写后缀归一化 + 极性判定）。
func MnemonicStemForTest(brief string) (string, int8) {
	return mnemonicStem(brief)
}

// CalcFeatureScoreForTest 暴露 calcFeatureScore，用于回归 D6（无版本映射知识条目内容特征兜底族判定）。
func CalcFeatureScoreForTest(norm *model.NormalizedLog, cand *model.Knowledge) int {
	return calcFeatureScore(norm, cand)
}

// ShouldAdmitTier4ForTest 暴露 shouldAdmitTier4，用于验证 Tier 4 全文检索准入规则与中文支持。
func ShouldAdmitTier4ForTest(module, brief, messageBody string) bool {
	return shouldAdmitTier4(module, brief, messageBody)
}

// BuildExactBestKeyForTest 暴露 buildExactBestKey 用于基准与单测
func BuildExactBestKeyForTest(module, upperBrief, product, version string, isWireless, isWired bool) string {
	return buildExactBestKey(module, upperBrief, product, version, isWireless, isWired)
}

// BuildBodyKeyForTest 暴露 buildBodyKey 用于基准与单测
func BuildBodyKeyForTest(bestKey string, severity int, messageBody string) string {
	return buildBodyKey(bestKey, severity, messageBody)
}

// FilterExcludedCandidatesForTest 暴露 filterExcludedCandidates，用于回归互斥族候选过滤（宽松/严格双模式）。
func FilterExcludedCandidatesForTest(candidates []*model.Knowledge, isWireless, isWired, strict bool) []*model.Knowledge {
	return filterExcludedCandidates(candidates, isWireless, isWired, strict)
}

// IsSuspectWinnerForTest 暴露 isSuspectWinner，用于回归互斥族豁免保留判定。
func IsSuspectWinnerForTest(winner *model.Knowledge, isWireless, isWired bool) bool {
	return isSuspectWinner(winner, isWireless, isWired)
}

// ApplySuspectCapForTest 暴露 applySuspectCap，用于回归豁免条目的 Tier 降级与置信度封顶。
func ApplySuspectCapForTest(tier string, conf float64, suspect bool) (string, float64) {
	return applySuspectCap(tier, conf, suspect)
}

// BestVersionMappingForTest 暴露 bestVersionMapping，用于回归"打分与文档归属同源"。
func BestVersionMappingForTest(k *model.Knowledge, targetProduct, targetVersion string) (int, *model.KnowledgeVersionMapping) {
	return bestVersionMapping(k, targetProduct, targetVersion)
}
