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

func TestHubStop(t *testing.T) {
	hub := &Hub{
		jobs:        make(map[string]*JobTracker),
		stopJanitor: make(chan struct{}),
	}
	go hub.startJanitor()

	// 停止 janitor
	hub.Stop()
	// 重复调用 Stop 应当幂等且安全
	hub.Stop()
}

func TestSubscribeBufferSizeAndTerminalDelivery(t *testing.T) {
	stages := []StageDef{
		{Key: "S1", Name: "Step 1"},
	}
	tracker := NewJobTracker("test_overflow", "", "log", stages)

	ch, unsub := tracker.Subscribe()
	defer unsub()

	// 检查通道容量是否为 64
	if cap(ch) != 64 {
		t.Fatalf("expected channel buffer capacity 64, got %d", cap(ch))
	}

	// 产生 100 次更新以填满缓冲区并触发溢出清理逻辑
	for i := 0; i < 100; i++ {
		tracker.AddLog("info", "log msg %d", i)
	}

	// 立即完成任务
	tracker.Complete(nil, "完成啦")

	// 消耗通道所有消息，验证最后一条必定是终态 JobCompleted
	var lastSnap ProgressSnapshot
	for {
		select {
		case snap := <-ch:
			lastSnap = snap
		default:
			goto DONE
		}
	}
DONE:
	if lastSnap.Status != JobCompleted {
		t.Fatalf("expected last received event to be JobCompleted, got %s", lastSnap.Status)
	}
}

// TestJobTracker_TerminalGuard 验证 N-F/N-B 终态守卫：
// 当任务被 Cancel/Fail 置为终态后，后续的 SetStage、UpdateProgress、Increment、Complete 绝不能覆盖终态与取消文案
func TestJobTracker_TerminalGuard(t *testing.T) {
	stages := []StageDef{
		{Key: "STAGE_1", Name: "阶段一"},
		{Key: "STAGE_2", Name: "阶段二"},
	}
	tracker := NewJobTracker("test_terminal_guard", "task_term", "log", stages)

	tracker.SetStage("STAGE_1", "正在进行阶段一")
	tracker.UpdateProgress(30, 100, "处理 30%")

	// 用户点击终止
	canceled := tracker.Cancel("用户主动终止任务")
	if !canceled {
		t.Fatalf("expected Cancel to succeed")
	}

	snap := tracker.GetSnapshot()
	if snap.Status != JobFailed {
		t.Fatalf("expected status failed, got %s", snap.Status)
	}
	if snap.Message != "用户主动终止任务" {
		t.Fatalf("expected message '用户主动终止任务', got %q", snap.Message)
	}

	// 模拟后续异步操作尝试切换阶段、更新进度或标记完成
	tracker.SetStage("STAGE_2", "尝试覆盖阶段")
	tracker.UpdateProgress(100, 100, "尝试覆盖进度")
	tracker.Increment(10, "尝试增量")
	tracker.Complete(nil, "尝试标记完成")

	// 断言：终态完全受保护，状态依然是 JobFailed，消息未被污染
	finalSnap := tracker.GetSnapshot()
	if finalSnap.Status != JobFailed {
		t.Errorf("terminal guard failed: status was overridden to %s", finalSnap.Status)
	}
	if finalSnap.Message != "用户主动终止任务" {
		t.Errorf("terminal guard failed: message was overridden to %q", finalSnap.Message)
	}
}

