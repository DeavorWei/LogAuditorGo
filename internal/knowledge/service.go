package knowledge

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/hdx"
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
	importMu sync.Mutex
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

// ImportDocumentFromDir 从本地目录导入 HDX 知识库（支持进度追踪 Tracker 回调）
// 支持直接指定单个文档目录，或指定包含多个文档包的父级目录（程序自动递归发现所有文档并批量导入）
// 参数 options 可选传入 conflictMode string（"overwrite" 或 "skip"）以及 tracker *progress.JobTracker
func (s *Service) ImportDocumentFromDir(dirPath string, options ...interface{}) (*ImportStats, error) {
	var tr *progress.JobTracker
	mode := "overwrite"

	for _, opt := range options {
		if str, ok := opt.(string); ok && str != "" {
			mode = str
		} else if tracker, ok := opt.(*progress.JobTracker); ok {
			tr = tracker
		}
	}

	s.importMu.Lock()
	defer s.importMu.Unlock()

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

// importSingleDocUnlocked 执行单个 HDX 文档目录的解析与入库（内部调用，需持有 importMu 锁）
func (s *Service) importSingleDocUnlocked(docRootDir string, conflictMode string, tr *progress.JobTracker) (*ImportStats, error) {
	startTime := time.Now()

	if tr != nil {
		tr.SetStage("META", fmt.Sprintf("正在解析 %s 的 profile.xml 与 navi.xml 元数据...", filepath.Base(docRootDir)))
	}

	// 1. 解析 profile.xml
	doc, naviRelPath, err := hdx.ParseProfileXML(docRootDir)
	if err != nil {
		return nil, fmt.Errorf("parse profile.xml failed: %w", err)
	}

	if tr != nil {
		tr.AddLog("info", "已识别文档: LibID=%s, 产品=%s, 版本=%s, 主题数=%d", doc.LibID, doc.ProductType, doc.ProductVersion, doc.TopicNumber)
	}

	// 检查冲突策略：如果为 skip 且该文档已存在，则直接跳过，避免耗时的并发 HTML 解析
	if conflictMode == "skip" {
		var existingDoc model.Document
		if err := s.db.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; err == nil {
			logger.Log.Infof("[Knowledge Service] Skipping already existing document: LibID=%s, Product=%s %s", doc.LibID, doc.ProductType, doc.ProductVersion)
			return &ImportStats{
				TotalDocuments: 1,
				DocumentID:     existingDoc.ID,
				LibID:          doc.LibID,
				ProductType:    doc.ProductType,
				ProductVersion: doc.ProductVersion,
				Skipped:        true,
			}, nil
		}
	}

	// 2. 解析 navi.xml 提取所有叶子节点
	leafItems, err := hdx.ParseNaviXML(docRootDir, naviRelPath)
	if err != nil {
		return nil, fmt.Errorf("parse navi.xml failed: %w", err)
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
		tr.SetStage("HTML_PARSE", fmt.Sprintf("正在并发解析 %d 个 HTML 知识页面...", len(leafItems)))
		tr.UpdateProgress(0, int64(len(leafItems)), fmt.Sprintf("已解析 0 / %d 个知识页面", len(leafItems)))
	}

	// 3. 并发解析 HTML 文件（根据 CPU 核心数动态自适应协程池）
	type parsedResult struct {
		knowledge *model.Knowledge
		item      hdx.LeafNaviItem
		err       error
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
			defer func() {
				if r := recover(); r != nil {
					logger.Log.Errorf("Recovered in knowledge worker goroutine: %v", r)
				}
			}()
			for item := range jobs {
				k, err := hdx.ParseHTMLKnowledge(docRootDir, item)
				results <- parsedResult{knowledge: k, item: item, err: err}

				if tr != nil {
					cur := atomic.AddInt64(&doneCount, 1)
					if cur%20 == 0 || cur == totalLeafCount {
						tr.UpdateProgress(cur, totalLeafCount, fmt.Sprintf("已并发提取 HTML 知识条目: %d / %d", cur, totalLeafCount))
					}
				}
			}
		}()
	}

	for _, item := range leafItems {
		jobs <- item
	}
	close(jobs)

	wg.Wait()
	close(results)

	var resList []parsedResult
	var hashes []string
	for res := range results {
		if res.err != nil || res.knowledge == nil {
			logger.Log.Debugf("[Knowledge Service] Skip nil or failed item: TopicID=%s, err=%v", res.item.TopicID, res.err)
			continue
		}
		k := res.knowledge
		hash := CalculateContentHash(k)
		k.ContentHash = hash
		resList = append(resList, res)
		hashes = append(hashes, hash)
	}

	if tr != nil {
		tr.SetStage("PERSIST", fmt.Sprintf("正在对 %d 个条目进行全局去重与数据库事务持久化...", len(resList)))
		tr.AddLog("info", "HTML 知识页面解析完成，有效条目 %d，开始事务入库...", len(resList))
	}

	// 4. 批量去重与原子事务入库
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 在事务内处理 Document 记录与旧映射清理
	var existingDoc model.Document
	if err := tx.Where("lib_id = ?", doc.LibID).First(&existingDoc).Error; err == nil {
		if err := tx.Where("document_id = ?", existingDoc.ID).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("delete old version mappings failed: %w", err)
		}
		doc.ID = existingDoc.ID
		if err := tx.Save(doc).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("update existing document failed: %w", err)
		}
	} else {
		if err := tx.Create(doc).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("save document record failed: %w", err)
		}
	}
	stats.DocumentID = doc.ID

	uniqueAdded := 0
	mappingsAdded := 0

	existingKMap := make(map[string]uint)
	for i := 0; i < len(hashes); i += 500 {
		end := i + 500
		if end > len(hashes) {
			end = len(hashes)
		}
		var existing []model.Knowledge
		if err := tx.Where("content_hash IN ?", hashes[i:end]).Find(&existing).Error; err == nil {
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
		if err := tx.CreateInBatches(newKnowledges, 100).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("batch insert knowledge failed: %w", err)
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
		if err := tx.CreateInBatches(newMappings, 100).Error; err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("batch insert mappings failed: %w", err)
		}
		mappingsAdded = len(newMappings)
	}

	// 更新文档中的统计数据
	doc.LogCount = stats.LeafLogCount
	doc.AlarmCount = stats.LeafAlarmCount
	if err := tx.Save(doc).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("update doc counts failed: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("commit transaction failed: %w", err)
	}

	if tr != nil {
		tr.AddLog("info", "数据库入库成功: 新增唯一知识 %d 条, 版本映射 %d 条", uniqueAdded, mappingsAdded)
		tr.SetStage("INDEX", fmt.Sprintf("正在为 %d 条新增知识构建 Bleve 全文检索索引...", len(newKnowledges)))
	}

	// 5. 自动同步建立全文检索索引 (Bleve Index)
	if s.indexer != nil && len(newKnowledges) > 0 {
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
		if err := s.indexer.IndexKnowledge(itemsToIndex); err != nil {
			logger.Log.Warnf("[Knowledge Service] Auto-indexing into Bleve failed: %v", err)
			if tr != nil {
				tr.AddLog("warning", "全文检索索引构建告警: %v", err)
			}
		} else {
			logger.Log.Debugf("[Knowledge Service] Auto-indexed %d new knowledge items to Bleve", len(itemsToIndex))
			if tr != nil {
				tr.AddLog("info", "Bleve 全文检索索引构建完成 (%d 条)", len(itemsToIndex))
			}
		}
	}

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
	if len(candidates) == 0 {
		return nil
	}

	targetProductTrim := strings.TrimSpace(targetProduct)
	targetVersionTrim := strings.TrimSpace(targetVersion)

	var bestMatch *model.Knowledge
	var highestScore int = -1

	for i := range candidates {
		k := &candidates[i]

		if len(k.Versions) == 0 {
			if highestScore < 5 {
				highestScore = 5
				bestMatch = k
			}
			continue
		}

		for _, v := range k.Versions {
			currentScore := 0
			vProductTrim := strings.TrimSpace(v.ProductType)
			vVersionTrim := strings.TrimSpace(v.ProductVersion)

			// 1. 同型号精确匹配 (要求非空)
			if targetProductTrim != "" && strings.EqualFold(vProductTrim, targetProductTrim) {
				currentScore += 100
				if targetVersionTrim != "" && strings.EqualFold(vVersionTrim, targetVersionTrim) {
					currentScore += 50 // 完全精准命中: 150
				} else {
					// 同型号不同版本，偏好较新版本
					currentScore += 20
				}
			} else if targetProductTrim != "" && vProductTrim != "" {
				// 2. 同产品族相近系列匹配 (如 CloudEngine 系列或 USG 系列)
				targetUpper := strings.ToUpper(targetProductTrim)
				vUpper := strings.ToUpper(vProductTrim)
				if (strings.Contains(targetUpper, "CLOUDENGINE") && strings.Contains(vUpper, "CLOUDENGINE")) ||
					(strings.Contains(targetUpper, "USG") && strings.Contains(vUpper, "USG")) ||
					(strings.Contains(targetUpper, "HISECENGINE") && strings.Contains(vUpper, "HISECENGINE")) ||
					(strings.Contains(targetUpper, "NETENGINE") && strings.Contains(vUpper, "NETENGINE")) ||
					(strings.Contains(targetUpper, "CAMPUS") && strings.Contains(vUpper, "CAMPUS")) {
					currentScore += 50
				} else {
					// 3. 跨产品全局通用知识
					currentScore += 10
				}
			} else {
				currentScore += 10
			}

			if currentScore > highestScore {
				highestScore = currentScore
				bestMatch = k
			}
		}
	}

	return bestMatch
}
