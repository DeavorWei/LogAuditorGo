# LogAuditorGo 全量架构与性能审计报告

> **审计时间**: 2026-08-26  
> **审计范围**: 全量源码（113 个文件，涵盖 15 个包）  
> **审计维度**: 架构设计 · 性能瓶颈 · 并发安全 · 错误处理 · 资源管理 · 安全漏洞 · 代码质量 · 测试覆盖

---

## 目录

- [一、项目架构总览](#一项目架构总览)
- [二、架构层面问题清单](#二架构层面问题清单)
- [三、性能层面问题清单](#三性能层面问题清单)
- [四、并发安全问题清单](#四并发安全问题清单)
- [五、错误处理问题清单](#五错误处理问题清单)
- [六、资源管理问题清单](#六资源管理问题清单)
- [七、安全漏洞问题清单](#七安全漏洞问题清单)
- [八、代码质量问题清单](#八代码质量问题清单)
- [九、测试覆盖缺口](#九测试覆盖缺口)
- [十、综合优化方案](#十综合优化方案)
- [附录：问题汇总表](#附录问题汇总表)

---

## 一、项目架构总览

### 1.1 包依赖关系图

```
cmd/LogAuditorGo/main.go (入口)
  ├── internal/config        ← 配置加载 (Viper)
  ├── internal/api           ← HTTP API 层 (Gin)
  │     ├── document_handler
  │     ├── knowledge_handler
  │     ├── task_handler
  │     ├── stats_handler
  │     ├── system_handler
  │     └── progress_handler
  ├── internal/knowledge     ← 知识库业务层
  │     └── deduplicator
  ├── internal/matcher       ← 四级流水线匹配引擎
  │     └── scoring
  ├── internal/task          ← 任务生命周期管理
  │     └── exporter
  ├── internal/rootcause     ← RCA 根因分析引擎
  │     ├── cluster
  │     └── topology_dag
  ├── internal/logparser     ← 日志解析器注册表
  │     ├── vrp_parser
  │     ├── sec_parser
  │     ├── param_extractor
  │     └── time_parser
  ├── internal/hdx           ← HDX 文档解析
  │     ├── archive
  │     ├── charset
  │     ├── extractor
  │     ├── html_parser
  │     └── navigator
  ├── internal/storage       ← 数据库存储层 (GORM + SQLite)
  ├── internal/search        ← 全文检索 (Bleve)
  ├── internal/model         ← 数据模型
  ├── pkg/logger             ← 日志组件
  ├── pkg/progress           ← 进度追踪与 SSE
  └── web/                   ← 前端静态资源 (embed)
```

### 1.2 技术栈

| 层次 | 技术选型 |
|------|---------|
| HTTP 框架 | Gin v1.12 |
| ORM | GORM v1.31 + SQLite (glebarez/sqlite) |
| 全文检索 | Bleve v2.6 |
| 配置管理 | Viper v1.21 |
| 日志 | Zap v1.28 (自定义封装) |
| 前端 | Vue 3 (embed.FS 打包) |

### 1.3 核心工作流

```
用户上传日志文件
  → 逐行解析 (logparser)
  → 四级知识库匹配 (matcher: Exact → Mnemonic → Template → Bleve)
  → 批量入库 (storage: 任务独立 SQLite)
  → RCA 根因分析 (rootcause: 滑动窗口 + DAG 拓扑)
  → HTML 报告导出 (exporter)
```

---

## 二、架构层面问题清单

### ARC-001 [Critical] 全局可变状态泛滥，阻碍测试与多租户扩展

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/config/config.go:41-44`, `internal/storage/knowledge_db.go:18-21`, `pkg/logger/logger.go`, `pkg/progress/progress.go:465-468`, `internal/search/bleve_indexer.go:100-101` |
| **问题描述** | 项目大量使用包级别全局单例变量：`config.GlobalConfig`、`storage.GlobalKnowledgeDB`、`logger.Log`、`progress.GlobalHub`、`search.indexerMap`。这些全局状态导致：(1) 并行测试相互污染；(2) 无法实现多租户隔离；(3) 依赖关系隐式传递而非显式注入。|
| **优化建议** | 将所有全局状态收敛为一个 `App` 结构体，通过构造函数注入依赖。使用 Wire 或手动 DI 管理生命周期。 |

### ARC-002 [High] 缺少接口抽象，具体类型直接耦合

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/router.go:19-25`, `internal/task/service.go:42-47`, `internal/matcher/engine.go:22-38` |
| **问题描述** | API Handler 直接依赖 `*knowledge.Service`、`*task.Service`、`*search.Indexer` 等具体结构体而非接口。`MatchEngine` 直接依赖 `*gorm.DB` 而非存储接口。这使得 mock 测试和组件替换极其困难。|
| **优化建议** | 为核心服务定义 Go 接口（`KnowledgeService`、`TaskService`、`SearchIndexer`、`KnowledgeRepository`），Handler 和 Engine 只依赖接口类型。 |

### ARC-003 [High] API Handler 层直接包含数据库查询，分层违反

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/stats_handler.go:27-44`, `internal/api/knowledge_handler.go:74-88`, `internal/api/task_handler.go:351-370` |
| **问题描述** | `StatsHandler` 直接持有 `*gorm.DB` 并在 Handler 内执行 `COUNT()`、`SUM()` 等聚合查询；`KnowledgeHandler` 在 Handler 内循环调用 `GetKnowledgeByID` 形成 N+1 查询；`TaskHandler` 在 Handler 内构建去重 Map。业务/数据逻辑泄漏到表现层。 |
| **优化建议** | 将统计查询封装到 `StatsService`；知识库搜索结果批量查询封装到 `KnowledgeService.BatchGetByIDs()`；数据编排逻辑下沉到 Service 层。 |

### ARC-004 [Medium] 配置层违反单一职责，直接操作 Logger

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/config/config.go:231-234` |
| **问题描述** | `UpdateLogConfig()` 直接调用 `logger.UpdatePolicy()` 和 `logger.SetLevel()`。配置层不应知道日志组件的内部实现。 |
| **优化建议** | 使用回调模式或事件机制，让 Logger 订阅配置变更事件，而非由 Config 直接驱动 Logger。 |

### ARC-005 [Medium] `ImportDocumentFromDir` 使用 `...interface{}` 参数，类型安全缺失

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:71-82` |
| **问题描述** | 使用 `options ...interface{}` 传递 conflictMode 和 tracker，需在函数体内通过类型断言逐一识别参数。极易出错且无法获得编译期类型检查。 |
| **优化建议** | 使用 Functional Options 模式：`type ImportOption func(*ImportConfig)`，如 `WithConflictMode("skip")` 和 `WithTracker(tr)`。 |

### ARC-006 [Medium] `importSingleDocUnlocked` 函数过长，违反单一职责

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:190-460` |
| **问题描述** | 该方法约 270 行，集成了 XML 解析、Worker 池创建、HTML 并发解析、数据去重、数据库事务、搜索索引等全部逻辑。违反 SRP，极难测试和维护。 |
| **优化建议** | 拆分为独立的 Pipeline Stage 函数：`parseMetadata()`, `parseHTMLPages()`, `deduplicateAndPersist()`, `buildSearchIndex()`。 |

### ARC-007 [Medium] Domain Model 与 ORM 模型耦合

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/model/*.go` |
| **问题描述** | 领域模型（Knowledge、Document 等）直接包含 GORM struct tag 和 `TableName()` 方法，领域层与持久化层无分离。更换数据库或 ORM 时影响面极大。 |
| **优化建议** | 分离 Domain Entity 和 Database DTO。在 Repository 层实现映射转换。 |

### ARC-008 [Medium] 缺少优雅关闭 (Graceful Shutdown) 机制

| 字段 | 内容 |
|------|------|
| **涉及文件** | `cmd/LogAuditorGo/main.go:61-63` |
| **问题描述** | 使用 `r.Run(addr)` 直接启动 HTTP 服务，未实现信号监听和优雅关闭。进程被 kill 时可能导致：正在处理的请求被强制中断、SQLite WAL 未正常 checkpoint、Bleve 索引损坏。 |
| **优化建议** | 使用 `http.Server` + `signal.NotifyContext`，在收到 SIGTERM/SIGINT 后调用 `server.Shutdown(ctx)`，依次关闭 HTTP 服务、Bleve 索引、数据库连接。 |

### ARC-009 [Low] `rootcause.NewEngine(nil)` 显式传递 nil 依赖

| 字段 | 内容 |
|------|------|
| **涉及文件** | `cmd/LogAuditorGo/main.go:53` |
| **问题描述** | RCA 引擎构造时传入 `nil` 作为自定义规则参数，暗示了接口设计不够清晰。 |
| **优化建议** | 使用无参构造函数 `rootcause.NewEngine()` 配合 `WithCustomRules(rules)` 选项模式。 |

---

## 三、性能层面问题清单

### PERF-001 [Critical] 上传日志文件全量读入内存，大文件 OOM

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/task_handler.go:49, 169` |
| **问题描述** | `io.ReadAll(f)` 将上传的整个日志文件一次性读入内存作为 string。对于几百 MB 甚至 GB 级的日志文件，将立即触发 OOM。 |
| **影响评级** | 在生产环境处理大日志文件时必然崩溃 |
| **优化建议** | 改用流式处理：使用 `bufio.Scanner` 直接读取 multipart file 的 `io.Reader`，逐行发送到解析 pipeline，避免全量驻留内存。 |

### PERF-002 [Critical] RCA 全量日志加载至内存后再分析

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:422-423` |
| **问题描述** | `taskDB.Order("timestamp asc, id asc").Find(&fullLogRecords)` 将任务的全部日志记录一次性加载到 `[]model.LogRecord` 切片中，随后再遍历构造 `[]*model.NormalizedLog`。若任务包含百万行日志，内存占用将达数 GB。 |
| **优化建议** | 使用游标/分页查询（`Rows()` + `ScanRows()`）进行流式处理，或按时间窗口分批加载。 |

### PERF-003 [Critical] Bleve 索引批量写入无分块限制，大量文档导致 OOM

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/search/bleve_indexer.go:163-213` |
| **问题描述** | `IndexKnowledge` 方法将全部 items 一次性提交到 `idx.index.NewBatch()` 进行索引。当 items 包含数千甚至数万条文档时，Bleve 内部将消耗大量 RAM 构建倒排索引。 |
| **优化建议** | 将 items 分块处理（每批 500-1000 条），逐批 `batch.Index()` + `idx.index.Batch(batch)`，释放中间内存。 |

### PERF-004 [High] RCA 滑动窗口算法 O(N²) 复杂度

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/rootcause/engine.go:75-173` |
| **问题描述** | BFS 前向搜索的内层循环 `for j := i + 1; j < len(sortedLogs); j++` 在每个潜在根因日志上都从 `i+1` 开始扫描整个窗口。对于高 EPS（如网络风暴产生数千条/秒的日志），复杂度退化为 O(N × W)，接近 O(N²)。 |
| **优化建议** | (1) 维护基于 Module+Brief 的倒排索引，只扫描与 DAG 边匹配的候选日志；(2) 使用双指针维护滑动窗口左右边界，避免每次从 `i+1` 重新扫描；(3) 对超过阈值（如 10000 条）的窗口内日志进行采样或截断。 |

### PERF-005 [High] 匹配引擎缓存无限增长，无淘汰策略

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/matcher/engine.go:35-37, 254` |
| **问题描述** | `cache`、`negativeCache`、`regexCache` 三个 `sync.Map` 只有 `Store` 操作，无任何淘汰机制。随着日志变体（含动态时间戳、序列号等）不断增长，这些 Map 将无限膨胀直至 OOM。 |
| **优化建议** | 使用 LRU 缓存替换 `sync.Map`（推荐 `github.com/hashicorp/golang-lru/v2` 或 `github.com/dgraph-io/ristretto`），设定合理的容量上限（如 100,000 条）。 |

### PERF-006 [High] 知识库全量加载至内存索引

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/matcher/engine.go:67-96` |
| **问题描述** | `loadIndexLocked()` 执行 `m.db.Preload("Versions").Find(&list)` 将全部知识库（含 Versions 关联表）一次性加载至内存构建 exactMap/moduleMap/idMap。知识库规模达数万条时，内存占用巨大。 |
| **优化建议** | (1) 延迟加载：按 Module 分组索引，首次匹配某 Module 时才加载该 Module 下的知识条目；(2) 使用内存映射数据结构或分级缓存减少驻留。 |

### PERF-007 [High] `FindBestKnowledgeMatchPtr` 内层循环重复字符串转换

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:539-545` |
| **问题描述** | 在匹配打分的内层 `for _, v := range k.Versions` 循环中，每次迭代都执行 `strings.ToUpper(targetProductTrim)` 和 `strings.Contains`。对于大量候选版本，产生巨量临时字符串分配和 GC 压力。 |
| **优化建议** | 将 `targetUpper` 提升到外层循环之前一次性计算；将版本信息的 `ProductType` 在索引加载时预处理为大写存储。 |

### PERF-008 [High] N+1 查询：知识库搜索结果逐条查询数据库

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/knowledge_handler.go:74-88` |
| **问题描述** | Bleve 搜索返回 N 个命中结果后，Handler 循环逐个调用 `GetKnowledgeByID(hit.KnowledgeID)` 查询数据库。搜索 10 条结果就触发 10 次独立 SQL 查询。 |
| **优化建议** | 收集所有 KnowledgeID 后批量查询：`SELECT * FROM knowledge WHERE id IN (?)`。 |

### PERF-009 [High] `StatsHandler` 在 API 请求中执行多个全表聚合查询

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/stats_handler.go:27-44` |
| **问题描述** | 每次调用 `/api/v1/system/stats` 都执行多个 `COUNT(*)` 和 `SUM()` 操作扫描 `LogRecord`、`TaskInfo` 等表，对于大数据量表会非常缓慢。 |
| **优化建议** | (1) 引入统计缓存层，定时更新（如每 30 秒刷新一次）；(2) 维护增量计数器表，在数据写入时同步更新。 |

### PERF-010 [Medium] SQLite 连接数硬编码为 1，严重限制读并发

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/storage/knowledge_db.go:62-63` |
| **问题描述** | `sqlDB.SetMaxOpenConns(1)` 将全局知识库的连接数限制为 1。虽然 SQLite 写操作需要串行化，但 WAL 模式允许并发读。当前设置导致所有读操作也必须串行等待。 |
| **优化建议** | 将 `MaxOpenConns` 设为 5-10 以支持 WAL 模式下的并发读；使用 `_txlock=immediate` DSN 参数控制写锁行为。 |

### PERF-011 [Medium] 去重哈希使用 `fmt.Sprintf` 连接字符串，性能低下

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/deduplicator.go:17-26` |
| **问题描述** | `CalculateContentHash` 使用 `fmt.Sprintf` 拼接 6 个字段后计算 SHA256。`fmt.Sprintf` 内部使用反射和大量内存分配。 |
| **优化建议** | 直接使用 `sha256.New()` + `hash.Write([]byte(...))` 逐字段写入，避免中间字符串分配。 |

### PERF-012 [Medium] `QueryTaskLogs` 每次分页都执行 `COUNT(*)` 全表扫描

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:547` |
| **问题描述** | 每次分页查询都执行 `.Count(&total)`，对于大量日志记录的任务表，该 `COUNT(*)` 操作在无索引优化时会全表扫描。 |
| **优化建议** | (1) 在任务创建后缓存 total count 到 TaskInfo 表；(2) 前端翻页时仅在首次请求 count，后续翻页复用缓存值。 |

### PERF-013 [Medium] 逐行正则参数提取产生大量 GC 压力

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/logparser/param_extractor.go:22, 49` |
| **问题描述** | 每行日志解析时，`FindAllStringSubmatch` 返回大量小字符串和 `map[string]string`，对于高吞吐解析场景造成显著 GC 暂停。 |
| **优化建议** | (1) 使用 `FindAllStringSubmatchIndex` + 手动切片避免字符串复制；(2) 使用对象池 (`sync.Pool`) 复用 map 实例。 |

---

## 四、并发安全问题清单

### CONC-001 [Critical] DAG 边条件延迟编译存在数据竞争

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/rootcause/topology_dag.go:134-154` |
| **问题描述** | `DAGEdge.MatchesNode()` 在首次调用时执行 `e.compile()` 修改 `e.compiled`、`e.fromMod` 等字段。如果多个 Worker goroutine 同时处理日志并调用该方法，将产生数据竞争（data race）。 |
| **优化建议** | (1) 在 `NewEngine()` 构造时预编译所有 DAG 边；或 (2) 使用 `sync.Once` 保护 `compile()` 方法。 |

### CONC-002 [High] Worker Panic 导致静默数据丢失

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:297-302` |
| **问题描述** | 日志解析 Worker 使用 `recover()` 捕获 panic 后直接 `wg.Done()` 返回。该 job 的结果不会被发送到 `results` channel，但 `allNewLogRecords` 已按 `totalValidLines` 预分配。结果：该位置的 `LogRecord` 保留零值，被静默写入数据库。 |
| **优化建议** | Panic 恢复后应发送一个标记错误的默认结果到 results channel，或使用 `errgroup` + 原子错误计数器明确追踪失败。 |

### CONC-003 [High] 知识库导入 Worker Panic 静默丢失页面

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:276-280` |
| **问题描述** | HTML 解析 Worker 的 `recover()` 仅日志记录 panic 但不向 `results` channel 推送错误对象。这些页面被静默丢弃，不计入失败计数器。 |
| **优化建议** | Panic 恢复后应发送一个 `parsedResult{err: ...}` 到 results channel，让收集端正确统计失败数量。 |

### CONC-004 [High] 日志清理 goroutine 存在竞争条件

| 字段 | 内容 |
|------|------|
| **涉及文件** | `pkg/logger/logger.go:134, 244, 250` |
| **问题描述** | 每次日志轮转时都 `go w.CleanOldLogs()` 启动新 goroutine。如果日志写入极快导致多次轮转，多个 `CleanOldLogs` 将并发执行，竞争读取目录并删除相同文件。 |
| **优化建议** | 使用 `sync.Once` 或 debounce 机制保证清理操作串行执行；或使用固定的后台 goroutine + channel 消费清理请求。 |

### CONC-005 [High] 日志写入全局互斥锁阻塞整个应用

| 字段 | 内容 |
|------|------|
| **涉及文件** | `pkg/logger/logger.go:159` |
| **问题描述** | `w.mu.Lock()` 在每次 `Write()` 时获取，包括可能触发的 `rotateLocked()` (文件关闭、重命名、创建新文件)。这意味着所有业务 goroutine 在日志轮转期间将被阻塞。 |
| **优化建议** | 使用 buffered channel + 单独的写 goroutine 实现异步日志写入，或改用成熟的轮转方案如 `lumberjack`。 |

### CONC-006 [Medium] 进度广播节流可能丢失关键状态更新

| 字段 | 内容 |
|------|------|
| **涉及文件** | `pkg/progress/progress.go:453-457` |
| **问题描述** | `throttleBroadcastLocked` 在 50ms 内丢弃重复广播。如果一个阶段在 50ms 内完成（如最后几行日志的快速处理），100% 进度更新可能被丢弃，导致前端停留在 99%。 |
| **优化建议** | 对关键状态变更（阶段切换、Complete、Fail）始终强制广播，仅对 `UpdateProgress` 等高频调用进行节流。 |

### CONC-007 [Medium] SSE 订阅者通道丢弃消息无通知

| 字段 | 内容 |
|------|------|
| **涉及文件** | `pkg/progress/progress.go:444-449` |
| **问题描述** | `broadcastLocked` 使用 `select/default` 非阻塞发送。缓冲区大小为 10 的 channel 在客户端消费慢时会静默丢弃更新，客户端无感知。 |
| **优化建议** | (1) 增大缓冲区至 50-100；(2) 丢弃消息时记录日志或设置标记让客户端知道有数据丢失。 |

### CONC-008 [Medium] 全局导入锁严重限制吞吐

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:83-84` |
| **问题描述** | `s.importMu.Lock()` 导致整个 Service 在导入期间全局加锁。只允许一个导入任务串行执行，即使不同文档之间无数据冲突。 |
| **优化建议** | 使用基于文档 ID 的细粒度锁 (`sync.Map` 存储 per-doc mutex)，或使用带限流的 Worker Pool 允许有限并发导入。 |

---

## 五、错误处理问题清单

### ERR-001 [Critical] 事务 `defer recover` 静默吞噬 panic 且不返回错误

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:324-328` |
| **问题描述** | 事务 defer 块仅执行 `tx.Rollback()` 但不 re-panic 或设置返回错误值。调用者将接收到 `nil` error，误认为导入成功，而实际数据已回滚丢失。 |
| **优化建议** | 使用命名返回值捕获 panic 信息：`defer func() { if r := recover(); r != nil { tx.Rollback(); err = fmt.Errorf(...) } }()`。 |

### ERR-002 [High] 多处 `recover()` 吞噬 panic 并丢失堆栈信息

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/logparser/parser.go:48-52`, `internal/hdx/html_parser.go:30-34`, `internal/hdx/navigator.go:46-50`, `internal/matcher/engine.go:115-120`, `internal/rootcause/engine.go:34-38` |
| **问题描述** | 项目广泛使用 `defer func() { if r := recover(); r != nil { ... } }()` 模式。这些 panic 恢复仅记录错误消息，丢失完整的 goroutine 堆栈信息，使生产环境调试极其困难。 |
| **优化建议** | (1) 使用 `runtime/debug.Stack()` 记录完整堆栈；(2) 优先修复 panic 的根本原因而非捕获；(3) 对于 library 代码，应返回 error 而非 panic。 |

### ERR-003 [Medium] 批量插入失败仅日志记录，不终止流程

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:411-413` |
| **问题描述** | `taskDB.Transaction(func(tx *gorm.DB) error { return tx.CreateInBatches(...) })` 失败时仅 `logger.Log.Errorf` 记录错误，但不 return error。后续流程继续执行 RCA 分析，基于不完整的数据产生错误的分析结果。 |
| **优化建议** | 批量插入失败后应立即标记任务状态为 `FAILED` 并 return error。 |

### ERR-004 [Medium] `DeleteTask` 忽略全局库删除错误

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:611` |
| **问题描述** | `_ = s.globalDB.Where("task_id = ?", taskID).Delete(&model.TaskInfo{})` 使用 `_` 忽略删除错误。如果全局库删除失败但物理文件已删除，将导致数据不一致。 |
| **优化建议** | 检查错误并在全局库删除失败时回退操作。 |

### ERR-005 [Low] 文件创建和 Save 操作错误被忽略

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:162-163, 380-382, 475-476` |
| **问题描述** | 多处 `s.globalDB.Save(&taskInfo)` 和 `taskDB.Save(&taskInfo)` 的返回错误被完全忽略。 |
| **优化建议** | 至少记录 warning 日志，关键路径上应返回 error。 |

---

## 六、资源管理问题清单

### RES-001 [Critical] 异步导入中 2 秒后删除临时目录 — 竞态灾难

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/document_handler.go:146-151` |
| **问题描述** | 上传 ZIP 后启动异步 goroutine 执行导入，然后 `time.Sleep(2 * time.Second); _ = os.RemoveAll(batchTempDir)`。如果导入耗时超过 2 秒（大文档几乎必然如此），临时目录在导入过程中被删除，导致导入失败甚至数据损坏。 |
| **优化建议** | 将清理逻辑放到异步 goroutine 的 `defer` 中：`defer os.RemoveAll(batchTempDir)`，确保导入完成后再清理。 |

### RES-002 [High] 任务删除不关闭数据库连接，Windows 上文件锁定

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:610-613` |
| **问题描述** | `DeleteTask` 调用 `storage.DeleteTaskDB` 删除物理数据库文件，但从未关闭已打开的 `*gorm.DB` 连接池。在 Windows 上，打开的文件句柄会阻止文件删除，导致 `os.Remove` 报错 "文件正在使用中"。 |
| **优化建议** | 在 `storage` 包中实现 `CloseTaskDB(taskID)` 方法，删除前先关闭连接池并从缓存中移除。 |

### RES-003 [High] 裸 goroutine 无边界控制，存在泄漏风险

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/document_handler.go:56, 146`, `internal/api/task_handler.go:121, 229` |
| **问题描述** | 多处使用 `go func() { ... }()` 启动异步任务，但没有 (1) context 传播与取消机制、(2) WaitGroup 等待、(3) 并发数量限制。如果用户频繁调用这些 API，可能产生大量并发 goroutine 消耗资源。 |
| **优化建议** | 使用带容量限制的 Worker Pool（如 `golang.org/x/sync/semaphore`），传递 `context.Context` 支持取消。 |

### RES-004 [Medium] 全局 Janitor goroutine 无法优雅关闭

| 字段 | 内容 |
|------|------|
| **涉及文件** | `pkg/progress/progress.go:513-530` |
| **问题描述** | `startJanitor()` 创建无限循环的后台 goroutine，使用 `time.NewTicker` 轮询清理过期任务。无法通过 context 取消或 channel 关闭来终止。 |
| **优化建议** | 接受 `context.Context` 参数，在 `select` 中同时监听 `ctx.Done()` 和 `ticker.C`。 |

### RES-005 [Medium] HDX 解码使用 `io.ReadAll` 全量读取

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/hdx/charset.go:23, 39` |
| **问题描述** | `DecodeGBK` 函数使用 `io.ReadAll` 将整个解码后的内容一次性读入内存。对于大型 HTML 文件会造成内存峰值。 |
| **优化建议** | 改用流式 `transform.NewReader` 直接包装给下游的 HTML 解析器使用。 |

### RES-006 [Low] Bleve `Close()` 存在死锁潜在风险

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/search/bleve_indexer.go:142-156` |
| **问题描述** | `Close()` 方法先锁 `idx.mu.Lock()` 再锁全局 `indexerMu.Lock()`，而 `InitIndexer` 先锁 `indexerMu` 再可能访问 `idx`。两个方法的锁获取顺序不一致，存在死锁风险。 |
| **优化建议** | 统一锁获取顺序：始终先获取全局 `indexerMu`，再获取实例级 `idx.mu`。 |

---

## 七、安全漏洞问题清单

### SEC-001 [Critical] CORS 配置错误 — `Allow-Origin: *` + `Allow-Credentials: true`

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/router.go:39-41` |
| **问题描述** | 同时设置 `Access-Control-Allow-Origin: *` 和 `Access-Control-Allow-Credentials: true` 在浏览器规范中是非法组合。现代浏览器会拒绝此配置，但部分旧版浏览器可能允许，造成 CSRF 攻击风险。 |
| **优化建议** | (1) 生产环境应设置明确的 Origin 白名单；(2) 使用 `gin-contrib/cors` 标准中间件替代手写逻辑。 |

### SEC-002 [High] taskID 未做严格校验，存在路径穿越风险

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:141, 508, 577` |
| **问题描述** | `taskID` 直接拼接到 `storage.GetOrCreateTaskDB(s.taskDir, taskID)` 中用于构建数据库文件路径。如果 API 层未对 `taskID` 做严格格式校验（如仅允许 hex 字符），攻击者可传入 `../../malicious` 在任意目录创建数据库文件。 |
| **优化建议** | (1) 在 API 层使用正则校验 taskID 格式 `^[a-f0-9]{16}$`；(2) 在 storage 层使用 `filepath.Clean` + `strings.HasPrefix` 确保路径在 taskDir 内。 |

### SEC-003 [Medium] 配置文件写入权限过宽

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/config/config.go:101, 199` |
| **问题描述** | 配置文件以 `0644` 权限写入。如果配置中包含敏感信息（数据库密码、API 密钥等），其他系统用户可读取。 |
| **优化建议** | 将文件权限改为 `0600`，仅允许文件所有者读写。 |

### SEC-004 [Medium] 上传路径清洗逻辑脆弱

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/api/document_handler.go:117-119` |
| **问题描述** | ZIP 文件上传后的路径清理逻辑使用简单字符串操作，可读性差且可能被绕过。 |
| **优化建议** | 使用 `filepath.Clean` + `filepath.Rel` + `strings.HasPrefix` 标准组合进行安全路径校验。 |

### SEC-005 [Low] 日志查询 LIKE 注入

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/service.go:521, 524, 530` |
| **问题描述** | `filter.Brief`、`filter.Hostname`、`filter.Keyword` 直接拼入 LIKE 模式 `"%"+filter.Keyword+"%"`。虽然 GORM 参数化防止了 SQL 注入，但 LIKE 通配符 `%` 和 `_` 可被利用进行拒绝服务（构造复杂 LIKE 模式触发全表扫描）。 |
| **优化建议** | 对用户输入中的 `%`、`_` 进行转义处理。 |

---

## 八、代码质量问题清单

### CQ-001 [Medium] HTML 报告导出使用字符串拼接而非模板引擎

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/task/exporter.go:13-161` |
| **问题描述** | 整个 HTML 报告（CSS、JavaScript、动态数据注入）通过 `strings.Builder` 手动拼接生成。极难维护、调试和扩展。 |
| **优化建议** | 使用 `html/template` 标准库，将 HTML 模板嵌入为 embed.FS 资源文件。 |

### CQ-002 [Medium] Scoring 模块大量魔数散落

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/matcher/scoring.go`, `internal/knowledge/service.go:529-553` |
| **问题描述** | 置信度计算中的阈值 `0.25`、`0.90`、`0.80`、`100`、`50`、`20`、`10` 等数值直接硬编码在代码中，无常量定义和文档说明。 |
| **优化建议** | 定义命名常量（如 `const ScoreExactProduct = 100`），并在注释中说明评分逻辑的设计依据。 |

### CQ-003 [Medium] 输入参数被意外修改

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/rootcause/engine.go:48-50` |
| **问题描述** | `Analyze` 方法直接修改传入的 `logs` 切片中元素的 `ID` 字段。这种隐式副作用会影响调用者的后续逻辑。 |
| **优化建议** | 在内部创建副本并修改副本，不修改输入参数。 |

### CQ-004 [Low] Parser 中使用字符串 switch 匹配正则组名

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/logparser/sec_parser.go:43-64`, `internal/logparser/vrp_parser.go:47-77` |
| **问题描述** | 使用 `SubexpNames()` + `switch name {}` 按字符串名匹配正则捕获组。每次匹配都需字符串比较，性能不如使用编译期确定的整数索引。 |
| **优化建议** | 使用 `FindStringSubmatchIndex` + 预计算的索引常量直接访问捕获组。 |

### CQ-005 [Low] 知识库去重哈希切片未去重

| 字段 | 内容 |
|------|------|
| **涉及文件** | `internal/knowledge/service.go:354-365` |
| **问题描述** | 批量查询已存在的 hash 时，`hashes` 切片中可能存在重复值。大量重复 hash 提交到 `WHERE content_hash IN (...)` 子句中增加无谓的 I/O。 |
| **优化建议** | 先通过 `map[string]struct{}` 去重，再构造查询条件。 |

---

## 九、测试覆盖缺口

### TEST-001 缺少并发压力测试

| 涉及模块 | 说明 |
|---------|------|
| `rootcause` | 无大规模日志（10K+ 行）下 RCA 滑动窗口的性能/正确性测试 |
| `matcher` | 无缓存膨胀测试，未验证高并发匹配下的线程安全性 |
| `logger` | 无 `CleanOldLogs` 并发安全测试 |
| `storage` | 无 SQLite 并发读写压力测试 |

### TEST-002 缺少边界条件测试

| 涉及模块 | 说明 |
|---------|------|
| `task/service` | 未测试 Worker panic 后数据完整性（零值 LogRecord 是否被正确处理） |
| `knowledge` | 未测试事务 rollback 后数据一致性 |
| `search` | 未测试大批量索引时的内存使用情况 |

### TEST-003 缺少集成/端到端测试

| 涉及模块 | 说明 |
|---------|------|
| `api` | 无 HTTP API 端到端测试（仅有 `api_test.go` 但未覆盖关键路径） |
| `task` | 缺少实际大文件上传 → 解析 → 匹配 → RCA 全流程集成测试 |

### TEST-004 缺少 Benchmark 测试

| 涉及模块 | 说明 |
|---------|------|
| `logparser` | 无 `BenchmarkParseLine` 吞吐量基准测试 |
| `matcher` | 无 `BenchmarkMatch` 匹配延迟基准测试 |
| `knowledge` | 无 `BenchmarkFindBestKnowledgeMatchPtr` 打分性能测试 |
| `deduplicator` | 无 `BenchmarkCalculateContentHash` 哈希计算基准测试 |

---

## 十、综合优化方案

### 10.1 架构优化路线图

#### 阶段一：消除关键风险（1-2 周）

| 优先级 | 优化项 | 对应问题 | 工作量 |
|-------|-------|---------|-------|
| P0 | 修复临时目录竞态删除 | RES-001 | 0.5h |
| P0 | 修复 CORS 安全配置 | SEC-001 | 1h |
| P0 | 日志文件流式处理替代全量读入 | PERF-001 | 4h |
| P0 | 事务 panic 正确返回错误 | ERR-001 | 1h |
| P0 | taskID 路径穿越防护 | SEC-002 | 1h |
| P1 | RCA 日志改为游标/分页加载 | PERF-002 | 4h |
| P1 | 批量插入失败终止流程 | ERR-003 | 1h |
| P1 | DAG 边预编译消除竞态 | CONC-001 | 2h |
| P1 | Worker Panic 发送错误结果 | CONC-002, CONC-003 | 2h |

#### 阶段二：性能优化（2-3 周）

| 优先级 | 优化项 | 对应问题 | 工作量 |
|-------|-------|---------|-------|
| P1 | 匹配缓存改用 LRU | PERF-005 | 4h |
| P1 | Bleve 索引分批写入 | PERF-003 | 2h |
| P1 | N+1 查询改为批量查询 | PERF-008 | 2h |
| P1 | SQLite 连接数调优 | PERF-010 | 1h |
| P2 | RCA 算法优化（倒排索引 + 双指针） | PERF-004 | 1w |
| P2 | 统计缓存层 | PERF-009 | 4h |
| P2 | `FindBestKnowledgeMatchPtr` 字符串预处理 | PERF-007 | 2h |
| P2 | 哈希计算优化 | PERF-011 | 1h |
| P3 | 参数提取使用 `sync.Pool` | PERF-013 | 4h |

#### 阶段三：架构重构（4-6 周）

| 优先级 | 优化项 | 对应问题 | 工作量 |
|-------|-------|---------|-------|
| P2 | 引入接口抽象层 | ARC-002 | 1w |
| P2 | 消除全局状态，引入 DI | ARC-001 | 1w |
| P2 | Handler 层逻辑下沉到 Service | ARC-003 | 3d |
| P2 | 实现 Graceful Shutdown | ARC-008 | 4h |
| P3 | 拆分 `importSingleDocUnlocked` | ARC-006 | 4h |
| P3 | 分离 Domain Model 和 ORM DTO | ARC-007 | 1w |
| P3 | HTML 报告改用模板引擎 | CQ-001 | 4h |

### 10.2 关键代码修复示例

#### 示例 1：修复临时目录竞态删除 (RES-001)

```go
// 修复前 (document_handler.go)
go func() {
    knowledgeSvc.ImportDocumentFromDir(batchTempDir, mode, tr)
}()
time.Sleep(2 * time.Second)
_ = os.RemoveAll(batchTempDir)

// 修复后
go func() {
    defer os.RemoveAll(batchTempDir)  // 导入完成后再清理
    knowledgeSvc.ImportDocumentFromDir(batchTempDir, mode, tr)
}()
```

#### 示例 2：事务 Panic 正确返回错误 (ERR-001)

```go
// 修复前 (knowledge/service.go)
defer func() {
    if r := recover(); r != nil {
        tx.Rollback()  // 错误被吞噬！
    }
}()

// 修复后 — 使用命名返回值
func (s *Service) importSingleDocUnlocked(...) (stats *ImportStats, err error) {
    tx := s.db.Begin()
    defer func() {
        if r := recover(); r != nil {
            tx.Rollback()
            err = fmt.Errorf("panic in import: %v\n%s", r, debug.Stack())
        }
    }()
    // ...
}
```

#### 示例 3：匹配缓存改用 LRU (PERF-005)

```go
import lru "github.com/hashicorp/golang-lru/v2"

type MatchEngine struct {
    // 替换无限增长的 sync.Map
    cache         *lru.Cache[string, *matchCacheItem]     // 正向缓存，容量 100K
    negativeCache *lru.Cache[string, struct{}]             // 负缓存，容量 50K
    regexCache    *lru.Cache[string, *regexp.Regexp]       // 正则缓存，容量 10K
}
```

#### 示例 4：Graceful Shutdown (ARC-008)

```go
func main() {
    // ... 初始化代码 ...

    srv := &http.Server{
        Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
        Handler: r,
    }

    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("HTTP server error: %v", err)
        }
    }()

    // 等待终止信号
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Infof("Shutting down gracefully...")
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    srv.Shutdown(ctx)
    indexer.Close()
    if sqlDB, err := globalDB.DB(); err == nil {
        sqlDB.Close()
    }
    log.Infof("Server stopped.")
}
```

#### 示例 5：流式日志处理替代全量读入 (PERF-001)

```go
// 修复前 (task_handler.go)
content, err := io.ReadAll(f)  // 全部读入内存！

// 修复后 — 流式分行传输
func (h *TaskHandler) streamFileToItems(f multipart.File, fileName string) <-chan string {
    lines := make(chan string, 4096)
    go func() {
        defer close(lines)
        scanner := bufio.NewScanner(f)
        buf := make([]byte, 64*1024)
        scanner.Buffer(buf, 1024*1024)
        for scanner.Scan() {
            if l := strings.TrimSpace(scanner.Text()); l != "" {
                lines <- l
            }
        }
    }()
    return lines
}
```

### 10.3 性能优化预估收益

| 优化项 | 当前瓶颈 | 预期改善 |
|-------|---------|---------|
| 流式日志处理 | 500MB 文件 → OOM 崩溃 | 支持 10GB+ 文件，内存占用 < 100MB |
| LRU 匹配缓存 | 缓存无限增长 → 数小时后 OOM | 内存占用稳定在 ~50MB |
| RCA 游标分页 | 100K 日志 → 1GB+ 内存 | 内存占用降至 ~50MB |
| 批量查询替代 N+1 | 10 次搜索结果 → 10 次 SQL 查询 | 1 次批量查询，延迟降低 80%+ |
| SQLite 并发读 | 连接数=1，读操作串行化 | 5-10 并发读，API 响应延迟降低 50%+ |
| RCA 倒排索引优化 | O(N²) 10K 日志 → 数秒 | O(N×M) M<<N，延迟降低 10 倍+ |

---

## 附录：问题汇总表

| 编号 | 类型 | 严重级别 | 文件 | 简要描述 |
|------|------|---------|------|---------|
| ARC-001 | 架构 | Critical | 多文件 | 全局可变状态泛滥 |
| ARC-002 | 架构 | High | api, task, matcher | 缺少接口抽象 |
| ARC-003 | 架构 | High | api/stats,knowledge,task | Handler 层分层违反 |
| ARC-004 | 架构 | Medium | config | Config 直接操作 Logger |
| ARC-005 | 架构 | Medium | knowledge/service | `...interface{}` 参数 |
| ARC-006 | 架构 | Medium | knowledge/service | God Function 270 行 |
| ARC-007 | 架构 | Medium | model/* | Domain 与 ORM 耦合 |
| ARC-008 | 架构 | Medium | main.go | 缺少 Graceful Shutdown |
| ARC-009 | 架构 | Low | main.go | 传递 nil 依赖 |
| PERF-001 | 性能 | Critical | task_handler | 大文件全量读入内存 |
| PERF-002 | 性能 | Critical | task/service | RCA 全量日志加载 |
| PERF-003 | 性能 | Critical | search/bleve_indexer | Bleve 批量索引无分块 |
| PERF-004 | 性能 | High | rootcause/engine | RCA 算法 O(N²) |
| PERF-005 | 性能 | High | matcher/engine | 缓存无限增长 |
| PERF-006 | 性能 | High | matcher/engine | 知识库全量内存加载 |
| PERF-007 | 性能 | High | knowledge/service | 内层循环重复字符串转换 |
| PERF-008 | 性能 | High | api/knowledge_handler | N+1 查询 |
| PERF-009 | 性能 | High | api/stats_handler | 全表聚合查询 |
| PERF-010 | 性能 | Medium | storage/knowledge_db | SQLite 连接数=1 |
| PERF-011 | 性能 | Medium | knowledge/deduplicator | fmt.Sprintf 低效哈希 |
| PERF-012 | 性能 | Medium | task/service | 每次分页 COUNT(*) |
| PERF-013 | 性能 | Medium | logparser/param_extractor | 正则参数提取 GC 压力 |
| CONC-001 | 并发 | Critical | rootcause/topology_dag | DAG 延迟编译数据竞争 |
| CONC-002 | 并发 | High | task/service | Worker Panic 静默数据丢失 |
| CONC-003 | 并发 | High | knowledge/service | 导入 Worker Panic 丢失 |
| CONC-004 | 并发 | High | pkg/logger | 日志清理竞争条件 |
| CONC-005 | 并发 | High | pkg/logger | 写入全局锁阻塞 |
| CONC-006 | 并发 | Medium | pkg/progress | 节流丢失关键更新 |
| CONC-007 | 并发 | Medium | pkg/progress | SSE 消息静默丢弃 |
| CONC-008 | 并发 | Medium | knowledge/service | 全局导入锁 |
| ERR-001 | 错误处理 | Critical | knowledge/service | 事务 panic 吞噬错误 |
| ERR-002 | 错误处理 | High | 多文件 | recover 丢失堆栈 |
| ERR-003 | 错误处理 | Medium | task/service | 批量插入失败不终止 |
| ERR-004 | 错误处理 | Medium | task/service | DeleteTask 忽略错误 |
| ERR-005 | 错误处理 | Low | task/service | Save 错误被忽略 |
| RES-001 | 资源管理 | Critical | api/document_handler | 2s 后删临时目录竞态 |
| RES-002 | 资源管理 | High | task/service | 删除任务不关闭 DB |
| RES-003 | 资源管理 | High | api/document,task_handler | 裸 goroutine 无边界 |
| RES-004 | 资源管理 | Medium | pkg/progress | Janitor 无法关闭 |
| RES-005 | 资源管理 | Medium | hdx/charset | io.ReadAll 全量读取 |
| RES-006 | 资源管理 | Low | search/bleve_indexer | Close 锁顺序不一致 |
| SEC-001 | 安全 | Critical | api/router | CORS 配置错误 |
| SEC-002 | 安全 | High | task/service | taskID 路径穿越 |
| SEC-003 | 安全 | Medium | config | 配置文件权限过宽 |
| SEC-004 | 安全 | Medium | api/document_handler | 上传路径清洗脆弱 |
| SEC-005 | 安全 | Low | task/service | LIKE 通配符注入 |
| CQ-001 | 代码质量 | Medium | task/exporter | 字符串拼接 HTML |
| CQ-002 | 代码质量 | Medium | matcher/scoring | 魔数散落 |
| CQ-003 | 代码质量 | Medium | rootcause/engine | 修改输入参数 |
| CQ-004 | 代码质量 | Low | logparser | 字符串 switch 匹配 |
| CQ-005 | 代码质量 | Low | knowledge/service | 哈希切片未去重 |
| TEST-001 | 测试 | High | 多模块 | 缺少并发压力测试 |
| TEST-002 | 测试 | Medium | 多模块 | 缺少边界条件测试 |
| TEST-003 | 测试 | Medium | 多模块 | 缺少集成测试 |
| TEST-004 | 测试 | Low | 多模块 | 缺少 Benchmark |

---

> **总计发现问题**: 48 项  
> - Critical: 8 项  
> - High: 16 项  
> - Medium: 18 项  
> - Low: 6 项  
>
> **建议优先级**: 先修复 8 个 Critical 问题（约 1-2 周），再推进 High/Medium 性能优化（2-3 周），最后进行架构重构（4-6 周）。
