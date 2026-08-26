package storage_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

func TestStorageAndModels(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "logauditor_test_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger.Init("debug", "console")

	// 1. 测试配置加载
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfgContent := `
server:
  port: 9090
storage:
  data_dir: "` + filepath.ToSlash(tmpDir) + `"
  knowledge_db: "` + filepath.ToSlash(filepath.Join(tmpDir, "knowledge.db")) + `"
  task_dir: "` + filepath.ToSlash(filepath.Join(tmpDir, "tasks")) + `"
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write config file failed: %v", err)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config load failed: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}

	// 2. 测试全局知识库初始化与 CRUD
	kdb, err := storage.InitKnowledgeDB(cfg.Storage.KnowledgeDB)
	if err != nil {
		t.Fatalf("init knowledge db failed: %v", err)
	}

	doc := model.Document{
		LibID:          "TEST001",
		LibName:        "Test Product Doc",
		ProductType:    "CloudEngine 16800",
		ProductVersion: "V200R024C00",
		IssueDate:      "2026-04-01",
		Language:       "zh",
		TopicNumber:    100,
		LogCount:       50,
		AlarmCount:     20,
		ImportedAt:     time.Now(),
	}
	if err := kdb.Create(&doc).Error; err != nil {
		t.Fatalf("create document failed: %v", err)
	}

	k := model.Knowledge{
		EntryType:   model.EntryTypeLog,
		Module:      "BGP",
		Severity:    4,
		Brief:       "BGP_AUTH_FAILED",
		Message:     "BGP session authentication failed.",
		Description: "BGP authentication failed description",
		Cause:       "Key mismatch",
		Action:      "Check keychain config",
		ContentHash: "hash123456",
		Versions: []model.KnowledgeVersionMapping{
			{
				DocumentID:     doc.ID,
				TopicID:        "ZH-CN_LOGREF_0001",
				ProductType:    "CloudEngine 16800",
				ProductVersion: "V200R024C00",
				HtmlPath:       "r24c00/bgp_auth_4.html",
			},
		},
	}
	if err := kdb.Create(&k).Error; err != nil {
		t.Fatalf("create knowledge failed: %v", err)
	}

	var queryK model.Knowledge
	if err := kdb.Preload("Versions").First(&queryK, "content_hash = ?", "hash123456").Error; err != nil {
		t.Fatalf("query knowledge failed: %v", err)
	}
	if queryK.Module != "BGP" || len(queryK.Versions) != 1 {
		t.Errorf("knowledge data mismatch: %+v", queryK)
	}

	// 3. 测试任务专属数据库工厂与隔离
	taskID := "task_test_001"
	taskDB, dbPath, err := storage.GetOrCreateTaskDB(cfg.Storage.TaskDir, taskID)
	if err != nil {
		t.Fatalf("get or create task db failed: %v", err)
	}
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("task db file not found at: %s", dbPath)
	}

	record := model.LogRecord{
		Timestamp:       time.Now(),
		Hostname:        "SW-CORE-01",
		Module:          "BGP",
		Severity:        4,
		Brief:           "BGP_AUTH_FAILED",
		SlotInfo:        "Slot=1/1",
		RawLog:          "Apr 15 2026 14:00:00 SW-CORE-01 %%01BGP/4/BGP_AUTH_FAILED(l)[1]: ...",
		MessageBody:     "BGP session authentication failed.",
		ParametersJSON:  `{"PeerID":"192.168.1.1"}`,
		KnowledgeID:     queryK.ID,
		MatchTier:       "EXACT",
		MatchConfidence: 1.0,
	}
	if err := taskDB.Create(&record).Error; err != nil {
		t.Fatalf("create log record in task db failed: %v", err)
	}

	var count int64
	taskDB.Model(&model.LogRecord{}).Count(&count)
	if count != 1 {
		t.Errorf("expected 1 log record, got %d", count)
	}

	// 测试任务数据库关闭与删除
	if err := storage.DeleteTaskDB(cfg.Storage.TaskDir, taskID); err != nil {
		t.Fatalf("delete task db failed: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("expected task db file deleted, but still exists")
	}
}

func TestTaskDBValidation(t *testing.T) {
	invalidIDs := []string{
		"../../etc/passwd",
		"short",
		"task with spaces",
		"task*with*wildcards",
		"task/slash",
		"task\\backslash",
		"",
	}

	for _, badID := range invalidIDs {
		if storage.IsValidTaskID(badID) {
			t.Errorf("expected IsValidTaskID to be false for '%s'", badID)
		}
		if _, _, err := storage.GetOrCreateTaskDB("tasks", badID); err == nil {
			t.Errorf("expected GetOrCreateTaskDB to fail for '%s'", badID)
		}
		if err := storage.CloseTaskDB(badID); err == nil {
			t.Errorf("expected CloseTaskDB to fail for '%s'", badID)
		}
		if err := storage.DeleteTaskDB("tasks", badID); err == nil {
			t.Errorf("expected DeleteTaskDB to fail for '%s'", badID)
		}
	}

	validIDs := []string{
		"task_test_001",
		"12345678",
		"Task-ID_12345",
	}
	for _, goodID := range validIDs {
		if !storage.IsValidTaskID(goodID) {
			t.Errorf("expected IsValidTaskID to be true for '%s'", goodID)
		}
	}
}

