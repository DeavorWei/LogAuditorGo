package matcher

import (
	"regexp"
	"strings"
	"sync"

	"gorm.io/gorm"

	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
)

type MatchEngine struct {
	db           *gorm.DB
	indexer      *search.Indexer
	knowledgeSvc *knowledge.Service
	cache        sync.Map // 内存精确匹配缓存: "MODULE:BRIEF" -> *model.Knowledge
	regexCache   sync.Map // 模板正则缓存: template -> *regexp.Regexp
}

func NewMatchEngine(db *gorm.DB, indexer *search.Indexer) *MatchEngine {
	return &MatchEngine{
		db:           db,
		indexer:      indexer,
		knowledgeSvc: knowledge.NewService(db),
	}
}

// Match 执行四级流水线知识匹配
func (m *MatchEngine) Match(norm *model.NormalizedLog, product string, version string) (matchedK *model.Knowledge, tier string, conf float64) {
	defer func() {
		if r := recover(); r != nil {
			logger.Log.Errorf("[Matcher] Panic recovered in Match: %v", r)
			matchedK, tier, conf = nil, TierUnmatch, 0.0
		}
	}()

	if m == nil || m.db == nil || norm == nil {
		return nil, TierUnmatch, 0.0
	}

	module := strings.ToUpper(strings.TrimSpace(norm.Module))
	brief := strings.TrimSpace(norm.Brief)

	// ---------------- Tier 1: EXACT 精确匹配 ----------------
	cacheKey := module + ":" + brief + ":" + strings.TrimSpace(product) + ":" + strings.TrimSpace(version)
	if cachedVal, exists := m.cache.Load(cacheKey); exists {
		if cachedK, ok := cachedVal.(*model.Knowledge); ok && cachedK != nil {
			logger.Log.Debugf("[Matcher] Tier 1 (EXACT-Cache Hit): %s -> Knowledge ID %d", cacheKey, cachedK.ID)
			return cachedK, TierExact, 1.0
		}
	}

	var exactCandidates []model.Knowledge
	if err := m.db.Preload("Versions").Where("UPPER(module) = ? AND (brief = ? OR UPPER(brief) = UPPER(?))", module, brief, brief).Find(&exactCandidates).Error; err == nil && len(exactCandidates) > 0 {
		best := m.knowledgeSvc.FindBestKnowledgeMatch(exactCandidates, product, version)
		if best != nil {
			m.cache.Store(cacheKey, best)
			logger.Log.Debugf("[Matcher] Tier 1 (EXACT-DB Hit): %s -> Knowledge ID %d (%s)", cacheKey, best.ID, best.Brief)
			return best, TierExact, 1.0
		}
	}

	// ---------------- Tier 2: MNEMONIC 助记符别名/前后缀匹配 ----------------
	// 常见后缀: _active, _clear, _fail, _error, _down, _up
	trimmedBrief := brief
	suffixes := []string{"_active", "_clear", "_fail", "_error", "_down", "_up", "_Active", "_Clear", "_Fail"}
	for _, suf := range suffixes {
		if strings.HasSuffix(trimmedBrief, suf) {
			trimmedBrief = strings.TrimSuffix(trimmedBrief, suf)
			break
		}
	}

	if trimmedBrief != "" {
		var mnemCandidates []model.Knowledge
		// 严密匹配：精准别名、去除后缀后一致、或严格前缀匹配
		if err := m.db.Preload("Versions").Where("UPPER(module) = ? AND (UPPER(brief) = UPPER(?) OR UPPER(brief) = UPPER(?) OR brief LIKE ?)",
			module, trimmedBrief, brief, trimmedBrief+"%").Find(&mnemCandidates).Error; err == nil && len(mnemCandidates) > 0 {
			best := m.knowledgeSvc.FindBestKnowledgeMatch(mnemCandidates, product, version)
			if best != nil {
				logger.Log.Debugf("[Matcher] Tier 2 (MNEMONIC Hit): %s (trimmed: %s) -> Knowledge ID %d (%s)",
					brief, trimmedBrief, best.ID, best.Brief)
				return best, TierMnemonic, 0.90
			}
		}
	}

	// ---------------- Tier 3: TEMPLATE 消息模板反向匹配 ----------------
	if norm.MessageBody != "" {
		var candidates []model.Knowledge
		if err := m.db.Preload("Versions").Where("UPPER(module) = ? AND message != ''", module).Find(&candidates).Error; err == nil && len(candidates) > 0 {
			var matchedCandidates []model.Knowledge
			for _, cand := range candidates {
				if m.matchTemplate(cand.Message, norm.MessageBody) {
					matchedCandidates = append(matchedCandidates, cand)
				}
			}
			if len(matchedCandidates) > 0 {
				best := m.knowledgeSvc.FindBestKnowledgeMatch(matchedCandidates, product, version)
				if best != nil {
					logger.Log.Debugf("[Matcher] Tier 3 (TEMPLATE Hit): Message matches template of Knowledge ID %d (%s)",
						best.ID, best.Brief)
					return best, TierTemplate, 0.80
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
			var hitIDs []uint
			hitScoreMap := make(map[uint]float64)
			for _, h := range res.Hits {
				if h.Score >= 0.25 {
					hitIDs = append(hitIDs, h.KnowledgeID)
					hitScoreMap[h.KnowledgeID] = h.Score
				}
			}

			if len(hitIDs) > 0 {
				var bleveCandidates []model.Knowledge
				if err := m.db.Preload("Versions").Where("id IN ?", hitIDs).Find(&bleveCandidates).Error; err == nil && len(bleveCandidates) > 0 {
					best := m.knowledgeSvc.FindBestKnowledgeMatch(bleveCandidates, product, version)
					if best != nil {
						rawScore := hitScoreMap[best.ID]
						confidence := CalculateConfidence(TierBleve, rawScore)
						logger.Log.Debugf("[Matcher] Tier 4 (BLEVE Hit): score=%.3f, conf=%.3f -> Knowledge ID %d (%s)",
							rawScore, confidence, best.ID, best.Brief)
						return best, TierBleve, confidence
					}
				}
			}
		}
	}

	// ---------------- Tier 5: UNMATCHED 未匹配 ----------------
	logger.Log.Debugf("[Matcher] Tier 5 (UNMATCHED): %s/%s (Msg: %s)", module, brief, norm.MessageBody)
	return nil, TierUnmatch, 0.0
}

var templatePlaceholderRegex = regexp.MustCompile(`(?:\\\[.*?\\\]|\\<.*?\\>|%(?:s|d)|\\{.*?\\})`)

// matchTemplate 将知识库中的参数占位符 [Param] / <Param> 转化为通配正则，匹配实际日志正文
func (m *MatchEngine) matchTemplate(template string, actualMsg string) bool {
	if m == nil || template == "" || actualMsg == "" {
		return false
	}

	trimmedTpl := strings.TrimSpace(template)
	if cached, ok := m.regexCache.Load(trimmedTpl); ok {
		if re, ok := cached.(*regexp.Regexp); ok && re != nil {
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

	m.regexCache.Store(trimmedTpl, re)
	return re.MatchString(actualMsg)
}
