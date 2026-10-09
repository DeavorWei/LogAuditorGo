package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func TestQueryTaskLogsSorting(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_order_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, nil, nil)
	taskInfo, err := svc.CreateEmptyTask("Order-Test-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	// 构造 3 条时序日志：
	// T1 (09:00:10)
	// T3 (09:00:30)
	// T2 (09:00:20) - 物理行上 T3 在 T2 之前
	content1 := "May 19 2026 09:00:10 CE-01 %%01IFNET/4/IF_DOWN(l)[1]: IF 1 down.\n" +
		"May 19 2026 09:00:30 CE-01 %%01IFNET/4/IF_DOWN(l)[3]: IF 3 down.\n" +
		"May 19 2026 09:00:20 CE-01 %%01IFNET/4/IF_DOWN(l)[2]: IF 2 down.\n"

	items1 := []task.FileUploadItem{
		{
			FileName: "log_initial.log",
			FileSize: int64(len(content1)),
			Content:  content1,
		},
	}

	_, err = svc.ImportLogs(taskInfo.TaskID, items1, "overwrite", nil)
	if err != nil {
		t.Fatalf("ImportLogs batch 1 failed: %v", err)
	}

	// 1. 测试默认排序（应为 time asc）
	logsDefault, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryTaskLogs default failed: %v", err)
	}
	if total != 3 || len(logsDefault) != 3 {
		t.Fatalf("expected 3 logs, got total=%d len=%d", total, len(logsDefault))
	}
	// 时间升序：09:00:10 -> 09:00:20 -> 09:00:30
	if !strings.Contains(logsDefault[0].RawLog, "IF 1 down") {
		t.Errorf("expected 1st log to be IF 1 down, got %s", logsDefault[0].RawLog)
	}
	if !strings.Contains(logsDefault[1].RawLog, "IF 2 down") {
		t.Errorf("expected 2nd log to be IF 2 down, got %s", logsDefault[1].RawLog)
	}
	if !strings.Contains(logsDefault[2].RawLog, "IF 3 down") {
		t.Errorf("expected 3rd log to be IF 3 down, got %s", logsDefault[2].RawLog)
	}

	// 2. 测试时间降序 (time desc)
	logsDesc, _, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "time", Order: "desc"})
	if err != nil {
		t.Fatalf("QueryTaskLogs time desc failed: %v", err)
	}
	if !strings.Contains(logsDesc[0].RawLog, "IF 3 down") ||
		!strings.Contains(logsDesc[1].RawLog, "IF 2 down") ||
		!strings.Contains(logsDesc[2].RawLog, "IF 1 down") {
		t.Errorf("time desc order mismatch: [0]=%s, [1]=%s, [2]=%s", logsDesc[0].Brief, logsDesc[1].Brief, logsDesc[2].Brief)
	}

	// 3. 测试原始入库序号排序 (id asc)
	logsIDAsc, _, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "id", Order: "asc"})
	if err != nil {
		t.Fatalf("QueryTaskLogs id asc failed: %v", err)
	}
	// 入库原始顺序：IF 1 down (id=1) -> IF 3 down (id=2) -> IF 2 down (id=3)
	if !strings.Contains(logsIDAsc[0].RawLog, "IF 1 down") ||
		!strings.Contains(logsIDAsc[1].RawLog, "IF 3 down") ||
		!strings.Contains(logsIDAsc[2].RawLog, "IF 2 down") {
		t.Errorf("id asc order mismatch: [0]=%s, [1]=%s, [2]=%s", logsIDAsc[0].RawLog, logsIDAsc[1].RawLog, logsIDAsc[2].RawLog)
	}

	// 4. 测试补充导入（补充一份更早时间的日志：09:00:05）
	contentEarly := "May 19 2026 09:00:05 CE-01 %%01IFNET/4/IF_DOWN(l)[0]: IF 0 earliest.\n"
	itemsEarly := []task.FileUploadItem{
		{
			FileName: "log_supplement.log",
			FileSize: int64(len(contentEarly)),
			Content:  contentEarly,
		},
	}
	_, err = svc.ImportLogs(taskInfo.TaskID, itemsEarly, "rename", nil)
	if err != nil {
		t.Fatalf("ImportLogs supplement failed: %v", err)
	}

	// 再次查询默认 time asc：补充的 09:00:05 应该自动排在第 1 条，而不是末尾！
	logsWithEarly, total4, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryTaskLogs after supplement failed: %v", err)
	}
	if total4 != 4 {
		t.Fatalf("expected 4 logs after supplement, got %d", total4)
	}
	if !strings.Contains(logsWithEarly[0].RawLog, "IF 0 earliest") {
		t.Errorf("expected earliest supplementary log to be [0], got %s", logsWithEarly[0].RawLog)
	}

	// 5. 验证 StreamTaskLogs 与 QueryTaskLogs 顺序一致性 (CSV 与 JSON 保持一致)
	var streamedLogs []model.LogRecord
	err = svc.StreamTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "time", Order: "asc"}, func(rec model.LogRecord) error {
		streamedLogs = append(streamedLogs, rec)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTaskLogs failed: %v", err)
	}
	if len(streamedLogs) != len(logsWithEarly) {
		t.Fatalf("streamed logs count %d != queried logs count %d", len(streamedLogs), len(logsWithEarly))
	}
	for i := range streamedLogs {
		if streamedLogs[i].ID != logsWithEarly[i].ID {
			t.Errorf("log at index %d mismatch: stream ID=%d, query ID=%d", i, streamedLogs[i].ID, logsWithEarly[i].ID)
		}
	}

	// 6. 验证 AfterID 与 SortBy: "time" 的防御拦截 (建议项 4)
	_, _, err = svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "time", AfterID: 10})
	if err == nil {
		t.Errorf("expected error when combining sort_by=time and after_id>0, got nil")
	}

	// 6.1 验证 AfterID 与 Order: "desc" 的防御拦截 (审计问题 3)
	_, _, err = svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "id", Order: "desc", AfterID: 10})
	if err == nil {
		t.Errorf("expected error when combining order=desc and after_id>0, got nil")
	}

	// 7. 验证 AfterID 在 SortBy: "id" 时正常工作
	logsAfter, _, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{SortBy: "id", AfterID: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("QueryTaskLogs with AfterID under id sort failed: %v", err)
	}
	if len(logsAfter) != 3 { // 总共 4 条，after id=1 应剩 3 条
		t.Errorf("expected 3 logs after id=1, got %d", len(logsAfter))
	}
}

// TestLogOrderingPaginationDeterminism 验证同时间戳下基于 (timestamp, id) 联合排序的分页确定性（跨页不重不漏）
// 对应审计问题 5：同时间戳 + id 二级键的分页确定性显式用例
func TestLogOrderingPaginationDeterminism(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_paging_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, nil, nil)
	taskInfo, err := svc.CreateEmptyTask("Paging-Determinism-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	// 构造 10 条时间戳完全相同但序号不同的日志
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		sb.WriteString("May 19 2026 12:00:00 CE-01 %%01IFNET/4/IF_DOWN(l)[")
		sb.WriteString(string(rune('0' + i)))
		sb.WriteString("]: IF identical timestamp line.\n")
	}
	content := sb.String()

	_, err = svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{
		{
			FileName: "identical_time.log",
			FileSize: int64(len(content)),
			Content:  content,
		},
	}, "overwrite", nil)
	if err != nil {
		t.Fatalf("ImportLogs failed: %v", err)
	}

	// 1. 验证升序跨页遍历：PageSize=4，应分 3 页（4 + 4 + 2）
	var collectedAscIDs []uint
	pageSize := 4
	for page := 1; ; page++ {
		pageLogs, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{
			Page:     page,
			PageSize: pageSize,
			SortBy:   "time",
			Order:    "asc",
		})
		if err != nil {
			t.Fatalf("page %d query failed: %v", page, err)
		}
		if total != 10 {
			t.Fatalf("expected total 10, got %d", total)
		}
		if len(pageLogs) == 0 {
			break
		}
		for _, r := range pageLogs {
			collectedAscIDs = append(collectedAscIDs, r.ID)
		}
		if len(collectedAscIDs) == int(total) {
			break
		}
	}

	if len(collectedAscIDs) != 10 {
		t.Fatalf("expected 10 collected IDs across pages, got %d", len(collectedAscIDs))
	}
	// 验证 ID 严格单调递增，且无重复、无遗漏
	seenAsc := make(map[uint]bool)
	for i, id := range collectedAscIDs {
		if seenAsc[id] {
			t.Errorf("duplicate ID %d encountered in pagination", id)
		}
		seenAsc[id] = true
		if i > 0 && id <= collectedAscIDs[i-1] {
			t.Errorf("IDs not strictly ascending: %d after %d", id, collectedAscIDs[i-1])
		}
	}

	// 2. 验证降序跨页遍历：PageSize=4，应分 3 页（4 + 4 + 2）
	var collectedDescIDs []uint
	for page := 1; ; page++ {
		pageLogs, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{
			Page:     page,
			PageSize: pageSize,
			SortBy:   "time",
			Order:    "desc",
		})
		if err != nil {
			t.Fatalf("desc page %d query failed: %v", page, err)
		}
		if len(pageLogs) == 0 {
			break
		}
		for _, r := range pageLogs {
			collectedDescIDs = append(collectedDescIDs, r.ID)
		}
		if len(collectedDescIDs) == int(total) {
			break
		}
	}

	if len(collectedDescIDs) != 10 {
		t.Fatalf("expected 10 collected IDs in desc across pages, got %d", len(collectedDescIDs))
	}
	seenDesc := make(map[uint]bool)
	for i, id := range collectedDescIDs {
		if seenDesc[id] {
			t.Errorf("duplicate ID %d encountered in desc pagination", id)
		}
		seenDesc[id] = true
		if i > 0 && id >= collectedDescIDs[i-1] {
			t.Errorf("IDs not strictly descending: %d after %d", id, collectedDescIDs[i-1])
		}
	}
}

func TestLogOrderingExplainQueryPlan(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_explain_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, nil, nil)
	taskInfo, err := svc.CreateEmptyTask("Explain-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	if err != nil {
		t.Fatalf("GetOrCreateTaskDB failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	type PlanRow struct {
		ID     int
		Parent int
		NotUsed int
		Detail string
	}

	// 1. 验证纯时间排序命中 idx_log_records_time_id，无 temp b-tree
	var planPure []PlanRow
	if err := taskDB.Raw("EXPLAIN QUERY PLAN SELECT * FROM log_records ORDER BY timestamp ASC, id ASC LIMIT 50").Scan(&planPure).Error; err != nil {
		t.Fatalf("explain pure query plan failed: %v", err)
	}
	pureUsesIndex := false
	pureUsesTempBTree := false
	for _, p := range planPure {
		t.Logf("[EXPLAIN Pure Sort] %s", p.Detail)
		if strings.Contains(p.Detail, "idx_log_records_time") || strings.Contains(p.Detail, "idx_log_records_timestamp") {
			pureUsesIndex = true
		}
		if strings.Contains(p.Detail, "USE TEMP B-TREE") {
			pureUsesTempBTree = true
		}
	}
	if !pureUsesIndex {
		t.Errorf("expected pure time sort to use an index on timestamp")
	}
	if pureUsesTempBTree {
		t.Errorf("expected pure time sort to NOT use temp b-tree for sorting")
	}

	// 2. 观测组合模块过滤场景 (WHERE module = 'IFNET' ORDER BY timestamp ASC, id ASC)
	// 说明（审计问题 5）：在带有 keyword/module 等过滤条件时，SQLite 无法同时用单列索引完成过滤和消除排序，
	// 会选择过滤索引并配合 temp b-tree 完成 top-N 排序。鉴于 Count 本就全扫且每页仅 50 条，开销完全可控。
	var planFiltered []PlanRow
	if err := taskDB.Raw("EXPLAIN QUERY PLAN SELECT * FROM log_records WHERE UPPER(module) = 'IFNET' ORDER BY timestamp ASC, id ASC LIMIT 50").Scan(&planFiltered).Error; err != nil {
		t.Fatalf("explain filtered query plan failed: %v", err)
	}
	for _, p := range planFiltered {
		t.Logf("[EXPLAIN Filtered Sort] %s", p.Detail)
	}
}
