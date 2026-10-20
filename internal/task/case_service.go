package task

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

// TaskCaseUpdateRequest 案例字段更新请求。
//
// 全部字段使用指针，区分"未提供"与"显式清空"：
// 前端把结论清空时必须能真正落库为空串，而不是被当成"未修改"。
type TaskCaseUpdateRequest struct {
	CaseStatus  *string `json:"case_status"`
	Conclusion  *string `json:"conclusion"`
	Notes       *string `json:"notes"`
	ExternalRef *string `json:"external_ref"`
	CaseTags    *string `json:"case_tags"`
}

// normalizeCaseTags 归一化案例标签：逗号分隔、去空白、忽略大小写去重、保持输入顺序。
func normalizeCaseTags(raw string) string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' })
	seen := make(map[string]bool, len(parts))
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return strings.Join(tags, ",")
}

// UpdateTaskCase 更新任务案例字段（结论/状态/备注/工单号/标签），双写全局库与任务库。
func (s *Service) UpdateTaskCase(taskID string, req TaskCaseUpdateRequest) (*model.TaskInfo, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}

	updates := make(map[string]interface{})
	if req.CaseStatus != nil {
		status := strings.ToLower(strings.TrimSpace(*req.CaseStatus))
		if status != "" && !model.CaseStatusSet[status] {
			return nil, fmt.Errorf("invalid case_status: %s", status)
		}
		updates["case_status"] = status
	}
	if req.Conclusion != nil {
		updates["conclusion"] = strings.TrimSpace(*req.Conclusion)
	}
	if req.Notes != nil {
		updates["notes"] = strings.TrimSpace(*req.Notes)
	}
	if req.ExternalRef != nil {
		updates["external_ref"] = strings.TrimSpace(*req.ExternalRef)
	}
	if req.CaseTags != nil {
		updates["case_tags"] = normalizeCaseTags(*req.CaseTags)
	}

	var existing model.TaskInfo
	if err := s.globalDB.Where("task_id = ?", taskID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("task not found: %s", taskID)
		}
		return nil, err
	}
	if len(updates) == 0 {
		return &existing, nil
	}

	// 任务库先行：失败时全局库尚未变更，避免"响应 500 但全局库已改"的中间态。
	if _, statErr := os.Stat(storage.TaskDBPath(s.taskDir, taskID)); statErr == nil {
		taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
		if err != nil {
			return nil, fmt.Errorf("open task database failed: %w", err)
		}
		defer storage.ReleaseTaskDB(taskID)
		if updateErr := taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates).Error; updateErr != nil {
			logger.Log.Errorf("[Task Service] sync case fields to task db failed: %v", updateErr)
			return nil, fmt.Errorf("sync case fields to task database failed: %w", updateErr)
		}
	}

	if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil {
		return nil, err
	}

	var taskInfo model.TaskInfo
	if err := s.globalDB.Where("task_id = ?", taskID).First(&taskInfo).Error; err != nil {
		return nil, err
	}
	return &taskInfo, nil
}

// TouchTaskOpened 记录任务最近打开时间（失败仅告警，不影响主流程）。
func (s *Service) TouchTaskOpened(taskID string) {
	if !isValidTaskID(taskID) || s == nil || s.globalDB == nil {
		return
	}
	now := time.Now()
	if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).
		Update("last_opened_at", now).Error; err != nil {
		logger.Log.Debugf("[Task Service] touch last_opened_at failed: %v", err)
	}
}

// GenerateConclusionDraft 基于诊断摘要确定性生成结论草稿（Markdown）。
//
// 产品约束：不调用任何模型、不联网；同一任务同一数据必然生成同一草稿。
// 草稿只返回给前端编辑，不自动落库，避免覆盖用户已写的结论。
func (s *Service) GenerateConclusionDraft(taskID string) (string, error) {
	summary, err := s.GetDiagnosisSummary(taskID)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(summary.TaskName)
	b.WriteString(" · 排障结论（草稿）\n\n")
	b.WriteString("- 任务 ID：")
	b.WriteString(summary.TaskID)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("- 日志 / 匹配：%d 行 / %d 条（知识覆盖率 %.1f%%）\n",
		summary.Overview.LogCount, summary.Overview.MatchedCount, summary.Overview.MatchRate))
	b.WriteString(fmt.Sprintf("- 设备 / 文件：%d 台 / %d 个\n", summary.Overview.DeviceCount, summary.Overview.FileCount))
	if summary.Overview.TimeStart != nil && summary.Overview.TimeEnd != nil {
		b.WriteString(fmt.Sprintf("- 时间范围：%s ~ %s\n",
			summary.Overview.TimeStart.Format("2006-01-02 15:04:05"),
			summary.Overview.TimeEnd.Format("2006-01-02 15:04:05")))
	}
	if summary.CaseStatus != "" {
		b.WriteString("- 案例状态：")
		b.WriteString(summary.CaseStatus)
		b.WriteString("\n")
	}

	b.WriteString("\n## 关键发现\n\n")
	if len(summary.TopModules) == 0 {
		b.WriteString("- 暂无模块统计（任务尚未导入日志）\n")
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

	b.WriteString("\n> 本草稿由系统根据任务统计自动生成，未调用任何模型；请人工核对后再作为最终结论。\n")
	return b.String(), nil
}
