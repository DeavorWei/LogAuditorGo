package task_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"logauditorgo/internal/enrich"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

type e2eKBResolver struct {
	db *gorm.DB
}

func (r *e2eKBResolver) GetKnowledgeMapByIDs(ids []uint) (map[uint]*model.Knowledge, error) {
	var list []model.Knowledge
	if err := r.db.Where("id IN ?", ids).Find(&list).Error; err != nil {
		return nil, err
	}
	res := make(map[uint]*model.Knowledge, len(list))
	for i := range list {
		res[list[i].ID] = &list[i]
	}
	return res, nil
}

func TestE2E_FiveFailureLogsRemediation(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "e2e_var_capture_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "knowledge.db")
	globalDB, err := storage.InitKnowledgeDB(dbPath)
	if err != nil {
		t.Fatalf("init global db failed: %v", err)
	}

	// 1. 初始化 5 条官方知识库（自增 ID + 唯一 hash）
	kbList := []model.Knowledge{
		// #1 active (包含 threashold 官方拼写错误)
		{
			Module:      "FWD",
			Severity:    4,
			Brief:       "hwEntityExtCpuUsageNotfication_active",
			Message:     "FWD/4/hwEntityExtCpuUsageNotfication_active: The cpu usage exceeds the threshold value. (forwarding type = [hwCpuUsageTrapType], slot id = [hwCpuUsageTrapSlot], cpu id = [hwCpuUsageTrapCpu], current cpu usage = [hwCpuUsageCurrentUsage], threashold = [hwCpuUsageThreashold])",
			Description: "CPU使用率超过告警阈值。",
			Parameters:  `[{"name":"hwCpuUsageTrapType","description":"转发类型"},{"name":"hwCpuUsageTrapSlot","description":"槽位号"},{"name":"hwCpuUsageTrapCpu","description":"CPU号"},{"name":"hwCpuUsageCurrentUsage","description":"CPU当前利用率"},{"name":"hwCpuUsageThreashold","description":"CPU利用率告警阈值"}]`,
			ContentHash: "hash_e2e_test_kb_1",
		},
		// #2 clear (包含 threashold 官方拼写错误)
		{
			Module:      "FWD",
			Severity:    4,
			Brief:       "hwEntityExtCpuUsageNotfication_clear",
			Message:     "FWD/4/hwEntityExtCpuUsageNotfication_clear: The cpu usage fell below the threshold value. The lower threshold is 0.9 times the upper threshold. (forwarding type = [hwCpuUsageTrapType], slot id = [hwCpuUsageTrapSlot], cpu id = [hwCpuUsageTrapCpu], current cpu usage = [hwCpuUsageCurrentUsage], threashold = [hwCpuUsageThreashold])",
			Description: "CPU利用率恢复阈值下限以下。其中阈值下限为阈值上限的0.9。",
			Parameters:  `[{"name":"hwCpuUsageTrapType","description":"转发类型"},{"name":"hwCpuUsageTrapSlot","description":"槽位号"},{"name":"hwCpuUsageTrapCpu","description":"CPU号"},{"name":"hwCpuUsageCurrentUsage","description":"CPU当前利用率"},{"name":"hwCpuUsageThreashold","description":"CPU利用率告警阈值"}]`,
			ContentHash: "hash_e2e_test_kb_2",
		},
		// #3 DROP_LOG
		{
			Module:      "FWD",
			Severity:    4,
			Brief:       "SYS_STAT_DROP_LOG",
			Message:     "FWD/4/SYS_STAT_DROP_LOG: The forwarding engine detects packet loss. (Slot=[slotId], CPU=[cpuId], Drop reason=[dropReason], Drop count=[dropCount])",
			Description: "1分钟内同一种丢包原因的报文丢弃数量超过1000个，则记录该丢包信息。",
			Parameters:  `[{"name":"slotId","description":"槽位号。"},{"name":"cpuId","description":"CPU号。"},{"name":"dropReason","description":"丢弃原因。"},{"name":"dropCount","description":"丢弃数量。"}]`,
			ContentHash: "hash_e2e_test_kb_3",
		},
		// #4 SESSCTRLEND
		{
			Module:      "FWD",
			Severity:    4,
			Brief:       "SESSCTRLEND",
			Message:     "FWD/4/SESSCTRLEND: Session creation control ended, SLOT [slot-id],CPU [cpu-id],The CPU usage was [cpu-usage]. In the process, [permitted-packets-num] packets were permitted and [blocked-packets-num] packets were blocked.",
			Description: "记录会话抑制结束事件。",
			Parameters:  `[{"name":"slot-id","description":"槽位号。"},{"name":"cpu-id","description":"CPU号。"},{"name":"cpu-usage","description":"CPU使用率。"},{"name":"permitted-packets-num","description":"会话抑制周期通过的报文包的个数。"},{"name":"blocked-packets-num","description":"会话抑制周期阻断的报文包的个数。"}]`,
			ContentHash: "hash_e2e_test_kb_4",
		},
		// #5 SuddenChange
		{
			Module:      "ENTEXT",
			Severity:    4,
			Brief:       "hwEntityExtCpuUsageSuddenChangeNotification_active",
			Message:     "ENTEXT/4/hwEntityExtCpuUsageSuddenChangeNotification_active: The CPU usage on SPU [hwEntitySlotID] CPU [hwEntityCpuID] is suddenly changed from [hwEntityPreviousValue]% to [hwEntityCurrentValue]%, and the change value is [hwEntityChangeValue]%, exceeding threshold value [hwEntityChangeValueThreshold]%.",
			Description: "CPU使用率发生突变。",
			Parameters:  `[{"name":"hwEntitySlotID","description":"槽位号。"},{"name":"hwEntityCpuID","description":"CPU号。"},{"name":"hwEntityPreviousValue","description":"上一个时间点的CPU使用率。"},{"name":"hwEntityCurrentValue","description":"当前时间点的CPU使用率。"},{"name":"hwEntityChangeValue","description":"上一个时间点和当前时间点CPU使用率的变化值。"},{"name":"hwEntityChangeValueThreshold","description":"CPU使用率的突变告警阈值。"}]`,
			ContentHash: "hash_e2e_test_kb_5",
		},
	}

	for i := range kbList {
		if err := globalDB.Create(&kbList[i]).Error; err != nil {
			t.Fatalf("insert kb failed: %v", err)
		}
	}

	matchEngine := matcher.NewMatchEngine(globalDB, nil)
	taskDir := filepath.Join(tmpDir, "tasks")
	svc := task.NewService(globalDB, taskDir, matchEngine, nil)

	taskInfo, err := svc.CreateEmptyTask("E2E-Variable-Capture-Task", "CloudEngine")
	if err != nil {
		t.Fatalf("create task failed: %v", err)
	}

	rawLogsContent := `Oct 9 2026 11:51:48+08:00 Longan-1 %%01FWD/4/hwEntityExtCpuUsageNotfication_active(l):CID=0x0-alarmID=0x00f103b4;The CPU usage exceeds the threshold value. (forwarding type=1, slot id=0, CPU id=0, current CPU usage=97, threshold=90)
Oct 9 2026 11:52:00+08:00 Longan-1 %%01FWD/4/hwEntityExtCpuUsageNotfication_clear(l):CID=0x0-alarmID=0x00f103b4-clearType=service_resume;The CPU usage fell below the threshold value. The lower threshold is 0.9 times the upper threshold. (forwarding type=1, slot id=0, CPU id=0, current CPU usage=55, threshold=81)
Oct 9 2026 11:52:11+08:00 Longan-1 %%01FWD/4/SYS_STAT_DROP_LOG(l):Service=hppd[10177]0;The forwarding engine detects packet loss. (Slot=0, CPU=0, Drop reason=TTL exceed packets discarded, Drop count=28298)
Oct 9 2026 11:52:18+08:00 Longan-1 %%01FWD/4/SESSCTRLEND(l):Service=hppd[10177]0;Session creation control ended, SLOT 0,CPU 0,The CPU usage was 100. In the process, 0 packets were permitted and 0 packets were blocked.
Oct 9 2026 11:52:31+08:00 Longan-1 %%01ENTEXT/4/hwEntityExtCpuUsageSuddenChangeNotification_active(l):CID=0x814f042e-alarmID=0x00f10334;The CPU usage on SPU 0 CPU 0 is suddenly changed from 98% to 1%, and the change value is 97%, exceeding threshold value 40%.`

	item := task.FileUploadItem{
		FileName: "failure_samples.log",
		Content:  rawLogsContent,
	}

	updatedTask, importErr := svc.ImportLogs(taskInfo.TaskID, []task.FileUploadItem{item}, "overwrite")
	if importErr != nil {
		t.Fatalf("import logs failed: %v", importErr)
	}
	if updatedTask.LogCount != 5 {
		t.Fatalf("expected 5 logs imported, got %d", updatedTask.LogCount)
	}

	logs, total, err := svc.QueryTaskLogs(taskInfo.TaskID, model.LogQueryFilter{})
	if err != nil || total != 5 {
		t.Fatalf("query logs failed: err=%v, total=%d", err, total)
	}

	// 2. 验证日志持久化与盲扫/模板捕获字段
	t.Logf("Query returned %d logs", len(logs))
	for i, log := range logs {
		var paramMap map[string]string
		_ = json.Unmarshal([]byte(log.ParametersJSON), &paramMap)

		switch i {
		case 0: // #1 active
			if paramMap["hwCpuUsageTrapType"] != "1" ||
				paramMap["hwCpuUsageTrapSlot"] != "0" ||
				paramMap["hwCpuUsageTrapCpu"] != "0" ||
				paramMap["hwCpuUsageCurrentUsage"] != "97" ||
				paramMap["hwCpuUsageThreashold"] != "90" {
				t.Errorf("#1 captured params missing in persistence: %s", log.ParametersJSON)
			}
			// 盲扫切分
			if paramMap["CID"] != "0x0" || paramMap["alarmID"] != "0x00f103b4" {
				t.Errorf("#1 blind scan hyphen split missing: %s", log.ParametersJSON)
			}
			// 复合键
			if paramMap["forwarding type"] != "1" || paramMap["slot id"] != "0" || paramMap["CPU id"] != "0" {
				t.Errorf("#1 composite keys missing: %s", log.ParametersJSON)
			}

		case 1: // #2 clear
			if paramMap["hwCpuUsageTrapType"] != "1" ||
				paramMap["hwCpuUsageCurrentUsage"] != "55" ||
				paramMap["hwCpuUsageThreashold"] != "81" {
				t.Errorf("#2 captured params missing: %s", log.ParametersJSON)
			}
			if paramMap["clearType"] != "service_resume" {
				t.Errorf("#2 clearType hyphen split missing: %s", log.ParametersJSON)
			}

		case 2: // #3 DROP_LOG
			if paramMap["slotId"] != "0" ||
				paramMap["cpuId"] != "0" ||
				paramMap["dropReason"] != "TTL exceed packets discarded" ||
				paramMap["dropCount"] != "28298" {
				t.Errorf("#3 captured params missing: %s", log.ParametersJSON)
			}
			if paramMap["Drop reason"] != "TTL exceed packets discarded" || paramMap["Drop count"] != "28298" {
				t.Errorf("#3 composite keys missing: %s", log.ParametersJSON)
			}

		case 3: // #4 SESSCTRLEND
			if paramMap["slot-id"] != "0" ||
				paramMap["cpu-id"] != "0" ||
				paramMap["cpu-usage"] != "100" ||
				paramMap["permitted-packets-num"] != "0" ||
				paramMap["blocked-packets-num"] != "0" {
				t.Errorf("#4 captured params missing: %s", log.ParametersJSON)
			}

		case 4: // #5 SuddenChange
			if paramMap["hwEntitySlotID"] != "0" ||
				paramMap["hwEntityCpuID"] != "0" ||
				paramMap["hwEntityPreviousValue"] != "98" ||
				paramMap["hwEntityCurrentValue"] != "1" ||
				paramMap["hwEntityChangeValue"] != "97" ||
				paramMap["hwEntityChangeValueThreshold"] != "40" {
				t.Errorf("#5 captured params missing: %s", log.ParametersJSON)
			}
			if paramMap["CID"] != "0x814f042e" || paramMap["alarmID"] != "0x00f10334" {
				t.Errorf("#5 hyphen split missing: %s", log.ParametersJSON)
			}
		}
	}

	// 3. 验证 EnrichLogs 富化结果（全链路）
	enrichSvc := enrich.NewService(&e2eKBResolver{db: globalDB})
	enrichedRecords := enrichSvc.EnrichLogs(logs)
	if len(enrichedRecords) != 5 {
		t.Fatalf("expected 5 enriched records, got %d", len(enrichedRecords))
	}

	expectedMatchedCounts := []int{5, 5, 4, 5, 6}
	for i, er := range enrichedRecords {
		matchedCount := 0
		for _, p := range er.EnrichedParameters {
			if p.Matched {
				matchedCount++
			}
		}
		if matchedCount < expectedMatchedCounts[i] {
			t.Errorf("record #%d matched count %d < expected %d", i+1, matchedCount, expectedMatchedCounts[i])
		}
		// 验证模板占位符已被完全替换，不含 "[" 或 "]"
		if strings.Contains(er.RenderedMessage, "[") || strings.Contains(er.RenderedMessage, "]") {
			t.Errorf("record #%d template instantiation incomplete: %s", i+1, er.RenderedMessage)
		}
	}

	// 4. 验证 HTML 导出报告一致性
	html, err := svc.ExportTaskHTML(taskInfo.TaskID)
	if err != nil {
		t.Fatalf("export html failed: %v", err)
	}
	if !strings.Contains(html, "hwEntityExtCpuUsageNotfication_active") ||
		!strings.Contains(html, "hwEntityExtCpuUsageSuddenChangeNotification_active") ||
		!strings.Contains(html, "TTL exceed packets discarded") {
		t.Errorf("exported HTML missing key captured variables or descriptions")
	}
}
