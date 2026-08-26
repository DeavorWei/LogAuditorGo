package knowledge

import (
	"fmt"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// HDXImportStages HDX 产品文档知识库导入全流程预设阶段
var HDXImportStages = []progress.StageDef{
	{Key: "UPLOAD", Name: "文件接收与解压"},
	{Key: "SCAN", Name: "扫描文档结构"},
	{Key: "META", Name: "解析导航与元数据"},
	{Key: "HTML_PARSE", Name: "并发解析知识条目"},
	{Key: "PERSIST", Name: "去重与数据库写入"},
	{Key: "INDEX", Name: "构建全文检索索引"},
	{Key: "COMPLETE", Name: "导入完成"},
}

type ImportStats struct {
	TotalDocuments       int           `json:"total_documents"`
	DocumentID           uint          `json:"document_id,omitempty"`
	LibID                string        `json:"lib_id,omitempty"`
	ProductType          string        `json:"product_type,omitempty"`
	ProductVersion       string        `json:"product_version,omitempty"`
	TotalTopicsInProfile int           `json:"total_topics_in_profile"`
	LeafLogCount         int           `json:"leaf_log_count"`
	LeafAlarmCount       int           `json:"leaf_alarm_count"`
	UniqueKnowledgeAdded int           `json:"unique_knowledge_added"`
	VersionMappingsAdded int           `json:"version_mappings_added"`
	Duration             time.Duration `json:"duration"`
	ImportedDocs         []string      `json:"imported_docs,omitempty"`
	SkippedDocs          []string      `json:"skipped_docs,omitempty"`
	FailedDocs           []string      `json:"failed_docs,omitempty"`
	Skipped              bool          `json:"skipped,omitempty"`
}

type Service struct {
	db       *gorm.DB
	indexer  *search.Indexer
	docLocks sync.Map
}

func NewService(db *gorm.DB, indexer ...*search.Indexer) *Service {
	svc := &Service{db: db}
	if len(indexer) > 0 && indexer[0] != nil {
		svc.indexer = indexer[0]
	}
	return svc
}

func (s *Service) SetIndexer(indexer *search.Indexer) {
	s.indexer = indexer
}

func (s *Service) getDocLock(key string) *sync.Mutex {
	actual, _ := s.docLocks.LoadOrStore(key, &sync.Mutex{})
	return actual.(*sync.Mutex)
}

// ImportOption 定义导入配置的 Functional Option
type ImportOption func(*ImportOptions)

// ImportOptions 导入文档配置
type ImportOptions struct {
	ConflictMode string
	Tracker      *progress.JobTracker
}

// WithConflictMode 设置文档冲突处理策略 ("overwrite" 或 "skip")
func WithConflictMode(mode string) ImportOption {
	return func(opts *ImportOptions) {
		if mode != "" {
			opts.ConflictMode = mode
		}
	}
}

// WithTracker 设置导入进度跟踪器 JobTracker
func WithTracker(tracker *progress.JobTracker) ImportOption {
	return func(opts *ImportOptions) {
		opts.Tracker = tracker
	}
}

type parsedResult struct {
	knowledge *model.Knowledge
	item      hdx.LeafNaviItem
	err       error
}

// ImportDocumentFromDir 从本地目录导入 HDX 知识库（支持进度追踪 Tracker 回调）
// 支持直接指定单个文档目录，或指定包含多个文档包的父级目录（程序自动递归发现所有文档并批量导入）
// 参数 options 支持 Functional Options (WithConflictMode, WithTracker) 以及向前兼容的 string / *progress.JobTracker
func (s *Service) ImportDocumentFromDir(dirPath string, options ...interface{}) (*ImportStats, error) {
	opts := &ImportOptions{
		ConflictMode: "overwrite",
		Tracker:      nil,
	}

	for _, opt := range options {
		switch v := opt.(type) {
		case ImportOption:
			if v != nil {
				v(opts)
			}
		case func(*ImportOptions):
			if v != nil {
				v(opts)
			}
		case *ImportOptions:
			if v != nil {
				*opts = *v
			}
		case string:
			if v != "" {
				opts.ConflictMode = v
			}
		case *progress.JobTracker:
			opts.Tracker = v
		}
	}

	tr := opts.Tracker
	mode := opts.ConflictMode

	startTime := time.Now()

	if tr != nil {
		tr.SetStage("SCAN", "正在递归扫描发现 HDX 知识库文档目录...")
	}

	docDirs, err := hdx.FindHDXDocDirs(dirPath)
	if err != nil {
		if tr != nil {
			tr.Fail(err, fmt.Sprintf("扫描 HDX 文档目录失败: %v", err))
		}
		return nil, err
	}

	if tr != nil {
		tr.AddLog("info", "发现 %d 个 HDX 官方文档目录", len(docDirs))
	}

	totalStats := &ImportStats{
		TotalDocuments: len(docDirs),
		ImportedDocs:   make([]string, 0, len(docDirs)),
		SkippedDocs:    make([]string, 0),
		FailedDocs:     make([]string, 0),
	}

	var firstDocID uint
	var firstLibID, firstProductType, firstProductVersion string
	var lastErr error
	successCount := 0

	for docIdx, docDir := range docDirs {
		if tr != nil {
			tr.AddLog("info", "开始处理第 %d/%d 个文档包: %s", docIdx+1, len(docDirs), filepath.Base(docDir))
		}

		st, err := s.importSingleDocUnlocked(docDir, mode, tr)
		if err != nil {
			logger.Log.Errorf("[Knowledge Service] Failed to import document at %s: %v", docDir, err)
			totalStats.FailedDocs = append(totalStats.FailedDocs, fmt.Sprintf("%s: %v", filepath.Base(docDir), err))
			lastErr = err
			if tr != nil {
				tr.AddLog("error", "文档包 %s 解析失败: %v", filepath.Base(docDir), err)
			}
			continue
		}

		if st.Skipped {
			docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
			totalStats.SkippedDocs = append(totalStats.SkippedDocs, docLabel)
			if tr != nil {
				tr.AddLog("warning", "已跳过已存在的文档包: %s", docLabel)
			}
			continue
		}

		successCount++
		if successCount == 1 {
			firstDocID = st.DocumentID
			firstLibID = st.LibID
			firstProductType = st.ProductType
			firstProductVersion = st.ProductVersion
		}

		docLabel := fmt.Sprintf("%s (%s %s)", st.LibID, st.ProductType, st.ProductVersion)
		totalStats.ImportedDocs = append(totalStats.ImportedDocs, docLabel)
		totalStats.TotalTopicsInProfile += st.TotalTopicsInProfile
		totalStats.LeafLogCount += st.LeafLogCount
		totalStats.LeafAlarmCount += st.LeafAlarmCount
		totalStats.UniqueKnowledgeAdded += st.UniqueKnowledgeAdded
		totalStats.VersionMappingsAdded += st.VersionMappingsAdded
	}

	if successCount == 0 && len(totalStats.SkippedDocs) == 0 && lastErr != nil {
		if tr != nil {
			tr.Fail(lastErr, fmt.Sprintf("未能成功导入任何文档: %v", lastErr))
		}
		return nil, fmt.Errorf("failed to import any documents: %w", lastErr)
	}

	if successCount == 1 {
		totalStats.DocumentID = firstDocID
		totalStats.LibID = firstLibID
		totalStats.ProductType = firstProductType
		totalStats.ProductVersion = firstProductVersion
	} else if successCount > 1 {
		totalStats.LibID = fmt.Sprintf("Batch (%d docs)", successCount)
	} else if len(totalStats.SkippedDocs) > 0 {
		totalStats.LibID = fmt.Sprintf("Skipped (%d docs)", len(totalStats.SkippedDocs))
	}

	totalStats.Duration = time.Since(startTime)
	logger.Log.Infof("Completed batch import of %d/%d documents (%d skipped) from %s in %v: %d leaf logs, %d leaf alarms, %d unique knowledge added, %d mappings",
		successCount, len(docDirs), len(totalStats.SkippedDocs), dirPath, totalStats.Duration, totalStats.LeafLogCount, totalStats.LeafAlarmCount, totalStats.UniqueKnowledgeAdded, totalStats.VersionMappingsAdded)

	if tr != nil {
		tr.SetStage("COMPLETE", "HDX 官方产品文档导入已全部完成")
		tr.Complete(totalStats, fmt.Sprintf("导入完成！已成功入库 %d 个文档包，提取叶子日志 %d 条，告警 %d 条，新增知识 %d 条",
			successCount, totalStats.LeafLogCount, totalStats.LeafAlarmCount, totalStats.UniqueKnowledgeAdded))
	}

	return totalStats, nil
}

// readDocumentMetadata 阶段 1：解析 HDX 文档的 profile.xml 与 navi.xml 元数据，并处理 skip 冲突跳过逻辑
func (s *Service) readDocumentMetadata(docRootDir string, conflictMode string, tr *progress.JobTracker) (*model.Document, []hdx.LeafNaviItem, *ImportStats, bool, error) {
	if tr != nil {
		tr.SetStage("META", fmt.Sprintf("正在解析 %s 的 profile.xml 与 navi.xml 元数据...", filepath.Base(docRootDir)))
	}

	// 1. 解析 profile.xml
	doc, naviRelPath, parseErr := hdx.ParseProfileXML(docRootDir)
	if parseErr != nil {
		return nil, nil, nil, false, fmt.Errorf("parse profile.xml failed: %w", parseErr)
	}

	if tr != nil {
		tr.AddLog("info", "已识别文档: LibID=%s, 产品=%s, 版本=%s, 主题数=%d", doc.LibID, doc.ProductType, doc.ProductVersion, doc.TopicNumber)
	}

	// 检查冲突策略：如果为 skip 且该文档已存在，则直接跳过，避免耗时的并发 HTML 解析
	if conflictMode == "skip" {
		var existingDoc model.Document
		if dbErr := s.db.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; dbErr == nil {
			logger.Log.Infof("[Knowledge Service] Skipping already existing document: LibID=%s, Product=%s %s", doc.LibID, doc.ProductType, doc.ProductVersion)
			return doc, nil, &ImportStats{
				TotalDocuments: 1,
				DocumentID:     existingDoc.ID,
				LibID:          doc.LibID,
				ProductType:    doc.ProductType,
				ProductVersion: doc.ProductVersion,
				Skipped:        true,
			}, true, nil
		}
	}

	// 2. 解析 navi.xml 提取所有叶子节点
	leafItems, naviErr := hdx.ParseNaviXML(docRootDir, naviRelPath)
	if naviErr != nil {
		return nil, nil, nil, false, fmt.Errorf("parse navi.xml failed: %w", naviErr)
	}

	stats := &ImportStats{
		TotalDocuments:       1,
		LibID:                doc.LibID,
		ProductType:          doc.ProductType,
		ProductVersion:       doc.ProductVersion,
		TotalTopicsInProfile: doc.TopicNumber,
	}

	// 统计叶子日志与告警数量
	for _, it := range leafItems {
		if it.EntryType == model.EntryTypeLog {
			stats.LeafLogCount++
		} else {
			stats.LeafAlarmCount++
		}
	}

	if tr != nil {
		tr.AddLog("info", "导航树提取完成: 共 %d 个叶子条目 (日志 %d 条, 告警 %d 条)", len(leafItems), stats.LeafLogCount, stats.LeafAlarmCount)
	}

	return doc, leafItems, stats, false, nil
}

// parseHTMLKnowledgeItems 阶段 2：启动并发协程池提取所有 HTML 页面的知识条目并计算 ContentHash
func (s *Service) parseHTMLKnowledgeItems(docRootDir string, leafItems []hdx.LeafNaviItem, tr *progress.JobTracker) ([]parsedResult, []string) {
	if tr != nil {
		tr.SetStage("HTML_PARSE", fmt.Sprintf("正在并发解析 %d 个 HTML 知识页面...", len(leafItems)))
		tr.UpdateProgress(0, int64(len(leafItems)), fmt.Sprintf("已解析 0 / %d 个知识页面", len(leafItems)))
	}

	workerNum := runtime.NumCPU() * 2
	if workerNum < 4 {
		workerNum = 4
	} else if workerNum > 32 {
		workerNum = 32
	}
	jobs := make(chan hdx.LeafNaviItem, len(leafItems))
	results := make(chan parsedResult, len(leafItems))

	var doneCount int64
	totalLeafCount := int64(len(leafItems))

	var wg sync.WaitGroup
	for i := 0; i < workerNum; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				func(it hdx.LeafNaviItem) {
					defer func() {
						if r := recover(); r != nil {
							logger.Log.Errorf("Recovered in knowledge worker goroutine: %v", r)
							results <- parsedResult{
								knowledge: nil,
								item:      it,
								err:       fmt.Errorf("panic parsing %s: %v", it.TopicID, r),
							}
							if tr != nil {
								cur := atomic.AddInt64(&doneCount, 1)
								if cur%20 == 0 || cur == totalLeafCount {
									tr.UpdateProgress(cur, totalLeafCount, fmt.Sprintf("已并发提取 HTML 知识条目: %d / %d", cur, totalLeafCount))
								}
							}
						}
					}()

					k, parseErr := hdx.ParseHTMLKnowledge(docRootDir, it)
					results <- parsedResult{knowledge: k, item: it, err: parseErr}

					if tr != nil {
						cur := atomic.AddInt64(&doneCount, 1)
						if cur%20 == 0 || cur == totalLeafCount {
							tr.UpdateProgress(cur, totalLeafCount, fmt.Sprintf("已并发提取 HTML 知识条目: %d / %d", cur, totalLeafCount))
						}
					}
				}(item)
			}
		}()
	}

	for _, item := range leafItems {
		jobs <- item
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var resList []parsedResult
	var hashes []string
	seenHashes := make(map[string]struct{})
	for res := range results {
		if res.err != nil || res.knowledge == nil {
			logger.Log.Debugf("[Knowledge Service] Skip nil or failed item: TopicID=%s, err=%v", res.item.TopicID, res.err)
			continue
		}
		k := res.knowledge
		hash := CalculateContentHash(k)
		k.ContentHash = hash
		resList = append(resList, res)
		if _, seen := seenHashes[hash]; !seen {
			seenHashes[hash] = struct{}{}
			hashes = append(hashes, hash)
		}
	}

	return resList, hashes
}

// persistKnowledgeAndMappings 阶段 3：执行数据库事务持久化与全局去重入库
func (s *Service) persistKnowledgeAndMappings(tx *gorm.DB, doc *model.Document, resList []parsedResult, uniqueHashes []string, stats *ImportStats) ([]*model.Knowledge, int, int, error) {
	// 在事务内处理 Document 记录与旧映射清理
	var existingDoc model.Document
	if dbErr := tx.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; dbErr == nil {
		if delErr := tx.Where("document_id = ?", existingDoc.ID).Delete(&model.KnowledgeVersionMapping{}).Error; delErr != nil {
			return nil, 0, 0, fmt.Errorf("delete old version mappings failed: %w", delErr)
		}
		doc.ID = existingDoc.ID
		if saveErr := tx.Save(doc).Error; saveErr != nil {
			return nil, 0, 0, fmt.Errorf("update existing document failed: %w", saveErr)
		}
	} else {
		if createErr := tx.Create(doc).Error; createErr != nil {
			return nil, 0, 0, fmt.Errorf("save document record failed: %w", createErr)
		}
	}
	stats.DocumentID = doc.ID

	uniqueAdded := 0
	mappingsAdded := 0

	existingKMap := make(map[string]uint)
	for i := 0; i < len(uniqueHashes); i += 500 {
		end := i + 500
		if end > len(uniqueHashes) {
			end = len(uniqueHashes)
		}
		var existing []model.Knowledge
		if findErr := tx.Where("content_hash IN ?", uniqueHashes[i:end]).Find(&existing).Error; findErr == nil {
			for _, ek := range existing {
				existingKMap[ek.ContentHash] = ek.ID
			}
		}
	}

	var newKnowledges []*model.Knowledge
	for _, res := range resList {
		if _, exists := existingKMap[res.knowledge.ContentHash]; !exists {
			existingKMap[res.knowledge.ContentHash] = 0
			newKnowledges = append(newKnowledges, res.knowledge)
		}
	}

	if len(newKnowledges) > 0 {
		if batchErr := tx.CreateInBatches(newKnowledges, 100).Error; batchErr != nil {
			return nil, 0, 0, fmt.Errorf("batch insert knowledge failed: %w", batchErr)
		}
		for _, nk := range newKnowledges {
			existingKMap[nk.ContentHash] = nk.ID
			uniqueAdded++
		}
	}

	var newMappings []*model.KnowledgeVersionMapping
	for _, res := range resList {
		kid := existingKMap[res.knowledge.ContentHash]
		if kid > 0 {
			vMap := &model.KnowledgeVersionMapping{
				KnowledgeID:    kid,
				DocumentID:     doc.ID,
				TopicID:        res.item.TopicID,
				ProductType:    doc.ProductType,
				ProductVersion: doc.ProductVersion,
				HtmlPath:       res.item.URL,
			}
			newMappings = append(newMappings, vMap)
		}
	}

	if len(newMappings) > 0 {
		if mapErr := tx.CreateInBatches(newMappings, 100).Error; mapErr != nil {
			return nil, 0, 0, fmt.Errorf("batch insert mappings failed: %w", mapErr)
		}
		mappingsAdded = len(newMappings)
	}

	// 更新文档中的统计数据
	doc.LogCount = stats.LeafLogCount
	doc.AlarmCount = stats.LeafAlarmCount
	if docSaveErr := tx.Save(doc).Error; docSaveErr != nil {
		return nil, 0, 0, fmt.Errorf("update doc counts failed: %w", docSaveErr)
	}

	return newKnowledges, uniqueAdded, mappingsAdded, nil
}

// indexNewKnowledgeItems 阶段 4：自动同步建立全文检索索引 (Bleve Index)
func (s *Service) indexNewKnowledgeItems(doc *model.Document, newKnowledges []*model.Knowledge, tr *progress.JobTracker) {
	if s.indexer == nil || len(newKnowledges) == 0 {
		return
	}

	if tr != nil {
		tr.SetStage("INDEX", fmt.Sprintf("正在为 %d 条新增知识构建 Bleve 全文检索索引...", len(newKnowledges)))
	}

	itemsToIndex := make([]model.Knowledge, 0, len(newKnowledges))
	for _, nk := range newKnowledges {
		item := *nk
		if len(item.Versions) == 0 {
			item.Versions = []model.KnowledgeVersionMapping{
				{ProductType: doc.ProductType, ProductVersion: doc.ProductVersion},
			}
		}
		itemsToIndex = append(itemsToIndex, item)
	}

	if idxErr := s.indexer.IndexKnowledge(itemsToIndex); idxErr != nil {
		logger.Log.Warnf("[Knowledge Service] Auto-indexing into Bleve failed: %v", idxErr)
		if tr != nil {
			tr.AddLog("warning", "全文检索索引构建告警: %v", idxErr)
		}
	} else {
		logger.Log.Debugf("[Knowledge Service] Auto-indexed %d new knowledge items to Bleve", len(itemsToIndex))
		if tr != nil {
			tr.AddLog("info", "Bleve 全文检索索引构建完成 (%d 条)", len(itemsToIndex))
		}
	}
}

// importSingleDocUnlocked 执行单个 HDX 文档目录的解析与入库（内部调用，自动获取文档级细粒度锁）
func (s *Service) importSingleDocUnlocked(docRootDir string, conflictMode string, tr *progress.JobTracker) (stats *ImportStats, err error) {
	docMu := s.getDocLock(filepath.Clean(docRootDir))
	docMu.Lock()
	defer docMu.Unlock()

	startTime := time.Now()

	// Stage 1: 解析文档元数据与导航树 (支持 skip 模式快速返回)
	doc, leafItems, stats, skipped, err := s.readDocumentMetadata(docRootDir, conflictMode, tr)
	if err != nil {
		return nil, err
	}
	if skipped {
		return stats, nil
	}

	// Stage 2: 并发解析 HTML 知识页面与内容哈希提取
	resList, uniqueHashes := s.parseHTMLKnowledgeItems(docRootDir, leafItems, tr)

	if tr != nil {
		tr.SetStage("PERSIST", fmt.Sprintf("正在对 %d 个条目进行全局去重与数据库事务持久化...", len(resList)))
		tr.AddLog("info", "HTML 知识页面解析完成，有效条目 %d，开始事务入库...", len(resList))
	}

	// Stage 3: 数据库事务持久化与全局去重入库
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			err = fmt.Errorf("panic during document import persistence: %v\n%s", r, debug.Stack())
			logger.Log.Errorf("[Knowledge Service] %v", err)
		}
	}()

	newKnowledges, uniqueAdded, mappingsAdded, persistErr := s.persistKnowledgeAndMappings(tx, doc, resList, uniqueHashes, stats)
	if persistErr != nil {
		tx.Rollback()
		return nil, persistErr
	}

	if commitErr := tx.Commit().Error; commitErr != nil {
		return nil, fmt.Errorf("commit transaction failed: %w", commitErr)
	}

	if tr != nil {
		tr.AddLog("info", "数据库入库成功: 新增唯一知识 %d 条, 版本映射 %d 条", uniqueAdded, mappingsAdded)
	}

	// Stage 4: 全文检索索引构建 (Bleve Index)
	s.indexNewKnowledgeItems(doc, newKnowledges, tr)

	stats.UniqueKnowledgeAdded = uniqueAdded
	stats.VersionMappingsAdded = mappingsAdded
	stats.Duration = time.Since(startTime)

	logger.Log.Infof("Imported Document %s (%s %s) in %v: %d leaf logs, %d leaf alarms, %d unique knowledge added, %d mappings",
		doc.LibID, doc.ProductType, doc.ProductVersion, stats.Duration, stats.LeafLogCount, stats.LeafAlarmCount, uniqueAdded, mappingsAdded)

	return stats, nil
}

// GetDocumentList 获取所有已导入的文档
func (s *Service) GetDocumentList() ([]model.Document, error) {
	var docs []model.Document
	err := s.db.Order("imported_at desc").Find(&docs).Error
	return docs, err
}

// GetKnowledgeByID 根据 ID 获取单条知识详情
func (s *Service) GetKnowledgeByID(id uint) (*model.Knowledge, error) {
	var k model.Knowledge
	err := s.db.Preload("Versions").First(&k, id).Error
	return &k, err
}

// GetKnowledgeByIDs 根据 ID 列表批量获取知识详情（含 Versions 关联），优化 N+1 查询
func (s *Service) GetKnowledgeByIDs(ids []uint) ([]model.Knowledge, error) {
	if len(ids) == 0 {
		return []model.Knowledge{}, nil
	}
	var list []model.Knowledge
	err := s.db.Preload("Versions").Where("id IN ?", ids).Find(&list).Error
	return list, err
}

// GetKnowledgeMapByIDs 根据 ID 列表批量获取知识详情并组装为 ID->*Knowledge 映射表
func (s *Service) GetKnowledgeMapByIDs(ids []uint) (map[uint]*model.Knowledge, error) {
	if len(ids) == 0 {
		return make(map[uint]*model.Knowledge), nil
	}
	list, err := s.GetKnowledgeByIDs(ids)
	if err != nil {
		return nil, err
	}
	res := make(map[uint]*model.Knowledge, len(list))
	for i := range list {
		res[list[i].ID] = &list[i]
	}
	return res, nil
}

// DeleteDocument 删除文档及关联映射
func (s *Service) DeleteDocument(docID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", docID).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Document{}, docID).Error
	})
}

// FindBestKnowledgeMatch 实现了基于产品型号与版本的智能回退（Fallback）策略
func (s *Service) FindBestKnowledgeMatch(candidates []model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	return matcher.FindBestKnowledgeMatch(candidates, targetProduct, targetVersion)
}

// FindBestKnowledgeMatchPtr 针对指针切片执行高效零拷贝匹配
func (s *Service) FindBestKnowledgeMatchPtr(candidates []*model.Knowledge, targetProduct, targetVersion string) *model.Knowledge {
	return matcher.FindBestKnowledgeMatchPtr(candidates, targetProduct, targetVersion)
}
