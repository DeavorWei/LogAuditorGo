package search

import (
	"encoding/json"
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

// IndexMappingVersion 索引 mapping 版本号。
//
// KB-01/KB-02: 启用 Store=true、brief 多字段、product_list 改 keyword 都是
// **破坏性 mapping 变更**——Bleve 无法对已有索引热改 mapping，必须物理重建。
// 这里用一个哨兵文档把版本号写进索引，打开时比对，
// 版本不一致就明确提示"需要重建索引"，而不是让用户面对一个"能搜但搜不准"的索引。
const IndexMappingVersion = 2

// mappingVersionDocID 存放 mapping 版本的哨兵文档 ID
const mappingVersionDocID = "__mapping_version__"

// indexBatchSize 单批索引提交条数上限
const indexBatchSize = 500

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
	path  string
	mu    sync.RWMutex
}

// buildIndexMapping 构造索引 mapping。
//
// KB-02: 所有文本字段统一 `Store: true`，让 Search 命中即可直接回填完整文档内容，
// 调用方不再需要按 ID 回查数据库（消除 N+1）。
// KB-09: brief 增加 cjk 子字段（brief.cjk），中文助记符不再是"整串精确匹配"；
//        product_list / version_list 改 keyword 分词 + TermQuery，
//        避免 `V800R021C00` 被切成二元碎片导致过滤不精确。
func buildIndexMapping() mapping.IndexMapping {
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultAnalyzer = "cjk"

	docMapping := bleve.NewDocumentMapping()

	// 文本字段映射（使用 CJK 中文分词分析器，并存储原文以便命中即回填）
	textMapping := bleve.NewTextFieldMapping()
	textMapping.Analyzer = "cjk"
	textMapping.Store = true
	docMapping.AddFieldMappingsAt("message", textMapping)
	docMapping.AddFieldMappingsAt("description", textMapping)
	docMapping.AddFieldMappingsAt("cause", textMapping)
	docMapping.AddFieldMappingsAt("action", textMapping)

	// 精确检索字段映射
	keywordMapping := bleve.NewTextFieldMapping()
	keywordMapping.Analyzer = "keyword"
	keywordMapping.Store = true
	docMapping.AddFieldMappingsAt("module", keywordMapping)
	docMapping.AddFieldMappingsAt("trap_oid", keywordMapping)
	docMapping.AddFieldMappingsAt("entry_type", keywordMapping)

	// brief 多字段（multi-field）：
	//   - "brief"      走 keyword，用于精确过滤；
	//   - "brief.cjk"  走 cjk 二元分词，用于中文短语模糊召回。
	// bleve 的 multi-field 通过"同一 property 挂多个 FieldMapping，其中 Named 的那个成为子字段"实现。
	briefKeyword := bleve.NewTextFieldMapping()
	briefKeyword.Analyzer = "keyword"
	briefKeyword.Store = true
	briefCJK := bleve.NewTextFieldMapping()
	briefCJK.Name = "cjk" // 生成子字段 brief.cjk
	briefCJK.Analyzer = "cjk"
	briefCJK.Store = true
	docMapping.AddFieldMappingsAt("brief", briefKeyword, briefCJK)

	// 产品与版本：keyword 精确匹配（需与下面的 TermQuery 配套）
	termListMapping := bleve.NewTextFieldMapping()
	termListMapping.Analyzer = "keyword"
	termListMapping.Store = true
	docMapping.AddFieldMappingsAt("product_list", termListMapping)
	docMapping.AddFieldMappingsAt("version_list", termListMapping)

	// 数值字段
	numMapping := bleve.NewNumericFieldMapping()
	numMapping.Store = true
	docMapping.AddFieldMappingsAt("severity", numMapping)

	indexMapping.DefaultMapping = docMapping
	return indexMapping
}

var (
	indexerMap = make(map[string]*Indexer)
	indexerMu  sync.Mutex
)

// InitIndexer 初始化或打开 Bleve 索引（同一路径复用同一实例）
func InitIndexer(indexPath string) (*Indexer, error) {
	indexerMu.Lock()
	defer indexerMu.Unlock()

	cleanPath := filepath.Clean(indexPath)
	if existing, exists := indexerMap[cleanPath]; exists && existing != nil && existing.index != nil {
		return existing, nil
	}

	idx, err := openIndexerAt(cleanPath)
	if err != nil {
		return nil, err
	}

	indexerMap[cleanPath] = idx
	return idx, nil
}

// openIndexerAt 打开（不存在则创建）指定路径的索引，并校验 mapping 版本
func openIndexerAt(cleanPath string) (*Indexer, error) {
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0755); err != nil {
		return nil, fmt.Errorf("create dir for bleve index failed: %w", err)
	}

	index, openErr := bleve.Open(cleanPath)
	if openErr == bleve.ErrorIndexPathDoesNotExist {
		indexMapping := buildIndexMapping()
		index, openErr = bleve.New(cleanPath, indexMapping)
		if openErr != nil {
			return nil, fmt.Errorf("create bleve index failed: %w", openErr)
		}
		logger.Log.Infof("Created new Bleve index at: %s", cleanPath)
	} else if openErr != nil {
		// 索引目录损坏时 bleve.Open 会失败。这里不自动删除用户数据，
		// 而是给出可执行的修复指引——重建入口（KB-01）会新建一个干净索引。
		return nil, fmt.Errorf("open bleve index at %s failed: %w (if the index is corrupted, delete this directory or trigger a full reindex)", cleanPath, openErr)
	} else {
		logger.Log.Infof("Opened existing Bleve index at: %s", cleanPath)
	}
	if index == nil {
		return nil, fmt.Errorf("bleve index at %s is nil", cleanPath)
	}

	idx := &Indexer{index: index, path: cleanPath}
	idx.ensureMappingVersion()
	return idx, nil
}

// ensureMappingVersion 写入/校验 mapping 版本哨兵文档。
// 版本落后时只打 WARN 提示需要重建，绝不自动重建——
// 重建是重操作，必须显式由用户在 UI 上触发。
func (idx *Indexer) ensureMappingVersion() {
	stored, err := idx.readMappingVersion()
	if err != nil {
		logger.Log.Debugf("[Bleve Indexer] read mapping version failed (assuming fresh index): %v", err)
		stored = 0
	}
	if stored == IndexMappingVersion {
		return
	}
	if err := idx.writeMappingVersion(); err != nil {
		logger.Log.Warnf("[Bleve Indexer] write mapping version failed: %v", err)
		return
	}
	if stored > 0 {
		logger.Log.Warnf("[Bleve Indexer] index mapping is outdated (stored=%d, current=%d). "+
			"Search accuracy may be degraded until a full reindex is performed.", stored, IndexMappingVersion)
	}
}

func (idx *Indexer) readMappingVersion() (int, error) {
	if idx.index == nil {
		return 0, fmt.Errorf("index is closed")
	}
	q := bleve.NewDocIDQuery([]string{mappingVersionDocID})
	req := bleve.NewSearchRequestOptions(q, 1, 0, false)
	req.Fields = []string{"*"}

	res, err := idx.index.Search(req)
	if err != nil || len(res.Hits) == 0 {
		return 0, fmt.Errorf("mapping version doc not found: %v", err)
	}
	if v, ok := res.Hits[0].Fields["mapping_version"]; ok {
		switch n := v.(type) {
		case float64:
			return int(n), nil
		case string:
			if parsed, err := strconv.Atoi(n); err == nil {
				return parsed, nil
			}
		case json.Number:
			if parsed, err := n.Int64(); err == nil {
				return int(parsed), nil
			}
		}
	}
	return 0, fmt.Errorf("mapping version field missing")
}

func (idx *Indexer) writeMappingVersion() error {
	if idx.index == nil {
		return fmt.Errorf("index is closed")
	}
	return idx.index.Index(mappingVersionDocID, map[string]interface{}{
		"mapping_version": IndexMappingVersion,
		"id":              mappingVersionDocID,
	})
}

// IsMappingOutdated 返回索引 mapping 是否落后于当前代码版本（需要重建）
func (idx *Indexer) IsMappingOutdated() bool {
	if idx == nil {
		return false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if idx.index == nil {
		return false
	}
	v, err := idx.readMappingVersion()
	if err != nil {
		return true
	}
	return v != IndexMappingVersion
}

// Path 返回索引所在目录
func (idx *Indexer) Path() string {
	if idx == nil {
		return ""
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.path
}

// DocCount 返回索引内文档总数（不含版本哨兵文档）
func (idx *Indexer) DocCount() (uint64, error) {
	if idx == nil {
		return 0, fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if idx.index == nil {
		return 0, fmt.Errorf("indexer is closed")
	}
	count, err := idx.index.DocCount()
	if err != nil {
		return 0, err
	}
	if count > 0 {
		count-- // 扣除 mapping 版本哨兵文档
	}
	return count, nil
}

// Close 关闭索引
func (idx *Indexer) Close() error {
	if idx == nil {
		return nil
	}

	indexerMu.Lock()
	idx.mu.Lock()

	for k, v := range indexerMap {
		if v == idx {
			delete(indexerMap, k)
		}
	}
	indexerMu.Unlock()

	defer idx.mu.Unlock()

	if idx.index == nil {
		return nil
	}

	err := idx.index.Close()
	idx.index = nil
	return err
}

// IndexKnowledge 批量为知识列表建立全文索引（按 500 条分块提交，防止单批超大导致内存激增 OOM）
func (idx *Indexer) IndexKnowledge(items []model.Knowledge) error {
	if idx == nil {
		return fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return fmt.Errorf("indexer is closed")
	}

	if len(items) == 0 {
		return nil
	}

	totalIndexed, failed := idx.indexChunkLocked(items)
	if failed > 0 {
		// KB-01: 单条索引失败不再只是"打一行 Warn 继续"，必须向上返回聚合错误，
		// 让调用方能标记 index_dirty 并通过重建入口自愈。
		return fmt.Errorf("index knowledge batch: %d/%d items failed", failed, len(items))
	}

	logger.Log.Debugf("[Bleve Indexer] Successfully indexed batch of %d knowledge items into Bleve", totalIndexed)
	return nil
}

// indexChunkLocked 在持锁状态下把一批知识写入索引，返回成功条数与失败条数
func (idx *Indexer) indexChunkLocked(items []model.Knowledge) (indexed, failed int) {
	for i := 0; i < len(items); i += indexBatchSize {
		end := i + indexBatchSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		batch := idx.index.NewBatch()
		for _, k := range chunk {
			doc := BuildIndexDoc(k)
			if err := batch.Index(doc.ID, doc); err != nil {
				logger.Log.Warnf("batch index knowledge ID %d failed: %v", k.ID, err)
				failed++
			}
		}

		if err := idx.index.Batch(batch); err != nil {
			logger.Log.Errorf("[Bleve Indexer] commit index batch failed: %v", err)
			failed += len(chunk)
			continue
		}
		indexed += len(chunk)
	}
	return indexed, failed
}

// BuildIndexDoc 把知识实体转换为 Bleve 索引文档
func BuildIndexDoc(k model.Knowledge) BleveIndexDoc {
	var products []string
	var versions []string
	seenProd := make(map[string]bool)
	seenVer := make(map[string]bool)
	for _, v := range k.Versions {
		if v.ProductType != "" && !seenProd[v.ProductType] {
			seenProd[v.ProductType] = true
			products = append(products, v.ProductType)
		}
		if v.ProductVersion != "" && !seenVer[v.ProductVersion] {
			seenVer[v.ProductVersion] = true
			versions = append(versions, v.ProductVersion)
		}
	}
	return BleveIndexDoc{
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
}

// Delete 从索引中删除指定单个文档 ID
func (idx *Indexer) Delete(docID string) error {
	if idx == nil {
		return fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return fmt.Errorf("indexer is closed")
	}
	return idx.index.Delete(docID)
}

// DeleteBatch 批量从索引中删除多个文档 ID
func (idx *Indexer) DeleteBatch(docIDs []string) error {
	if idx == nil {
		return fmt.Errorf("indexer is nil")
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return fmt.Errorf("indexer is closed")
	}
	if len(docIDs) == 0 {
		return nil
	}

	batch := idx.index.NewBatch()
	for _, id := range docIDs {
		if id != "" {
			batch.Delete(id)
		}
	}
	return idx.index.Batch(batch)
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

	if idx.IsMappingOutdatedLocked() {
		logger.Log.Warnf("[Bleve Indexer] searching an outdated index (mapping v%d required), results may be inaccurate", IndexMappingVersion)
	}

	var queries []query.Query

	// 1. 关键词查询：默认字段与 brief.cjk 子字段任一命中即可。
	// KB-09: 中文助记符原本走 keyword 分析器只能整串精确匹配，
	// 现在补上 cjk 子字段后，"接口 down" 这类中文短语也能召回。
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		defaultMatch := bleve.NewMatchQuery(keyword)
		briefMatch := bleve.NewMatchQuery(keyword)
		briefMatch.SetField("brief.cjk")
		queries = append(queries, bleve.NewDisjunctionQuery(defaultMatch, briefMatch))
	}

	// 2. 模块过滤（keyword 精确匹配）
	if mod := strings.TrimSpace(filter.Module); mod != "" {
		termQuery := bleve.NewTermQuery(strings.ToUpper(mod))
		termQuery.SetField("module")
		queries = append(queries, termQuery)
	}

	// 3. 级别过滤
	//
	// KB-10: 原实现 `min=1, max=*filter.Severity` 是"范围过滤"，
	// 查询 severity=4 实际返回 1~4 全部，与 UI 上"级别：4"的单选语义不符。
	// 产品决策：语义为"等于"。
	//
	// 注意：不能直接用 min = max = 目标值构造 NumericRangeQuery——
	// bleve 的区间搜索器对零宽区间（min == max）不产生任何候选区间，实测返回 0 条。
	// 这里改用开区间 (sev-0.5, sev+0.5)：severity 恒为整数，
	// 因此该区间有且仅有目标级别落入，既精确又规避零宽区间陷阱。
	if filter.Severity != nil {
		center := float64(*filter.Severity)
		min, max := center-0.5, center+0.5
		minInclusive, maxInclusive := false, false
		numRangeQuery := bleve.NewNumericRangeInclusiveQuery(&min, &max, &minInclusive, &maxInclusive)
		numRangeQuery.SetField("severity")
		queries = append(queries, numRangeQuery)
	}

	// 4. 类型过滤
	if entryType := strings.TrimSpace(filter.EntryType); entryType != "" {
		termQuery := bleve.NewTermQuery(strings.ToUpper(entryType))
		termQuery.SetField("entry_type")
		queries = append(queries, termQuery)
	}

	// 5. 产品过滤（keyword 精确匹配，需与 mapping 配套）
	if prod := strings.TrimSpace(filter.Product); prod != "" {
		termQuery := bleve.NewTermQuery(prod)
		termQuery.SetField("product_list")
		queries = append(queries, termQuery)
	}

	// 6. 版本过滤
	if ver := strings.TrimSpace(filter.Version); ver != "" {
		termQuery := bleve.NewTermQuery(ver)
		termQuery.SetField("version_list")
		queries = append(queries, termQuery)
	}

	rootQuery := buildRootQuery(queries)

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
	// KB-02: 请求返回全部已存储字段，命中即带完整文档，调用方无需 N+1 回查
	searchRequest.Fields = []string{"*"}

	searchResult, err := idx.index.Search(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("search bleve failed: %w", err)
	}

	hits := make([]SearchHit, 0, len(searchResult.Hits))
	for _, hit := range searchResult.Hits {
		kid, err := strconv.Atoi(hit.ID)
		if err != nil {
			continue // 跳过 mapping 版本哨兵文档等非知识条目
		}
		hits = append(hits, SearchHit{
			KnowledgeID: uint(kid),
			Score:       hit.Score,
			Document:    docFromFields(hit.Fields),
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

// IsMappingOutdatedLocked 在已持锁状态下判断 mapping 是否过期
func (idx *Indexer) IsMappingOutdatedLocked() bool {
	v, err := idx.readMappingVersion()
	if err != nil {
		return true
	}
	return v != IndexMappingVersion
}

// buildRootQuery 组合查询条件。
// 关键词的两个分支（默认字段 / brief.cjk）之间是"或"，其余过滤条件之间是"且"。
func buildRootQuery(queries []query.Query) query.Query {
	if len(queries) == 0 {
		return bleve.NewMatchAllQuery()
	}
	if len(queries) == 1 {
		return queries[0]
	}
	return bleve.NewConjunctionQuery(queries...)
}

// docFromFields 把 Bleve 返回的 Fields 映射还原为强类型索引文档。
//
// KB-02: 这是 SearchHit.Document 终于能被填充的关键。
// 字段缺失时安全降级为零值，绝不会因为类型断言失败而 panic。
func docFromFields(fields map[string]interface{}) BleveIndexDoc {
	var doc BleveIndexDoc
	if len(fields) == 0 {
		return doc
	}
	doc.ID = stringField(fields["id"])
	doc.EntryType = stringField(fields["entry_type"])
	doc.Module = stringField(fields["module"])
	doc.Brief = stringField(fields["brief"])
	doc.TrapOID = stringField(fields["trap_oid"])
	doc.Message = stringField(fields["message"])
	doc.Description = stringField(fields["description"])
	doc.Cause = stringField(fields["cause"])
	doc.Action = stringField(fields["action"])
	doc.Severity = floatField(fields["severity"])
	doc.ProductList = stringSliceField(fields["product_list"])
	doc.VersionList = stringSliceField(fields["version_list"])
	return doc
}

func stringField(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

func floatField(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return f
		}
	}
	return 0
}

// stringSliceField 还原多值字段（product_list / version_list）。
//
// bleve 对"只有一个元素"的多值字段，取回的存储值可能是标量而非数组，
// 因此必须对标量做单元素包装，否则单产品/单版本的知识会丢失版本信息。
func stringSliceField(v interface{}) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, stringField(item))
		}
		return out
	default:
		s := stringField(v)
		if s == "" {
			return nil
		}
		return []string{s}
	}
}
