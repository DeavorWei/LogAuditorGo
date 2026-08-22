package knowledge

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/hdx"
	"logauditorgo/internal/model"
	"logauditorgo/internal/search"
	"logauditorgo/pkg/logger"
)

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
	FailedDocs           []string      `json:"failed_docs,omitempty"`
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

// ImportDocumentFromDir 从本地目录导入 HDX 知识库
// 支持直接指定单个文档目录，或指定包含多个文档包的父级目录（程序自动递归发现所有文档并批量导入）
func (s *Service) ImportDocumentFromDir(dirPath string) (*ImportStats, error) {
	s.importMu.Lock()
	defer s.importMu.Unlock()

	startTime := time.Now()

	docDirs, err := hdx.FindHDXDocDirs(dirPath)
	if err != nil {
		return nil, err
	}

	totalStats := &ImportStats{
		TotalDocuments: len(docDirs),
		ImportedDocs:   make([]string, 0, len(docDirs)),
	}

	var firstDocID uint
	var firstLibID, firstProductType, firstProductVersion string
	var lastErr error
	successCount := 0

	for _, docDir := range docDirs {
		st, err := s.importSingleDocUnlocked(docDir)
		if err != nil {
			logger.Log.Errorf("[Knowledge Service] Failed to import document at %s: %v", docDir, err)
			totalStats.FailedDocs = append(totalStats.FailedDocs, fmt.Sprintf("%s: %v", filepath.Base(docDir), err))
			lastErr = err
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

	if successCount == 0 && lastErr != nil {
		return nil, fmt.Errorf("failed to import any documents: %w", lastErr)
	}

	if successCount == 1 {
		totalStats.DocumentID = firstDocID
		totalStats.LibID = firstLibID
		totalStats.ProductType = firstProductType
		totalStats.ProductVersion = firstProductVersion
	} else {
		totalStats.LibID = fmt.Sprintf("Batch (%d docs)", successCount)
	}

	totalStats.Duration = time.Since(startTime)
	logger.Log.Infof("Completed batch import of %d/%d documents from %s in %v: %d leaf logs, %d leaf alarms, %d unique knowledge added, %d mappings",
		successCount, len(docDirs), dirPath, totalStats.Duration, totalStats.LeafLogCount, totalStats.LeafAlarmCount, totalStats.UniqueKnowledgeAdded, totalStats.VersionMappingsAdded)

	return totalStats, nil
}

// importSingleDocUnlocked 执行单个 HDX 文档目录的解析与入库（内部调用，需持有 importMu 锁）
func (s *Service) importSingleDocUnlocked(docRootDir string) (*ImportStats, error) {
	startTime := time.Now()

	// 1. 解析 profile.xml
	doc, naviRelPath, err := hdx.ParseProfileXML(docRootDir)
	if err != nil {
		return nil, fmt.Errorf("parse profile.xml failed: %w", err)
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

	// 3. 并发解析 HTML 文件
	type parsedResult struct {
		knowledge *model.Knowledge
		item      hdx.LeafNaviItem
		err       error
	}

	workerNum := 16
	jobs := make(chan hdx.LeafNaviItem, len(leafItems))
	results := make(chan parsedResult, len(leafItems))

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
		} else {
			logger.Log.Debugf("[Knowledge Service] Auto-indexed %d new knowledge items to Bleve", len(itemsToIndex))
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
