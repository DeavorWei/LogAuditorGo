package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

func TestFailTask_Comprehensive(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_fail_test_*")
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

	// 1. 测试标准错误流转：cause != nil, message != ""
	taskID1 := "fail_test_task_01"
	taskInfo1 := &model.TaskInfo{
		TaskID:     taskID1,
		TaskName:   "失败测试任务1",
		DeviceType: "CloudEngine",
		Status:     model.TaskStatusProcessing,
		StartTime:  time.Now(),
	}
	if err := globalDB.Create(taskInfo1).Error; err != nil {
		t.Fatalf("create task in globalDB failed: %v", err)
	}

	taskDB1, _, err := storage.GetOrCreateTaskDB(taskDir, taskID1)
	if err != nil {
		t.Fatalf("create taskDB failed: %v", err)
	}
	defer storage.ReleaseTaskDB(taskID1)

	if err := taskDB1.Create(taskInfo1).Error; err != nil {
		t.Fatalf("create task in taskDB failed: %v", err)
	}

	stageDefs := []progress.StageDef{
		{Key: "import", Name: "导入"},
	}
	tr1 := progress.NewJobTracker("job_01", taskID1, "log", stageDefs)
	tr1.SetStage("import", "正在导入")

	causeErr := errors.New("io error occurred")
	resInfo, retErr := svc.failTask(taskDB1, taskInfo1, tr1, causeErr, "任务导入过程中止")

	if resInfo != nil {
		t.Errorf("expected nil taskInfo on failure, got %+v", resInfo)
	}
	if !errors.Is(retErr, causeErr) {
		t.Errorf("expected return error %v, got %v", causeErr, retErr)
	}
	if taskInfo1.Status != model.TaskStatusFailed {
		t.Errorf("expected taskInfo.Status to be FAILED, got %s", taskInfo1.Status)
	}
	if taskInfo1.ErrorMessage != "io error occurred" {
		t.Errorf("expected ErrorMessage 'io error occurred', got %s", taskInfo1.ErrorMessage)
	}

	// 验证双库持久化
	var gTask1 model.TaskInfo
	if err := globalDB.Where("task_id = ?", taskID1).First(&gTask1).Error; err != nil {
		t.Fatalf("query task from globalDB failed: %v", err)
	}
	if gTask1.Status != model.TaskStatusFailed || gTask1.ErrorMessage != "io error occurred" {
		t.Errorf("globalDB task state mismatch: %+v", gTask1)
	}

	var tTask1 model.TaskInfo
	if err := taskDB1.Where("task_id = ?", taskID1).First(&tTask1).Error; err != nil {
		t.Fatalf("query task from taskDB failed: %v", err)
	}
	if tTask1.Status != model.TaskStatusFailed || tTask1.ErrorMessage != "io error occurred" {
		t.Errorf("taskDB task state mismatch: %+v", tTask1)
	}

	// 验证 progress tracker
	jobSnap := tr1.GetSnapshot()
	if jobSnap.Status != progress.JobFailed {
		t.Errorf("expected tracker status failed, got %s", jobSnap.Status)
	}
	if jobSnap.Error != "io error occurred" {
		t.Errorf("expected tracker Error 'io error occurred', got %s", jobSnap.Error)
	}

	// 2. 测试边界：cause == nil, message != ""
	taskID2 := "fail_test_task_02"
	taskInfo2 := &model.TaskInfo{
		TaskID:     taskID2,
		TaskName:   "失败测试任务2",
		DeviceType: "CloudEngine",
		Status:     model.TaskStatusProcessing,
	}
	_, retErr2 := svc.failTask(nil, taskInfo2, nil, nil, "纯提示信息失败")
	if retErr2 == nil || retErr2.Error() != "纯提示信息失败" {
		t.Errorf("expected error message '纯提示信息失败', got %v", retErr2)
	}
	if taskInfo2.ErrorMessage != "纯提示信息失败" {
		t.Errorf("expected taskInfo.ErrorMessage '纯提示信息失败', got %s", taskInfo2.ErrorMessage)
	}

	// 3. 测试边界：cause == nil, message == "" (未知错误兜底)
	taskInfo3 := &model.TaskInfo{TaskID: "fail_test_task_03"}
	_, retErr3 := svc.failTask(nil, taskInfo3, nil, nil, "")
	if retErr3 == nil || !strings.Contains(retErr3.Error(), "unknown task failure") {
		t.Errorf("expected unknown task failure error, got %v", retErr3)
	}

	// 4. 测试边界：taskInfo == nil, tr == nil (全空防 panic 防御)
	_, retErr4 := svc.failTask(nil, nil, nil, errors.New("bare error"), "")
	if retErr4 == nil || retErr4.Error() != "bare error" {
		t.Errorf("expected bare error, got %v", retErr4)
	}
}
