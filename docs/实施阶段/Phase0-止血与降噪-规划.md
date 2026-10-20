# Phase 0 实施方案：止血与降噪

> 依据：《产品功能与体验优化分析及演进规划》v1.1 §8.2 阶段总览（Phase 0，第 1~2 周）
> 状态：规划稿（实施前）
> 目标版本：v1.1

---

## 1. 阶段定位

Phase 0 不新增业务能力，只做三件事：**把误导性入口撤出主航道、把默认路径缩短、把"坏掉/说不清"的地方修到可信**。

阶段总览给出的 Phase 0 交付与成功标志：

| 维度 | 原文要求 | 本阶段落地口径 |
| :-- | :-- | :-- |
| 命名/状态/操作列 | UI 文案按术语表收敛；状态中文化；操作收纳 | 导航与工作台术语、任务状态中文映射、案例库操作列下拉收纳 |
| RCA 实验化 | 设置页实验开关 + 高级抽屉；取消一级入口；修 confidence/LOW | feature flag（默认关）+ 手动触发；RCA 只出现在"高级"抽屉；修 confidence 0→80% 与 LOW 不可筛 |
| 设备按钮 | 设备管理从一级视图变为按钮+抽屉 | Header"设备"按钮 + 右侧抽屉 |
| 三 Tab | 一级视图 5 → 3 | 日志分析 / 事件时间线 / 分析报告 |
| 知识库空结果/错误态 | 修 total=1；渲染 searchError | Bleve 哨兵文档排除 + 前端错误态与重试 |
| 索引健康 | 设置页可见、可重建 | 设置页"知识库与索引"分区 + 存储概览 |
| 前端冒烟 | 4 条红路线有自动化测试 | 后端红路线集成测试 + 前端契约断言脚本 |
| 成功标志 | 主界面无 RCA 一级入口；设备/高级按钮可用；4 条红路线有自动化测试 | 同左 |

---

## 2. 现状核对（代码事实）

| 现状 | 位置 | 对本阶段的影响 |
| :-- | :-- | :-- |
| 工作台 5 个视图：workbench / devices / multi-timeline / rca / multi-report | `web/src/constants/viewModes.js`、`WorkbenchNavBar.vue` | 需收敛为 3 Tab，设备与 RCA 改抽屉 |
| 右栏同时有"官方知识"与"RCA 拓扑"两个 Tab | `LogKnowledgePanel.vue` | 删除 RCA Tab |
| Header 5 个同级主操作 | `WorkbenchHeader.vue` | 收敛为导入/导出/设备/高级/更多 |
| 导入即自动跑 RCA | `task/service.go` 三处 `TriggerTaskRCA` | 改为受 feature flag 控制，默认不跑 |
| 切换任务即全量拉 RCA | `stores/task.js#switchTask` | flag 关闭时不请求 |
| `Search` 的 `Total` 含 mapping 哨兵文档 | `search/bleve_indexer.go:569` | 空库 total=1 的直接原因 |
| 任务列表全量返回 | `task/service.go#GetTaskList` | 无搜索/筛选/分页 |
| `GET /tasks` 被工作台选择器/案例库/仪表盘共用 | `stores/task.js`、`Tasks.vue`、`Dashboard.vue` | 不能直接改契约 |
| 设置页只有日志策略 | `views/Settings.vue` | 需补存储/索引/实验室/关于 |
| 无单实例保护 | `cmd/LogAuditorGo/main.go` | 双开会写同一 DB |
| RcaCenter confidence 默认 0.8、缺 LOW | `components/RcaCenter.vue` | 实验功能仍需可用的显示 |

---

## 3. 实施方案

### 3.1 后端

#### A. Feature Flag（`internal/config`）

新增 `features` 配置段并落默认配置文件：

```yaml
features:
  rca: false
  native_file_dialog: true
  browser_upload: false
  lan_mode: false
  masking: false
```

- `Config.Features FeaturesConfig`
- `config.Features()`：读全局配置快照，未初始化时返回安全默认值（RCA=false）。
- `config.UpdateFeatures(patch) (FeaturesConfig, error)`：写内存 + `Save("")` 持久化；并发安全。
- 开关语义只影响 UI/自动行为，不删除任何 RCA 代码（保留一个版本周期回滚能力）。

#### B. RCA 退出主航道（`internal/task`）

- 导入完成、重新分析完成、删除文件后重算三处自动派发，前置判断 `config.Features().RCA`。
- 关闭时：不写 `RCAStatus=QUEUED`、不派发 worker、清空旧事件仅在用户主动重算时发生。
- `POST /tasks/:id/rca/reanalyze` 保留为手动入口（实验开关开启后由前端调用）。
- 新增 `GET /system/features`、`PUT /system/features`（仅允许已知开关名）。

#### C. 知识库检索正确性（`internal/search`）

- `Search` 的根查询增加 `MustNot: DocID(__mapping_version__)`，使 `Total` 与 `Hits` 都不再包含哨兵文档。
- 保留 `IsMappingOutdated` 与索引健康语义不变。

#### D. 任务列表检索与分页（`internal/task` + `internal/api`）

**不改 `GET /tasks` 契约**（工作台选择器与仪表盘依赖数组），新增 `GET /tasks/query`：

| 参数 | 说明 |
| :-- | :-- |
| keyword | 任务名/任务 ID/结论模糊匹配（LIKE 转义） |
| status | PENDING/PROCESSING/COMPLETED/FAILED（多值逗号分隔） |
| case_status | 预留（Phase 1 生效） |
| start_from / start_to | 创建时间范围 |
| sort_by | start_time / log_count / matched_count |
| order | asc / desc |
| page / page_size | 默认 1 / 20，上限 200 |

响应：`{ items, total, page, page_size }`。

#### E. 系统信息（`internal/api`）

- `GET /system/storage`：数据目录、知识库/Bleve/任务库/日志目录占用、知识条目数、文档数、任务数；版本信息。
- `GET /system/features`、`PUT /system/features`。
- 索引健康复用已存在的 `GET /system/knowledge-index/status`。

#### F. 单实例保护（`internal/singleinstance` + `cmd/LogAuditorGo`）

- Windows 命名互斥体（目录路径 SHA-256 前缀命名），非 Windows 使用带 PID 校验的锁文件。
- 首个实例写 `<data_dir>/instance.json`（pid + 访问 URL），退出时删除。
- 第二个实例读取 `instance.json` 并打开浏览器后退出，不启动第二个服务。
- 环境变量 `LOGAUDITORGO_DISABLE_SINGLE_INSTANCE=1` 可禁用（自动化/多实例调试）。

### 3.2 前端

#### A. 信息架构收敛

- 导航文案：日志审计工作台 → **排障工作台**；历史任务 → **案例库**；产品文档管理 → **产品文档**（Phase 2 并入知识中心）。
- 工作台三 Tab：**日志分析 / 事件时间线 / 分析报告**（`viewModes.js` 保留旧值到新值的迁移映射，避免 localStorage 脏值导致空白）。
- 设备管理改为 Header"设备"按钮 + 右侧抽屉。
- 新增"高级"抽屉：专家筛选入口、关联事件（实验开关开启时懒挂载 RcaCenter）、原始字段/诊断信息、批量操作入口。
- 右栏删除 RCA Tab；未匹配时给出可执行动作（检索知识 / 复制模块助记符 / 补充本地知识入口占位）。

#### B. Header 收敛

左：任务选择 + 状态标签 + 时间范围 + 设备数；右：导入 / 导出 / 设备 / 高级 / 更多（重新分析、刷新、已导入文件、删除）。

#### C. 案例库（Tasks.vue）

搜索框、状态筛选、时间范围、排序、分页；状态中文 + 颜色语义（PROCESSING 不再用 danger）；操作列收敛为"查看 + 更多"；移除"设备类型"列与"根因事件"列（实验能力不进入任务 KPI）。

#### D. 设置页

分区：常规（日志策略）、数据与存储（占用 + 清理日志）、知识库与索引（状态/漂移/重建）、实验室功能（关联事件开关 + 免责声明）、关于/诊断（版本、路径、单实例说明）。

#### E. 仪表盘快修

新增"最近任务"（5 条）+ 运行中/失败提示；空图表改为带行动按钮的空态；累计指标降级为次级卡片。首启 checklist 留到 Phase 2。

#### F. 错误态与空态

- 知识库：渲染 `searchError` + 重试；空结果显示"没有匹配的知识"而不是"共 1 条"。
- 工作台未匹配右栏给出下一步。

### 3.3 测试

- Go：`search` 哨兵排除单测；`task` 列表查询单测；`api` 红路线集成测试（4 条）。
- 前端：`web/scripts/verify-ui-contract.mjs`（纯 Node，无新依赖）断言状态映射、视图模式迁移、RCA 证据强度格式化、三 Tab 定义。

---

## 4. 验收标准

| # | 验收项 | 方法 |
| :-- | :-- | :-- |
| 1 | 默认界面无 RCA 一级入口、无右栏 RCA Tab | 代码检查 + 前端契约脚本 |
| 2 | 关闭 flag 时导入不自动产生 RCA 事件、不拉取 RCA | Go 集成测试 |
| 3 | 开启 flag 后高级抽屉出现"关联事件（实验性）"，手动可触发 | API 测试 + 手动核对 |
| 4 | 设备按钮 1 次点击打开抽屉 | 组件结构核对 |
| 5 | 知识库空库 `total=0`；有数据时 total 与命中一致 | Go 单测 |
| 6 | 案例库 100 个任务下搜索/筛选/分页可用，响应 < 300ms | 单元测试 + 本地实测 |
| 7 | 设置页可见索引状态/存储占用/实验室开关 | 页面核对 |
| 8 | 双开不会启动第二个服务 | 单元测试（非 Windows 锁文件路径）+ 手工核对 |
| 9 | 4 条红路线自动化测试通过 | `go test ./...` + `npm run test:unit` |

---

## 5. 风险与回滚

| 风险 | 缓解 |
| :-- | :-- |
| 移除 RCA 入口导致旧任务无法查看事件 | 开关开启后仍可从高级抽屉进入；数据与接口不删 |
| 自动 RCA 关闭与既有测试断言冲突 | 测试改为显式开启 flag 或显式调用 `TriggerTaskRCA`，保留手动入口覆盖 |
| 任务列表分页改动破坏三处调用方 | 保留 `GET /tasks` 契约，新增 `/tasks/query` |
| 视图模式枚举变更导致 localStorage 脏值 | `normalizeViewMode` 做旧值迁移 + 非法值回退 |
| 单实例影响开发调试 | 环境变量禁用 + 仅 main 装配，不进入业务包 |
| Bleve 查询结构变化影响召回 | MustNot 仅排除固定 DocID；补充"命中/空库/关键词"三类单测 |

**回滚**：feature flag 置 true 即恢复 RCA 自动运行；前端 IA 为纯展示层改动，回滚只需还原组件；后端新增端点均为增量。
