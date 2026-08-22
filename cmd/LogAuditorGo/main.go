package main

import (
	"fmt"
	"os"

	"logauditorgo/internal/api"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/pkg/logger"
)

func main() {
	// 1. 初始化配置
	cfg, err := config.Load("")
	if err != nil {
		fmt.Printf("Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志记录器
	log := logger.Init(cfg.Log.Level, cfg.Log.Format)
	log.Infof("Starting LogAuditorGo server on port %d...", cfg.Server.Port)

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
	defer indexer.Close()

	// 5. 初始化业务引擎
	knowledgeSvc := knowledge.NewService(globalDB, indexer)
	matchEngine := matcher.NewMatchEngine(globalDB, indexer)
	rcaEngine := rootcause.NewEngine(nil)
	taskSvc := task.NewService(globalDB, cfg.Storage.TaskDir, matchEngine, rcaEngine)

	// 6. 初始化并启动 HTTP 服务
	r := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Infof("Server listening on http://localhost%s", addr)

	if err := r.Run(addr); err != nil {
		log.Fatalf("HTTP server run error: %v", err)
	}
}
