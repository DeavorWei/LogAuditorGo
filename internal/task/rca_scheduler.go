package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gorm.io/gorm"

	"logauditorgo/internal/config"
	"logauditorgo/internal/model"
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
	instances sync.Map // taskID (string) -> *rcaInstance
	genSeq    uint64
}

// GlobalRCAScheduler 全局单例调度器
var GlobalRCAScheduler = &RCAScheduler{
	semaphore: make(chan struct{}, 2), // 默认全局最多允许 2 个任务同时执行密集 RCA
}

// InitRCAScheduler 根据配置初始化调度器并发槽位
func InitRCAScheduler(concurrency int) {
	if concurrency <= 0 {
		concurrency = 2
	}
	GlobalRCAScheduler.mu.Lock()
	defer GlobalRCAScheduler.mu.Unlock()
	GlobalRCAScheduler.semaphore = make(chan struct{}, concurrency)
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
	val, ok := sch.instances.Load(taskID)
	return ok && val.(*rcaInstance) == inst
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
		if err := s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil {
			logger.Log.Errorf("[Task Service] update rca state in global db failed for %s: %v", taskID, err)
		}
	}

	// 2. 更新任务库
	taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
	if err == nil {
		if err := taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updates).Error; err != nil {
			logger.Log.Errorf("[Task Service] update rca state in task db failed for %s: %v", taskID, err)
		}
		storage.ReleaseTaskDB(taskID)
	}
}

// CancelTaskRCA 取消指定任务正在排队或执行的 RCA 分析任务
func (s *Service) CancelTaskRCA(taskID string) {
	if val, ok := GlobalRCAScheduler.instances.Load(taskID); ok {
		if inst, ok := val.(*rcaInstance); ok {
			inst.cancel()
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
	existingVal, hasExisting := GlobalRCAScheduler.instances.Load(taskID)
	if hasExisting {
		if !forceReanalyze {
			GlobalRCAScheduler.mu.Unlock()
			logger.Log.Infof("[Task Service] Task %s RCA already queued/running, skipping duplicate trigger", taskID)
			return
		}
		// 强制重新分析：取消旧 worker 实例
		oldInst := existingVal.(*rcaInstance)
		logger.Log.Infof("[Task Service] Force reanalyzing task %s, canceling prior worker gen=%d", taskID, oldInst.gen)
		oldInst.cancel()
	}

	GlobalRCAScheduler.genSeq++
	inst := &rcaInstance{
		gen:  GlobalRCAScheduler.genSeq,
		done: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	inst.cancel = cancel

	GlobalRCAScheduler.instances.Store(taskID, inst)
	GlobalRCAScheduler.mu.Unlock()

	go func() {
		defer func() {
			close(inst.done)
			// 仅当实例仍为自身时，才从 map 移除；若已被新实例替换则不删除 (CAS 清理，杜绝误删新实例)
			GlobalRCAScheduler.instances.CompareAndDelete(taskID, inst)
		}()

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

// fetchNormLogsForRCA 游标读取待分析日志并在计算前显式 Close，严格遵循 MaxOpenConns=1 约束
func fetchNormLogsForRCA(ctx context.Context, taskDB *gorm.DB) ([]*model.NormalizedLog, bool, error) {
	rows, err := taskDB.WithContext(ctx).Model(&model.LogRecord{}).
		Where("knowledge_id > 0 OR severity <= ?", rcaSeverityThreshold).
		Order("timestamp asc, id asc").Rows()
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var list []*model.NormalizedLog
	truncated := false
	for rows.Next() {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		default:
		}
		if len(list) >= maxRCALogs {
			truncated = true
			logger.Log.Warnf("[Task Service] RCA sample size exceeded max limit (%d), truncating subsequent records", maxRCALogs)
			break
		}
		var rec model.LogRecord
		if scanErr := taskDB.ScanRows(rows, &rec); scanErr != nil {
			logger.Log.Warnf("[Task Service] Scan log row for RCA failed: %v", scanErr)
			continue
		}
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
	return list, truncated, rows.Err()
}

// executeRCAPipeline 执行 RCA 分析核心流水线与短事务落库
func (s *Service) executeRCAPipeline(ctx context.Context, taskID string, inst *rcaInstance) {
	// 阶段 1: 游标拉取待分析数据，提取完毕后立即关闭游标并释放连接引用
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
		if errors.Is(fetchErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			logger.Log.Infof("[Task Service] RCA fetch canceled for task %s", taskID)
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

	// 检查点：是否已被新触发实例取代或取消
	if !GlobalRCAScheduler.isCurrentInstance(taskID, inst) || ctx.Err() == context.Canceled {
		logger.Log.Infof("[Task Service] Task %s worker gen=%d canceled/superseded after analysis", taskID, inst.gen)
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
		errMsg := "分析达到超时上限，已保存部分结果"
		if fetchTruncated {
			errMsg = fmt.Sprintf("分析样本达到硬上限(%d行)，已保存前%d行分析结果", maxRCALogs, maxRCALogs)
		} else if res.Err != nil && !errors.Is(res.Err, context.DeadlineExceeded) && !errors.Is(res.Err, context.Canceled) {
			errMsg = fmt.Sprintf("分析超时中断: %v", res.Err)
		}
		s.updateTaskRCAState(taskID, model.RCAStatusTimeout, errMsg, analyzedUntil, len(res.Events))
		logger.Log.Warnf("[Task Service] RCA finished with TIMEOUT for task %s: %d events saved (fetchTruncated=%v)", taskID, len(res.Events), fetchTruncated)
	} else if res.Err != nil {
		s.updateTaskRCAState(taskID, model.RCAStatusFailed, res.Err.Error(), analyzedUntil, len(res.Events))
		logger.Log.Errorf("[Task Service] RCA failed for task %s: %v", taskID, res.Err)
	} else {
		s.updateTaskRCAState(taskID, model.RCAStatusCompleted, "", analyzedUntil, len(res.Events))
		logger.Log.Infof("[Task Service] RCA completed successfully for task %s: %d events", taskID, len(res.Events))
	}
}

// WaitForTaskRCA 等待指定任务的 RCA 计算结束（供测试或单步调用使用）
func (s *Service) WaitForTaskRCA(taskID string, timeout time.Duration) bool {
	val, ok := GlobalRCAScheduler.instances.Load(taskID)
	if !ok {
		// 检查数据库状态，若为 QUEUED 或 RUNNING，说明有任务正在瞬态排队，不能直接返回假成功
		taskInfo, err := s.GetTaskByID(taskID)
		if err == nil && (taskInfo.RCAStatus == model.RCAStatusQueued || taskInfo.RCAStatus == model.RCAStatusRunning) {
			return false
		}
		return true
	}
	inst, ok := val.(*rcaInstance)
	if !ok {
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
	dummyInst := &rcaInstance{gen: 0}
	GlobalRCAScheduler.instances.Store(taskID, dummyInst)
	defer GlobalRCAScheduler.instances.CompareAndDelete(taskID, dummyInst)
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
		dbPath := filepath.Join(s.taskDir, fmt.Sprintf("task_%s.db", t.TaskID))
		if _, statErr := os.Stat(dbPath); statErr == nil {
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
