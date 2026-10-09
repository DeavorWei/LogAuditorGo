package storage

import (
	"database/sql"
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteRegexpFunction(t *testing.T) {
	RegisterSQLiteFunctions()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite failed: %v", err)
	}
	defer db.Close()

	// 1. 基础正则匹配
	var match int
	if err := db.QueryRow("SELECT 'BFD_SESS_DOWN' REGEXP 'BFD.*DOWN'").Scan(&match); err != nil {
		t.Fatalf("regexp query failed: %v", err)
	}
	if match != 1 {
		t.Errorf("expected match=1, got %d", match)
	}

	// 2. 正则未命中
	var noMatch int
	if err := db.QueryRow("SELECT 'IF_UP' REGEXP 'BFD.*DOWN'").Scan(&noMatch); err != nil {
		t.Fatalf("regexp query failed: %v", err)
	}
	if noMatch != 0 {
		t.Errorf("expected noMatch=0, got %d", noMatch)
	}

	// 3. NULL 安全性防御测试（防止 interface conversion nil panic）
	var nullMatch int
	if err := db.QueryRow("SELECT NULL REGEXP 'BFD.*DOWN'").Scan(&nullMatch); err != nil {
		t.Fatalf("regexp with NULL value failed: %v", err)
	}
	if nullMatch != 0 {
		t.Errorf("expected nullMatch=0, got %d", nullMatch)
	}

	// 4. NULL pattern 安全性测试
	var nullPatMatch int
	if err := db.QueryRow("SELECT 'BFD_SESS_DOWN' REGEXP NULL").Scan(&nullPatMatch); err != nil {
		t.Fatalf("regexp with NULL pattern failed: %v", err)
	}
	if nullPatMatch != 0 {
		t.Errorf("expected nullPatMatch=0, got %d", nullPatMatch)
	}

	// 5. 非法正则表达式返回错误而非 panic
	var badMatch int
	err = db.QueryRow("SELECT 'BFD_SESS_DOWN' REGEXP '[invalid'").Scan(&badMatch)
	if err == nil {
		t.Errorf("expected error for invalid regexp pattern, got nil")
	}
}

func TestRegexpCache(t *testing.T) {
	cache := newRegexpCache(3)

	// 填充缓存
	re1, err := cache.GetOrCompile("abc.*")
	if err != nil || re1 == nil {
		t.Fatalf("compile abc.* failed: %v", err)
	}

	re1Cached, err := cache.GetOrCompile("abc.*")
	if err != nil || re1Cached != re1 {
		t.Errorf("expected cached instance, got different or err: %v", err)
	}

	// 测试超过上限后清空
	_, _ = cache.GetOrCompile("def.*")
	_, _ = cache.GetOrCompile("ghi.*")
	_, _ = cache.GetOrCompile("jkl.*")

	cache.RLock()
	cacheLen := len(cache.cache)
	cache.RUnlock()
	if cacheLen > 3 {
		t.Errorf("expected cache length <= 3, got %d", cacheLen)
	}
}

func TestTaskDBTagTablesMigration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task_db_tag_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	taskID := "test_task_tag_migration_01"
	db, err := openTaskDB(tempDir, taskID)
	if err != nil {
		t.Fatalf("openTaskDB failed: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// 验证 log_tags 与 log_tag_relations 表是否存在
	var tagTableCount int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('log_tags', 'log_tag_relations')").Scan(&tagTableCount)
	if tagTableCount != 2 {
		t.Errorf("expected 2 tag tables, got %d", tagTableCount)
	}

	// 验证反向索引 idx_tag_rel_log_id 是否存在 (无冗余)
	var indexCount int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_tag_rel_log_id'").Scan(&indexCount)
	if indexCount != 1 {
		t.Errorf("expected idx_tag_rel_log_id index, got %d", indexCount)
	}

	// 验证级联删除触发器 trg_cascade_delete_log_tag_rel 是否存在
	var triggerCount int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='trg_cascade_delete_log_tag_rel'").Scan(&triggerCount)
	if triggerCount != 1 {
		t.Errorf("expected trg_cascade_delete_log_tag_rel trigger, got %d", triggerCount)
	}
}

// TestTaskDBUpgrade_LegacyOrphansAndTriggers 验证升级旧版本任务库：
// 即使旧库无外键约束且残留孤儿关联、且包含旧版悬挂触发器，openTaskDB 也必须成功打开、自愈并重新建立触发器 (P0/P1)
func TestTaskDBUpgrade_LegacyOrphansAndTriggers(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task_db_legacy_upgrade_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	taskID := "legacy_task_001"
	dbFile := TaskDBPath(tempDir, taskID)

	// 1. 手工用基础 SQLite 连接模拟旧库状态：无 FK 约束、有孤儿关联、有触发器
	rawDB, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{})
	if err != nil {
		t.Fatalf("open raw db failed: %v", err)
	}

	// 建立旧表结构与触发器
	stmts := []string{
		"CREATE TABLE log_records (id INTEGER PRIMARY KEY AUTOINCREMENT, raw_log TEXT, device_id INTEGER, timestamp TEXT, knowledge_id INTEGER, module TEXT)",
		"CREATE TABLE log_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, color TEXT, remark TEXT)",
		"CREATE TABLE log_tag_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, tag_id INTEGER, log_id INTEGER, created_at DATETIME)",
		`CREATE TRIGGER trg_cascade_delete_log_tag_rel AFTER DELETE ON log_records BEGIN DELETE FROM log_tag_relations WHERE log_id = OLD.id; END;`,
		// 插入合规数据：1条日志，1个标签，1条正常关联
		"INSERT INTO log_records (id, raw_log) VALUES (1, 'normal log')",
		"INSERT INTO log_tags (id, name) VALUES (10, 'normal tag')",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (10, 1)",
		// 插入孤儿关联：指向不存在的 log_id=999 和 tag_id=888
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (888, 999)",
	}
	for _, stmt := range stmts {
		if err := rawDB.Exec(stmt).Error; err != nil {
			t.Fatalf("setup legacy db statement '%s' failed: %v", stmt, err)
		}
	}
	rawSQL, _ := rawDB.DB()
	_ = rawSQL.Close()

	// 2. 通过 openTaskDB 打开该旧库，触发 AutoMigrate 与自愈防线
	db, err := openTaskDB(tempDir, taskID)
	if err != nil {
		t.Fatalf("openTaskDB for legacy db failed (P0 regression): %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	// 3. 断言孤儿行已被自动清理，合规关联仍完好保留
	var relCount int64
	db.Raw("SELECT count(*) FROM log_tag_relations").Scan(&relCount)
	if relCount != 1 {
		t.Errorf("expected 1 valid relation remaining (orphan purged), got %d", relCount)
	}

	var remainingRel struct {
		TagID uint
		LogID uint
	}
	db.Raw("SELECT tag_id, log_id FROM log_tag_relations LIMIT 1").Scan(&remainingRel)
	if remainingRel.TagID != 10 || remainingRel.LogID != 1 {
		t.Errorf("expected relation (10, 1), got (%d, %d)", remainingRel.TagID, remainingRel.LogID)
	}

	// 4. 断言触发器已重新建立并能正常工作
	if err := db.Exec("DELETE FROM log_records WHERE id = 1").Error; err != nil {
		t.Fatalf("delete log record failed: %v", err)
	}
	db.Raw("SELECT count(*) FROM log_tag_relations").Scan(&relCount)
	if relCount != 0 {
		t.Errorf("expected 0 relations after cascade delete, got %d", relCount)
	}
}

