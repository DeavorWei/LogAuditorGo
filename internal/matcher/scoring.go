package matcher

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"logauditorgo/internal/model"
)

type ProductFamily string

const (
	FamilyUnknown     ProductFamily = "UNKNOWN"
	FamilyWLAN        ProductFamily = "WLAN"
	FamilyCampus      ProductFamily = "CAMPUS"
	FamilyCloudEngine ProductFamily = "CLOUDENGINE"
	FamilyHiSecEngine ProductFamily = "HISECENGINE"
	FamilyNetEngine   ProductFamily = "NETENGINE"
)

type familyMappingRule struct {
	Family        ProductFamily
	Aliases       []string
	ExtraPatterns []string // 原始正则别名（不转义），用于"型号前缀 + 数字 + 可选字母后缀"形态，如 USG\d{4}[A-Z]?
}

// 别名表长前缀优先，所有别名采用单词边界匹配，杜绝子串误判 (闭环 C6 与 P2-2)
var familyRules = []familyMappingRule{
	{
		Family: FamilyWLAN,
		Aliases: []string{
			"AIR-ENGINE", "AIRENGINE", "AC6805", "AC6800", "AC6605", "AC6508", "AC6005", "FIT-AP", "FAT-AP", "WLAN",
		},
		ExtraPatterns: []string{`AC\d{4}[A-Z]?`},
	},
	{
		Family: FamilyCampus,
		Aliases: []string{
			"S12700E", "S12700", "S9700", "S7700", "S6700", "S5700", "CAMPUS",
		},
	},
	{
		Family: FamilyCloudEngine,
		Aliases: []string{
			"CLOUDENGINE", "CE16800", "CE12800", "CE9860", "CE8800", "CE6800", "CE5800",
		},
	},
	{
		Family: FamilyHiSecEngine,
		Aliases: []string{
			"HISECENGINE", "USG6000", "USG9000",
		},
		ExtraPatterns: []string{`USG\d{4}[A-Z]?`},
	},
	{
		Family: FamilyNetEngine,
		Aliases: []string{
			"NETENGINE", "NE8000", "NE5000E", "NE40E", "NE20E", "CX600",
		},
	},
}

var familyBoundaryRegex map[ProductFamily]*regexp.Regexp

func init() {
	familyBoundaryRegex = make(map[ProductFamily]*regexp.Regexp, len(familyRules))
	for _, rule := range familyRules {
		sorted := make([]string, len(rule.Aliases))
		copy(sorted, rule.Aliases)
		sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
		parts := make([]string, 0, len(sorted)+len(rule.ExtraPatterns))
		for _, a := range sorted {
			parts = append(parts, regexp.QuoteMeta(a))
		}
		parts = append(parts, rule.ExtraPatterns...)
		pattern := `(?:^|\b|[\s,;/])(?:` + strings.Join(parts, "|") + `)(?:\b|[\s,;/]|$)`
		familyBoundaryRegex[rule.Family] = regexp.MustCompile(pattern)
	}
}

func resolveProductFamily(product string) ProductFamily {
	upper := strings.ToUpper(strings.TrimSpace(product))
	if upper == "" {
		return FamilyUnknown
	}
	for _, rule := range familyRules {
		if re, ok := familyBoundaryRegex[rule.Family]; ok && re.MatchString(upper) {
			return rule.Family
		}
	}
	return FamilyUnknown
}

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
	ScoreExactProductExactVersion = 150 // 同型号同版本精准命中
	ScoreExactProductNewerVersion = 120 // 同型号不同版本
	ScoreSameFamily               = 50  // 同产品族相近系列匹配 (如 CloudEngine 或 USG 系列)
	ScoreCrossProductGeneral      = 10  // 跨产品全局通用知识
	ScoreNoVersionFallback        = 5   // 无版本信息兜底候选

	// Bleve 全文检索匹配门限
	BleveMinScoreThreshold = 0.25 // Bleve 搜索候选最低相关度分数阈值

	// 各层级匹配置信度 (Confidence Level)
	ConfidenceExact    = 1.0  // Tier 1 (EXACT) 精确匹配置信度
	ConfidenceMnemonic = 0.90 // Tier 2 (MNEMONIC) 助记符别名匹配置信度
	ConfidenceTemplate = 0.80 // Tier 3 (TEMPLATE) 消息模板反向匹配置信度
	ConfidenceBleveMin = 0.50 // Tier 4 (BLEVE) 全文语义检索最小置信度
	ConfidenceBleveMax = 0.75 // Tier 4 (BLEVE) 全文语义检索最大置信度
)

// Tier4 选优权重 (PARSE-07)：综合 Bleve 相关度与版本贴合度，避免"版本分高但语义无关"的条目胜出
const (
	BleveWeightRelevance = 0.6
	BleveWeightVersion   = 0.4
)

// Tier1 软短路相关阈值与惩罚系数 (PARSE-06)
const (
	// MinTier1Confidence Tier1 命中后立即返回所需的最低一致性分。
	// 低于此值时释放控制权，让日志继续下探 Tier2/3/4。
	MinTier1Confidence = 0.60

	// tier1SeverityPenalty 严重级别每相差一级的扣分
	tier1SeverityPenalty = 0.10
	// tier1SeverityPenaltyMax 严重级别不一致的扣分上限
	tier1SeverityPenaltyMax = 0.40
	// tier1TemplateMismatchPenalty 模板与日志正文完全对不上时的扣分
	tier1TemplateMismatchPenalty = 0.25
)

// Tier1Consistency 计算 Tier1（Module + Brief 精确命中）候选与当前日志的一致性分 [0,1]。
//
// PARSE-06: 旧逻辑只要 Module+Brief 命中就无条件返回，
// 把"不同严重级、不同子类的日志"糊成同一条知识，且置信度恒为 1.0，
// 前端完全无法分辨哪条结论可信。这里只对确凿存在的不一致扣分：
//   - 严重级别：日志与知识都给出了级别且不一致时，按差值扣分（封顶 0.40）；
//   - 正文模板：知识提供了消息模板却与日志正文完全对不上时扣分 0.25。
//
// 任一方信息缺失（级别为 0 / 模板为空）不扣分——缺失不是"不一致"。
//
// matchTemplate 由调用方（MatchEngine）注入，以便复用其带 LRU 的模板正则缓存；
// 该函数在每条 Tier1 命中的日志上都会执行，绝不能每次重新编译正则。
func Tier1Consistency(norm *model.NormalizedLog, cand *model.Knowledge, matchTemplate func(string, string) bool) float64 {
	if norm == nil || cand == nil {
		return 0
	}

	score := 1.0

	// 1. 严重级别一致性
	if norm.Severity >= 1 && norm.Severity <= 8 && cand.Severity >= 1 && cand.Severity <= 8 {
		diff := cand.Severity - norm.Severity
		if diff < 0 {
			diff = -diff
		}
		if diff > 0 {
			penalty := float64(diff) * tier1SeverityPenalty
			if penalty > tier1SeverityPenaltyMax {
				penalty = tier1SeverityPenaltyMax
			}
			score -= penalty
		}
	}

	// 2. 消息模板一致性（知识有模板、日志有正文时才比较）
	if matchTemplate != nil &&
		strings.TrimSpace(cand.Message) != "" && strings.TrimSpace(norm.MessageBody) != "" &&
		!matchTemplate(cand.Message, norm.MessageBody) {
		score -= tier1TemplateMismatchPenalty
	}

	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// bleveScoreNormRef 是 Bleve 原始 TF-IDF 分的归一化参考上界。
// Bleve 分数不封顶（随词频/字段长度增长），直接除以常数会在 raw>=2.5 时恒定触顶，
// 因此改用 Log1p 压缩到 [0,1] 再映射到置信区间。
const bleveScoreNormRef = 100.0

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
		// PARSE-07/16：原实现 rawBleveScore/10 在 raw>=2.5 时即恒定触顶 0.75（置信度失去区分度），
		// 且 `< BleveMin` 的下限分支因分数非负而永远不可达（死代码）。
		// 改为 Log1p 归一化后线性映射到 [BleveMin, BleveMax]：raw=0 落在下限，raw 越大越接近上限。
		// 仅对真正的非法分数（NaN / 负值）判为不可信。
		if math.IsNaN(rawBleveScore) || rawBleveScore < 0 {
			return 0.0
		}
		return ConfidenceBleveMin + (ConfidenceBleveMax-ConfidenceBleveMin)*NormalizeBleveScore(rawBleveScore, 0)
	default:
		return 0.0
	}
}

// NormalizeBleveScore 将 Bleve 原始 TF-IDF 分归一化到 [0,1]。
// maxScore 为本批命中的最高分，>0 时按最高分做相对归一化以拉开候选梯度；
// 否则以 bleveScoreNormRef 为上界做 Log1p 绝对归一化。
func NormalizeBleveScore(rawScore, maxScore float64) float64 {
	if math.IsNaN(rawScore) || rawScore <= 0 {
		return 0
	}
	ref := maxScore
	if math.IsNaN(ref) || ref <= 0 || ref < rawScore {
		ref = bleveScoreNormRef
	}
	v := math.Log1p(rawScore) / math.Log1p(ref)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// FindBestKnowledgeMatch 实现了基于产品型号与版本的智能回退（Fallback）策略
func FindBestKnowledgeMatch(candidates []model.Knowledge, targetProduct, targetVersion string, norm ...*model.NormalizedLog) *model.Knowledge {
	if len(candidates) == 0 {
		return nil
	}
	ptrs := make([]*model.Knowledge, len(candidates))
	for i := range candidates {
		ptrs[i] = &candidates[i]
	}
	return FindBestKnowledgeMatchPtr(ptrs, targetProduct, targetVersion, norm...)
}

// versionNumRegex 提取版本串中的数字段，用于华为版本号语义比较
var versionNumRegex = regexp.MustCompile(`\d+`)

// versionNumbers 将 "V200R019C00SPC500" / "V800R021C00" / "10.2.0.1" 解析为数字序列
func versionNumbers(s string) []int {
	var out []int
	for _, m := range versionNumRegex.FindAllString(s, -1) {
		if n, err := strconv.Atoi(m); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// CompareVersion 语义比较两个版本号，返回 -1 / 0 / 1（a<b / a==b / a>b）。
// 支持华为 V/R/C/SPC 形态与点分十进制形态；解析不出数字时退化为字典序比较。
func CompareVersion(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	na, nb := versionNumbers(a), versionNumbers(b)
	if len(na) == 0 || len(nb) == 0 {
		return strings.Compare(strings.ToUpper(a), strings.ToUpper(b))
	}
	n := len(na)
	if len(nb) < n {
		n = len(nb)
	}
	for i := 0; i < n; i++ {
		if na[i] < nb[i] {
			return -1
		}
		if na[i] > nb[i] {
			return 1
		}
	}
	switch {
	case len(na) < len(nb):
		return -1
	case len(na) > len(nb):
		return 1
	}
	return 0
}

// scoreVersion 计算单条版本映射相对目标产品/版本的贴合度得分
func scoreVersion(vProduct, vVersion, targetProduct, targetVersion string) int {
	targetProductTrim := strings.TrimSpace(targetProduct)
	targetVersionTrim := strings.TrimSpace(targetVersion)
	vProductTrim := strings.TrimSpace(vProduct)
	vVersionTrim := strings.TrimSpace(vVersion)

	// 1. 同型号精确匹配 (要求非空)
	if targetProductTrim != "" && strings.EqualFold(vProductTrim, targetProductTrim) {
		if targetVersionTrim != "" && strings.EqualFold(vVersionTrim, targetVersionTrim) {
			return ScoreExactProductExactVersion // 完全精准命中: 150
		}
		return ScoreExactProductNewerVersion // 同型号不同版本: 120
	}

	// 2. 同产品族相近系列匹配 (基于确定性枚举与边界正则)
	if targetProductTrim != "" && vProductTrim != "" {
		tf := resolveProductFamily(targetProductTrim)
		vf := resolveProductFamily(vProductTrim)
		if tf != FamilyUnknown && tf == vf {
			return ScoreSameFamily // 50
		}
	}

	// 3. 跨产品全局通用知识 / 无产品信息兜底
	return ScoreCrossProductGeneral // 10
}

// IsWirelessLog 统一判定报文是否包含 WLAN 无线接入协议特征
func IsWirelessLog(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.Contains(trimmed, "[WLAN_") ||
		(strings.Contains(trimmed, "ACMAC:") && (strings.Contains(trimmed, "SSID:") || strings.Contains(trimmed, "APMAC:")))
}

// IsWiredLog 判定报文是否包含典型有线协议标志 (闭环 P2-3 对称性保护)
func IsWiredLog(body string) bool {
	trimmed := strings.TrimSpace(body)
	return strings.Contains(trimmed, "[USER_INFO_") || strings.Contains(trimmed, "DEVICEMAC:")
}

func calcFeatureScore(norm *model.NormalizedLog, cand *model.Knowledge) int {
	if norm == nil || cand == nil {
		return 0
	}

	candFamily := FamilyUnknown
	for _, v := range cand.Versions {
		if fam := resolveProductFamily(v.ProductType); fam != FamilyUnknown {
			candFamily = fam
			break
		}
	}

	// 1. 无线报文场景判定
	if IsWirelessLog(norm.MessageBody) {
		if candFamily == FamilyWLAN {
			return 30
		}

		// 对所有非 WLAN 族的已知有线族候选条目扣除排斥分，拉大与 WLAN 候选的分差
		if candFamily == FamilyCloudEngine || candFamily == FamilyNetEngine ||
			candFamily == FamilyCampus || candFamily == FamilyHiSecEngine {
			// 归一化参数字符串：大写并移除 '_' 与 '-'，确保连写或下划线分隔（如 USER_INFO_OFFLINE 与 USERINFO）均精准命中 (闭环 P1-2)
			paramNorm := strings.ReplaceAll(strings.ReplaceAll(strings.ToUpper(cand.Parameters), "_", ""), "-", "")
			if strings.Contains(paramNorm, "WIREDUSERINFO") ||
				(strings.Contains(paramNorm, "USERINFO") && !strings.Contains(paramNorm, "ACMAC")) {
				return -20
			}
			// 即使参数中无明确有线标识，非 WLAN 族候选也施加轻度排斥分
			return -10
		}
	}

	// 2. 有线报文场景对称保护：典型有线日志绝不挂 WLAN 知识 (闭环 P2-3)
	if IsWiredLog(norm.MessageBody) {
		if candFamily == FamilyWLAN {
			return -20
		}
	}

	return 0
}

// ScoreKnowledgeVersion 计算单条知识相对目标产品/版本的整体贴合度（取所有版本映射中的最高分）。
// 供 Tier4 做"相关度 + 版本贴合度"加权选优使用 (PARSE-07)。
func ScoreKnowledgeVersion(k *model.Knowledge, targetProduct, targetVersion string) int {
	if k == nil {
		return 0
	}
	if len(k.Versions) == 0 {
		return ScoreNoVersionFallback
	}
	best := 0
	for _, v := range k.Versions {
		if s := scoreVersion(v.ProductType, v.ProductVersion, targetProduct, targetVersion); s > best {
			best = s
		}
	}
	return best
}

// newestVersion 取一组版本映射中的最新版本串
func newestVersion(versions []model.KnowledgeVersionMapping) string {
	newest := ""
	for _, v := range versions {
		if CompareVersion(v.ProductVersion, newest) > 0 {
			newest = v.ProductVersion
		}
	}
	return newest
}

// betterCandidate 在同分时为两个候选给出确定性排序 (PARSE-04)：
//  1. 已知目标版本时，优先"不高于目标版本"的候选，其次取其中最新的；
//  2. 仍无法区分时按 ID 升序，消除数据库返回顺序带来的结果不确定性。
func betterCandidate(cand, cur *model.Knowledge, targetVersion string) bool {
	if cur == nil {
		return true
	}
	if cand == nil {
		return false
	}
	if tv := strings.TrimSpace(targetVersion); tv != "" {
		candV := newestVersion(cand.Versions)
		curV := newestVersion(cur.Versions)
		candOK := CompareVersion(candV, tv) <= 0
		curOK := CompareVersion(curV, tv) <= 0
		if candOK != curOK {
			return candOK
		}
		if c := CompareVersion(candV, curV); c != 0 {
			return c > 0
		}
	}
	return cand.ID < cur.ID
}

// FindBestKnowledgeMatchPtr 针对指针切片执行高效零拷贝版本与特征加权匹配打分
func FindBestKnowledgeMatchPtr(candidates []*model.Knowledge, targetProduct, targetVersion string, norm ...*model.NormalizedLog) *model.Knowledge {
	if len(candidates) == 0 {
		return nil
	}

	var activeNorm *model.NormalizedLog
	if len(norm) > 0 && norm[0] != nil {
		activeNorm = norm[0]
	}

	targetVersionTrim := strings.TrimSpace(targetVersion)

	var bestMatch *model.Knowledge
	var highestTotalScore = -999

	for _, k := range candidates {
		if k == nil {
			continue
		}
		vScore := ScoreKnowledgeVersion(k, targetProduct, targetVersion)
		fScore := calcFeatureScore(activeNorm, k)
		totalScore := vScore + fScore

		if totalScore > highestTotalScore ||
			(totalScore == highestTotalScore && betterCandidate(k, bestMatch, targetVersionTrim)) {
			highestTotalScore = totalScore
			bestMatch = k
		}
	}

	return bestMatch
}
