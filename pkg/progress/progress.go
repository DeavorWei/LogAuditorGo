package progress

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// StageStatus 阶段状态
type StageStatus string

const (
	StagePending   StageStatus = "pending"
	StageRunning   StageStatus = "running"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
	StageSkipped   StageStatus = "skipped"
)

// JobStatus 任务状态
type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
)

// StageDef 阶段定义
type StageDef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// StageInfo 阶段详细运行信息
type StageInfo struct {
	Key        string      `json:"key"`
	Name       string      `json:"name"`
	Status     StageStatus `json:"status"`
	Current    int64       `json:"current"`
	Total      int64       `json:"total"`
	DurationMs int64       `json:"duration_ms"`
	Detail     string      `json:"detail"`
	startTime  time.Time
}

// LogEntry 单条实时日志
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	Level     string `json:"level"` // info, success, warning, error
}

// ProgressSnapshot 进度数据快照（前端接收模型）
type ProgressSnapshot struct {
	JobID        string      `json:"job_id"`
	TaskID       string      `json:"task_id,omitempty"`
	JobType      string      `json:"job_type"` // "hdx" | "log"
	Status       JobStatus   `json:"status"`
	CurrentStage string      `json:"current_stage"`
	StageIndex   int         `json:"stage_index"`
	TotalStages  int         `json:"total_stages"`
	Current      int64       `json:"current"`
	Total        int64       `json:"total"`
	Percent      float64     `json:"percent"`
	Message      string      `json:"message"`
	// OverallLabel 批量处理时的整体进度描述（例如"第 3/12 个文档包"），为空表示无整体进度概念
	OverallLabel string      `json:"overall_label,omitempty"`
	Stages       []StageInfo `json:"stages"`
	Logs         []LogEntry  `json:"logs"`
	Error        string      `json:"error,omitempty"`
	Result       interface{} `json:"result,omitempty"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// JobTracker 单个任务的进度追踪器
type JobTracker struct {
	jobID       string
	taskID      string
	jobType     string
	status      JobStatus
	stages      []StageInfo
	stageIndex  int
	current     int64
	total       int64
	percent     float64
	message     string
	logs        []LogEntry
	errorMsg    string
	result      interface{}
	startTime   time.Time
	updatedAt   time.Time
	mu          sync.RWMutex
	subscribers map[chan ProgressSnapshot]struct{}
	subMu       sync.Mutex
	lastNotify  time.Time
	totalDelta  int64

	// overallPercent 覆盖式整体进度（0-100），-1 表示不覆盖、按阶段权重推算
	overallPercent float64
	// overallLabel 整体进度的文字描述，例如"第 3/12 个文档包"
	overallLabel string
	// forwardOnly 为 true 时阶段只允许前进，避免批量处理阶段条来回跳动
	forwardOnly bool
}

// NewJobTracker 创建新的任务进度追踪器
func NewJobTracker(jobID string, taskID string, jobType string, stageDefs []StageDef) *JobTracker {
	if jobID == "" {
		jobID = fmt.Sprintf("job_%d_%s", time.Now().Unix(), uuid.New().String()[:8])
	}

	stages := make([]StageInfo, len(stageDefs))
	for i, def := range stageDefs {
		stages[i] = StageInfo{
			Key:    def.Key,
			Name:   def.Name,
			Status: StagePending,
		}
	}

	tracker := &JobTracker{
		jobID:       jobID,
		taskID:      taskID,
		jobType:     jobType,
		status:      JobRunning,
		stages:      stages,
		stageIndex:  -1,
		subscribers: make(map[chan ProgressSnapshot]struct{}),
		startTime:      time.Now(),
		updatedAt:      time.Now(),
		logs:           make([]LogEntry, 0, 50),
		overallPercent: -1,
	}

	return tracker
}

func (t *JobTracker) JobID() string {
	return t.jobID
}

func (t *JobTracker) TaskID() string {
	return t.taskID
}

// SetStage 切换到指定阶段
func (t *JobTracker) SetStage(key string, message ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	msg := ""
	if len(message) > 0 {
		msg = message[0]
	}

	// 批量处理场景（例如逐个导入多个 HDX 文档包）下阶段只允许前进，
	// 避免阶段条在多个处理单元之间来回跳动
	if t.forwardOnly {
		targetIdx := -1
		for i := range t.stages {
			if t.stages[i].Key == key {
				targetIdx = i
				break
			}
		}
		if targetIdx >= 0 && t.stageIndex >= 0 && targetIdx < t.stageIndex {
			if msg != "" {
				t.message = msg
			}
			t.updatedAt = now
			t.appendLogLocked(t.message, "info")
			t.throttleBroadcastLocked(200 * time.Millisecond)
			return
		}
	}

	// 结束前一个阶段
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		prev := &t.stages[t.stageIndex]
		if prev.Status == StageRunning {
			prev.Status = StageCompleted
			if !prev.startTime.IsZero() {
				prev.DurationMs = time.Since(prev.startTime).Milliseconds()
			}
		}
	}

	// 查找并启动新阶段
	found := false
	for i := range t.stages {
		if t.stages[i].Key == key {
			t.stageIndex = i
			t.stages[i].Status = StageRunning
			t.stages[i].startTime = now
			t.stages[i].Detail = msg
			found = true
			break
		}
	}

	if !found && len(t.stages) > 0 {
		t.stageIndex = 0
		t.stages[0].Status = StageRunning
		t.stages[0].startTime = now
		t.stages[0].Detail = msg
	}

	if msg != "" {
		t.message = msg
	} else if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		t.message = t.stages[t.stageIndex].Name
	}

	t.current = 0
	t.total = 0
	t.recalculatePercentLocked()
	t.updatedAt = now

	t.appendLogLocked(t.message, "info")
	t.broadcastLocked()
}

// SetStageName 动态修改某个阶段的显示名称。
// 用于运行前才能确定的场景，例如检测到导入目录已解压时把"文件扫描与解压"改写为"文件扫描（无需解压）"。
func (t *JobTracker) SetStageName(key string, name string) {
	if name == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for i := range t.stages {
		if t.stages[i].Key != key {
			continue
		}
		t.stages[i].Name = name
		// 若当前正停留在该阶段且未设置过明细消息，则同步刷新主提示
		if t.stageIndex == i && t.message == t.stages[i].Name {
			t.message = name
		}
		break
	}

	t.updatedAt = time.Now()
	t.broadcastLocked()
}

// EnableForwardOnlyStages 启用"阶段只前进"模式。
// 批量处理多个单元（如多个 HDX 文档包）时，每个单元都会经历相同的阶段序列，
// 开启后阶段条不会回退到前面的阶段，避免视觉上反复跳动。
func (t *JobTracker) EnableForwardOnlyStages() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.forwardOnly = true
}

// SetOverallProgress 设置覆盖式整体进度（0-100）及其文字描述，
// 优先级高于按阶段权重推算的百分比。用于批量处理场景：
// 让进度条按"第 N/M 个"平滑推进，而不是随阶段循环回退。
func (t *JobTracker) SetOverallProgress(percent float64, label string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	t.overallPercent = percent
	t.overallLabel = label
	t.updatedAt = time.Now()

	t.recalculatePercentLocked()
	t.throttleBroadcastLocked(200 * time.Millisecond)
}

// UpdateProgress 更新当前阶段的明细进度（支持数值与百分比）
func (t *JobTracker) UpdateProgress(current int64, total int64, message ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.current = current
	t.total = total
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		t.stages[t.stageIndex].Current = current
		t.stages[t.stageIndex].Total = total
	}

	if len(message) > 0 && message[0] != "" {
		t.message = message[0]
		if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
			t.stages[t.stageIndex].Detail = message[0]
		}
	}

	t.recalculatePercentLocked()
	t.updatedAt = time.Now()

	t.throttleBroadcastLocked(50 * time.Millisecond)
}

// Increment 原子递增当前处理数量（高并发 worker 友好）
func (t *JobTracker) Increment(delta int64, message ...string) {
	atomic.AddInt64(&t.totalDelta, delta)
	// 节流处理，避免高频锁竞争
	t.mu.Lock()
	defer t.mu.Unlock()

	t.current += delta
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		t.stages[t.stageIndex].Current = t.current
	}

	if len(message) > 0 && message[0] != "" {
		t.message = message[0]
	}

	t.recalculatePercentLocked()
	t.updatedAt = time.Now()

	t.throttleBroadcastLocked(100 * time.Millisecond)
}

// AddLog 添加控制台日志
func (t *JobTracker) AddLog(level string, format string, args ...interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	msg := fmt.Sprintf(format, args...)
	t.appendLogLocked(msg, level)
	t.updatedAt = time.Now()
	t.broadcastLocked()
}

// Complete 标记整个任务成功完成
func (t *JobTracker) Complete(result interface{}, message ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	// 完成最后一个阶段
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		prev := &t.stages[t.stageIndex]
		prev.Status = StageCompleted
		if !prev.startTime.IsZero() {
			prev.DurationMs = time.Since(prev.startTime).Milliseconds()
		}
	}

	for i := range t.stages {
		if t.stages[i].Status == StagePending {
			t.stages[i].Status = StageCompleted
		}
	}

	t.status = JobCompleted
	t.percent = 100.0
	t.stageIndex = len(t.stages)
	if len(message) > 0 && message[0] != "" {
		t.message = message[0]
	} else {
		t.message = "处理全部完成"
	}
	t.result = result
	t.updatedAt = now

	t.appendLogLocked(t.message, "success")
	t.broadcastLocked()
}

// Fail 标记整个任务失败
func (t *JobTracker) Fail(err error, message ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		prev := &t.stages[t.stageIndex]
		prev.Status = StageFailed
		if !prev.startTime.IsZero() {
			prev.DurationMs = time.Since(prev.startTime).Milliseconds()
		}
	}

	t.status = JobFailed
	if err != nil {
		t.errorMsg = err.Error()
	}
	if len(message) > 0 && message[0] != "" {
		t.message = message[0]
	} else if err != nil {
		t.message = err.Error()
	}
	t.updatedAt = now

	t.appendLogLocked(fmt.Sprintf("❌ 失败: %s", t.message), "error")
	t.broadcastLocked()
}

// GetSnapshot 获取当前进度的完整快照
func (t *JobTracker) GetSnapshot() ProgressSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.getSnapshotLocked()
}

func (t *JobTracker) getSnapshotLocked() ProgressSnapshot {
	stagesCopy := make([]StageInfo, len(t.stages))
	copy(stagesCopy, t.stages)

	logsCopy := make([]LogEntry, len(t.logs))
	copy(logsCopy, t.logs)

	curStageName := ""
	if t.stageIndex >= 0 && t.stageIndex < len(t.stages) {
		curStageName = t.stages[t.stageIndex].Name
	}

	return ProgressSnapshot{
		JobID:        t.jobID,
		TaskID:       t.taskID,
		JobType:      t.jobType,
		Status:       t.status,
		CurrentStage: curStageName,
		StageIndex:   t.stageIndex,
		TotalStages:  len(t.stages),
		Current:      t.current,
		Total:        t.total,
		Percent:      t.percent,
		Message:      t.message,
		OverallLabel: t.overallLabel,
		Stages:       stagesCopy,
		Logs:         logsCopy,
		Error:        t.errorMsg,
		Result:       t.result,
		UpdatedAt:    t.updatedAt,
	}
}

// Subscribe 订阅进度通知流 (用于 SSE)
func (t *JobTracker) Subscribe() (chan ProgressSnapshot, func()) {
	ch := make(chan ProgressSnapshot, 64)
	t.subMu.Lock()
	t.subscribers[ch] = struct{}{}
	t.subMu.Unlock()

	// 立即发送当前状态
	snap := t.GetSnapshot()
	select {
	case ch <- snap:
	default:
	}

	unsubscribe := func() {
		t.subMu.Lock()
		delete(t.subscribers, ch)
		t.subMu.Unlock()
		close(ch)
	}

	return ch, unsubscribe
}

func (t *JobTracker) recalculatePercentLocked() {
	if t.status == JobCompleted {
		t.percent = 100.0
		return
	}
	// 整体进度覆盖：由调用方按处理单元（如文档包）驱动，进度条不会随阶段循环回退
	if t.overallPercent >= 0 {
		t.percent = t.overallPercent
		if t.percent > 99.0 {
			t.percent = 99.0
		}
		if t.percent < 0.0 {
			t.percent = 0.0
		}
		return
	}
	totalStages := len(t.stages)
	if totalStages == 0 {
		t.percent = 0
		return
	}

	stageWeight := 100.0 / float64(totalStages)
	completedStages := 0
	for i := 0; i < t.stageIndex && i < totalStages; i++ {
		completedStages++
	}

	basePercent := float64(completedStages) * stageWeight
	stageInternalPercent := 0.0
	if t.total > 0 && t.current >= 0 {
		ratio := float64(t.current) / float64(t.total)
		if ratio > 1.0 {
			ratio = 1.0
		}
		stageInternalPercent = ratio * stageWeight
	} else if t.stageIndex >= 0 {
		stageInternalPercent = 0.1 * stageWeight // 阶段刚开始占该阶段10%
	}

	t.percent = basePercent + stageInternalPercent
	if t.percent > 99.0 && t.status != JobCompleted {
		t.percent = 99.0
	}
	if t.percent < 0.0 {
		t.percent = 0.0
	}
}

func (t *JobTracker) appendLogLocked(msg string, level string) {
	if level == "" {
		level = "info"
	}
	entry := LogEntry{
		Timestamp: time.Now().Format("15:04:05"),
		Message:   msg,
		Level:     level,
	}
	// 限制保留最多 100 条日志
	if len(t.logs) >= 100 {
		t.logs = append(t.logs[1:], entry)
	} else {
		t.logs = append(t.logs, entry)
	}
}

func (t *JobTracker) broadcastLocked() {
	snap := t.getSnapshotLocked()
	t.subMu.Lock()
	defer t.subMu.Unlock()

	for ch := range t.subscribers {
		select {
		case ch <- snap:
		default:
			// 如果缓冲已满，丢弃最旧的一条快照，插入最新快照，确保终态和最新阶段不会丢失
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
	t.lastNotify = time.Now()
}

func (t *JobTracker) throttleBroadcastLocked(interval time.Duration) {
	if time.Since(t.lastNotify) >= interval {
		t.broadcastLocked()
	}
}

// Hub 全局进度追踪器管理器
type Hub struct {
	mu          sync.RWMutex
	jobs        map[string]*JobTracker
	stopJanitor chan struct{}
	stopped     bool
}

var (
	GlobalHub *Hub
	hubOnce   sync.Once
)

// GetHub 获取全局进度管理器单例
func GetHub() *Hub {
	hubOnce.Do(func() {
		GlobalHub = &Hub{
			jobs:        make(map[string]*JobTracker),
			stopJanitor: make(chan struct{}),
		}
		// 启动后台清理协程
		go GlobalHub.startJanitor()
	})
	return GlobalHub
}

// Stop 停止 Hub 的后台清理任务
func (h *Hub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.stopped && h.stopJanitor != nil {
		h.stopped = true
		close(h.stopJanitor)
	}
}

// NewJob 创建并注册新的追踪任务
func (h *Hub) NewJob(jobType string, taskID string, stages []StageDef) *JobTracker {
	jobID := fmt.Sprintf("job_%d_%s", time.Now().Unix(), uuid.New().String()[:8])
	tracker := NewJobTracker(jobID, taskID, jobType, stages)

	h.mu.Lock()
	h.jobs[jobID] = tracker
	h.mu.Unlock()

	return tracker
}

// RegisterJob 注册已有 ID 的追踪任务（例如任务 ID 直接作为 jobID）
func (h *Hub) RegisterJob(jobID string, taskID string, jobType string, stages []StageDef) *JobTracker {
	tracker := NewJobTracker(jobID, taskID, jobType, stages)

	h.mu.Lock()
	h.jobs[jobID] = tracker
	h.mu.Unlock()

	return tracker
}

// GetJob 获取指定的 JobTracker
func (h *Hub) GetJob(jobID string) *JobTracker {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.jobs[jobID]
}

// startJanitor 定期清理超过 30 分钟未更新的任务
func (h *Hub) startJanitor() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopJanitor:
			return
		case <-ticker.C:
			h.mu.Lock()
			now := time.Now()
			for id, job := range h.jobs {
				job.mu.RLock()
				lastUp := job.updatedAt
				isDone := job.status == JobCompleted || job.status == JobFailed
				job.mu.RUnlock()

				if isDone && now.Sub(lastUp) > 30*time.Minute {
					delete(h.jobs, id)
				}
			}
			h.mu.Unlock()
		}
	}
}
