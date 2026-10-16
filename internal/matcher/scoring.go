package matcher

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

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

// 别名表长前缀优先，所有别名采用单词边界匹配，杜绝子串误判 (闭环 C6、P2-2 与实测放行具体型号)
var familyRules = []familyMappingRule{
	{
		Family: FamilyWLAN,
		Aliases: []string{
			"AIR-ENGINE", "AIRENGINE", "AC6805", "AC6800", "AC6605", "AC6508", "AC6005", "FIT-AP", "FAT-AP", "WLAN",
		},
		ExtraPatterns: []string{`AC\d{4,5}[A-Z]?`, `AP\d{4,5}[A-Z]?`, `AIRENGINE\s*\d{4,5}[A-Z]?`},
	},
	{
		Family: FamilyCampus,
		Aliases: []string{
			"S12700E", "S12700", "S9700", "S7700", "S6700", "S5700", "CAMPUS",
		},
		ExtraPatterns: []string{`S\d{4,5}[A-Z]?`},
	},
	{
		Family: FamilyCloudEngine,
		Aliases: []string{
			"CLOUDENGINE", "CE16800", "CE12800", "CE9860", "CE8800", "CE6800", "CE5800",
		},
		ExtraPatterns: []string{`CE\d{4,5}[A-Z]?`},
	},
	{
		Family: FamilyHiSecEngine,
		Aliases: []string{
			"HISECENGINE", "USG6000", "USG9000",
		},
		ExtraPatterns: []string{`USG\d{4,5}[A-Z]?`},
	},
	{
		Family: FamilyNetEngine,
		Aliases: []string{
			"NETENGINE", "NE8000", "NE5000E", "NE40E", "NE20E", "CX600",
		},
		ExtraPatterns: []string{`NE\d{2,5}[A-Z]?`, `CX\d{3,4}[A-Z]?`},
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

var productFamilyMemo sync.Map // map[string]ProductFamily

func resolveProductFamily(product string) ProductFamily {
	upper := strings.ToUpper(strings.TrimSpace(product))
	if upper == "" {
		return FamilyUnknown
	}
	if cached, ok := productFamilyMemo.Load(upper); ok {
		return cached.(ProductFamily)
	}

	fam := FamilyUnknown
	for _, rule := range familyRules {
		if re, ok := familyBoundaryRegex[rule.Family]; ok && re.MatchString(upper) {
			fam = rule.Family
			break
		}
	}
	productFamilyMemo.Store(upper, fam)
	return fam
}

// ClearProductFamilyCache 清空产品族解析记忆化缓存与无版本条目兜底判定缓存 (闭环 S2)
func ClearProductFamilyCache() {
	candFallbackFamilyCache.Range(func(key, value any) bool {
		candFallbackFamilyCache.Delete(key)
		return true
	})
	productFamilyMemo.Range(func(key, value any) bool {
		productFamilyMemo.Delete(key)
		return true
	})
}

// ResolveProductFamily 解析产品所属的大族（公开供 API 校验等场景复用）
func ResolveProductFamily(product string) ProductFamily {
	return resolveProductFamily(product)
}

// MatchTier 匹配层级常量
const (
	TierExact         = "EXACT"          // 1.0: 模块与简名精确匹配
	TierMnemonic      = "MNEMONIC"       // 0.9: 助记符/别名不区分大小写模糊匹配
	TierTemplate      = "TEMPLATE"       // 0.8: 日志消息模板反向正则匹配
	TierBleve         = "BLEVE"          // 0.5 ~ 0.75: 全文语义关键词检索召回
	TierUnmatch       = "UNMATCHED"      // 0.0: 未匹配
	TierFamilySuspect = "FAMILY_SUSPECT" // ≤ 0.30: 互斥族豁免保留（窗口内无同族候选）
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

	// ConfidenceFamilySuspectMax 互斥族豁免保留条目的置信度上限。
	// 该类条目仅因候选窗口内不存在同族知识而被保留，跨产品复用的可能性无法排除，
	// 因此绝不允许以前述常规置信度向前端呈现。
	ConfidenceFamilySuspectMax = 0.30
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
	case TierFamilySuspect:
		// 互斥族豁免保留：恒定使用封顶置信度，绝不因上层传入原始分而放大
		return ConfidenceFamilySuspectMax
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

// candFallbackFamilyCache 缓存无版本映射条目的兜底产品族判定，避免每行日志重复执行字符串提取与匹配 (闭环 R3)
var candFallbackFamilyCache sync.Map // map[uint]ProductFamily

func resolveCandidateFamily(cand *model.Knowledge) ProductFamily {
	if cand == nil {
		return FamilyUnknown
	}

	// 1. 优先查版本映射
	for _, v := range cand.Versions {
		if fam := resolveProductFamily(v.ProductType); fam != FamilyUnknown {
			return fam
		}
	}

	// 2. 无版本映射时的内容特征兜底（条目级内存缓存）
	if cand.ID > 0 {
		if cached, ok := candFallbackFamilyCache.Load(cand.ID); ok {
			return cached.(ProductFamily)
		}
	}

	fam := FamilyUnknown
	checkContains := func(patterns ...string) bool {
		testStr := func(s string) bool {
			if s == "" {
				return false
			}
			upper := strings.ToUpper(s)
			for _, p := range patterns {
				if strings.Contains(upper, p) {
					return true
				}
			}
			return false
		}
		return testStr(cand.Brief) || testStr(cand.Parameters) || testStr(cand.Message)
	}

	if checkContains("WLAN", "ACMAC", "APMAC", "SSID") {
		fam = FamilyWLAN
	} else if checkContains("WIREDUSERINFO", "WIRED_USER_INFO") {
		// 闭环 S4: CE/NE 数据中心交换机常见字段，精准兜底为数据中心网络族
		fam = FamilyCloudEngine
	} else if checkContains("USER_INFO_", "USERINFO", "DEVICEMAC") {
		// 闭环 S4: 园区网络典型特征，精准兜底为园区网络族
		fam = FamilyCampus
	}

	if cand.ID > 0 {
		candFallbackFamilyCache.Store(cand.ID, fam)
	}
	return fam
}

func calcFeatureScore(norm *model.NormalizedLog, cand *model.Knowledge) int {
	if norm == nil || cand == nil {
		return 0
	}

	isWireless := IsWirelessLog(norm.MessageBody)
	isWired := IsWiredLog(norm.MessageBody)
	// 闭环 R3: 热路径短路，仅对无线或有线报文执行特征加减分，普通报文零分配快速返回
	if !isWireless && !isWired {
		return 0
	}

	candFamily := resolveCandidateFamily(cand)

	// 1. 有线报文场景对称保护：典型有线日志绝不挂 WLAN 知识 (闭环 P2-1 / P2-3)
	// 即使报文同时含有无线特征（如 [WLAN_ 与 DEVICEMAC: 并存），有线排斥规则优先执行，杜绝误获 +30
	if isWired && candFamily == FamilyWLAN {
		return -20
	}

	// 2. 无线报文场景判定
	if isWireless {
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

	return 0
}

// ---------------- 互斥产品族过滤与可疑降级 (FAMILY-EXCLUSIVITY) ----------------
//
// 背景：同一助记符（如 CM:USER_OFFLINERESULT）在多个产品族文档中各有一条知识时，
// 仅靠版本分与特征分无法保证"无线报文必配无线文档"：
//   上下文=CloudEngine 且候选无专属参数标记时，CE 候选可得 50-10=40 分，
//   与 WLAN 候选的 10+30=40 分完全持平，最终由 betterCandidate 的 ID 升序仲裁决定，
//   小 ID 的交换机条目会夺走无线报文的匹配结果。
// 单纯加大扣分（如 -200）并不能解决问题：FindBestKnowledgeMatchPtr 无准入阈值，
// 负分候选仍会作为"最佳候选"被落库为已匹配。
// 因此在打分之前引入候选集级硬过滤，把"能不能进"与"谁更靠前"彻底分离：
//   - filterExcludedCandidates / isFamilyConflict 负责"能不能进"（本段）；
//   - calcFeatureScore 负责"谁更靠前"的软排序微调（上一段，语义保持不变）。

// isFamilyConflict 判定候选所属产品族是否与报文物理协议特征互斥（对称保护）。
//
// 无线接入协议（WLAN）与数据中心/园区/核心路由/防火墙族在物理层与知识体系上互不覆盖，
// 报文正文中的协议标签（[WLAN_ / ACMAC: / [USER_INFO_ / DEVICEMAC:）属于不可辩驳的客观事实，
// 其优先级高于设备型号配置。
func isFamilyConflict(fam ProductFamily, isWireless, isWired bool) bool {
	if isWired && fam == FamilyWLAN {
		return true // 有线报文禁止匹配 WLAN 知识
	}
	if isWireless && (fam == FamilyCloudEngine || fam == FamilyCampus ||
		fam == FamilyNetEngine || fam == FamilyHiSecEngine) {
		return true // 无线报文禁止匹配有线交换机 / 路由器 / 防火墙知识
	}
	return false
}

// filterExcludedCandidates 剔除与报文特征互斥的候选条目。
//
// 不变式 1（并发安全）：输入切片可能直接来自全体 Worker 共享的 m.exactMap / m.moduleMap，
// 因此必须分配新切片返回，严禁原地压缩（原地修改会永久破坏内存索引并产生数据竞争）。
//
// 不变式 2（宽松模式，strict=false，Tier 1~3）：仅当窗口内存在"合规候选"时才硬剔除。
// 窗口内全部是互斥族时原样返回，交由调用方用 isSuspectWinner 标记为 FAMILY_SUSPECT。
// 这样既修复了互斥族共存的错配，又不会让"助记符只被其他产品族文档收录"的
// 跨产品公共模块（AAA / IFNET 等）凭空丢失匹配（假阴性）。
//
// 不变式 3（严格模式，strict=true，Tier 4）：无条件剔除互斥族候选，不设豁免。
// Tier4 是无确定性模块/简名约束的全文模糊检索，窗口内"只有互斥族候选"更可能是
// 跨模块噪音偶合而非真实跨产品条目；若在 Tier1~3 已判定该模块存在同族知识的情况下
// Tier4 又凭全文相关度把跨族条目捞回来，会产出高置信度错配。故 Tier4 宁可 UNMATCHED。
func filterExcludedCandidates(candidates []*model.Knowledge, isWireless, isWired, strict bool) []*model.Knowledge {
	if len(candidates) == 0 || (!isWireless && !isWired) {
		return candidates
	}

	if !strict {
		hasCompliant := false
		for _, c := range candidates {
			if c != nil && !isFamilyConflict(resolveCandidateFamily(c), isWireless, isWired) {
				hasCompliant = true
				break
			}
		}
		if !hasCompliant {
			return candidates // 宽松模式豁免：全部保留，由调用方标记可疑
		}
	}

	kept := make([]*model.Knowledge, 0, len(candidates))
	for _, c := range candidates {
		if c == nil {
			continue
		}
		if isFamilyConflict(resolveCandidateFamily(c), isWireless, isWired) {
			continue
		}
		kept = append(kept, c)
	}
	return kept // 严格模式下可能为空切片，调用方需按"无候选"处理
}

// isSuspectWinner 判定胜出条目是否为"互斥族豁免保留"。
//
// 数学等价性：严格模式下互斥候选必被剔除；宽松模式下互斥候选能够胜出，
// 当且仅当窗口内不存在任何合规候选（即豁免模式生效）。
// 因此该判定只依赖 (胜出条目族, isWireless, isWired)，与"是否命中缓存""来自哪个 Tier"无关，
// 可在任意位置安全复算——这是 suspect 状态的唯一权威来源。
//
// 为什么不由选优函数返回 suspect：exactBestCache 只缓存 *model.Knowledge，
// 缓存命中时选优函数根本不会执行，返回值形式的 suspect 会被静默丢成 false，
// 导致豁免条目以原 Tier、原置信度返回并被写入结果缓存。
func isSuspectWinner(winner *model.Knowledge, isWireless, isWired bool) bool {
	if winner == nil || (!isWireless && !isWired) {
		return false
	}
	return isFamilyConflict(resolveCandidateFamily(winner), isWireless, isWired)
}

// applySuspectCap 对豁免保留的胜出条目降级 Tier 并封顶置信度。
// suspect 为 false 时原样返回，保证合规路径零行为变更。
func applySuspectCap(tier string, conf float64, suspect bool) (string, float64) {
	if !suspect {
		return tier, conf
	}
	if conf > ConfidenceFamilySuspectMax {
		conf = ConfidenceFamilySuspectMax
	}
	return TierFamilySuspect, conf
}

// bestVersionMapping 是"版本贴合度打分"与"最佳版本映射决定"的唯一权威实现。
//
// 返回 (最高得分, 胜出的版本映射)；k 为空或无版本映射时返回 (-1, nil)。
// 同分时按映射 ID 升序裁决，与 FindBestKnowledgeMatchPtr 的 betterCandidate 末位规则保持一致，
// 消除"数据库返回顺序"带来的不确定性。
//
// 注意：调用方需要得到"得分"时用 ScoreKnowledgeVersion，需要得到"文档归属"时用 BestDocumentIDFor，
// 两者必须共用本函数，严禁各自实现一份选优循环（否则会出现打分与落库文档不一致的隐性缺陷）。
func bestVersionMapping(k *model.Knowledge, targetProduct, targetVersion string) (int, *model.KnowledgeVersionMapping) {
	if k == nil || len(k.Versions) == 0 {
		return -1, nil
	}
	bestScore := -1
	var best *model.KnowledgeVersionMapping
	for i := range k.Versions {
		v := &k.Versions[i]
		s := scoreVersion(v.ProductType, v.ProductVersion, targetProduct, targetVersion)
		if s > bestScore || (s == bestScore && (best == nil || v.ID < best.ID)) {
			bestScore = s
			best = v
		}
	}
	return bestScore, best
}

// ScoreKnowledgeVersion 计算单条知识相对目标产品/版本的整体贴合度（取所有版本映射中的最高分）。
// 供 Tier4 做"相关度 + 版本贴合度"加权选优使用 (PARSE-07)。
// 无版本映射的条目返回 ScoreNoVersionFallback（5 分，非 0），保证其不会被误判为"完全不相关"。
func ScoreKnowledgeVersion(k *model.Knowledge, targetProduct, targetVersion string) int {
	if k == nil {
		return 0
	}
	if len(k.Versions) == 0 {
		return ScoreNoVersionFallback
	}
	if s, _ := bestVersionMapping(k, targetProduct, targetVersion); s > 0 {
		return s
	}
	return ScoreCrossProductGeneral
}

// BestDocumentIDFor 复算并返回胜出版本映射对应的产品文档 ID（documents.id）。
//
// 用途：匹配落库时记录"这条日志最终归属哪一篇产品文档"，供展示层/导出直接消费，
// 取代此前"取 Versions 中第一个非空 DocName"的猜测逻辑。
//
// 为什么必须复算而不是让 MatchEngine.Match 返回：
// Match 存在两级缓存短路（bodyKey 结果缓存与 exactBestCache 选优缓存），
// 两者都只携带 *model.Knowledge，无法携带版本映射；而本函数与选优共用 bestVersionMapping，
// 结论与 Match 的选优结果必然一致，且不受缓存命中与否影响，开销为 O(len(Versions))。
//
// 语义边界：Tier4 的知识条目由"相关度 + 版本贴合度"选出，此时本函数返回的是
// "版本贴合度最高的映射"，而非"决定 Tier 选优的那一个"。
func BestDocumentIDFor(k *model.Knowledge, targetProduct, targetVersion string) uint {
	if _, m := bestVersionMapping(k, targetProduct, targetVersion); m != nil {
		return m.DocumentID
	}
	return 0
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

// findBestCore 选优唯一实现（零拷贝指针切片）：先做互斥族过滤（宽松模式），再执行版本/特征加权打分。
//
// isWireless / isWired 由调用方传入：MatchEngine.Match 已按同一对布尔构建选优缓存 Key
// (buildExactBestKey)，此处复用同一对布尔可保证"缓存键语义"与"过滤语义"物理同源，
// 杜绝二者各自计算导致的分叉。
//
// 注意：本函数不返回 suspect。调用方必须在拿到 best 之后统一用 isSuspectWinner 判定，
// 这样才能覆盖 exactBestCache 命中（不执行本函数）的路径。
func findBestCore(candidates []*model.Knowledge, targetProduct, targetVersion string, norm *model.NormalizedLog, isWireless, isWired bool) *model.Knowledge {
	if len(candidates) == 0 {
		return nil
	}

	kept := filterExcludedCandidates(candidates, isWireless, isWired, false)
	if len(kept) == 0 {
		return nil
	}

	targetVersionTrim := strings.TrimSpace(targetVersion)

	var bestMatch *model.Knowledge
	var highestTotalScore = -999

	for _, k := range kept {
		if k == nil {
			continue
		}
		vScore := ScoreKnowledgeVersion(k, targetProduct, targetVersion)
		fScore := calcFeatureScore(norm, k)
		totalScore := vScore + fScore

		if totalScore > highestTotalScore ||
			(totalScore == highestTotalScore && betterCandidate(k, bestMatch, targetVersionTrim)) {
			highestTotalScore = totalScore
			bestMatch = k
		}
	}

	return bestMatch
}

// FindBestKnowledgeMatchPtr 针对指针切片执行高效零拷贝版本与特征加权匹配打分。
// 公开签名保持不变，供引擎与知识服务层既有调用点零改动复用（内部委托 findBestCore）。
func FindBestKnowledgeMatchPtr(candidates []*model.Knowledge, targetProduct, targetVersion string, norm ...*model.NormalizedLog) *model.Knowledge {
	var activeNorm *model.NormalizedLog
	if len(norm) > 0 && norm[0] != nil {
		activeNorm = norm[0]
	}

	var isWireless, isWired bool
	if activeNorm != nil {
		isWireless = IsWirelessLog(activeNorm.MessageBody)
		isWired = IsWiredLog(activeNorm.MessageBody)
	}

	return findBestCore(candidates, targetProduct, targetVersion, activeNorm, isWireless, isWired)
}
