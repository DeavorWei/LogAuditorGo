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
func ErrorResponse(c *gin.Context, httpStatus int, code int, message string) {
	if httpStatus >= 500 {
		logger.Log.Errorf("[API Error %d] %s", httpStatus, message)
		if message == "" {
			message = "Internal Server Error"
		} else {
			message = "Internal Server Error: " + message
		}
	}
	c.JSON(httpStatus, gin.H{
		"code":    code,
		"message": message,
		"data":    nil,
	})
}
