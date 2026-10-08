package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
	"logauditorgo/internal/rootcause"
	"logauditorgo/internal/storage"
	"logauditorgo/pkg/logger"
)

// rcaInstance 记录单个正在排队或执行的 RCA 分析任务实例
type rcaInstance struct {
	gen    uint64
	cancel context.CancelFunc
	done   chan struct{}
}

// RCAScheduler 统一调度管理 RCA 异步分析的并发槽位与生命周期
type RCAScheduler struct {
	mu        sync.Mutex
	semaphore chan struct{}
	instances map[string]*rcaInstance              // taskID -> 当前最新主控实例
	active    map[string]map[*rcaInstance]struct{} // taskID -> 所有活跃实例（包含等待退出的前代 worker）
	genSeq    uint64
}

// GlobalRCAScheduler 全局单例调度器
var GlobalRCAScheduler = &RCAScheduler{
	semaphore: make(chan struct{}, 2), // 默认全局最多允许 2 个任务同时执行密集 RCA
	instances: make(map[string]*rcaInstance),
	active:    make(map[string]map[*rcaInstance]struct{}),
}

// InitRCAScheduler 根据配置初始化调度器并发槽位。
//
// P2 修复：运行期调用（配置热加载、测试 cleanup）绝不允许清空 instances/active——
// 在途 worker 的 isCurrentInstance 会因此恒为 false，静默退出且不落任何终态，
// 任务永久停在 QUEUED/RUNNING，只能等重启自愈。实例表仅在完全空闲时才重置。
// 信号量替换是安全的：worker 在排队前通过 getSemaphore 取得通道快照，
// 获取与释放严格作用于同一条通道，不会出现跨通道泄漏。
func InitRCAScheduler(concurrency int) {
	if concurrency <= 0 {
		concurrency = 2
	}
	GlobalRCAScheduler.mu.Lock()
	defer GlobalRCAScheduler.mu.Unlock()
	GlobalRCAScheduler.semaphore = make(chan struct{}, concurrency)
	if len(GlobalRCAScheduler.instances) > 0 || len(GlobalRCAScheduler.active) > 0 {
		logger.Log.Warnf("[Task Service] InitRCAScheduler called while %d RCA instance(s) still in flight, keeping instance registry intact",
			len(GlobalRCAScheduler.instances))
		return
	}
	GlobalRCAScheduler.instances = make(map[string]*rcaInstance)
	GlobalRCAScheduler.active = make(map[string]map[*rcaInstance]struct{})
}

func (sch *RCAScheduler) getSemaphore() chan struct{} {
	sch.mu.Lock()
	defer sch.mu.Unlock()
	return sch.semaphore
}

func (sch *RCAScheduler) isCurrentInstance(taskID string, inst *rcaInstance) bool {
	if inst == nil {
		return false
	}
	sch.mu.Lock()
	defer sch.mu.Unlock()
	return sch.instances[taskID] == inst
}

// updateTaskRCAState 增量更新任务的 RCA 独立状态字段到全局库和任务库
func (s *Service) updateTaskRCAState(taskID string, status model.RCAStatus, errMsg string, analyzedUntil *time.Time, rcaCount ...int) {
	now := time.Now()
	updates := map[string]interface{}{
		"rca_status":        status,
		"rca_error_message": errMsg,
	}

	if status == model.RCAStatusQueued {
		// 重新排队分析时，重置旧指标与时间戳 (体验与状态一致性)
		updates["rca_finish_time"] = nil
		updates["rca_analyzed_until"] = nil
		updates["rca_count"] = 0
	} else if status == model.RCAStatusCompleted || status == model.RCAStatusTimeout || status == model.RCAStatusFailed {
		updates["rca_finish_time"] = &now
	}

	if analyzedUntil != nil && !analyzedUntil.IsZero() {
		updates["rca_analyzed_until"] = analyzedUntil
	}
	if len(rcaCount) > 0 {
		updates["rca_count"] = rcaCount[0]
	}

	// 1. 更新全局库
	if s.globalDB != nil {
		res := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates)
		if res.Error != nil {
			logger.Log.Errorf("[Task Service] update rca state in global db failed for %s: %v", taskID, res.Error)
		}
		if res.RowsAffected == 0 {
			// 全局库已无该任务（已被删除），直接退出，绝不调用 GetOrCreateTaskDB 复活任务库物理文件
			return
		}
	}

	// 2. 更新任务库（若磁盘物理文件不存在，绝不调用 GetOrCreateTaskDB 重建空库）
	if _, statErr := os.Stat(storage.TaskDBPath(s.taskDir, taskID)); statErr != nil {
		return
	}

	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err == nil {
		if err := taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil {
			logger.Log.Errorf("[Task Service] update rca state in task db failed for %s: %v", taskID, err)
		}
		storage.ReleaseTaskDB(taskID)
	}
}

// CancelTaskRCA 取消指定任务正在排队或执行的 RCA 分析任务，并等待所有活跃 worker 完全退出
func (s *Service) CancelTaskRCA(taskID string) {
	GlobalRCAScheduler.mu.Lock()
	var toWait []chan struct{}
	if set, ok := GlobalRCAScheduler.active[taskID]; ok {
		for inst := range set {
			inst.cancel()
			toWait = append(toWait, inst.done)
		}
	}
	GlobalRCAScheduler.mu.Unlock()

	for _, doneCh := range toWait {
		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			logger.Log.Warnf("[Task Service] CancelTaskRCA wait for worker exit timed out for task %s", taskID)
		}
	}
}

// TriggerTaskRCA 触发后台异步 RCA 分析任务
func (s *Service) TriggerTaskRCA(taskID string, forceReanalyze bool) {
	if !isValidTaskID(taskID) {
		return
	}

	// 1. 单飞控制前置：在此处立即检查并登记实例，排队与计算均纳入视野
	GlobalRCAScheduler.mu.Lock()
	currentInst, hasCurrent := GlobalRCAScheduler.instances[taskID]
	if hasCurrent {
		if !forceReanalyze {
			GlobalRCAScheduler.mu.Unlock()
			logger.Log.Infof("[Task Service] Task %s RCA already queued/running, skipping duplicate trigger", taskID)
			return
		}
		// 强制重新分析：取消旧 worker 实例
		logger.Log.Infof("[Task Service] Force reanalyzing task %s, canceling prior worker gen=%d", taskID, currentInst.gen)
		currentInst.cancel()
	}

	GlobalRCAScheduler.genSeq++
	inst := &rcaInstance{
		gen:  GlobalRCAScheduler.genSeq,
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel

	GlobalRCAScheduler.instances[taskID] = inst
	if GlobalRCAScheduler.active[taskID] == nil {
		GlobalRCAScheduler.active[taskID] = make(map[*rcaInstance]struct{})
	}
	GlobalRCAScheduler.active[taskID][inst] = struct{}{}
	GlobalRCAScheduler.mu.Unlock()

	go func() {
		defer func() {
			close(inst.done)
			GlobalRCAScheduler.mu.Lock()
			if GlobalRCAScheduler.instances[taskID] == inst {
				delete(GlobalRCAScheduler.instances, taskID)
			}
			if set, ok := GlobalRCAScheduler.active[taskID]; ok {
				delete(set, inst)
				if len(set) == 0 {
					delete(GlobalRCAScheduler.active, taskID)
				}
			}
			GlobalRCAScheduler.mu.Unlock()
		}()

		// 若存在被取代的前代实例，等待其完全退出后再执行后续步骤，杜绝两代 worker 并发
		if currentInst != nil {
			select {
			case <-currentInst.done:
			case <-ctx.Done():
				return
			}
		}

		// 检查是否在启动前已被新实例取消
		if ctx.Err() == context.Canceled || !GlobalRCAScheduler.isCurrentInstance(taskID, inst) {
			return
		}

		// 顺序置状态为 QUEUED
		s.updateTaskRCAState(taskID, model.RCAStatusQueued, "", nil)

		totalTimeout := 10 * time.Minute
		calcTimeout := 5 * time.Minute
		if config.GlobalConfig != nil && config.GlobalConfig.RCA.Timeout > 0 {
			calcTimeout = time.Duration(config.GlobalConfig.RCA.Timeout) * time.Second
			totalTimeout = calcTimeout + 5*time.Minute
		}

		queueCtx, queueCancel := context.WithTimeout(ctx, totalTimeout)
		defer queueCancel()

		// 获取当前信号量通道引用，保证释放时与获取时 channel 严格一致
		sem := GlobalRCAScheduler.getSemaphore()

		// 排队等待令牌
		select {
		case <-queueCtx.Done():
			if ctx.Err() == context.Canceled || !GlobalRCAScheduler.isCurrentInstance(taskID, inst) {
				logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled during queueing", taskID, inst.gen)
				return
			}
			s.updateTaskRCAState(taskID, model.RCAStatusTimeout, "排队超时未获取到计算资源", nil)
			return
		case sem <- struct{}{}:
			defer func() { <-sem }()
		}

		// 检查是否已被新实例取代
		if ctx.Err() == context.Canceled || !GlobalRCAScheduler.isCurrentInstance(taskID, inst) {
			logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled before starting calculation", taskID, inst.gen)
			return
		}

		calcCtx, calcCancel := context.WithTimeout(ctx, calcTimeout)
		defer calcCancel()

		// 状态变更为 RUNNING
		s.updateTaskRCAState(taskID, model.RCAStatusRunning, "", nil)

		s.executeRCAPipeline(calcCtx, taskID, inst)
	}()
}

// fetchNormLogsForRCA 使用 keyset 复合游标 (timestamp, id) 分页拉取待分析日志，
// 批次间自动释放连接，彻底消除长游标扫描对前台连接池的占用。
//
// 游标语义说明：样本按 (timestamp, id) 升序截取，多文件乱序导入时
// 保证"时间最早的 N 行"而非"入库顺序最早的 N 行"（后者会让截断样本不可控）。
// 支撑索引 idx_log_records_time_id 由 ensureTaskIndexes 维护。
func fetchNormLogsForRCA(ctx context.Context, taskDB *gorm.DB) ([]*model.NormalizedLog, bool, error) {
	const batchSize = 5000
	var list []*model.NormalizedLog
	var lastTS time.Time
	var lastID uint
	truncated := false

	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		default:
		}

		limit := batchSize
		if len(list)+limit > maxRCALogs {
			limit = maxRCALogs - len(list)
		}

		query := taskDB.WithContext(ctx).Model(&model.LogRecord{}).
			Where("(knowledge_id > 0 OR severity <= ?)", rcaSeverityThreshold)
		if !lastTS.IsZero() {
			query = query.Where("(timestamp > ? OR (timestamp = ? AND id > ?))", lastTS, lastTS, lastID)
		}

		var records []model.LogRecord
		if err := query.Order("timestamp asc, id asc").Limit(limit).Find(&records).Error; err != nil {
			return nil, false, err
		}

		if len(records) == 0 {
			break
		}

		for _, rec := range records {
			// 游标必须取本批最后一条记录的 (timestamp, id)。
			// 复合排序下"批内最大 id" ≠ "尾行 id"（入库顺序与时间顺序无关），
			// 取最大 id 会把同时间戳的未读行错误排除在下一批之外
			lastTS = rec.Timestamp
			lastID = rec.ID
			var params map[string]string
			if rec.ParametersJSON != "" {
				_ = json.Unmarshal([]byte(rec.ParametersJSON), &params)
			}
			list = append(list, &model.NormalizedLog{
				ID:              rec.ID,
				RawLog:          rec.RawLog,
				Timestamp:       rec.Timestamp,
				Hostname:        rec.Hostname,
				Module:          rec.Module,
				Severity:        rec.Severity,
				Brief:           rec.Brief,
				SlotInfo:        rec.SlotInfo,
				SourceFile:      rec.SourceFile,
				MessageBody:     rec.MessageBody,
				Parameters:      params,
				DeviceID:        rec.DeviceID,
				KnowledgeID:     rec.KnowledgeID,
				MatchTier:       rec.MatchTier,
				MatchConfidence: rec.MatchConfidence,
			})
		}

		if len(list) >= maxRCALogs {
			// 截断探测：LIMIT 1 的存在性探测。
			// 此前用 Count() 探测，聚合函数不受 LIMIT 约束，命中上限时会额外全尾扫描一遍。
			var probe []uint
			_ = taskDB.WithContext(ctx).Model(&model.LogRecord{}).
				Where("(knowledge_id > 0 OR severity <= ?) AND (timestamp > ? OR (timestamp = ? AND id > ?))",
					rcaSeverityThreshold, lastTS, lastTS, lastID).
				Limit(1).
				Pluck("id", &probe).Error
			if len(probe) > 0 {
				truncated = true
				logger.Log.Warnf("[Task Service] RCA sample size exceeded max limit (%d), truncating subsequent records", maxRCALogs)
			}
			break
		}

		if len(records) < limit {
			break
		}
	}

	return list, truncated, nil
}

// rcaTargetAlive 确认任务仍存活于全局库且任务库物理文件未被并发删除。
// 收敛 executeRCAPipeline 中分散的存在性守卫（3 处 os.Stat + 2 处 Count），
// stage 仅用于区分日志语境。
func (s *Service) rcaTargetAlive(taskID string, stage string) bool {
	if s.globalDB != nil {
		var exists int64
		if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Count(&exists).Error; err != nil || exists == 0 {
			logger.Log.Infof("[Task Service] Task %s not found in global db (%s), skipping RCA pipeline", taskID, stage)
			return false
		}
	}
	if _, statErr := os.Stat(storage.TaskDBPath(s.taskDir, taskID)); statErr != nil {
		logger.Log.Infof("[Task Service] Task %s db file does not exist (%s), skipping RCA pipeline", taskID, stage)
		return false
	}
	return true
}

// executeRCAPipeline 执行 RCA 分析核心流水线与短事务落库
func (s *Service) executeRCAPipeline(ctx context.Context, taskID string, inst *rcaInstance) {
	// 阶段 1 前置检查：任务是否已在全局库中删除，或物理库已被移除
	if !s.rcaTargetAlive(taskID, "pre-check") {
		return
	}

	// 阶段 1: keyset 分页拉取待分析数据，批次间自动释放连接，拉取完毕后立即释放连接句柄
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		logger.Log.Errorf("[Task Service] Open task db for RCA failed: %v", err)
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, fmt.Sprintf("打开任务数据库失败: %v", err), nil)
		return
	}

	normLogs, fetchTruncated, fetchErr := fetchNormLogsForRCA(ctx, taskDB)
	storage.ReleaseTaskDB(taskID) // 立即归还连接句柄，杜绝计算密集期阻塞前台

	// 检查点：是否已被新触发实例取代或取消
	if !GlobalRCAScheduler.isCurrentInstance(taskID, inst) || ctx.Err() == context.Canceled {
		logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled/superseded after fetch", taskID, inst.gen)
		return
	}

	if fetchErr != nil {
		if errors.Is(fetchErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) ||
			!GlobalRCAScheduler.isCurrentInstance(taskID, inst) || storage.IsTaskDBClosed(fetchErr) {
			logger.Log.Infof("[Task Service] RCA fetch canceled/aborted for task %s", taskID)
			return
		}
		if errors.Is(fetchErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.updateTaskRCAState(taskID, model.RCAStatusTimeout, "读取日志记录超时", nil)
			return
		}
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, fmt.Sprintf("读取分析日志失败: %v", fetchErr), nil)
		return
	}

	if len(normLogs) == 0 || s.rcaEngine == nil {
		s.updateTaskRCAState(taskID, model.RCAStatusCompleted, "", nil, 0)
		return
	}

	// 阶段 2: 密集拓扑计算 (纯内存计算，不占数据库连接)
	res := s.rcaEngine.AnalyzeWithContext(ctx, normLogs, 300)

	// 第二道防线（P0）：无论引擎内部是否正确标记截断，只要计算上下文已超时，
	// 一律按截断处理，杜绝引擎遗漏时把"超时"当作"完整完成"落库
	if ctx.Err() != nil && !res.Truncated {
		res.Truncated = true
		if res.Err == nil {
			res.Err = ctx.Err()
		}
	}

	// 检查点：是否已被新触发实例取代或取消
	if !GlobalRCAScheduler.isCurrentInstance(taskID, inst) || ctx.Err() == context.Canceled {
		logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled/superseded after analysis", taskID, inst.gen)
		return
	}

	// 阶段 3 前置检查：再次确认物理文件与全局任务未被并发删除
	if !s.rcaTargetAlive(taskID, "post-calculation") {
		return
	}

	// 阶段 3: 短事务落库 (耗时 < 20ms)
	writeDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err != nil {
		logger.Log.Errorf("[Task Service] Open task db for saving RCA failed: %v", err)
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, fmt.Sprintf("写入分析结果失败: %v", err), nil)
		return
	}
	defer storage.ReleaseTaskDB(taskID)

	// 检查点：写入事务前二次确认代数有效性
	if !GlobalRCAScheduler.isCurrentInstance(taskID, inst) || ctx.Err() == context.Canceled {
		logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled/superseded before DB transaction", taskID, inst.gen)
		return
	}

	txErr := writeDB.Transaction(func(tx *gorm.DB) error {
		if delErr := tx.Where("id > 0").Delete(&model.RCAEvent{}).Error; delErr != nil {
			return fmt.Errorf("delete old RCA events failed: %w", delErr)
		}
		if len(res.Events) > 0 {
			if createErr := tx.Create(&res.Events).Error; createErr != nil {
				return fmt.Errorf("create RCA events failed: %w", createErr)
			}
		}
		return nil
	})

	if txErr != nil {
		logger.Log.Errorf("[Task Service] Save RCA events failed for task %s: %v", taskID, txErr)
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, fmt.Sprintf("保存分析结果失败: %v", txErr), nil)
		return
	}

	// 检查点：状态更新前确认代数有效性
	if !GlobalRCAScheduler.isCurrentInstance(taskID, inst) || ctx.Err() == context.Canceled {
		logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled/superseded before status update", taskID, inst.gen)
		return
	}

	var analyzedUntil *time.Time
	if !res.AnalyzedUntil.IsZero() {
		analyzedUntil = &res.AnalyzedUntil
	}

	if res.Truncated || fetchTruncated {
		// 三类截断分别给出准确文案：样本超限 / 事件数触顶 / 超时中断
		errMsg := "分析达到超时上限，已保存部分结果"
		switch {
		case fetchTruncated:
			errMsg = fmt.Sprintf("分析样本达到硬上限(%d行)，已保存前%d行分析结果", maxRCALogs, maxRCALogs)
		case errors.Is(res.Err, rootcause.ErrEventCap):
			errMsg = fmt.Sprintf("根因事件数量达到单次分析上限，已保存前%d条结果，可通过缩小日志范围后重跑", len(res.Events))
		case res.Err != nil && !errors.Is(res.Err, context.DeadlineExceeded) && !errors.Is(res.Err, context.Canceled):
			errMsg = fmt.Sprintf("分析超时中断: %v", res.Err)
		}
		s.updateTaskRCAState(taskID, model.RCAStatusTimeout, errMsg, analyzedUntil, len(res.Events))
		logger.Log.Warnf("[Task Service] RCA finished with TIMEOUT for task %s: %d events saved (fetchTruncated=%v, err=%v)",
			taskID, len(res.Events), fetchTruncated, res.Err)
	} else if res.Err != nil {
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, res.Err.Error(), analyzedUntil, len(res.Events))
		logger.Log.Errorf("[Task Service] RCA failed for task %s: %v", taskID, res.Err)
	} else {
		s.updateTaskRCAState(taskID, model.RCAStatusCompleted, "", analyzedUntil, len(res.Events))
		logger.Log.Infof("[Task Service] RCA completed successfully for task %s: %d events", taskID, len(res.Events))
	}
}

// MarkRCAQueuedSync 同步把任务 RCA 状态落库为 QUEUED。
//
// P2 修复：TriggerTaskRCA 把 QUEUED 写入移到了 worker goroutine 内（且需等前代实例退出），
// 而 ReanalyzeRCA 接口已同步返回 rca_status: QUEUED——存在窗口期"接口说 QUEUED、库里还是旧终态"，
// 前端"重跑 RCA"按钮的 disabled 判据会短暂失效。API 层在触发后调用本方法立即落库，
// 保证接口返回时数据库已处于 QUEUED/RUNNING 等进行中状态。
func (s *Service) MarkRCAQueuedSync(taskID string) {
	if !isValidTaskID(taskID) {
		return
	}
	s.updateTaskRCAState(taskID, model.RCAStatusQueued, "", nil)
}

// WaitForTaskRCA 等待指定任务的 RCA 计算结束（供测试或单步调用使用）
func (s *Service) WaitForTaskRCA(taskID string, timeout time.Duration) bool {
	GlobalRCAScheduler.mu.Lock()
	inst, ok := GlobalRCAScheduler.instances[taskID]
	GlobalRCAScheduler.mu.Unlock()

	if !ok {
		// 检查数据库状态，若为 QUEUED 或 RUNNING，说明有任务正在瞬态排队，不能直接返回假成功
		taskInfo, err := s.GetTaskByID(taskID)
		if err == nil && (taskInfo.RCAStatus == model.RCAStatusQueued || taskInfo.RCAStatus == model.RCAStatusRunning) {
			return false
		}
		return true
	}
	select {
	case <-inst.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// ExecuteRCAPipelineForTest 供单元与集成测试直接验证流水线在特定 Context 下的行为
func (s *Service) ExecuteRCAPipelineForTest(ctx context.Context, taskID string) {
	// cancel 必须非 nil：一旦与 DeleteTask / force 重触发并发，
	// CancelTaskRCA / TriggerTaskRCA 对该实例调用 inst.cancel() 会直接 nil panic
	dummyInst := &rcaInstance{gen: 0, cancel: func() {}, done: make(chan struct{})}
	defer close(dummyInst.done)

	GlobalRCAScheduler.mu.Lock()
	GlobalRCAScheduler.instances[taskID] = dummyInst
	if GlobalRCAScheduler.active[taskID] == nil {
		GlobalRCAScheduler.active[taskID] = make(map[*rcaInstance]struct{})
	}
	GlobalRCAScheduler.active[taskID][dummyInst] = struct{}{}
	GlobalRCAScheduler.mu.Unlock()

	defer func() {
		GlobalRCAScheduler.mu.Lock()
		if GlobalRCAScheduler.instances[taskID] == dummyInst {
			delete(GlobalRCAScheduler.instances, taskID)
		}
		if set, ok := GlobalRCAScheduler.active[taskID]; ok {
			delete(set, dummyInst)
			if len(set) == 0 {
				delete(GlobalRCAScheduler.active, taskID)
			}
		}
		GlobalRCAScheduler.mu.Unlock()
	}()

	s.executeRCAPipeline(ctx, taskID, dummyInst)
}

// RecoverDanglingRCATasks 启动自愈：扫描全局库中处于 QUEUED 或 RUNNING 的任务，
// 统一自愈为 FAILED，避免服务重启后任务永久悬挂
func (s *Service) RecoverDanglingRCATasks() error {
	if s.globalDB == nil {
		return nil
	}
	var dangling []model.TaskInfo
	if err := s.globalDB.Where("rca_status IN ?", []string{string(model.RCAStatusQueued), string(model.RCAStatusRunning)}).Find(&dangling).Error; err != nil {
		return err
	}
	if len(dangling) == 0 {
		return nil
	}

	now := time.Now()
	errMsg := "服务重启中断，请手动点击重新分析"
	for _, t := range dangling {
		logger.Log.Warnf("[Task Service] Recovering dangling RCA task %s from status %s to FAILED", t.TaskID, t.RCAStatus)
		_ = s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", t.TaskID).Updates(map[string]interface{}{
			"rca_status":        model.RCAStatusFailed,
			"rca_error_message": errMsg,
			"rca_finish_time":   &now,
		}).Error

		// 开库前先检查物理文件是否存在，避免对已删除任务反向创建空库
		if _, statErr := os.Stat(storage.TaskDBPath(s.taskDir, t.TaskID)); statErr == nil {
			taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, t.TaskID)
			if err == nil {
				_ = taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", t.TaskID).Updates(map[string]interface{}{
					"rca_status":        model.RCAStatusFailed,
					"rca_error_message": errMsg,
					"rca_finish_time":   &now,
				}).Error
				storage.ReleaseTaskDB(t.TaskID)
			}
		}
	}
	return nil
}
