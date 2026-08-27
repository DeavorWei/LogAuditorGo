package matcher

import (
	"regexp"
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

const (
	defaultMatchCacheCap    = 100000
	defaultNegativeCacheCap = 50000
	defaultRegexCacheCap    = 10000
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

	// 运行时结果缓存与负缓存 (LRU Bounded)
	cache         *cache.LRUCache[string, *matchCacheItem]
	negativeCache *cache.LRUCache[string, struct{}]
	regexCache    *cache.LRUCache[string, *regexp.Regexp]
}

func NewMatchEngine(db *gorm.DB, indexer *search.Indexer) *MatchEngine {
	engine := &MatchEngine{
		db:            db,
		indexer:       indexer,
		exactMap:      make(map[string][]*model.Knowledge),
		moduleMap:     make(map[string][]*model.Knowledge),
		idMap:         make(map[uint]*model.Knowledge),
		cache:         cache.NewLRUCache[string, *matchCacheItem](defaultMatchCacheCap),
		negativeCache: cache.NewLRUCache[string, struct{}](defaultNegativeCacheCap),
		regexCache:    cache.NewLRUCache[string, *regexp.Regexp](defaultRegexCacheCap),
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
	if m.negativeCache != nil {
		m.negativeCache.Purge()
	}
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

		for i := range list {
			k := &list[i]
			modKey := strings.ToUpper(strings.TrimSpace(k.Module))
			briefKey := strings.ToUpper(strings.TrimSpace(k.Brief))
			exactKey := modKey + ":" + briefKey

			newExact[exactKey] = append(newExact[exactKey], k)
			newModule[modKey] = append(newModule[modKey], k)
			newID[k.ID] = k
		}

		m.exactMap = newExact
		m.moduleMap = newModule
		m.idMap = newID
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

	// 构造缓存 Key (统一大写避免大小写不一致造成缓存分片)
	cacheKey := module + ":" + upperBrief + ":" + strings.ToUpper(strings.TrimSpace(product)) + ":" + strings.ToUpper(strings.TrimSpace(version))

	// 1. 优先检查精确命中缓存
	if m.cache != nil {
		if item, exists := m.cache.Get(cacheKey); exists && item != nil {
			return item.k, item.tier, item.conf
		}
	}

	// 2. 检查负缓存（Negative Cache Hit）：若此前已确认未匹配，立即返回
	if m.negativeCache != nil {
		if _, exists := m.negativeCache.Get(cacheKey); exists {
			return nil, TierUnmatch, 0.0
		}
	}

	m.ensureIndex()

	m.indexMu.RLock()
	defer m.indexMu.RUnlock()

	// ---------------- Tier 1: EXACT 内存精确匹配 ----------------
	if exactCandidates, ok := m.exactMap[module+":"+upperBrief]; ok && len(exactCandidates) > 0 {
		best := FindBestKnowledgeMatchPtr(exactCandidates, product, version)
		if best != nil {
			if m.cache != nil {
				m.cache.Put(cacheKey, &matchCacheItem{k: best, tier: TierExact, conf: ConfidenceExact})
			}
			return best, TierExact, ConfidenceExact
		}
	}

	// ---------------- Tier 2: MNEMONIC 助记符别名/前后缀匹配 ----------------
	trimmedBrief := brief
	suffixes := []string{"_active", "_clear", "_fail", "_error", "_down", "_up", "_Active", "_Clear", "_Fail"}
	for _, suf := range suffixes {
		if strings.HasSuffix(trimmedBrief, suf) {
			trimmedBrief = strings.TrimSuffix(trimmedBrief, suf)
			break
		}
	}

	trimmedUpper := strings.ToUpper(trimmedBrief)
	if trimmedUpper != "" {
		// 优先查去掉后缀后的 exactMap
		if mnemCandidates, ok := m.exactMap[module+":"+trimmedUpper]; ok && len(mnemCandidates) > 0 {
			best := FindBestKnowledgeMatchPtr(mnemCandidates, product, version)
			if best != nil {
				if m.cache != nil {
					m.cache.Put(cacheKey, &matchCacheItem{k: best, tier: TierMnemonic, conf: ConfidenceMnemonic})
				}
				return best, TierMnemonic, ConfidenceMnemonic
			}
		}

		// 其次扫描该模块下的候选：仅当候选条目剥离对称后缀后与 trimmedBrief 严格一致时才判定为别名命中，严禁随意前缀包含以杜绝反义词条误召回
		if moduleCandidates, ok := m.moduleMap[module]; ok && len(moduleCandidates) > 0 {
			var mnemonicCandidates []*model.Knowledge
			for _, cand := range moduleCandidates {
				candBrief := cand.Brief
				for _, suf := range suffixes {
					if strings.HasSuffix(candBrief, suf) {
						candBrief = strings.TrimSuffix(candBrief, suf)
						break
					}
				}
				if strings.EqualFold(candBrief, trimmedBrief) {
					mnemonicCandidates = append(mnemonicCandidates, cand)
				}
			}
			if len(mnemonicCandidates) > 0 {
				best := FindBestKnowledgeMatchPtr(mnemonicCandidates, product, version)
				if best != nil {
					if m.cache != nil {
						m.cache.Put(cacheKey, &matchCacheItem{k: best, tier: TierMnemonic, conf: ConfidenceMnemonic})
					}
					return best, TierMnemonic, ConfidenceMnemonic
				}
			}
		}
	}

	// ---------------- Tier 3: TEMPLATE 消息模板反向匹配 ----------------
	if norm.MessageBody != "" {
		if moduleCandidates, ok := m.moduleMap[module]; ok && len(moduleCandidates) > 0 {
			var matchedCandidates []*model.Knowledge
			for _, cand := range moduleCandidates {
				if cand.Message != "" && m.matchTemplate(cand.Message, norm.MessageBody) {
					matchedCandidates = append(matchedCandidates, cand)
				}
			}
			if len(matchedCandidates) > 0 {
				best := FindBestKnowledgeMatchPtr(matchedCandidates, product, version)
				if best != nil {
					if m.cache != nil {
						m.cache.Put(cacheKey, &matchCacheItem{k: best, tier: TierTemplate, conf: ConfidenceTemplate})
					}
					return best, TierTemplate, ConfidenceTemplate
				}
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
				best := FindBestKnowledgeMatchPtr(bleveCandidates, product, version)
				if best != nil {
					rawScore := hitScoreMap[best.ID]
					confidence := CalculateConfidence(TierBleve, rawScore)
					if m.cache != nil {
						m.cache.Put(cacheKey, &matchCacheItem{k: best, tier: TierBleve, conf: confidence})
					}
					return best, TierBleve, confidence
				}
			}
		}
	}

	// ---------------- Tier 5: UNMATCHED 负缓存记录 ----------------
	if m.negativeCache != nil {
		m.negativeCache.Put(cacheKey, struct{}{})
	}
	return nil, TierUnmatch, 0.0
}

var templatePlaceholderRegex = regexp.MustCompile(`(?:\\\[.*?\\\]|\\<.*?\\>|%(?:s|d)|\\{.*?\\})`)

// matchTemplate 将知识库中的参数占位符 [Param] / <Param> 转化为通配正则，匹配实际日志正文
func (m *MatchEngine) matchTemplate(template string, actualMsg string) bool {
	if m == nil || template == "" || actualMsg == "" {
		return false
	}

	trimmedTpl := strings.TrimSpace(template)
	if m.regexCache != nil {
		if re, ok := m.regexCache.Get(trimmedTpl); ok && re != nil {
			return re.MatchString(actualMsg)
		}
	}

	reStr := regexp.QuoteMeta(trimmedTpl)
	// 将转义后的占位符 \[.*?\], \<.*?\>, %s, %d, \{.*?\} 统一替换为通配符 .*?
	regexPattern := "(?i)^" + templatePlaceholderRegex.ReplaceAllString(reStr, `.*?`) + "$"
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
