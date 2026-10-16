package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	"logauditorgo/internal/tray"
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
	hostFlag := flag.String("host", "", "覆盖配置文件中的监听主机地址 (默认 127.0.0.1)")
	portFlag := flag.Int("port", 0, "覆盖配置文件中的监听端口 (1-65535)")
	showVersion := flag.Bool("version", false, "打印版本信息并退出")
	noTray := flag.Bool("no-tray", false, "禁用系统托盘图标 (适用于无头服务器环境)")
	trayTheme := flag.String("tray-theme", "blue_shield", "托盘图标风格: blue_shield (默认蓝盾), emerald_terminal (翠绿终端), indigo_radar (星穹雷达)")
	iconPath := flag.String("icon", "", "自定义托盘图标文件路径 (.ico 或 .png)")
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
	if *hostFlag != "" {
		cfg.Server.Host = *hostFlag
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if *portFlag != 0 {
		if *portFlag >= 1 && *portFlag <= 65535 {
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

	// 3. 注册 SQLite 自定义函数并初始化全局知识库
	storage.RegisterSQLiteFunctions()
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
	if err := taskSvc.RecoverDanglingTasks(); err != nil {
		log.Warnf("Failed to recover dangling tasks on startup: %v", err)
	}
	if err := taskSvc.RecoverDanglingRCATasks(); err != nil {
		log.Warnf("Failed to recover dangling RCA tasks on startup: %v", err)
	}

	// 6. 初始化并启动 HTTP 服务
	r := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		WriteTimeout:      writeTimeout,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// 显式先执行 TCP 监听，确保端口绑定成功后再触发就绪信号与托盘通知 (N2 / ISSUE-CMD-03)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Errorf("HTTP server listen error on %s: %v", addr, err)
		os.Exit(1)
	}

	serverErrCh := make(chan error, 1)
	readyCh := make(chan struct{})
	close(readyCh)
	log.Infof("Server listening on http://%s", addr)

	notifyCtx, notifyCancel := context.WithCancel(context.Background())
	defer notifyCancel()

	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("HTTP server serve error: %v", err)
			serverErrCh <- err
			select {
			case quit <- syscall.SIGTERM:
			default:
			}
			return
		}
		serverErrCh <- nil
	}()

	// 7. 初始化并启动系统托盘图标 (Windows)
	var systemTray tray.Tray
	if !*noTray {
		systemTray = tray.New(tray.Config{
			Port:     cfg.Server.Port,
			LogDir:   cfg.Log.Dir,
			DataDir:  filepath.Dir(cfg.Storage.KnowledgeDB),
			Version:  "v1.0.0",
			Theme:    tray.IconTheme(*trayTheme),
			IconPath: *iconPath,
			OnExit: func() {
				log.Info("Exit requested from system tray menu")
				select {
				case quit <- syscall.SIGTERM:
				default:
				}
			},
		})

		go func() {
			if err := systemTray.Start(); err != nil {
				log.Warnf("System tray failed to start: %v", err)
			}
		}()

		// 启动就绪后延迟弹出 Windows 原生气泡通知（使用独立 notifyCtx，杜绝抢占 quit 退出信号）
		go func() {
			select {
			case <-readyCh:
				select {
				case <-time.After(300 * time.Millisecond):
					systemTray.ShowNotification(
						"LogAuditorGo 服务已就绪",
						fmt.Sprintf("日志审计系统正在后台运行 (地址: %s)\n点击托盘图标可快速打开控制台", addr),
					)
				case <-notifyCtx.Done():
					return
				}
			case <-notifyCtx.Done():
				return
			}
		}()
	}

	// 8. 等待终止信号（或 HTTP 启动失败）
	sig := <-quit
	notifyCancel()
	log.Infof("Shutting down server gracefully (signal: %v)...", sig)

	// 退出时安全卸载托盘图标
	if systemTray != nil {
		systemTray.Stop()
	}

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
