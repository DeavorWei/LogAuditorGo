package storage

import (
	"os"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestLegacyTaskDBUpgrade_OrphansAndTriggers 回归测试守护 (P0/P1):
// 验证升级旧版本任务库（无外键、含孤儿关联数据、含挂载在 log_records 上引用 log_tag_relations 的旧触发器）：
// 1. openTaskDB 必须顺利打开，不因外键约束或触发器互斥而发生回滚或致命错误；
// 2. 孤儿关联被彻底自愈清理，合规关联完整保留；
// 3. PRAGMA foreign_key_check 返回 0 条违规，保证外键数据严格自洽；
// 4. 级联删除触发器重新建立成功并正常级联删除；
// 5. 迁移完成后连接池配置正确恢复为 4。
func TestLegacyTaskDBUpgrade_OrphansAndTriggers(t *testing.T) {
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

	stmts := []string{
		"CREATE TABLE log_records (id INTEGER PRIMARY KEY AUTOINCREMENT, raw_log TEXT, device_id INTEGER, timestamp TEXT, knowledge_id INTEGER, module TEXT)",
		"CREATE TABLE log_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, color TEXT, remark TEXT)",
		"CREATE TABLE log_tag_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, tag_id INTEGER, log_id INTEGER, created_at DATETIME)",
		`CREATE TRIGGER trg_cascade_delete_log_tag_rel AFTER DELETE ON log_records BEGIN DELETE FROM log_tag_relations WHERE log_id = OLD.id; END;`,
		`CREATE TRIGGER trg_cascade_delete_tag_tag_rel AFTER DELETE ON log_tags BEGIN DELETE FROM log_tag_relations WHERE tag_id = OLD.id; END;`,
		// 插入合规数据：1条日志，1个标签，1条正常关联
		"INSERT INTO log_records (id, raw_log) VALUES (1, 'normal log')",
		"INSERT INTO log_tags (id, name) VALUES (10, 'normal tag')",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (10, 1)",
		// 插入孤儿关联：分别指向不存在的 log_id=999 和 tag_id=888
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (10, 999)",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (888, 1)",
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
		t.Errorf("expected 1 valid relation remaining (orphans purged), got %d", relCount)
	}

	var remainingRel struct {
		TagID uint
		LogID uint
	}
	db.Raw("SELECT tag_id, log_id FROM log_tag_relations LIMIT 1").Scan(&remainingRel)
	if remainingRel.TagID != 10 || remainingRel.LogID != 1 {
		t.Errorf("expected relation (10, 1), got (%d, %d)", remainingRel.TagID, remainingRel.LogID)
	}

	// 4. 断言外键完整性校验：PRAGMA foreign_key_check 必须为 0 违规
	type fkCheckRow struct {
		Table  string `gorm:"column:table"`
		RowID  int64  `gorm:"column:rowid"`
		Parent string `gorm:"column:parent"`
		FkID   int    `gorm:"column:fkid"`
	}
	var fkViolations []fkCheckRow
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&fkViolations).Error; err != nil {
		t.Fatalf("pragma foreign_key_check failed: %v", err)
	}
	if len(fkViolations) != 0 {
		t.Fatalf("expected 0 foreign key violations, got %d: %+v", len(fkViolations), fkViolations)
	}

	// 5. 断言触发器已重新建立并能正常工作
	if err := db.Exec("DELETE FROM log_records WHERE id = 1").Error; err != nil {
		t.Fatalf("delete log record failed: %v", err)
	}
	db.Raw("SELECT count(*) FROM log_tag_relations").Scan(&relCount)
	if relCount != 0 {
		t.Errorf("expected 0 relations after cascade delete, got %d", relCount)
	}

	// 6. 断言连接池配置在迁移后已成功恢复为 4
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	if sqlDB.Stats().MaxOpenConnections != 4 {
		t.Errorf("expected MaxOpenConnections=4 after migration, got %d", sqlDB.Stats().MaxOpenConnections)
	}
}

// TestPurgeOrphanTagRelations 验证孤儿清理函数对无效 log_id 与 tag_id 的幂等清理
func TestPurgeOrphanTagRelations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task_db_orphan_purge_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	taskID := "test_task_purge_01"
	db, err := openTaskDB(tempDir, taskID)
	if err != nil {
		t.Fatalf("openTaskDB failed: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	// 插入一条合法记录
	if err := db.Exec("INSERT INTO log_records (id, raw_log) VALUES (1, 'log 1')").Error; err != nil {
		t.Fatalf("insert log failed: %v", err)
	}
	if err := db.Exec("INSERT INTO log_tags (id, name) VALUES (1, 'tag 1')").Error; err != nil {
		t.Fatalf("insert tag failed: %v", err)
	}
	if err := db.Exec("INSERT INTO log_tag_relations (id, tag_id, log_id) VALUES (1, 1, 1)").Error; err != nil {
		t.Fatalf("insert valid relation failed: %v", err)
	}

	// 临时关闭外键约束模拟注入 2 条孤儿数据
	_ = db.Exec("PRAGMA foreign_keys = OFF")
	_ = db.Exec("INSERT INTO log_tag_relations (id, tag_id, log_id) VALUES (2, 1, 999)") // 无效 log_id
	_ = db.Exec("INSERT INTO log_tag_relations (id, tag_id, log_id) VALUES (3, 888, 1)") // 无效 tag_id
	_ = db.Exec("PRAGMA foreign_keys = ON")

	var count int64
	db.Raw("SELECT count(*) FROM log_tag_relations").Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 relations before purge, got %d", count)
	}

	purged, err := purgeOrphanTagRelations(db, taskID)
	if err != nil {
		t.Fatalf("purgeOrphanTagRelations failed: %v", err)
	}
	if purged != 2 {
		t.Fatalf("expected 2 orphan relations purged, got %d", purged)
	}

	db.Raw("SELECT count(*) FROM log_tag_relations").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 valid relation remaining, got %d", count)
	}

	// 再次调用应幂等返回 0
	purgedAgain, err := purgeOrphanTagRelations(db, taskID)
	if err != nil {
		t.Fatalf("purgeOrphanTagRelations idempotent call failed: %v", err)
	}
	if purgedAgain != 0 {
		t.Fatalf("expected 0 purged on idempotent call, got %d", purgedAgain)
	}
}

// TestLegacyTaskDBUpgrade_EndToEndFKConsistency 验证端到端升级后的外键自洽性
func TestLegacyTaskDBUpgrade_EndToEndFKConsistency(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task_db_e2e_fk_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	taskID := "e2e_fk_task_001"
	dbFile := TaskDBPath(tempDir, taskID)

	rawDB, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{})
	if err != nil {
		t.Fatalf("open raw db failed: %v", err)
	}

	// 构造含孤儿且未建立外键的旧表
	stmts := []string{
		"CREATE TABLE log_records (id INTEGER PRIMARY KEY AUTOINCREMENT, raw_log TEXT)",
		"CREATE TABLE log_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)",
		"CREATE TABLE log_tag_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, tag_id INTEGER, log_id INTEGER, created_at DATETIME)",
		"INSERT INTO log_records (id, raw_log) VALUES (1, 'log1')",
		"INSERT INTO log_tags (id, name) VALUES (1, 'tag1')",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (1, 1)",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (999, 888)", // 孤儿
	}
	for _, stmt := range stmts {
		_ = rawDB.Exec(stmt)
	}
	rawSQL, _ := rawDB.DB()
	_ = rawSQL.Close()

	// 打开库
	db, err := openTaskDB(tempDir, taskID)
	if err != nil {
		t.Fatalf("openTaskDB failed: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	// 核心断言：外键检查必须零违规
	type fkCheckRow struct {
		Table  string `gorm:"column:table"`
		RowID  int64  `gorm:"column:rowid"`
		Parent string `gorm:"column:parent"`
		FkID   int    `gorm:"column:fkid"`
	}
	var violations []fkCheckRow
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		t.Fatalf("pragma foreign_key_check failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations after migration, got %d: %+v", len(violations), violations)
	}
}

// TestLegacyTaskDBUpgrade_DegradedMigrationBranch 验证当跳过前置孤儿清理时，
// 系统准确触发降级分支 (PRAGMA foreign_keys = OFF)，并通过后置清理保证外键数据严格自洽
func TestLegacyTaskDBUpgrade_DegradedMigrationBranch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "task_db_degraded_branch_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	taskID := "degraded_branch_task_001"
	dbFile := TaskDBPath(tempDir, taskID)

	rawDB, err := gorm.Open(sqlite.Open(dbFile), &gorm.Config{})
	if err != nil {
		t.Fatalf("open raw db failed: %v", err)
	}

	// 构造含孤儿且未建立外键的旧表
	stmts := []string{
		"CREATE TABLE log_records (id INTEGER PRIMARY KEY AUTOINCREMENT, raw_log TEXT)",
		"CREATE TABLE log_tags (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)",
		"CREATE TABLE log_tag_relations (id INTEGER PRIMARY KEY AUTOINCREMENT, tag_id INTEGER, log_id INTEGER, created_at DATETIME)",
		"INSERT INTO log_records (id, raw_log) VALUES (1, 'log1')",
		"INSERT INTO log_tags (id, name) VALUES (1, 'tag1')",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (1, 1)",
		"INSERT INTO log_tag_relations (tag_id, log_id) VALUES (999, 888)", // 孤儿
	}
	for _, stmt := range stmts {
		_ = rawDB.Exec(stmt)
	}
	rawSQL, _ := rawDB.DB()
	_ = rawSQL.Close()

	// 激活测试钩子：跳过前置清理，强制让 AutoMigrate 抛出 FOREIGN KEY constraint failed 触发降级分支
	skipPreMigrationPurgeForTest = true
	defer func() {
		skipPreMigrationPurgeForTest = false
	}()

	beforeCount := atomic.LoadInt64(&migrationDegradedCount)

	db, err := openTaskDB(tempDir, taskID)
	if err != nil {
		t.Fatalf("openTaskDB under degraded branch should succeed, got err: %v", err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()

	afterCount := atomic.LoadInt64(&migrationDegradedCount)
	if afterCount <= beforeCount {
		t.Fatalf("expected degraded migration branch to be triggered (before=%d, after=%d)", beforeCount, afterCount)
	}

	// 核心断言：即使走了降级分支，后置清理也必须确保 PRAGMA foreign_key_check 为 0 条违规
	type fkCheckRow struct {
		Table  string `gorm:"column:table"`
		RowID  int64  `gorm:"column:rowid"`
		Parent string `gorm:"column:parent"`
		FkID   int    `gorm:"column:fkid"`
	}
	var violations []fkCheckRow
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		t.Fatalf("pragma foreign_key_check failed: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations after degraded migration, got %d: %+v", len(violations), violations)
	}
}

