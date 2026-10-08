package task_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

type stepCancelContext struct {
	doneCh    chan struct{}
	callCount int
	threshold int
	err       error
}

func (c *stepCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *stepCancelContext) Done() <-chan struct{} {
	c.callCount++
	if c.callCount >= c.threshold {
		select {
		case <-c.doneCh:
		default:
			close(c.doneCh)
			c.err = context.DeadlineExceeded
		}
	}
	return c.doneCh
}
func (c *stepCancelContext) Err() error { return c.err }
func (c *stepCancelContext) Value(key any) any { return nil }

func setupE2ETaskService(t *testing.T) (*task.Service, *gorm.DB, string, func()) {
	t.Helper()
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "rca_e2e_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	_ = storage.CloseKnowledgeDB()
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "test.bleve")
	indexer, err := search.InitIndexer(indexPath)
	if err != nil {
		t.Fatalf("init indexer failed: %v", err)
	}

	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")

	oldConfig := config.GlobalConfig
	cfg := &config.Config{
		RCA: config.RCAConfig{
			Timeout:     300,
			Concurrency: 2,
		},
	}
	config.GlobalConfig = cfg

	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	cleanup := func() {
		config.GlobalConfig = oldConfig
		task.InitRCAScheduler(2)
		_ = indexer.Close()
		_ = storage.CloseKnowledgeDB()
		_ = os.RemoveAll(tmpDir)
	}

	return svc, globalDB, taskDir, cleanup
}

// 验收指标 1: 跨重叠滑动窗口的长故障因果链防漏报验证
// 构造跨越 300s 窗口与 60s 重叠边界的 5 级故障级联链 (IFNET -> BFD -> OSPF -> BGP -> RM)
// 验证在动态水位剪枝机制下，检出率 100%，无任何跨窗口断链或漏报
func TestE2E_RCA_CrossWindowChainIntegrity(t *testing.T) {
	svc, _, _, cleanup := setupE2ETaskService(t)
	defer cleanup()

	taskInfo, err := svc.CreateEmptyTask("E2E-ChainIntegrity-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	// 构造时序跨度跨越第 300 秒边界 (10:05:00) 且生成 ≥3 个重叠簇的级联故障日志集：
	// T+0s:   DEVM/4/DEV_NORMAL (启动时间锚点，使 Cluster 1 严格覆盖 10:00:00 ~ 10:05:00，此时 IFNET 处于尾部且下游尚未出现)
	// T+280s: IFNET/4/IF_DOWN (Root 根因，位于 Cluster 1 尾部 10:04:40，在 Cluster 1 内尚无下游，触发水位记录)
	// --- 第 300 秒边界 (10:05:00) 跨越线 ---
	// T+310s: BFD/2/BFD_SESS_DOWN (下游 1，位于 Cluster 2 10:05:10，超出 Cluster 1 边界)
	// T+320s: OSPF/4/NBR_CHG (下游 2，位于 Cluster 2 10:05:20)
	// T+330s: BGP/2/PEER_DOWN (下游 3，位于 Cluster 2 10:05:30)
	// T+340s: RM/4/ROUTE_DELETE (下游 4，位于 Cluster 2 10:05:40)
	// T+600s: DEVM/4/DEV_NORMAL (结束时间锚点，强制生成 Cluster 3 覆盖 10:10:00)
	logLines := `
Apr 15 2026 10:00:00 CORE-SW-01 %%01DEVM/4/DEV_NORMAL(l)[1]: Device normal status.
Apr 15 2026 10:04:40 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[2]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 10:05:10 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[3]: BFD session state changed to DOWN. (SessionID=10)
Apr 15 2026 10:05:20 CORE-SW-01 %%01OSPF/4/NBR_CHG(l)[4]: OSPF Neighbor status changed. (Neighbor=192.168.1.2)
Apr 15 2026 10:05:30 CORE-SW-01 %%01BGP/2/PEER_DOWN(l)[5]: The BGP peer went Down. (PeerIP=192.168.1.2)
Apr 15 2026 10:05:40 CORE-SW-01 %%01RM/4/ROUTE_DELETE(l)[6]: Routing table entry deleted. (Prefix=10.0.0.0/8)
Apr 15 2026 10:10:00 CORE-SW-01 %%01DEVM/4/DEV_NORMAL(l)[7]: Device normal status.
`

	item := task.FileUploadItem{
		FileName: "cascade.log",
		FileSize: int64(len(logLines)),
		Content:  logLines,
	}

	importedInfo, err := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 断言 1: 主导入流程立即就绪，状态为 COMPLETED
	if importedInfo.Status != model.TaskStatusCompleted {
		t.Fatalf("expected task status to be COMPLETED, got %s", importedInfo.Status)
	}

	// 等待 RCA 后台分析完成
	ok := svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)
	if !ok {
		t.Fatalf("timed out waiting for RCA to finish")
	}

	finalInfo, err := svc.GetTaskByID(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}

	// 断言 2: RCA 状态更新为 COMPLETED，RcaCount 恰好为 1
	if finalInfo.RCAStatus != model.RCAStatusCompleted {
		t.Fatalf("expected RCAStatus to be COMPLETED, got %s (err: %s)", finalInfo.RCAStatus, finalInfo.RCAErrorMessage)
	}
	if finalInfo.RcaCount != 1 {
		t.Fatalf("expected exactly 1 RCA event, got %d", finalInfo.RcaCount)
	}

	// 断言 3: 获取富化 RCA 事件详情，验证全链路 4 个下游事件均被精准关联
	enriched, err := svc.GetEnrichedRCAEvents(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get enriched RCA events failed: %v", err)
	}
	if len(enriched) != 1 {
		t.Fatalf("expected 1 enriched RCA event, got %d", len(enriched))
	}

	ev := enriched[0]
	if ev.RootModule != "IFNET" || ev.RootBrief != "IF_DOWN" {
		t.Fatalf("expected root to be IFNET/IF_DOWN, got %s/%s", ev.RootModule, ev.RootBrief)
	}
	if ev.CorrelatedCount != 4 {
		t.Fatalf("expected 4 correlated cascade events across windows, got %d", ev.CorrelatedCount)
	}

	// 验证 4 个衍生事件的时序与模块完整性
	if len(ev.ImpactDetails) != 4 {
		t.Fatalf("expected 4 impact details steps, got %d", len(ev.ImpactDetails))
	}
	expectedCascade := []struct {
		module string
		brief  string
	}{
		{"BFD", "BFD_SESS_DOWN"},
		{"OSPF", "NBR_CHG"},
		{"BGP", "PEER_DOWN"},
		{"RM", "ROUTE_DELETE"},
	}
	for i, exp := range expectedCascade {
		act := ev.ImpactDetails[i]
		if act.Module != exp.module || act.Brief != exp.brief {
			t.Errorf("cascade step %d: expected %s/%s, got %s/%s", i, exp.module, exp.brief, act.Module, act.Brief)
		}
	}
}

// 验收指标 2: 超时熔断、状态标记与截断时序边界落库验证
// 注入极短执行超时预算，断言系统安全中止、状态置为 TIMEOUT、已分析事件不丢失、截断时序边界正确持久化
func TestE2E_RCA_TimeoutCircuitBreakerAndTruncation(t *testing.T) {
	svc, _, taskDir, cleanup := setupE2ETaskService(t)
	defer cleanup()

	taskInfo, err := svc.CreateEmptyTask("E2E-Timeout-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	// 生成包含多批次告警的日志数据
	var logBuilder string
	for i := 0; i < 50; i++ {
		sec := i * 15
		logBuilder += fmt.Sprintf("Apr 15 2026 10:%02d:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface down.\n",
			sec/60, sec%60, i*2+1)
		logBuilder += fmt.Sprintf("Apr 15 2026 10:%02d:%02d CORE-SW-01 %%%%01BFD/2/BFD_SESS_DOWN(l)[%d]: Session down.\n",
			sec/60, (sec%60)+1, i*2+2)
	}

	item := task.FileUploadItem{
		FileName: "timeout_test.log",
		FileSize: int64(len(logBuilder)),
		Content:  logBuilder,
	}

	_, err = svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 等待初始导入完成
	svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)

	// 模拟执行超时熔断场景：
	// 使用 stepCancelContext，在引擎检查点调用达到第 15 次时精确触发超时熔断
	calcCtx := &stepCancelContext{
		doneCh:    make(chan struct{}),
		threshold: 15,
	}

	svc.ExecuteRCAPipelineForTest(calcCtx, taskInfo.TaskID)

	// 验证任务元数据
	updated, err := svc.GetTaskByID(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}

	// 断言状态为 TIMEOUT
	if updated.RCAStatus != model.RCAStatusTimeout {
		t.Fatalf("expected RCAStatus to be TIMEOUT, got %s", updated.RCAStatus)
	}
	if updated.RCAErrorMessage == "" {
		t.Errorf("expected non-empty RCAErrorMessage for timeout task")
	}

	// 断言结束时间与截断时间戳正常写入
	if updated.RCAFinishTime == nil {
		t.Errorf("expected non-nil RCAFinishTime")
	}
	if updated.RCAAnalyzedUntil == nil || updated.RCAAnalyzedUntil.IsZero() {
		t.Errorf("expected non-zero RCAAnalyzedUntil for midway timeout")
	}

	// 验证 taskDB 数据一致性
	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	if err != nil {
		t.Fatalf("open taskDB failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	var inTaskDB model.TaskInfo
	if err := taskDB.Where("task_id = ?", taskInfo.TaskID).First(&inTaskDB).Error; err != nil {
		t.Fatalf("find task in taskDB failed: %v", err)
	}
	if inTaskDB.RCAStatus != model.RCAStatusTimeout {
		t.Errorf("expected RCAStatus in taskDB to be TIMEOUT, got %s", inTaskDB.RCAStatus)
	}
}

// 验收指标 3: 调度器并发排队与信号量保护验证
// 设置并发槽位为 2，同时触发 4 个任务的 RCA，验证排队按序消费无死锁、无竞争冲突
func TestE2E_RCA_ConcurrencySlotQueueing(t *testing.T) {
	svc, _, _, cleanup := setupE2ETaskService(t)
	defer cleanup()

	// 显式将全局调度器并发槽位限制为 2
	task.InitRCAScheduler(2)

	const taskCount = 4
	tasks := make([]*model.TaskInfo, taskCount)

	logContent := `
Apr 15 2026 10:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.
Apr 15 2026 10:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: Session down.
`
	for i := 0; i < taskCount; i++ {
		tInfo, err := svc.CreateEmptyTask(fmt.Sprintf("Concurrent-Task-%d", i), "CloudEngine")
		if err != nil {
			t.Fatalf("create task %d failed: %v", i, err)
		}
		item := task.FileUploadItem{
			FileName: fmt.Sprintf("log_%d.log", i),
			FileSize: int64(len(logContent)),
			Content:  logContent,
		}
		_, err = svc.ImportLogs(tInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
		if err != nil {
			t.Fatalf("import logs for task %d failed: %v", i, err)
		}
		tasks[i] = tInfo
	}

	// 确保初始导入已完成结算
	for _, tInfo := range tasks {
		svc.WaitForTaskRCA(tInfo.TaskID, 5*time.Second)
	}

	// 并发触发 4 个任务的 RCA 重跑
	var wg sync.WaitGroup
	for _, tInfo := range tasks {
		wg.Add(1)
		go func(tid string) {
			defer wg.Done()
			svc.TriggerTaskRCA(tid, true)
		}(tInfo.TaskID)
	}
	wg.Wait()

	// 等待所有 4 个任务均排队处理完成
	for i, tInfo := range tasks {
		ok := svc.WaitForTaskRCA(tInfo.TaskID, 10*time.Second)
		if !ok {
			t.Fatalf("task %d (%s) timed out waiting for RCA queue consumption", i, tInfo.TaskID)
		}
	}

	// 校验所有 4 个任务状态均顺利达到 COMPLETED
	for i, tInfo := range tasks {
		info, err := svc.GetTaskByID(tInfo.TaskID)
		if err != nil {
			t.Fatalf("get task %d failed: %v", i, err)
		}
		if info.RCAStatus != model.RCAStatusCompleted {
			t.Errorf("task %d: expected RCAStatus COMPLETED, got %s", i, info.RCAStatus)
		}
		if info.RcaCount != 1 {
			t.Errorf("task %d: expected RcaCount 1, got %d", i, info.RcaCount)
		}
	}
}

// 验收指标 4: 数据库连接池隔离与无阻塞并发查询验证
// 验证在 RCA 密集推导期间，前台查询与任务库访问完全不发生 SQLite "database is locked"
func TestE2E_RCA_DatabaseConnectionIsolation(t *testing.T) {
	svc, _, _, cleanup := setupE2ETaskService(t)
	defer cleanup()

	taskInfo, err := svc.CreateEmptyTask("DB-Isolation-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sec := i * 5
		sb.WriteString(fmt.Sprintf("Apr 15 2026 10:%02d:%02d CORE-SW-01 %%%%01IFNET/4/IF_DOWN(l)[%d]: Interface down.\n", sec/60, sec%60, i*2+1))
		sb.WriteString(fmt.Sprintf("Apr 15 2026 10:%02d:%02d CORE-SW-01 %%%%01BFD/2/BFD_SESS_DOWN(l)[%d]: Session down.\n", sec/60, (sec%60)+1, i*2+2))
	}
	logContent := sb.String()

	item := task.FileUploadItem{
		FileName: "iso.log",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}
	_, err = svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 在后台持续触发 RCA 计算的同时，并发执行 50 次日志分页与过滤查询
	svc.TriggerTaskRCA(taskInfo.TaskID, true)

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(iter int) {
			defer wg.Done()
			logs, total, qErr := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Page: 1, PageSize: 10})
			if qErr != nil {
				errCh <- fmt.Errorf("concurrent QueryLogs iteration %d failed: %w", iter, qErr)
				return
			}
			if total < 100 || len(logs) < 10 {
				errCh <- fmt.Errorf("unexpected query result: total=%d, logs=%d", total, len(logs))
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for qErr := range errCh {
		t.Errorf("concurrency lock issue detected: %v", qErr)
	}

	// 最终等待 RCA 完成
	svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)
}

// 验收指标 5: 崩溃残留状态启动自愈与自愈后重新触发验证
// 模拟进程在 QUEUED 与 RUNNING 阶段崩溃，断言重启自愈修正为 FAILED，
// 且用户触发重跑后能正常从 FAILED 恢复执行并达到 COMPLETED
func TestE2E_RCA_StartupCrashRecoveryAndHealing(t *testing.T) {
	svc, globalDB, taskDir, cleanup := setupE2ETaskService(t)
	defer cleanup()

	// 1. 创建三个任务，分别模拟崩溃前的三种状态
	taskQueued, err := svc.CreateEmptyTask("Crash-Queued-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	taskRunning, err := svc.CreateEmptyTask("Crash-Running-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	taskDone, err := svc.CreateEmptyTask("Crash-Completed-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	logContent := `
Apr 15 2026 10:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.
Apr 15 2026 10:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: Session down.
`
	item := task.FileUploadItem{
		FileName: "crash.log",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}
	for _, tid := range []string{taskQueued.TaskID, taskRunning.TaskID, taskDone.TaskID} {
		_, _ = svc.ImportLogs(tid, []task.FileUploadItem{item}, "overwrite")
		svc.WaitForTaskRCA(tid, 5*time.Second)
	}

	// 模拟异常终止：人为在数据库中置为 QUEUED 与 RUNNING
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskQueued.TaskID).Update("rca_status", model.RCAStatusQueued).Error
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskRunning.TaskID).Update("rca_status", model.RCAStatusRunning).Error
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskDone.TaskID).Update("rca_status", model.RCAStatusCompleted).Error

	// 2. 执行自愈
	if err := svc.RecoverDanglingRCATasks(); err != nil {
		t.Fatalf("RecoverDanglingRCATasks failed: %v", err)
	}

	// 3. 断言自愈结果
	infoQueued, _ := svc.GetTaskByID(taskQueued.TaskID)
	if infoQueued.RCAStatus != model.RCAStatusFailed {
		t.Errorf("expected taskQueued to be FAILED, got %s", infoQueued.RCAStatus)
	}
	if infoQueued.RCAErrorMessage != "服务重启中断，请手动点击重新分析" {
		t.Errorf("unexpected error message: %s", infoQueued.RCAErrorMessage)
	}

	infoRunning, _ := svc.GetTaskByID(taskRunning.TaskID)
	if infoRunning.RCAStatus != model.RCAStatusFailed {
		t.Errorf("expected taskRunning to be FAILED, got %s", infoRunning.RCAStatus)
	}

	infoDone, _ := svc.GetTaskByID(taskDone.TaskID)
	if infoDone.RCAStatus != model.RCAStatusCompleted {
		t.Errorf("expected taskDone to remain COMPLETED, got %s", infoDone.RCAStatus)
	}

	// 4. 对处于 FAILED 的自愈任务触发重算
	svc.TriggerTaskRCA(taskQueued.TaskID, true)
	ok := svc.WaitForTaskRCA(taskQueued.TaskID, 5*time.Second)
	if !ok {
		t.Fatalf("waiting for reanalyzed task timed out")
	}

	reanalyzedInfo, _ := svc.GetTaskByID(taskQueued.TaskID)
	if reanalyzedInfo.RCAStatus != model.RCAStatusCompleted {
		t.Fatalf("expected reanalyzed task to reach COMPLETED, got %s", reanalyzedInfo.RCAStatus)
	}
	if reanalyzedInfo.RcaCount != 1 {
		t.Fatalf("expected RcaCount 1, got %d", reanalyzedInfo.RcaCount)
	}
	_ = taskDir
}

// 验收指标 6: 边界防线验证 - rcaEngine 为 nil 时安全兜底完成
func TestE2E_RCA_NilEngineSafe(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rca_nil_engine_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	defer storage.CloseKnowledgeDB()

	taskDir := filepath.Join(tmpDir, "tasks")
	// 显式传入 nil 作为 rcaEngine
	svc := task.NewService(globalDB, taskDir, nil, nil)

	taskInfo, err := svc.CreateEmptyTask("Nil-Engine-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	logContent := "Apr 15 2026 10:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.\n"
	item := task.FileUploadItem{
		FileName: "test.log",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}
	_, err = svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	ok := svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)
	if !ok {
		t.Fatalf("wait for RCA timed out")
	}

	info, err := svc.GetTaskByID(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if info.RCAStatus != model.RCAStatusCompleted {
		t.Errorf("expected RCAStatus to be COMPLETED even with nil rcaEngine, got %s", info.RCAStatus)
	}
	if info.RcaCount != 0 {
		t.Errorf("expected RcaCount 0, got %d", info.RcaCount)
	}
}

// 验收指标 7: 并发删除任务安全性验证 - worker 执行中途任务被删不复活物理文件
func TestE2E_RCA_DeleteTaskDuringExecution(t *testing.T) {
	svc, _, taskDir, cleanup := setupE2ETaskService(t)
	defer cleanup()

	taskInfo, err := svc.CreateEmptyTask("Delete-Concurrent-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	logContent := `
Apr 15 2026 10:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface down.
Apr 15 2026 10:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: Session down.
`
	item := task.FileUploadItem{
		FileName: "test.log",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}
	_, err = svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 触发 RCA 后立即执行删除任务
	svc.TriggerTaskRCA(taskInfo.TaskID, true)
	if err := svc.DeleteTask(taskInfo.TaskID); err != nil {
		t.Fatalf("delete task failed: %v", err)
	}

	// 等待一段时间让可能残留的 worker 执行完毕
	time.Sleep(100 * time.Millisecond)

	// 断言任务库物理文件已被彻底清理且未被 worker 复活
	dbPath := filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskInfo.TaskID))
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("expected task db file to not exist after delete, but stat err is %v", err)
	}
}
