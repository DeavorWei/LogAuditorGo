package task

import (
	"testing"
	"time"

	"logauditorgo/internal/model"
)

func TestAlignChunkTimestamps(t *testing.T) {
	t1, _ := time.Parse("2006-01-02 15:04:05", "2026-05-19 09:33:32")
	t2, _ := time.Parse("2006-01-02 15:04:05", "2026-05-19 09:33:35")
	tEarlier, _ := time.Parse("2006-01-02 15:04:05", "2026-05-19 08:00:00")

	t.Run("borrow forward from first valid log for file headers", func(t *testing.T) {
		records := []model.LogRecord{
			{ID: 1, SourceFile: "dev1.log", Brief: "LOGFILE_HEADER", Timestamp: time.Time{}}, // 文件头注释无时间
			{ID: 2, SourceFile: "dev1.log", Brief: "LOGFILE_DIGEST", Timestamp: time.Time{}}, // 校验行无时间
			{ID: 3, SourceFile: "dev1.log", Brief: "IF_DOWN", Timestamp: t1},                 // 首条有效日志
			{ID: 4, SourceFile: "dev1.log", Brief: "IF_UP", Timestamp: t2},                   // 次条有效日志
		}

		var lastFile string
		var lastTS time.Time
		alignChunkTimestamps(records, &lastFile, &lastTS)

		if records[0].Timestamp != t1 {
			t.Errorf("record 0 expected timestamp %v, got %v", t1, records[0].Timestamp)
		}
		if records[1].Timestamp != t1 {
			t.Errorf("record 1 expected timestamp %v, got %v", t1, records[1].Timestamp)
		}
		if records[2].Timestamp != t1 {
			t.Errorf("record 2 expected timestamp %v, got %v", t1, records[2].Timestamp)
		}
		if records[3].Timestamp != t2 {
			t.Errorf("record 3 expected timestamp %v, got %v", t2, records[3].Timestamp)
		}
		if lastTS != t2 {
			t.Errorf("lastTS expected %v, got %v", t2, lastTS)
		}
		if lastFile != "dev1.log" {
			t.Errorf("lastFile expected dev1.log, got %s", lastFile)
		}
	})

	t.Run("inherit backward across chunks", func(t *testing.T) {
		lastFile := "dev1.log"
		lastTS := t1
		records := []model.LogRecord{
			{ID: 5, SourceFile: "dev1.log", Brief: "UNPARSED", Timestamp: time.Time{}}, // 中间无时间行，应继承 t1
			{ID: 6, SourceFile: "dev1.log", Brief: "BGP_DOWN", Timestamp: t2},
			{ID: 7, SourceFile: "dev1.log", Brief: "COMMENT_TAIL", Timestamp: time.Time{}}, // 尾部无时间行，应继承 t2
		}

		alignChunkTimestamps(records, &lastFile, &lastTS)

		if records[0].Timestamp != t1 {
			t.Errorf("record 0 expected timestamp %v, got %v", t1, records[0].Timestamp)
		}
		if records[1].Timestamp != t2 {
			t.Errorf("record 1 expected timestamp %v, got %v", t2, records[1].Timestamp)
		}
		if records[2].Timestamp != t2 {
			t.Errorf("record 2 expected timestamp %v, got %v", t2, records[2].Timestamp)
		}
		if lastTS != t2 {
			t.Errorf("lastTS expected %v, got %v", t2, lastTS)
		}
	})

	t.Run("empty or all zero timestamp file does not panic", func(t *testing.T) {
		var lastFile string
		var lastTS time.Time
		records := []model.LogRecord{
			{ID: 1, SourceFile: "notes.txt", Brief: "NOTE", Timestamp: time.Time{}},
			{ID: 2, SourceFile: "notes.txt", Brief: "NOTE2", Timestamp: time.Time{}},
		}

		alignChunkTimestamps(records, &lastFile, &lastTS)
		if !records[0].Timestamp.IsZero() || !records[1].Timestamp.IsZero() {
			t.Errorf("expected zero timestamp preserved when no valid time found")
		}
	})

	// 对应审计问题 2：重分析跨文件时钟隔离，杜绝新文件头注释时间漂移
	t.Run("reset clock across file boundaries in multi-file reanalysis", func(t *testing.T) {
		records := []model.LogRecord{
			// 文件 A（晚期日志）：尾部时间为 t2 (09:33:35)
			{ID: 1, SourceFile: "fileA.log", Brief: "LOG_A1", Timestamp: t1},
			{ID: 2, SourceFile: "fileA.log", Brief: "LOG_A2", Timestamp: t2},
			// 文件 B（补充导入的早期日志）：头注释行无时间，首条有效日志为 tEarlier (08:00:00)
			{ID: 3, SourceFile: "fileB.log", Brief: "HEADER_B", Timestamp: time.Time{}},
			{ID: 4, SourceFile: "fileB.log", Brief: "LOG_B1", Timestamp: tEarlier},
			{ID: 5, SourceFile: "fileB.log", Brief: "COMMENT_B2", Timestamp: time.Time{}},
		}

		var lastFile string
		var lastTS time.Time
		alignChunkTimestamps(records, &lastFile, &lastTS)

		// 文件 A 的尾部应该为 t2
		if records[1].Timestamp != t2 {
			t.Errorf("fileA tail expected %v, got %v", t2, records[1].Timestamp)
		}
		// 关键断言：文件 B 的头注释必须借用文件 B 的 tEarlier (08:00:00)，绝不可跨文件继承文件 A 的 t2 (09:33:35)
		if records[2].Timestamp != tEarlier {
			t.Errorf("fileB header comment must borrow fileB earliest timestamp %v, but got %v (drifted!)", tEarlier, records[2].Timestamp)
		}
		// 文件 B 的正文
		if records[3].Timestamp != tEarlier {
			t.Errorf("fileB body expected %v, got %v", tEarlier, records[3].Timestamp)
		}
		// 文件 B 的尾部注释继承文件 B 的 tEarlier
		if records[4].Timestamp != tEarlier {
			t.Errorf("fileB tail comment expected %v, got %v", tEarlier, records[4].Timestamp)
		}
		if lastTS != tEarlier {
			t.Errorf("lastTS expected fileB time %v, got %v", tEarlier, lastTS)
		}
		if lastFile != "fileB.log" {
			t.Errorf("lastFile expected fileB.log, got %s", lastFile)
		}
	})
}
