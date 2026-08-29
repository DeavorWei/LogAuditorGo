package main

import (
	"context"
	"errors"
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

func main() {
	// 1. 初始化配置
	cfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志记录器并注册配置同步钩子
	log := logger.InitWithConfig(logger.Config{
		Level:     cfg.Log.Level,
		Format:    cfg.Log.Format,
		Dir:       cfg.Log.Dir,
		MaxSizeMB: cfg.Log.MaxSizeMB,
		MaxDays:   cfg.Log.MaxDays,
	})
	config.RegisterLogUpdateHook(func(maxSizeMB, maxDays int, level, format string) {
		logger.UpdatePolicy(maxSizeMB, maxDays)
		if level != "" {
			logger.SetLevel(level)
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
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Infof("Server listening on http://localhost%s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server listen error: %v", err)
		}
	}()

	// 7. 监听系统中断信号实现优雅关闭 (Graceful Shutdown)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	sig := <-quit
	log.Infof("Shutting down server gracefully (signal: %v)...", sig)

	// 8. 设置 15 秒优雅关闭超时上下文
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Errorf("Server forced to shutdown: %v", err)
	} else {
		log.Info("HTTP server stopped cleanly")
	}

	// 9. 释放后台任务与存储资源
	progress.GetHub().Stop()
	log.Info("Progress hub stopped")

	storage.CloseAllTaskDBs()
	log.Info("All task SQLite databases closed cleanly")

	if err := indexer.Close(); err != nil {
		log.Errorf("Failed to close Bleve indexer: %v", err)
	} else {
		log.Info("Bleve indexer closed")
	}

	if sqlDB, err := globalDB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			log.Errorf("Failed to close database: %v", err)
		} else {
			log.Info("Database connection closed")
		}
	}

	log.Info("LogAuditorGo exited cleanly")
}
