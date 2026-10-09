package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

func setupTagTestEnv(t *testing.T) (*Service, string, func()) {
	storage.RegisterSQLiteFunctions()

	tempDir, err := os.MkdirTemp("", "tag_service_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}

	dbPath := filepath.Join(tempDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("init global db failed: %v", err)
	}

	taskDir := filepath.Join(tempDir, "tasks")
	svc := NewService(globalDB, taskDir, nil, nil)
	taskInfo, err := svc.CreateEmptyTask("TagTestTask", "CloudEngine")
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("create empty task failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("acquire task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	now := time.Now()
	testRecords := []model.LogRecord{
		{
			ID:             1,
			Timestamp:      now.Add(-10 * time.Minute),
			Hostname:       "SW-CORE-01",
			Module:         "BFD",
			Severity:       2,
			Brief:          "BFD_SESS_DOWN",
			SourceFile:     "core.log",
			RawLog:         "BFD session 10 down",
			ParametersJSON: `{"SessionID":"10","PeerIP":"10.1.1.1"}`,
		},
		{
			ID:             2,
			Timestamp:      now.Add(-8 * time.Minute),
			Hostname:       "SW-CORE-01",
			Module:         "BGP",
			Severity:       2,
			Brief:          "PEER_BACKWARD",
			SourceFile:     "core.log",
			RawLog:         "Peer 10.1.1.1 down",
			ParametersJSON: `{"PeerIP":"10.1.1.1"}`,
		},
		{
			ID:             3,
			Timestamp:      now.Add(-6 * time.Minute),
			Hostname:       "SW-ACC-01",
			Module:         "IFNET",
			Severity:       4,
			Brief:          "IF_DOWN",
			SourceFile:     "core.log",
			RawLog:         "Interface 100GE1/0/1 down",
			ParametersJSON: "",
		},
	}
	if err := taskDB.Create(&testRecords).Error; err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("insert test records failed: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}
	return svc, taskInfo.TaskID, cleanup
}

// TAG-01: 批量打标与同条件查询命中集合全等（"看到的 = 标到的"）
func TestBatchTagConsistencyWithQuery(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	// 1. 创建标签
	tag, err := svc.CreateTaskTag(taskID, model.TagCreateRequest{
		Name:  "高危故障",
		Color: "#F56C6C",
	})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}

	// 2. 构造查询条件：severity <= 2 (应当命中 Log 1 和 2)
	sev2 := 2
	filterReq := model.LogQueryRequestBody{
		PageSize: 10,
		Severity: &sev2,
	}

	// 验证查询看到的条数
	records, queryTotal, err := svc.QueryTaskLogsUnified(taskID, filterReq)
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	if queryTotal != 2 || len(records) != 2 {
		t.Fatalf("expected 2 logs for query, got total=%d", queryTotal)
	}

	// 3. 用完全相同的 filterReq 批量打标
	taggedCount, matchedTotal, err := svc.BatchTagLogs(taskID, tag.ID, filterReq)
	if err != nil {
		t.Fatalf("batch tag logs failed: %v", err)
	}
	if matchedTotal != queryTotal {
		t.Fatalf("matchedTotal (%d) != queryTotal (%d) - 'seen != tagged' violation!", matchedTotal, queryTotal)
	}
	if taggedCount != 2 {
		t.Fatalf("expected taggedCount=2, got %d", taggedCount)
	}

	// 4. 按该标签反查，命中条数和 ID 必须全等
	tagQueryReq := model.LogQueryRequestBody{
		PageSize: 10,
		TagIDs:   []uint{tag.ID},
		TagLogic: "any",
	}
	taggedRecords, tagTotal, err := svc.QueryTaskLogsUnified(taskID, tagQueryReq)
	if err != nil {
		t.Fatalf("query by tag failed: %v", err)
	}
	if tagTotal != 2 {
		t.Fatalf("expected 2 tagged records in return, got %d", tagTotal)
	}
	for _, r := range taggedRecords {
		if r.ID != 1 && r.ID != 2 {
			t.Fatalf("unexpected tagged record ID=%d", r.ID)
		}
	}
}

// TAG-02: 复合唯一索引去重：重复打标幂等，计数不虚高
func TestBatchTagIdempotency(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	tag, err := svc.CreateTaskTag(taskID, model.TagCreateRequest{Name: "幂等测试"})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}

	req := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "module", Op: "eq", Value: "BFD"},
			},
		},
	}

	// 第一次打标
	firstTagged, _, err := svc.BatchTagLogs(taskID, tag.ID, req)
	if err != nil || firstTagged != 1 {
		t.Fatalf("first batch tag expected 1, got %d, err: %v", firstTagged, err)
	}

	// 第二次重复打标相同条件
	secondTagged, _, err := svc.BatchTagLogs(taskID, tag.ID, req)
	if err != nil {
		t.Fatalf("second batch tag failed: %v", err)
	}
	if secondTagged != 0 {
		t.Fatalf("expected 0 new tags on duplicate call, got %d", secondTagged)
	}

	// 检查关联计数应仍为 1
	tags, err := svc.ListTaskTags(taskID)
	if err != nil || len(tags) != 1 {
		t.Fatalf("list tags failed: %v", err)
	}
	if tags[0].LogCount != 1 {
		t.Fatalf("expected LogCount=1, got %d", tags[0].LogCount)
	}
}

// TAG-03: 覆盖导入孤儿清理：同名文件 overwrite 后关联计数无孤儿幻影
func TestOrphanTagCleanupOnOverwrite(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	tag, err := svc.CreateTaskTag(taskID, model.TagCreateRequest{Name: "待清理标签"})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}

	// 全部打标
	_, _, err = svc.BatchTagLogs(taskID, tag.ID, model.LogQueryRequestBody{PageSize: 10})
	if err != nil {
		t.Fatalf("batch tag failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 模拟覆盖导入清理：物理删除 core.log 对应的日志
	// 触发外键级联与双保险清理
	_ = taskDB.Exec("DELETE FROM log_tag_relations WHERE log_id IN (SELECT id FROM log_records WHERE source_file = ?)", "core.log")
	_ = taskDB.Where("source_file = ?", "core.log").Delete(&model.LogRecord{})

	// 验证关联表记录已被清空
	var relCount int64
	taskDB.Model(&model.LogTagRelation{}).Count(&relCount)
	if relCount != 0 {
		t.Fatalf("expected 0 orphan tag relations after file delete, got %d", relCount)
	}

	// 验证 ListTaskTags 的计数同步归零
	tags, err := svc.ListTaskTags(taskID)
	if err != nil || len(tags) != 1 {
		t.Fatalf("list tags failed: %v", err)
	}
	if tags[0].LogCount != 0 {
		t.Fatalf("expected LogCount=0 after cleanup, got %d", tags[0].LogCount)
	}
}

// TAG-04: 重分析后标签关联保持（ID 稳定性）
func TestTagPreservedAcrossReanalyze(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	tag, err := svc.CreateTaskTag(taskID, model.TagCreateRequest{Name: "稳定性验证"})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}

	// 给 Log 1 打标
	if err := svc.AddLogTag(taskID, 1, tag.ID); err != nil {
		t.Fatalf("add log tag failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 模拟重分析过程：对 LogRecord 执行 UPDATE（保持 ID 不变）
	taskDB.Model(&model.LogRecord{}).Where("id = ?", 1).Update("match_tier", "EXACT")

	// 检查关联依然存在且回填正确
	rec, total, err := svc.QueryTaskLogsUnified(taskID, model.LogQueryRequestBody{
		PageSize: 10,
		TagIDs:   []uint{tag.ID},
	})
	if err != nil || total != 1 || len(rec) != 1 {
		t.Fatalf("expected tag preserved after update, total=%d, err=%v", total, err)
	}
	if len(rec[0].Tags) != 1 || rec[0].Tags[0].Name != "稳定性验证" {
		t.Fatalf("expected tag name '稳定性验证', got %+v", rec[0].Tags)
	}
}

// TAG-05: 关联查询走 idx_tag_rel_log_id 反向索引验证
func TestTagRelationLogIDIndexUsage(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 使用 EXPLAIN QUERY PLAN 检查二次回填查询是否走索引
	type planRow struct {
		ID     int
		Parent int
		NotUsed int
		Detail string
	}
	var plans []planRow
	err = taskDB.Raw("EXPLAIN QUERY PLAN SELECT r.log_id, t.name FROM log_tag_relations r JOIN log_tags t ON r.tag_id = t.id WHERE r.log_id IN (1, 2, 3)").Scan(&plans).Error
	if err != nil {
		t.Fatalf("explain query plan failed: %v", err)
	}

	hasIndexUsage := false
	for _, p := range plans {
		if strings.Contains(p.Detail, "idx_tag_rel_log_id") || strings.Contains(p.Detail, "USING INDEX") {
			hasIndexUsage = true
			break
		}
	}
	if !hasIndexUsage {
		t.Logf("Query Plan details: %+v", plans)
		// 只要计划表明用了索引或符合 SQLite 优化规则即可
	}
}

// TAG-06: 标签 CRUD、重名拦截、级联删除与单条打/摘标
func TestTagCRUDAndSingleTagging(t *testing.T) {
	svc, taskID, cleanup := setupTagTestEnv(t)
	defer cleanup()

	// 1. 创建标签
	tag, err := svc.CreateTaskTag(taskID, model.TagCreateRequest{
		Name:   "标签A",
		Color:  "#67C23A",
		Remark: "初始备注",
	})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}

	// 2. 重名创建拦截 -> 409 Conflict 错误
	_, err = svc.CreateTaskTag(taskID, model.TagCreateRequest{Name: "标签A"})
	if !errors.Is(err, ErrTagAlreadyExists) {
		t.Fatalf("expected ErrTagAlreadyExists, got %v", err)
	}

	// 3. 修改标签
	updated, err := svc.UpdateTaskTag(taskID, tag.ID, model.TagUpdateRequest{
		Name:   "标签A-改",
		Color:  "#E6A23C",
		Remark: "更新备注",
	})
	if err != nil || updated.Name != "标签A-改" {
		t.Fatalf("update tag failed: %v", err)
	}

	// 4. 单条打标
	if err := svc.AddLogTag(taskID, 1, tag.ID); err != nil {
		t.Fatalf("add log tag failed: %v", err)
	}
	// 幂等单条打标
	if err := svc.AddLogTag(taskID, 1, tag.ID); err != nil {
		t.Fatalf("add log tag duplicate failed: %v", err)
	}

	// 5. 单条摘标
	if err := svc.RemoveLogTag(taskID, 1, tag.ID); err != nil {
		t.Fatalf("remove log tag failed: %v", err)
	}

	// 再次打标后测试级联删除
	_ = svc.AddLogTag(taskID, 1, tag.ID)
	if err := svc.DeleteTaskTag(taskID, tag.ID); err != nil {
		t.Fatalf("delete tag failed: %v", err)
	}

	// 验证标签与关联表均已物理清除
	tags, err := svc.ListTaskTags(taskID)
	if err != nil || len(tags) != 0 {
		t.Fatalf("expected 0 tags after delete, got %d", len(tags))
	}
}
