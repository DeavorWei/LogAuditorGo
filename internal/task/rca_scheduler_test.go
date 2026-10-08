package task_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func setupTestTaskService(t *testing.T) (*task.Service, *gorm.DB, string, func()) {
	t.Helper()
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "rca_scheduler_test_*")
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

	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	cleanup := func() {
		_ = indexer.Close()
		_ = storage.CloseKnowledgeDB()
		_ = os.RemoveAll(tmpDir)
	}

	return svc, globalDB, taskDir, cleanup
}

// TestRecoverDanglingRCATasks 验证服务启动自愈逻辑：将残留的 QUEUED/RUNNING 任务修正为 FAILED
func TestRecoverDanglingRCATasks(t *testing.T) {
	svc, globalDB, taskDir, cleanup := setupTestTaskService(t)
	defer cleanup()

	// 1. 创建两个空任务
	task1, err := svc.CreateEmptyTask("Dangling-Queued", "CloudEngine")
	if err != nil {
		t.Fatalf("create task 1 failed: %v", err)
	}
	task2, err := svc.CreateEmptyTask("Dangling-Running", "CloudEngine")
	if err != nil {
		t.Fatalf("create task 2 failed: %v", err)
	}

	// 2. 模拟进程异常退出前的状态：分别置为 QUEUED 和 RUNNING
	taskDB1, _, err := storage.GetOrCreateTaskDB(taskDir, task1.TaskID)
	if err != nil {
		t.Fatalf("open taskDB1 failed: %v", err)
	}
	_ = taskDB1.Model(&model.TaskInfo{}).Where("task_id = ?", task1.TaskID).Update("rca_status", model.RCAStatusQueued).Error
	storage.ReleaseTaskDB(task1.TaskID)

	taskDB2, _, err := storage.GetOrCreateTaskDB(taskDir, task2.TaskID)
	if err != nil {
		t.Fatalf("open taskDB2 failed: %v", err)
	}
	_ = taskDB2.Model(&model.TaskInfo{}).Where("task_id = ?", task2.TaskID).Update("rca_status", model.RCAStatusRunning).Error
	storage.ReleaseTaskDB(task2.TaskID)

	// 同时更新全局库
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", task1.TaskID).Update("rca_status", model.RCAStatusQueued).Error
	_ = globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", task2.TaskID).Update("rca_status", model.RCAStatusRunning).Error

	// 3. 执行启动自愈
	if err := svc.RecoverDanglingRCATasks(); err != nil {
		t.Fatalf("RecoverDanglingRCATasks failed: %v", err)
	}

	// 4. 验证自愈结果
	updated1, err := svc.GetTaskByID(task1.TaskID)
	if err != nil {
		t.Fatalf("get task 1 failed: %v", err)
	}
	if updated1.RCAStatus != model.RCAStatusFailed {
		t.Errorf("expected task 1 RCAStatus to be FAILED, got %s", updated1.RCAStatus)
	}
	if updated1.RCAErrorMessage != "服务重启中断，请手动点击重新分析" {
		t.Errorf("unexpected error message: %s", updated1.RCAErrorMessage)
	}

	updated2, err := svc.GetTaskByID(task2.TaskID)
	if err != nil {
		t.Fatalf("get task 2 failed: %v", err)
	}
	if updated2.RCAStatus != model.RCAStatusFailed {
		t.Errorf("expected task 2 RCAStatus to be FAILED, got %s", updated2.RCAStatus)
	}
}

// TestAsyncRCADispatchAndCompletion 验证导入日志后，主任务状态立即就绪，RCA 异步执行完成后更新状态
func TestAsyncRCADispatchAndCompletion(t *testing.T) {
	svc, globalDB, _, cleanup := setupTestTaskService(t)
	defer cleanup()

	// 注入一条知识
	k := model.Knowledge{
		ID:          1,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Message:     "Interface state turned to DOWN.",
		Description: "接口物理中断",
		Cause:       "光纤松动",
		Action:      "检查光纤",
		ContentHash: "hash_ifnet_down",
	}
	globalDB.Create(&k)

	logContent := `
Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)
`
	taskInfo, err := svc.CreateEmptyTask("Async-Test-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	item := task.FileUploadItem{
		FileName: "test.log",
		FileSize: int64(len(logContent)),
		Content:  logContent,
	}

	// 执行导入（主流程异步派发 RCA）
	importedInfo, err := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 断言：主任务状态立即置为 COMPLETED，日志数据立即就绪
	if importedInfo.Status != model.TaskStatusCompleted {
		t.Errorf("expected task Status to be COMPLETED, got %s", importedInfo.Status)
	}
	if importedInfo.LogCount != 2 {
		t.Errorf("expected LogCount=2, got %d", importedInfo.LogCount)
	}

	// 等待后台异步 RCA Worker 完成
	ok := svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)
	if !ok {
		t.Fatalf("wait for RCA timeout")
	}

	// 重新读取任务元数据
	finalInfo, err := svc.GetTaskByID(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if finalInfo.RCAStatus != model.RCAStatusCompleted {
		t.Errorf("expected RCAStatus to be COMPLETED, got %s", finalInfo.RCAStatus)
	}
	if finalInfo.RcaCount < 1 {
		t.Errorf("expected RcaCount >= 1, got %d", finalInfo.RcaCount)
	}

	// 验证 RCA 事件已落库
	events, err := svc.GetTaskRCAEvents(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get rca events failed: %v", err)
	}
	if len(events) < 1 {
		t.Errorf("expected >= 1 RCA event in db, got %d", len(events))
	}
}

// TestTriggerTaskRCA_ForceReanalyze 验证 forceReanalyze=true 时能够重新触发并替换原有分析
func TestTriggerTaskRCA_ForceReanalyze(t *testing.T) {
	svc, _, _, cleanup := setupTestTaskService(t)
	defer cleanup()

	taskInfo, err := svc.CreateEmptyTask("Force-Reanalyze-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	// 第一次触发
	svc.TriggerTaskRCA(taskInfo.TaskID, false)
	// 第二次强制重新触发
	svc.TriggerTaskRCA(taskInfo.TaskID, true)

	// 等待完成
	ok := svc.WaitForTaskRCA(taskInfo.TaskID, 5*time.Second)
	if !ok {
		t.Fatalf("wait for RCA timeout")
	}

	finalInfo, err := svc.GetTaskByID(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if finalInfo.RCAStatus != model.RCAStatusCompleted {
		t.Errorf("expected RCAStatus to be COMPLETED, got %s", finalInfo.RCAStatus)
	}
}
