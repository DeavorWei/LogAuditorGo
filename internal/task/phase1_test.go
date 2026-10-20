package task_test

import (
	"strings"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
)

func newPhase1Service(t *testing.T) (*task.Service, func()) {
	t.Helper()
	dir := t.TempDir()
	storage.CloseAllTaskDBs()
	_ = storage.CloseKnowledgeDB()
	globalDB, err := storage.InitKnowledgeDB(dir + "/knowledge.db")
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}
	svc := task.NewService(globalDB, dir+"/tasks", nil, nil)
	cleanup := func() {
		storage.CloseAllTaskDBs()
		_ = storage.CloseKnowledgeDB()
	}
	return svc, cleanup
}

const phase1MultiHostLogs = "Apr 15 2026 10:00:01 CE16800-CORE %%01IFNET/4/IF_DOWN(l)[1]: Interface 100GE1/0/1 state turned to DOWN. (InterfaceName=100GE1/0/1)\n" +
	"Apr 15 2026 10:00:02 CE16800-CORE %%01BGP/2/PEER_BACKWARD(l)[2]: The BGP peer went down. (PeerAddress=192.168.1.2)\n" +
	"Apr 15 2026 10:00:03 USG6000F-FW %%01SEC/4/POLICY_DENY(l)[3]: The packet was dropped by policy. (SrcIP=10.0.0.1)\n"

// TestImportAssignsPerHostnameDevice 验证单文件多 Hostname 逐行归属（F14）。
func TestImportAssignsPerHostnameDevice(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateAndRunTask("多设备单文件", "", phase1MultiHostLogs)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	if info.LogCount != 3 {
		t.Fatalf("log count=%d, want 3", info.LogCount)
	}

	devices, err := svc.ListDevices(info.TaskID)
	if err != nil {
		t.Fatalf("list devices failed: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d: %+v", len(devices), devices)
	}

	counts := map[uint]int64{}
	for _, d := range devices {
		counts[d.ID] = 0
	}
	records, _, err := svc.QueryTaskLogs(info.TaskID, model.LogQueryFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("query logs failed: %v", err)
	}
	for _, rec := range records {
		if rec.DeviceID == 0 {
			t.Fatalf("record %d has no device assigned (host=%s)", rec.ID, rec.Hostname)
		}
		counts[rec.DeviceID]++
	}
	if len(counts) != 2 {
		t.Fatalf("expected records across 2 devices, got %v", counts)
	}
	for id, count := range counts {
		if count == 0 {
			t.Fatalf("device %d has 0 records; counts=%v", id, counts)
		}
	}
}

// TestImportPreviewReportsDevicesAndConflicts 验证导入预览的设备分布、时间范围与同名冲突。
func TestImportPreviewReportsDevicesAndConflicts(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateEmptyTask("预览任务", "")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	item := task.FileUploadItem{FileName: "multi.log", FileSize: int64(len(phase1MultiHostLogs)), Content: phase1MultiHostLogs}
	preview, err := svc.PreviewImport(info.TaskID, []task.FileUploadItem{item})
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if preview.TotalFiles != 1 || preview.EstimatedLogs != 3 {
		t.Fatalf("preview totals = files %d logs %d, want 1/3", preview.TotalFiles, preview.EstimatedLogs)
	}
	if len(preview.Devices) != 2 {
		t.Fatalf("preview devices = %+v, want 2", preview.Devices)
	}
	if preview.Devices[0].Lines != 2 {
		t.Fatalf("top device lines = %d, want 2", preview.Devices[0].Lines)
	}
	if preview.TimeRange == nil || preview.TimeRange.Start == nil || preview.TimeRange.End == nil {
		t.Fatalf("preview time range missing: %+v", preview.TimeRange)
	}
	if !preview.TimeRange.Start.Before(*preview.TimeRange.End) {
		t.Fatalf("preview time range invalid: %v ~ %v", preview.TimeRange.Start, preview.TimeRange.End)
	}
	if preview.EstimatedSeconds < 1 {
		t.Fatalf("estimated seconds must be >= 1")
	}

	// 真正导入后再次预览：同名文件应被识别为冲突
	if _, err := svc.ImportLogs(info.TaskID, []task.FileUploadItem{item}, "rename"); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	preview2, err := svc.PreviewImport(info.TaskID, []task.FileUploadItem{item})
	if err != nil {
		t.Fatalf("second preview failed: %v", err)
	}
	if len(preview2.Conflicts) != 1 || preview2.Conflicts[0] != "multi.log" {
		t.Fatalf("expected conflict multi.log, got %+v", preview2.Conflicts)
	}
}

// TestDiagnosisSummaryAggregates 验证诊断摘要的聚合结果与确定性建议。
func TestDiagnosisSummaryAggregates(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateAndRunTask("摘要任务", "", phase1MultiHostLogs)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}
	summary, err := svc.GetDiagnosisSummary(info.TaskID)
	if err != nil {
		t.Fatalf("get summary failed: %v", err)
	}
	if summary.Overview.LogCount != 3 {
		t.Fatalf("log count = %d, want 3", summary.Overview.LogCount)
	}
	if summary.Overview.MatchedCount != 0 {
		t.Fatalf("matched count = %d, want 0 (no knowledge seeder)", summary.Overview.MatchedCount)
	}
	if summary.Overview.DeviceCount != 2 {
		t.Fatalf("device count = %d, want 2", summary.Overview.DeviceCount)
	}
	if len(summary.SeverityDist) == 0 {
		t.Fatalf("severity distribution empty")
	}
	if len(summary.TopModules) == 0 || summary.TopModules[0].Module == "" {
		t.Fatalf("top modules empty: %+v", summary.TopModules)
	}
	if len(summary.UnmatchedTop) != 3 {
		t.Fatalf("unmatched hotspots = %d, want 3", len(summary.UnmatchedTop))
	}
	if len(summary.Suggestions) == 0 {
		t.Fatalf("suggestions empty")
	}
	// 同数据两次生成必须一致（确定性）
	again, err := svc.GetDiagnosisSummary(info.TaskID)
	if err != nil {
		t.Fatalf("second summary failed: %v", err)
	}
	if strings.Join(summary.Suggestions, "|") != strings.Join(again.Suggestions, "|") {
		t.Fatalf("suggestions are not deterministic")
	}
	if summary.FirstHighLogID != 0 {
		t.Fatalf("no knowledge match means no first_high_log_id, got %d", summary.FirstHighLogID)
	}
}

// TestTaskCaseUpdateAndDraft 验证案例字段更新、标签归一化与结论草稿。
func TestTaskCaseUpdateAndDraft(t *testing.T) {
	svc, cleanup := newPhase1Service(t)
	defer cleanup()

	info, err := svc.CreateAndRunTask("案例任务", "", phase1MultiHostLogs)
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	status := model.CaseStatusLocated
	conclusion := "  已定位为链路中断  "
	notes := "现场已更换光纤"
	ref := "INC-12345"
	tags := "BGP, 链路, bgp, ,链路"
	updated, err := svc.UpdateTaskCase(info.TaskID, task.TaskCaseUpdateRequest{
		CaseStatus:  &status,
		Conclusion:  &conclusion,
		Notes:       &notes,
		ExternalRef: &ref,
		CaseTags:    &tags,
	})
	if err != nil {
		t.Fatalf("update case failed: %v", err)
	}
	if updated.CaseStatus != model.CaseStatusLocated {
		t.Fatalf("case status = %q", updated.CaseStatus)
	}
	if updated.Conclusion != "已定位为链路中断" {
		t.Fatalf("conclusion trimming failed: %q", updated.Conclusion)
	}
	if updated.CaseTags != "BGP,链路" {
		t.Fatalf("case tags normalization failed: %q", updated.CaseTags)
	}

	fetched, err := svc.GetTaskByID(info.TaskID)
	if err != nil || fetched.ExternalRef != ref || fetched.Notes != notes {
		t.Fatalf("dual write mismatch: %+v, err=%v", fetched, err)
	}

	bad := "bogus"
	if _, err := svc.UpdateTaskCase(info.TaskID, task.TaskCaseUpdateRequest{CaseStatus: &bad}); err == nil {
		t.Fatalf("invalid case status must be rejected")
	}

	draft, err := svc.GenerateConclusionDraft(info.TaskID)
	if err != nil {
		t.Fatalf("generate draft failed: %v", err)
	}
	if !strings.Contains(draft, "排障结论（草稿）") || !strings.Contains(draft, "下一步") || !strings.Contains(draft, "案例任务") {
		t.Fatalf("draft content unexpected:\n%s", draft)
	}

	markdown, err := svc.GenerateSummaryMarkdown(info.TaskID)
	if err != nil {
		t.Fatalf("generate summary markdown failed: %v", err)
	}
	for _, want := range []string{"排障摘要", "已定位为链路中断", "知识盲区", "方法与局限"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("summary markdown missing %q:\n%s", want, markdown)
		}
	}
}
