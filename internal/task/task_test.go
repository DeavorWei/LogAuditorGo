package task_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	// 验证 H-02: 未匹配日志保留其真实 Severity (BFD 日志级别应为 2)
	for _, r := range records {
		if r.Brief == "BFD_SESS_DOWN" {
			if r.Severity != 2 {
				t.Errorf("H-02 regression: unhandled log severity was corrupted, expected 2, got %d", r.Severity)
			}
		}
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
	if !strings.HasSuffix(files[0].FileName, "sw01.log") || files[0].LineCount != 2 {
		t.Errorf("unexpected file1 info: %+v", files[0])
	}
	if !strings.HasSuffix(files[1].FileName, "fw02.log") || files[1].LineCount != 1 {
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

func TestTaskIDValidation(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_sec_test_*")
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

	invalidIDs := []string{
		"../../etc/passwd",
		"short",
		"task with spaces",
		"task*with*wildcards",
		"task/slash",
		"task\\backslash",
		"",
	}

	for _, badID := range invalidIDs {
		if _, err := svc.GetTaskFiles(badID); err == nil {
			t.Errorf("expected error for GetTaskFiles with invalid id '%s', got nil", badID)
		}
		if _, err := svc.GetTaskByID(badID); err == nil {
			t.Errorf("expected error for GetTaskByID with invalid id '%s', got nil", badID)
		}
		if _, err := svc.ImportLogs(badID, []task.FileUploadItem{{FileName: "a.txt", Content: "log"}}, "overwrite"); err == nil {
			t.Errorf("expected error for ImportLogs with invalid id '%s', got nil", badID)
		}
		if _, _, err := svc.QueryTaskLogs(badID, model.LogQueryFilter{}); err == nil {
			t.Errorf("expected error for QueryTaskLogs with invalid id '%s', got nil", badID)
		}
		if _, err := svc.GetTaskRCAEvents(badID); err == nil {
			t.Errorf("expected error for GetTaskRCAEvents with invalid id '%s', got nil", badID)
		}
		if _, err := svc.ExportTaskHTML(badID); err == nil {
			t.Errorf("expected error for ExportTaskHTML with invalid id '%s', got nil", badID)
		}
		if err := svc.DeleteTask(badID); err == nil {
			t.Errorf("expected error for DeleteTask with invalid id '%s', got nil", badID)
		}
	}
}

func TestQueryTaskLogsLikeEscaping(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_like_test_*")
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

	logContent := `
Apr 15 2026 14:00:01 CORE_SW_01 %%01IFNET/4/IF_DOWN(l)[1]: Interface down 100% loss. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD down with 50% packet drop. (SessionID=10)
Apr 15 2026 14:00:03 COREXSWX01 %%01BGP/2/PEER_BACKWARD(l)[3]: BGP peer down. (PeerAddress=192.168.1.2)
`
	taskInfo, err := svc.CreateAndRunTask("Test-Like-Escaping", "CloudEngine", logContent)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	// 1. 测试 Hostname 中含 "_" 的精确匹配，如果未转义，CORE_SW_01 中的 "_" 会匹配 COREXSWX01 和 CORE-SW-01
	records, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Hostname: "CORE_SW_01"})
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	if total != 1 || len(records) != 1 || records[0].Hostname != "CORE_SW_01" {
		t.Errorf("expected 1 record for CORE_SW_01, got total=%d, len=%d", total, len(records))
	}

	// 2. 测试 Keyword 中含 "%" 的模糊匹配，搜索 "100%" 只应命中第 1 条，不应将 "%" 当作通配符匹配其他
	records100, total100, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Keyword: "100%"})
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	if total100 != 1 || len(records100) != 1 {
		t.Errorf("expected 1 record for '100%%', got total=%d, len=%d", total100, len(records100))
	}

	// 3. 测试 Brief 中含通配符安全搜索
	recordsBrief, totalBrief, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Brief: "IF_DOWN"})
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	if totalBrief != 1 || len(recordsBrief) != 1 {
		t.Errorf("expected 1 record for IF_DOWN, got total=%d", totalBrief)
	}
}

func TestTaskDeletionAndCleanup(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_del_test_*")
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

	taskInfo, err := svc.CreateEmptyTask("TaskToDelete", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	taskDBPath := filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskInfo.TaskID))
	if _, err := os.Stat(taskDBPath); os.IsNotExist(err) {
		t.Fatalf("expected task db file at %s", taskDBPath)
	}

	// 删除任务
	if err := svc.DeleteTask(taskInfo.TaskID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	// 验证全局库中记录被删除
	var checkInfo model.TaskInfo
	if err := globalDB.First(&checkInfo, "task_id = ?", taskInfo.TaskID).Error; err == nil {
		t.Errorf("expected task record deleted from global db, but found")
	}

	// 验证物理文件被清理
	if _, err := os.Stat(taskDBPath); !os.IsNotExist(err) {
		t.Errorf("expected task db file deleted, but still exists")
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

func TestFileUploadItemStreamingAndCleanup(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_stream_test_*")
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

	emptyTask, err := svc.CreateEmptyTask("StreamingTask", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	// 1. 测试从 io.Reader 流式导入
	readerItem := task.FileUploadItem{
		FileName: "reader_test.log",
		FileSize: 100,
		Reader:   strings.NewReader("Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN.\n"),
	}

	// 2. 测试从临时文件流式导入并在完成后自动清理
	tempLogPath := filepath.Join(tmpDir, "temp_log.txt")
	if err := os.WriteFile(tempLogPath, []byte("Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session down.\n"), 0644); err != nil {
		t.Fatalf("write temp log file failed: %v", err)
	}

	fileItem := task.FileUploadItem{
		FileName: "temp_file.log",
		FileSize: 100,
		FilePath: tempLogPath,
		TempFile: true,
	}

	taskInfo, err := svc.ImportLogs(emptyTask.TaskID, []task.FileUploadItem{readerItem, fileItem}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs failed: %v", err)
	}

	if taskInfo.LogCount != 2 {
		t.Errorf("expected 2 logs, got %d", taskInfo.LogCount)
	}

	// 验证临时文件已被 Cleanup() 删除
	if _, err := os.Stat(tempLogPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file %s to be deleted after ImportLogs, but it still exists", tempLogPath)
	}
}

func TestGenerateHTMLReportDetailed(t *testing.T) {
	// 1. Test nil task
	nilHTML := task.GenerateHTMLReport(nil, nil, nil)
	if !strings.Contains(nilHTML, "Task not found") {
		t.Errorf("expected 'Task not found' for nil task, got: %s", nilHTML)
	}

	// 2. Test valid task with records and RCAs
	taskInfo := &model.TaskInfo{
		TaskID:       "task12345678",
		TaskName:     "Test HTML Report & <Escaping>",
		DeviceType:   "CloudEngine 16800",
		LogCount:     150,
		MatchedCount: 75,
	}

	records := make([]model.LogRecord, 120)
	for i := 0; i < 120; i++ {
		sev := (i % 8) + 1
		var kid uint
		var matchTier string
		if i%2 == 0 {
			kid = uint(i + 1)
			matchTier = "EXACT"
		}
		records[i] = model.LogRecord{
			ID:          uint(i + 1),
			Timestamp:   time.Date(2026, 8, 26, 12, 0, i%60, 0, time.UTC),
			Hostname:    "SW-CORE-01",
			Severity:    sev,
			Module:      "BGP",
			Brief:       "PEER_BACKWARD",
			RawLog:      "BGP peer 1.1.1.1 went down <critical>",
			KnowledgeID: kid,
			MatchTier:   matchTier,
		}
	}

	rcas := []model.RCAEvent{
		{
			ID:                1,
			RootLogID:         10,
			RootTimestamp:     "2026-08-26 12:00:00",
			RootModule:        "IFNET",
			RootBrief:         "IF_DOWN",
			Confidence:        0.95,
			RootCauseSummary:  "Interface 100GE1/0/1 physical link down.",
			RecommendedAction: "Check physical fiber optics and transceivers.",
		},
	}

	htmlReport := task.GenerateHTMLReport(taskInfo, records, rcas)

	// Verify escaping of task name
	if strings.Contains(htmlReport, "<Escaping>") {
		t.Errorf("expected HTML escaping for task name, got raw '<Escaping>'")
	}
	if !strings.Contains(htmlReport, "&lt;Escaping&gt;") && !strings.Contains(htmlReport, "Test HTML Report") {
		t.Errorf("expected escaped task name in report")
	}

	// Verify coverage percent: 75 / 150 = 50.0%
	if !strings.Contains(htmlReport, "50.0%") {
		t.Errorf("expected '50.0%%' in report, got: %s", htmlReport)
	}

	// Verify RCA section is present
	if !strings.Contains(htmlReport, "根因分析（RCA）排查建议") {
		t.Errorf("expected RCA section in report")
	}
	if !strings.Contains(htmlReport, "95%") {
		t.Errorf("expected '95%%' confidence in report")
	}

	// Verify table records limit (max 100)
	if !strings.Contains(htmlReport, "SW-CORE-01") {
		t.Errorf("expected records table in report")
	}

	// 3. Test empty records and empty RCAs
	emptyTask := &model.TaskInfo{
		TaskID:       "empty12345678",
		TaskName:     "Empty Task",
		DeviceType:   "USG6000",
		LogCount:     0,
		MatchedCount: 0,
	}
	emptyHTML := task.GenerateHTMLReport(emptyTask, nil, nil)
	if strings.Contains(emptyHTML, "根因分析（RCA）排查建议") {
		t.Errorf("expected no RCA section when rcas is empty")
	}
	if !strings.Contains(emptyHTML, "0.0%") {
		t.Errorf("expected '0.0%%' coverage for 0 log count")
	}
}

func TestDeviceManagementAndMultiDeviceAnalysis(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "device_test_*")
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

	// 注入 OSPF 知识
	kOSPF := model.Knowledge{
		ID:          10,
		Module:      "OSPF",
		Brief:       "OSPF_NBR_CHG",
		Message:     "Neighbor status changed.",
		Description: "OSPF 邻居状态变化",
		Cause:       "链路震荡或 Timer 超时",
		Action:      "排查链路",
		ContentHash: "hash_ospf_nbr_chg",
	}
	globalDB.Create(&kOSPF)

	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")

	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	// 1. 创建空任务
	taskInfo, err := svc.CreateEmptyTask("Multi-Router-OSPF-Audit", "NetEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	// 2. 创建 3 台路由器设备
	dev1, err := svc.CreateDevice(taskInfo.TaskID, &model.Device{
		DeviceName:   "Router-Core-01",
		DeviceType:   "NetEngine",
		Hostname:     "Router-Core-01",
		ManagementIP: "10.0.0.1",
		Color:        "#3B82F6",
	})
	if err != nil {
		t.Fatalf("CreateDevice dev1 failed: %v", err)
	}

	dev2, err := svc.CreateDevice(taskInfo.TaskID, &model.Device{
		DeviceName:   "Router-Edge-02",
		DeviceType:   "NetEngine",
		Hostname:     "Router-Edge-02",
		ManagementIP: "10.0.0.2",
		Color:        "#10B981",
	})
	if err != nil {
		t.Fatalf("CreateDevice dev2 failed: %v", err)
	}

	dev3, err := svc.CreateDevice(taskInfo.TaskID, &model.Device{
		DeviceName:   "Router-Branch-03",
		DeviceType:   "NetEngine",
		Hostname:     "Router-Branch-03",
		ManagementIP: "10.0.0.3",
		Color:        "#F59E0B",
	})
	if err != nil {
		t.Fatalf("CreateDevice dev3 failed: %v", err)
	}

	// 检查设备列表
	devs, err := svc.ListDevices(taskInfo.TaskID)
	if err != nil || len(devs) != 3 {
		t.Fatalf("ListDevices failed, expected 3, got %d (err: %v)", len(devs), err)
	}

	// 3. 分别向各设备导入时序日志 (模拟 OSPF 邻居状态变化)
	logRouter1 := `
Apr 15 2026 10:00:00 Router-Core-01 %%01OSPF/4/OSPF_NBR_CHG(l)[1]: OSPF 1 Neighbor 10.0.0.2 changed state from FULL to DOWN. (NbrIP=10.0.0.2)
Apr 15 2026 10:00:02 Router-Core-01 %%01OSPF/4/OSPF_NBR_CHG(l)[2]: OSPF 1 Neighbor 10.0.0.3 changed state from FULL to DOWN. (NbrIP=10.0.0.3)
`
	logRouter2 := `
Apr 15 2026 10:00:05 Router-Edge-02 %%01OSPF/4/OSPF_NBR_CHG(l)[1]: OSPF 1 Neighbor 10.0.0.1 changed state from FULL to INIT. (NbrIP=10.0.0.1)
Apr 15 2026 10:00:15 Router-Edge-02 %%01OSPF/4/OSPF_NBR_CHG(l)[2]: OSPF 1 Neighbor 10.0.0.1 changed state from INIT to DOWN. (NbrIP=10.0.0.1)
`
	logRouter3 := `
Apr 15 2026 10:00:10 Router-Branch-03 %%01OSPF/4/OSPF_NBR_CHG(l)[1]: OSPF 1 Neighbor 10.0.0.1 changed state from FULL to DOWN. (NbrIP=10.0.0.1)
`

	_, err = svc.ImportLogsToDevice(taskInfo.TaskID, dev1.ID, []task.FileUploadItem{
		{FileName: "router1.log", Content: logRouter1, FileSize: int64(len(logRouter1))},
	}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogsToDevice dev1 failed: %v", err)
	}

	_, err = svc.ImportLogsToDevice(taskInfo.TaskID, dev2.ID, []task.FileUploadItem{
		{FileName: "router2.log", Content: logRouter2, FileSize: int64(len(logRouter2))},
	}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogsToDevice dev2 failed: %v", err)
	}

	_, err = svc.ImportLogsToDevice(taskInfo.TaskID, dev3.ID, []task.FileUploadItem{
		{FileName: "router3.log", Content: logRouter3, FileSize: int64(len(logRouter3))},
	}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogsToDevice dev3 failed: %v", err)
	}

	// 4. 测试单设备查询过滤
	dev1Logs, total1, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{
		DeviceID: &dev1.ID,
	})
	if err != nil || total1 != 2 || len(dev1Logs) != 2 {
		t.Fatalf("QueryTaskLogs for dev1 failed, expected 2 logs, got %d (err: %v)", total1, err)
	}

	// 5. 测试多设备联合时间线查询 (按时间升序)
	timelineEvents, totalAll, err := svc.QueryMultiDeviceLogs(taskInfo.TaskID, model.MultiDeviceLogFilter{
		DeviceIDs: []uint{dev1.ID, dev2.ID, dev3.ID},
		Modules:   []string{"OSPF"},
		AscOrder:  true,
	})
	if err != nil || totalAll != 5 || len(timelineEvents) != 5 {
		t.Fatalf("QueryMultiDeviceLogs failed, expected 5 events, got %d (err: %v)", totalAll, err)
	}

	// 验证时间线升序顺序: 10:00:00(R1), 10:00:02(R1), 10:00:05(R2), 10:00:10(R3), 10:00:15(R2)
	if timelineEvents[0].DeviceName != "Router-Core-01" || timelineEvents[2].DeviceName != "Router-Edge-02" || timelineEvents[3].DeviceName != "Router-Branch-03" {
		t.Errorf("Timeline order mismatch, got: %+v", timelineEvents)
	}

	// 6. 测试多设备对比分析报告生成与自动结论
	report, err := svc.GetMultiDeviceReport(taskInfo.TaskID, []uint{dev1.ID, dev2.ID, dev3.ID})
	if err != nil {
		t.Fatalf("GetMultiDeviceReport failed: %v", err)
	}
	if len(report.Devices) != 3 {
		t.Errorf("expected 3 devices in report, got %d", len(report.Devices))
	}
	if len(report.CommonEvents) == 0 {
		t.Errorf("expected common events for OSPF/OSPF_NBR_CHG across devices")
	}
	if !strings.Contains(report.Conclusion, "多设备协同审计综述") || !strings.Contains(report.Conclusion, "OSPF 邻居震荡排查") {
		t.Errorf("expected diagnostic conclusion with OSPF suggestions, got: %s", report.Conclusion)
	}

	// 7. 测试多设备 HTML 报告导出
	htmlReport, err := svc.ExportMultiDeviceHTML(taskInfo.TaskID, []uint{dev1.ID, dev2.ID, dev3.ID})
	if err != nil {
		t.Fatalf("ExportMultiDeviceHTML failed: %v", err)
	}
	if !strings.Contains(htmlReport, "多设备协同分析与时间线诊断报告") || !strings.Contains(htmlReport, "Router-Core-01") {
		t.Errorf("expected valid multi-device HTML report, got: %s", htmlReport)
	}

	// 8. 测试 AutoAssignDevices 自动按 Hostname 识别
	taskInfoAuto, _ := svc.CreateEmptyTask("Auto-Assign-Test", "NetEngine")
	mixedLogs := `
Apr 15 2026 10:00:00 PE-Router-A %%01OSPF/4/OSPF_NBR_CHG(l)[1]: OSPF neighbor down.
Apr 15 2026 10:00:01 PE-Router-B %%01OSPF/4/OSPF_NBR_CHG(l)[1]: OSPF neighbor down.
`
	_, _ = svc.ImportLogs(taskInfoAuto.TaskID, []task.FileUploadItem{
		{FileName: "mixed.log", Content: mixedLogs},
	}, "overwrite")

	autoDevs, err := svc.AutoAssignDevices(taskInfoAuto.TaskID)
	if err != nil || len(autoDevs) != 2 {
		t.Fatalf("AutoAssignDevices failed, expected 2 devices, got %d (err: %v)", len(autoDevs), err)
	}
}

func TestReanalyzeTask(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "reanalyze_test_*")
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

	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	// 1. 创建任务并导入日志（此时知识库没有任何条目，匹配数为0）
	taskInfo, err := svc.CreateEmptyTask("Reanalyze-Task-Test", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	logContent := `
Apr 15 2026 14:00:01 CORE-SW-01 %%01CUSTOMMOD/4/CUSTOM_ALERT(l)[1]: Custom alert triggered.
Apr 15 2026 14:00:02 CORE-SW-01 %%01CUSTOMMOD/2/CUSTOM_ERR(l)[2]: Custom error occurred.
`
	updatedTask, err := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{
		{FileName: "test.log", Content: logContent},
	}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs failed: %v", err)
	}

	if updatedTask.MatchedCount != 0 {
		t.Fatalf("expected 0 matched initially, got %d", updatedTask.MatchedCount)
	}

	// 2. 模拟知识库后来导入了新知识
	k := model.Knowledge{
		ID:          999,
		Module:      "CUSTOMMOD",
		Brief:       "CUSTOM_ALERT",
		Message:     "Custom alert triggered.",
		Description: "自定义告警",
		ContentHash: "hash_reanalyze_custom_alert",
	}
	globalDB.Create(&k)
	matchEngine.Reload()

	// 3. 执行基于任务维度的重新分析
	reanalyzedTask, err := svc.ReanalyzeTask(taskInfo.TaskID, nil)
	if err != nil {
		t.Fatalf("ReanalyzeTask failed: %v", err)
	}

	if reanalyzedTask.MatchedCount != 1 {
		t.Fatalf("expected 1 matched after reanalyze, got %d", reanalyzedTask.MatchedCount)
	}
	if reanalyzedTask.Status != model.TaskStatusCompleted {
		t.Fatalf("expected status COMPLETED, got %s", reanalyzedTask.Status)
	}

	// 4. 验证数据库中 LogRecord 确实被更新
	logs, _, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{Module: "CUSTOMMOD", Brief: "CUSTOM_ALERT"})
	if err != nil || len(logs) == 0 {
		t.Fatalf("QueryTaskLogs failed: %v", err)
	}
	if logs[0].KnowledgeID != 999 {
		t.Errorf("expected LogRecord KnowledgeID to be 999, got %d", logs[0].KnowledgeID)
	}

	// 5. 验证之前解析失败（UNPARSED/零时间戳）的日志在重新分析时能被自动补齐重解析
	taskDB, _, _ := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	unparsedLog := model.LogRecord{
		RawLog:      "May 19 2026 09:33:32+08:00 SZ_PS_DMZLeaf_2Z29-34U-CE8865-01 %%01M-LAG/4/hwMlagPortDown_active(l):CID=0x81de0458-alarmID=0x0ae52007;M-LAG member interfaces Down.",
		Brief:       "UNPARSED",
		Module:      "UNKNOWN",
		Severity:    8,
		MessageBody: "May 19 2026 09:33:32+08:00 SZ_PS_DMZLeaf_2Z29-34U-CE8865-01 %%01M-LAG/4/hwMlagPortDown_active(l):CID=0x81de0458-alarmID=0x0ae52007;M-LAG member interfaces Down.",
	}
	taskDB.Create(&unparsedLog)

	_, err = svc.ReanalyzeTask(taskInfo.TaskID, nil)
	if err != nil {
		t.Fatalf("ReanalyzeTask with unparsed log failed: %v", err)
	}

	var fixedLog model.LogRecord
	if err := taskDB.First(&fixedLog, "id = ?", unparsedLog.ID).Error; err != nil {
		t.Fatalf("query fixed log failed: %v", err)
	}
	if fixedLog.Brief != "hwMlagPortDown_active" {
		t.Errorf("expected fixed brief 'hwMlagPortDown_active', got '%s'", fixedLog.Brief)
	}
	if fixedLog.Module != "M-LAG" {
		t.Errorf("expected fixed module 'M-LAG', got '%s'", fixedLog.Module)
	}
	if fixedLog.Timestamp.IsZero() || fixedLog.Timestamp.Year() != 2026 || fixedLog.Timestamp.Month() != time.May || fixedLog.Timestamp.Day() != 19 {
		t.Errorf("expected timestamp parsed on reanalyze, got: %v", fixedLog.Timestamp)
	}
}

// TestTaskImportConcurrencyLock 验证 H-06: 同任务并发导入应被互斥锁拦截，避免重复入库与竞态
func TestTaskImportConcurrencyLock(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "task_lock_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, nil, nil)

	taskInfo, err := svc.CreateEmptyTask("Lock-Test-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	// 模拟外部已持锁
	lock := svc.GetTaskLockForTest(taskInfo.TaskID)
	lock.Lock()
	defer lock.Unlock()

	// 并发尝试导入，预期被立即拒绝
	item := task.FileUploadItem{
		FileName: "test.log",
		Content:  "Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN.",
	}
	_, importErr := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if importErr == nil || !strings.Contains(importErr.Error(), "already processing another import job") {
		t.Fatalf("expected concurrency lock error, got: %v", importErr)
	}
}

func TestDuplicateFileNameImportAndOverwrite(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_dup_name_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, nil, nil)

	taskInfo, err := svc.CreateEmptyTask("Dup-Name-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create empty task failed: %v", err)
	}

	// 1. 同批次同时上传两份同名为 log.log 的日志 (分别属于 SW-01 和 SW-02)
	item1 := task.FileUploadItem{
		FileName: "log.log",
		Content:  "Apr 15 2026 14:00:01 SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 down.",
	}
	item2 := task.FileUploadItem{
		FileName: "log.log",
		Content:  "Apr 15 2026 14:00:02 SW-02 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/2 down.",
	}

	tUpdated, err := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item1, item2}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs with duplicate file names failed: %v", err)
	}

	// 验证两个文件均被保存，日志总数为 2
	if tUpdated.FileCount != 2 {
		t.Errorf("expected 2 files imported, got %d", tUpdated.FileCount)
	}
	if tUpdated.LogCount != 2 {
		t.Errorf("expected 2 logs imported, got %d", tUpdated.LogCount)
	}

	// 验证自动创建了两个设备 SW-01 和 SW-02
	devs, err := svc.ListDevices(taskInfo.TaskID)
	if err != nil || len(devs) != 2 {
		t.Fatalf("expected 2 auto devices recognized, got %d (err: %v)", len(devs), err)
	}

	// 2. 补充导入第三份同名日志 log.log (来自新设备 SW-03)
	item3 := task.FileUploadItem{
		FileName: "log.log",
		Content:  "Apr 15 2026 14:00:03 SW-03 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/3 down.",
	}
	tUpdated2, err := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item3}, "overwrite")
	if err != nil {
		t.Fatalf("supplementary ImportLogs failed: %v", err)
	}

	if tUpdated2.FileCount != 3 {
		t.Errorf("expected 3 files after supplementary import, got %d", tUpdated2.FileCount)
	}
	if tUpdated2.LogCount != 3 {
		t.Errorf("expected 3 logs after supplementary import, got %d", tUpdated2.LogCount)
	}

	// 验证设备管理中自动识别出了 SW-03
	devs2, err := svc.ListDevices(taskInfo.TaskID)
	if err != nil || len(devs2) != 3 {
		t.Fatalf("expected 3 auto devices recognized including SW-03, got %d", len(devs2))
	}
}




