package task

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/model"
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
