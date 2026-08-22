package api

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/search"
	"logauditorgo/internal/task"
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
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
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
	taskHandler := NewTaskHandler(taskSvc, knowledgeSvc)
	statsHandler := NewStatsHandler(globalDB)

	v1 := r.Group("/api/v1")
	{
		// 系统统计
		v1.GET("/system/stats", statsHandler.GetSystemStats)

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
		v1.GET("/tasks/:id/rca", taskHandler.GetRCA)
		v1.GET("/tasks/:id/export", taskHandler.ExportReport)
		v1.DELETE("/tasks/:id", taskHandler.DeleteTask)
	}

	// 静态前端文件托管 (如果 web/dist 存在)
	if _, err := os.Stat("web/dist"); err == nil {
		r.Static("/assets", "web/dist/assets")
		r.NoRoute(func(c *gin.Context) {
			c.File("web/dist/index.html")
		})
	} else {
		r.GET("/", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{
				"name":    "LogAuditorGo API Server",
				"status":  "running",
				"version": "v1.0.0",
			})
		})
	}

	return r
}
