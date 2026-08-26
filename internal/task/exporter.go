package task

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

// ReportViewModel 包含渲染独立 HTML 离线报告所需的全部结构化数据
type ReportViewModel struct {
	Task       *model.TaskInfo
	Records    []model.LogRecord
	RCAs       []model.RCAEvent
	ExportTime string
}

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

// matchBadgeHTML 生成安全的知识库匹配状态徽章 HTML
func matchBadgeHTML(knowledgeID uint, matchTier string) template.HTML {
	if knowledgeID > 0 {
		return template.HTML(fmt.Sprintf(`<span class="badge" style="background:#dcfce7; color:#166534;">已匹配 (%s)</span>`, template.HTMLEscapeString(matchTier)))
	}
	return template.HTML(`<span class="badge" style="background:#f1f5f9; color:#64748b;">未匹配</span>`)
}

var (
	reportFuncMap = template.FuncMap{
		"formatTime":         formatTime,
		"coveragePercent":    coveragePercent,
		"formatConfidence":   formatConfidence,
		"severityBadgeClass": severityBadgeClass,
		"matchBadgeHTML":     matchBadgeHTML,
	}

	reportTemplate = template.Must(template.New("html_report").Funcs(reportFuncMap).Parse(htmlReportTpl))
)

const htmlReportTpl = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>LogAuditorGo 审计分析报告 - {{ .Task.TaskName }}</title>
<style>
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; margin: 30px; background: #f8fafc; color: #1e293b; }
  .header { background: #1e293b; color: #fff; padding: 24px; border-radius: 8px; margin-bottom: 24px; }
  .header h1 { margin: 0 0 10px 0; font-size: 24px; }
  .stats-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 24px; }
  .stat-card { background: #fff; padding: 16px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); border-left: 4px solid #3b82f6; }
  .stat-card .val { font-size: 24px; font-weight: bold; color: #1e293b; }
  .stat-card .label { color: #64748b; font-size: 13px; margin-top: 4px; }
  .section { background: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); margin-bottom: 24px; }
  .section h2 { margin-top: 0; font-size: 18px; border-bottom: 2px solid #e2e8f0; padding-bottom: 8px; color: #0f172a; }
  .rca-card { border: 1px solid #fed7aa; background: #fff7ed; border-radius: 6px; padding: 16px; margin-bottom: 16px; }
  .rca-title { font-weight: bold; color: #c2410c; font-size: 15px; margin-bottom: 8px; }
  .rca-action { background: #ffedd5; padding: 10px; border-radius: 4px; font-size: 13px; color: #9a3412; margin-top: 8px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { padding: 10px 12px; text-align: left; border-bottom: 1px solid #e2e8f0; }
  th { background: #f1f5f9; color: #475569; font-weight: 600; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: bold; }
  .sev-crit { background: #fee2e2; color: #991b1b; }
  .sev-err { background: #ffedd5; color: #9a3412; }
  .sev-warn { background: #fef9c3; color: #854d0e; }
  .sev-info { background: #e0f2fe; color: #075985; }
  .code { font-family: monospace; background: #f1f5f9; padding: 2px 4px; border-radius: 3px; font-size: 12px; word-break: break-all; }
</style>
</head>
<body>
  <div class="header">
    <h1>LogAuditorGo 华为网络设备日志智能审计报告</h1>
    <div>任务名称: {{ .Task.TaskName }} | 设备类型: {{ .Task.DeviceType }} | 导出时间: {{ .ExportTime }}</div>
  </div>

  <div class="stats-grid">
    <div class="stat-card">
      <div class="val">{{ .Task.LogCount }}</div>
      <div class="label">总分析日志数</div>
    </div>
    <div class="stat-card" style="border-left-color: #10b981;">
      <div class="val">{{ .Task.MatchedCount }}</div>
      <div class="label">知识库匹配数</div>
    </div>
    <div class="stat-card" style="border-left-color: #f97316;">
      <div class="val">{{ len .RCAs }}</div>
      <div class="label">识别根因事件数</div>
    </div>
    <div class="stat-card" style="border-left-color: #8b5cf6;">
      <div class="val">{{ coveragePercent .Task.MatchedCount .Task.LogCount }}</div>
      <div class="label">官方知识覆盖率</div>
    </div>
  </div>
{{ if .RCAs }}
  <div class="section">
    <h2>🎯 根因分析（RCA）排查建议</h2>
{{ range .RCAs }}    <div class="rca-card">
      <div class="rca-title">💥 根因事件 [{{ .RootModule }}/{{ .RootBrief }}] - {{ .RootTimestamp }} (置信度: {{ formatConfidence .Confidence }})</div>
      <div>{{ .RootCauseSummary }}</div>
      <div class="rca-action"><strong>官方建议排查方案：</strong>{{ .RecommendedAction }}</div>
    </div>
{{ end }}  </div>
{{ end }}
  <div class="section">
    <h2>📋 日志分析明细 (前 100 条)</h2>
    <table>
      <thead>
        <tr>
          <th>时间戳</th>
          <th>主机名</th>
          <th>级别</th>
          <th>模块/事件</th>
          <th>原始报文</th>
          <th>知识库匹配</th>
        </tr>
      </thead>
      <tbody>
{{ range .Records }}        <tr>
          <td>{{ formatTime .Timestamp }}</td>
          <td>{{ .Hostname }}</td>
          <td><span class="badge {{ severityBadgeClass .Severity }}">{{ .Severity }}</span></td>
          <td><strong>{{ .Module }}</strong>/{{ .Brief }}</td>
          <td><div class="code">{{ .RawLog }}</div></td>
          <td>{{ matchBadgeHTML .KnowledgeID .MatchTier }}</td>
        </tr>
{{ end }}      </tbody>
    </table>
  </div>
</body>
</html>`

// GenerateHTMLReport 生成任务分析的独立 HTML 离线报告
func GenerateHTMLReport(task *model.TaskInfo, records []model.LogRecord, rcas []model.RCAEvent) string {
	if task == nil {
		return "<html><body><h3>Task not found</h3></body></html>"
	}

	displayRecords := records
	if len(displayRecords) > 100 {
		displayRecords = displayRecords[:100]
	}

	data := ReportViewModel{
		Task:       task,
		Records:    displayRecords,
		RCAs:       rcas,
		ExportTime: time.Now().Format("2006-01-02 15:04:05"),
	}

	var sb strings.Builder
	if err := reportTemplate.Execute(&sb, data); err != nil {
		return fmt.Sprintf("<html><body><h3>Render report failed: %s</h3></body></html>", template.HTMLEscapeString(err.Error()))
	}

	return sb.String()
}

