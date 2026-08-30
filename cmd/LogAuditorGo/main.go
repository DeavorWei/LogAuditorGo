package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"logauditorgo/internal/api"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
	"logauditorgo/pkg/progress"
)

// HTTP 服务器超时参数 (ARCH-05)。
//
// 原实现 `&http.Server{Addr, Handler}`，没有任何超时设置：
// 慢速连接会长期占用 goroutine（Slowloris 型资源耗尽），
// SSE 长连接也没有空闲边界。
//
// 注意 WriteTimeout 必须为 0：进度流（SSE）与文档导入（单次可达数分钟）
// 都需要长时间持有连接，设置写超时会直接把它们切断。
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	idleTimeout       = 120 * time.Second
	writeTimeout      = 0 // SSE 与长耗时导入需要，禁止设置写超时
	shutdownTimeout   = 15 * time.Second
)

func main() {
	// ARCH-20: 用 flag 暴露配置路径与端口，支持便携部署与脚本化启停。
	// 优先级：命令行参数 > 配置文件 > 默认值。
	configPath := flag.String("config", "", "指定配置文件路径 (默认 <data_dir>/config.yaml)")
	portFlag := flag.Int("port", 0, "覆盖配置文件中的监听端口")
	showVersion := flag.Bool("version", false, "打印版本信息并退出")
	flag.Parse()

	if *showVersion {
		fmt.Println("LogAuditorGo v1.0.0")
		return
	}

	// 1. 初始化配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}
	if *portFlag > 0 {
		if *portFlag > 0 && *portFlag <= 65535 {
			cfg.Server.Port = *portFlag
		} else {
			fmt.Printf("Invalid port %d, must be within 1-65535\n", *portFlag)
			os.Exit(1)
		}
	}

	// 2. 初始化日志记录器并注册配置同步钩子
	log := logger.InitWithConfig(logger.Config{
		Level:     cfg.Log.Level,
		Format:    cfg.Log.Format,
		Dir:       cfg.Log.Dir,
		MaxSizeMB: cfg.Log.MaxSizeMB,
		MaxDays:   cfg.Log.MaxDays,
	})
	// ARCH-13: format 形参此前从未被使用，导致"改了日志格式却不生效"。
	// 这里真正接上运行时切换，并额外支持动态端口之外的留存策略热更新。
	config.RegisterLogUpdateHook(func(maxSizeMB, maxDays int, level, format string) {
		logger.UpdatePolicy(maxSizeMB, maxDays)
		if level != "" {
			logger.SetLevel(level)
		}
		if format != "" {
			logger.SetFormat(format)
		}
	})
	log.Infof("Starting LogAuditorGo server on port %d...", cfg.Server.Port)
	log.Infof("File logging active at dir: %s (Max: %dMB, Retain: %ddays)", cfg.Log.Dir, cfg.Log.MaxSizeMB, cfg.Log.MaxDays)

	// 3. 初始化全局知识库 SQLite
	globalDB, err := storage.InitKnowledgeDB(cfg.Storage.KnowledgeDB)
	if err != nil {
		log.Fatalf("Failed to initialize knowledge DB: %v", err)
	}

	// 4. 初始化 Bleve 全文搜索引擎
	indexer, err := search.InitIndexer(cfg.Storage.BleveIndex)
	if err != nil {
		log.Fatalf("Failed to initialize Bleve indexer: %v", err)
	}

	// 5. 初始化业务引擎
	knowledgeSvc := knowledge.NewService(globalDB, indexer)
	knowledgeSvc.SetExtractDir(cfg.Storage.UploadDir)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	knowledgeSvc.SetMatchEngine(matchEngine)
	rcaEngine := rootcause.NewEngine()
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rcaEngine)

	// 6. 初始化并启动 HTTP 服务
	r := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)
	addr := fmt.Sprintf(":%d", cfg.Server.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		WriteTimeout:      writeTimeout,
	}

	// quit 通道提升到 main 作用域，供启动 goroutine 在 ListenAndServe 失败时通知主流程。
	//
	// ARCH-04: 原实现在 goroutine 内直接 `log.Fatalf(...)`，
	// 端口占用等启动失败会跳过全部资源释放：Bleve 索引未 Close，
	// SQLite 未 Close，`defer cancel()` 也不执行。
	// Fatal 在 goroutine 里还会绕过所有已注册的 defer，是典型的资源泄漏点。
	quit := make(chan os.Signal, 1)
	// ARCH-20: os.Interrupt 在 Unix 上就是 SIGINT，原先同时注册了三者属于重复项
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	serverErrCh := make(chan error, 1)
	go func() {
		log.Infof("Server listening on http://localhost%s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("HTTP server listen error: %v", err)
			serverErrCh <- err
			// 通知主流程进入优雅关闭，而不是直接 Fatal 退出
			select {
			case quit <- syscall.SIGTERM:
			default:
			}
			return
		}
		serverErrCh <- nil
	}()

	// 7. 等待终止信号（或 HTTP 启动失败）
	sig := <-quit
	log.Infof("Shutting down server gracefully (signal: %v)...", sig)

	// 8. 设置 15 秒优雅关闭超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Errorf("Server forced to shutdown: %v", err)
	} else {
		log.Info("HTTP server stopped cleanly")
	}

	// 9. 释放后台任务与存储资源
	//     顺序与依赖方向一致：进度中心 → 任务库 → Bleve → 主库 → 日志
	progress.GetHub().Stop()
	log.Info("Progress hub stopped")

	storage.CloseAllTaskDBs()
	log.Info("All task SQLite databases closed cleanly")

	if err := indexer.Close(); err != nil {
		log.Errorf("Failed to close Bleve indexer: %v", err)
	} else {
		log.Info("Bleve indexer closed")
	}

	// KB-17: 关闭前先做一次 WAL checkpoint，把日志合并回主库
	if err := storage.CloseKnowledgeDB(); err != nil {
		log.Errorf("Failed to close database: %v", err)
	} else {
		log.Info("Database connection closed")
	}

	// ARCH-18: 最后释放日志子系统（清理协程 + 文件句柄）
	logger.Shutdown()

	log.Info("LogAuditorGo exited cleanly")

	// 若 HTTP 层启动即失败，需要以非零退出码告知调用方（systemd / 脚本依赖）
	if err := <-serverErrCh; err != nil {
		os.Exit(1)
	}
}
