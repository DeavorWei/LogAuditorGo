package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

// TestIngestStreamingPipeline_OrderAndTimestamp 验证乱序 Worker 场景下 ReorderBuffer 严格保序与时间戳继承
func TestIngestStreamingPipeline_OrderAndTimestamp(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_order_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-order-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-order-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-order-test",
		cleanName:  "order_test.log",
		totalLines: 5,
		deviceID:   1,
	}

	lines := []string{
		"# Header line without timestamp",
		"Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: GigabitEthernet0/0/1 down.",
		"Speed changed to 1000M", // 无时间戳，继承前序
		"Duplex changed to FULL", // 无时间戳，继续继承
		"Apr 15 2026 14:00:10 CORE-SW-01 %%01IFNET/4/IF_UP(l)[2]: GigabitEthernet0/0/1 up.",
	}

	cfg := PipelineConfig{
		WorkerNum:     4,
		LineChanCap:   16,
		RecordChanCap: 32,
		BatchSize:     2, // 较小 batch，多次触发 flush
		FlushInterval: 100 * time.Millisecond,
	}

	lineSource := makeSliceLineSource(lines)
	err = svc.IngestStreamingPipeline(
		context.Background(),
		taskDB,
		lineSource,
		bundle,
		"CloudEngine",
		"",
		cfg,
		nil,
	)
	if err != nil {
		t.Fatalf("pipeline failed: %v", err)
	}

	var records []model.LogRecord
	if err := taskDB.Order("id asc").Find(&records).Error; err != nil {
		t.Fatalf("query records failed: %v", err)
	}

	if len(records) != len(lines) {
		t.Fatalf("expected %d records, got %d", len(lines), len(records))
	}

	// 验证时间戳对齐
	t1 := records[0].Timestamp
	t2 := records[1].Timestamp
	t3 := records[2].Timestamp
	t4 := records[3].Timestamp
	t5 := records[4].Timestamp

	if !t1.Equal(t2) {
		t.Errorf("t1 should borrow first valid timestamp t2: t1=%v, t2=%v", t1, t2)
	}
	if !t3.Equal(t2) {
		t.Errorf("t3 should inherit t2: t3=%v, t2=%v", t3, t2)
	}
	if !t4.Equal(t2) {
		t.Errorf("t4 should inherit t2: t4=%v, t2=%v", t4, t2)
	}
	if !t5.After(t4) {
		t.Errorf("t5 should be after t4: t5=%v, t4=%v", t5, t4)
	}
}

// TestIngestStreamingPipeline_ErrorCancelAndDrain 验证落库失败时取消传播与 Goroutine 泄漏防护 (R2)
func TestIngestStreamingPipeline_ErrorCancelAndDrain(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_err_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-err-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-err-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-err-test",
		cleanName:  "err_test.log",
		totalLines: 100,
		deviceID:   1,
	}

	// 模拟产出 500 行的 Reader
	lines := make([]string, 500)
	for i := 0; i < 500; i++ {
		lines[i] = fmt.Sprintf("Apr 15 2026 14:00:01 CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface %d down.", i+1, i+1)
	}

	// 记录初始 goroutine 数量
	runtime.GC()
	initialGoroutines := runtime.NumGoroutine()

	// 使用一个模拟故障的数据源（模拟扫描超长行错误）
	faultySource := func(lineChan chan<- indexedLine, ctx context.Context) error {
		for i := 0; i < 20; i++ {
			select {
			case <-ctx.Done():
				return nil
			case lineChan <- indexedLine{seq: i, text: lines[i]}:
			}
		}
		// 模拟扫描到超长行直接报错退出
		return errors.New("bufio.Scanner: token too long")
	}

	cfg := PipelineConfig{
		WorkerNum:     8,
		LineChanCap:   16,
		RecordChanCap: 32,
		BatchSize:     50,
		FlushInterval: 100 * time.Millisecond,
	}

	pipeErr := svc.IngestStreamingPipeline(
		context.Background(),
		taskDB,
		faultySource,
		bundle,
		"CloudEngine",
		"",
		cfg,
		nil,
	)

	if pipeErr == nil {
		t.Fatalf("expected pipeline to return scanner error, got nil")
	}

	// 等待短暂时间后验证 goroutine 是否全部收敛归还（无泄漏）
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	currentGoroutines := runtime.NumGoroutine()
	// 允许少量波动（如 runtime internal goroutines）
	if currentGoroutines > initialGoroutines+5 {
		t.Errorf("possible goroutine leak: initial=%d, current=%d", initialGoroutines, currentGoroutines)
	}
}

// TestIngestStreamingPipeline_ConcurrentStress 验证高并发压力下批次刷盘完整性
func TestIngestStreamingPipeline_ConcurrentStress(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_stress_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-stress-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-stress-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-stress-test",
		cleanName:  "stress.log",
		totalLines: 1200,
		deviceID:   1,
	}

	lines := make([]string, 1200)
	for i := 0; i < 1200; i++ {
		lines[i] = fmt.Sprintf("Apr 15 2026 14:00:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface ge-%d down.", i%60, i+1, i)
	}

	cfg := PipelineConfig{
		WorkerNum:     16,
		LineChanCap:   256,
		RecordChanCap: 512,
		BatchSize:     200,
		FlushInterval: 50 * time.Millisecond,
	}

	var progressReports []int
	var pMu sync.Mutex
	onProgress := func(n int) {
		pMu.Lock()
		defer pMu.Unlock()
		progressReports = append(progressReports, n)
	}

	err = svc.IngestStreamingPipeline(
		context.Background(),
		taskDB,
		makeSliceLineSource(lines),
		bundle,
		"CloudEngine",
		"",
		cfg,
		onProgress,
	)
	if err != nil {
		t.Fatalf("stress pipeline failed: %v", err)
	}

	var count int64
	taskDB.Model(&model.LogRecord{}).Count(&count)
	if count != 1200 {
		t.Fatalf("expected 1200 records in task db, got %d", count)
	}

	// 验证 TaskFile 实体已正确创建且 LineCount 正确
	var tf model.TaskFile
	if err := taskDB.First(&tf, "file_name = ?", "stress.log").Error; err != nil {
		t.Fatalf("task file not found: %v", err)
	}
	if tf.LineCount != 1200 {
		t.Errorf("task file line count mismatch: got %d, want 1200", tf.LineCount)
	}
}

// TestService_GetEffectiveParseWorkers 验证 worker 数量配置与上下限钳制
func TestService_GetEffectiveParseWorkers(t *testing.T) {
	svc := &Service{}

	// 1. 未配置时（GlobalConfig 为空或 0），自适应 NumCPU 并夹在 [2, 32]
	config.GlobalConfig = nil
	wDefault := svc.getEffectiveParseWorkers()
	if wDefault < minParseWorkers || wDefault > maxParseWorkers {
		t.Errorf("expected wDefault in [%d, %d], got %d", minParseWorkers, maxParseWorkers, wDefault)
	}

	// 2. 配置生效
	config.GlobalConfig = &config.Config{
		Task: config.TaskConfig{ParseWorkers: 12},
	}
	if got := svc.getEffectiveParseWorkers(); got != 12 {
		t.Errorf("expected configured 12, got %d", got)
	}

	// 3. 超过上限被钳制为 maxParseWorkers (32)
	config.GlobalConfig.Task.ParseWorkers = 128
	if got := svc.getEffectiveParseWorkers(); got != maxParseWorkers {
		t.Errorf("expected capped at %d, got %d", maxParseWorkers, got)
	}

	// 4. 小于下限被钳制为 minParseWorkers (2)
	config.GlobalConfig.Task.ParseWorkers = 1
	if got := svc.getEffectiveParseWorkers(); got != minParseWorkers {
		t.Errorf("expected capped at %d, got %d", minParseWorkers, got)
	}

	// 恢复全局配置
	config.GlobalConfig = nil
}
