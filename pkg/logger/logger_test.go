package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRotatingFileWriter_StartupBehavior(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger_test_startup_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 第一次启动：空目录，应该生成空白 log.log
	w1, err := NewRotatingFileWriter(tempDir, 1024, 180)
	if err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	msg1 := []byte("first run log message\n")
	if _, err := w1.Write(msg1); err != nil {
		t.Fatalf("write msg1 failed: %v", err)
	}
	_ = w1.Close()

	activeLogPath := filepath.Join(tempDir, DefaultLogFileName)
	content1, err := os.ReadFile(activeLogPath)
	if err != nil {
		t.Fatalf("read active log failed: %v", err)
	}
	if string(content1) != string(msg1) {
		t.Fatalf("expected '%s', got '%s'", msg1, content1)
	}

	// 模拟等待10毫秒
	time.Sleep(20 * time.Millisecond)

	// 第二次启动：应该将旧的 log.log 重命名为 log_YYYYMMDD_HHMMSSxx.log，并生成新的空白 log.log
	w2, err := NewRotatingFileWriter(tempDir, 1024, 180)
	if err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	defer w2.Close()

	// 验证新的 active log.log 初始为空白
	newContent, err := os.ReadFile(activeLogPath)
	if err != nil {
		t.Fatalf("read new active log failed: %v", err)
	}
	if len(newContent) != 0 {
		t.Fatalf("expected empty new log.log, got %d bytes: %s", len(newContent), string(newContent))
	}

	// 检查目录中是否有归档文件 log_*.log，且其内容为 msg1
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read temp dir failed: %v", err)
	}

	var foundArchive bool
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "log_") && strings.HasSuffix(name, ".log") {
			foundArchive = true
			archiveContent, err := os.ReadFile(filepath.Join(tempDir, name))
			if err != nil {
				t.Fatalf("read archive file %s failed: %v", name, err)
			}
			if string(archiveContent) != string(msg1) {
				t.Fatalf("archive file content mismatch: expected '%s', got '%s'", msg1, archiveContent)
			}
		}
	}

	if !foundArchive {
		t.Fatalf("expected archived log file, but none found in %s", tempDir)
	}
}

func TestRotatingFileWriter_SizeRotation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger_test_rotation_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewRotatingFileWriter(tempDir, 1024, 180)
	if err != nil {
		t.Fatalf("init writer failed: %v", err)
	}
	defer w.Close()

	// 设置一个较小的单文件最大值用于测试（例如 200 字节）
	w.maxSingleSize = 200

	chunk1 := bytes.Repeat([]byte("A"), 150)
	if _, err := w.Write(chunk1); err != nil {
		t.Fatalf("write chunk1 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// 写入 chunk2 (100 字节)，总计将超过 200 字节，应该触发轮转
	chunk2 := bytes.Repeat([]byte("B"), 100)
	if _, err := w.Write(chunk2); err != nil {
		t.Fatalf("write chunk2 failed: %v", err)
	}

	// 此时应该存在一个归档文件内容为 chunk1，当前 active log.log 内容为 chunk2
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}

	var archiveCount int
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "log_") && strings.HasSuffix(name, ".log") {
			archiveCount++
			data, _ := os.ReadFile(filepath.Join(tempDir, name))
			if len(data) != 150 {
				t.Fatalf("expected archive size 150, got %d", len(data))
			}
		}
	}

	if archiveCount != 1 {
		t.Fatalf("expected 1 archive file, got %d", archiveCount)
	}

	activeData, err := os.ReadFile(filepath.Join(tempDir, DefaultLogFileName))
	if err != nil {
		t.Fatalf("read active file failed: %v", err)
	}
	if len(activeData) != 100 {
		t.Fatalf("expected active log size 100, got %d", len(activeData))
	}
}

func TestRotatingFileWriter_RetentionCleanup(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger_test_retention_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewRotatingFileWriter(tempDir, 1, 10) // 1MB 限制, 10 天限制
	if err != nil {
		t.Fatalf("init writer failed: %v", err)
	}
	defer w.Close()

	// 模拟创建历史归档文件:
	// 1. 20天前的旧文件 (应该被天数规则删除)
	oldFile := filepath.Join(tempDir, "log_20200101_12000000.log")
	if err := os.WriteFile(oldFile, bytes.Repeat([]byte("X"), 100), 0644); err != nil {
		t.Fatalf("create old file failed: %v", err)
	}
	oldTime := time.Now().Add(-20 * 24 * time.Hour)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	// 2. 5天前的超大文件 (1.2MB, 超过 1MB 限制，应该被总大小规则触发从旧到新清理)
	bigFileOld := filepath.Join(tempDir, "log_20260810_12000000.log")
	bigData := bytes.Repeat([]byte("Y"), 700*1024)
	if err := os.WriteFile(bigFileOld, bigData, 0644); err != nil {
		t.Fatalf("create big file failed: %v", err)
	}
	t1 := time.Now().Add(-5 * 24 * time.Hour)
	_ = os.Chtimes(bigFileOld, t1, t1)

	bigFileNew := filepath.Join(tempDir, "log_20260815_12000000.log")
	if err := os.WriteFile(bigFileNew, bigData, 0644); err != nil {
		t.Fatalf("create big file 2 failed: %v", err)
	}
	t2 := time.Now().Add(-2 * 24 * time.Hour)
	_ = os.Chtimes(bigFileNew, t2, t2)

	// 执行清理
	w.CleanOldLogs()

	// 检查：
	// 1. oldFile (20天前) 必定已被删除
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("expected oldFile to be deleted, but still exists")
	}

	// 2. 由于 700KB + 700KB = 1.4MB > 1MB，最旧的 bigFileOld (5天前) 应该被删除，留下较新的 bigFileNew (2天前)
	if _, err := os.Stat(bigFileOld); !os.IsNotExist(err) {
		t.Fatalf("expected bigFileOld to be deleted due to size limit, but still exists")
	}

	if _, err := os.Stat(bigFileNew); os.IsNotExist(err) {
		t.Fatalf("expected bigFileNew to be preserved, but was deleted")
	}
}

func TestRotatingFileWriter_ConcurrentWrites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger_test_concurrent_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	w, err := NewRotatingFileWriter(tempDir, 1024, 180)
	if err != nil {
		t.Fatalf("init writer failed: %v", err)
	}
	defer w.Close()

	w.maxSingleSize = 500 // 频繁轮转测试

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				line := fmt.Sprintf("worker %d line %d: %s\n", workerID, j, strings.Repeat("M", 20))
				if _, err := w.Write([]byte(line)); err != nil {
					t.Errorf("write error: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	stats := w.GetStats()
	if stats.TotalSize <= 0 {
		t.Fatalf("expected positive total size, got %d", stats.TotalSize)
	}
	if stats.FileCount == 0 {
		t.Fatalf("expected log files to exist")
	}
}

func TestLogger_InitWithConfig_And_DynamicUpdates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger_test_init_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := Config{
		Level:     "debug",
		Format:    "console",
		Dir:       tempDir,
		MaxSizeMB: 500,
		MaxDays:   90,
	}

	sugar := InitWithConfig(cfg)
	if sugar == nil {
		t.Fatalf("expected non-nil sugar logger")
	}

	sugar.Debug("debug message")
	sugar.Info("info message")
	sugar.Warn("warning message")

	// 动态更新级别
	SetLevel("error")
	sugar.Info("should be ignored by error level")

	// 动态更新策略
	UpdatePolicy(200, 60)

	stats := GetLogStats()
	if stats.MaxSizeMB != 200 || stats.MaxDays != 60 {
		t.Fatalf("policy update failed: expected 200MB/60Days, got %dMB/%dDays", stats.MaxSizeMB, stats.MaxDays)
	}
	if stats.CurrentSize <= 0 {
		t.Fatalf("expected non-zero current size in log.log, got %d", stats.CurrentSize)
	}
}
