package task_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
)

// TestQueryTaskListFiltersSortAndPagination 验证案例库检索：关键词、状态、时间范围、排序与分页。
func TestQueryTaskListFiltersSortAndPagination(t *testing.T) {
	globalDB, err := storage.InitKnowledgeDB(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	defer storage.CloseKnowledgeDB()

	svc := task.NewService(globalDB, t.TempDir(), nil, nil)
	base := time.Date(2026, 4, 1, 10, 0, 0, 0, time.Local)
	seed := []model.TaskInfo{
		{TaskID: "task-bgp-00000001", TaskName: "BGP 邻居震荡", Status: model.TaskStatusCompleted, StartTime: base.Add(3 * time.Hour), LogCount: 100, MatchedCount: 80},
		{TaskID: "task-ospf-0000002", TaskName: "OSPF 区域故障", Status: model.TaskStatusFailed, StartTime: base.Add(2 * time.Hour), LogCount: 50, MatchedCount: 10},
		{TaskID: "task-ifnet-0000003", TaskName: "BGP 接口 Down", Status: model.TaskStatusProcessing, StartTime: base.Add(1 * time.Hour), LogCount: 20, MatchedCount: 5},
		{TaskID: "task-empty-0000004", TaskName: "空任务", Status: model.TaskStatusPending, StartTime: base, LogCount: 0, MatchedCount: 0},
	}
	for i := range seed {
		if err := globalDB.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed task failed: %v", err)
		}
	}

	// 1. 关键词（任务名）
	items, total, err := svc.QueryTaskList(task.TaskListQuery{Keyword: "BGP"})
	if err != nil {
		t.Fatalf("query keyword failed: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("keyword BGP: total=%d len=%d, want 2", total, len(items))
	}

	// 2. 状态过滤
	items, total, err = svc.QueryTaskList(task.TaskListQuery{Status: []string{string(model.TaskStatusFailed), string(model.TaskStatusPending)}})
	if err != nil {
		t.Fatalf("query status failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("status filter: total=%d, want 2", total)
	}

	// 3. 时间范围
	from := base.Add(90 * time.Minute)
	to := base.Add(150 * time.Minute)
	items, total, err = svc.QueryTaskList(task.TaskListQuery{StartFrom: &from, StartTo: &to})
	if err != nil {
		t.Fatalf("query time range failed: %v", err)
	}
	if total != 1 || items[0].TaskID != "task-ospf-0000002" {
		t.Fatalf("time range filter: total=%d items=%+v, want ospf task only", total, items)
	}

	// 4. 排序 + 分页（默认 start_time desc）
	items, total, err = svc.QueryTaskList(task.TaskListQuery{Page: 1, PageSize: 2, SortBy: "start_time", Order: "desc"})
	if err != nil {
		t.Fatalf("query pagination failed: %v", err)
	}
	if total != 4 {
		t.Fatalf("pagination total=%d, want 4", total)
	}
	if len(items) != 2 || items[0].TaskID != "task-bgp-00000001" {
		t.Fatalf("pagination page1 = %+v, want newest BGP task first", items)
	}
	page2, _, err := svc.QueryTaskList(task.TaskListQuery{Page: 2, PageSize: 2, SortBy: "start_time", Order: "desc"})
	if err != nil {
		t.Fatalf("query page2 failed: %v", err)
	}
	if len(page2) != 2 || page2[0].TaskID == items[0].TaskID {
		t.Fatalf("page2 overlaps page1: %+v", page2)
	}

	// 5. 按日志量排序
	items, _, err = svc.QueryTaskList(task.TaskListQuery{Page: 1, PageSize: 4, SortBy: "log_count", Order: "asc"})
	if err != nil {
		t.Fatalf("query log_count sort failed: %v", err)
	}
	if items[0].LogCount != 0 || items[len(items)-1].LogCount != 100 {
		t.Fatalf("log_count asc order unexpected: %v", []int{items[0].LogCount, items[len(items)-1].LogCount})
	}

	// 6. 非法排序字段回退默认，不应报错也不应注入
	if _, _, err := svc.QueryTaskList(task.TaskListQuery{SortBy: "log_count; DROP TABLE task_info", Order: "asc"}); err != nil {
		t.Fatalf("invalid sort_by must fall back instead of failing: %v", err)
	}

	// 7. LIKE 通配符转义：% 不应匹配全部任务
	items, total, err = svc.QueryTaskList(task.TaskListQuery{Keyword: "%"})
	if err != nil {
		t.Fatalf("query wildcard failed: %v", err)
	}
	if total != 0 {
		t.Fatalf("literal %% keyword should match nothing, got total=%d items=%+v", total, items)
	}

	// 8. 状态清洗：未知状态被忽略并回报
	statuses, ignored := task.NormalizeTaskStatuses("completed, BOGUS ,, failed")
	if len(statuses) != 2 || len(ignored) != 1 || ignored[0] != "BOGUS" {
		t.Fatalf("NormalizeTaskStatuses = %v / %v", statuses, ignored)
	}

	// 9. 空结果必须返回空切片而不是 null（前端直接 .length）
	items, total, err = svc.QueryTaskList(task.TaskListQuery{Keyword: fmt.Sprintf("no-such-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatalf("query empty failed: %v", err)
	}
	if total != 0 || items == nil {
		t.Fatalf("empty result must be non-nil empty slice, got %#v", items)
	}
}
