package storage

import (
	"container/list"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"logauditorgo/internal/model"
	"logauditorgo/pkg/logger"
)

var taskIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)

// 连接池规模与生命周期相关常量。
// KB-06: 原实现用裸 map 缓存 *gorm.DB，池满时用 `for range map` 随机挑 16 个直接 Close，
// 既没有 LRU 语义（注释写的"淘汰最早条目"从未实现），也没有引用计数——
// 正在被查询的连接会被关闭（sql: database is closed），
// 而 DEV-04 需要在删除任务前彻底关闭句柄，没有引用计数就无法安全地做这件事。
const (
	// defaultPoolMaxSize 池内允许同时驻留的最大任务库连接数
	defaultPoolMaxSize = 64
	// poolEvictTarget 池满时一次性收缩到的水位线，避免每次插入都触发淘汰
	poolEvictTarget = 48
	// poolIdleTimeout 连接空闲多久后可被 LRU 淘汰
	poolIdleTimeout = 10 * time.Minute
	// evictWaitTimeout 强制驱逐时等待引用归零的最长时间，超时则强制 Close
	evictWaitTimeout = 5 * time.Second
)

// IsValidTaskID 校验任务ID格式是否合法，防止路径遍历与注入攻击
func IsValidTaskID(taskID string) bool {
	return taskIDRegex.MatchString(taskID)
}

func isValidTaskID(taskID string) bool {
	return IsValidTaskID(taskID)
}

// TaskDBPath 返回任务库文件的绝对路径
func TaskDBPath(taskDir string, taskID string) string {
	return filepath.Join(taskDir, fmt.Sprintf("task_%s.db", taskID))
}

// dbEntry 池内单个任务库连接的运行时状态
type dbEntry struct {
	db       *gorm.DB
	refCount int32     // 原子引用计数：> 0 表示正在被使用，不可淘汰
	lastUsed time.Time // 最近一次 Acquire/Release 时间，用于 LRU 与空闲淘汰
	elem     *list.Element
	closing  bool // 已被 EvictTaskDB 移出池，等待引用归零后关闭
}

// TaskDBPool 带引用计数与 LRU 淘汰的任务库连接池。
//
// 设计要点：
//  1. Acquire/Release 必须成对调用，Release 前连接不会被淘汰或关闭；
//  2. EvictTaskDB 用于删除任务场景：先把条目移出池并标记 closing，
//     再等待引用归零（最多 evictWaitTimeout），超时则强制关闭底层 sql.DB，
//     从而保证 Windows 上 os.Remove 时不会遇到句柄占用；
//  3. 池满时按 LRU 淘汰"空闲且未被引用"的条目，永不关闭在途连接。
type TaskDBPool struct {
	mu       sync.Mutex
	maxSize  int
	idleTime time.Duration
	entries  map[string]*dbEntry
	order    *list.List // front = 最近使用
}

// NewTaskDBPool 创建任务库连接池
func NewTaskDBPool(maxSize int, idleTimeout time.Duration) *TaskDBPool {
	if maxSize <= 0 {
		maxSize = defaultPoolMaxSize
	}
	if idleTimeout <= 0 {
		idleTimeout = poolIdleTimeout
	}
	return &TaskDBPool{
		maxSize:  maxSize,
		idleTime: idleTimeout,
		entries:  make(map[string]*dbEntry),
		order:    list.New(),
	}
}

// GlobalPool 进程级全局任务库连接池
var GlobalPool = NewTaskDBPool(defaultPoolMaxSize, poolIdleTimeout)

// AcquireTaskDB 获取（或创建）任务库连接，引用计数 +1。
// 调用方必须在使用完毕后调用 ReleaseTaskDB，否则该连接永远无法被淘汰。
func (p *TaskDBPool) AcquireTaskDB(taskDir string, taskID string) (*gorm.DB, error) {
	if !isValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id: %s", taskID)
	}

	p.mu.Lock()
	if entry, ok := p.entries[taskID]; ok && !entry.closing {
		atomic.AddInt32(&entry.refCount, 1)
		entry.lastUsed = time.Now()
		p.order.MoveToFront(entry.elem)
		p.mu.Unlock()
		return entry.db, nil
	}
	p.mu.Unlock()

	// 未命中：在锁外完成建库、PRAGMA 与 AutoMigrate 等重 I/O，避免长时间持锁阻塞其他任务
	db, err := openTaskDB(taskDir, taskID)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// 双重检查：可能在建库期间已被其他 goroutine 创建
	if entry, ok := p.entries[taskID]; ok && !entry.closing {
		p.mu.Unlock()
		// 丢弃本次新建的连接，复用池中已有连接
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		atomic.AddInt32(&entry.refCount, 1)
		return entry.db, nil
	}

	// 有条目正在被驱逐（closing）时，先把它彻底清理掉，避免同一 taskID 两套句柄
	if pending, ok := p.entries[taskID]; ok {
		p.removeLocked(taskID, pending)
		p.closeEntry(pending)
	}

	p.evictIdleLocked()

	entry := &dbEntry{db: db, refCount: 1, lastUsed: time.Now()}
	entry.elem = p.order.PushFront(taskID)
	p.entries[taskID] = entry
	p.mu.Unlock()
	return db, nil
}

// ReleaseTaskDB 归还任务库连接，引用计数 -1 并刷新 LRU 位置。
// 对未知 taskID 或重复 Release 是安全的（不会把计数减成负数）。
func (p *TaskDBPool) ReleaseTaskDB(taskID string) {
	if !isValidTaskID(taskID) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, ok := p.entries[taskID]
	if !ok {
		return
	}
	entry.lastUsed = time.Now()
	if entry.elem != nil {
		p.order.MoveToFront(entry.elem)
	}
	if atomic.AddInt32(&entry.refCount, -1) < 0 {
		atomic.StoreInt32(&entry.refCount, 0)
	}
	if entry.closing && atomic.LoadInt32(&entry.refCount) <= 0 {
		p.removeLocked(taskID, entry)
		p.closeEntry(entry)
	}
}

// EvictTaskDB 强制驱逐并从磁盘删除任务库文件。
//
// DEV-04 / TASK-11: Windows 上只要还有句柄占用，os.Remove 一定失败，
// 因此必须先关闭底层 sql.DB 再删文件。这里先把条目移出池并标记 closing，
// 等引用归零（最多 5s）后关闭；超时则强制关闭，宁可让在途查询失败，
// 也不能让删除任务永久残留孤儿库文件。
func (p *TaskDBPool) EvictTaskDB(taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}

	p.mu.Lock()
	entry, ok := p.entries[taskID]
	if ok {
		entry.closing = true
		if atomic.LoadInt32(&entry.refCount) <= 0 {
			p.removeLocked(taskID, entry)
			p.mu.Unlock()
			p.closeEntry(entry)
			return nil
		}
		p.removeLocked(taskID, entry)
	}
	p.mu.Unlock()

	if !ok {
		return nil
	}

	// 等待在途引用归零
	deadline := time.Now().Add(evictWaitTimeout)
	for atomic.LoadInt32(&entry.refCount) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&entry.refCount) > 0 {
		logger.Log.Warnf("[TaskDBPool] force closing task db %s with %d in-flight reference(s)",
			taskID, atomic.LoadInt32(&entry.refCount))
	}
	p.closeEntry(entry)
	return nil
}

// CloseAll 关闭池内全部连接（停机路径使用）
func (p *TaskDBPool) CloseAll() {
	p.mu.Lock()
	snapshot := make([]*dbEntry, 0, len(p.entries))
	for id, entry := range p.entries {
		p.removeLocked(id, entry)
		snapshot = append(snapshot, entry)
	}
	p.mu.Unlock()

	for _, entry := range snapshot {
		p.closeEntry(entry)
	}
}

// Stats 返回当前池的规模快照，便于测试与排障
func (p *TaskDBPool) Stats() (size int, inUse int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, entry := range p.entries {
		size++
		if atomic.LoadInt32(&entry.refCount) > 0 {
			inUse++
		}
	}
	return size, inUse
}

// evictIdleLocked 在插入新条目之前，按 LRU 淘汰"未被引用且已空闲"的条目。
// 与旧实现的关键区别：只淘汰空闲条目，绝不关闭在途连接。
func (p *TaskDBPool) evictIdleLocked() {
	now := time.Now()
	// 至少淘汰到 poolEvictTarget，避免每次插入都触发一轮淘汰
	target := p.maxSize - 1
	if target > poolEvictTarget {
		target = poolEvictTarget
	}

	for len(p.entries) >= p.maxSize {
		victimID, victim := p.pickEvictableLocked(now)
		if victim == nil {
			return // 全部在途，宁可超限也不关闭正在使用的连接
		}
		p.removeLocked(victimID, victim)
		p.closeEntry(victim)
		if len(p.entries) <= target {
			return
		}
	}
}

// pickEvictableLocked 从 LRU 尾部（最久未使用）开始寻找可安全淘汰的条目
func (p *TaskDBPool) pickEvictableLocked(now time.Time) (string, *dbEntry) {
	for elem := p.order.Back(); elem != nil; elem = elem.Prev() {
		id, _ := elem.Value.(string)
		entry, ok := p.entries[id]
		if !ok || entry.closing {
			continue
		}
		if atomic.LoadInt32(&entry.refCount) > 0 {
			continue // 在途连接，跳过
		}
		if now.Sub(entry.lastUsed) < p.idleTime {
			// 尚未到达空闲超时：只有在池严重超限（>150%）时才提前淘汰
			if len(p.entries) < p.maxSize+p.maxSize/2 {
				return "", nil
			}
		}
		return id, entry
	}
	return "", nil
}

// removeLocked 从池与 LRU 队列中摘除条目（调用方需持有 p.mu）
func (p *TaskDBPool) removeLocked(taskID string, entry *dbEntry) {
	if entry.elem != nil {
		p.order.Remove(entry.elem)
		entry.elem = nil
	}
	delete(p.entries, taskID)
}

// closeEntry 关闭底层 sql.DB。Go 的 database/sql 允许并发调用 Close，
// 即使有在途查询也只是让它们返回 error，不会 panic。
func (p *TaskDBPool) closeEntry(entry *dbEntry) {
	if entry == nil || entry.db == nil {
		return
	}
	sqlDB, err := entry.db.DB()
	if err != nil {
		return
	}
	if err := sqlDB.Close(); err != nil {
		logger.Log.Warnf("[TaskDBPool] close task sql.DB failed: %v", err)
	}
}

// openTaskDB 打开（必要时创建）任务库并施加 SQLite 调优参数与表结构迁移
func openTaskDB(taskDir string, taskID string) (*gorm.DB, error) {
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return nil, fmt.Errorf("create task dir failed: %w", err)
	}

	dbPath := TaskDBPath(taskDir, taskID)
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open task db (%s) failed: %w", dbPath, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}

	// pragmas 统一在此处维护，避免在 DSN 与 Exec 两处重复设置且互相覆盖
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
		&model.Device{},
	); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("auto migrate task tables failed: %w", err)
	}

	// DEV-12: 补齐多设备与时序查询所需的复合索引。
	// 单列索引无法同时支撑 "device_id 过滤 + timestamp 排序"，
	// 大表上会退化为全表扫描后排序。索引创建失败不影响可用性，仅告警。
	if err := ensureTaskIndexes(db); err != nil {
		logger.Log.Warnf("[TaskDBPool] ensure task db indexes failed: %v", err)
	}

	return db, nil
}

// ensureTaskIndexes 为任务库补齐复合索引（幂等）
func ensureTaskIndexes(db *gorm.DB) error {
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_log_records_device_time ON log_records(device_id, timestamp)",
		"CREATE INDEX IF NOT EXISTS idx_log_records_device_kb ON log_records(device_id, knowledge_id)",
		"CREATE INDEX IF NOT EXISTS idx_log_records_module ON log_records(module)",
	}
	for _, stmt := range indexes {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetOrCreateTaskDB 获取或创建任务专属 SQLite 数据库。
//
// 返回值中的 *gorm.DB 已被 Acquire，调用方必须在使用完毕后调用 ReleaseTaskDB(taskID)，
// 否则该连接将永远无法被淘汰或从磁盘删除。
func GetOrCreateTaskDB(taskDir string, taskID string) (*gorm.DB, string, error) {
	db, err := GlobalPool.AcquireTaskDB(taskDir, taskID)
	if err != nil {
		return nil, "", err
	}
	return db, TaskDBPath(taskDir, taskID), nil
}

// ReleaseTaskDB 归还由 GetOrCreateTaskDB / AcquireTaskDB 获取的连接
func ReleaseTaskDB(taskID string) {
	GlobalPool.ReleaseTaskDB(taskID)
}

// CloseTaskDB 关闭并移除任务 DB 连接。
// 与 EvictTaskDB 的区别：它只关闭连接，不删除磁盘文件。
func CloseTaskDB(taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	return GlobalPool.EvictTaskDB(taskID)
}

// CloseAllTaskDBs 关闭所有任务 DB 连接，用于系统停机或资源清理
func CloseAllTaskDBs() {
	GlobalPool.CloseAll()
}

// DeleteTaskDBFiles 删除任务库物理文件（.db / -wal / -shm）。
// 调用方必须先通过 EvictTaskDB 关闭连接，否则 Windows 上必然失败。
func DeleteTaskDBFiles(taskDir string, taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	dbPath := TaskDBPath(taskDir, taskID)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task db file (%s) failed: %w", dbPath, err)
	}
	return nil
}

// DeleteTaskDB 强制驱逐连接并删除任务数据库物理文件
func DeleteTaskDB(taskDir string, taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}
	if err := GlobalPool.EvictTaskDB(taskID); err != nil {
		return err
	}
	return DeleteTaskDBFiles(taskDir, taskID)
}
