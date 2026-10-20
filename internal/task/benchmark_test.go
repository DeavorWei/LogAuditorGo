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

// seedBenchmarkTask 构造 10 万行任务用于聚合/查询基准（b.N 由 -benchtime 控制）。
func seedBenchmarkTask(b *testing.B, rows int) (*task.Service, string, func()) {
	b.Helper()
	dir := b.TempDir()
	storage.CloseAllTaskDBs()
	_ = storage.CloseKnowledgeDB()
	globalDB, err := storage.InitKnowledgeDB(filepath.Join(dir, "knowledge.db"))
	if err != nil {
		b.Fatalf("init db failed: %v", err)
	}
	taskDir := filepath.Join(dir, "tasks")
	svc := task.NewService(globalDB, taskDir, nil, nil)
	info, err := svc.CreateEmptyTask("bench-task", "")
	if err != nil {
		b.Fatalf("create task failed: %v", err)
	}
	b.Cleanup(func() {
		storage.CloseAllTaskDBs()
		_ = storage.CloseKnowledgeDB()
	})
	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, info.TaskID)
	if err != nil {
		b.Fatalf("open task db failed: %v", err)
	}

	modules := []struct {
		module string
		brief  string
		sev    int
	}{
		{"IFNET", "IF_DOWN", 4},
		{"BGP", "PEER_BACKWARD", 2},
		{"OSPF", "NBR_CHG", 4},
		{"AAA", "RADIUS_DOWN", 3},
	}
	baseTime := time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local)
	batch := make([]model.LogRecord, 0, 5000)
	for i := 0; i < rows; i++ {
		item := modules[i%len(modules)]
		ts := baseTime.Add(time.Duration(i) * time.Second)
		rec := model.LogRecord{
			DeviceID:       1,
			Timestamp:      ts,
			Hostname:       fmt.Sprintf("HOST-%d", i%5),
			Module:         item.module,
			Severity:       item.sev,
			Brief:          item.brief,
			SourceFile:     "bench.log",
			RawLog:         fmt.Sprintf("bench log %d", i),
			MessageBody:    fmt.Sprintf("bench log %d", i),
			ParametersJSON: "{}",
			KnowledgeID:    uint(i % 3),
			MatchTier:      "EXACT",
		}
		batch = append(batch, rec)
		if len(batch) >= 1000 {
			if err := taskDB.CreateInBatches(batch, 1000).Error; err != nil {
				b.Fatalf("insert logs failed: %v", err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := taskDB.CreateInBatches(batch, 1000).Error; err != nil {
			b.Fatalf("insert remaining logs failed: %v", err)
		}
	}
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", info.TaskID).
		Updates(map[string]interface{}{"log_count": rows, "matched_count": rows / 3, "status": model.TaskStatusCompleted}).Error
	storage.ReleaseTaskDB(info.TaskID)

	cleanup := func() {
		storage.CloseAllTaskDBs()
		_ = storage.CloseKnowledgeDB()
	}
	return svc, info.TaskID, cleanup
}

func BenchmarkDiagnosisSummary100k(b *testing.B) {
	svc, taskID, cleanup := seedBenchmarkTask(b, 100000)
	defer cleanup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.GetDiagnosisSummary(taskID); err != nil {
			b.Fatalf("summary failed: %v", err)
		}
	}
}

func BenchmarkQueryLogs100k(b *testing.B) {
	svc, taskID, cleanup := seedBenchmarkTask(b, 100000)
	defer cleanup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.QueryTaskLogsUnified(taskID, model.LogQueryRequestBody{
			Page: 1, PageSize: 100, ExcludeComment: true,
		}); err != nil {
			b.Fatalf("query failed: %v", err)
		}
	}
}

func BenchmarkLogHistogram100k(b *testing.B) {
	svc, taskID, cleanup := seedBenchmarkTask(b, 100000)
	defer cleanup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.GetLogHistogram(taskID, model.LogQueryRequestBody{ExcludeComment: true}, 60); err != nil {
			b.Fatalf("histogram failed: %v", err)
		}
	}
}

func BenchmarkAggregatedLogs100k(b *testing.B) {
	svc, taskID, cleanup := seedBenchmarkTask(b, 100000)
	defer cleanup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.GetAggregatedLogs(taskID, model.LogQueryRequestBody{ExcludeComment: true}, 300, 200); err != nil {
			b.Fatalf("aggregate failed: %v", err)
		}
	}
}
