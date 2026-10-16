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

