package task

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

var (
	// ErrTagAlreadyExists 标签名称重复
	ErrTagAlreadyExists = errors.New("tag with this name already exists")
	// ErrTagNotFound 标签不存在
	ErrTagNotFound = errors.New("tag not found")
	// ErrLogNotFound 日志不存在
	ErrLogNotFound = errors.New("log record not found")
	// ErrBatchLimitExceeded 批量操作超出单次 50 万条上限
	ErrBatchLimitExceeded = errors.New("batch operation limit exceeded (max 500,000 logs)")
	// ErrRelationNotFound 标签关联不存在
	ErrRelationNotFound = errors.New("tag relation not found")
)

// ListTaskTags 获取任务标签列表及关联的日志计数
func (s *Service) ListTaskTags(taskID string) ([]model.LogTag, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	defer storage.ReleaseTaskDB(taskID)

	var tags []model.LogTag
	err = taskDB.Table("log_tags t").
		Select("t.id, t.name, t.color, t.remark, t.created_at, count(r.id) as log_count").
		Joins("LEFT JOIN log_tag_relations r ON t.id = r.tag_id").
		Group("t.id").
		Order("t.id asc").
		Scan(&tags).Error
	if err != nil {
		return nil, fmt.Errorf("list task tags failed: %w", err)
	}
	return tags, nil
}

// CreateTaskTag 创建标签
func (s *Service) CreateTaskTag(taskID string, req model.TagCreateRequest) (*model.LogTag, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("tag name cannot be empty")
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	defer storage.ReleaseTaskDB(taskID)

	// 重名检查
	var existingCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("name = ?", name).Count(&existingCount).Error; err != nil {
		return nil, err
	}
	if existingCount > 0 {
		return nil, ErrTagAlreadyExists
	}

	color := strings.TrimSpace(req.Color)
	if color == "" {
		color = "#409EFF" // 默认蓝色
	}

	tag := model.LogTag{
		Name:      name,
		Color:     color,
		Remark:    strings.TrimSpace(req.Remark),
		CreatedAt: time.Now(),
	}
	if err := taskDB.Create(&tag).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrTagAlreadyExists
		}
		return nil, fmt.Errorf("create tag failed: %w", err)
	}
	return &tag, nil
}

// UpdateTaskTag 修改标签
func (s *Service) UpdateTaskTag(taskID string, tagID uint, req model.TagUpdateRequest) (*model.LogTag, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, err
	}
	defer storage.ReleaseTaskDB(taskID)

	var tag model.LogTag
	if err := taskDB.First(&tag, tagID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTagNotFound
		}
		return nil, err
	}

	name := strings.TrimSpace(req.Name)
	if name != "" && name != tag.Name {
		var dupCount int64
		if err := taskDB.Model(&model.LogTag{}).Where("name = ? AND id != ?", name, tagID).Count(&dupCount).Error; err != nil {
			return nil, err
		}
		if dupCount > 0 {
			return nil, ErrTagAlreadyExists
		}
		tag.Name = name
	}

	if req.Color != "" {
		tag.Color = strings.TrimSpace(req.Color)
	}
	if req.Remark != "" {
		tag.Remark = strings.TrimSpace(req.Remark)
	}

	if err := taskDB.Save(&tag).Error; err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrTagAlreadyExists
		}
		return nil, fmt.Errorf("update tag failed: %w", err)
	}

	// 附带统计计数
	var count int64
	_ = taskDB.Model(&model.LogTagRelation{}).Where("tag_id = ?", tagID).Count(&count).Error
	tag.LogCount = count

	return &tag, nil
}

// DeleteTaskTag 删除标签并级联清空关联
func (s *Service) DeleteTaskTag(taskID string, tagID uint) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

	return taskDB.Transaction(func(tx *gorm.DB) error {
		var tag model.LogTag
		if err := tx.First(&tag, tagID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTagNotFound
			}
			return err
		}

		// 清理关联表
		if err := tx.Where("tag_id = ?", tagID).Delete(&model.LogTagRelation{}).Error; err != nil {
			return fmt.Errorf("delete tag relations failed: %w", err)
		}
		// 删除标签定义
		if err := tx.Delete(&tag).Error; err != nil {
			return fmt.Errorf("delete tag definition failed: %w", err)
		}
		return nil
	})
}

// BatchTagLogs 按高级筛选条件与简单条件组合批量打标
func (s *Service) BatchTagLogs(taskID string, tagID uint, req model.LogQueryRequestBody) (int64, int64, error) {
	if !isValidTaskID(taskID) {
		return 0, 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return 0, 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	// 确认标签存在
	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		return 0, 0, err
	}
	if tagCount == 0 {
		return 0, 0, ErrTagNotFound
	}

	baseQuery := taskDB.Model(&model.LogRecord{})
	query, err := BuildLogFilterScope(baseQuery, req)
	if err != nil {
		return 0, 0, err
	}

	var matchedTotal int64
	if err := query.Count(&matchedTotal).Error; err != nil {
		return 0, 0, err
	}
	if matchedTotal == 0 {
		return 0, 0, nil
	}

	// 单次批量操作上限 50 万条，防止超长事务独占锁
	const maxBatchLimit = 500000
	if matchedTotal > maxBatchLimit {
		return 0, matchedTotal, ErrBatchLimitExceeded
	}

	// 高效单条 SQL 批量插入并幂等去重 (INSERT OR IGNORE INTO ... SELECT)
	subQuery := query.Select("id")
	res := taskDB.Exec(
		"INSERT OR IGNORE INTO log_tag_relations (tag_id, log_id, created_at) SELECT ?, id, ? FROM (?)",
		tagID, time.Now(), subQuery,
	)
	if res.Error != nil {
		return 0, matchedTotal, fmt.Errorf("batch tag insert failed: %w", res.Error)
	}

	return res.RowsAffected, matchedTotal, nil
}

// BatchUntagLogs 按筛选条件批量摘标
func (s *Service) BatchUntagLogs(taskID string, tagID uint, req model.LogQueryRequestBody) (int64, error) {
	if !isValidTaskID(taskID) {
		return 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		return 0, err
	}
	if tagCount == 0 {
		return 0, ErrTagNotFound
	}

	baseQuery := taskDB.Model(&model.LogRecord{})
	query, err := BuildLogFilterScope(baseQuery, req)
	if err != nil {
		return 0, err
	}

	var matchedTotal int64
	if err := query.Count(&matchedTotal).Error; err != nil {
		return 0, err
	}
	const maxBatchLimit = 500000
	if matchedTotal > maxBatchLimit {
		return 0, ErrBatchLimitExceeded
	}

	subQuery := query.Select("id")
	res := taskDB.Exec(
		"DELETE FROM log_tag_relations WHERE tag_id = ? AND log_id IN (?)",
		tagID, subQuery,
	)
	if res.Error != nil {
		return 0, fmt.Errorf("batch untag failed: %w", res.Error)
	}

	return res.RowsAffected, nil
}

// AddLogTag 单条日志打标
func (s *Service) AddLogTag(taskID string, logID uint, tagID uint) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

	// 确认标签与日志是否存在，防吞真实 DB 错误
	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		return err
	}
	if tagCount == 0 {
		return ErrTagNotFound
	}
	var logCount int64
	if err := taskDB.Model(&model.LogRecord{}).Where("id = ?", logID).Count(&logCount).Error; err != nil {
		return err
	}
	if logCount == 0 {
		return ErrLogNotFound
	}

	return taskDB.Exec(
		"INSERT OR IGNORE INTO log_tag_relations (tag_id, log_id, created_at) VALUES (?, ?, ?)",
		tagID, logID, time.Now(),
	).Error
}

// RemoveLogTag 单条日志摘标
func (s *Service) RemoveLogTag(taskID string, logID uint, tagID uint) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return err
	}
	defer storage.ReleaseTaskDB(taskID)

	// 确认标签与日志是否存在
	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		return err
	}
	if tagCount == 0 {
		return ErrTagNotFound
	}
	var logCount int64
	if err := taskDB.Model(&model.LogRecord{}).Where("id = ?", logID).Count(&logCount).Error; err != nil {
		return err
	}
	if logCount == 0 {
		return ErrLogNotFound
	}

	res := taskDB.Where("tag_id = ? AND log_id = ?", tagID, logID).
		Delete(&model.LogTagRelation{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrRelationNotFound
	}
	return nil
}
