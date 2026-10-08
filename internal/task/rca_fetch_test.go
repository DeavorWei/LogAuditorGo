package task

import (
	"context"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

// TestFetchNormLogsForRCA_CompositeKeysetCursor 验证复合游标分页的完整性与排序语义：
// 1. 12000 行（> batchSize 5000，覆盖多批次翻页）全部返回、无重复、无遗漏；
// 2. 返回序列严格按 (timestamp, id) 升序，与入库顺序无关；
// 3. 同一秒的多行按 id 决胜，游标不丢失、不重复。
func TestFetchNormLogsForRCA_CompositeKeysetCursor(t *testing.T) {
	logger.Init("debug", "console")

	taskDir := filepath.Join(t.TempDir(), "tasks")
	taskID := "keysetfetch001"
	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskID)
	if err != nil {
		t.Fatalf("open task db failed: %v", err)
	}
	defer storage.CloseTaskDB(taskID)

	const total = 12000
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	r := rand.New(rand.NewSource(42))
	perm := r.Perm(total / 3) // 4000 个互不相同的秒偏移

	// 按"乱序秒偏移 + 每 3 行同秒"写入：id 顺序与时间顺序完全无关
	batch := make([]model.LogRecord, 0, 1000)
	for i := 0; i < total; i++ {
		batch = append(batch, model.LogRecord{
			Timestamp: base.Add(time.Duration(perm[i/3]) * time.Second),
			Hostname:  "SW-01",
			Module:    "IFNET",
			Brief:     "IF_DOWN",
			Severity:  4,
			RawLog:    "Interface down.",
		})
		if len(batch) == 1000 {
			if err := taskDB.Create(&batch).Error; err != nil {
				t.Fatalf("insert batch failed: %v", err)
			}
			batch = batch[:0]
		}
	}

	logs, truncated, err := fetchNormLogsForRCA(context.Background(), taskDB)
	if err != nil {
		t.Fatalf("fetchNormLogsForRCA failed: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation: total %d < cap %d", total, maxRCALogs)
	}
	if len(logs) != total {
		t.Fatalf("expected all %d logs across multiple batches, got %d", total, len(logs))
	}

	// 无重复、无遗漏
	seen := make(map[uint]bool, total)
	for _, l := range logs {
		if seen[l.ID] {
			t.Fatalf("duplicate log id %d returned by keyset pagination", l.ID)
		}
		seen[l.ID] = true
	}
	if len(seen) != total {
		t.Fatalf("expected %d distinct ids, got %d", total, len(seen))
	}

	// 严格 (timestamp, id) 升序
	for i := 1; i < len(logs); i++ {
		prev, cur := logs[i-1], logs[i]
		if cur.Timestamp.Before(prev.Timestamp) {
			t.Fatalf("logs[%d].Timestamp (%v) before logs[%d].Timestamp (%v): composite cursor ordering broken",
				i, cur.Timestamp, i-1, prev.Timestamp)
		}
		if cur.Timestamp.Equal(prev.Timestamp) && cur.ID <= prev.ID {
			t.Fatalf("equal timestamps must be ordered by id: logs[%d].ID=%d <= logs[%d].ID=%d",
				i, cur.ID, i-1, prev.ID)
		}
	}
}

// TestFetchNormLogsForRCA_TruncationProbe 验证样本超限时的截断探测：
// 1. 命中上限时 truncated=true，返回数量恰为上限，且是 (timestamp, id) 意义上最早的样本；
// 2. 总量恰好等于上限时（无更多行）不得误报 truncated；
// 3. 截断提示路径（fetchTruncated → TIMEOUT + 明确文案）依赖该返回值。
func TestFetchNormLogsForRCA_TruncationProbe(t *testing.T) {
	logger.Init("debug", "console")

	taskDir := filepath.Join(t.TempDir(), "tasks")
	taskID := "truncprobe002"
	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskID)
	if err != nil {
		t.Fatalf("open task db failed: %v", err)
	}
	defer storage.CloseTaskDB(taskID)

	const total = 9000
	base := time.Date(2026, 4, 15, 10, 0, 0, 0, time.Local)
	r := rand.New(rand.NewSource(7))
	perm := r.Perm(total)

	var all []model.LogRecord
	batch := make([]model.LogRecord, 0, 1000)
	for i := 0; i < total; i++ {
		batch = append(batch, model.LogRecord{
			Timestamp: base.Add(time.Duration(perm[i]) * time.Second),
			Hostname:  "SW-01",
			Module:    "IFNET",
			Brief:     "IF_DOWN",
			Severity:  4,
			RawLog:    "Interface down.",
		})
		if len(batch) == 1000 {
			if err := taskDB.Create(&batch).Error; err != nil {
				t.Fatalf("insert batch failed: %v", err)
			}
			all = append(all, batch...)
			batch = batch[:0]
		}
	}

	// 期望集合：按 (timestamp, id) 升序取前 cap 行
	sorted := make([]model.LogRecord, len(all))
	copy(sorted, all)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Timestamp.Equal(sorted[j].Timestamp) {
			return sorted[i].Timestamp.Before(sorted[j].Timestamp)
		}
		return sorted[i].ID < sorted[j].ID
	})

	orig := maxRCALogs
	defer func() { maxRCALogs = orig }()

	// 场景 1：超限截断
	maxRCALogs = 7000
	logs, truncated, err := fetchNormLogsForRCA(context.Background(), taskDB)
	if err != nil {
		t.Fatalf("fetchNormLogsForRCA failed: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated=true when total %d exceeds cap %d", total, maxRCALogs)
	}
	if len(logs) != maxRCALogs {
		t.Fatalf("expected exactly %d logs (cap), got %d", maxRCALogs, len(logs))
	}
	for i := 0; i < len(logs); i++ {
		if logs[i].ID != sorted[i].ID {
			t.Fatalf("truncated sample must be the earliest %d rows by (timestamp, id): index %d got id %d, want %d",
				maxRCALogs, i, logs[i].ID, sorted[i].ID)
		}
	}

	// 场景 2：总量恰好等于上限（探测发现无更多行），不得误报截断
	maxRCALogs = total
	logsExact, truncatedExact, err := fetchNormLogsForRCA(context.Background(), taskDB)
	if err != nil {
		t.Fatalf("fetchNormLogsForRCA failed: %v", err)
	}
	if truncatedExact {
		t.Fatalf("unexpected truncated=true when total %d exactly equals cap", total)
	}
	if len(logsExact) != total {
		t.Fatalf("expected all %d logs, got %d", total, len(logsExact))
	}
}
