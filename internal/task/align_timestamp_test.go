package task

import (
	"testing"
	"time"

	"logauditorgo/internal/model"
)

func TestAlignChunkTimestamps(t *testing.T) {
	t1, _ := time.Parse("2006-01-02 15:04:05", "2026-05-19 09:33:32")
	t2, _ := time.Parse("2006-01-02 15:04:05", "2026-05-19 09:33:35")

	t.Run("borrow forward from first valid log for file headers", func(t *testing.T) {
		records := []model.LogRecord{
			{ID: 1, Brief: "LOGFILE_HEADER", Timestamp: time.Time{}}, // 文件头注释无时间
			{ID: 2, Brief: "LOGFILE_DIGEST", Timestamp: time.Time{}}, // 校验行无时间
			{ID: 3, Brief: "IF_DOWN", Timestamp: t1},                 // 首条有效日志
			{ID: 4, Brief: "IF_UP", Timestamp: t2},                   // 次条有效日志
		}

		var lastTS time.Time
		alignChunkTimestamps(records, &lastTS)

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
	})

	t.Run("inherit backward across chunks", func(t *testing.T) {
		lastTS := t1
		records := []model.LogRecord{
			{ID: 5, Brief: "UNPARSED", Timestamp: time.Time{}}, // 中间无时间行，应继承 t1
			{ID: 6, Brief: "BGP_DOWN", Timestamp: t2},
			{ID: 7, Brief: "COMMENT_TAIL", Timestamp: time.Time{}}, // 尾部无时间行，应继承 t2
		}

		alignChunkTimestamps(records, &lastTS)

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
		var lastTS time.Time
		records := []model.LogRecord{
			{ID: 1, Brief: "NOTE", Timestamp: time.Time{}},
			{ID: 2, Brief: "NOTE2", Timestamp: time.Time{}},
		}

		alignChunkTimestamps(records, &lastTS)
		if !records[0].Timestamp.IsZero() || !records[1].Timestamp.IsZero() {
			t.Errorf("expected zero timestamp preserved when no valid time found")
		}
	})
}
