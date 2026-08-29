package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"logauditorgo/internal/fsx"
)

// maxStatPaths 单次路径校验的最大条数
const maxStatPaths = 500

// FSHandler 服务端本地文件系统只读浏览
type FSHandler struct {
	shortcuts []fsx.Root
}

func NewFSHandler(shortcuts ...fsx.Root) *FSHandler {
	return &FSHandler{shortcuts: shortcuts}
}

// GetRoots 获取可浏览的根目录与常用快捷入口
func (h *FSHandler) GetRoots(c *gin.Context) {
	SuccessResponse(c, fsx.Roots(h.shortcuts...))
}

// Browse 分页浏览指定目录
func (h *FSHandler) Browse(c *gin.Context) {
	path := strings.TrimSpace(c.Query("path"))
	if path == "" {
		ErrorResponse(c, http.StatusBadRequest, -1, "query param 'path' is required")
		return
	}

	offset, _ := strconv.Atoi(c.Query("offset"))
	limit, _ := strconv.Atoi(c.Query("limit"))

	res, err := fsx.Browse(fsx.BrowseOptions{
		Path:     path,
		Exts:     parseExts(c.Query("exts")),
		Keyword:  c.Query("keyword"),
		DirsOnly: c.Query("dirs_only") == "true",
		Offset:   offset,
		Limit:    limit,
	})
	if err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, err.Error())
		return
	}
	SuccessResponse(c, res)
}

// Stat 批量校验路径是否存在及其类型与大小
func (h *FSHandler) Stat(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, -1, "Invalid request: "+err.Error())
		return
	}
	if len(req.Paths) == 0 {
		ErrorResponse(c, http.StatusBadRequest, -1, "paths is required")
		return
	}
	if len(req.Paths) > maxStatPaths {
		ErrorResponse(c, http.StatusBadRequest, -1, "too many paths, limit is "+strconv.Itoa(maxStatPaths))
		return
	}
	SuccessResponse(c, gin.H{"entries": fsx.Stat(req.Paths)})
}

// RequireLoopback 限制文件系统浏览仅允许本机回环访问。
// 由于全局 CORS 允许任意来源，若不加以限制，任意网页都可以通过
// 用户浏览器读取其本地磁盘文件。
func RequireLoopback() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := net.ParseIP(c.ClientIP())
		if ip == nil || !ip.IsLoopback() {
			ErrorResponse(c, http.StatusForbidden, -1, "filesystem browsing is only allowed from localhost")
			c.Abort()
			return
		}
		c.Next()
	}
}

// parseExts 解析逗号分隔的扩展名列表
func parseExts(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	exts := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			exts = append(exts, p)
		}
	}
	if len(exts) == 0 {
		return nil
	}
	return exts
}
