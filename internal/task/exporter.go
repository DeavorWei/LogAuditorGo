package task

import (
	"fmt"
	"html"
	"strings"
	"time"

	"logauditorgo/internal/model"
)

// GenerateHTMLReport 生成任务分析的独立 HTML 离线报告
func GenerateHTMLReport(task *model.TaskInfo, records []model.LogRecord, rcas []model.RCAEvent) string {
	if task == nil {
		return "<html><body><h3>Task not found</h3></body></html>"
	}

	var sb strings.Builder

	taskName := html.EscapeString(task.TaskName)
	deviceType := html.EscapeString(task.DeviceType)

	coverageStr := "0.0%"
	if task.LogCount > 0 {
		coverageStr = fmt.Sprintf("%.1f%%", float64(task.MatchedCount)/float64(task.LogCount)*100)
	}

	sb.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>LogAuditorGo 审计分析报告 - ` + taskName + `</title>
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
    <div>任务名称: ` + taskName + ` | 设备类型: ` + deviceType + ` | 导出时间: ` + time.Now().Format("2006-01-02 15:04:05") + `</div>
  </div>

  <div class="stats-grid">
    <div class="stat-card">
      <div class="val">` + fmt.Sprintf("%d", task.LogCount) + `</div>
      <div class="label">总分析日志数</div>
    </div>
    <div class="stat-card" style="border-left-color: #10b981;">
      <div class="val">` + fmt.Sprintf("%d", task.MatchedCount) + `</div>
      <div class="label">知识库匹配数</div>
    </div>
    <div class="stat-card" style="border-left-color: #f97316;">
      <div class="val">` + fmt.Sprintf("%d", len(rcas)) + `</div>
      <div class="label">识别根因事件数</div>
    </div>
    <div class="stat-card" style="border-left-color: #8b5cf6;">
      <div class="val">` + coverageStr + `</div>
      <div class="label">官方知识覆盖率</div>
    </div>
  </div>
`)

	// 根因分析部分
	if len(rcas) > 0 {
		sb.WriteString(`  <div class="section">
    <h2>🎯 根因分析（RCA）排查建议</h2>
`)
		for _, rca := range rcas {
			rcaModule := html.EscapeString(rca.RootModule)
			rcaBrief := html.EscapeString(rca.RootBrief)
			rcaTime := html.EscapeString(rca.RootTimestamp)
			rcaSummary := html.EscapeString(rca.RootCauseSummary)
			rcaAction := html.EscapeString(rca.RecommendedAction)

			sb.WriteString(`    <div class="rca-card">
      <div class="rca-title">💥 根因事件 [` + rcaModule + `/` + rcaBrief + `] - ` + rcaTime + ` (置信度: ` + fmt.Sprintf("%.0f%%", rca.Confidence*100) + `)</div>
      <div>` + rcaSummary + `</div>
      <div class="rca-action"><strong>官方建议排查方案：</strong>` + rcaAction + `</div>
    </div>
`)
		}
		sb.WriteString(`  </div>
`)
	}

	// 结构化日志表格
	sb.WriteString(`  <div class="section">
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
`)

	limit := len(records)
	if limit > 100 {
		limit = 100
	}
	for i := 0; i < limit; i++ {
		r := records[i]
		sevClass := "sev-info"
		if r.Severity <= 2 {
			sevClass = "sev-crit"
		} else if r.Severity <= 4 {
			sevClass = "sev-err"
		} else if r.Severity <= 5 {
			sevClass = "sev-warn"
		}

		matchBadge := `<span class="badge" style="background:#f1f5f9; color:#64748b;">未匹配</span>`
		if r.KnowledgeID > 0 {
			matchBadge = `<span class="badge" style="background:#dcfce7; color:#166534;">已匹配 (` + html.EscapeString(r.MatchTier) + `)</span>`
		}

		sb.WriteString(`        <tr>
          <td>` + html.EscapeString(r.Timestamp.Format("2006-01-02 15:04:05")) + `</td>
          <td>` + html.EscapeString(r.Hostname) + `</td>
          <td><span class="badge ` + sevClass + `">` + fmt.Sprintf("%d", r.Severity) + `</span></td>
          <td><strong>` + html.EscapeString(r.Module) + `</strong>/` + html.EscapeString(r.Brief) + `</td>
          <td><div class="code">` + html.EscapeString(r.RawLog) + `</div></td>
          <td>` + matchBadge + `</td>
        </tr>
`)
	}

	sb.WriteString(`      </tbody>
    </table>
  </div>
</body>
</html>`)

	return sb.String()
}
