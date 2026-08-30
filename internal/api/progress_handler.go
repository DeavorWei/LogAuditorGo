package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"logauditorgo/pkg/progress"
)

type ProgressHandler struct {
	hub *progress.Hub
}

func NewProgressHandler() *ProgressHandler {
	return &ProgressHandler{
		hub: progress.GetHub(),
	}
}

// GetProgress 获取任务当前进度快照 (HTTP 轮询模式)
func (h *ProgressHandler) GetProgress(c *gin.Context) {
	jobID := c.Param("job_id")
	tracker := h.hub.GetJob(jobID)
	if tracker == nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Progress job not found or expired")
		return
	}

	snap := tracker.GetSnapshot()
	SuccessResponse(c, snap)
}

// CancelProgress 终止一个正在运行的长耗时任务 (UI-02)。
//
// 取消是协作式的：这里只关闭任务的 context 并把状态置为 FAILED，
// 真正的停止由业务循环（导入 / 重分析 / 文档导入）检查 ctx.Done() 完成。
func (h *ProgressHandler) CancelProgress(c *gin.Context) {
	jobID := c.Param("job_id")
	tracker := h.hub.GetJob(jobID)
	if tracker == nil {
		ErrorResponse(c, http.StatusNotFound, -1, "Progress job not found or expired")
		return
	}

	if !h.hub.CancelJob(jobID, "用户主动终止任务") {
		// 任务已处于终态：返回 409 让前端知道无需再等，而不是 500
		ErrorResponse(c, http.StatusConflict, -1, "该任务已结束，无需终止")
		return
	}

	SuccessResponse(c, gin.H{"job_id": jobID, "canceled": true}, "已发送终止请求，任务将在当前阶段结束后停止")
}

// StreamProgress 基于 SSE (Server-Sent Events) 实时推送任务全阶段进度
func (h *ProgressHandler) StreamProgress(c *gin.Context) {
	jobID := c.Param("job_id")
	tracker := h.hub.GetJob(jobID)
	if tracker == nil {
		// SSE 流一旦开始就无法再用 JSON 错误体表达，只能在流式输出前拦截
		c.JSON(http.StatusNotFound, gin.H{
			"code":    404,
			"message": "Progress job not found or expired",
		})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := tracker.Subscribe()
	defer unsubscribe()

	c.Stream(func(w io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case snap, ok := <-ch:
			if !ok {
				return false
			}
			data, err := json.Marshal(snap)
			if err != nil {
				return true
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			// 如果任务已经完成或失败，通知客户端后可正常断开
			if snap.Status == progress.JobCompleted || snap.Status == progress.JobFailed {
				time.Sleep(100 * time.Millisecond)
				return false
			}
			return true
		}
	})
}
