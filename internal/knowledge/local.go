package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

// LocalKnowledgeInput 本地知识创建/更新入参。
type LocalKnowledgeInput struct {
	Module         string `json:"module"`
	Brief          string `json:"brief"`
	ProductFamily  string `json:"product_family"`
	ProductVersion string `json:"product_version"`
	Severity       int    `json:"severity"`
	EntryType      string `json:"entry_type"`
	Message        string `json:"message"`
	Description    string `json:"description"`
	Cause          string `json:"cause"`
	Action         string `json:"action"`
	Impact         string `json:"impact"`
	Reference      string `json:"reference"`
}

// LocalCSVHeader 本地知识 CSV 列顺序（导入导出共用）。
var LocalCSVHeader = []string{
	"module", "brief", "product", "version", "severity", "entry_type",
	"message", "description", "cause", "action", "impact", "reference",
}

// localContentHash 本地知识稳定指纹（按 module+brief），保证更新不撞唯一索引。
func localContentHash(module, brief string) string {
	sum := sha256.Sum256([]byte("local|" + strings.ToUpper(strings.TrimSpace(module)) + "|" + strings.TrimSpace(brief)))
	return hex.EncodeToString(sum[:])
}

func normalizeLocalInput(in LocalKnowledgeInput) (LocalKnowledgeInput, error) {
	module := strings.ToUpper(strings.TrimSpace(in.Module))
	brief := strings.TrimSpace(in.Brief)
	if module == "" || brief == "" {
		return in, fmt.Errorf("module and brief are required")
	}
	if in.Severity < 0 || in.Severity > 8 {
		return in, fmt.Errorf("severity must be within 0~8")
	}
	if in.Severity == 0 {
		in.Severity = 4
	}
	entryType := strings.ToUpper(strings.TrimSpace(in.EntryType))
	if entryType == "" {
		entryType = string(model.EntryTypeLog)
	}
	if entryType != string(model.EntryTypeLog) && entryType != string(model.EntryTypeAlarm) {
		return in, fmt.Errorf("entry_type must be LOG or ALARM")
	}
	in.Module = module
	in.Brief = brief
	in.EntryType = entryType
	in.ProductFamily = strings.TrimSpace(in.ProductFamily)
	if in.ProductFamily == "" {
		in.ProductFamily = "通用"
	}
	in.ProductVersion = strings.TrimSpace(in.ProductVersion)
	return in, nil
}

// UpsertLocalKnowledge 新建或更新本地知识（按 module+brief 唯一）。
//
// 本地知识与官方知识同表（source=local），并强制写入一条版本映射，
// 避免被"删除文档后的孤儿知识清理"误删（方案审视 §2.1）。
func (s *Service) UpsertLocalKnowledge(in LocalKnowledgeInput) (*model.Knowledge, error) {
	return s.upsertLocalKnowledge(in, true)
}

// upsertLocalKnowledge 内部实现；indexNow=false 供 CSV 批量导入统一重建索引。
func (s *Service) upsertLocalKnowledge(in LocalKnowledgeInput, indexNow bool) (*model.Knowledge, error) {
	normalized, err := normalizeLocalInput(in)
	if err != nil {
		return nil, err
	}

	var existing model.Knowledge
	findErr := s.db.Where("source = ? AND UPPER(module) = ? AND UPPER(brief) = ?",
		"local", normalized.Module, strings.ToUpper(normalized.Brief)).First(&existing).Error
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("query local knowledge failed: %w", findErr)
	}

	entity := existing
	entity.Source = "local"
	entity.Module = normalized.Module
	entity.Brief = normalized.Brief
	entity.EntryType = model.EntryType(normalized.EntryType)
	entity.Severity = normalized.Severity
	entity.Message = strings.TrimSpace(normalized.Message)
	entity.Description = strings.TrimSpace(normalized.Description)
	entity.Cause = strings.TrimSpace(normalized.Cause)
	entity.Action = strings.TrimSpace(normalized.Action)
	entity.Impact = strings.TrimSpace(normalized.Impact)
	entity.ReferenceURL = strings.TrimSpace(normalized.Reference)
	if entity.ContentHash == "" {
		entity.ContentHash = localContentHash(normalized.Module, normalized.Brief)
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&entity).Error; err != nil {
			return err
		}
		if err := tx.Where("knowledge_id = ?", entity.ID).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			return err
		}
		mapping := model.KnowledgeVersionMapping{
			KnowledgeID:    entity.ID,
			DocumentID:     0,
			ProductType:    normalized.ProductFamily,
			ProductVersion: normalized.ProductVersion,
		}
		return tx.Create(&mapping).Error
	}); err != nil {
		return nil, fmt.Errorf("persist local knowledge failed: %w", err)
	}

	entity.Versions = []model.KnowledgeVersionMapping{{
		KnowledgeID:    entity.ID,
		ProductType:    normalized.ProductFamily,
		ProductVersion: normalized.ProductVersion,
	}}

	if indexNow {
		if s.indexer != nil {
			if err := s.indexer.IndexKnowledge([]model.Knowledge{entity}); err != nil {
				logger.Log.Warnf("[Knowledge] index local knowledge %d failed: %v", entity.ID, err)
			}
		}
		if s.matchEngine != nil {
			s.matchEngine.Reload()
		}
	}
	return &entity, nil
}

// UpdateLocalKnowledge 按 ID 更新本地知识（允许修改 module/brief）。
func (s *Service) UpdateLocalKnowledge(id uint, in LocalKnowledgeInput) (*model.Knowledge, error) {
	var entity model.Knowledge
	if err := s.db.First(&entity, id).Error; err != nil {
		return nil, fmt.Errorf("local knowledge not found: %w", err)
	}
	if entity.Source != "local" {
		return nil, fmt.Errorf("official knowledge cannot be modified via local endpoint")
	}

	normalized, err := normalizeLocalInput(in)
	if err != nil {
		return nil, err
	}
	entity.Module = normalized.Module
	entity.Brief = normalized.Brief
	entity.EntryType = model.EntryType(normalized.EntryType)
	entity.Severity = normalized.Severity
	entity.Message = strings.TrimSpace(normalized.Message)
	entity.Description = strings.TrimSpace(normalized.Description)
	entity.Cause = strings.TrimSpace(normalized.Cause)
	entity.Action = strings.TrimSpace(normalized.Action)
	entity.Impact = strings.TrimSpace(normalized.Impact)
	entity.ReferenceURL = strings.TrimSpace(normalized.Reference)
	entity.ContentHash = localContentHash(normalized.Module, normalized.Brief)

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&entity).Error; err != nil {
			return err
		}
		if err := tx.Where("knowledge_id = ?", entity.ID).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			return err
		}
		return tx.Create(&model.KnowledgeVersionMapping{
			KnowledgeID:    entity.ID,
			DocumentID:     0,
			ProductType:    normalized.ProductFamily,
			ProductVersion: normalized.ProductVersion,
		}).Error
	}); err != nil {
		return nil, err
	}

	entity.Versions = []model.KnowledgeVersionMapping{{
		KnowledgeID:    entity.ID,
		ProductType:    normalized.ProductFamily,
		ProductVersion: normalized.ProductVersion,
	}}
	if s.indexer != nil {
		_ = s.indexer.IndexKnowledge([]model.Knowledge{entity})
	}
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}
	return &entity, nil
}

// ListLocalKnowledge 列出本地知识，支持关键词模糊过滤。
func (s *Service) ListLocalKnowledge(keyword string) ([]model.Knowledge, error) {
	query := s.db.Where("source = ?", "local").Preload("Versions")
	if kw := strings.TrimSpace(keyword); kw != "" {
		// 先转义反斜杠，否则含 \ 的关键词会破坏 LIKE ESCAPE 语义
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(kw)
		pattern := "%" + escaped + "%"
		query = query.Where("module LIKE ? ESCAPE '\\' OR brief LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\'",
			pattern, pattern, pattern)
	}
	result := make([]model.Knowledge, 0)
	if err := query.Order("updated_at DESC, id DESC").Find(&result).Error; err != nil {
		return nil, err
	}
	return result, nil
}

// DeleteLocalKnowledge 删除本地知识（仅允许 source=local）。
func (s *Service) DeleteLocalKnowledge(id uint) error {
	var entity model.Knowledge
	if err := s.db.First(&entity, id).Error; err != nil {
		return fmt.Errorf("knowledge not found: %w", err)
	}
	if entity.Source != "local" {
		return fmt.Errorf("official knowledge cannot be deleted via local endpoint")
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("knowledge_id = ?", id).Delete(&model.KnowledgeVersionMapping{}).Error; err != nil {
			return err
		}
		if err := tx.Where("knowledge_id = ?", id).Delete(&model.KnowledgeOverride{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Knowledge{}, id).Error
	}); err != nil {
		return err
	}

	if s.indexer != nil {
		if err := s.indexer.DeleteBatch([]string{strconv.FormatUint(uint64(id), 10)}); err != nil {
			logger.Log.Warnf("[Knowledge] delete local knowledge from index failed: %v", err)
		}
	}
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}
	return nil
}

// ExportLocalCSV 导出本地知识 CSV（带 UTF-8 BOM，便于 Excel 直接打开）。
func (s *Service) ExportLocalCSV() (string, error) {
	items, err := s.ListLocalKnowledge("")
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(&buf)
	_ = writer.Write(LocalCSVHeader)
	for i := range items {
		k := &items[i]
		product, version := "", ""
		if len(k.Versions) > 0 {
			product = k.Versions[0].ProductType
			version = k.Versions[0].ProductVersion
		}
		_ = writer.Write([]string{
			csvFormulaSafe(k.Module), csvFormulaSafe(k.Brief), csvFormulaSafe(product), csvFormulaSafe(version),
			strconv.Itoa(k.Severity), string(k.EntryType), csvFormulaSafe(k.Message), csvFormulaSafe(k.Description),
			csvFormulaSafe(k.Cause), csvFormulaSafe(k.Action), csvFormulaSafe(k.Impact), csvFormulaSafe(k.ReferenceURL),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// csvFormulaSafe 防止本地知识导出后被 Excel 当作公式执行（= + - @ 开头）。
func csvFormulaSafe(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}

// LocalImportResult CSV 导入结果（逐行容错，不整批失败）。
type LocalImportResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors"`
}

// ImportLocalCSV 从 CSV 文本导入/更新本地知识。
func (s *Service) ImportLocalCSV(content string) (*LocalImportResult, error) {
	content = strings.TrimPrefix(content, "\xEF\xBB\xBF")
	reader := csv.NewReader(strings.NewReader(content))
	reader.FieldsPerRecord = -1

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse csv failed: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("csv is empty")
	}

	result := &LocalImportResult{Errors: []string{}}
	indexed := make([]model.Knowledge, 0)
	start := 0
	if len(rows[0]) > 0 && strings.EqualFold(strings.TrimSpace(rows[0][0]), "module") {
		start = 1
	}

	for i := start; i < len(rows); i++ {
		row := rows[i]
		lineNo := i + 1
		if len(row) < 2 {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 行：列数不足", lineNo))
			continue
		}
		field := func(idx int) string {
			if idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}
		severity := 0
		if raw := field(4); raw != "" {
			if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
				severity = parsed
			}
		}
		input := LocalKnowledgeInput{
			Module:         field(0),
			Brief:          field(1),
			ProductFamily:  field(2),
			ProductVersion: field(3),
			Severity:       severity,
			EntryType:      field(5),
			Message:        field(6),
			Description:    field(7),
			Cause:          field(8),
			Action:         field(9),
			Impact:         field(10),
			Reference:      field(11),
		}
		var existing model.Knowledge
		exists := s.db.Where("source = ? AND UPPER(module) = ? AND UPPER(brief) = ?",
			"local", strings.ToUpper(strings.TrimSpace(input.Module)), strings.ToUpper(strings.TrimSpace(input.Brief))).
			First(&existing).Error == nil

		entity, upsertErr := s.upsertLocalKnowledge(input, false)
		if upsertErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("第 %d 行：%v", lineNo, upsertErr))
			continue
		}
		indexed = append(indexed, *entity)
		if exists {
			result.Updated++
		} else {
			result.Created++
		}
	}

	// 批量导入只在结束时重建一次索引/刷新一次匹配引擎，避免逐行全量 Reload。
	if len(indexed) > 0 {
		if s.indexer != nil {
			if err := s.indexer.IndexKnowledge(indexed); err != nil {
				logger.Log.Warnf("[Knowledge] batch index local knowledge failed: %v", err)
			}
		}
		if s.matchEngine != nil {
			s.matchEngine.Reload()
		}
	}
	return result, nil
}

// CreateOverride 把 module/brief 绑定到已有官方知识。
func (s *Service) CreateOverride(module, brief string, knowledgeID uint) (*model.KnowledgeOverride, error) {
	module = strings.ToUpper(strings.TrimSpace(module))
	brief = strings.TrimSpace(brief)
	if module == "" || brief == "" {
		return nil, fmt.Errorf("module and brief are required")
	}

	var target model.Knowledge
	if err := s.db.First(&target, knowledgeID).Error; err != nil {
		return nil, fmt.Errorf("target knowledge not found: %w", err)
	}
	if target.Source == "local" {
		return nil, fmt.Errorf("local knowledge already matches exactly; override only supports official knowledge")
	}

	var override model.KnowledgeOverride
	err := s.db.Where("UPPER(module) = ? AND UPPER(brief) = ?", module, strings.ToUpper(brief)).First(&override).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	override.Module = module
	override.Brief = brief
	override.KnowledgeID = knowledgeID
	override.Enabled = true
	if err := s.db.Save(&override).Error; err != nil {
		return nil, err
	}

	// 覆盖关系变化后确保目标知识在索引中，并刷新匹配引擎。
	if s.indexer != nil {
		var withVersions model.Knowledge
		if s.db.Preload("Versions").First(&withVersions, knowledgeID).Error == nil {
			_ = s.indexer.IndexKnowledge([]model.Knowledge{withVersions})
		}
	}
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}
	return &override, nil
}

// ListOverrides 列出全部本地覆盖。
func (s *Service) ListOverrides() ([]model.KnowledgeOverride, error) {
	result := make([]model.KnowledgeOverride, 0)
	if err := s.db.Order("updated_at DESC, id DESC").Find(&result).Error; err != nil {
		return nil, err
	}
	return result, nil
}

// SetOverrideEnabled 启用/禁用覆盖。
func (s *Service) SetOverrideEnabled(id uint, enabled bool) error {
	res := s.db.Model(&model.KnowledgeOverride{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("override not found")
	}
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}
	return nil
}

// DeleteOverride 删除覆盖。
func (s *Service) DeleteOverride(id uint) error {
	res := s.db.Delete(&model.KnowledgeOverride{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("override not found")
	}
	if s.matchEngine != nil {
		s.matchEngine.Reload()
	}
	return nil
}
