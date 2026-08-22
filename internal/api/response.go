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
		logger.Log.Errorf("[API Error 500] %s", message)
		message = "Internal Server Error"
	}
	c.JSON(httpStatus, gin.H{
		"code":    code,
		"message": message,
		"data":    nil,
	})
}
