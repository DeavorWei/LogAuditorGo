package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// 常量定义
const (
	DefaultLogFileName   = "log.log"
	DefaultSingleMaxByte = 10 * 1024 * 1024 // 单个日志文件最大 10MB
	DefaultMaxSizeMB     = 1024             // 默认最大保留总大小 1GB (1024MB)
	DefaultMaxDays       = 180              // 默认最大保留 180 天
)

// Log 全局 SugaredLogger 实例
var Log *zap.SugaredLogger

// globalWriter 全局轮转写入器实例
var globalWriter *RotatingFileWriter

// globalAtomicLevel 全局动态日志级别控制
var globalAtomicLevel zap.AtomicLevel

// Config 日志初始化配置
type Config struct {
	Level     string `json:"level"`
	Format    string `json:"format"`
	Dir       string `json:"dir"`
	MaxSizeMB int    `json:"max_size_mb"`
	MaxDays   int    `json:"max_days"`
}

// LogFileInfo 单个日志文件信息
type LogFileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
	IsActive bool      `json:"is_active"`
}

// LogStats 日志目录统计信息
type LogStats struct {
	Dir         string        `json:"dir"`
	CurrentSize int64         `json:"current_size"`
	TotalSize   int64         `json:"total_size"`
	FileCount   int           `json:"file_count"`
	MaxSizeMB   int           `json:"max_size_mb"`
	MaxDays     int           `json:"max_days"`
	Files       []LogFileInfo `json:"files"`
}

// RotatingFileWriter 支持启动转储、写时10MB轮转、留存自动清理的写入器
type RotatingFileWriter struct {
	mu            sync.Mutex
	cleanMu       sync.Mutex
	dir           string
	filename      string
	file          *os.File
	currentSize   int64
	maxSingleSize int64

	// cleanTrigger / cleanDone 用于把"日志留存清理"串行化到单个常驻 goroutine (ARCH-18)。
	//
	// 原实现在构造时与每次轮转后各 `go w.CleanOldLogs()`：
	// 高频轮转场景下 goroutine 会短暂堆积，
	// 更严重的是停机瞬间可能与 shutdown 竞态删除文件。
	// 改为 Start()/Stop() 显式管理的单协程 + channel 模型。
	cleanTrigger chan struct{}
	cleanDone    chan struct{}
	cleanOnce    sync.Once

	maxSizeMB int
	maxDays   int
	isClosed  bool
}

func init() {
	if Log == nil {
		globalAtomicLevel = zap.NewAtomicLevelAt(zapcore.InfoLevel)
		Log = zap.NewNop().Sugar()
	}
}

// FormatArchiveName 根据时间生成归档日志文件名: log_YYYYMMDD_HHMMSSxx.log
func FormatArchiveName(t time.Time) string {
	datePart := t.Format("20060102")
	timePart := t.Format("150405")
	centis := t.Nanosecond() / 10000000 // 两位厘秒 (00~99)
	return fmt.Sprintf("log_%s_%s%02d.log", datePart, timePart, centis)
}

// NewRotatingFileWriter 创建并初始化轮转文件写入器
func NewRotatingFileWriter(dir string, maxSizeMB, maxDays int) (*RotatingFileWriter, error) {
	if dir == "" {
		dir = filepath.Join("LogAuditorGoData", "log")
	}
	if maxSizeMB <= 0 {
		maxSizeMB = DefaultMaxSizeMB
	}
	if maxDays <= 0 {
		maxDays = DefaultMaxDays
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir %s failed: %w", dir, err)
	}

	w := &RotatingFileWriter{
		dir:           dir,
		filename:      DefaultLogFileName,
		maxSingleSize: DefaultSingleMaxByte,
		maxSizeMB:     maxSizeMB,
		maxDays:       maxDays,
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// 规则 1 & 2: 程序每次启动时，若存在上次程序运行产生的 log.log，将其改名为该文件上次修改时间，并生成独立的空白 log.log
	activePath := filepath.Join(w.dir, w.filename)
	if info, err := os.Stat(activePath); err == nil && !info.IsDir() {
		archiveName := resolveCollision(w.dir, FormatArchiveName(info.ModTime()))
		archivePath := filepath.Join(w.dir, archiveName)
		_ = os.Rename(activePath, archivePath)
	}

	// 创建全新的空白 log.log
	file, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log file %s failed: %w", activePath, err)
	}
	w.file = file
	w.currentSize = 0
	w.cleanTrigger = make(chan struct{}, 1)
	w.cleanDone = make(chan struct{})

	// ARCH-18: 启动唯一的常驻清理协程，并立即触发一次留存规则清理
	go w.cleanLoop()
	w.triggerClean()

	return w, nil
}

// cleanLoop 常驻清理协程：串行消费清理请求，收到停止信号后退出 (ARCH-18)
func (w *RotatingFileWriter) cleanLoop() {
	defer close(w.cleanDone)
	for range w.cleanTrigger {
		w.CleanOldLogs()
	}
}

// triggerClean 以非阻塞方式投递一次清理请求（重复投递会被合并，避免协程堆积）
func (w *RotatingFileWriter) triggerClean() {
	select {
	case w.cleanTrigger <- struct{}{}:
	default:
	}
}

// Stop 停止清理协程并关闭底层日志文件。
//
// ARCH-18: 必须保证"清理协程先退出、再关闭文件句柄"的顺序，
// 否则停机瞬间可能出现"一边删归档、一边写日志"的竞态。
func (w *RotatingFileWriter) Stop() {
	if w == nil {
		return
	}

	w.cleanOnce.Do(func() {
		if w.cleanTrigger != nil {
			close(w.cleanTrigger)
		}
		if w.cleanDone != nil {
			<-w.cleanDone
		}
	})

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isClosed || w.file == nil {
		w.isClosed = true
		return
	}
	_ = w.file.Sync()
	_ = w.file.Close()
	w.file = nil
	w.isClosed = true
}

// resolveCollision 处理归档文件名重复冲突，若存在同名文件则递增后缀
func resolveCollision(dir, targetName string) string {
	targetPath := filepath.Join(dir, targetName)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		return targetName
	}

	base := strings.TrimSuffix(targetName, ".log")
	for i := 1; i < 10000; i++ {
		newName := fmt.Sprintf("%s_%d.log", base, i)
		newPath := filepath.Join(dir, newName)
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			return newName
		}
	}
	return fmt.Sprintf("%s_%d.log", base, time.Now().UnixNano())
}

// Write 实现 io.Writer，写时检测是否超过 10MB 触发轮转
func (w *RotatingFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.isClosed {
		return 0, fmt.Errorf("rotating file writer is closed")
	}

	writeLen := int64(len(p))
	// 规则 3: 当单个 log.log 文件大于 10M，则改名转储并新建空白 log.log
	if w.file != nil && (w.currentSize+writeLen > w.maxSingleSize) {
		if err := w.rotateLocked(); err != nil {
			// 如果轮转失败，尝试继续写入当前文件，不丢失日志
			fmt.Fprintf(os.Stderr, "[Logger Error] rotate log file failed: %v\n", err)
		}
	}

	if w.file == nil {
		activePath := filepath.Join(w.dir, w.filename)
		f, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return 0, err
		}
		w.file = f
	}

	n, err = w.file.Write(p)
	w.currentSize += int64(n)
	return n, err
}

// Sync 实现 zapcore.WriteSyncer
func (w *RotatingFileWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// Close 关闭文件写入器
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.isClosed = true
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// rotateLocked 内部轮转逻辑（调用方须持有锁）
func (w *RotatingFileWriter) rotateLocked() error {
	activePath := filepath.Join(w.dir, w.filename)
	modTime := time.Now()

	if w.file != nil {
		if stat, err := w.file.Stat(); err == nil {
			modTime = stat.ModTime()
		}
		_ = w.file.Sync()
		_ = w.file.Close()
		w.file = nil
	}

	// 改名转储
	archiveName := resolveCollision(w.dir, FormatArchiveName(modTime))
	archivePath := filepath.Join(w.dir, archiveName)
	if err := os.Rename(activePath, archivePath); err != nil {
		return fmt.Errorf("rename %s to %s failed: %w", activePath, archivePath, err)
	}

	// 新建独立空白 log.log
	file, err := os.OpenFile(activePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create new log file %s failed: %w", activePath, err)
	}
	w.file = file
	w.currentSize = 0

	// 异步执行留存自动清理（由常驻协程串行处理，不再每次轮转都新建 goroutine）
	w.triggerClean()

	return nil
}

// CleanOldLogs 执行日志留存策略检查与清理 (规则 4)
// 默认最大保留 1G 日志，保留 180 天日志，先达到哪个条件就按日期从旧的文件开始清理
func (w *RotatingFileWriter) CleanOldLogs() {
	w.cleanMu.Lock()
	defer w.cleanMu.Unlock()

	w.mu.Lock()
	dir := w.dir
	maxSizeMB := w.maxSizeMB
	maxDays := w.maxDays
	activeFilename := w.filename
	w.mu.Unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var archiveFiles []LogFileInfo
	var totalSize int64

	now := time.Now()
	cutoffTime := now.Add(-time.Duration(maxDays) * 24 * time.Hour)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		size := info.Size()
		modTime := info.ModTime()
		fullPath := filepath.Join(dir, name)
		isActive := (name == activeFilename)

		totalSize += size

		if !isActive && strings.HasPrefix(name, "log_") {
			archiveFiles = append(archiveFiles, LogFileInfo{
				Name:     name,
				Path:     fullPath,
				Size:     size,
				ModTime:  modTime,
				IsActive: false,
			})
		}
	}

	// 按修改时间升序排列（最旧的在前面）
	sort.Slice(archiveFiles, func(i, j int) bool {
		return archiveFiles[i].ModTime.Before(archiveFiles[j].ModTime)
	})

	maxSizeBytes := int64(maxSizeMB) * 1024 * 1024
	var remainingArchives []LogFileInfo

	// 步骤 A: 先按天数清理超过 maxDays 的历史归档日志
	for _, file := range archiveFiles {
		if file.ModTime.Before(cutoffTime) {
			if err := os.Remove(file.Path); err == nil {
				totalSize -= file.Size
			}
		} else {
			remainingArchives = append(remainingArchives, file)
		}
	}

	// 步骤 B: 若总大小依然超过 maxSizeMB，按日期从最旧的文件开始清理，直到总大小 <= maxSizeBytes
	if totalSize > maxSizeBytes {
		for _, file := range remainingArchives {
			if totalSize <= maxSizeBytes {
				break
			}
			if err := os.Remove(file.Path); err == nil {
				totalSize -= file.Size
			}
		}
	}
}

// UpdatePolicy 动态更新日志保留策略并立即触发清理
func (w *RotatingFileWriter) UpdatePolicy(maxSizeMB, maxDays int) {
	w.mu.Lock()
	if maxSizeMB > 0 {
		w.maxSizeMB = maxSizeMB
	}
	if maxDays > 0 {
		w.maxDays = maxDays
	}
	w.mu.Unlock()

	w.CleanOldLogs()
}

// GetStats 获取当前日志目录的统计状态
func (w *RotatingFileWriter) GetStats() LogStats {
	w.mu.Lock()
	dir := w.dir
	maxSizeMB := w.maxSizeMB
	maxDays := w.maxDays
	activeFilename := w.filename
	currSize := w.currentSize
	if w.file != nil {
		_ = w.file.Sync()
	}
	w.mu.Unlock()

	stats := LogStats{
		Dir:         dir,
		CurrentSize: currSize,
		MaxSizeMB:   maxSizeMB,
		MaxDays:     maxDays,
		Files:       make([]LogFileInfo, 0),
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return stats
	}

	var totalSize int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		size := info.Size()
		modTime := info.ModTime()
		isActive := (name == activeFilename)
		if isActive && currSize > size {
			size = currSize
		}
		totalSize += size

		if isActive {
			stats.CurrentSize = size
		}

		stats.Files = append(stats.Files, LogFileInfo{
			Name:     name,
			Path:     filepath.Join(dir, name),
			Size:     size,
			ModTime:  modTime,
			IsActive: isActive,
		})
	}

	// 文件列表按修改时间降序（活跃文件排在最前）
	sort.Slice(stats.Files, func(i, j int) bool {
		if stats.Files[i].IsActive != stats.Files[j].IsActive {
			return stats.Files[i].IsActive
		}
		return stats.Files[i].ModTime.After(stats.Files[j].ModTime)
	})

	stats.TotalSize = totalSize
	stats.FileCount = len(stats.Files)
	return stats
}

// parseZapLevel 解析日志级别
func parseZapLevel(levelStr string) zapcore.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// InitWithConfig 完整配置初始化日志系统
// loggerState 保存重建 logger 所需的运行时状态，用于支持运行时切换格式 (ARCH-13)
type loggerState struct {
	mu     sync.Mutex
	config Config
}

var currentLoggerState loggerState

// buildEncoder 依据格式名构造控制台 / 文件编码器
func buildEncoder(format string) (console zapcore.Encoder, file zapcore.Encoder) {
	consoleCfg := zap.NewProductionEncoderConfig()
	consoleCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	consoleCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	// 文件日志使用非颜色编码器，避免 ANSI 转义序列污染离线日志文件
	fileCfg := zap.NewProductionEncoderConfig()
	fileCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	fileCfg.EncodeLevel = zapcore.CapitalLevelEncoder

	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return zapcore.NewJSONEncoder(consoleCfg), zapcore.NewJSONEncoder(fileCfg)
	}
	return zapcore.NewConsoleEncoder(consoleCfg), zapcore.NewConsoleEncoder(fileCfg)
}

// normalizeFormat 归一化日志格式名，未知值回退为 console
func normalizeFormat(format string) string {
	if strings.EqualFold(strings.TrimSpace(format), "json") {
		return "json"
	}
	return "console"
}

func InitWithConfig(cfg Config) *zap.SugaredLogger {
	currentLoggerState.mu.Lock()
	defer currentLoggerState.mu.Unlock()

	cfg.Format = normalizeFormat(cfg.Format)
	currentLoggerState.config = cfg

	level := parseZapLevel(cfg.Level)
	globalAtomicLevel = zap.NewAtomicLevelAt(level)

	consoleEncoder, fileEncoder := buildEncoder(cfg.Format)

	// 初始化轮转文件写入器
	var cores []zapcore.Core

	// 控制台输出
	cores = append(cores, zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), globalAtomicLevel))

	// 文件输出
	if cfg.Dir != "" {
		writer, err := NewRotatingFileWriter(cfg.Dir, cfg.MaxSizeMB, cfg.MaxDays)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Logger Init Error] %v\n", err)
		} else {
			// 避免重复初始化时遗留上一个 writer 的清理协程 (ARCH-18)
			if globalWriter != nil {
				globalWriter.Stop()
			}
			globalWriter = writer
			cores = append(cores, zapcore.NewCore(fileEncoder, zapcore.AddSync(writer), globalAtomicLevel))
		}
	}

	teeCore := zapcore.NewTee(cores...)
	logger := zap.New(teeCore, zap.AddCaller())
	Log = logger.Sugar()
	return Log
}

// SetFormat 运行时切换日志编码格式 (ARCH-13)。
//
// 原实现的 `RegisterLogUpdateHook(func(..., format string) {...})` 里 format 形参从未被使用：
// `PUT /system/config/log` 接受并持久化了 format，但下一次重启前完全不生效，
// 用户误以为已经切到了 JSON 日志。
// 这里复用 zap.AtomicLevel，只重建 encoder 而不重建写入器，
// 因此切换过程中日志文件句柄与归档计数都不会丢失。
func SetFormat(format string) {
	currentLoggerState.mu.Lock()
	defer currentLoggerState.mu.Unlock()

	normalized := normalizeFormat(format)
	if currentLoggerState.config.Format == normalized {
		return
	}
	currentLoggerState.config.Format = normalized

	if globalAtomicLevel == (zap.AtomicLevel{}) {
		// 尚未初始化：只更新配置，等 Init 时生效
		return
	}

	consoleEncoder, fileEncoder := buildEncoder(normalized)
	var cores []zapcore.Core
	cores = append(cores, zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), globalAtomicLevel))
	if globalWriter != nil {
		cores = append(cores, zapcore.NewCore(fileEncoder, zapcore.AddSync(globalWriter), globalAtomicLevel))
	}

	Log = zap.New(zapcore.NewTee(cores...), zap.AddCaller()).Sugar()
}

// Shutdown 释放日志子系统资源，供优雅停机调用 (ARCH-18)。
//
// 顺序很关键：先停清理协程，再关闭文件句柄，
// 否则可能出现"清理协程正在删归档、同时还有日志在写入"的竞态。
func Shutdown() {
	currentLoggerState.mu.Lock()
	w := globalWriter
	globalWriter = nil
	currentLoggerState.mu.Unlock()

	if w == nil {
		return
	}

	// 停机前把缓冲刷盘，保证最后几条日志不丢
	if Log != nil {
		_ = Log.Sync()
	}
	w.Stop()
}

// Init 兼容历史签名的快捷初始化方法
func Init(levelStr string, format string, logDir ...string) *zap.SugaredLogger {
	dir := ""
	if len(logDir) > 0 && logDir[0] != "" {
		dir = logDir[0]
	}
	return InitWithConfig(Config{
		Level:     levelStr,
		Format:    format,
		Dir:       dir,
		MaxSizeMB: DefaultMaxSizeMB,
		MaxDays:   DefaultMaxDays,
	})
}

// SetLevel 动态调整日志记录级别
func SetLevel(levelStr string) {
	globalAtomicLevel.SetLevel(parseZapLevel(levelStr))
}

// UpdatePolicy 动态调整全局日志留存策略
func UpdatePolicy(maxSizeMB, maxDays int) {
	if globalWriter != nil {
		globalWriter.UpdatePolicy(maxSizeMB, maxDays)
	}
}

// CleanOldLogs 手动触发一次日志清理
func CleanOldLogs() {
	if globalWriter != nil {
		globalWriter.CleanOldLogs()
	}
}

// GetLogStats 获取当前日志运行状态与文件列表
func GetLogStats() LogStats {
	if globalWriter != nil {
		return globalWriter.GetStats()
	}
	return LogStats{
		Files: make([]LogFileInfo, 0),
	}
}

// GetGlobalWriter 获取底层轮转写入器（供测试或特殊调用）
func GetGlobalWriter() *RotatingFileWriter {
	return globalWriter
}
