package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

var (
	GlobalKnowledgeDB *gorm.DB
	kdbMu             sync.Mutex
)

// InitKnowledgeDB 初始化全局知识库 SQLite
func InitKnowledgeDB(dbPath string) (*gorm.DB, error) {
	kdbMu.Lock()
	defer kdbMu.Unlock()

	if GlobalKnowledgeDB != nil {
		return GlobalKnowledgeDB, nil
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create dir for knowledge db failed: %w", err)
	}

	dsn := dbPath
	if !strings.Contains(dbPath, "?") {
		dsn = dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)&_txlock=immediate"
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open knowledge db failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}

	// SQLite PRAGMA 性能调优
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA cache_size = -64000;", // 64MB 缓存
		"PRAGMA foreign_keys = ON;",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			logger.Log.Warnf("exec pragma '%s' failed: %v", p, err)
		}
	}

	sqlDB.SetMaxOpenConns(10) // WAL 模式下支持并发读
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 自动迁移全局表
	if err := db.AutoMigrate(
		&model.Document{},
		&model.Knowledge{},
		&model.KnowledgeVersionMapping{},
		&model.TaskInfo{},
	); err != nil {
		return nil, fmt.Errorf("auto migrate knowledge tables failed: %w", err)
	}

	GlobalKnowledgeDB = db
	logger.Log.Infof("Knowledge DB initialized successfully at: %s", dbPath)
	return GlobalKnowledgeDB, nil
}
