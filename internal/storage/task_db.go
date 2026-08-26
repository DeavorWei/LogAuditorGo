package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

var (
	taskDBPool  = make(map[string]*gorm.DB)
	poolMu      sync.RWMutex
	taskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)
)

// IsValidTaskID 校验任务ID格式是否合法，防止路径遍历与注入攻击
func IsValidTaskID(taskID string) bool {
	return taskIDRegex.MatchString(taskID)
}

func isValidTaskID(taskID string) bool {
	return IsValidTaskID(taskID)
}

// GetOrCreateTaskDB 获取或创建任务专属 SQLite 数据库
func GetOrCreateTaskDB(taskDir string, taskID string) (*gorm.DB, string, error) {
	if !isValidTaskID(taskID) {
		return nil, "", fmt.Errorf("invalid task id: %s", taskID)
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	if db, exists := taskDBPool[taskID]; exists {
		dbPath := filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskID))
		return db, dbPath, nil
	}

	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create task dir failed: %w", err)
	}

	dbPath := filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskID))
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, "", fmt.Errorf("open task db (%s) failed: %w", dbPath, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, "", fmt.Errorf("get sql.DB failed: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA cache_size = -32000;", // 32MB 缓存
		"PRAGMA foreign_keys = ON;",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			logger.Log.Warnf("exec pragma '%s' on task db failed: %v", p, err)
		}
	}

	sqlDB.SetMaxOpenConns(1) // SQLite 并发写入控制
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	// 自动迁移任务专属表
	if err := db.AutoMigrate(
		&model.TaskInfo{},
		&model.TaskFile{},
		&model.LogRecord{},
		&model.RCAEvent{},
	); err != nil {
		return nil, "", fmt.Errorf("auto migrate task tables failed: %w", err)
	}

	taskDBPool[taskID] = db
	return db, dbPath, nil
}

// CloseTaskDB 关闭并移除任务 DB 连接
func CloseTaskDB(taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}

	poolMu.Lock()
	defer poolMu.Unlock()

	if db, exists := taskDBPool[taskID]; exists {
		delete(taskDBPool, taskID)
		if sqlDB, err := db.DB(); err == nil {
			if closeErr := sqlDB.Close(); closeErr != nil {
				logger.Log.Warnf("close sql.DB for task %s failed: %v", taskID, closeErr)
			}
		}
	}
	return nil
}

// DeleteTaskDB 删除任务数据库物理文件并关闭连接
func DeleteTaskDB(taskDir string, taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}

	_ = CloseTaskDB(taskID)
	dbPath := filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskID))
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task db file (%s) failed: %w", dbPath, err)
	}
	return nil
}
