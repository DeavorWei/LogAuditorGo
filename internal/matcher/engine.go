package matcher

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/cache"
	"logauditorgo/pkg/logger"
)

type matchCacheItem struct {
	k    *model.Knowledge
	tier string
	conf float64
}

// ---------------- 缓存 Key 构造 (PARSE-01 / PARSE-17 / P0-A) ----------------
//
// 关键背景：匹配流水线中 Tier1(EXACT) 与 Tier2(MNEMONIC) 主要依赖 (Module, Brief, Product, Version)。
// 但若同 Module:Brief 下共存有线与无线两种互斥报文（如 USER_OFFLINERESULT），原 baseKey 会导致
// 先到者的匹配结果被后到者盲目复用（缓存串档）。
// 因此我们在 baseKey 中追加粗粒度无线协议特征位 (:W=0 或 :W=1)，在保留跨行高复用率的同时，
// 物理阻断有线与无线报文的缓存污染。
// 各字段用 strconv.Quote 编码，避免产品名/版本号中出现的 ':' 造成跨字段碰撞 (PARSE-17)。

// buildExactBestKey 构建候选选优缓存 Key (闭环 S1 & S5):
// 选优逻辑 (FindBestKnowledgeMatchPtr) 仅依赖 module, brief, product, version 以及是否具备无线/有线协议特征。
// 它与具体日志的正文变动、严重级别完全解耦，可被同一 brief 的海量不同正文日志 100% 跨行复用！
func buildExactBestKey(module, upperBrief, product, version string, isWireless, isWired bool) string {
	wFlag := "0"
	if isWireless {
		wFlag = "1"
	}
	wiredFlag := "0"
	if isWired {
		wiredFlag = "1"
	}
	return strconv.Quote(module) + ":" + strconv.Quote(upperBrief) + ":" +
		strconv.Quote(strings.ToUpper(strings.TrimSpace(product))) + ":" +
		strconv.Quote(strings.ToUpper(strings.TrimSpace(version))) + ":W=" + wFlag + ":Wired=" + wiredFlag
}

// buildBodyKey 构建正文级终端匹配置信度缓存 Key (闭环 R1 & S5):
// 融合选优特征、严重级别以及正文哈希指纹，用于相同报文秒级回放，同时绝不跨正文污染一致性判定。
func buildBodyKey(bestKey string, severity int, messageBody string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(messageBody))
	return bestKey + ":S=" + strconv.Itoa(severity) + ":" + strconv.FormatUint(h.Sum64(), 36)
}

// ---------------- 助记符后缀归一化 (PARSE-13) ----------------
//
// 华为 brief 既有 IF_DOWN 大写形态，也有 hwBfdSessionDown 驼峰形态；
// 原实现用大小写敏感的小写后缀表，导致大写 brief 永远走不到归一化逻辑；
// 而一旦库中出现小写后缀，又会把 IF_up / IF_down 这类反义词坍缩到同一词干，
// 产生 0.90 高置信的语义反转误配。
// 这里统一先转大写再剥离，并对互斥极性后缀建立黑名单。

// mnemonicStem 剥离状态后缀并返回语义极性：
//   - +1 表示"恢复/正常"（UP / ACTIVE / CLEAR / RESUME）
//   - -1 表示"故障/异常"（DOWN / FAIL / ERROR / INACTIVE）
//   - 0  表示中性（无状态后缀）
func mnemonicStem(brief string) (stem string, polarity int8) {
	upper := strings.ToUpper(strings.TrimSpace(brief))
	// 注意：较长后缀必须先判，避免 _INACTIVE 被 _ACTIVE 抢先匹配
	switch {
	case strings.HasSuffix(upper, "_INACTIVE"):
		return upper[:len(upper)-len("_INACTIVE")], -1
	case strings.HasSuffix(upper, "_ACTIVE"):
		return upper[:len(upper)-len("_ACTIVE")], +1
	case strings.HasSuffix(upper, "_RESUME"):
		return upper[:len(upper)-len("_RESUME")], +1
	case strings.HasSuffix(upper, "_CLEAR"):
		return upper[:len(upper)-len("_CLEAR")], +1
	case strings.HasSuffix(upper, "_ERROR"):
		return upper[:len(upper)-len("_ERROR")], -1
	case strings.HasSuffix(upper, "_FAIL"):
		return upper[:len(upper)-len("_FAIL")], -1
	case strings.HasSuffix(upper, "_DOWN"):
		return upper[:len(upper)-len("_DOWN")], -1
	case strings.HasSuffix(upper, "_UP"):
		return upper[:len(upper)-len("_UP")], +1
	}
	return upper, 0
}

// polarityAllowed 判断候选知识能否与日志 brief 建立助记符别名关系。
// 双方极性都明确且相反时（如 IF_down ↔ IF_up）禁止互配，杜绝语义反转。
func polarityAllowed(logPol, candPol int8) bool {
	return logPol == 0 || candPol == 0 || logPol == candPol
}

const (
	defaultMatchCacheCap     = 100000
	defaultExactBestCacheCap = 50000
	defaultNegativeCacheCap  = 50000
	defaultRegexCacheCap     = 10000
)

type MatchEngine struct {
	db      *gorm.DB
	indexer *search.Indexer

	// 全量内存索引结构
	indexMu   sync.RWMutex
	exactMap  map[string][]*model.Knowledge // Key: UPPER(MODULE) + ":" + UPPER(BRIEF)
	moduleMap map[string][]*model.Knowledge // Key: UPPER(MODULE)
	idMap     map[uint]*model.Knowledge     // Key: ID
	loaded    bool

	// templateBuckets Tier3 模板倒排索引 (PARSE-15)：
	//   UPPER(MODULE) → 模板首 token → 候选知识列表。
	// 只保存 Message 非空的候选（Tier3 的前提），并用 "" 桶收纳"模板以占位符开头"
	// 这类首 token 不确定的模板，保证预筛不会漏召回。
	// 有了它，Tier3 不再需要对模块内全量候选逐条跑正则。
	templateBuckets map[string]map[string][]*model.Knowledge

	// 运行时结果缓存与负缓存 (LRU Bounded)
	cache          *cache.LRUCache[string, *matchCacheItem]
	exactBestCache *cache.LRUCache[string, *model.Knowledge] // 选优结果缓存 (闭环 S1)
	negativeCache  *cache.LRUCache[string, struct{}]
	regexCache     *cache.LRUCache[string, *regexp.Regexp]
}

func NewMatchEngine(db *gorm.DB, indexer *search.Indexer) *MatchEngine {
	engine := &MatchEngine{
		db:             db,
		indexer:        indexer,
		exactMap:       make(map[string][]*model.Knowledge),
		moduleMap:      make(map[string][]*model.Knowledge),
		idMap:          make(map[uint]*model.Knowledge),
		cache:          cache.NewLRUCache[string, *matchCacheItem](defaultMatchCacheCap),
		exactBestCache: cache.NewLRUCache[string, *model.Knowledge](defaultExactBestCacheCap),
		negativeCache:  cache.NewLRUCache[string, struct{}](defaultNegativeCacheCap),
		regexCache:     cache.NewLRUCache[string, *regexp.Regexp](defaultRegexCacheCap),
	}
	if db != nil {
		engine.loadIndexLocked()
	}
	return engine
}

// Reload 重新从数据库加载知识库到内存索引并清空运行时缓存
func (m *MatchEngine) Reload() {
	if m == nil || m.db == nil {
		return
	}
	m.indexMu.Lock()
	defer m.indexMu.Unlock()
	m.loadIndexLocked()
	if m.cache != nil {
		m.cache.Purge()
	}
	if m.exactBestCache != nil {
		m.exactBestCache.Purge()
	}
	if m.negativeCache != nil {
		m.negativeCache.Purge()
	}
	// PARSE-14: 知识库热更新后，模板正则缓存仍指向旧知识的模板串，必须一并清空
	if m.regexCache != nil {
		m.regexCache.Purge()
	}
	// 闭环 S2: 清理产品族解析与兜底记忆化缓存，保证知识库原地热更新后族判定即时生效
	ClearProductFamilyCache()
}

func (m *MatchEngine) loadIndexLocked() {
	if m.db == nil {
		return
	}
	var list []model.Knowledge
	if err := m.db.Preload("Versions").Find(&list).Error; err == nil {
		newExact := make(map[string][]*model.Knowledge, len(list))
		newModule := make(map[string][]*model.Knowledge)
		newID := make(map[uint]*model.Knowledge, len(list))
		// PARSE-15: 同步构建 Tier3 模板倒排索引
		newBuckets := make(map[string]map[string][]*model.Knowledge)

		for i := range list {
			k := &list[i]
			modKey := strings.ToUpper(strings.TrimSpace(k.Module))
			briefKey := strings.ToUpper(strings.TrimSpace(k.Brief))
			exactKey := modKey + ":" + briefKey

			newExact[exactKey] = append(newExact[exactKey], k)
			newModule[modKey] = append(newModule[modKey], k)
			newID[k.ID] = k

			if tpl := strings.TrimSpace(k.Message); tpl != "" {
				bucket := newBuckets[modKey]
				if bucket == nil {
					bucket = make(map[string][]*model.Knowledge)
					newBuckets[modKey] = bucket
				}
				head := templateHeadToken(tpl)
				bucket[head] = append(bucket[head], k)
			}
		}

		m.exactMap = newExact
		m.moduleMap = newModule
		m.idMap = newID
		m.templateBuckets = newBuckets
		if len(list) > 0 {
			m.loaded = true
		}
		logger.Log.Debugf("[Matcher] In-memory knowledge index loaded: %d items", len(list))
	}
}

func (m *MatchEngine) ensureIndex() {
	m.indexMu.RLock()
	if m.loaded && len(m.idMap) > 0 {
		m.indexMu.RUnlock()
		return
	}
	m.indexMu.RUnlock()

	m.indexMu.Lock()
	defer m.indexMu.Unlock()
	if !m.loaded || len(m.idMap) == 0 {
		m.loadIndexLocked()
	}
}

// Match 执行四级流水线知识匹配（纯内存极速检索）
func (m *MatchEngine) Match(norm *model.NormalizedLog, product string, version string) (matchedK *model.Knowledge, tier string, conf float64) {
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Errorf("[Matcher] Panic recovered in Match: %v", r)
			matchedK, tier, conf = nil, TierUnmatch, 0.0
		}
	}()

	if m == nil || norm == nil {
		return nil, TierUnmatch, 0.0
	}

	module := strings.ToUpper(strings.TrimSpace(norm.Module))
	brief := strings.TrimSpace(norm.Brief)
	upperBrief := strings.ToUpper(brief)

	// PARSE-02: 解析失败的兜底行（调用方统一置为 UNKNOWN / UNPARSED）或注释行（COMMENT）不参与匹配，
	// 也不写入任何缓存。否则它们会共享同一条缓存项，一旦被 Bleve 偶然召回，
	// 全部异常行会被批量挂到同一条知识上，产生大面积误判。
	if module == "UNKNOWN" || upperBrief == "UNPARSED" || module == "COMMENT" {
		return nil, TierUnmatch, 0.0
	}

	// PARSE-01 / P0-A / D7: 缓存 Key 分层（详见 buildBaseKey / buildBodyKey 注释）
	isWireless := (norm != nil && IsWirelessLog(norm.MessageBody))
	isWired := (norm != nil && IsWiredLog(norm.MessageBody))
	severity := 0
	if norm != nil {
		severity = norm.Severity
	}
	exactBestKey := buildExactBestKey(module, upperBrief, product, version, isWireless, isWired)
	bodyKey := buildBodyKey(exactBestKey, severity, norm.MessageBody)

	// 1. 查询结果缓存（统一按包含正文指纹与属性维度的 bodyKey 粒度查询，相同报文 0 耗时秒回，闭环 R1）
	if m.cache != nil {
		if item, exists := m.cache.Get(bodyKey); exists && item != nil {
			return item.k, item.tier, item.conf
		}
	}

	// 2. 检查负缓存：按正文粒度记账，避免"首条未命中即永久毒化同类日志"
	if m.negativeCache != nil {
		if _, exists := m.negativeCache.Get(bodyKey); exists {
			return nil, TierUnmatch, 0.0
		}
	}

	m.ensureIndex()

	m.indexMu.RLock()
	defer m.indexMu.RUnlock()

	// ---------------- Tier 1: EXACT 内存精确匹配 ----------------
	//
	// PARSE-06 / S1 优化:
	// 1. 候选选优缓存 (exactBestCache): FindBestKnowledgeMatchPtr 仅依赖 (module, brief, product, version, W, Wired)，
	//    与具体正文和严重级别无关。同 brief 日志的选优结果 100% 共享，消灭 99.9% 的候选打分冷路径开销！
	// 2. 正文一致性逐行校验 (Tier1Consistency): 选优后逐行计算严重级差与模板匹配扣分，达到阈值立即软短路返回；
	//    既彻底杜绝正文间的一致性缓存污染，又将每行开销降至单次轻量校验级别。
	var fallbackK *model.Knowledge
	var fallbackConf float64

	if exactCandidates, ok := m.exactMap[module+":"+upperBrief]; ok && len(exactCandidates) > 0 {
		var best *model.Knowledge
		if m.exactBestCache != nil {
			if cached, ok := m.exactBestCache.Get(exactBestKey); ok {
				best = cached
			}
		}
		if best == nil {
			best = FindBestKnowledgeMatchPtr(exactCandidates, product, version, norm)
			if best != nil && m.exactBestCache != nil {
				m.exactBestCache.Put(exactBestKey, best)
			}
		}

		if best != nil {
			consistency := Tier1Consistency(norm, best, m.matchTemplate)
			if consistency >= MinTier1Confidence {
				conf := ConfidenceExact * consistency
				if m.cache != nil {
					m.cache.Put(bodyKey, &matchCacheItem{k: best, tier: TierExact, conf: conf})
				}
				return best, TierExact, conf
			}
			fallbackK = best
			fallbackConf = ConfidenceExact * consistency
			logger.Log.Debugf("[Matcher] Tier1 candidate '%s' consistency %.2f < %.2f, degrade to lower tiers (module=%s brief=%s)",
				best.Brief, consistency, MinTier1Confidence, module, brief)
		}
	}

	// ---------------- Tier 2: MNEMONIC 助记符别名/前后缀匹配 ----------------
	// 闭环 P1-1: Tier2 的候选召回与选优仅依赖 (module, brief词干, product, version, isWireless, isWired)，
	// 与具体日志正文和严重级别解耦，置信度恒为 ConfidenceMnemonic。
	// 若 Tier1 未短路拦截，进入 Tier2 前先查 exactBestKey 跨正文复用，杜绝海量不同正文重复遍历全模块候选集与选优打分！
	if m.cache != nil {
		if item, exists := m.cache.Get(exactBestKey); exists && item != nil && item.tier == TierMnemonic {
			return item.k, item.tier, item.conf
		}
	}

	// PARSE-13: 统一转大写后剥离状态后缀，并对互斥极性后缀建立黑名单
	logStem, logPol := mnemonicStem(brief)
	if logStem != "" {
		// 优先查去掉后缀后的 exactMap (仅当 logStem != upperBrief 时才真正剥离了状态后缀)
		if logStem != upperBrief {
			if mnemCandidates, ok := m.exactMap[module+":"+logStem]; ok && len(mnemCandidates) > 0 {
				best := FindBestKnowledgeMatchPtr(mnemCandidates, product, version, norm)
				if best != nil {
					_, candPol := mnemonicStem(best.Brief)
					if polarityAllowed(logPol, candPol) {
						if m.cache != nil {
							item := &matchCacheItem{k: best, tier: TierMnemonic, conf: ConfidenceMnemonic}
							m.cache.Put(exactBestKey, item)
							m.cache.Put(bodyKey, item)
						}
						return best, TierMnemonic, ConfidenceMnemonic
					}
				}
			}
		}

		// 其次扫描该模块下的候选：仅当候选条目剥离对称后缀后与日志词干严格一致时才判定为别名命中，
		// 严禁随意前缀包含，且极性相反时禁止互配，以杜绝反义词条误召回；
		// 严格排除与日志 brief 完全相同的同名候选，杜绝绕过 Tier1 软短路一致性保护降级。
		if moduleCandidates, ok := m.moduleMap[module]; ok && len(moduleCandidates) > 0 {
			var mnemonicCandidates []*model.Knowledge
			for _, cand := range moduleCandidates {
				if strings.ToUpper(cand.Brief) == upperBrief {
					continue // 排除 Tier1 已处理的同名候选，避免破坏软短路降级
				}
				candStem, candPol := mnemonicStem(cand.Brief)
				if candStem == logStem && polarityAllowed(logPol, candPol) {
					mnemonicCandidates = append(mnemonicCandidates, cand)
				}
			}
			if len(mnemonicCandidates) > 0 {
				best := FindBestKnowledgeMatchPtr(mnemonicCandidates, product, version, norm)
				if best != nil {
					if m.cache != nil {
						item := &matchCacheItem{k: best, tier: TierMnemonic, conf: ConfidenceMnemonic}
						m.cache.Put(exactBestKey, item)
						m.cache.Put(bodyKey, item)
					}
					return best, TierMnemonic, ConfidenceMnemonic
				}
			}
		}
	}

	// ---------------- Tier 3: TEMPLATE 消息模板反向匹配 ----------------
	//
	// PARSE-15: 原实现对模块内全量候选逐条跑正则，
	// IFNET/BGP 这类大模块在百万行日志下会退化成 O(行数 × 模块条目数)。
	// 这里改用首 token 倒排分桶预筛，只对"模板首 token 与正文首 token 一致"
	// 以及"模板以占位符开头（首 token 不确定）"两类候选做正则匹配。
	if norm.MessageBody != "" {
		if matchedCandidates := m.matchTemplateCandidates(module, norm.MessageBody); len(matchedCandidates) > 0 {
			best := FindBestKnowledgeMatchPtr(matchedCandidates, product, version, norm)
			if best != nil {
				// Tier3 依赖正文，必须写入带正文指纹的 bodyKey
				if m.cache != nil {
					m.cache.Put(bodyKey, &matchCacheItem{k: best, tier: TierTemplate, conf: ConfidenceTemplate})
				}
				return best, TierTemplate, ConfidenceTemplate
			}
		}
	}

	// ---------------- Tier 4: BLEVE 语义搜索召回 ----------------
	if m.indexer != nil && (norm.MessageBody != "" || brief != "") {
		queryKeyword := brief
		if norm.MessageBody != "" {
			queryKeyword = brief + " " + norm.MessageBody
		}

		res, err := m.indexer.Search(search.SearchFilter{
			Keyword:  queryKeyword,
			Module:   module,
			PageSize: 10,
		})
		if err == nil && res.Total > 0 && len(res.Hits) > 0 {
			var bleveCandidates []*model.Knowledge
			hitScoreMap := make(map[uint]float64)
			for _, h := range res.Hits {
				if h.Score >= BleveMinScoreThreshold {
					if cand, exists := m.idMap[h.KnowledgeID]; exists {
						bleveCandidates = append(bleveCandidates, cand)
						hitScoreMap[h.KnowledgeID] = h.Score
					}
				}
			}

			if len(bleveCandidates) > 0 {
				// PARSE-07: 原实现只按产品/版本打分选优，完全忽略 Bleve 相关度，
				// 会导致"文本最相关但产品族稍远"的条目被淘汰，反而召回语义无关的条目。
				// 改为 α*归一化相关度 + β*版本贴合度 加权选优。
				maxHit := 0.0
				for _, h := range res.Hits {
					if h.Score > maxHit {
						maxHit = h.Score
					}
				}
				type scored struct {
					k     *model.Knowledge
					score float64
				}
				ranked := make([]scored, 0, len(bleveCandidates))
				for _, cand := range bleveCandidates {
					relevance := NormalizeBleveScore(hitScoreMap[cand.ID], maxHit)
					versionFit := float64(ScoreKnowledgeVersion(cand, product, version)) / float64(ScoreExactProductExactVersion)
					ranked = append(ranked, scored{
						k:     cand,
						score: BleveWeightRelevance*relevance + BleveWeightVersion*versionFit,
					})
				}
				sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

				best := ranked[0].k
				if best != nil {
					confidence := CalculateConfidence(TierBleve, hitScoreMap[best.ID])
					// Tier4 依赖正文，必须写入带正文指纹的 bodyKey
					if m.cache != nil {
						m.cache.Put(bodyKey, &matchCacheItem{k: best, tier: TierBleve, conf: confidence})
					}
					return best, TierBleve, confidence
				}
			}
		}
	}

	// ---------------- Tier 5: 保底降级与负缓存 ----------------
	//
	// PARSE-06: Tier1 候选因一致性不足而未短路时，若 Tier2/3/4 全部落空，
	// 仍然退回该候选并给出被一致性折扣后的置信度。
	// 这比直接判 UNMATCHED 更有诊断价值，同时置信度真实反映了"只是模块+助记符相同"。
	if fallbackK != nil {
		if m.cache != nil {
			m.cache.Put(bodyKey, &matchCacheItem{k: fallbackK, tier: TierExact, conf: fallbackConf})
		}
		return fallbackK, TierExact, fallbackConf
	}

	// 按正文粒度记账：某条正文未命中，不代表同 module:brief 的另一条正文也匹配不上
	if m.negativeCache != nil {
		m.negativeCache.Put(bodyKey, struct{}{})
	}
	return nil, TierUnmatch, 0.0
}

// matchTemplateCandidates 利用首 token 倒排索引预筛 Tier3 候选 (PARSE-15)。
//
// 只测试两类候选：
//  1. 模板首 token 与日志正文首 token 一致的候选（精确桶）；
//  2. 模板以占位符开头的候选（首 token 不确定，必须全测）。
//
// 返回值已按"是否命中模板正则"过滤，调用方无需再判空 Message。
func (m *MatchEngine) matchTemplateCandidates(module, messageBody string) []*model.Knowledge {
	buckets, ok := m.templateBuckets[module]
	if !ok || len(buckets) == 0 {
		return nil
	}

	// templateHeadToken 返回的桶键已统一为大写，与建索引时的键口径一致
	head := templateHeadToken(messageBody)

	var matchedCandidates []*model.Knowledge
	seen := make(map[uint]bool)

	consider := func(cands []*model.Knowledge) {
		for _, cand := range cands {
			if cand == nil || seen[cand.ID] {
				continue
			}
			seen[cand.ID] = true
			if cand.Message != "" && m.matchTemplate(cand.Message, messageBody) {
				matchedCandidates = append(matchedCandidates, cand)
			}
		}
	}

	if head != "" {
		consider(buckets[head])
	}
	// "" 桶存放"模板以占位符开头、首 token 不确定"的模板，必须参与匹配
	consider(buckets[""])

	return matchedCandidates
}

// templateHeadToken 取模板/正文的首个 token（大写）。
// 以占位符（[] <> {} %s %d）开头的模板返回 ""，表示首 token 不确定。
func templateHeadToken(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	switch trimmed[0] {
	case '[', '<', '{', '%':
		return ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	head := strings.Trim(fields[0], ".,:;")
	if head == "" {
		return ""
	}
	// 去掉首 token 中可能的占位符残留，保证桶键稳定
	if idx := strings.IndexAny(head, "[<{"); idx >= 0 {
		head = head[:idx]
	}
	if head == "" {
		return ""
	}
	return strings.ToUpper(head)
}

// placeholderMaskRegex 在 QuoteMeta 之前把各种占位符形态统一替换为哨兵串。
//
// PARSE-05: 原实现的 templatePlaceholderRegex 形如 `\\<.*?\\>`，期望匹配 QuoteMeta 转义后的
// `\<...\>`；但 Go 的 regexp.QuoteMeta 只转义 `\.+*?()|[]{}^$`，**不包含 `<` 和 `>`**，
// 因此 `\<` 永远匹配不上字面量，导致 `<Param>` 风格的知识模板在 Tier3 中 100% 失效。
// 这里改为在转义前先遮盖占位符，彻底规避 QuoteMeta 的字符集差异。
var placeholderMaskRegex = regexp.MustCompile(`<[^<>]*>|\[[^\[\]]*\]|\{[^}]*\}|%(?:s|d)`)

// placeholderSentinel 占位符遮盖哨兵：\x00 不是正则元字符，QuoteMeta 后原样保留，可安全回替
const placeholderSentinel = "\x00PH\x00"

// matchTemplate 将知识库中的参数占位符 [Param] / <Param> 转化为通配正则，匹配实际日志正文
func (m *MatchEngine) matchTemplate(template string, actualMsg string) bool {
	if m == nil || template == "" || actualMsg == "" {
		return false
	}

	trimmedTpl := strings.TrimSpace(template)

	// PARSE-15: LRU 的 Get() 会更新 LRU 队列，必须拿写锁；
	// 32 个 worker 并发匹配时所有 goroutine 串行争抢同一把锁，是实打实的热点。
	// 这里先用 Peek()（只拿读锁，可并发）走快路径，未命中时才升级为 Get/Put。
	if m.regexCache != nil {
		if re, ok := m.regexCache.Peek(trimmedTpl); ok && re != nil {
			return re.MatchString(actualMsg)
		}
		if re, ok := m.regexCache.Get(trimmedTpl); ok && re != nil {
			return re.MatchString(actualMsg)
		}
	}

	// 1) 转义前先把 [Param] / <Param> / {Param} / %s / %d 遮盖成哨兵
	masked := placeholderMaskRegex.ReplaceAllString(trimmedTpl, placeholderSentinel)
	// 2) 对剩余字面量做元字符转义
	reStr := regexp.QuoteMeta(masked)
	// 3) 把哨兵回替为非贪婪通配符（(?s) 允许占位符内容跨行）
	reStr = strings.ReplaceAll(reStr, placeholderSentinel, `(?s:.*?)`)
	regexPattern := "(?i)^" + reStr + "$"
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		logger.Log.Warnf("[Matcher] Compile template regex failed for '%s': %v", template, err)
		return false
	}

	if m.regexCache != nil {
		m.regexCache.Put(trimmedTpl, re)
	}
	return re.MatchString(actualMsg)
}

// ClearCache 清空引擎运行期缓存供测试与配置热重载使用 (闭环 P0-A)
func (m *MatchEngine) ClearCache() {
	m.indexMu.Lock()
	defer m.indexMu.Unlock()
	if m.cache != nil {
		m.cache.Purge()
	}
	if m.negativeCache != nil {
		m.negativeCache.Purge()
	}
}
