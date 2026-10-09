package storage

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/glebarez/sqlite"
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

	// 验证反向索引 idx_tag_relations_log_id 是否存在
	var indexCount int64
	db.Raw("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_tag_relations_log_id'").Scan(&indexCount)
	if indexCount != 1 {
		t.Errorf("expected idx_tag_relations_log_id index, got %d", indexCount)
	}
}
