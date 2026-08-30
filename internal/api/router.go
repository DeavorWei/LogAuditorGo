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
	"logauditorgo/internal/fsx"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
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

	// ARCH-15: 原实现仅在 mode == "release" 时切换到 ReleaseMode，
	// 配置写成 prod / 空值 / 大小写不一致时会停留在 debug 模式，
	// 向 stdout 打印全部路由注册噪声且丢失 release 下的若干优化。
	// 这里显式 switch，未知值一律按 release 处理并告警。
	switch strings.ToLower(strings.TrimSpace(cfg.Server.Mode)) {
	case "debug":
		gin.SetMode(gin.DebugMode)
	case "test":
		gin.SetMode(gin.TestMode)
	case "release":
		gin.SetMode(gin.ReleaseMode)
	default:
		gin.SetMode(gin.ReleaseMode)
		logger.Log.Warnf("[Router] unknown server.mode=%q, falling back to release mode", cfg.Server.Mode)
	}

	r := gin.New()

	// ARCH-02: 装配全局路径白名单守卫，三个导入入口共用同一实例
	pathGuard = fsx.NewSecurePathGuard(cfg.Storage.AllowedRoots)
	if pathGuard.Enabled() {
		logger.Log.Infof("[Router] Path guard enabled, allowed roots: %v", pathGuard.Roots())
	}

	// ARCH-06: RequestID 必须在所有中间件之前注册，
	// 这样后续中间件与 handler 产生的 5xx 都能带上同一个可追溯 ID。
	r.Use(RequestIDMiddleware())

	// ARCH-01: 显式关闭对 X-Forwarded-For / X-Real-IP 的信任。
	// gin 默认信任所有代理，会让基于 IP 的访问控制（如 RequireLoopback）可被请求头伪造绕过。
	// 本工具是本地单机服务，不存在前置代理，直接禁用最安全。
	if err := r.SetTrustedProxies(nil); err != nil {
		logger.Log.Warnf("[Router] Disable trusted proxies failed: %v", err)
	}

	// ARCH-09: 默认 gin.Recovery() 只把 panic 栈打到 stderr，落不到日志轮转文件，
	// 离线工具出问题时无法回溯。这里改为写入 zap，并附带请求方法与路径。
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		logger.Log.Errorf("[Router] Panic recovered: %v | %s %s", recovered, c.Request.Method, c.Request.URL.Path)
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	// CORS 跨域支持中间件
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		// ARCH-08: 原实现在来源不被允许时兜底回 `Access-Control-Allow-Origin: *`，
		// 等于对任意网站放开了本服务全部数据（任务、知识库、配置）的读写。
		// 未通过校验的来源直接不设置 ACAO 头，浏览器会按同源策略拦截响应。
		if origin != "" && isAllowedOrigin(origin, c.Request.Host) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Add("Vary", "Origin")
		}
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	docHandler := NewDocumentHandler(knowledgeSvc)
	knowHandler := NewKnowledgeHandler(knowledgeSvc, indexer)
	taskHandler := NewTaskHandler(taskSvc, knowledgeSvc)
	statsHandler := NewStatsHandler(globalDB)
	systemHandler := NewSystemHandler()
	progressHandler := NewProgressHandler()

	// ARCH-11: 暴露统计处理器引用，供导入/删除等业务变更主动失效缓存，
	// 避免仪表盘停留在最多 15s 前的陈旧数据。
	globalStatsHandler = statsHandler
	fsHandler := NewFSHandler(fsx.Root{Name: "数据存储目录", Path: cfg.Storage.DataDir})

	v1 := r.Group("/api/v1")
	{
		// 服务端本地文件系统只读浏览（仅限本机回环访问）
		fs := v1.Group("/fs")
		fs.Use(RequireLoopback())
		{
			fs.GET("/roots", fsHandler.GetRoots)
			fs.GET("/browse", fsHandler.Browse)
			fs.POST("/stat", fsHandler.Stat)
		}

		// 全流程阶段进度实时追踪 (SSE 流与 HTTP 轮询) 与长任务终止 (UI-02)
		v1.GET("/progress/:job_id", progressHandler.GetProgress)
		v1.GET("/progress/:job_id/stream", progressHandler.StreamProgress)
		v1.DELETE("/progress/:job_id", progressHandler.CancelProgress)

		// 系统统计与系统配置
		v1.GET("/system/stats", statsHandler.GetSystemStats)
		v1.GET("/system/config", systemHandler.GetConfig)
		v1.PUT("/system/config/log", systemHandler.UpdateLogConfig)
		v1.POST("/system/config/log", systemHandler.UpdateLogConfig)
		v1.GET("/system/logs", systemHandler.GetLogs)
		v1.POST("/system/logs/clean", systemHandler.CleanLogs)
		// KB-01: 索引健康状态（静态路径，避免与 /knowledge/:id 通配路由冲突）
		v1.GET("/system/knowledge-index/status", knowHandler.GetIndexStatus)

		// 文档管理（服务端本地路径导入与目录智能扫描）
		v1.POST("/documents/scan", docHandler.ScanDir)
		v1.POST("/documents/import-dir", docHandler.ImportDir)
		v1.GET("/documents", docHandler.ListDocuments)
		v1.DELETE("/documents/:id", docHandler.DeleteDocument)
		v1.POST("/documents/batch-delete", docHandler.BatchDeleteDocuments)

		// 知识库
		v1.GET("/knowledge/search", knowHandler.SearchKnowledge)
		v1.GET("/knowledge/:id", knowHandler.GetKnowledgeDetail)
		// KB-01: 索引重建入口。默认走异步模式，返回 job_id 由前端挂进度弹窗
		v1.POST("/knowledge/reindex", knowHandler.RebuildIndex)

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
			// ARCH-10: 补全 data 字段，保证与 SuccessResponse/ErrorResponse 同一契约
			c.JSON(http.StatusNotFound, gin.H{
				"code":    404,
				"message": "API endpoint not found",
				"data":    nil,
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
