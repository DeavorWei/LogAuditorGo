package task

import (
	"fmt"
	"strings"
	"time"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
)

// DiagnosisSeverityBucket 严重度分布（0=Emergency，8=Informational）。
type DiagnosisSeverityBucket struct {
	Severity int   `json:"severity"`
	Count    int64 `json:"count"`
}

// DiagnosisTierCount 匹配质量分档计数。
type DiagnosisTierCount struct {
	Tier  string `json:"tier"`
	Count int64  `json:"count"`
}

// DiagnosisModuleStat 按模块聚合的高频统计。
type DiagnosisModuleStat struct {
	Module      string `json:"module"`
	Count       int64  `json:"count"`
	MaxSeverity int    `json:"max_severity"`
	SampleLogID uint   `json:"sample_log_id"`
}

// DiagnosisHotspot 未匹配热点（module+brief）。
type DiagnosisHotspot struct {
	Module      string     `json:"module"`
	Brief       string     `json:"brief"`
	Count       int64      `json:"count"`
	FirstSeen   *time.Time `json:"first_seen,omitempty"`
	LastSeen    *time.Time `json:"last_seen,omitempty"`
	SampleLogID uint       `json:"sample_log_id"`
}

// DiagnosisOverview 诊断摘要概览。
type DiagnosisOverview struct {
	LogCount        int64      `json:"log_count"`
	MatchedCount    int64      `json:"matched_count"`
	UnparsedCount   int64      `json:"unparsed_count"`
	MatchRate       float64    `json:"match_rate"`
	ParseRate       float64    `json:"parse_rate"`
	DeviceCount     int64      `json:"device_count"`
	FileCount       int64      `json:"file_count"`
	TimeStart       *time.Time `json:"time_start,omitempty"`
	TimeEnd         *time.Time `json:"time_end,omitempty"`
	UnassignedCount int64      `json:"unassigned_count"`
}

// DiagnosisSummary 导入完成后的"这次到底发生了什么"聚合结果。
//
// 所有字段均来自任务库单表聚合 SQL，不加载明细，保证 10 万行任务也能在毫秒级返回。
type DiagnosisSummary struct {
	TaskID         string                    `json:"task_id"`
	TaskName       string                    `json:"task_name"`
	TaskStatus     string                    `json:"task_status"`
	CaseStatus     string                    `json:"case_status"`
	Conclusion     string                    `json:"conclusion"`
	Overview       DiagnosisOverview         `json:"overview"`
	SeverityDist   []DiagnosisSeverityBucket `json:"severity_distribution"`
	MatchQuality   []DiagnosisTierCount      `json:"match_quality"`
	TopModules     []DiagnosisModuleStat     `json:"top_modules"`
	UnmatchedTop   []DiagnosisHotspot        `json:"unmatched_hotspots"`
	Suggestions    []string                  `json:"suggestions"`
	FirstHighLogID uint                      `json:"first_high_log_id"`
}

// GetDiagnosisSummary 汇总任务诊断摘要。
func (s *Service) GetDiagnosisSummary(taskID string) (*DiagnosisSummary, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}
	taskInfo, err := s.GetTaskByID(taskID)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		return nil, fmt.Errorf("open task db failed: %w", err)
	}
	defer storage.ReleaseTaskDB(taskID)

	summary := &DiagnosisSummary{
		TaskID:       taskInfo.TaskID,
		TaskName:     taskInfo.TaskName,
		TaskStatus:   string(taskInfo.Status),
		CaseStatus:   taskInfo.CaseStatus,
		Conclusion:   taskInfo.Conclusion,
		SeverityDist: []DiagnosisSeverityBucket{},
		MatchQuality: []DiagnosisTierCount{},
		TopModules:   []DiagnosisModuleStat{},
		UnmatchedTop: []DiagnosisHotspot{},
		Suggestions:  []string{},
	}

	// 1. 概览（单次扫描）
	//
	// SQLite 以字符串存储时间，直接 Scan 到 *time.Time 会报
	// "unsupported Scan, storing driver.Value type string"；先取字符串再解析。
	var overviewRow struct {
		Total      int64
		Matched    int64
		Unparsed   int64
		Unassigned int64
		MinTime    string
		MaxTime    string
	}
	if err := taskDB.Model(&model.LogRecord{}).
		Select("COUNT(*) AS total, " +
			"COALESCE(SUM(CASE WHEN knowledge_id > 0 THEN 1 ELSE 0 END), 0) AS matched, " +
			"COALESCE(SUM(CASE WHEN module = 'UNPARSED' OR brief = 'UNPARSED' THEN 1 ELSE 0 END), 0) AS unparsed, " +
			"COALESCE(SUM(CASE WHEN device_id = 0 THEN 1 ELSE 0 END), 0) AS unassigned, " +
			"MIN(timestamp) AS min_time, MAX(timestamp) AS max_time").
		Scan(&overviewRow).Error; err != nil {
		return nil, fmt.Errorf("aggregate overview failed: %w", err)
	}
	summary.Overview = DiagnosisOverview{
		LogCount:        overviewRow.Total,
		MatchedCount:    overviewRow.Matched,
		UnparsedCount:   overviewRow.Unparsed,
		UnassignedCount: overviewRow.Unassigned,
		TimeStart:       parseSQLTime(overviewRow.MinTime),
		TimeEnd:         parseSQLTime(overviewRow.MaxTime),
	}
	if overviewRow.Total > 0 {
		summary.Overview.MatchRate = roundRatio(overviewRow.Matched, overviewRow.Total)
		summary.Overview.ParseRate = roundRatio(overviewRow.Total-overviewRow.Unparsed, overviewRow.Total)
	}

	var fileCount int64
	if err := taskDB.Model(&model.TaskFile{}).Where("task_id = ?", taskID).Count(&fileCount).Error; err == nil {
		summary.Overview.FileCount = fileCount
	}
	if err := taskDB.Model(&model.LogRecord{}).Where("device_id > 0").
		Distinct("device_id").Count(&summary.Overview.DeviceCount).Error; err != nil {
		summary.Overview.DeviceCount = 0
	}

	// 2. 严重度分布
	if err := taskDB.Model(&model.LogRecord{}).
		Select("severity, COUNT(*) AS count").
		Group("severity").Order("severity ASC").
		Scan(&summary.SeverityDist).Error; err != nil {
		return nil, fmt.Errorf("aggregate severity failed: %w", err)
	}

	// 3. 匹配质量
	if err := taskDB.Model(&model.LogRecord{}).
		Select("match_tier AS tier, COUNT(*) AS count").
		Group("match_tier").Order("count DESC").
		Scan(&summary.MatchQuality).Error; err != nil {
		return nil, fmt.Errorf("aggregate match tier failed: %w", err)
	}

	// 4. Top 模块（排除注释行与空模块）
	if err := taskDB.Model(&model.LogRecord{}).
		Select("module, COUNT(*) AS count, MAX(severity) AS max_severity, MIN(id) AS sample_log_id").
		Where("module <> '' AND module <> ?", "COMMENT").
		Group("module").Order("count DESC, module ASC").Limit(5).
		Scan(&summary.TopModules).Error; err != nil {
		return nil, fmt.Errorf("aggregate modules failed: %w", err)
	}

	// 5. 未匹配热点（时间先取字符串，再统一解析）
	var hotspotRows []struct {
		Module      string
		Brief       string
		Count       int64
		FirstSeen   string
		LastSeen    string
		SampleLogID uint
	}
	if err := taskDB.Model(&model.LogRecord{}).
		Select("module, brief, COUNT(*) AS count, MIN(timestamp) AS first_seen, MAX(timestamp) AS last_seen, MIN(id) AS sample_log_id").
		Where("knowledge_id = 0 AND module <> '' AND module <> ?", "COMMENT").
		Group("module, brief").Order("count DESC, module ASC").Limit(10).
		Scan(&hotspotRows).Error; err != nil {
		return nil, fmt.Errorf("aggregate unmatched hotspots failed: %w", err)
	}
	for _, row := range hotspotRows {
		summary.UnmatchedTop = append(summary.UnmatchedTop, DiagnosisHotspot{
			Module:      row.Module,
			Brief:       row.Brief,
			Count:       row.Count,
			FirstSeen:   parseSQLTime(row.FirstSeen),
			LastSeen:    parseSQLTime(row.LastSeen),
			SampleLogID: row.SampleLogID,
		})
	}

	// 6. 最高严重度且已匹配的日志（"开始逐条排查"一键定位）
	var firstHigh struct {
		ID uint
	}
	if err := taskDB.Model(&model.LogRecord{}).
		Select("id").
		Where("knowledge_id > 0 AND module <> ? AND module <> ''", "COMMENT").
		Order("severity ASC, timestamp ASC, id ASC").Limit(1).
		Scan(&firstHigh).Error; err == nil {
		summary.FirstHighLogID = firstHigh.ID
	}

	summary.Suggestions = buildDiagnosisSuggestions(summary)
	return summary, nil
}

// roundRatio 计算百分比（保留一位小数），分母为 0 时返回 0。
func roundRatio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	ratio := float64(numerator) * 100 / float64(denominator)
	return float64(int64(ratio*10+0.5)) / 10
}

// buildDiagnosisSuggestions 生成确定性的下一步建议（不调用模型，可复现）。
func buildDiagnosisSuggestions(summary *DiagnosisSummary) []string {
	if summary == nil {
		return nil
	}
	suggestions := make([]string, 0, 5)
	if summary.Overview.LogCount == 0 {
		return []string{"任务尚无日志：请先导入日志文件或粘贴日志文本，再查看诊断摘要。"}
	}
	if len(summary.TopModules) > 0 {
		top := summary.TopModules[0]
		suggestions = append(suggestions, fmt.Sprintf(
			"优先查看 %s 模块（%d 条，最高级别 Lv.%d），它在本任务中出现最频繁。",
			top.Module, top.Count, top.MaxSeverity))
	}
	if len(summary.UnmatchedTop) > 0 {
		top := summary.UnmatchedTop[0]
		suggestions = append(suggestions, fmt.Sprintf(
			"有 %d 类日志暂无官方知识，Top 为 %s/%s（%d 条）；建议在知识库补充或绑定本地知识。",
			len(summary.UnmatchedTop), top.Module, top.Brief, top.Count))
	}
	if summary.Overview.MatchedCount == 0 {
		suggestions = append(suggestions,
			"本任务尚无任何知识命中：请确认已导入对应产品/版本的 HDX 文档，必要时在“产品文档”中补充。")
	}
	if summary.Overview.UnassignedCount > 0 {
		suggestions = append(suggestions, fmt.Sprintf(
			"仍有 %d 条日志未归属设备，打开“设备”抽屉执行自动归属后重新分析可获得更准确的匹配上下文。",
			summary.Overview.UnassignedCount))
	}
	suggestions = append(suggestions,
		"逐条排查后，把判断写入案例“结论”（支持自动草稿），再导出报告或证据包完成交接。")
	return suggestions
}

// GenerateSummaryMarkdown 生成可复制/可粘贴的交接摘要（不做脱敏）。
func (s *Service) GenerateSummaryMarkdown(taskID string) (string, error) {
	summary, err := s.GetDiagnosisSummary(taskID)
	if err != nil {
		return "", err
	}
	task, err := s.GetTaskByID(taskID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(task.TaskName)
	b.WriteString(" · 排障摘要\n\n")
	b.WriteString(fmt.Sprintf("- 任务 ID：%s\n", task.TaskID))
	b.WriteString(fmt.Sprintf("- 案例状态：%s\n", caseStatusLabel(task.CaseStatus)))
	b.WriteString(fmt.Sprintf("- 日志 / 匹配：%d 行 / %d 条（知识覆盖率 %.1f%%）\n",
		summary.Overview.LogCount, summary.Overview.MatchedCount, summary.Overview.MatchRate))
	b.WriteString(fmt.Sprintf("- 设备 / 文件：%d 台 / %d 个\n", summary.Overview.DeviceCount, summary.Overview.FileCount))
	if summary.Overview.TimeStart != nil && summary.Overview.TimeEnd != nil {
		b.WriteString(fmt.Sprintf("- 时间范围：%s ~ %s\n",
			summary.Overview.TimeStart.Format("2006-01-02 15:04:05"),
			summary.Overview.TimeEnd.Format("2006-01-02 15:04:05")))
	}
	b.WriteString("\n## 结论\n\n")
	if strings.TrimSpace(task.Conclusion) != "" {
		b.WriteString(task.Conclusion)
	} else {
		b.WriteString("（未填写；可在工作台“结论”抽屉补充后重新生成）")
	}
	b.WriteString("\n\n## 关键发现\n\n")
	if len(summary.TopModules) == 0 {
		b.WriteString("- 暂无模块统计\n")
	}
	for _, item := range summary.TopModules {
		b.WriteString(fmt.Sprintf("- %s：%d 条，最高级别 Lv.%d\n", item.Module, item.Count, item.MaxSeverity))
	}
	if len(summary.UnmatchedTop) > 0 {
		b.WriteString("\n## 知识盲区\n\n")
		for _, item := range summary.UnmatchedTop {
			b.WriteString(fmt.Sprintf("- %s/%s：%d 条暂无官方知识\n", item.Module, item.Brief, item.Count))
		}
	}
	b.WriteString("\n## 下一步\n\n")
	for _, suggestion := range summary.Suggestions {
		b.WriteString("- ")
		b.WriteString(suggestion)
		b.WriteString("\n")
	}
	b.WriteString("\n## 方法与局限\n\n")
	b.WriteString("- 知识匹配基于已导入的华为官方 HDX 文档，未命中不代表日志无意义。\n")
	b.WriteString("- 本摘要不做脱敏，包含任务与日志统计信息，外发前请自行评估敏感性。\n")
	b.WriteString("- 关联事件（实验性）为启发式推断，不是官方结论，默认不进入本摘要。\n")
	return b.String(), nil
}
