package storage_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

func TestGetExistingTaskDB_NoGhostCreation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "task_pool_ghost_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDir := filepath.Join(tmpDir, "tasks")
	taskID := "ghost_task_12345"
	dbFile := storage.TaskDBPath(taskDir, taskID)

	// 1. 文件原本不存在时，GetExistingTaskDB 必须报错且绝不在磁盘新建文件
	_, _, err = storage.GetExistingTaskDB(taskDir, taskID)
	if err == nil {
		t.Fatalf("expected error for non-existent db, got nil")
	}
	if _, statErr := os.Stat(dbFile); !os.IsNotExist(statErr) {
		t.Fatalf("ghost db file was created at: %s", dbFile)
	}

	// 2. 正常创建任务库并释放
	db, _, err := storage.GetOrCreateTaskDB(taskDir, taskID)
	if err != nil {
		t.Fatalf("create task db failed: %v", err)
	}
	if db == nil {
		t.Fatalf("expected valid db pointer")
	}
	storage.ReleaseTaskDB(taskID)

	// 3. 文件已存在时，GetExistingTaskDB 应该正常获取
	existingDB, path, err := storage.GetExistingTaskDB(taskDir, taskID)
	if err != nil {
		t.Fatalf("get existing task db failed: %v", err)
	}
	if existingDB == nil || path != dbFile {
		t.Fatalf("unexpected existing db or path: %v, %s", existingDB, path)
	}
	storage.ReleaseTaskDB(taskID)

	// 4. 彻底删除数据库文件后
	if err := storage.DeleteTaskDB(taskDir, taskID); err != nil {
		t.Fatalf("delete task db failed: %v", err)
	}

	// 5. 再次调用 GetExistingTaskDB，必须返回错误，且磁盘上依然不能出现新文件！
	_, _, err = storage.GetExistingTaskDB(taskDir, taskID)
	if err == nil {
		t.Fatalf("expected error after deletion, got nil")
	}
	if _, statErr := os.Stat(dbFile); !os.IsNotExist(statErr) {
		t.Fatalf("ghost db file revived after deletion at: %s", dbFile)
	}
}

func TestCleanUnusedInitLocks_Lifecycle(t *testing.T) {
	pool := storage.NewTaskDBPool(10, 100*time.Millisecond)
	defer pool.CloseAll()

	tmpDir, err := os.MkdirTemp("", "task_pool_clean_locks_*")
	if err != nil {
		t.Fatalf("create temp dir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	taskDir := filepath.Join(tmpDir, "tasks")

	// 并发获取和释放多个任务库
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		taskID := fmt.Sprintf("task_lock_%08d", i)
		go func(id string) {
			defer wg.Done()
			db, err := pool.AcquireTaskDB(taskDir, id)
			if err != nil {
				t.Errorf("acquire task db failed: %v", err)
				return
			}
			pool.ReleaseTaskDB(id)
			_ = db
		}(taskID)
	}
	wg.Wait()

	// 驱逐全部任务库
	for i := 0; i < 10; i++ {
		taskID := fmt.Sprintf("task_lock_%08d", i)
		_ = pool.EvictTaskDB(taskID)
	}

	// 调用 CleanUnusedInitLocks 清理无引用的锁
	pool.CleanUnusedInitLocks()

	// 再次调用应当安全幂等
	pool.CleanUnusedInitLocks()
}


