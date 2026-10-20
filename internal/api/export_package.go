package api

import (
	"archive/zip"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/model"
	"logauditorgo/internal/version"
	"logauditorgo/pkg/logger"
)

// GetSummaryMarkdown 返回可复制的 Markdown 交接摘要（不脱敏）。
func (h *TaskHandler) GetSummaryMarkdown(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	markdown, err := h.taskSvc.GenerateSummaryMarkdown(taskID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		ErrorResponse(c, status, -1, err.Error())
		return
	}
	c.Header("Content-Type", "text/markdown; charset=utf-8")
	c.String(http.StatusOK, markdown)
}

// ExportEvidencePackage 以流式 ZIP 下发证据包：
// report.html + logs.csv + metadata.json + README.txt。
//
// 设计约束（方案审视 §3.3）：不落临时文件、不组装整包到内存；
// zip.Writer 直接写 HTTP 响应，CSV 复用流式游标。
func (h *TaskHandler) ExportEvidencePackage(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	task, err := h.taskSvc.GetTaskByID(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Task not found")
		return
	}

	// 先完成可失败的渲染，再写响应头，避免"已经 200 了才返回 JSON 错误"。
	htmlReport, err := h.taskSvc.ExportTaskHTMLUnified(taskID, model.LogQueryRequestBody{})
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, "Render report failed: "+err.Error())
		return
	}
	summary, summaryErr := h.taskSvc.GetDiagnosisSummary(taskID)
	if summaryErr != nil {
		logger.Log.Warnf("[API Tasks] evidence package summary failed: %v", summaryErr)
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", attachmentHeader(task.TaskName, "证据包", "zip"))
	c.Header("Cache-Control", "no-store")

	zw := zip.NewWriter(c.Writer)
	defer func() {
		if err := zw.Close(); err != nil {
			logger.Log.Warnf("[API Tasks] close evidence zip failed: %v", err)
		}
	}()

	// 1. HTML 报告
	if w, err := zw.Create("report.html"); err == nil {
		_, _ = w.Write([]byte(htmlReport))
	}

	// 2. 全量日志 CSV（UTF-8 BOM + 与在线导出同列）
	if w, err := zw.Create("logs.csv"); err == nil {
		_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
		_, _ = w.Write([]byte("ID,时间,级别,模块,助记符,主机名,来源文件,匹配层级,置信度,标签,原始报文\n"))
		streamErr := h.taskSvc.StreamTaskLogsUnified(taskID, model.LogQueryRequestBody{SortBy: "time", Order: "asc"}, func(rec model.LogRecord) error {
			_, writeErr := w.Write([]byte(buildCSVRow(rec)))
			return writeErr
		})
		if streamErr != nil {
			logger.Log.Errorf("[API Tasks] evidence package CSV stream failed: %v", streamErr)
		}
	}

	// 3. 元数据
	if w, err := zw.Create("metadata.json"); err == nil {
		metadata := gin.H{
			"app":         "LogAuditorGo",
			"version":     version.String,
			"export_time": time.Now().Format(time.RFC3339),
			"task":        task,
			"summary":     summary,
			"note":        "本证据包包含原始日志内容，不做脱敏；外发前请自行评估敏感性。",
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(metadata)
	}

	// 4. 说明文件
	if w, err := zw.Create("README.txt"); err == nil {
		_, _ = w.Write([]byte("LogAuditorGo 证据包\n" +
			"====================\n\n" +
			"包含内容：\n" +
			"  report.html   - 可离线打开的排障分析报告（含结论、关键证据、未匹配与处置建议）\n" +
			"  logs.csv      - 全量日志明细（UTF-8 BOM，Excel 可直接打开）\n" +
			"  metadata.json - 任务元信息、诊断摘要与导出版本\n" +
			"  README.txt    - 本说明\n\n" +
			"注意：证据包包含原始日志内容，本产品不做脱敏；对外发送前请自行评估敏感性。\n"))
	}
}
