package task

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

// reportTemplates 报告模板集合 (REANA-14)。
//
// 原先约 250 行 HTML/CSS 硬编码在 Go 源的字符串常量里：
// 既无法被 IDE/编辑器正确高亮与校验，也让后续扩展报告章节（REANA-13）
// 只能继续往字符串里堆内容。改为 //go:embed 外置后，
// 模板文件可独立编辑、diff 与 review。
//
//go:embed templates/*.tmpl
var reportTemplates embed.FS

// ReportViewModel 包含渲染独立 HTML 离线报告所需的全部结构化数据
type ReportViewModel struct {
	Task       *model.TaskInfo
	Records    []model.LogRecord
	RCAs       []model.RCAEvent
	Devices    []model.Device
	ExportTime string

	// TotalLogs 为任务中的日志总条数；当 TotalLogs > len(Records) 时说明报告发生了截断，
	// 模板会在报告头部显式提示，避免用户误以为拿到的是全量留痕 (DEV-03)。
	TotalLogs        int
	RecordsTruncated bool

	// KnowledgeMap 命中知识的简要信息，用于在明细表里直接展示官方释义 (REANA-13)
	KnowledgeMap map[uint]model.Knowledge
}

// MultiDeviceReportViewModel 多设备报告的渲染视图。
// 相比 model.MultiDeviceReport 额外携带时间线截断信息，
// 使报告能明确告诉用户"看到的是多少 / 总共多少" (DEV-03 / UI-04)。
type MultiDeviceReportViewModel struct {
	model.MultiDeviceReport
	TimelineTotal     int
	TimelineTruncated bool
}

// 时间线在报告中最多展示的条数
const multiDeviceTimelineLimit = 500

// formatTime 格式化时间戳，对于零值返回 "-"
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

// coveragePercent 计算知识库覆盖率百分比字符串
func coveragePercent(matched, total int) string {
	if total <= 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(matched)/float64(total)*100)
}

// formatConfidence 格式化置信度百分比
func formatConfidence(conf float64) string {
	return fmt.Sprintf("%.0f%%", conf*100)
}

// severityBadgeClass 根据日志级别返回对应的 CSS 样式类
func severityBadgeClass(sev int) string {
	switch {
	case sev <= 2:
		return "sev-crit"
	case sev <= 4:
		return "sev-err"
	case sev <= 5:
		return "sev-warn"
	default:
		return "sev-info"
	}
}

// safeColorPalette 预定义的安全调色板。
//
// REANA-15: html/template 对 style 属性值走 CSS 值过滤，
// 非白名单值（例如用户自定义颜色）会被替换成 `#ZgotmplZ`，配色直接失效。
// 这里先把输入映射到预定义调色板，命中白名单才输出，否则回退到默认蓝。
var safeColorPalette = map[string]string{
	"#3b82f6": "#3b82f6", "#3B82F6": "#3B82F6",
	"#10b981": "#10b981", "#10B981": "#10B981",
	"#f59e0b": "#f59e0b", "#F59E0B": "#F59E0B",
	"#ef4444": "#ef4444", "#EF4444": "#EF4444",
	"#8b5cf6": "#8b5cf6", "#8B5CF6": "#8B5CF6",
	"#ec4899": "#ec4899", "#EC4899": "#EC4899",
	"#14b8a6": "#14b8a6", "#14B8A6": "#14B8A6",
	"#f97316": "#f97316", "#F97316": "#F97316",
	"#6366f1": "#6366f1", "#6366F1": "#6366F1",
	"#84cc16": "#84cc16", "#84CC16": "#84CC16",
	"#64748b": "#64748b", "#64748B": "#64748B",
}

// defaultSafeColor 配色缺失时的回退值
const defaultSafeColor = "#3b82f6"

// safeColor 把任意颜色输入安全地转换为可在 style 属性中使用的 CSS 值。
//
// 双重防护：先映射到预定义调色板白名单，再用 template.CSS 包装输出。
// 直接把用户可控字符串插进 style 属性，既会被 Go 模板过滤器抹成 #ZgotmplZ，
// 理论上也存在样式注入风险。
func safeColor(raw string) template.CSS {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return template.CSS(defaultSafeColor)
	}
	if mapped, ok := safeColorPalette[trimmed]; ok {
		return template.CSS(mapped)
	}
	// 未登记在白名单中的自定义颜色：降级为默认色，
	// 绝不让 Go 的 CSS 过滤器把值替换成 #ZgotmplZ 破坏整份报告的样式。
	return template.CSS(defaultSafeColor)
}

// matchBadgeHTML 生成安全的知识库匹配状态徽章 HTML
func matchBadgeHTML(knowledgeID uint, matchTier string) template.HTML {
	if knowledgeID > 0 {
		return template.HTML(fmt.Sprintf(`<span class="badge" style="background:#dcfce7; color:#166534;">已匹配 (%s)</span>`, template.HTMLEscapeString(matchTier)))
	}
	return template.HTML(`<span class="badge" style="background:#f1f5f9; color:#64748b;">未匹配</span>`)
}

// knowledgeBrief 从知识映射中取出可读的简要说明（含义优先，其次消息模板）
func knowledgeBrief(kbMap map[uint]model.Knowledge, id uint) string {
	if id == 0 || len(kbMap) == 0 {
		return ""
	}
	kb, ok := kbMap[id]
	if !ok {
		return ""
	}
	brief := strings.TrimSpace(kb.Description)
	if brief == "" {
		brief = strings.TrimSpace(kb.Message)
	}
	runes := []rune(brief)
	if len(runes) > 80 {
		brief = string(runes[:80]) + "..."
	}
	return brief
}

var reportFuncMap = template.FuncMap{
	"formatTime":         formatTime,
	"coveragePercent":    coveragePercent,
	"formatConfidence":   formatConfidence,
	"severityBadgeClass": severityBadgeClass,
	"matchBadgeHTML":     matchBadgeHTML,
	"safeColor":          safeColor,
	"knowledgeBrief":     knowledgeBrief,
}

// reportTemplate 单任务 HTML 报告模板（加载失败时 panic，属于编译期可发现的问题）
var reportTemplate = template.Must(template.New("report.html.tmpl").Funcs(reportFuncMap).ParseFS(reportTemplates, "templates/report.html.tmpl"))

// multiReportTemplate 多设备协同分析报告模板
var multiReportTemplate = template.Must(template.New("multi_device.html.tmpl").Funcs(reportFuncMap).ParseFS(reportTemplates, "templates/multi_device.html.tmpl"))

// ReportOption 报告渲染的可选扩展项 (REANA-13)。
// 采用 Functional Option 是为了在不破坏既有 4 参数调用点的前提下扩展报告内容。
type ReportOption func(*ReportViewModel)

// WithDevices 在报告中补充设备概况章节
func WithDevices(devices []model.Device) ReportOption {
	return func(vm *ReportViewModel) { vm.Devices = devices }
}

// WithKnowledgeMap 在日志明细中补充命中知识的官方释义
func WithKnowledgeMap(kbMap map[uint]model.Knowledge) ReportOption {
	return func(vm *ReportViewModel) { vm.KnowledgeMap = kbMap }
}

// GenerateHTMLReport 生成任务分析的独立 HTML 离线报告
//
// DEV-03: 这里原本还会二次截断到 100 条，叠加调用方已经做过的 Limit(100)，
// 用户拿到的"完整报告"实际只有前 100 条日志，而 RCA 引用的日志可能根本不在其中，
// 审计留痕不可信。截断职责统一上移到 service 层（可配置上限 + 总数回显），
// 本函数只负责渲染传入的数据，不再二次丢弃。
func GenerateHTMLReport(task *model.TaskInfo, records []model.LogRecord, rcas []model.RCAEvent, totalLogs int, opts ...ReportOption) string {
	if task == nil {
		return "<html><body><h3>Task not found</h3></body></html>"
	}

	if totalLogs < len(records) {
		totalLogs = len(records)
	}

	data := ReportViewModel{
		Task:             task,
		Records:          records,
		RCAs:             rcas,
		ExportTime:       time.Now().Format("2006-01-02 15:04:05"),
		TotalLogs:        totalLogs,
		RecordsTruncated: totalLogs > len(records),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&data)
		}
	}

	var sb strings.Builder
	if err := reportTemplate.Execute(&sb, data); err != nil {
		return fmt.Sprintf("<html><body><h3>Render report failed: %s</h3></body></html>", template.HTMLEscapeString(err.Error()))
	}

	return sb.String()
}

// GenerateMultiDeviceHTMLReport 生成多设备协同对比与时间线 HTML 离线报告
func GenerateMultiDeviceHTMLReport(report *model.MultiDeviceReport) string {
	if report == nil {
		return "<html><body><h3>Report not found</h3></body></html>"
	}

	total := len(report.Timeline)
	displayTimeline := report.Timeline
	if len(displayTimeline) > multiDeviceTimelineLimit {
		displayTimeline = displayTimeline[:multiDeviceTimelineLimit]
	}

	data := MultiDeviceReportViewModel{
		MultiDeviceReport: *report,
		TimelineTotal:     total,
		TimelineTruncated: total > len(displayTimeline),
	}
	data.Timeline = displayTimeline
	if data.ExportTime == "" {
		data.ExportTime = time.Now().Format("2006-01-02 15:04:05")
	}

	var sb strings.Builder
	if err := multiReportTemplate.Execute(&sb, data); err != nil {
		return fmt.Sprintf("<html><body><h3>Render multi-device report failed: %s</h3></body></html>", template.HTMLEscapeString(err.Error()))
	}

	return sb.String()
}
