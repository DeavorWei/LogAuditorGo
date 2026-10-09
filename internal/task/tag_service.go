package task

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

var (
	// BatchTagStages 批量打标流水线阶段定义
	BatchTagStages = []progress.StageDef{
		{Key: "tagging", Name: "批量打标处理"},
	}
	// BatchUntagStages 批量摘标流水线阶段定义
	BatchUntagStages = []progress.StageDef{
		{Key: "untagging", Name: "批量摘标处理"},
	}
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

// CountMatchingLogs 计算满足筛选条件的日志总数
func (s *Service) CountMatchingLogs(taskID string, req model.LogQueryRequestBody) (int64, error) {
	if !isValidTaskID(taskID) {
		return 0, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	baseQuery := taskDB.Model(&model.LogRecord{})
	query, err := BuildLogFilterScope(baseQuery, req)
	if err != nil {
		return 0, err
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// BatchTagLogs 按高级筛选条件与简单条件组合批量打标
func (s *Service) BatchTagLogs(taskID string, tagID uint, req model.LogQueryRequestBody, matchedTotal int64, tracker *progress.JobTracker) (int64, int64, error) {
	if tracker != nil {
		tracker.SetStage("tagging", "准备批量打标...")
	}

	if !isValidTaskID(taskID) {
		err := fmt.Errorf("invalid task id: %s", taskID)
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, 0, err
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	// 确认标签存在
	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, 0, err
	}
	if tagCount == 0 {
		if tracker != nil {
			tracker.Fail(ErrTagNotFound)
		}
		return 0, 0, ErrTagNotFound
	}

	baseQuery := taskDB.Model(&model.LogRecord{})
	query, err := BuildLogFilterScope(baseQuery, req)
	if err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, 0, err
	}

	// 若未透传预计算总量 (matchedTotal <= 0)，则在服务内动态统计
	if matchedTotal <= 0 {
		if err := query.Count(&matchedTotal).Error; err != nil {
			if tracker != nil {
				tracker.Fail(err)
			}
			return 0, 0, err
		}
	}
	if matchedTotal == 0 {
		if tracker != nil {
			tracker.UpdateProgress(0, 0, "未匹配到任何日志")
			tracker.Complete(map[string]any{"tagged_count": 0, "matched_total": 0})
		}
		return 0, 0, nil
	}

	// 单次批量操作上限 50 万条，防止超长事务独占锁
	const maxBatchLimit = 500000
	if matchedTotal > maxBatchLimit {
		if tracker != nil {
			tracker.Fail(ErrBatchLimitExceeded)
		}
		return 0, matchedTotal, ErrBatchLimitExceeded
	}

	// 当数据量 > 50,000 或显式传入 tracker 时，采用分批处理（每批 20,000 条），
	// 并在每批次之间短暂睡眠出让 SQLite 写锁，防止长事务阻塞并发导入
	const batchChunkSize = 20000
	if matchedTotal > 50000 || (tracker != nil && matchedTotal > batchChunkSize) {
		var totalTagged int64
		for offset := int64(0); offset < matchedTotal; offset += batchChunkSize {
			if tracker != nil && tracker.IsCanceled() {
				return totalTagged, matchedTotal, fmt.Errorf("batch tag canceled")
			}
			chunkQuery := query.Session(&gorm.Session{}).Select("id").Order("id ASC").Limit(int(batchChunkSize)).Offset(int(offset))
			res := taskDB.Exec(
				"INSERT OR IGNORE INTO log_tag_relations (tag_id, log_id, created_at) SELECT ?, id, ? FROM (?)",
				tagID, time.Now(), chunkQuery,
			)
			if res.Error != nil {
				if tracker != nil {
					tracker.Fail(res.Error)
				}
				return totalTagged, matchedTotal, fmt.Errorf("batch tag insert chunk failed: %w", res.Error)
			}
			totalTagged += res.RowsAffected
			processed := offset + batchChunkSize
			if processed > matchedTotal {
				processed = matchedTotal
			}
			if tracker != nil {
				tracker.UpdateProgress(processed, matchedTotal, fmt.Sprintf("已处理 %d / %d 条日志", processed, matchedTotal))
			}
			time.Sleep(2 * time.Millisecond) // 出让锁窗口
		}

		// 循环结束后进行 sanity 校验，若存在并发导入/删除导致较大漂移 (>10%)，记录告警
		var finalCount int64
		if err := query.Count(&finalCount).Error; err == nil && matchedTotal > 0 {
			diff := finalCount - matchedTotal
			if diff > 1000 || diff < -1000 {
				driftRatio := math.Abs(float64(diff)) / float64(matchedTotal)
				if driftRatio > 0.10 {
					logger.Log.Warnf("[BatchTagLogs] task %s tag %d: concurrency drift detected > 10%% (initial: %d, final: %d, tagged: %d)",
						taskID, tagID, matchedTotal, finalCount, totalTagged)
				}
			}
		}

		if tracker != nil {
			tracker.Complete(map[string]any{"tagged_count": totalTagged, "matched_total": matchedTotal}, "批量打标完成")
		}
		return totalTagged, matchedTotal, nil
	}

	// 5 万条以内走高效单条 SQL 批量插入并幂等去重
	subQuery := query.Select("id")
	res := taskDB.Exec(
		"INSERT OR IGNORE INTO log_tag_relations (tag_id, log_id, created_at) SELECT ?, id, ? FROM (?)",
		tagID, time.Now(), subQuery,
	)
	if res.Error != nil {
		if tracker != nil {
			tracker.Fail(res.Error)
		}
		return 0, matchedTotal, fmt.Errorf("batch tag insert failed: %w", res.Error)
	}
	if tracker != nil {
		tracker.UpdateProgress(matchedTotal, matchedTotal, "批量打标完成")
		tracker.Complete(map[string]any{"tagged_count": res.RowsAffected, "matched_total": matchedTotal})
	}

	return res.RowsAffected, matchedTotal, nil
}

// BatchUntagLogs 按筛选条件批量摘标
func (s *Service) BatchUntagLogs(taskID string, tagID uint, req model.LogQueryRequestBody, matchedTotal int64, tracker *progress.JobTracker) (int64, error) {
	if tracker != nil {
		tracker.SetStage("untagging", "准备批量摘标...")
	}

	if !isValidTaskID(taskID) {
		err := fmt.Errorf("invalid task id: %s", taskID)
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, err
	}
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, err
	}
	defer storage.ReleaseTaskDB(taskID)

	var tagCount int64
	if err := taskDB.Model(&model.LogTag{}).Where("id = ?", tagID).Count(&tagCount).Error; err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, err
	}
	if tagCount == 0 {
		if tracker != nil {
			tracker.Fail(ErrTagNotFound)
		}
		return 0, ErrTagNotFound
	}

	baseQuery := taskDB.Model(&model.LogRecord{})
	query, err := BuildLogFilterScope(baseQuery, req)
	if err != nil {
		if tracker != nil {
			tracker.Fail(err)
		}
		return 0, err
	}

	if matchedTotal <= 0 {
		if err := query.Count(&matchedTotal).Error; err != nil {
			if tracker != nil {
				tracker.Fail(err)
			}
			return 0, err
		}
	}
	if matchedTotal == 0 {
		if tracker != nil {
			tracker.UpdateProgress(0, 0, "未匹配到任何日志")
			tracker.Complete(map[string]any{"untagged_count": 0, "matched_total": 0})
		}
		return 0, nil
	}
	const maxBatchLimit = 500000
	if matchedTotal > maxBatchLimit {
		if tracker != nil {
			tracker.Fail(ErrBatchLimitExceeded)
		}
		return 0, ErrBatchLimitExceeded
	}

	const batchChunkSize = 20000
	if matchedTotal > 50000 || (tracker != nil && matchedTotal > batchChunkSize) {
		var totalUntagged int64
		for offset := int64(0); offset < matchedTotal; offset += batchChunkSize {
			if tracker != nil && tracker.IsCanceled() {
				return totalUntagged, fmt.Errorf("batch untag canceled")
			}
			chunkQuery := query.Session(&gorm.Session{}).Select("id").Order("id ASC").Limit(int(batchChunkSize)).Offset(int(offset))
			res := taskDB.Exec(
				"DELETE FROM log_tag_relations WHERE tag_id = ? AND log_id IN (SELECT id FROM (?))",
				tagID, chunkQuery,
			)
			if res.Error != nil {
				if tracker != nil {
					tracker.Fail(res.Error)
				}
				return totalUntagged, fmt.Errorf("batch untag chunk failed: %w", res.Error)
			}
			totalUntagged += res.RowsAffected
			processed := offset + batchChunkSize
			if processed > matchedTotal {
				processed = matchedTotal
			}
			if tracker != nil {
				tracker.UpdateProgress(processed, matchedTotal, fmt.Sprintf("已处理 %d / %d 条日志", processed, matchedTotal))
			}
			time.Sleep(2 * time.Millisecond) // 出让锁窗口
		}

		// 循环结束后进行 sanity 校验，若存在并发导入/删除导致较大漂移 (>10%)，记录告警
		var finalCount int64
		if err := query.Count(&finalCount).Error; err == nil && matchedTotal > 0 {
			diff := finalCount - matchedTotal
			if diff > 1000 || diff < -1000 {
				driftRatio := math.Abs(float64(diff)) / float64(matchedTotal)
				if driftRatio > 0.10 {
					logger.Log.Warnf("[BatchUntagLogs] task %s tag %d: concurrency drift detected > 10%% (initial: %d, final: %d, untagged: %d)",
						taskID, tagID, matchedTotal, finalCount, totalUntagged)
				}
			}
		}

		if tracker != nil {
			tracker.Complete(map[string]any{"untagged_count": totalUntagged, "matched_total": matchedTotal}, "批量摘标完成")
		}
		return totalUntagged, nil
	}

	subQuery := query.Select("id")
	res := taskDB.Exec(
		"DELETE FROM log_tag_relations WHERE tag_id = ? AND log_id IN (?)",
		tagID, subQuery,
	)
	if res.Error != nil {
		if tracker != nil {
			tracker.Fail(res.Error)
		}
		return 0, fmt.Errorf("batch untag failed: %w", res.Error)
	}
	if tracker != nil {
		tracker.UpdateProgress(matchedTotal, matchedTotal, "批量摘标完成")
		tracker.Complete(map[string]any{"untagged_count": res.RowsAffected, "matched_total": matchedTotal})
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
