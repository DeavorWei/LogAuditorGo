package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

func TestScanLineSourceWithGarbageAndNUL(t *testing.T) {
	// 构造模拟日志流：
	// 行 1: 正常日志行
	// 行 2: 连续 333 个 \x00 (模拟文件空洞/坏块)
	// 行 3: 纯空白行
	// 行 4: 带有控制字符的混合脏行（无有效文本）
	// 行 5: 夹杂 \x00 的有效日志行
	// 行 6: 中文夹杂坏字节行（绝不能被丢弃）
	rawContent := strings.Join([]string{
		"2026-10-13 12:00:00 Host-A VRP %%01IFNET/4/UP: Interface up",
		strings.Repeat("\x00", 333),
		"   \t   ",
		"\x01\x02\x03\x00\x7F",
		"2026-10-13 12:00:01 Host-B\x00 VRP %%01IFNET/4/DOWN: Interface down\x00",
		"2026-10-13 12:00:02 Host-C VRP %%01IFNET/4/WARN: 接口\xff\xfe状态异常",
	}, "\n")

	r := strings.NewReader(rawContent)
	total, sample, err := scanLineSource(r, 10)
	if err != nil {
		t.Fatalf("scanLineSource failed: %v", err)
	}

	// 预期：行 1、行 5、行 6 是有效日志行，行 2/3/4 应被过滤丢弃
	if total != 3 {
		t.Fatalf("expected 3 valid lines, got %d", total)
	}

	if len(sample) != 3 {
		t.Fatalf("expected 3 sample lines, got %d", len(sample))
	}

	// 验证第 2 条有效日志已去除了 \x00 (转为空格且不粘词)
	if strings.Contains(sample[1], "\x00") {
		t.Errorf("sample[1] should not contain \\x00, got %q", sample[1])
	}

	// 验证第 3 条中文日志保留了可读汉字
	if !strings.Contains(sample[2], "接口") || !strings.Contains(sample[2], "状态异常") {
		t.Errorf("sample[2] chinese characters swallowed, got %q", sample[2])
	}
}

// 守护用例 ①：磁盘文件含 NUL 行 → 验证流式重读过滤、行数与 task_files 记账一致性
func TestDiskFileImportAndReanalyzeWithGarbageHole(t *testing.T) {
	logger.Init("debug", "console")
	tmpDir, err := os.MkdirTemp("", "task_sanitize_test_*")
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
	svc := NewService(globalDB, taskDir, nil, nil)

	// 创建包含坏块的物理文件：正常行 + 333 个 \x00 + 正常行
	logFilePath := filepath.Join(tmpDir, "bad_hole.log")
	fileContent := strings.Join([]string{
		"Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface state turned to DOWN.",
		strings.Repeat("\x00", 333),
		"Apr 15 2026 14:00:02 CORE-SW-01 %%01IFNET/4/IF_UP(l)[2]: Interface state turned to UP.",
	}, "\n")
	if err := os.WriteFile(logFilePath, []byte(fileContent), 0644); err != nil {
		t.Fatalf("write log file failed: %v", err)
	}

	// 1. 创建任务并执行物理文件导入
	taskInfo, err := svc.CreateEmptyTask("TestGarbageHoleTask", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	items := []FileUploadItem{
		{
			FileName: "bad_hole.log",
			FilePath: logFilePath,
		},
	}
	updatedTask, err := svc.ImportLogsWithDevice(taskInfo.TaskID, 0, items, "skip")
	if err != nil {
		t.Fatalf("import logs failed: %v", err)
	}

	// 验证计数与落库行数严格一致（P0-1 闭环验证：只有 2 行，绝对无空记录）
	if updatedTask.LogCount != 2 {
		t.Errorf("taskInfo.LogCount got %d, want 2", updatedTask.LogCount)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	if err != nil {
		t.Fatalf("open task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	var dbCount int64
	taskDB.Model(&model.LogRecord{}).Count(&dbCount)
	if dbCount != 2 {
		t.Errorf("db record count got %d, want 2", dbCount)
	}

	var emptyRawCount int64
	taskDB.Model(&model.LogRecord{}).Where("raw_log = '' OR raw_log IS NULL").Count(&emptyRawCount)
	if emptyRawCount != 0 {
		t.Errorf("found %d empty raw_log records in db, expected 0", emptyRawCount)
	}

	// 验证 task_files.line_count 严格等于 2
	var tf model.TaskFile
	if err := taskDB.First(&tf, "task_id = ?", taskInfo.TaskID).Error; err != nil {
		t.Fatalf("query task file failed: %v", err)
	}
	if tf.LineCount != 2 {
		t.Errorf("task_files.line_count got %d, want 2", tf.LineCount)
	}

	// 2. 执行全流程重新分析
	reanalyzedTask, err := svc.ReanalyzeTask(taskInfo.TaskID, nil)
	if err != nil {
		t.Fatalf("reanalyze task failed: %v", err)
	}

	if reanalyzedTask.LogCount != 2 {
		t.Errorf("after reanalyze task.LogCount got %d, want 2", reanalyzedTask.LogCount)
	}

	var postReanalyzeCount int64
	taskDB.Model(&model.LogRecord{}).Count(&postReanalyzeCount)
	if postReanalyzeCount != 2 {
		t.Errorf("after reanalyze db record count got %d, want 2", postReanalyzeCount)
	}
}

// 守护用例 ②：注入存量脏行 → 验证重分析时 raw_log 清洗回写、纯坏块行物理删除、以及 task_files.line_count 刷新
func TestReanalyzeCleansesInjectedGarbageRecordsAndRefreshesLineCount(t *testing.T) {
	logger.Init("debug", "console")
	tmpDir, err := os.MkdirTemp("", "task_reanalyze_sanitize_*")
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
	svc := NewService(globalDB, taskDir, nil, nil)

	taskInfo, err := svc.CreateEmptyTask("TestInjectedGarbageTask", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(taskDir, taskInfo.TaskID)
	if err != nil {
		t.Fatalf("open task db failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskInfo.TaskID)

	sourceFile := "injected.log"

	// 模拟老版本系统注入 3 条存量记录：
	// 记录 1：正常日志
	// 记录 2：连续 333 个 \x00 的纯坏块行（现场故障同款）
	// 记录 3：夹杂 \x00 的脏日志
	injectedRecords := []model.LogRecord{
		{
			ID:          1,
			SourceFile:  sourceFile,
			Timestamp:   time.Now(),
			Hostname:    "CORE-SW-01",
			Module:      "IFNET",
			Severity:    4,
			RawLog:      "Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface state turned to DOWN.",
			MessageBody: "Interface state turned to DOWN.",
		},
		{
			ID:          2,
			SourceFile:  sourceFile,
			Timestamp:   time.Now(),
			Hostname:    "UNKNOWN",
			Module:      "UNKNOWN",
			Severity:    8,
			Brief:       "UNPARSED",
			RawLog:      strings.Repeat("\x00", 333),
			MessageBody: strings.Repeat("\x00", 333),
		},
		{
			ID:          3,
			SourceFile:  sourceFile,
			Timestamp:   time.Now(),
			Hostname:    "CORE-SW-01",
			Module:      "IFNET",
			Severity:    4,
			RawLog:      "Apr 15 2026 14:00:02 CORE-SW-01\x00 %%01IFNET/4/IF_UP(l)[2]: Interface state turned to UP.",
			MessageBody: "Interface state turned to UP.",
		},
	}
	if err := taskDB.Create(&injectedRecords).Error; err != nil {
		t.Fatalf("inject records failed: %v", err)
	}

	// 模拟对应的 TaskFile 和 TaskInfo 记录（初始行数为 3）
	tf := model.TaskFile{
		TaskID:    taskInfo.TaskID,
		FileName:  sourceFile,
		LineCount: 3,
		CreatedAt: time.Now(),
	}
	if err := taskDB.Create(&tf).Error; err != nil {
		t.Fatalf("create task file failed: %v", err)
	}

	taskInfo.LogCount = 3
	svc.persistTaskInfo(taskDB, taskInfo)

	// 执行重分析：触发清洗回写与坏块物理删除
	reanalyzedTask, err := svc.ReanalyzeTask(taskInfo.TaskID, nil)
	if err != nil {
		t.Fatalf("ReanalyzeTask failed unexpectedly: %v", err)
	}

	// 1. 验证总行数被修正为 2（坏块行 ID=2 被物理删除）
	if reanalyzedTask.LogCount != 2 {
		t.Errorf("reanalyzedTask.LogCount got %d, want 2", reanalyzedTask.LogCount)
	}

	var dbCount int64
	taskDB.Model(&model.LogRecord{}).Count(&dbCount)
	if dbCount != 2 {
		t.Errorf("db record count got %d, want 2", dbCount)
	}

	// 2. 验证 ID=2 的坏块行已不存在
	var rec2 model.LogRecord
	if err := taskDB.First(&rec2, 2).Error; err == nil {
		t.Errorf("expected record 2 to be physically deleted, but still found in db: %+v", rec2)
	}

	// 3. 验证记录 3 的 raw_log 得到清洗回写，不再包含 \x00
	var rec3 model.LogRecord
	if err := taskDB.First(&rec3, 3).Error; err != nil {
		t.Fatalf("failed to query record 3: %v", err)
	}
	if strings.Contains(rec3.RawLog, "\x00") {
		t.Errorf("record 3 raw_log still contains \\x00: %q", rec3.RawLog)
	}
	if strings.Contains(rec3.MessageBody, "\x00") {
		t.Errorf("record 3 message_body still contains \\x00: %q", rec3.MessageBody)
	}

	// 4. 验证 task_files.line_count 得到刷新回写（从 3 刷新为 2）
	var refreshedTF model.TaskFile
	if err := taskDB.First(&refreshedTF, "task_id = ? AND file_name = ?", taskInfo.TaskID, sourceFile).Error; err != nil {
		t.Fatalf("failed to query task file: %v", err)
	}
	if refreshedTF.LineCount != 2 {
		t.Errorf("refreshed task_file.LineCount got %d, want 2", refreshedTF.LineCount)
	}
}
