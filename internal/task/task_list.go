package task

import (
	"fmt"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

// TaskListQuery 案例库 / 任务列表的检索与分页参数。
//
// 设计说明（Phase 0）：
//   - 与 GET /tasks（返回全量数组，供工作台任务选择器复用）分离，
//     避免为了分页而破坏既有契约；
//   - 仅做元数据检索，不跨任务库扫描原始日志（全量日志索引不在本阶段范围）；
//   - 排序字段走白名单，禁止把用户输入直接拼进 ORDER BY。
type TaskListQuery struct {
	Keyword    string
	Status     []string
	CaseStatus []string
	StartFrom  *time.Time
	StartTo    *time.Time
	SortBy     string
	Order      string
	Page       int
	PageSize   int
}

const (
	defaultTaskPageSize = 20
	maxTaskPageSize     = 200
)

// taskStatusSet 允许出现在状态筛选中的取值；未知值由 Handler 直接拒绝。
var taskStatusSet = map[string]bool{
	string(model.TaskStatusPending):    true,
	string(model.TaskStatusProcessing): true,
	string(model.TaskStatusCompleted):  true,
	string(model.TaskStatusFailed):     true,
}

// NormalizeTaskStatuses 清洗逗号分隔的状态列表，返回去重后的合法状态。
// ignored 为无法识别的取值，供 Handler 决定是否拒绝请求。
func NormalizeTaskStatuses(raw string) (statuses []string, ignored []string) {
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		item := strings.ToUpper(strings.TrimSpace(part))
		if item == "" {
			continue
		}
		if !taskStatusSet[item] {
			ignored = append(ignored, item)
			continue
		}
		if !seen[item] {
			seen[item] = true
			statuses = append(statuses, item)
		}
	}
	return statuses, ignored
}

// NormalizeCaseStatuses 清洗逗号分隔的案例状态列表。
func NormalizeCaseStatuses(raw string) (statuses []string, ignored []string) {
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		item := strings.ToLower(strings.TrimSpace(part))
		if item == "" {
			continue
		}
		if !model.CaseStatusSet[item] {
			ignored = append(ignored, item)
			continue
		}
		if !seen[item] {
			seen[item] = true
			statuses = append(statuses, item)
		}
	}
	return statuses, ignored
}

// taskSortColumns 排序白名单：仅允许对列表可见且已建索引的列排序。
var taskSortColumns = map[string]string{
	"start_time":    "start_time",
	"finish_time":   "finish_time",
	"log_count":     "log_count",
	"matched_count": "matched_count",
	"task_name":     "task_name",
}

// QueryTaskList 按检索条件分页返回任务列表。
func (s *Service) QueryTaskList(q TaskListQuery) ([]model.TaskInfo, int64, error) {
	if s == nil || s.globalDB == nil {
		return nil, 0, fmt.Errorf("task service is not initialized")
	}

	query := s.globalDB.Model(&model.TaskInfo{})

	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		pattern := "%" + escapeLikePattern(kw) + "%"
		// 案例检索覆盖：任务名、任务 ID、结论、备注、标签、工单号，
		// 以及 TaskDigest 中的主机名与高频模块（跨任务库检索的辅助索引）。
		query = query.Joins("LEFT JOIN task_digests AS digest ON digest.task_id = task_info.task_id").
			Where(
				"task_info.task_name LIKE ? ESCAPE '\\' OR task_info.task_id LIKE ? ESCAPE '\\' OR "+
					"task_info.conclusion LIKE ? ESCAPE '\\' OR task_info.notes LIKE ? ESCAPE '\\' OR "+
					"task_info.case_tags LIKE ? ESCAPE '\\' OR task_info.external_ref LIKE ? ESCAPE '\\' OR "+
					"digest.hostnames LIKE ? ESCAPE '\\' OR digest.top_modules LIKE ? ESCAPE '\\'",
				pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern,
			)
	}
	if len(q.Status) > 0 {
		query = query.Where("status IN ?", q.Status)
	}
	if len(q.CaseStatus) > 0 {
		query = query.Where("case_status IN ?", q.CaseStatus)
	}
	if q.StartFrom != nil {
		query = query.Where("start_time >= ?", *q.StartFrom)
	}
	if q.StartTo != nil {
		query = query.Where("start_time <= ?", *q.StartTo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count tasks failed: %w", err)
	}

	orderColumn := "start_time"
	if col, ok := taskSortColumns[strings.ToLower(strings.TrimSpace(q.SortBy))]; ok {
		orderColumn = col
	}
	direction := "DESC"
	if strings.EqualFold(strings.TrimSpace(q.Order), "asc") {
		direction = "ASC"
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize < 1 {
		pageSize = defaultTaskPageSize
	}
	if pageSize > maxTaskPageSize {
		pageSize = maxTaskPageSize
	}

	var tasks []model.TaskInfo
	// 次级排序用 task_id 保证翻页稳定（同毫秒创建的任务不会在不同页重复/漏出）
	orderClause := fmt.Sprintf("%s %s, task_info.task_id DESC", orderColumn, direction)
	if err := query.
		Order(orderClause).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&tasks).Error; err != nil {
		return nil, 0, fmt.Errorf("query tasks failed: %w", err)
	}
	if tasks == nil {
		tasks = []model.TaskInfo{}
	}
	return tasks, total, nil
}
