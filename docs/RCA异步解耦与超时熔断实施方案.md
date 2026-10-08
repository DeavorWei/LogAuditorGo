# RCA 根因分析异步解耦、超时熔断与渐进式展示实施方案 (Rev. 2)

> **版本变更说明 (Rev. 2)**：
> 1. **修正 P0 正确性缺陷**：放弃无状态的 `scannedOrphans` 全局布尔拉黑，引入**扫描水位剪枝（Scan Watermark）**，彻底避免重叠滑动窗口下跨簇因果链漏报；
> 2. **消除 P1 单连接池阻塞风险**：针对 SQLite `MaxOpenConns=1` 特性，规范游标生命周期与连接隔离，避免后台流式读取与落库事务阻断前台用户浏览；
> 3. **完善超时与并发预算机制**：将超时预算配置化（`rca.timeout`，默认 300s），检查点前移覆盖 DB 读取全流程，排队状态独立化；
> 4. **规避 P2 API Breaking Change**：保持 `GET /api/v1/tasks/:id/rca` 裸数组契约向后兼容，在元数据接口中透传独立状态；
> 5. **补齐自愈与兜底逻辑**：增加服务启动期的悬挂状态扫描、`ReanalyzeTask` 同步链路改造、以及无风险的 `rootRuleMissed` 静态剪枝优化。

---

## 一、 现状分析与核心矛盾

### 1.1 现状调用链路
在当前代码中，日志导入（`ImportLogsWithDevice`）与重新分析（`ReanalyzeTask`）均采用**强同步阻塞**模型：
```
文件分片读取 → 统一解析入库 → 知识库批量匹配 → [同步阻塞执行 runRCAPipeline] → 任务收尾 COMPLETED → 前端允许查看日志
```
当导入海量日志（万级至十万级）时，用户界面停留在：
`开始RCA根因分析计算...`。若开启了 Debug 日志，控制台停在 [`internal/rootcause/engine.go:596`](file:///d:/Document/GO/LogAuditorGo/internal/rootcause/engine.go#L596)：
`[RCA Engine] Analyzing N logs with overlapping window 300s (overlap 60s)`（默认 `info` 级别下该行不输出，前端表现为无声假死）。

### 1.2 瓶颈根因与误区澄清
1. **多重倒排与重叠窗口回溯**：
   - 算法虽有倒排索引与硬限制（`maxRCALogs=100000`、`maxRCAEventsPerAnalyze=2000`、`maxCorrelatedPerEvent=500`），但在密集告警或时间跨度大的场景下，大量的孤立告警（无下游衍生）在 `propagate` 失败后未能沉淀剪枝信息；
   - 此外，大量甚至无法匹配根因链首规则（`matchRootRule` 返回 nil）的日志在每个重叠簇中被无谓地重复尝试规则匹配。
2. **连接池排队与资源争抢（P1 隐患）**：
   - [`internal/storage/task_db.go:345`](file:///d:/Document/GO/LogAuditorGo/internal/storage/task_db.go#L345) 明确配置了 `sqlDB.SetMaxOpenConns(1)`；
   - [`internal/task/import_prepare.go:277-298`](file:///d:/Document/GO/LogAuditorGo/internal/task/import_prepare.go#L277) 的游标扫描若未读完即 `break`，连接被长久持有；若直接异步化，后台流式读取和事务写入将导致用户在前台查看日志、过滤查询、关联设备时全部陷入数据库连接等待。
3. **主次矛盾割裂（核心痛点）**：
   - 用户导入日志的核心第一诉求是**“立即查看已入库的日志、确认解析与知识库命中结果”**；
   - RCA 作为后置拓扑诊断，却强行阻塞主导入流程长达数分钟，导致整个任务在分析期间不可用。

---

## 二、 总体架构与重构目标

### 2.1 核心设计：渐进式可用（Progressive Availability）
将 RCA 根因拓扑分析解耦为**受控异步后台服务**，主干导入流程在入库匹配完成后即刻开放：

```mermaid
flowchart TD
    subgraph 主导入流程 (秒级就绪)
        A[上传/导入文件] --> B[分词解析与入库]
        B --> C[知识库多级匹配]
        C --> D[持久化任务元信息 status=COMPLETED]
        D --> E[🎉 弹窗完成, 用户立即查看日志与知识]
    end

    subgraph 独立后台 RCA Worker (受控预算)
        D -.->|异步派发| F[入队排队 QUEUED]
        F --> G{获取并发信号量}
        G --> H["开始执行 RUNNING (覆盖 DB读+计算 的超时 Context)"]
        H --> I[游标流式拉取 normLogs 并在计算前显式 Close]
        I --> J["算法计算: 静态规则剪枝 + 扫描水位动态剪枝"]
        J --> K{状态判定}
        K -- 正常完成 --> L[短事务写 rca_events, rca_status=COMPLETED]
        K -- 超时截断 --> M["写部分结果, rca_status=TIMEOUT, 记录 analyzed_until"]
        K -- 异常失败 --> N[rca_status=FAILED, 记录错误信息]
    end
```

### 2.2 核心重构目标
1. **主路径极速就绪**：万级日志导入感知耗时收敛至数据入库与知识匹配完成时间（P95 < 15秒），弹窗关闭后即可在工作台检索浏览；
2. **超时可控与全链路覆盖**：RCA 总体预算受配置（`rca.timeout`，默认 300s）硬性约束，超时预算覆盖“DB 查询 + 倒排构建 + 窗口分析”全程；
3. **正确性零妥协**：通过“扫描水位”实现剪枝，绝对不发生跨重叠窗口因果链漏报；
4. **数据库并发隔离**：严守 `MaxOpenConns(1)` 约束，游标用后即关，事务极短，杜绝前后台排队卡死。

---

## 三、 数据模型设计（Model）

在 [`internal/model/task.go`](file:///d:/Document/GO/LogAuditorGo/internal/model/task.go) 中扩展独立状态字段：

```go
// RCAStatus 根因分析独立状态枚举
type RCAStatus string

const (
    RCAStatusPending   RCAStatus = "PENDING"   // 存量默认/待分析
    RCAStatusQueued    RCAStatus = "QUEUED"    // 排队等待计算令牌
    RCAStatusRunning   RCAStatus = "RUNNING"   // 正在拉取数据或执行推导
    RCAStatusCompleted RCAStatus = "COMPLETED" // 分析完整完成
    RCAStatusTimeout   RCAStatus = "TIMEOUT"   // 达到超时预算，已保存部分结果
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
    Status        TaskStatus `gorm:"size:32" json:"status"`         // 主状态: COMPLETED 即代表日志数据可用
    ErrorMessage  string     `gorm:"type:text" json:"error_message,omitempty"`
    StartTime     time.Time  `json:"start_time"`
    FinishTime    *time.Time `json:"finish_time,omitempty"`

    // --- 新增 RCA 独立异步状态字段 (存量数据零值兜底为 PENDING) ---
    RCAStatus       RCAStatus  `gorm:"size:32;default:'PENDING'" json:"rca_status"`
    RCAErrorMessage string     `gorm:"type:text" json:"rca_error_message,omitempty"`
    RCAFinishTime   *time.Time `json:"rca_finish_time,omitempty"`
    // AnalyzedUntil 记录超时截断时已分析到的时序边界，供前端渲染提示
    RCAAnalyzedUntil *time.Time `json:"rca_analyzed_until,omitempty"`
}
```

---

## 四、 算法内核优化（Correctness & Pruning）

文件：[`internal/rootcause/engine.go`](file:///d:/Document/GO/LogAuditorGo/internal/rootcause/engine.go)

### 4.1 正确的剪枝设计：扫描水位（Scan Watermark）与规则缓存

为避免重叠窗口将跨窗因果链切碎引发漏报，**严禁使用全局布尔值拉黑**。改用双层优化：

1. **优化 A（低成本静态规则剪枝）**：
   规则库是静态的，单条日志是否匹配链首规则与时序窗口无关。使用 `rootRuleMissed map[uint]bool` 缓存未命中结果，直接跳过非候选日志；
2. **优化 B（扫描水位动态剪枝）**：
   记录日志在特定右边界 `hi` 内已确认无下游。因簇的 `hi` 单调递增，后续簇若 `hi <= upTo` 则安全跳过，若 `hi > upTo` 则必须继续扫描。

### 4.2 强类型返回值与超时 Context 检查点

```go
// RCAResult 包装 RCA 执行结果，使截断与覆盖水位成为一等公民
type RCAResult struct {
    Events        []model.RCAEvent
    Truncated     bool       // 是否发生超时中断
    AnalyzedUntil time.Time  // 分析覆盖到的最后时序点
    Err           error
}

func (e *Engine) AnalyzeWithContext(ctx context.Context, logs []*model.NormalizedLog, windowSeconds int) RCAResult {
    // 检查点 1: 入口检查
    select {
    case <-ctx.Done():
        return RCAResult{Truncated: true, Err: ctx.Err()}
    default:
    }

    if e == nil || len(logs) == 0 {
        return RCAResult{}
    }
    e.ensureIndexes()

    // ... 日志规范化与稳定排序 ...

    // 检查点 2: 排序后、索引构建前
    select {
    case <-ctx.Done():
        return RCAResult{Truncated: true, Err: ctx.Err()}
    default:
    }

    ix := newInvertedIndex(sortedLogs)
    clusters := ClusterByOverlappingWindow(sortedLogs, windowSeconds, DefaultOverlapSeconds)

    // 状态表维护
    claimed := make(map[uint]uint, len(sortedLogs))
    rootRuleMissed := make(map[uint]bool, len(sortedLogs))      // 优化 A: 静态规则未命中缓存
    scannedWatermark := make(map[uint]int, len(sortedLogs))    // 优化 B: 扫描水位表 (logID -> 已扫描的最大 hi)

    var events []model.RCAEvent
    var lastProcessedTime time.Time
    truncated := false

    for _, cluster := range clusters {
        if len(events) >= maxRCAEventsPerAnalyze {
            break
        }
        lo, hi := cluster.StartIdx, cluster.EndIdx
        if lo < 0 { lo = 0 }
        if hi > len(sortedLogs) || hi < 0 { hi = len(sortedLogs) }
        if lo >= hi { continue }

        for i := lo; i < hi; i++ {
            // 检查点 3: 循环内周期性检测超时预算
            select {
            case <-ctx.Done():
                truncated = true
                logger.Log.Warnf("[RCA Engine] RCA analysis timed out by context, returning partial %d events (analyzed until %v)",
                    len(events), lastProcessedTime)
                return RCAResult{
                    Events:        events,
                    Truncated:     true,
                    AnalyzedUntil: lastProcessedTime,
                    Err:           ctx.Err(),
                }
            default:
            }

            if len(events) >= maxRCAEventsPerAnalyze {
                break
            }
            log := sortedLogs[i]
            if log == nil { continue }
            lastProcessedTime = log.Timestamp

            // 1. 已被认领的下游衍生直接跳过
            if _, ok := claimed[log.ID]; ok {
                continue
            }
            // 2. 静态未命中缓存：非规则候选直接跳过
            if rootRuleMissed[log.ID] {
                continue
            }
            // 3. 扫描水位剪枝：当前簇边界不超过历史已扫描水位，安全跳过
            if upTo, ok := scannedWatermark[log.ID]; ok && hi <= upTo {
                continue
            }

            matchedRule := e.matchRootRule(log.Module, log.Brief)
            if matchedRule == nil {
                rootRuleMissed[log.ID] = true
                continue
            }

            event, ok := e.propagate(sortedLogs, ix, lo, hi, log, matchedRule, windowSeconds, claimed)
            if !ok {
                // 水位推进：记录在当前 hi 范围内确认无衍生，而非全局永久拉黑
                scannedWatermark[log.ID] = hi
                continue
            }
            events = append(events, event)
        }
    }

    return RCAResult{
        Events:        events,
        Truncated:     truncated,
        AnalyzedUntil: lastProcessedTime,
        Err:           nil,
    }
}
```

---

## 五、 后端调度、流水线与并发安全设计

### 5.1 数据库连接池与资源生命周期（严防阻塞）
针对 [`task_db.go`](file:///d:/Document/GO/LogAuditorGo/internal/storage/task_db.go) 中 `SetMaxOpenConns(1)` 的硬约束，必须严格规范生命周期：

1. **游标使用即关**：
   在从 `taskDB` 查询抽样日志时，必须保证在进入密集计算之前显式关闭游标：
   ```go
   func fetchNormLogsForRCA(ctx context.Context, taskDB *gorm.DB) ([]*model.NormalizedLog, error) {
       rows, err := taskDB.WithContext(ctx).Model(&model.LogRecord{}).
           Where("knowledge_id > 0 OR severity <= ?", rcaSeverityThreshold).
           Order("timestamp asc, id asc").Rows()
       if err != nil {
           return nil, err
       }
       // 务必使用局部函数或确保 Close 立即执行，禁止在长耗时函数中拖带 defer
       defer rows.Close()

       var list []*model.NormalizedLog
       for rows.Next() {
           select {
           case <-ctx.Done():
               return nil, ctx.Err()
           default:
           }
           if len(list) >= maxRCALogs {
               break
           }
           var rec model.LogRecord
           if scanErr := taskDB.ScanRows(rows, &rec); scanErr != nil {
               continue
           }
           // ... 组装 NormalizedLog ...
           list = append(list, nl)
       }
       return list, rows.Err()
   }
   ```
2. **极短写入事务**：
   计算完成后，开启短事务写入 `rca_events` 并更新 `task_info`。写操作耗时在 20ms 以内，完全不影响前台用户的日常交互。

### 5.2 异步 Worker 调度与排队预算
调度器统一管理排队时间、执行超时与单飞控制：

```go
type RCAScheduler struct {
    semaphore chan struct{}
    running   sync.Map // taskID -> context.CancelFunc
}

var GlobalRCAScheduler = &RCAScheduler{
    semaphore: make(chan struct{}, 2), // 全局最多允许 2 个任务同时执行密集 RCA
}

func (s *Service) TriggerTaskRCA(taskID string, forceReanalyze bool) {
    // 1. 若已有正在计算的任务，视策略忽略或取消重建
    if cancelFn, exists := GlobalRCAScheduler.running.Load(taskID); exists {
        if !forceReanalyze {
            logger.Log.Infof("[Task Service] Task %s RCA already in progress, skipping", taskID)
            return
        }
        // 若强制重新分析，取消当前执行实例
        cancelFn.(context.CancelFunc)()
    }

    // 2. 状态立即置为 QUEUED，更新统一持久化路径
    s.updateTaskRCAState(taskID, model.RCAStatusQueued, "", nil)

    go func() {
        // 总超时预算（排队 + 计算，默认 10 分钟；计算本身 5 分钟）
        totalTimeout := 10 * time.Minute
        calcTimeout := 5 * time.Minute
        
        ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
        defer cancel()

        // 排队等待令牌（受 context 取消保护，避免无界阻塞）
        select {
        case <-ctx.Done():
            s.updateTaskRCAState(taskID, model.RCAStatusTimeout, "排队超时未获取到计算资源", nil)
            return
        case GlobalRCAScheduler.semaphore <- struct{}{}:
            defer func() { <-GlobalRCAScheduler.semaphore }()
        }

        // 登记单飞取消函数
        calcCtx, calcCancel := context.WithTimeout(context.Background(), calcTimeout)
        defer calcCancel()
        GlobalRCAScheduler.running.Store(taskID, calcCancel)
        defer GlobalRCAScheduler.running.Delete(taskID)

        // 状态变更为 RUNNING
        s.updateTaskRCAState(taskID, model.RCAStatusRunning, "", nil)

        s.executeRCAPipeline(calcCtx, taskID)
    }()
}
```

### 5.3 改造两个入口（消除第二入口遗漏）
1. **入口一：`ImportLogsWithDevice`**（[`internal/task/service.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/service.go)）：
   - 日志批量写入、知识库匹配、设备自动归集完成后，立即执行 `finalizeTaskInfo` 将任务主状态置为 `TaskStatusCompleted`；
   - 移除原来的同步 `runRCAPipeline`；
   - 派发后台异步分析：`s.TriggerTaskRCA(taskID, false)`；
   - 进度跟踪器立即上报 `COMPLETE`，前端弹窗顺利关闭。
2. **入口二：`ReanalyzeTask`**（[`internal/task/service.go:2091`](file:///d:/Document/GO/LogAuditorGo/internal/task/service.go#L2091)）：
   - 全量重新分词、重新抽取参数、重新匹配知识库并持久化后，直接将任务置为 `COMPLETED`；
   - 移除此处同步调用的 `runRCAPipeline`；
   - 派发异步分析：`s.TriggerTaskRCA(taskID, true)`，使点击“重新分析”的用户同样获得秒级反馈。

### 5.4 服务重启自愈机制
在系统初始化（如 [`cmd/LogAuditorGo/main.go`](file:///d:/Document/GO/LogAuditorGo/cmd/LogAuditorGo/main.go) 或 `task.Service` 构造时）：
扫描全局库中 `rca_status IN ('QUEUED', 'RUNNING')` 的存量任务，统一自愈修正为 `RCAStatusFailed`，并在错误信息中注明 `"服务重启中断，请手动点击重新分析"`，杜绝悬挂死锁。

---

## 六、 接口（API）与前端交互规范

### 6.1 保持向后兼容的接口契约（避免 Breaking Change）
- **`GET /api/v1/tasks/:id/rca`**：
  - **维持现有契约不变**：始终返回 `SuccessResponse(c, enrichedList)`（裸数组 JSON）；
  - 若 `rca_status` 为 `QUEUED` 或 `RUNNING`，返回空数组 `[]`；
  - 若为 `TIMEOUT`，返回已分析出的部分事件列表；
- **`GET /api/v1/tasks/:id`**：
  - 元数据实体返回 `rca_status`、`rca_error_message`、`rca_analyzed_until`；
  - 前端组件（如 `AuditWorkbench.vue`）基于任务元数据做统一状态展示，彻底避免白屏。
- **`POST /api/v1/tasks/:id/rca/reanalyze`**（新增轻量重跑端点）：
  - 仅触发 RCA 拓扑重跑，不重新执行耗时的日志分词解析与知识匹配。

### 6.2 前端视觉呈现与系统性截断说明

1. **工作台顶部导航栏**（[`AuditWorkbench.vue`](file:///d:/Document/GO/LogAuditorGo/web/src/views/AuditWorkbench.vue)）：
   - `rca_status` 为 `QUEUED` / `RUNNING` 时，Badge 展示蓝色旋转 Loading 图标；Tooltip 提示：“根因拓扑正在后台推导中，工作台已就绪可正常审计”；
   - `rca_status` 为 `COMPLETED` 时，Badge 显示红色数字；
   - `rca_status` 为 `TIMEOUT` 时，Badge 显示橙色标签：“N条 (超时截断)”；
2. **RCA 分析中心**（[`RcaCenter.vue`](file:///d:/Document/GO/LogAuditorGo/web/src/components/RcaCenter.vue)）：
   - 状态为执行中时展示骨架屏并带“推导中”动画，启动静默轮询（每 3 秒刷新任务元数据）；
   - 状态变为 `TIMEOUT` 时，拓扑图顶部增加显著的 Alert 警示栏：
     > ⚠️ **注意**：由于分析样本极大，本次分析在达到超时上限（5分钟）时安全中止，当前拓扑仅包含至 `YYYY-MM-DD HH:mm:ss` 前的故障链路，后续时段可能存在未识别事件。
3. **导出报告**（[`exporter.go`](file:///d:/Document/GO/LogAuditorGo/internal/task/exporter.go)）：
   - 当 RCA 未完成时，导出报告中友好提示“RCA 拓扑尚在后台生成中”，全量日志与匹配知识完整导出。

---

## 七、 实施计划与明确的验收基准

### 7.1 分阶段推进

| 阶段 | 核心任务 | 交付物 / 影响模块 | 周期 |
| :--- | :--- | :--- | :--- |
| **Phase 1: 算法内核改造** | 1. 落地 `rootRuleMissed` 静态缓存与 `scannedWatermark` 动态水位剪枝<br>2. 实现 `AnalyzeWithContext` 与结构化 `RCAResult` | `internal/rootcause/` | 1 天 |
| **Phase 2: 调度解耦与服务自愈** | 1. 扩展 `TaskInfo` 模型并在启动期自愈悬挂状态<br>2. 改造 `ImportLogsWithDevice` 与 `ReanalyzeTask` 为异步派发<br>3. 严格落实游标前置关闭与短事务隔离 | `internal/task/`<br>`internal/storage/` | 1.5 天 |
| **Phase 3: 接口与前端交互** | 1. 保持 `/rca` 裸数组契约，元信息透传状态<br>2. 完善导航栏 Badge、`RcaCenter` 骨架屏及超时截断警示<br>3. 适配 HTML 报告导出未就绪容错 | `internal/api/`<br>`web/src/` | 1.5 天 |
| **Phase 4: 全流程验证与压测** | 1. 运行回归用例验证因果链完整性（防漏报）<br>2. 注入 1s 超时验证熔断与截断标记<br>3. 并发导入与重启自愈验证 | 全仓 | 1 天 |

### 7.2 验收测试指标（P95 口径与基线对比）

1. **导入就绪延迟**：
   - 测试环境：标准单机开发环境，测试数据集为 10,000 行含密集告警的标准网络日志；
   - **基线**：改造前导入弹窗需等待全流程完成，感知阻塞耗时 **> 180 秒**；
   - **验收指标**：改造后导入弹窗在数据入库与知识匹配完成时关闭（**P95 < 12 秒**），工作台各表格即刻可用。
2. **正确性守卫（防漏报验证）**：
   - 构造跨越重叠窗口边界（跨越第 300 秒边界）的长故障传播链；
   - 验证在扫描水位机制下，跨簇衍生事件能够被 100% 稳定识别并串联，断言检出率不因剪枝发生任何衰减。
3. **超时熔断与自愈**：
   - 配置 `rca.timeout = 2s`，导入超大样本，断言在 2 秒内准确触发中断退出，状态正确置为 `TIMEOUT`，`rca_analyzed_until` 字段正确落库；
   - 模拟进程在 `RUNNING` 阶段强杀重启，断言启动后该任务状态自动修复为 `FAILED`，前端不再无限转圈。
