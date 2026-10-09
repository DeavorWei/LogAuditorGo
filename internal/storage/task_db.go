package storage

import (
	"container/list"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// ErrTaskDBClosed 任务库连接已被驱逐/关闭的哨兵错误。
//
// EvictTaskDB 强制关闭在途连接后，正在执行的查询收到底层驱动错误
// （"database is closed" / sql.ErrConnDone），驱动错误无法改写为哨兵本身，
// 因此 IsTaskDBClosed 采用 errors.Is + 驱动错误特征双保险判定，
// 取代调用方各自 strings.Contains(err, "closed") 的脆匹配。
var ErrTaskDBClosed = errors.New("task db closed or evicted")

// IsTaskDBClosed 判定错误是否由任务库连接被驱逐/关闭引起
func IsTaskDBClosed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTaskDBClosed) || errors.Is(err, sql.ErrConnDone) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is closed") ||
		strings.Contains(msg, "sql: connection is already closed")
}

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
	mu        sync.Mutex
	maxSize   int
	idleTime  time.Duration
	entries   map[string]*dbEntry
	order     *list.List // front = 最近使用
	initLocks sync.Map   // taskID -> *sync.Mutex: 串行化同一任务库的打开与 AutoMigrate，杜绝并发建库冲突
}

func (p *TaskDBPool) getInitLock(taskID string) *sync.Mutex {
	actual, _ := p.initLocks.LoadOrStore(taskID, &sync.Mutex{})
	return actual.(*sync.Mutex)
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

	// 未命中缓存：按 taskID 获取互斥锁，确保同一任务库在任何时刻只有一个 goroutine 执行 openTaskDB / AutoMigrate
	initLock := p.getInitLock(taskID)
	initLock.Lock()
	defer initLock.Unlock()

	// 获取锁后进行二次检查（排在前方的并发 goroutine 可能已经完成初始化并入池）
	p.mu.Lock()
	if entry, ok := p.entries[taskID]; ok && !entry.closing {
		atomic.AddInt32(&entry.refCount, 1)
		entry.lastUsed = time.Now()
		p.order.MoveToFront(entry.elem)
		p.mu.Unlock()
		return entry.db, nil
	}
	p.mu.Unlock()

	// 此时持有该 taskID 的互斥锁，安全地在锁外完成建库、PRAGMA 与 AutoMigrate
	db, err := openTaskDB(taskDir, taskID)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// 有条目正在被驱逐（closing）时，先把它彻底清理掉，避免同一 taskID 两套句柄
	if pending, ok := p.entries[taskID]; ok {
		p.removeLocked(taskID, pending)
		p.closeEntry(pending)
	}

	evicted := p.evictIdleLocked()

	entry := &dbEntry{db: db, refCount: 1, lastUsed: time.Now()}
	entry.elem = p.order.PushFront(taskID)
	p.entries[taskID] = entry
	p.mu.Unlock()

	// 发生 LRU 淘汰后，在锁外顺便回收已不在池中的锁对象，防止 initLocks 内存膨胀
	if evicted {
		p.CleanUnusedInitLocks()
	}

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
	curRef := atomic.AddInt32(&entry.refCount, -1)
	if curRef < 0 {
		atomic.StoreInt32(&entry.refCount, 0)
		curRef = 0
	}
	if entry.closing && curRef <= 0 {
		p.removeLocked(taskID, entry)
		p.closeEntry(entry)
	}
}

// EvictTaskDB 强制驱逐并从磁盘删除任务库文件。
//
// DEV-04 / TASK-11: Windows 上只要还有句柄占用，os.Remove 一定失败，
// 因此必须先关闭底层 sql.DB 再删文件。这里先把条目标记 closing，
// 等引用归零（最多 5s）后由 ReleaseTaskDB 或本方法关闭；超时则强制关闭，宁可让在途查询失败，
// 也不能让删除任务永久残留孤儿库文件。
func (p *TaskDBPool) EvictTaskDB(taskID string) error {
	if !isValidTaskID(taskID) {
		return fmt.Errorf("invalid task id: %s", taskID)
	}

	// 互斥正在进行中的建库/迁移 (AcquireTaskDB)，防止删库与建库并发导致幽灵连接入池
	initLock := p.getInitLock(taskID)
	initLock.Lock()
	defer func() {
		p.initLocks.Delete(taskID)
		initLock.Unlock()
	}()

	p.mu.Lock()
	entry, ok := p.entries[taskID]
	if !ok {
		p.mu.Unlock()
		return nil
	}

	entry.closing = true
	if atomic.LoadInt32(&entry.refCount) <= 0 {
		p.removeLocked(taskID, entry)
		p.mu.Unlock()
		p.closeEntry(entry)
		return nil
	}
	p.mu.Unlock()

	// 等待在途引用归零（ReleaseTaskDB 会在归零时关闭，或超时兜底强制关闭）
	deadline := time.Now().Add(evictWaitTimeout)
	for atomic.LoadInt32(&entry.refCount) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	p.mu.Lock()
	if _, stillInPool := p.entries[taskID]; stillInPool {
		p.removeLocked(taskID, entry)
		p.mu.Unlock()
		if atomic.LoadInt32(&entry.refCount) > 0 {
			logger.Log.Warnf("[TaskDBPool] force closing task db %s with %d in-flight reference(s)",
				taskID, atomic.LoadInt32(&entry.refCount))
		}
		p.closeEntry(entry)
	} else {
		p.mu.Unlock()
	}
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

	p.initLocks.Range(func(key, _ any) bool {
		p.initLocks.Delete(key)
		return true
	})
}

// CleanUnusedInitLocks 扫描并清理已不在池中且当前无竞争的初始化锁
func (p *TaskDBPool) CleanUnusedInitLocks() {
	p.initLocks.Range(func(key, value any) bool {
		taskID, ok := key.(string)
		if !ok {
			return true
		}
		lock, ok := value.(*sync.Mutex)
		if !ok {
			return true
		}

		// 若当前有协程持有该锁（TryLock 失败），说明正在建库或驱逐中，跳过
		if !lock.TryLock() {
			return true
		}
		defer lock.Unlock()

		p.mu.Lock()
		_, inPool := p.entries[taskID]
		p.mu.Unlock()

		// 不在池中且没有在途竞争，可以安全释放锁对象
		if !inPool {
			p.initLocks.Delete(taskID)
		}
		return true
	})
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
// 与旧实现的关键区别：只淘汰空闲条目，绝不关闭在途连接。返回是否实际淘汰了条目。
func (p *TaskDBPool) evictIdleLocked() bool {
	now := time.Now()
	// 至少淘汰到 poolEvictTarget，避免每次插入都触发一轮淘汰
	target := p.maxSize - 1
	if target > poolEvictTarget {
		target = poolEvictTarget
	}

	evicted := false
	for len(p.entries) >= p.maxSize {
		victimID, victim := p.pickEvictableLocked(now)
		if victim == nil {
			return evicted // 全部在途，宁可超限也不关闭正在使用的连接
		}
		p.removeLocked(victimID, victim)
		p.closeEntry(victim)
		evicted = true
		if len(p.entries) <= target {
			return evicted
		}
	}
	return evicted
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

// taskDBDSN 构造任务库的连接 DSN。
//
// P1 修复：PRAGMA 是连接级设置，`sqlDB.Exec` 只作用于当时取到的那一条连接，
// 连接池扩到 4 条后，第 2/3/4 条连接会带着默认值出生（synchronous=FULL、
// foreign_keys=off、cache_size 2MB）。这里把全部 PRAGMA 迁入 DSN，
// 驱动在 newConn→applyQueryParams 中对每一条新连接逐个应用（已核实 glebarez/go-sqlite v1.21.2）。
//
// _txlock=immediate 让写事务在 BEGIN 阶段即取写锁：deferred 事务升级写锁时
// 遇到并发写会立刻返回 SQLITE_BUSY_SNAPSHOT（该错误不触发 busy handler），
// immediate + busy_timeout 则退化为"排队等待"而非"立即失败"。
func taskDBDSN(dbPath string) string {
	return dbPath +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=cache_size(-32000)" + // 32MB 页缓存
		"&_txlock=immediate"
}

// openTaskDB 打开（必要时创建）任务库并施加 SQLite 调优参数与表结构迁移
func openTaskDB(taskDir string, taskID string) (*gorm.DB, error) {
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return nil, fmt.Errorf("create task dir failed: %w", err)
	}

	dbPath := TaskDBPath(taskDir, taskID)
	db, err := gorm.Open(sqlite.Open(taskDBDSN(dbPath)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open task db (%s) failed: %w", dbPath, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB failed: %w", err)
	}

	sqlDB.SetMaxOpenConns(4) // WAL 模式下多读单写安全，前后台并发隔离杜绝排队
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	// 迁移前防御处理：
	// 1. 解决历史孤儿数据导致的 FOREIGN KEY 约束校验失败 (P0)：
	// 若已存在 log_tag_relations 表，在 AutoMigrate 重建表前先执行一次性孤儿自愈清理，
	// 避免 GORM 在 SQLite 下重建表做外键校验时触发 `FOREIGN KEY constraint failed (787)` 导致迁移回滚。
	if db.Migrator().HasTable("log_tag_relations") {
		_ = db.Exec("DELETE FROM log_tag_relations WHERE log_id NOT IN (SELECT id FROM log_records)").Error
		_ = db.Exec("DELETE FROM log_tag_relations WHERE tag_id NOT IN (SELECT id FROM log_tags)").Error
	}

	// 2. 避免级联删除触发器与表结构重建冲突 (P1)：
	// trg_cascade_delete_log_tag_rel 挂在 log_records 上但引用 log_tag_relations，
	// GORM 在对 log_tag_relations 执行整表重建（rename/drop）时会因触发器引用校验报错：
	// `SQL logic error: error in trigger trg_cascade_delete_log_tag_rel: no such table: main.log_tag_relations`。
	// 因此在迁移前先 DROP 触发器，迁移完成后再由 ensureTaskTriggers 重建。
	_ = db.Exec("DROP TRIGGER IF EXISTS trg_cascade_delete_log_tag_rel").Error
	_ = db.Exec("DROP TRIGGER IF EXISTS trg_cascade_delete_tag_tag_rel").Error

	// 自动迁移任务专属表
	if err := db.AutoMigrate(
		&model.TaskInfo{},
		&model.TaskFile{},
		&model.LogRecord{},
		&model.RCAEvent{},
		&model.Device{},
		&model.LogTag{},
		&model.LogTagRelation{},
	); err != nil {
		if strings.Contains(err.Error(), "duplicate column name") {
			logger.Log.Warnf("[openTaskDB] task %s auto migrate encountered duplicate column, safely ignored: %v", taskID, err)
		} else if strings.Contains(err.Error(), "constraint failed") || strings.Contains(err.Error(), "FOREIGN KEY") {
			// 若极端情况下仍触发约束错误，记录警告并在关闭外键校验下尝试兼容降级迁移，绝不阻断任务访问
			logger.Log.Warnf("[openTaskDB] task %s auto migrate encountered constraint warning: %v, attempting tolerant migration", taskID, err)
			_ = db.Exec("PRAGMA foreign_keys = OFF").Error
			retryErr := db.AutoMigrate(
				&model.TaskInfo{},
				&model.TaskFile{},
				&model.LogRecord{},
				&model.RCAEvent{},
				&model.Device{},
				&model.LogTag{},
				&model.LogTagRelation{},
			)
			_ = db.Exec("PRAGMA foreign_keys = ON").Error
			if retryErr != nil && !strings.Contains(retryErr.Error(), "duplicate column name") {
				_ = sqlDB.Close()
				return nil, fmt.Errorf("auto migrate task tables failed: %w", retryErr)
			}
		} else {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("auto migrate task tables failed: %w", err)
		}
	}

	// DEV-12: 补齐多设备与时序查询所需的复合索引。
	// 单列索引无法同时支撑 "device_id 过滤 + timestamp 排序"，
	// 大表上会退化为全表扫描后排序。索引创建失败不影响可用性，仅告警。
	if err := ensureTaskIndexes(db); err != nil {
		logger.Log.Warnf("[TaskDBPool] ensure task db indexes failed: %v", err)
	}

	// 建立 SQLite 级联删除触发器，作为外键级联的双保险防线
	// 无论通过任何路径删除 log_records 或 log_tags，均由引擎层级自动级联删除关联关系，杜绝孤儿记录
	if err := ensureTaskTriggers(db); err != nil {
		logger.Log.Warnf("[TaskDBPool] ensure task cascade triggers failed: %v", err)
	}

	return db, nil
}

// ensureTaskIndexes 为任务库补齐复合索引（幂等）
func ensureTaskIndexes(db *gorm.DB) error {
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_log_records_device_time ON log_records(device_id, timestamp)",
		"CREATE INDEX IF NOT EXISTS idx_log_records_device_kb ON log_records(device_id, knowledge_id)",
		"CREATE INDEX IF NOT EXISTS idx_log_records_module ON log_records(module)",
		// RCA keyset 分页游标 (timestamp, id) 的支撑索引，
		// 保证 "WHERE (timestamp > ? OR (timestamp = ? AND id > ?)) ORDER BY timestamp, id" 不退化为全表排序
		"CREATE INDEX IF NOT EXISTS idx_log_records_time_id ON log_records(timestamp, id)",
	}
	for _, stmt := range indexes {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// ensureTaskTriggers 为任务库建立级联删除触发器（幂等）
func ensureTaskTriggers(db *gorm.DB) error {
	triggers := []string{
		// 当 log_records 行被物理删除时，自动级联清理其标签关联
		`CREATE TRIGGER IF NOT EXISTS trg_cascade_delete_log_tag_rel
		AFTER DELETE ON log_records
		BEGIN
			DELETE FROM log_tag_relations WHERE log_id = OLD.id;
		END;`,
		// 当 log_tags 行被删除时，自动级联清理其关联关系
		`CREATE TRIGGER IF NOT EXISTS trg_cascade_delete_tag_tag_rel
		AFTER DELETE ON log_tags
		BEGIN
			DELETE FROM log_tag_relations WHERE tag_id = OLD.id;
		END;`,
	}
	for _, stmt := range triggers {
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

	var lastErr error
	for i := 0; i < 10; i++ {
		lastErr = os.Remove(dbPath)
		if lastErr == nil || os.IsNotExist(lastErr) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("remove task db file (%s) failed: %w", dbPath, lastErr)
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
