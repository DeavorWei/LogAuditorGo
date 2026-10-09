package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/model"
	"logauditorgo/internal/task"
)

// ListTags 获取任务所有标签及计数
func (h *TaskHandler) ListTags(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	tags, err := h.taskSvc.ListTaskTags(taskID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, tags)
}

// CreateTag 创建标签
func (h *TaskHandler) CreateTag(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}

	var req model.TagCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}

	tag, err := h.taskSvc.CreateTaskTag(taskID, req)
	if err != nil {
		if errors.Is(err, task.ErrTagAlreadyExists) {
			ErrorResponse(c, http.StatusConflict, -1, "Tag with this name already exists")
			return
		}
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, tag)
}

// UpdateTag 修改标签
func (h *TaskHandler) UpdateTag(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	tagID, err := strconv.ParseUint(c.Param("tag_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid tag ID")
		return
	}

	var req model.TagUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}

	tag, err := h.taskSvc.UpdateTaskTag(taskID, uint(tagID), req)
	if err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		if errors.Is(err, task.ErrTagAlreadyExists) {
			ErrorResponse(c, http.StatusConflict, -1, "Tag with this name already exists")
			return
		}
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, tag)
}

// DeleteTag 删除标签
func (h *TaskHandler) DeleteTag(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	tagID, err := strconv.ParseUint(c.Param("tag_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid tag ID")
		return
	}

	if err := h.taskSvc.DeleteTaskTag(taskID, uint(tagID)); err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}
	SuccessResponse(c, gin.H{"deleted": true})
}

// BatchTagLogs 按高级筛选条件与简单条件组合批量打标
func (h *TaskHandler) BatchTagLogs(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	tagID, err := strconv.ParseUint(c.Param("tag_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid tag ID")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)

	var req model.LogQueryRequestBody
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			ErrorResponse(c, http.StatusBadRequest, -1, "Invalid filter body: "+err.Error())
			return
		}
	}

	taggedCount, matchedTotal, err := h.taskSvc.BatchTagLogs(taskID, uint(tagID), req)
	if err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		if errors.Is(err, task.ErrBatchLimitExceeded) {
			ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
			return
		}
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}

	SuccessResponse(c, gin.H{
		"tagged_count":  taggedCount,
		"matched_total": matchedTotal,
	})
}

// BatchUntagLogs 按筛选批量摘标
func (h *TaskHandler) BatchUntagLogs(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	tagID, err := strconv.ParseUint(c.Param("tag_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid tag ID")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)

	var req model.LogQueryRequestBody
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			ErrorResponse(c, http.StatusBadRequest, -1, "Invalid filter body: "+err.Error())
			return
		}
	}

	untaggedCount, err := h.taskSvc.BatchUntagLogs(taskID, uint(tagID), req)
	if err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		if errors.Is(err, task.ErrBatchLimitExceeded) {
			ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
			return
		}
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}

	SuccessResponse(c, gin.H{
		"untagged_count": untaggedCount,
	})
}

// AddLogTag 单条日志打标
func (h *TaskHandler) AddLogTag(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	logID, err := strconv.ParseUint(c.Param("log_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid log ID")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)

	var req model.SingleTagRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request body: "+err.Error())
		return
	}

	if err := h.taskSvc.AddLogTag(taskID, uint(logID), req.TagID); err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		if errors.Is(err, task.ErrLogNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Log record not found")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, gin.H{"tagged": true})
}

// RemoveLogTag 单条日志摘标
func (h *TaskHandler) RemoveLogTag(c *gin.Context) {
	taskID := c.Param("id")
	if !isValidTaskID(taskID) {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid task ID format")
		return
	}
	logID, err := strconv.ParseUint(c.Param("log_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid log ID")
		return
	}
	tagID, err := strconv.ParseUint(c.Param("tag_id"), 10, 32)
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid tag ID")
		return
	}

	if err := h.taskSvc.RemoveLogTag(taskID, uint(logID), uint(tagID)); err != nil {
		if errors.Is(err, task.ErrTagNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag not found")
			return
		}
		if errors.Is(err, task.ErrLogNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Log record not found")
			return
		}
		if errors.Is(err, task.ErrRelationNotFound) {
			ErrorResponse(c, http.StatusNotFound, -1, "Tag relation not found")
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, -1, err.Error())
		return
	}

	SuccessResponse(c, gin.H{"removed": true})
}
