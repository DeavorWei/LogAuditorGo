package progress

import (
	"fmt"
	"testing"
	"time"
)

func TestJobTrackerLifecycle(t *testing.T) {
	stages := []StageDef{
		{Key: "STAGE_1", Name: "阶段一"},
		{Key: "STAGE_2", Name: "阶段二"},
		{Key: "STAGE_3", Name: "阶段三"},
	}

	tracker := NewJobTracker("test_job_1", "task_001", "log", stages)
	if tracker.JobID() != "test_job_1" {
		t.Fatalf("expected jobID test_job_1, got %s", tracker.JobID())
	}
	if tracker.TaskID() != "task_001" {
		t.Fatalf("expected taskID task_001, got %s", tracker.TaskID())
	}

	ch, unsub := tracker.Subscribe()
	defer unsub()

	// 阶段一
	tracker.SetStage("STAGE_1", "开始执行阶段一")
	tracker.AddLog("info", "阶段一初始化日志")
	tracker.UpdateProgress(50, 100, "阶段一进行到一半")

	snap := tracker.GetSnapshot()
	if snap.Status != JobRunning {
		t.Fatalf("expected status running, got %s", snap.Status)
	}
	if snap.CurrentStage != "阶段一" {
		t.Fatalf("expected stage 阶段一, got %s", snap.CurrentStage)
	}
	if snap.Current != 50 || snap.Total != 100 {
		t.Fatalf("expected 50/100, got %d/%d", snap.Current, snap.Total)
	}

	// 阶段二
	tracker.SetStage("STAGE_2", "切换到阶段二")
	tracker.Increment(10, "递增测试")

	// 完成
	tracker.Complete(map[string]string{"foo": "bar"}, "测试完成")
	snap = tracker.GetSnapshot()
	if snap.Status != JobCompleted {
		t.Fatalf("expected status completed, got %s", snap.Status)
	}
	if snap.Percent != 100.0 {
		t.Fatalf("expected 100%%, got %f", snap.Percent)
	}

	// 验证订阅通道接收
	select {
	case received := <-ch:
		if received.JobID != "test_job_1" {
			t.Fatalf("unexpected message from channel: %+v", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for subscription event")
	}
}

func TestHubJobManagement(t *testing.T) {
	hub := GetHub()
	stages := []StageDef{
		{Key: "S1", Name: "Step 1"},
	}

	job := hub.NewJob("hdx", "", stages)
	if job == nil {
		t.Fatalf("expected job created, got nil")
	}

	retrieved := hub.GetJob(job.JobID())
	if retrieved != job {
		t.Fatalf("expected to retrieve same job")
	}

	job.Fail(fmt.Errorf("simulated error"), "模拟错误")
	snap := job.GetSnapshot()
	if snap.Status != JobFailed {
		t.Fatalf("expected failed status, got %s", snap.Status)
	}
}
