package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

type BleveIndexDoc struct {
	ID          string   `json:"id"`
	EntryType   string   `json:"entry_type"`
	Module      string   `json:"module"`
	Severity    float64  `json:"severity"`
	Brief       string   `json:"brief"`
	TrapOID     string   `json:"trap_oid"`
	Message     string   `json:"message"`
	Description string   `json:"description"`
	Cause       string   `json:"cause"`
	Action      string   `json:"action"`
	ProductList []string `json:"product_list"`
	VersionList []string `json:"version_list"`
}

type SearchFilter struct {
	Keyword   string
	Module    string
	Severity  *int
	EntryType string
	Product   string
	Version   string
	Page      int
	PageSize  int
}

type SearchHit struct {
	KnowledgeID uint                `json:"knowledge_id"`
	Score       float64             `json:"score"`
	Document    BleveIndexDoc       `json:"document"`
	Fragments   map[string][]string `json:"fragments,omitempty"`
}

type SearchResult struct {
	Total int64       `json:"total"`
	Hits  []SearchHit `json:"hits"`
}

type Indexer struct {
	index bleve.Index
	mu    sync.RWMutex
}

func buildIndexMapping() mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultAnalyzer = "cjk"

	docMapping := bleve.NewDocumentMapping()

	// 文本字段映射（使用 CJK 中文分词分析器）
	textMapping := bleve.NewTextFieldMapping()
	textMapping.Analyzer = "cjk"
	docMapping.AddFieldMappingsAt("message", textMapping)
	docMapping.AddFieldMappingsAt("description", textMapping)
	docMapping.AddFieldMappingsAt("cause", textMapping)
	docMapping.AddFieldMappingsAt("action", textMapping)

	// 精确检索字段映射
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	docMapping.AddFieldMappingsAt("module", keywordMapping)
	docMapping.AddFieldMappingsAt("brief", keywordMapping)
	docMapping.AddFieldMappingsAt("trap_oid", keywordMapping)
	docMapping.AddFieldMappingsAt("entry_type", keywordMapping)

	// 产品与版本多值检索字段
	prodMapping := bleve.NewTextFieldMapping()
	prodMapping.Analyzer = "cjk"
	docMapping.AddFieldMappingsAt("product_list", prodMapping)
	docMapping.AddFieldMappingsAt("version_list", prodMapping)

	// 数值字段
	numMapping := bleve.NewNumericFieldMapping()
	docMapping.AddFieldMappingsAt("severity", numMapping)

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

var (
	indexerMap = make(map[string]*Indexer)
	indexerMu  sync.Mutex
)

// InitIndexer 初始化或打开 Bleve 索引
func InitIndexer(indexPath string) (*Indexer, error) {
	indexerMu.Lock()
	defer indexerMu.Unlock()

	cleanPath := filepath.Clean(indexPath)
	if existing, exists := indexerMap[cleanPath]; exists && existing != nil && existing.index != nil {
		return existing, nil
	}

	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return nil, fmt.Errorf("create dir for bleve index failed: %w", err)
	}

	index, err := bleve.Open(cleanPath)
	if err == bleve.ErrorIndexPathDoesNotExist {
		indexMapping := buildIndexMapping()
		index, err = bleve.New(cleanPath, indexMapping)
		if err != nil {
			return nil, fmt.Errorf("create bleve index failed: %w", err)
		}
		logger.Log.Infof("Created new Bleve index at: %s", cleanPath)
	} else if err != nil {
		return nil, fmt.Errorf("open bleve index failed: %w", err)
	} else {
		logger.Log.Infof("Opened existing Bleve index at: %s", cleanPath)
	}

	idx := &Indexer{index: index}
	indexerMap[cleanPath] = idx
	return idx, nil
}

// Close 关闭索引
func (idx *Indexer) Close() error {
	if idx == nil {
		return nil
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.index == nil {
		return nil
	}

	indexerMu.Lock()
	for k, v := range indexerMap {
		if v == idx {
			delete(indexerMap, k)
		}
	}
	indexerMu.Unlock()

	err := idx.index.Close()
	idx.index = nil
	return err
}

// IndexKnowledge 批量为知识列表建立全文索引
func (idx *Indexer) IndexKnowledge(items []model.Knowledge) error {
	if idx == nil {
		return fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return fmt.Errorf("indexer is closed")
	}

	batch := idx.index.NewBatch()
	for _, k := range items {
		var products []string
		var versions []string
		for _, v := range k.Versions {
			if v.ProductType != "" {
				products = append(products, v.ProductType)
			}
			if v.ProductVersion != "" {
				versions = append(versions, v.ProductVersion)
			}
		}

		doc := BleveIndexDoc{
			ID:          strconv.Itoa(int(k.ID)),
			EntryType:   string(k.EntryType),
			Module:      strings.ToUpper(strings.TrimSpace(k.Module)),
			Severity:    float64(k.Severity),
			Brief:       strings.TrimSpace(k.Brief),
			TrapOID:     strings.TrimSpace(k.TrapOID),
			Message:     k.Message,
			Description: k.Description,
			Cause:       k.Cause,
			Action:      k.Action,
			ProductList: products,
			VersionList: versions,
		}

		if err := batch.Index(doc.ID, doc); err != nil {
			logger.Log.Warnf("batch index knowledge ID %d failed: %v", k.ID, err)
		}
	}

	if err := idx.index.Batch(batch); err != nil {
		return err
	}

	logger.Log.Debugf("[Bleve Indexer] Successfully indexed batch of %d knowledge items into Bleve", len(items))
	return nil
}

// Search 执行多字段检索
func (idx *Indexer) Search(filter SearchFilter) (*SearchResult, error) {
	if idx == nil {
		return nil, fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return nil, fmt.Errorf("indexer is closed")
	}

	var queries []query.Query

	// 1. 关键词查询
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		matchQuery := bleve.NewMatchQuery(keyword)
		queries = append(queries, matchQuery)
	}

	// 2. 模块过滤
	if mod := strings.TrimSpace(filter.Module); mod != "" {
		termQuery := bleve.NewTermQuery(strings.ToUpper(mod))
		termQuery.SetField("module")
		queries = append(queries, termQuery)
	}

	// 3. 级别过滤
	if filter.Severity != nil {
		min := float64(1)
		max := float64(*filter.Severity)
		numRangeQuery := bleve.NewNumericRangeQuery(&min, &max)
		numRangeQuery.SetField("severity")
		queries = append(queries, numRangeQuery)
	}

	// 4. 类型过滤
	if entryType := strings.TrimSpace(filter.EntryType); entryType != "" {
		termQuery := bleve.NewTermQuery(strings.ToUpper(entryType))
		termQuery.SetField("entry_type")
		queries = append(queries, termQuery)
	}

	// 5. 产品过滤
	if prod := strings.TrimSpace(filter.Product); prod != "" {
		matchProduct := bleve.NewMatchQuery(prod)
		matchProduct.SetField("product_list")
		queries = append(queries, matchProduct)
	}

	// 6. 版本过滤
	if ver := strings.TrimSpace(filter.Version); ver != "" {
		matchVersion := bleve.NewMatchQuery(ver)
		matchVersion.SetField("version_list")
		queries = append(queries, matchVersion)
	}

	var rootQuery query.Query
	if len(queries) == 0 {
		rootQuery = bleve.NewMatchAllQuery()
	} else if len(queries) == 1 {
		rootQuery = queries[0]
	} else {
		conjQuery := bleve.NewConjunctionQuery(queries...)
		rootQuery = conjQuery
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	from := (page - 1) * pageSize

	searchRequest := bleve.NewSearchRequestOptions(rootQuery, pageSize, from, false)
	searchRequest.Highlight = bleve.NewHighlight()

	searchResult, err := idx.index.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search bleve failed: %w", err)
	}

	var hits []SearchHit
	for _, hit := range searchResult.Hits {
		kid, _ := strconv.Atoi(hit.ID)
		hits = append(hits, SearchHit{
			KnowledgeID: uint(kid),
			Score:       hit.Score,
			Fragments:   hit.Fragments,
		})
	}

	logger.Log.Debugf("[Bleve Indexer] Search executed: keyword='%s', mod='%s', type='%s', total=%d, hits=%d, took=%v",
		filter.Keyword, filter.Module, filter.EntryType, searchResult.Total, len(hits), searchResult.Took)

	return &SearchResult{
		Total: int64(searchResult.Total),
		Hits:  hits,
	}, nil
}
