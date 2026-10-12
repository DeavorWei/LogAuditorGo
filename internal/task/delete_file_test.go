package task_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func TestDeleteTaskFileE2E(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "del_task_files_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("InitKnowledgeDB failed: %v", err)
	}

	matchEngine := matcher.NewMatchEngine(globalDB, nil)
	rcaEngine := rootcause.NewEngine(nil)
	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, matchEngine, rcaEngine)

	// 1. 创建任务
	createdTask, err := svc.CreateEmptyTask("Test Task for Deletion", "CloudEngine")
	if err != nil {
		t.Fatalf("CreateEmptyTask failed: %v", err)
	}

	// 2. 导入两个文件
	f1 := task.FileUploadItem{
		FileName: "sw01.log",
		FileSize: 200,
		Content: `Apr 15 2026 14:00:01 CORE-SW-01 %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)
Apr 15 2026 14:00:02 CORE-SW-01 %%01BFD/2/BFD_SESS_DOWN(l)[2]: BFD session state changed to DOWN. (SessionID=10)`,
	}
	f2 := task.FileUploadItem{
		FileName: "fw02.log",
		FileSize: 100,
		Content:  `Apr 15 2026 14:05:00 USG-FW-01 %%01AAA/4/USER_AUTH_FAIL(l)[202]: User authentication failed. (UserName=testuser, UserIP=192.168.10.5)`,
	}

	updatedTask, err := svc.ImportLogs(createdTask.TaskID, []task.FileUploadItem{f1, f2}, "overwrite")
	if err != nil {
		t.Fatalf("ImportLogs failed: %v", err)
	}
	if updatedTask.LogCount != 3 || updatedTask.FileCount != 2 {
		t.Fatalf("expected 3 logs and 2 files, got logs=%d, files=%d", updatedTask.LogCount, updatedTask.FileCount)
	}

	// 3. 获取文件清单
	files, err := svc.GetTaskFiles(createdTask.TaskID)
	if err != nil || len(files) != 2 {
		t.Fatalf("GetTaskFiles failed: %v, len=%d", err, len(files))
	}

	var swFile model.TaskFile
	for _, f := range files {
		if strings.HasSuffix(f.FileName, "sw01.log") {
			swFile = f
			break
		}
	}
	if swFile.ID == 0 {
		t.Fatalf("sw01.log file ID not found")
	}

	// 4. 删除 sw01.log
	afterDel1, err := svc.DeleteTaskFile(createdTask.TaskID, swFile.ID)
	if err != nil {
		t.Fatalf("DeleteTaskFile sw01 failed: %v", err)
	}
	if afterDel1.FileCount != 1 || afterDel1.LogCount != 1 {
		t.Fatalf("expected 1 file and 1 log after deleting sw01, got files=%d, logs=%d", afterDel1.FileCount, afterDel1.LogCount)
	}

	// 验证 sw01 日志已被清除
	swLogs, totalSw, err := svc.QueryTaskLogs(createdTask.TaskID, model.LogQueryFilter{SourceFile: swFile.FileName})
	if err != nil || totalSw != 0 || len(swLogs) != 0 {
		t.Fatalf("expected 0 logs for sw01 after deletion, got %d", totalSw)
	}

	// 5. 删除剩余的 fw02.log
	remainingFiles, err := svc.GetTaskFiles(createdTask.TaskID)
	if err != nil || len(remainingFiles) != 1 {
		t.Fatalf("expected 1 remaining file, got %d", len(remainingFiles))
	}

	afterDel2, err := svc.DeleteTaskFile(createdTask.TaskID, remainingFiles[0].ID)
	if err != nil {
		t.Fatalf("DeleteTaskFile fw02 failed: %v", err)
	}
	if afterDel2.FileCount != 0 || afterDel2.LogCount != 0 {
		t.Fatalf("expected 0 files and 0 logs, got files=%d, logs=%d", afterDel2.FileCount, afterDel2.LogCount)
	}
	if afterDel2.Status != model.TaskStatusPending {
		t.Fatalf("expected PENDING status after all files deleted, got %s", afterDel2.Status)
	}
	if afterDel2.FinishTime != nil {
		t.Fatalf("expected nil FinishTime for PENDING task, got %v", afterDel2.FinishTime)
	}
}
