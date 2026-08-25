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

// StreamProgress 基于 SSE (Server-Sent Events) 实时推送任务全阶段进度
func (h *ProgressHandler) StreamProgress(c *gin.Context) {
	jobID := c.Param("job_id")
	tracker := h.hub.GetJob(jobID)
	if tracker == nil {
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
