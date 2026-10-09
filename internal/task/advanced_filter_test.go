package task

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"path/filepath"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

func setupTestTaskWithLogs(t *testing.T) (*Service, string, func()) {
	storage.RegisterSQLiteFunctions()

	tempDir, err := os.MkdirTemp("", "adv_filter_test_*")
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
	taskInfo, err := svc.CreateEmptyTask("AdvFilterTest", "CloudEngine")
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("create empty task failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(tempDir, taskInfo.TaskID)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("acquire task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	// 插入代表性日志数据：涵盖空串 parameters_json、NULL slot_info、特定 KV 参数、不同级别与模块
	now := time.Now()
	testRecords := []model.LogRecord{
		{
			ID:             1,
			Timestamp:      now.Add(-10 * time.Minute),
			Hostname:       "SW-CORE-01",
			Module:         "BFD",
			Severity:       2,
			Brief:          "BFD_SESS_DOWN",
			SlotInfo:       "Slot=1/1",
			SourceFile:     "core.log",
			RawLog:         "2026-04-15 14:00:01 SW-CORE-01 %%01BFD/2/BFD_SESS_DOWN: BFD session 10 down",
			MessageBody:    "BFD session 10 down",
			ParametersJSON: `{"SessionID":"10","PeerIP":"10.1.1.1"}`,
		},
		{
			ID:             2,
			Timestamp:      now.Add(-8 * time.Minute),
			Hostname:       "SW-CORE-01",
			Module:         "BGP",
			Severity:       2,
			Brief:          "PEER_BACKWARD",
			SlotInfo:       "", // 空串 slot_info
			SourceFile:     "core.log",
			RawLog:         "2026-04-15 14:00:02 SW-CORE-01 %%01BGP/2/PEER_BACKWARD: Peer 10.1.1.1 changed from Established to Idle",
			MessageBody:    "Peer 10.1.1.1 changed to Idle",
			ParametersJSON: `{"PeerIP":"10.1.1.1","State":"Idle"}`,
		},
		{
			ID:             3,
			Timestamp:      now.Add(-6 * time.Minute),
			Hostname:       "SW-ACC-01",
			Module:         "IFNET",
			Severity:       4,
			Brief:          "IF_DOWN",
			SlotInfo:       "Slot=2/1",
			SourceFile:     "access.log",
			RawLog:         "2026-04-15 14:00:03 SW-ACC-01 %%01IFNET/4/IF_DOWN: Interface 100GE1/0/1 turned down",
			MessageBody:    "Interface 100GE1/0/1 turned down",
			ParametersJSON: "", // 关键测试点：空字符串 parameters_json，验证 json_valid 保护
		},
		{
			ID:             4,
			Timestamp:      now.Add(-4 * time.Minute),
			Hostname:       "SW-ACC-01",
			Module:         "DEVM",
			Severity:       5,
			Brief:          "FAN_NORMAL",
			SlotInfo:       "",
			SourceFile:     "access.log",
			RawLog:         "2026-04-15 14:00:04 SW-ACC-01 %%01DEVM/5/FAN_NORMAL: Fan speed normal",
			MessageBody:    "Fan speed normal",
			ParametersJSON: `{}`,
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

// ADV-01: 字段与操作符白名单校验（SQL 注入防御测试组）
func TestAdvancedFilterWhitelistSecurity(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	injectionCases := []struct {
		name  string
		field string
		op    string
		value string
	}{
		{"illegal field with DROP", "id; DROP TABLE log_records;--", "eq", "1"},
		{"field injection single quote", "raw_log' --", "contains", "test"},
		{"illegal op injection", "raw_log", "1=1", "test"},
		{"unsupported op for field", "source_file", "regex", ".*"},
		{"field not in whitelist", "password", "eq", "secret"},
	}

	for _, tc := range injectionCases {
		t.Run(tc.name, func(t *testing.T) {
			req := model.LogQueryRequestBody{
				PageSize: 10,
				Advanced: &model.AdvancedFilter{
					Logic: "AND",
					Conditions: []model.AdvancedCondition{
						{Field: tc.field, Op: tc.op, Value: tc.value},
					},
				},
			}
			_, _, err := svc.QueryTaskLogsUnified(taskID, req)
			if err == nil {
				t.Fatalf("expected error for security test case '%s', got nil", tc.name)
			}
		})
	}
}

// ADV-02: 坏正则返回清晰错误，合法正则查询正确
func TestAdvancedFilterRegexValidation(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	// 1. 坏正则测试
	badReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "raw_log", Op: "regex", Value: "[invalid_unclosed"},
			},
		},
	}
	_, _, err := svc.QueryTaskLogsUnified(taskID, badReq)
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "condition #1: invalid regex pattern") {
		t.Errorf("expected error message with condition index, got: %v", err)
	}

	// 2. 合法正则测试
	goodReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "raw_log", Op: "regex", Value: "BFD_SESS_DOWN|PEER_BACKWARD"},
			},
		},
	}
	records, total, err := svc.QueryTaskLogsUnified(taskID, goodReq)
	if err != nil {
		t.Fatalf("valid regex query failed: %v", err)
	}
	if total != 2 || len(records) != 2 {
		t.Fatalf("expected 2 matches for BFD/BGP regex, got total=%d, len=%d", total, len(records))
	}
}

// ADV-03: AND / OR 顶层逻辑与空 advanced 回归
func TestAdvancedFilterLogicModes(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	// 1. AND 逻辑：module=BFD AND brief=BFD_SESS_DOWN -> 1 条
	andReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "module", Op: "eq", Value: "BFD"},
				{Field: "brief", Op: "eq", Value: "BFD_SESS_DOWN"},
			},
		},
	}
	records, total, err := svc.QueryTaskLogsUnified(taskID, andReq)
	if err != nil {
		t.Fatalf("AND query failed: %v", err)
	}
	if total != 1 || len(records) != 1 || records[0].ID != 1 {
		t.Fatalf("expected log ID=1 for AND logic, got total=%d", total)
	}

	// 2. OR 逻辑：module=BFD OR module=DEVM -> 2 条
	orReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "OR",
			Conditions: []model.AdvancedCondition{
				{Field: "module", Op: "eq", Value: "BFD"},
				{Field: "module", Op: "eq", Value: "DEVM"},
			},
		},
	}
	recordsOr, totalOr, err := svc.QueryTaskLogsUnified(taskID, orReq)
	if err != nil {
		t.Fatalf("OR query failed: %v", err)
	}
	if totalOr != 2 || len(recordsOr) != 2 {
		t.Fatalf("expected 2 logs for OR logic, got total=%d", totalOr)
	}

	// 3. 空 Advanced 等价旧行为 (全部 4 条)
	emptyReq := model.LogQueryRequestBody{PageSize: 10}
	recordsEmpty, totalEmpty, err := svc.QueryTaskLogsUnified(taskID, emptyReq)
	if err != nil {
		t.Fatalf("empty advanced query failed: %v", err)
	}
	if totalEmpty != 4 || len(recordsEmpty) != 4 {
		t.Fatalf("expected 4 logs for empty advanced, got %d", totalEmpty)
	}
}

// ADV-04: KV 参数过滤在含空串、NULL 以及非法畸形 JSON 记录的表上不报错且命中正确
func TestAdvancedFilterKVParameters(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 插入一条 parameters_json 显式为 NULL 的记录，以及一条非法非 JSON 格式的脏数据
	rawLogs := []model.LogRecord{
		{
			ID:             5,
			Timestamp:      time.Now(),
			Severity:       3,
			RawLog:         "null params log",
			ParametersJSON: "", // 临时赋空
		},
		{
			ID:             6,
			Timestamp:      time.Now(),
			Severity:       4,
			RawLog:         "malformed json log",
			ParametersJSON: "not_a_valid_{json}",
		},
	}
	taskDB.Create(&rawLogs)
	taskDB.Exec("UPDATE log_records SET parameters_json = NULL WHERE id = 5")

	// 查询 PeerIP=10.1.1.1（精确匹配） -> 命中文档 ID 1 和 2，NULL 和非法 JSON 被安全排除且不报 SQL 语法错
	kvReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "parameters", Op: "kv_eq", Value: "PeerIP=10.1.1.1"},
			},
		},
	}
	records, total, err := svc.QueryTaskLogsUnified(taskID, kvReq)
	if err != nil {
		t.Fatalf("kv_eq query failed: %v", err)
	}
	if total != 2 || len(records) != 2 {
		t.Fatalf("expected 2 matches for PeerIP=10.1.1.1, got total=%d", total)
	}

	// 查询 kv_contains: PeerIP=10.1.
	kvContainsReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "parameters", Op: "kv_contains", Value: "PeerIP=10.1."},
			},
		},
	}
	recordsC, totalC, err := svc.QueryTaskLogsUnified(taskID, kvContainsReq)
	if err != nil {
		t.Fatalf("kv_contains query failed: %v", err)
	}
	if totalC != 2 || len(recordsC) != 2 {
		t.Fatalf("expected 2 matches for kv_contains, got %d", totalC)
	}
}

// ADV-05: not_* 三值逻辑防御（NULL / 空列不被静默丢失）
func TestAdvancedFilterThreeValuedLogic(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	// slot_info: ID=2,4 为空串。not_contains 'Slot=1/1' 应该匹配 ID=2,3,4
	notContainsReq := model.LogQueryRequestBody{
		PageSize: 10,
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "slot_info", Op: "not_contains", Value: "Slot=1/1"},
			},
		},
	}
	records, total, err := svc.QueryTaskLogsUnified(taskID, notContainsReq)
	if err != nil {
		t.Fatalf("not_contains query failed: %v", err)
	}
	if total != 3 || len(records) != 3 {
		t.Fatalf("expected 3 logs for not_contains (preserving empty), got %d", total)
	}
}

// ADV-06: key 非法格式校验（防 JSON path 注入）
func TestAdvancedFilterKeyValidation(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	invalidKeys := []string{
		`bad key=1`,
		`"bad"=1`,
		`key';--=1`,
		`.leading_dot=1`,
	}

	for _, k := range invalidKeys {
		req := model.LogQueryRequestBody{
			PageSize: 10,
			Advanced: &model.AdvancedFilter{
				Logic: "AND",
				Conditions: []model.AdvancedCondition{
					{Field: "parameters", Op: "kv_eq", Value: k},
				},
			},
		}
		_, _, err := svc.QueryTaskLogsUnified(taskID, req)
		if err == nil {
			t.Fatalf("expected error for invalid key '%s', got nil", k)
		}
	}
}

// ADV-07: 标签过滤（any / all 子查询与回填，断言无 JOIN 且防行膨胀）
func TestAdvancedFilterTagSubqueries(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	// 创建两个标签
	tag1 := model.LogTag{Name: "核心故障", Color: "#F56C6C"}
	tag2 := model.LogTag{Name: "待复核", Color: "#E6A23C"}
	taskDB.Create(&tag1)
	taskDB.Create(&tag2)

	// 打标：Log 1 打 tag1 & tag2；Log 2 打 tag1
	taskDB.Create(&[]model.LogTagRelation{
		{TagID: tag1.ID, LogID: 1},
		{TagID: tag2.ID, LogID: 1},
		{TagID: tag1.ID, LogID: 2},
	})

	// 断言 SQL 形态：必须采用子查询防行膨胀，严禁 JOIN log_tag_relations
	sqlStr := taskDB.ToSQL(func(tx *gorm.DB) *gorm.DB {
		q, _ := BuildLogFilterScope(tx.Model(&model.LogRecord{}), model.LogQueryRequestBody{
			TagIDs:   []uint{tag1.ID, tag2.ID},
			TagLogic: "all",
		})
		return q.Find(&[]model.LogRecord{})
	})
	if strings.Contains(strings.ToUpper(sqlStr), "JOIN LOG_TAG_RELATIONS") {
		t.Fatalf("expected NO JOIN on log_tag_relations to avoid row explosion, got SQL: %s", sqlStr)
	}
	if !strings.Contains(strings.ToUpper(sqlStr), "ID IN (SELECT LOG_ID FROM LOG_TAG_RELATIONS") {
		t.Fatalf("expected subquery 'id IN (SELECT log_id...', got SQL: %s", sqlStr)
	}

	// 1. any 逻辑：tag_ids=[tag1, tag2] -> 命中 Log 1, 2
	anyReq := model.LogQueryRequestBody{
		PageSize: 10,
		TagIDs:   []uint{tag1.ID, tag2.ID},
		TagLogic: "any",
	}
	recordsAny, totalAny, err := svc.QueryTaskLogsUnified(taskID, anyReq)
	if err != nil {
		t.Fatalf("tag any query failed: %v", err)
	}
	if totalAny != 2 || len(recordsAny) != 2 {
		t.Fatalf("expected 2 matches for tag any, got %d", totalAny)
	}

	// 验证回填结果
	foundLog1 := false
	for _, r := range recordsAny {
		if r.ID == 1 {
			foundLog1 = true
			if len(r.Tags) != 2 {
				t.Fatalf("expected 2 tags for log 1, got %d", len(r.Tags))
			}
		}
	}
	if !foundLog1 {
		t.Fatal("expected log 1 in results")
	}

	// 2. all 逻辑且包含重复 tag ID（[tag1, tag1, tag2] 验证去重）：依然正确命中 Log 1
	allReqWithDup := model.LogQueryRequestBody{
		PageSize: 10,
		TagIDs:   []uint{tag1.ID, tag1.ID, tag2.ID},
		TagLogic: "all",
	}
	recordsAll, totalAll, err := svc.QueryTaskLogsUnified(taskID, allReqWithDup)
	if err != nil {
		t.Fatalf("tag all query with duplicates failed: %v", err)
	}
	if totalAll != 1 || len(recordsAll) != 1 || recordsAll[0].ID != 1 {
		t.Fatalf("expected only log 1 for tag all with duplicates, got total=%d", totalAll)
	}
}

// EXP-01: 流式导出协同与 CSV 标签列验证
func TestStreamExportWithTags(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	taskDB, _, err := storage.GetOrCreateTaskDB(svc.taskDir, taskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	tag := model.LogTag{Name: "重要关注", Color: "#409EFF"}
	taskDB.Create(&tag)
	taskDB.Create(&model.LogTagRelation{TagID: tag.ID, LogID: 1})

	var buf bytes.Buffer
	req := model.LogQueryRequestBody{
		Advanced: &model.AdvancedFilter{
			Logic: "AND",
			Conditions: []model.AdvancedCondition{
				{Field: "module", Op: "eq", Value: "BFD"},
			},
		},
	}

	err = svc.StreamTaskLogsUnified(taskID, req, func(rec model.LogRecord) error {
		tagNames := make([]string, len(rec.Tags))
		for i, tg := range rec.Tags {
			tagNames[i] = tg.Name
		}
		buf.WriteString(fmt.Sprintf("%d,%s,%s\n", rec.ID, rec.Module, strings.Join(tagNames, ";")))
		return nil
	})
	if err != nil {
		t.Fatalf("stream export failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "1,BFD,重要关注") {
		t.Fatalf("expected exported row with tag name, got:\n%s", output)
	}
}

// 验证 module 字段在所有支持操作符下的大小写不敏感语义
func TestModuleFilterCaseInsensitivity(t *testing.T) {
	svc, taskID, cleanup := setupTestTaskWithLogs(t)
	defer cleanup()

	tests := []struct {
		name     string
		op       string
		val      string
		expected int64
	}{
		{name: "eq lower", op: "eq", val: "bfd", expected: 1},
		{name: "contains lower", op: "contains", val: "fd", expected: 1},
		{name: "prefix lower", op: "prefix", val: "bf", expected: 1},
		{name: "suffix lower", op: "suffix", val: "fd", expected: 1},
		{name: "regex lower", op: "regex", val: "bf[dD]", expected: 1},
		{name: "in lower", op: "in", val: "bfd,devm", expected: 2},
		{name: "not_contains lower", op: "not_contains", val: "bfd", expected: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := model.LogQueryRequestBody{
				PageSize: 10,
				Advanced: &model.AdvancedFilter{
					Logic: "AND",
					Conditions: []model.AdvancedCondition{
						{Field: "module", Op: tc.op, Value: tc.val},
					},
				},
			}
			_, total, err := svc.QueryTaskLogsUnified(taskID, req)
			if err != nil {
				t.Fatalf("query failed for op %s: %v", tc.op, err)
			}
			if total != tc.expected {
				t.Fatalf("expected total %d for op %s with val '%s', got %d", tc.expected, tc.op, tc.val, total)
			}
		})
	}
}
