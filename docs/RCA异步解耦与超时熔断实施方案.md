# RCA 根因分析异步解耦、超时熔断与渐进式展示实施方案

## 一、 背景与核心痛点

### 1.1 现状与问题机理
在当前实现中，日志导入流水线（[`internal/task/service.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/service.go)）采用**强同步阻塞**模型：
```
文件上传/读取 → 分词解析与入库 → 知识库批量匹配 → [同步阻塞执行 RCA Analyze] → 标记任务 COMPLETED → 前端允许查看日志
```
当导入海量日志（万级至十万级）时，系统卡在：
`开始RCA根因分析计算...`，后台输出日志停留在 [`internal/rootcause/engine.go:596`](file:///d:/Document/GO/LogAuditorGo/internal/rootcause/engine.go#L596)：
`[RCA Engine] Analyzing N logs with overlapping window 300s (overlap 60s)`。

造成该现象的核心机理如下：
1. **孤立告警未认领导致算力黑洞**：实际网络设备日志中超过 80%~90% 是单点孤立告警（下游无级联告警或端口/实例不匹配）。在 `engine.go` 的滑动窗口 BFS 中，当一条日志未找到下游衍生事件时，`claimed` 表未记录该日志，导致在后续相邻重叠窗口中被**反复当作根因候选触发全量 BFS 扫描**，时间复杂度退化至 $O(N^2)$ 甚至更高；
2. **缺乏超时控制与熔断机制**：`Analyze` 是纯 CPU 计算密集型同步循环，无法响应外部取消信号，遇到密集告警或大规模样本时长时间独占 CPU；
3. **关键体验受阻（阻断主路径）**：RCA 属于高级拓扑诊断能力，而用户的核心第一诉求是**“立即查看导入的日志、检查分词结果、过滤检索并阅读匹配的知识条目”**。强同步阻塞导致用户被迫等待数分钟甚至数十分钟，界面产生严重的“假死感”。

---

## 二、 总体架构与重构目标

### 2.1 核心设计理念：渐进式可用（Progressive Availability）
将 RCA 根因拓扑分析从日志导入的主干路径中**彻底解耦**为独立的**后台异步分析任务**：

```mermaid
flowchart TD
    subgraph 改造前：同步阻塞模式
        A1[上传/导入日志] --> B1[分词解析与入库]
        B1 --> C1[知识库多级匹配]
        C1 --> D1["同步阻塞执行 RCA Analyze (卡顿点 ⚠️)"]
        D1 --> E1[任务标记 COMPLETED]
        E1 --> F1[用户才能关闭弹窗并在工作台看日志]
    end

    subgraph 改造后：渐进式异步解耦模式
        A2[上传/导入日志] --> B2[分词解析与入库]
        B2 --> C2[知识库多级匹配]
        C2 --> E2[主任务标记 COMPLETED / 导入就绪]
        E2 --> F2[🎉 用户立即进入工作台查看日志与知识]
        
        C2 -.->|异步派发后台 Worker| G2["独立 RCA 协程 (带 5 分钟 Context 超时)"]
        G2 --> H2{是否在 5 分钟内完成?}
        H2 -- 正常完成 --> I2[落库 RCA 事件, rca_status = COMPLETED, 刷新徽章]
        H2 -- 达到 5 分钟 --> J2[安全熔断, 保存已有部分事件, rca_status = TIMEOUT]
    end
```

### 2.2 核心目标
1. **毫秒级感知就绪**：万级日志导入完成后，页面在入库匹配结束（5~15秒内）立刻关闭弹窗并开放工作台，用户即刻可查看、过滤日志和知识；
2. **后台超时保护**：RCA 运算严格受限于 **5 分钟超时控制**，超时立即安全熔断，杜绝 CPU 永久霸占；
3. **算法内核防空转**：修复孤立告警重复回溯缺陷，使有效 RCA 计算性能提升 10 倍以上；
4. **前端状态自洽**：前端工作台提供清晰的“RCA 分析中 / 已就绪 / 超时保留部分结果”视觉反馈，支持局部轮询或重试。

---

## 三、 数据模型设计（Model）

在 [`internal/model/task.go`](file:///d:/Document/GO/LogAuditorGo/internal/model/task.go) 中对 `TaskInfo` 进行字段扩展，解耦主任务状态与 RCA 状态：

```go
// RCAStatus 根因分析独立状态
type RCAStatus string

const (
    RCAStatusPending   RCAStatus = "PENDING"   // 未开始/待分析
    RCAStatusRunning   RCAStatus = "RUNNING"   // 后台分析计算中
    RCAStatusCompleted RCAStatus = "COMPLETED" // 分析完成
    RCAStatusTimeout   RCAStatus = "TIMEOUT"   // 达到5分钟超时（已保存部分结果）
    RCAStatusFailed    RCAStatus = "FAILED"    // 分析异常终止
)

type TaskInfo struct {
    TaskID        string     `gorm:"primaryKey;size:64" json:"task_id"`
    TaskName      string     `gorm:"size:255" json:"task_name"`
    DeviceType    string     `gorm:"size:128" json:"device_type"`
    DeviceVersion string     `gorm:"size:64" json:"device_version"`
    FileCount     int        `json:"file_count"`
    DeviceCount   int        `json:"device_count"`
    LogCount      int        `json:"log_count"`
    MatchedCount  int        `json:"matched_count"`
    RcaCount      int        `json:"rca_count"`
    DBPath        string     `gorm:"size:512" json:"db_path"`
    Status        TaskStatus `gorm:"size:32" json:"status"`         // 主状态: COMPLETED 即代表日志可查看
    ErrorMessage  string     `gorm:"type:text" json:"error_message,omitempty"`
    StartTime     time.Time  `json:"start_time"`
    FinishTime    *time.Time `json:"finish_time,omitempty"`

    // --- 新增 RCA 独立异步状态字段 ---
    RCAStatus       RCAStatus  `gorm:"size:32;default:'PENDING'" json:"rca_status"`
    RCAErrorMessage string     `gorm:"type:text" json:"rca_error_message,omitempty"`
    RCAFinishTime   *time.Time `json:"rca_finish_time,omitempty"`
}
```

---

## 四、 后端改造实施细节

### 4.1 算法内核改造：Context 超时响应与孤立告警剪枝
文件：[`internal/rootcause/engine.go`](file:///d:/Document/GO/LogAuditorGo/internal/rootcause/engine.go)

1. **改造函数签名**：
   ```go
   // AnalyzeWithContext 支持外部 Context 超时与取消机制的根因推导
   func (e *Engine) AnalyzeWithContext(ctx context.Context, logs []*model.NormalizedLog, windowSeconds int) []model.RCAEvent
   ```
2. **剪枝优化（避免无衍生事件的孤立日志重复扫描）**：
   ```go
   // scannedOrphans 记录已经完整尝试过前向 BFS 但未发现任何下游衍生事件的日志 ID
   scannedOrphans := make(map[uint]bool, len(sortedLogs))

   for _, cluster := range clusters {
       for i := lo; i < hi; i++ {
           // 定时检测超时熔断信号
           select {
           case <-ctx.Done():
               logger.Log.Warnf("[RCA Engine] RCA analysis timed out (Context cancelled), returning accumulated %d events", len(events))
               return events
           default:
           }

           log := sortedLogs[i]
           // 已被认领或已确认为无下游孤立告警的直接跳过
           if claimed[log.ID] > 0 || scannedOrphans[log.ID] {
               continue
           }

           matchedRule := e.matchRootRule(log.Module, log.Brief)
           if matchedRule == nil {
               continue
           }

           event, ok := e.propagate(sortedLogs, ix, lo, hi, log, matchedRule, windowSeconds, claimed)
           if !ok {
               // 核心修复点：明确标记该日志在此窗口及重叠窗口内无下游，避免后续重复 BFS
               scannedOrphans[log.ID] = true
               continue
           }
           events = append(events, event)
       }
   }
   ```

### 4.2 导入主流程改造：日志入库完成即收尾并派发 RCA
文件：[`internal/task/service.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/service.go) 与 [`internal/task/import_prepare.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/import_prepare.go)

1. **主流水线解耦**：
   - 移除 `ImportLogsWithDevice` 中同步调用 `s.runRCAPipeline(taskDB)` 的逻辑；
   - 日志批量持久化与设备归集完成后，直接调用 `s.finalizeTaskInfo(...)` 将主状态置为 `TaskStatusCompleted`，主进度条上报 `COMPLETE`；
   - 异步派发后台 RCA 分析协程：
     ```go
     // 主导入成功收尾后，异步触发独立 RCA 分析
     go s.AsyncRunTaskRCA(taskID)
     ```

2. **异步 RCA 执行调度器（并发控制与 5 分钟超时）**：
   ```go
   // rcaTaskMutex 用于同一任务互斥，避免并发重复分析
   var rcaTaskMutex sync.Map

   // rcaSemaphore 全局并发控制信号量，最多允许 2 个任务同时进行 CPU 密集型 RCA
   var rcaSemaphore = make(chan struct{}, 2)

   func (s *Service) AsyncRunTaskRCA(taskID string) {
       // 同一任务单飞保护：若已有 RCA 正在运行则跳过
       if _, loaded := rcaTaskMutex.LoadOrStore(taskID, struct{}{}); loaded {
           logger.Log.Infof("[Task Service] Task %s RCA is already running, skipping", taskID)
           return
       }
       defer rcaTaskMutex.Delete(taskID)

       // 更新任务状态为 RUNNING
       s.updateTaskRCAStatus(taskID, model.RCAStatusRunning, "")

       // 限流排队获取计算令牌
       rcaSemaphore <- struct{}{}
       defer func() { <-rcaSemaphore }()

       // 设置 5 分钟硬超时
       ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
       defer cancel()

       taskDB, _, err := storage.GetOrCreateTaskDB(s.taskDir, taskID)
       if err != nil {
           s.updateTaskRCAStatus(taskID, model.RCAStatusFailed, err.Error())
           return
       }
       defer storage.ReleaseTaskDB(taskID)

       events, status, err := s.executeRCAPipelineWithContext(ctx, taskDB)
       now := time.Now()
       
       // 更新全局与任务库元数据
       updateFields := map[string]interface{}{
           "rca_count":       len(events),
           "rca_status":      status,
           "rca_finish_time": &now,
       }
       if err != nil {
           updateFields["rca_error_message"] = err.Error()
       }
       s.globalDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updateFields)
       taskDB.Model(&model.TaskInfo{}).Where("task_id = ?", taskID).Updates(updateFields)
   }
   ```

3. **数据库并发写入防护（SQLite）**：
   - 数据抽取阶段使用**只读查询**流式构建 `normLogs`；
   - 最终写入 `rca_events` 表时采用毫秒级事务，并在连接中配置 `_busy_timeout=5000`，确保与前端用户同时浏览或打标无锁冲突。

---

## 五、 API 与通信接口设计

### 5.1 任务详情接口升级
- **`GET /api/v1/tasks/:id`**：返回体中新增 `rca_status`、`rca_error_message`。

### 5.2 独立 RCA 重新分析触发接口
- **`POST /api/v1/tasks/:id/rca/reanalyze`**（新增）：
  - 允许用户在不重新分词解析、不重新匹配知识库的前提下，仅单独在后台重跑 RCA 根因计算；
  - 响应：`{ code: 200, message: "RCA 根因拓扑分析已在后台启动", data: { rca_status: "RUNNING" } }`。

### 5.3 获取 RCA 事件接口兼容
- **`GET /api/v1/tasks/:id/rca`**：
  - 返回包装对象，包含当前的 `rca_status` 及 `events` 列表；
  - 若 `rca_status === "RUNNING"`，返回 `events: []` 并附带 `"正在计算中"` 提示；
  - 若 `rca_status === "TIMEOUT"`，返回已成功识别出的部分事件，并附带 `"分析已达到5分钟超时限制，已保留阶段性结果"` 提示。

---

## 六、 前端交互与视觉设计

### 6.1 日志导入弹窗（[`ImportProgressModal.vue`](file:///d:/Document/GO/LogAuditorGo/web/src/components/ImportProgressModal.vue)）
- 当分词与入库匹配结束时，进度直接达到 100% 并标记完成；
- 成功提示语：“日志导入完成！共导入 X 行日志，命中知识 Y 条。RCA 故障拓扑分析正在后台计算中，您可直接开始审计工作。”；
- 用户点击“进入工作台”，无需任何停顿等待。

### 6.2 顶部视图导航栏（[`AuditWorkbench.vue`](file:///d:/Document/GO/LogAuditorGo/web/src/views/AuditWorkbench.vue)）
在导航模式 `VIEW_MODE.RCA` 按钮上，根据 `currentTask.rca_status` 展示动态徽章：
1. **`RUNNING`（后台计算中）**：
   - 徽章显示为带旋转动画的蓝色 Loading 图标；
   - 鼠标悬浮 Tooltip：“正在后台推导全量 RCA 根因链路（超时限制 5 分钟），计算就绪后将自动刷新”；
2. **`COMPLETED`（正常完成）**：
   - 徽章显示为红色数字 Badge，展示识别出的根因数（如 `3`）；
3. **`TIMEOUT`（超时完成）**：
   - 徽章显示为橙色警告 Badge：“N条 (超时)”；
4. **`FAILED`（失败）**：
   - 徽章显示为灰色 Badge：“分析失败”。

### 6.3 独立 RCA 故障联动中心（[`RcaCenter.vue`](file:///d:/Document/GO/LogAuditorGo/web/src/components/RcaCenter.vue)）
- 若用户在后台分析未完成时点击进入该 Tab：
  - 展示友好的分析等待骨架屏，展示动态动画与计时器：“正在基于时序关联与协议故障传播图推导根因拓扑中...已耗时 XX 秒”；
  - 开启轻量静默轮询（每 3 秒请求一次任务状态）；
  - 当状态变更为 `COMPLETED` 或 `TIMEOUT` 时，骨架屏自动淡出并无缝渲染 ECharts 拓扑图。

### 6.4 离线 HTML 报告导出（[`exporter.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/exporter.go)）
- 用户刚导入完即点击导出报告时：
  - 若 RCA 尚未完成，导出报告中“根因分析（RCA）排查建议”章节优雅展示提示信息：“RCA 根因拓扑分析仍在后台运行中，本次报告暂未附带拓扑图，待后台就绪后可重新导出完整诊断报告”；
  - 绝不因 RCA 异步未完成而阻断全量日志与匹配知识的导出。

---

## 七、 实施步骤与分阶段计划

| 阶段 | 改造任务 | 影响范围 | 预计工作量 |
| :--- | :--- | :--- | :--- |
| **Phase 1: 算法提速与超时改造** | 1. 修复孤立告警无衍生未记录导致的重复回溯问题 (`scannedOrphans`)<br>2. 增加 `AnalyzeWithContext` 与 5 分钟超时中断检测 | `internal/rootcause/` | 1 天 |
| **Phase 2: 后端流水线解耦** | 1. `TaskInfo` 扩展 `rca_status` 等字段<br>2. 改造 `ImportLogsWithDevice`：入库匹配后立即置 `COMPLETED`<br>3. 增加异步调度协程、限流信号量与并发锁控制 | `internal/task/`<br>`internal/model/` | 1.5 天 |
| **Phase 3: 接口与前端交互适配** | 1. 适配 `GET /rca` 接口状态字段与新增重跑接口<br>2. `AuditWorkbench.vue` 导航栏 Badge 状态动态切换<br>3. `RcaCenter.vue` 骨架屏与后台轮询<br>4. `ImportProgressModal.vue` 提示语更新 | `internal/api/`<br>`web/src/` | 1.5 天 |
| **Phase 4: 全流程回归与压测** | 1. 运行 `rootcause` 回归测试套件<br>2. 模拟 50,000 行大规模告警日志全流程导入测试<br>3. 验证 5 分钟强制熔断与并发导入场景 | 全仓 | 1 天 |

---

## 八、 验收测试指标

1. **导入响应时延**：
   - 导入 10,000 行包含密集告警的日志包，前端导入弹窗在 **10 秒内**完成关闭并开放工作台（原需卡顿 >3 分钟）；
2. **后台超时保护**：
   - 构造极端病态循环或超大规模数据，验证在 5 分钟时控制台准时输出 Warn 日志，协程安全退出，任务 `rca_status` 正确置为 `TIMEOUT`，且保留已识别的部分结果；
3. **算法优化效果**：
   - `rootcause_test.go` 中原有的因果拓扑判定与回归测试 100% 通过；
   - 正常 10,000 行日志的实际 RCA 计算耗时从原有的数分钟缩短至 **1.5 秒以内**。
