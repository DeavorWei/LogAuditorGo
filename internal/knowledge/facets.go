package knowledge

import (
	"fmt"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

// KnowledgeFacetItem 分面计数项。
type KnowledgeFacetItem struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// KnowledgeFacets 知识库分面（产品/版本/严重度/类型）。
type KnowledgeFacets struct {
	Products   []KnowledgeFacetItem `json:"products"`
	Versions   []KnowledgeFacetItem `json:"versions"`
	Severities []KnowledgeFacetItem `json:"severities"`
	EntryTypes []KnowledgeFacetItem `json:"entry_types"`
}

// GetKnowledgeFacets 计算知识分面。
//
// 口径（方案审视 §3.4）：只受 module/entry_type 约束，关键词不参与分面，
// 避免 Bleve 与 SQL 混合过滤造成计数与结果不一致。
func (s *Service) GetKnowledgeFacets(module, entryType string) (*KnowledgeFacets, error) {
	facets := &KnowledgeFacets{
		Products:   []KnowledgeFacetItem{},
		Versions:   []KnowledgeFacetItem{},
		Severities: []KnowledgeFacetItem{},
		EntryTypes: []KnowledgeFacetItem{},
	}

	base := s.db.Model(&model.Knowledge{})
	if mod := strings.TrimSpace(module); mod != "" {
		base = base.Where("UPPER(module) = ?", strings.ToUpper(mod))
	}
	if entry := strings.ToUpper(strings.TrimSpace(entryType)); entry != "" {
		base = base.Where("entry_type = ?", entry)
	}

	var severityRows []KnowledgeFacetItem
	if err := base.Select("CAST(severity AS TEXT) AS value, COUNT(*) AS count").
		Group("severity").Order("count DESC").Scan(&severityRows).Error; err != nil {
		return nil, fmt.Errorf("aggregate severity facets failed: %w", err)
	}
	facets.Severities = severityRows

	var typeRows []KnowledgeFacetItem
	if err := base.Select("entry_type AS value, COUNT(*) AS count").
		Group("entry_type").Order("count DESC").Scan(&typeRows).Error; err != nil {
		return nil, fmt.Errorf("aggregate entry type facets failed: %w", err)
	}
	facets.EntryTypes = typeRows

	// 产品/版本来自版本映射，受知识过滤条件约束
	mappingQuery := s.db.Model(&model.KnowledgeVersionMapping{}).
		Select("product_type AS value, COUNT(DISTINCT knowledge_id) AS count").
		Where("product_type <> ''").
		Group("product_type").Order("count DESC").Limit(30)
	if mod := strings.TrimSpace(module); mod != "" {
		mappingQuery = mappingQuery.Where("knowledge_id IN (?)",
			s.db.Model(&model.Knowledge{}).Select("id").Where("UPPER(module) = ?", strings.ToUpper(mod)))
	}
	if entry := strings.ToUpper(strings.TrimSpace(entryType)); entry != "" {
		mappingQuery = mappingQuery.Where("knowledge_id IN (?)",
			s.db.Model(&model.Knowledge{}).Select("id").Where("entry_type = ?", entry))
	}
	if err := mappingQuery.Scan(&facets.Products).Error; err != nil {
		return nil, fmt.Errorf("aggregate product facets failed: %w", err)
	}

	versionQuery := s.db.Model(&model.KnowledgeVersionMapping{}).
		Select("product_version AS value, COUNT(DISTINCT knowledge_id) AS count").
		Where("product_version <> ''").
		Group("product_version").Order("count DESC").Limit(50)
	if mod := strings.TrimSpace(module); mod != "" {
		versionQuery = versionQuery.Where("knowledge_id IN (?)",
			s.db.Model(&model.Knowledge{}).Select("id").Where("UPPER(module) = ?", strings.ToUpper(mod)))
	}
	if entry := strings.ToUpper(strings.TrimSpace(entryType)); entry != "" {
		versionQuery = versionQuery.Where("knowledge_id IN (?)",
			s.db.Model(&model.Knowledge{}).Select("id").Where("entry_type = ?", entry))
	}
	if err := versionQuery.Scan(&facets.Versions).Error; err != nil {
		return nil, fmt.Errorf("aggregate version facets failed: %w", err)
	}
	return facets, nil
}

// SetFavorite 收藏/取消收藏知识。
func (s *Service) SetFavorite(knowledgeID uint, favorite bool) error {
	var entity model.Knowledge
	if err := s.db.First(&entity, knowledgeID).Error; err != nil {
		return fmt.Errorf("knowledge not found: %w", err)
	}
	if favorite {
		return s.db.Save(&model.KnowledgeFavorite{KnowledgeID: knowledgeID, CreatedAt: time.Now()}).Error
	}
	return s.db.Delete(&model.KnowledgeFavorite{}, "knowledge_id = ?", knowledgeID).Error
}

// ListFavorites 返回收藏的知识（按收藏时间倒序）。
func (s *Service) ListFavorites() ([]model.Knowledge, error) {
	var rows []model.KnowledgeFavorite
	if err := s.db.Order("created_at DESC").Limit(200).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.KnowledgeID)
	}
	knowledgeMap, err := s.GetKnowledgeMapByIDs(ids)
	if err != nil {
		return nil, err
	}
	result := make([]model.Knowledge, 0, len(ids))
	for _, id := range ids {
		if kb, ok := knowledgeMap[id]; ok && kb != nil {
			result = append(result, *kb)
		}
	}
	return result, nil
}

// RecordRecent 记录最近浏览（最多保留 50 条）。
func (s *Service) RecordRecent(knowledgeID uint) {
	if knowledgeID == 0 {
		return
	}
	_ = s.db.Save(&model.KnowledgeRecent{KnowledgeID: knowledgeID, ViewedAt: time.Now()}).Error
	// 只保留最近 50 条
	var ids []uint
	if err := s.db.Model(&model.KnowledgeRecent{}).
		Order("viewed_at DESC").Offset(50).Limit(200).
		Pluck("knowledge_id", &ids).Error; err == nil && len(ids) > 0 {
		_ = s.db.Delete(&model.KnowledgeRecent{}, "knowledge_id IN ?", ids).Error
	}
}

// ListRecent 返回最近浏览的知识。
func (s *Service) ListRecent(limit int) ([]model.Knowledge, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var rows []model.KnowledgeRecent
	if err := s.db.Order("viewed_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.KnowledgeID)
	}
	knowledgeMap, err := s.GetKnowledgeMapByIDs(ids)
	if err != nil {
		return nil, err
	}
	// 保持最近浏览顺序
	result := make([]model.Knowledge, 0, len(ids))
	for _, id := range ids {
		if kb, ok := knowledgeMap[id]; ok && kb != nil {
			result = append(result, *kb)
		}
	}
	return result, nil
}
