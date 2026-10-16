package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to open memory sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.LogRecord{}); err != nil {
		t.Fatalf("failed to auto migrate: %v", err)
	}
	return db
}

func TestBatchUpdateLogRecords(t *testing.T) {
	db := setupTestDB(t)

	// 创建 350 条测试数据（超过单个 chunk 200 的大小，触发多次分片执行）
	total := 350
	initialRecords := make([]model.LogRecord, total)
	now := time.Now().Truncate(time.Millisecond)

	for i := 0; i < total; i++ {
		initialRecords[i] = model.LogRecord{
			Timestamp:       now.Add(time.Duration(i) * time.Second),
			Hostname:        fmt.Sprintf("orig-host-%d", i),
			Module:          "ORIG_MOD",
			Severity:        1,
			Brief:           "ORIG_BRIEF",
			SlotInfo:        "0/0",
			SourceFile:      "test.log",
			RawLog:          fmt.Sprintf("raw log %d", i),
			MessageBody:     fmt.Sprintf("orig msg %d", i),
			ParametersJSON:  "{}",
			KnowledgeID:     0,
			MatchTier:       "NONE",
			MatchConfidence: 0.0,
		}
	}

	if err := db.CreateInBatches(initialRecords, 100).Error; err != nil {
		t.Fatalf("failed to insert initial records: %v", err)
	}

	// 准备更新后的数据
	updatedRecords := make([]model.LogRecord, total)
	for i := 0; i < total; i++ {
		r := initialRecords[i]
		r.ID = uint(i + 1) // SQLite autoincrement starts at 1
		r.Hostname = fmt.Sprintf("new-host-%d", i)
		r.Module = "NEW_MOD"
		r.Severity = 4
		r.Brief = fmt.Sprintf("updated-brief-%d", i)
		r.KnowledgeID = uint(100 + i)
		r.MatchTier = "EXACT"
		r.MatchConfidence = 0.95
		r.ParametersJSON = `{"key":"value"}`

		r.RawLog = fmt.Sprintf("updated raw log %d", i)

		// 边界测试 1: 第 4 条记录包含单引号（防注入与转义验证）
		if i == 3 {
			r.MessageBody = "O'Reilly's network device log with 'quotes'"
			r.Brief = "It's a test"
		} else if i == 5 {
			// 边界测试 2: 第 6 条记录包含连续 NUL 字节（本次 Bug 现场复现与验证）
			r.MessageBody = "data_with_" + strings.Repeat("\x00", 333) + "_nul_bytes"
		} else {
			r.MessageBody = fmt.Sprintf("new msg body %d", i)
		}

		updatedRecords[i] = r
	}

	// 执行重构后的参数化批量更新
	if err := batchUpdateLogRecords(db, updatedRecords); err != nil {
		t.Fatalf("batchUpdateLogRecords failed unexpectedly: %v", err)
	}

	// 验证更新结果
	var count int64
	db.Model(&model.LogRecord{}).Count(&count)
	if count != int64(total) {
		t.Errorf("expected %d records in db, got %d", total, count)
	}

	// 检查第 4 条（单引号与 raw_log）
	var rec4 model.LogRecord
	if err := db.First(&rec4, 4).Error; err != nil {
		t.Fatalf("failed to query record 4: %v", err)
	}
	if rec4.RawLog != "updated raw log 3" {
		t.Errorf("record 4 raw_log mismatch: %q", rec4.RawLog)
	}
	if rec4.MessageBody != "O'Reilly's network device log with 'quotes'" {
		t.Errorf("record 4 message body mismatch: %q", rec4.MessageBody)
	}
	if rec4.Brief != "It's a test" {
		t.Errorf("record 4 brief mismatch: %q", rec4.Brief)
	}

	// 检查第 6 条（包含 \x00）
	var rec6 model.LogRecord
	if err := db.First(&rec6, 6).Error; err != nil {
		t.Fatalf("failed to query record 6: %v", err)
	}
	expectedRec6Body := "data_with_" + strings.Repeat("\x00", 333) + "_nul_bytes"
	if rec6.MessageBody != expectedRec6Body {
		t.Errorf("record 6 message body mismatch, len got %d, want %d", len(rec6.MessageBody), len(expectedRec6Body))
	}
	if rec6.KnowledgeID != 105 {
		t.Errorf("record 6 knowledge_id got %d, want 105", rec6.KnowledgeID)
	}

	// 检查第 300 条（跨越 chunk 边界）
	var rec300 model.LogRecord
	if err := db.First(&rec300, 300).Error; err != nil {
		t.Fatalf("failed to query record 300: %v", err)
	}
	if rec300.Hostname != "new-host-299" || rec300.KnowledgeID != 399 {
		t.Errorf("record 300 data mismatch: host=%s, kid=%d", rec300.Hostname, rec300.KnowledgeID)
	}
}

// TestReanalyzeTaskConcurrentOrderPreservation 验证并发重分析下的严格保序性与时间戳继承正确性
func TestReanalyzeTaskConcurrentOrderPreservation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "reanalyze_order_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("InitKnowledgeDB failed: %v", err)
	}

	matchEngine := matcher.NewMatchEngine(globalDB, nil)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")
	svc := NewService(globalDB, taskDir, matchEngine, rcaEngine)

	task, err := svc.CreateEmptyTask("ConcurrentOrderTest", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, task.TaskID)
	if err != nil {
		t.Fatalf("get task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(task.TaskID)

	// 插入混合日志：注释行（无时间戳） + 有效行 + 散漫行 + 纯 NUL 坏块
	rawLogs := []string{
		"# This is a file header comment without timestamp",
		"Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface GigabitEthernet0/0/1 turned to down.",
		"Interface GigabitEthernet0/0/1 speed changed to 1000M", // 无时间戳行，应继承前序有效时间戳
		"\x00\x00\x00",                                          // 纯垃圾坏块行，应被物理删除
		"Apr 15 2026 14:00:05 CORE-SW-01 %%01IFNET/4/IF_UP(l)[2]: Interface GigabitEthernet0/0/1 turned to up.",
	}

	var initRecords []model.LogRecord
	for _, l := range rawLogs {
		initRecords = append(initRecords, model.LogRecord{
			SourceFile: "test_order.log",
			RawLog:     l,
		})
	}
	if err := taskDB.CreateInBatches(initRecords, 10).Error; err != nil {
		t.Fatalf("insert init records failed: %v", err)
	}

	// 执行并发重分析
	reanalyzed, err := svc.ReanalyzeTask(task.TaskID, nil)
	if err != nil {
		t.Fatalf("reanalyze task failed: %v", err)
	}

	if reanalyzed.Status != model.TaskStatusCompleted {
		t.Fatalf("expected completed status, got %s", reanalyzed.Status)
	}

	var results []model.LogRecord
	if err := taskDB.Order("id asc").Find(&results).Error; err != nil {
		t.Fatalf("query results failed: %v", err)
	}

	// 垃圾行应被清理，剩余 4 行
	if len(results) != 4 {
		t.Fatalf("expected 4 records after garbage deletion, got %d", len(results))
	}

	// 验证时间戳对齐正确性：
	// 首行注释行向前借用了第 2 行的首个有效时间戳 14:00:01
	// 第 2 行有效行自身时间戳 14:00:01
	// 第 3 行无时间戳行继承了前序有效时间戳 14:00:01
	// 第 4 行有效行自身时间戳 14:00:05
	t1 := results[0].Timestamp
	t2 := results[1].Timestamp
	t3 := results[2].Timestamp
	t4 := results[3].Timestamp

	if !t1.Equal(t2) {
		t.Errorf("expected header comment to borrow first valid timestamp: t1=%v, t2=%v", t1, t2)
	}
	if !t3.Equal(t2) {
		t.Errorf("expected trailing log without timestamp to inherit last valid timestamp: t3=%v, t2=%v", t3, t2)
	}
	if !t4.After(t3) {
		t.Errorf("expected t4 to be after t3: t3=%v, t4=%v", t3, t4)
	}
}
