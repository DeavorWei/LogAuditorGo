package storage_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"logauditorgo/internal/model"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

func TestAcquireTaskDB_Concurrency(t *testing.T) {
	logger.Init("debug", "console")

	tmpDir, err := os.MkdirTemp("", "task_pool_concurrency_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDir := filepath.Join(tmpDir, "tasks")
	taskID := "concurrent123456"

	// 并发 20 个 goroutine 同时调用 AcquireTaskDB
	const concurrency = 20
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, _, err := storage.GetOrCreateTaskDB(taskDir, taskID)
			if err != nil {
				errCh <- err
				return
			}
			defer storage.ReleaseTaskDB(taskID)

			// 验证连接可用性，简单写入或查询
			var count int64
			if err := db.Model(&model.TaskInfo{}).Count(&count).Error; err != nil {
				errCh <- err
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent AcquireTaskDB encountered error: %v", err)
	}

	// 验证池状态
	size, inUse := storage.GlobalPool.Stats()
	if size == 0 {
		t.Errorf("expected task db to be cached in pool, got size 0")
	}
	if inUse != 0 {
		t.Errorf("expected all references released, got inUse=%d", inUse)
	}

	// 清理
	_ = storage.GlobalPool.EvictTaskDB(taskID)
}

func TestEvictTaskDB_ConcurrencyWithAcquire(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "task_pool_evict_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDir := filepath.Join(tmpDir, "tasks")
	taskID := "evict_race_123456"

	// 协程 A 尝试 AcquireTaskDB，协程 B 紧接着执行 EvictTaskDB
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		db, _, err := storage.GetOrCreateTaskDB(taskDir, taskID)
		if err == nil && db != nil {
			storage.ReleaseTaskDB(taskID)
		}
	}()

	go func() {
		defer wg.Done()
		_ = storage.GlobalPool.EvictTaskDB(taskID)
	}()

	wg.Wait()

	// 再次确保驱逐干净
	_ = storage.GlobalPool.EvictTaskDB(taskID)

	// 验证池清理
	storage.GlobalPool.CleanUnusedInitLocks()
}

