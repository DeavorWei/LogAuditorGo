package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"logauditorgo/internal/api"
	"logauditorgo/internal/backup"
	"logauditorgo/internal/browser"
	"logauditorgo/internal/config"
	"logauditorgo/internal/knowledge"
	"logauditorgo/internal/matcher"
	"logauditorgo/internal/netutil"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/search"
	"logauditorgo/internal/singleinstance"
	"logauditorgo/internal/storage"
	"logauditorgo/internal/task"
	"logauditorgo/internal/tray"
	"logauditorgo/internal/version"
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
	noBrowser := flag.Bool("no-browser", false, "禁用程序启动时自动打开系统默认浏览器")
	trayTheme := flag.String("tray-theme", "blue_shield", "托盘图标风格: blue_shield (默认蓝盾), emerald_terminal (翠绿终端), indigo_radar (星穹雷达)")
	iconPath := flag.String("icon", "", "自定义托盘图标文件路径 (.ico 或 .png)")
	flag.Parse()

	if *showVersion {
		fmt.Println("LogAuditorGo " + version.String)
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

	// 2.5 单实例保护（规划 §4.3 / UX-17）
	//
	// 任务库是物理 SQLite 文件，双开会并发写同一份 WAL。这里在打开任何数据库
	// 之前抢占锁；第二个实例只负责把已在运行的 Web 控制台拉起来后退出。
	// 环境变量 LOGAUDITORGO_DISABLE_SINGLE_INSTANCE=1 可关闭该保护（调试/自动化）。
	var instanceLock *singleinstance.Lock
	if os.Getenv("LOGAUDITORGO_DISABLE_SINGLE_INSTANCE") != "1" {
		lock, lockErr := singleinstance.Acquire(cfg.Storage.DataDir)
		switch {
		case errors.Is(lockErr, singleinstance.ErrAlreadyRunning):
			openRunningInstance(cfg, *noBrowser)
			return
		case lockErr != nil:
			// 只读数据目录 / 权限受限时不阻断启动，仅告警
			log.Warnf("单实例保护初始化失败（继续启动，双开可能损坏数据）: %v", lockErr)
		default:
			instanceLock = lock
			defer instanceLock.Release()
		}
	}

	// 2.6 应用待处理的数据恢复（v1.3）
	//
	// 恢复包由设置页准备，只在启动阶段、打开任何数据库/索引之前应用，
	// 避免在线替换文件造成句柄与状态不一致。
	if backup.HasPendingRestore(cfg.Storage.DataDir) {
		log.Warnf("检测到待应用的数据恢复包，开始恢复（当前数据将保留为 .pre_restore_<时间>）")
		restoreOpts := backup.Options{
			DataDir:     cfg.Storage.DataDir,
			KnowledgeDB: cfg.Storage.KnowledgeDB,
			BleveIndex:  cfg.Storage.BleveIndex,
			TaskDir:     cfg.Storage.TaskDir,
			LogDir:      cfg.Log.Dir,
			ConfigFile:  config.ConfigFileUsed,
		}
		if restoreErr := backup.ApplyPendingRestore(restoreOpts, log.Warnf); restoreErr != nil {
			log.Errorf("应用数据恢复失败，已中止启动（原数据保留，修复后可重启重试）: %v", restoreErr)
			os.Exit(1)
		}
		log.Infof("数据恢复完成，继续启动")
	}

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

	// 6. 初始化并启动 HTTP 服务（自动检测端口冲突，失败时自动累加端口尝试启动，最多尝试 5 次，全失败则终止）
	ln, actualPort, err := netutil.ListenWithPortRetry(cfg.Server.Host, cfg.Server.Port, netutil.DefaultMaxPortRetries, log)
	if err != nil {
		log.Errorf("HTTP 服务端口绑定失败且重试已达上限: %v", err)
		os.Exit(1)
	}

	// 端口顺延后同步更新配置对象与实际服务访问地址
	cfg.Server.Port = actualPort
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, actualPort)

	webURLForLock := fmt.Sprintf("http://%s:%d", normalizedWebHost(cfg.Server.Host), actualPort)
	if instanceLock != nil {
		if err := instanceLock.Publish(singleinstance.InstanceInfo{
			PID:       os.Getpid(),
			URL:       webURLForLock,
			Version:   version.String,
			StartedAt: time.Now(),
		}); err != nil {
			log.Warnf("写入实例信息失败（第二个实例将无法自动打开控制台）: %v", err)
		}
	}

	r := api.SetupRouter(cfg, globalDB, knowledgeSvc, indexer, taskSvc)
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

	// 7. 程序运行时自动调用系统默认浏览器打开网页 (可通过 -no-browser 禁用)
	if !*noBrowser {
		go func() {
			select {
			case <-readyCh:
				time.Sleep(150 * time.Millisecond)
				webURL := fmt.Sprintf("http://%s:%d", normalizedWebHost(cfg.Server.Host), actualPort)
				log.Infof("正在调用系统默认浏览器打开 Web 控制台: %s", webURL)
				if err := browser.Open(webURL); err != nil {
					log.Warnf("调用系统默认浏览器打开失败: %v, 请手动在浏览器访问: %s", err, webURL)
				} else {
					log.Infof("已成功调用默认浏览器打开网页: %s", webURL)
				}
			case <-notifyCtx.Done():
				return
			}
		}()
	}

	// 8. 初始化并启动系统托盘图标 (Windows)
	var systemTray tray.Tray
	if !*noTray {
		systemTray = tray.New(tray.Config{
			Port:     actualPort,
			LogDir:   cfg.Log.Dir,
			DataDir:  filepath.Dir(cfg.Storage.KnowledgeDB),
			Version:  version.String,
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

// normalizedWebHost 把监听地址归一化为浏览器可访问的主机名。
func normalizedWebHost(host string) string {
	if host == "0.0.0.0" || host == "" || host == "::" {
		return "127.0.0.1"
	}
	return host
}

// openRunningInstance 在检测到已有实例时，打开其 Web 控制台（或打印地址）。
func openRunningInstance(cfg *config.Config, noBrowser bool) {
	target := ""
	pid := 0
	if info, err := singleinstance.ReadInstanceInfo(cfg.Storage.DataDir); err == nil && info.URL != "" {
		target = info.URL
		pid = info.PID
	} else {
		target = fmt.Sprintf("http://%s:%d", normalizedWebHost(cfg.Server.Host), cfg.Server.Port)
	}

	if pid > 0 {
		logger.Log.Infof("检测到已有实例正在运行 (pid=%d): %s", pid, target)
	} else {
		logger.Log.Infof("检测到已有实例正在运行: %s", target)
	}
	if noBrowser {
		return
	}
	if err := browser.Open(target); err != nil {
		logger.Log.Warnf("打开已有实例控制台失败: %v，请手动访问 %s", err, target)
	}
}
