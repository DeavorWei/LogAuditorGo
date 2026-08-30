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

	// KB-17: 原实现在 DSN 里设置 busy_timeout(10000)，随后又被 PRAGMA 语句覆盖为 5000，
	// 两处配置相互冲突且无注释说明。这里统一在 DSN 一处声明（保留 _txlock=immediate 语义），
	// 下面的 pragmas 循环只负责 DSN 无法表达的参数。
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

	// SQLite PRAGMA 性能调优（busy_timeout 已在 DSN 中统一声明，此处不再覆盖）
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
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

	// KB-17: 为 knowledge_version_mappings 补齐复合索引，
	// 支撑"按知识查版本映射"与"按产品版本过滤"两类高频查询。
	for _, stmt := range []string{
		"CREATE INDEX IF NOT EXISTS idx_kvm_knowledge ON knowledge_version_mappings(knowledge_id)",
		"CREATE INDEX IF NOT EXISTS idx_kvm_product ON knowledge_version_mappings(product_type, product_version)",
		"CREATE INDEX IF NOT EXISTS idx_kvm_doc ON knowledge_version_mappings(document_id)",
		"CREATE INDEX IF NOT EXISTS idx_knowledges_content_hash ON knowledges(content_hash)",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			logger.Log.Warnf("create knowledge index failed (%s): %v", stmt, err)
		}
	}

	GlobalKnowledgeDB = db
	logger.Log.Infof("Knowledge DB initialized successfully at: %s", dbPath)
	return GlobalKnowledgeDB, nil
}

// CloseKnowledgeDB 关闭全局知识库连接。
//
// KB-17: 原实现没有任何关闭出口，停机时只 close 了 Bleve 与任务库，
// 主库的 WAL 从未 checkpoint，进程被强杀时可能留下未合并的 -wal 文件。
// 这里先做一次 WAL checkpoint(TRUNCATE) 把日志合并回主库，再关闭连接池。
func CloseKnowledgeDB() error {
	kdbMu.Lock()
	defer kdbMu.Unlock()

	if GlobalKnowledgeDB == nil {
		return nil
	}
	db := GlobalKnowledgeDB
	GlobalKnowledgeDB = nil

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get knowledge sql.DB failed: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		logger.Log.Warnf("knowledge db wal checkpoint failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close knowledge db failed: %w", err)
	}
	logger.Log.Info("Knowledge DB closed cleanly (WAL checkpointed)")
	return nil
}
