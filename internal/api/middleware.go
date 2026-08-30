package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"logauditorgo/internal/fsx"
	"logauditorgo/pkg/logger"
)

// requestIDKey 是 gin 上下文中存放 RequestID 的键名
const requestIDKey = "X-Request-ID"

// pathGuard 全局路径白名单守卫，由 SetupRouter 依据配置装配。
// 三个导入入口共用同一个实例，避免"各处各写一遍、漏一处即失效"。
var pathGuard *fsx.SecurePathGuard

// RequestIDMiddleware 为每一个请求生成 RequestID 并注入响应头 (ARCH-06)。
//
// 有了 RequestID 才能做到"5xx 对外脱敏、对内可追溯"：
// 客户端只拿到固定文案 + 一个 ID，运维可凭该 ID 在服务端日志中精确定位完整堆栈。
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDKey)
		if id == "" {
			id = uuid.New().String()
		}
		c.Set(requestIDKey, id)
		c.Writer.Header().Set(requestIDKey, id)
		c.Next()
	}
}

// RequestIDFrom 从 gin 上下文中取出 RequestID（缺失时返回兜底值）
func RequestIDFrom(c *gin.Context) string {
	if c == nil {
		return "-"
	}
	if v, ok := c.Get(requestIDKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	if s := c.GetHeader(requestIDKey); s != "" {
		return s
	}
	return "-"
}

// guardPaths 用全局路径白名单守卫批量校验路径。
// 校验失败时直接写 403 并中断请求，返回 false。
func guardPaths(c *gin.Context, paths []string) bool {
	if pathGuard == nil || !pathGuard.Enabled() {
		return true
	}
	if err := pathGuard.ValidateAll(paths); err != nil {
		logger.Log.Warnf("[API] Path guard rejected request %s %s (request_id=%s): %v",
			c.Request.Method, c.Request.URL.Path, RequestIDFrom(c), err)
		ErrorResponse(c, http.StatusForbidden, -1, err.Error())
		c.Abort()
		return false
	}
	return true
}
