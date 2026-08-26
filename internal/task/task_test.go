package task_test

import (
	"os"
	"path/filepath"
	"testing"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func TestTaskServiceAndExport(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "test.bleve")
	indexer, _ := search.InitIndexer(indexPath)
	defer indexer.Close()

	// 注入一条知识
	k := model.Knowledge{
		ID:          1,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Message:     "Interface state turned to DOWN.",
		Description: "接口物理中断",
		Cause:       "光纤松动",
		Action:      "检查光纤",
		ContentHash: "hash_if_down",
	}
	globalDB.Create(&k)

	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	logContent := `
Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)
Apr 15 2026 14:00:03 CORE-SW-01 %%01BGP/2/PEER_BACKWARD(l)[3]: The BGP peer went down. (PeerAddress=192.168.1.2)
`

	taskInfo, err := svc.CreateAndRunTask("Test-Cascade-Task", "CloudEngine", logContent)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	if taskInfo.LogCount != 3 {
		t.Errorf("expected 3 logs, got %d", taskInfo.LogCount)
	}
	if taskInfo.MatchedCount < 1 {
		t.Errorf("expected >= 1 matched log, got %d", taskInfo.MatchedCount)
	}
	if taskInfo.RcaCount < 1 {
		t.Errorf("expected >= 1 RCA event, got %d", taskInfo.RcaCount)
	}

	// 测试查询
	records, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	if total != 3 || len(records) != 3 {
		t.Errorf("expected 3 total records, got total=%d, len=%d", total, len(records))
	}

	// 测试 RCA 事件查询
	rcas, err := svc.GetTaskRCAEvents(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("get rca failed: %v", err)
	}
	if len(rcas) == 0 {
		t.Errorf("expected RCA events, got 0")
	}

	// 测试 HTML 报告生成
	html, err := svc.ExportTaskHTML(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("export html failed: %v", err)
	}
	if len(html) < 100 {
		t.Errorf("exported HTML too short: %s", html)
	}
}

func TestTaskFileTrackingAndConflictModes(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_files_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	matchEngine := matcher.NewMatchEngine(globalDB, nil)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	// 1. 测试创建初始 PENDING 空任务
	emptyTask, err := svc.CreateEmptyTask("Pending-Task-01", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}
	if emptyTask.Status != model.TaskStatusPending {
		t.Errorf("expected PENDING status, got %s", emptyTask.Status)
	}
	if emptyTask.LogCount != 0 || emptyTask.FileCount != 0 {
		t.Errorf("expected 0 logs and 0 files for empty task, got logs=%d, files=%d", emptyTask.LogCount, emptyTask.FileCount)
	}

	// 2. 导入 2 个多日志文件
	file1 := task.FileUploadItem{
		FileName: "sw01.log",
		FileSize: 200,
		Content: `Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)`,
	}
	file2 := task.FileUploadItem{
		FileName: "fw02.log",
		FileSize: 100,
		Content:  `Apr 15 2026 14:05:00 USG-FW-01 %%01AAA/4/USER_AUTH_FAIL(l)[202]: User authentication failed. (UserName=testuser, UserIP=192.168.10.5)`,
	}

	updatedTask, err := svc.ImportLogs(emptyTask.TaskID, []task.FileUploadItem{file1, file2}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs failed: %v", err)
	}
	if updatedTask.Status != model.TaskStatusCompleted {
		t.Errorf("expected COMPLETED status, got %s", updatedTask.Status)
	}
	if updatedTask.LogCount != 3 {
		t.Errorf("expected 3 total logs, got %d", updatedTask.LogCount)
	}
	if updatedTask.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", updatedTask.FileCount)
	}

	// 3. 验证已上传文件列表
	files, err := svc.GetTaskFiles(emptyTask.TaskID)
	if err != nil {
		t.Fatalf("GetTaskFiles failed: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 task files, got %d", len(files))
	}
	if files[0].FileName != "sw01.log" || files[0].LineCount != 2 {
		t.Errorf("unexpected file1 info: %+v", files[0])
	}
	if files[1].FileName != "fw02.log" || files[1].LineCount != 1 {
		t.Errorf("unexpected file2 info: %+v", files[1])
	}

	// 4. 验证按 SourceFile 过滤查询
	swLogs, totalSw, err := svc.QueryTaskLogs(emptyTask.TaskID, model.LogQueryFilter{SourceFile: "sw01.log"})
	if err != nil || totalSw != 2 || len(swLogs) != 2 {
		t.Errorf("expected 2 logs for sw01.log, got total=%d, len=%d, err=%v", totalSw, len(swLogs), err)
	}

	// 5. 测试补充导入（包含同名文件，测试 conflict_mode = "skip"）
	conflictFileSkip := task.FileUploadItem{
		FileName: "sw01.log", // 同名文件
		FileSize: 50,
		Content:  `Apr 15 2026 14:00:10 CORE-SW-01 %%01BGP/2/PEER_BACKWARD(l)[3]: BGP peer down.`,
	}
	newFile3 := task.FileUploadItem{
		FileName: "sw03.log", // 新文件
		FileSize: 50,
		Content:  `Apr 15 2026 14:00:20 CORE-SW-03 %%01IFNET/4/IF_DOWN(l)[4]: IF down.`,
	}

	taskAfterSkip, err := svc.ImportLogs(emptyTask.TaskID, []task.FileUploadItem{conflictFileSkip, newFile3}, "skip")
	if err != nil {
		t.Fatalf("ImportLogs with skip failed: %v", err)
	}
	// 原 3 条 + 新增 1 条 (sw03.log) = 4 条，sw01.log 被跳过未被替换
	if taskAfterSkip.LogCount != 4 {
		t.Errorf("expected 4 logs after skip, got %d", taskAfterSkip.LogCount)
	}
	if taskAfterSkip.FileCount != 3 {
		t.Errorf("expected 3 files after skip, got %d", taskAfterSkip.FileCount)
	}

	// 6. 测试补充导入（包含同名文件，测试 conflict_mode = "overwrite"）
	conflictFileOverwrite := task.FileUploadItem{
		FileName: "sw01.log", // 覆盖 sw01.log (原 2 条替换为 1 条)
		FileSize: 60,
		Content:  `Apr 15 2026 14:00:50 CORE-SW-01 %%01BGP/2/PEER_BACKWARD(l)[5]: Overwritten single line.`,
	}
	taskAfterOverwrite, err := svc.ImportLogs(emptyTask.TaskID, []task.FileUploadItem{conflictFileOverwrite}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs with overwrite failed: %v", err)
	}
	// sw01.log 由 2 条变为 1 条，总数由 4 条变为 3 条
	if taskAfterOverwrite.LogCount != 3 {
		t.Errorf("expected 3 logs after overwrite sw01.log, got %d", taskAfterOverwrite.LogCount)
	}
	if taskAfterOverwrite.FileCount != 3 {
		t.Errorf("expected 3 files after overwrite, got %d", taskAfterOverwrite.FileCount)
	}
}

func BenchmarkTaskImportPipeline(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "task_bench_*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, _ := storage.InitKnowledgeDB(dbPath)
	k := model.Knowledge{
		ID:          1,
		Module:      "IFNET",
		Brief:       "IF_DOWN",
		Message:     "Interface state turned to DOWN.",
		ContentHash: "hash_if_down_bench",
	}
	globalDB.Create(&k)

	matchEngine := matcher.NewMatchEngine(globalDB, nil)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	lines := make([]string, 5000)
	for i := 0; i < 5000; i++ {
		if i%2 == 0 {
			lines[i] = "Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)"
		} else {
			lines[i] = "Apr 15 2026 14:00:02 CORE-SW-01 %%01DEBUG/6/ROUTINE_INFO(l)[2]: Routine keepalive ok. (PeerID=10.0.0.1)"
		}
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		emptyTask, _ := svc.CreateEmptyTask("", "CloudEngine")
		item := task.FileUploadItem{
			FileName: "bench.log",
			FileSize: int64(len(content)),
			Content:  content,
		}
		_, _ = svc.ImportLogs(emptyTask.TaskID, []task.FileUploadItem{item}, "overwrite")
	}
}

