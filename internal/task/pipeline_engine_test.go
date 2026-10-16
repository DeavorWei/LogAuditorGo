package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestPersistFileBundle_CompensateCleanup_OnFailure 验证 M1 缺陷修复：
// 当导入中途出现读错误或管道故障时，补偿清理必须彻底删除 TaskFile 和已入库碎片，绝不残留幽灵记录
func TestPersistFileBundle_CompensateCleanup_OnFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bundle_cleanup_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "task-cleanup-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("task-cleanup-test")

	// 创建一个坏文件：在第 50 行写入超过 1MB 的超长单行，触发 Scanner 错误
	badFilePath := filepath.Join(tmpDir, "corrupted.log")
	f, err := os.Create(badFilePath)
	if err != nil {
		t.Fatalf("create bad file failed: %v", err)
	}
	for i := 0; i < 50; i++ {
		f.WriteString(fmt.Sprintf("Apr 15 2026 14:00:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface down.\n", i%60, i+1))
	}
	// 写入超过 maxScanLineBytes (1MB) 的超长 token
	superLongLine := strings.Repeat("X", 2*1024*1024) + "\n"
	f.WriteString(superLongLine)
	f.Close()

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "task-cleanup-test",
		cleanName:  "corrupted.log",
		totalLines: 100,
		deviceID:   1,
		item: FileUploadItem{
			FileName: "corrupted.log",
			FilePath: badFilePath,
			FileSize: 2 * 1024 * 1024,
		},
	}

	err = svc.persistFileBundle(context.Background(), "task-cleanup-test", taskDB, bundle, "CloudEngine", "", nil)
	if err == nil {
		t.Fatalf("expected persistFileBundle to fail on corrupted file, got nil")
	}

	// 关键断言 (M1 闭环)：无论 pipeline 内部是否创建了 TaskFile，补偿清理后 task_files 必须为 0 条！
	var tfCount int64
	taskDB.Model(&model.TaskFile{}).Where("task_id = ? AND file_name = ?", "task-cleanup-test", "corrupted.log").Count(&tfCount)
	if tfCount != 0 {
		t.Errorf("M1 regression: task_files has ghost records: count=%d, want 0", tfCount)
	}

	var recCount int64
	taskDB.Model(&model.LogRecord{}).Where("source_file = ?", "corrupted.log").Count(&recCount)
	if recCount != 0 {
		t.Errorf("log_records not cleaned up: count=%d, want 0", recCount)
	}
}

// TestIngestStreamingPipeline_ContextCancel 验证 N1 缺陷修复：
// 外部取消 ctx 时，流水线必须返回 ctx.Err()，绝不能将其混同于正常 EOF
func TestIngestStreamingPipeline_ContextCancel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_cancel_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-cancel-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-cancel-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-cancel-test",
		cleanName:  "cancel.log",
		totalLines: 1000,
		deviceID:   1,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// 模拟流式读取：读到第 30 行时主动触发 cancel()
	cancelSource := func(lineChan chan<- indexedLine, c context.Context) error {
		for i := 0; i < 500; i++ {
			if i == 30 {
				cancel()
			}
			select {
			case <-c.Done():
				return c.Err()
			case lineChan <- indexedLine{seq: i, text: fmt.Sprintf("Apr 15 2026 14:00:%02d SW %%%%01SYS/4/UP: up %d", i%60, i)}:
			}
		}
		return nil
	}

	cfg := PipelineConfig{
		WorkerNum:     4,
		LineChanCap:   16,
		RecordChanCap: 32,
		BatchSize:     50,
		FlushInterval: 100 * time.Millisecond,
	}

	pipeErr := svc.IngestStreamingPipeline(
		ctx,
		taskDB,
		cancelSource,
		bundle,
		"CloudEngine",
		"",
		cfg,
		nil,
	)

	// 必须返回 context.Canceled，证明取消与正常 EOF 明确区分
	if !errors.Is(pipeErr, context.Canceled) {
		t.Fatalf("N1 regression: expected context.Canceled error, got %v", pipeErr)
	}
}

// TestIngestStreamingPipeline_LeadingCommentsTickFlush 验证 N2 缺陷修复：
// 文件前部存在大量无时间戳注释行，且中途触发 Ticker 定时刷盘时，不得破坏时间戳向前借用对齐
func TestIngestStreamingPipeline_LeadingCommentsTickFlush(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_tick_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-tick-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-tick-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-tick-test",
		cleanName:  "tick.log",
		totalLines: 30,
		deviceID:   1,
	}

	// 前 10 行是无时间戳注释行，后 20 行是带时间戳的日志
	lines := make([]string, 30)
	for i := 0; i < 10; i++ {
		lines[i] = fmt.Sprintf("###### Comment Header Line %d without timestamp ######", i+1)
	}
	for i := 10; i < 30; i++ {
		lines[i] = fmt.Sprintf("Apr 15 2026 14:00:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface down.", i%60, i+1)
	}

	// FlushInterval 设为极短 (10ms)，确保触发多次 Ticker 刷盘判断
	cfg := PipelineConfig{
		WorkerNum:     2,
		LineChanCap:   16,
		RecordChanCap: 32,
		BatchSize:     100, // 大于 30，确保不会因为 BatchSize 触发提前 flush
		FlushInterval: 10 * time.Millisecond,
	}

	slowSource := func(lineChan chan<- indexedLine, ctx context.Context) error {
		for i, l := range lines {
			if i < 10 {
				// 前 10 行每行稍作停顿，故意触发 Ticker
				time.Sleep(15 * time.Millisecond)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case lineChan <- indexedLine{seq: i, text: l}:
			}
		}
		return nil
	}

	err = svc.IngestStreamingPipeline(
		context.Background(),
		taskDB,
		slowSource,
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
		t.Fatalf("find records failed: %v", err)
	}
	if len(records) != 30 {
		t.Fatalf("expected 30 records, got %d", len(records))
	}

	// 验证：第 1~10 条前导注释行的时间戳必须成功对齐到第 11 条日志的时间戳，绝对不能为零值！
	targetTS := records[10].Timestamp
	if targetTS.IsZero() || targetTS.Year() <= 1970 {
		t.Fatalf("record[10] timestamp is invalid: %v", targetTS)
	}
	for i := 0; i < 10; i++ {
		if records[i].Timestamp.IsZero() || records[i].Timestamp.Year() <= 1970 {
			t.Errorf("N2 regression: record[%d] timestamp is zero (not aligned): %v", i, records[i].Timestamp)
		}
		if !records[i].Timestamp.Equal(targetTS) {
			t.Errorf("record[%d] timestamp mismatch: got %v, want %v", i, records[i].Timestamp, targetTS)
		}
	}
}

// TestIngestStreamingPipeline_WorkerPanicRecovery 验证 M2 缺陷修复：
// 当解析过程发生 panic 时，Worker 协程单行 recover 兜底生效，协程不退出，整进程不崩溃，落库行数守恒
func TestIngestStreamingPipeline_WorkerPanicRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "pipeline_panic_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDB, _, err := storage.GetOrCreateTaskDB(tmpDir, "pipe-panic-test")
	if err != nil {
		t.Fatalf("init task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB("pipe-panic-test")

	svc := &Service{}
	bundle := &fileBundle{
		taskID:     "pipe-panic-test",
		cleanName:  "panic_recovery.log",
		totalLines: 100,
		deviceID:   1,
	}

	// 构造 100 行日志，其中包含正常行、畸形行、特殊字符行
	lines := make([]string, 100)
	for i := 0; i < 100; i++ {
		if i == 50 {
			// 极端畸形行（包含大量不可见控制字符和 ANSI 序列）
			lines[i] = "\x1b[31;1m\x00\xff\xfe\x01\x02MALFORMED_SPECIAL_LINE\x1b[0m"
		} else {
			lines[i] = fmt.Sprintf("Apr 15 2026 14:00:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface down.", i%60, i+1)
		}
	}

	cfg := PipelineConfig{
		WorkerNum:     4,
		LineChanCap:   16,
		RecordChanCap: 32,
		BatchSize:     25,
		FlushInterval: 50 * time.Millisecond,
	}

	err = svc.IngestStreamingPipeline(
		context.Background(),
		taskDB,
		makeSliceLineSource(lines),
		bundle,
		"CloudEngine",
		"",
		cfg,
		nil,
	)
	if err != nil {
		t.Fatalf("pipeline failed with unexpected error: %v", err)
	}

	var count int64
	taskDB.Model(&model.LogRecord{}).Count(&count)
	if count != 100 {
		t.Fatalf("line count not preserved: got %d, want 100", count)
	}
}


