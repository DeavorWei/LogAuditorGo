package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"logauditorgo/pkg/logger"
)

// SuccessResponse 标准成功响应
func SuccessResponse(c *gin.Context, data interface{}, message ...string) {
	msg := "success"
	if len(message) > 0 {
		msg = message[0]
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": msg,
		"data":    data,
	})
}

// ErrorResponse 标准错误响应
//
// ARCH-06: 原实现把内部错误原文（`err.Error()`）原样拼进响应体，
// SQL 语句、表结构、服务端绝对路径等内部细节会直接泄露给客户端。
// 现在 5xx 一律只回固定文案 + request_id，完整错误只写服务端日志；
// 4xx 属于客户端可修正的错误，仍返回具体原因以便用户自助处理。
func ErrorResponse(c *gin.Context, httpStatus int, code int, message string) {
	if httpStatus >= 500 {
		requestID := RequestIDFrom(c)
		// 完整堆栈与上下文只进日志，绝不外泄
		logger.Log.Errorf("[API Error %d] request_id=%s | %s %s | %s",
			httpStatus, requestID, requestMethodPath(c), "", message)
		message = "Internal Server Error"
		if requestID != "" && requestID != "-" {
			message = "Internal Server Error (request_id: " + requestID + ")"
		}
	}
	c.JSON(httpStatus, gin.H{
		"code":    code,
		"message": message,
		"data":    nil,
	})
}

// requestMethodPath 安全地拼出 "METHOD path"，供日志使用
func requestMethodPath(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "-"
	}
	return c.Request.Method + " " + c.Request.URL.Path
}
