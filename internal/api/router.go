package api

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
	"logauditorgo/internal/task"
	"logauditorgo/web"
)

// SetupRouter 组装 Gin 路由与中间件
func SetupRouter(
	cfg *config.Config,
	globalDB *gorm.DB,
	knowledgeSvc *knowledge.Service,
	indexer *search.Indexer,
	taskSvc *task.Service,
) *gin.Engine {
	if cfg == nil || globalDB == nil || knowledgeSvc == nil || indexer == nil || taskSvc == nil {
		panic("nil pointer passed to SetupRouter")
	}

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// CORS 跨域支持中间件
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, c.Request.Host) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Add("Vary", "Origin")
		} else {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	docHandler := NewDocumentHandler(knowledgeSvc, cfg.Storage.UploadDir)
	knowHandler := NewKnowledgeHandler(knowledgeSvc, indexer)
	taskHandler := NewTaskHandler(taskSvc, knowledgeSvc, cfg.Storage.UploadDir)
	statsHandler := NewStatsHandler(globalDB)
	systemHandler := NewSystemHandler()
	progressHandler := NewProgressHandler()

	v1 := r.Group("/api/v1")
	{
		// 全流程阶段进度实时追踪 (SSE 流与 HTTP 轮询)
		v1.GET("/progress/:job_id", progressHandler.GetProgress)
		v1.GET("/progress/:job_id/stream", progressHandler.StreamProgress)

		// 系统统计与系统配置
		v1.GET("/system/stats", statsHandler.GetSystemStats)
		v1.GET("/system/config", systemHandler.GetConfig)
		v1.PUT("/system/config/log", systemHandler.UpdateLogConfig)
		v1.POST("/system/config/log", systemHandler.UpdateLogConfig)
		v1.GET("/system/logs", systemHandler.GetLogs)
		v1.POST("/system/logs/clean", systemHandler.CleanLogs)

		// 文档管理
		v1.POST("/documents/import-dir", docHandler.ImportDir)
		v1.POST("/documents/upload", docHandler.UploadHDX)
		v1.GET("/documents", docHandler.ListDocuments)
		v1.DELETE("/documents/:id", docHandler.DeleteDocument)

		// 知识库
		v1.GET("/knowledge/search", knowHandler.SearchKnowledge)
		v1.GET("/knowledge/:id", knowHandler.GetKnowledgeDetail)

		// 任务分析
		v1.POST("/tasks", taskHandler.CreateTask)
		v1.GET("/tasks", taskHandler.ListTasks)
		v1.GET("/tasks/:id", taskHandler.GetTask)
		v1.GET("/tasks/:id/files", taskHandler.GetTaskFiles)
		v1.POST("/tasks/:id/import", taskHandler.ImportLogs)
		v1.GET("/tasks/:id/logs", taskHandler.QueryLogs)
		v1.GET("/tasks/:id/modules", taskHandler.GetTaskModules)
		v1.POST("/tasks/:id/reanalyze", taskHandler.ReanalyzeTask)
		v1.GET("/tasks/:id/rca", taskHandler.GetRCA)
		v1.GET("/tasks/:id/export", taskHandler.ExportReport)
		v1.DELETE("/tasks/:id", taskHandler.DeleteTask)

		// 设备管理
		v1.POST("/tasks/:id/devices", taskHandler.CreateDevice)
		v1.GET("/tasks/:id/devices", taskHandler.ListDevices)
		v1.GET("/tasks/:id/devices/:device_id", taskHandler.GetDevice)
		v1.PUT("/tasks/:id/devices/:device_id", taskHandler.UpdateDevice)
		v1.DELETE("/tasks/:id/devices/:device_id", taskHandler.DeleteDevice)
		v1.POST("/tasks/:id/devices/:device_id/import", taskHandler.ImportLogsToDevice)
		v1.POST("/tasks/:id/devices/auto-assign", taskHandler.AutoAssignDevices)

		// 多设备时间线与协同分析
		v1.POST("/tasks/:id/multi-device/logs", taskHandler.QueryMultiDeviceLogs)
		v1.POST("/tasks/:id/multi-device/timeline", taskHandler.GetDeviceTimeline)
		v1.POST("/tasks/:id/multi-device/report", taskHandler.GetMultiDeviceReport)
		v1.GET("/tasks/:id/multi-device/export", taskHandler.ExportMultiDeviceReport)
	}

	// 静态前端资源与 SPA 路由托管 (基于 Go embed.FS 纯单二进制打包)
	distFS := web.DistFS()
	fileServer := http.FileServer(http.FS(distFS))

	indexFile, err := distFS.Open("index.html")
	var indexContent []byte
	if err == nil {
		indexContent, _ = io.ReadAll(indexFile)
		_ = indexFile.Close()
	}

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// 如果是 API 请求，返回 404 JSON
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    404,
				"message": "API endpoint not found",
			})
			return
		}

		// 检查是否是请求具体的静态资源文件 (例如 /assets/xxx.js, /favicon.ico 等)
		cleanPath := strings.TrimPrefix(path, "/")
		if cleanPath != "" {
			f, err := distFS.Open(cleanPath)
			if err == nil {
				stat, err := f.Stat()
				_ = f.Close()
				if err == nil && !stat.IsDir() {
					fileServer.ServeHTTP(c.Writer, c.Request)
					return
				}
			}
		}

		// 如果请求根路径或前端路由 (如 /workbench, /tasks 等)，且存在打包的 index.html，则返回 SPA 页面
		if len(indexContent) > 0 {
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexContent)
			return
		}

		// 降级提示（若未打包前端）
		c.JSON(http.StatusOK, gin.H{
			"name":    "LogAuditorGo API Server",
			"status":  "running",
			"version": "v1.0.0",
			"hint":    "Frontend static files not found. Please build web project before compiling.",
		})
	})

	return r
}

// isAllowedOrigin 校验跨域来源是否属于受信本地回环域或匹配服务端主机
func isAllowedOrigin(origin, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" {
		return true
	}
	if host != "" {
		hostOnly := host
		if h, _, err := net.SplitHostPort(host); err == nil {
			hostOnly = h
		}
		if strings.EqualFold(hostname, hostOnly) {
			return true
		}
	}
	return false
}
