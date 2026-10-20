# Phase 0 方案审视：止血与降噪

> 审视对象：`Phase0-止血与降噪-规划.md`
> 审视时间：实施前
> 审视方法：逐条对照 §8.2 交付与成功标志 → 核对代码现状（文件/行） → 检查对既有契约与测试的影响 → 给出调整结论

---

## 1. 总体结论

**方案方向成立，但不能按原稿直接开工。** 发现 6 个会阻断验收或引入回归的问题，其中 3 个必须在实施前改口径：任务列表接口契约、RCA 关闭后的测试基线、三 Tab 的"分析报告"载体。其余为补充约束。

---

## 2. 逐项审视

### 2.1 阶段目标 vs 交付：覆盖完整

| 阶段总览要求 | 规划对应 | 结论 |
| :-- | :-- | :-- |
| 命名/状态/操作列 | §3.2 A/C | 覆盖 |
| RCA 设置页实验开关 + 高级抽屉 | §3.1 A/B + §3.2 A | 覆盖 |
| 设备按钮 | §3.2 A | 覆盖 |
| 三 Tab | §3.2 A | 覆盖，但需改载体（见 2.3） |
| 知识库空结果/错误态 | §3.1 C + §3.2 F | 覆盖 |
| 索引健康 | §3.1 E + §3.2 D | 覆盖 |
| 前端冒烟 | §3.3 | 覆盖，但技术路径需替换（见 2.4） |

### 2.2 阻断项 1：`GET /tasks` 契约变更风险被低估

规划原稿写"新增 /tasks/query"，但同时又把案例库描述为"分页查询"。核实：`stores/task.js#fetchTasks` 直接取 `res.data` 为数组，`Dashboard.vue`、`WorkbenchHeader.vue` 的任务选择器都依赖同一数组；`internal/api/api_test.go` 未断言 `GET /tasks` 结构。

**结论**：
- 保留 `GET /tasks`（数组，全量）供选择器与快速视图使用；
- 新增 `GET /tasks/query`（分页对象）只给案例库；
- 案例库首次加载走 `/tasks/query`，工作台仍走 `/tasks`，二者共享同一 `ListTasks` 查询构造器，避免两套过滤语义。

### 2.3 阻断项 2："分析报告"Tab 没有现成载体

规划原稿把 `MultiDeviceReport.vue` 当作"分析报告"Tab。核实：该组件是**多设备对比报告的导出参数页**（选择设备 → 导出 HTML），不是报告预览，直接挂载会让"分析报告"变成第二个导出入口，与阶段总览"合并多设备报告到分析报告"的意图不符。

**结论**：Phase 0 的"分析报告"Tab 定义为**报告中枢**：
- 任务概览（结论占位、指标、时间范围、设备数）；
- 导出动作（全量 HTML/CSV、多设备报告入口、筛选子集）；
- 报告说明与"证据链将在 Phase 1 补齐"的明确提示。

`MultiDeviceReport` 作为该 Tab 内的入口组件保留，不再是一级视图。Phase 1 再替换为完整报告预览。

### 2.4 阻断项 3：前端 E2E 技术路径不可行

规划原稿写"4 条红路线自动化测试"，但没有定义载体。核实：`web/package.json` 仅有 `node scripts/verify-logFormatter.mjs`，无 Playwright/Vitest，且本机为离线环境，下载浏览器二进制不可控。

**结论**：Phase 0 采用双层冒烟：
1. **Go 红路线集成测试**（`internal/api/red_route_test.go`）：真实 router + 临时库，覆盖"导入→日志可见""知识命中→可解释""导出→可下载""案例检索→可找回"；
2. **前端契约脚本**（`web/scripts/verify-ui-contract.mjs`）：状态映射、视图模式迁移、RCA 证据强度、三 Tab 常量。
Playwright 与视觉回归推迟到 Phase 3 的性能/可用性阶段评估，不在 Phase 0 承诺。

### 2.5 阻断项 4：关闭自动 RCA 与既有测试的耦合

核实：`rca_end_to_end_test.go` 均显式调用 `TriggerTaskRCA`，不依赖自动派发；`rca_scheduler_test.go` 直接写 `rca_status`。因此关闭自动派发**不会**破坏现有测试，但需要新增"关闭时导入不产生事件/不写 QUEUED"的反向测试。

**结论**：
- 自动派发点统一收口到一个 `autoRCAEnabled()` 判定；
- 新增 feature flag 默认值测试、关闭态导入测试；
- 手动接口保留，测试覆盖"开关关闭 + 手动调用仍可执行"的运维路径。

### 2.6 阻断项 5：三 Tab 与 localStorage 脏值

核实：`filterStore` 持久化 `viewMode`，`useSubViewRouter` 只做 `isValidViewMode` 校验。若直接删除 `devices/rca/multi-report` 枚举，旧用户 localStorage 中的值会回退默认视图（可接受），但 `multi-timeline` 若改名也会被丢弃，导致"上次视图"丢失。

**结论**：新增 `normalizeViewMode()`：`multi-timeline→timeline`、`workbench→logs`、`multi-report→report`；`devices/rca` 回退 `logs` 并同时打开对应抽屉的意图标记（仅迁移，不自动弹窗）。

### 2.7 阻断项 6：单实例保护可能污染测试与开发

规划原稿在 `main.go` 直接加锁。核实：`go test ./cmd/LogAuditorGo` 不执行 `main()`，风险可控；但开发者本地多实例调试、CI 并行运行会受影响。

**结论**：
- 锁只在 `main()` 装配，业务包不感知；
- 支持 `LOGAUDITORGO_DISABLE_SINGLE_INSTANCE=1`；
- 锁文件写入失败只告警不阻断（只读数据目录场景）；
- 第二个实例优先读 `instance.json` 的 URL，读不到则用配置端口拼 URL。

---

## 3. 补充约束（实施中必须遵守）

1. **不改 API/DB 字段语义**：本阶段只加列/加端点，不删列；RCA 相关字段保留。
2. **术语只改 UI**：`task_id`、`rca_status`、`device_type` 等标识符不动；"审计"字样只保留在历史 URL/接口名中。
3. **状态颜色语义**：PROCESSING 使用 primary + spinner，不得再用 danger；FAILED 提供失败原因 tooltip。
4. **高级抽屉懒挂载**：RCA 组件仅在"开关开启 + 抽屉打开 + 用户点击关联事件"时挂载，满足"任务切换不拉取 RCA"。
5. **未匹配右栏**：至少给出三个动作（检索知识库 / 复制模块与助记符 / 补充本地知识入口占位），不得只显示空状态。
6. **空态与错误态分离**：知识库错误态必须可重试；空结果文案不得出现计数。
7. **删除能力保留**：Header"更多"中的删除必须二次确认，并提示"删除后不可恢复"；归档能力 Phase 1 提供，本阶段不承诺。
8. **批量操作**：高级抽屉中的批量操作直接复用现有 `AdvancedFilterModal` 的批量打标能力，不新增并行实现。

---

## 4. 调整后的实施顺序

1. 后端：features 配置 → RCA 收口 → 知识库 total 修复 → 任务查询 → storage/features 端点 → 单实例。
2. 前端：契约常量/状态组件 → 工作台 IA（三 Tab + 抽屉 + 右栏） → Header → 案例库 → 设置页 → 仪表盘 → 知识库错误态。
3. 测试：Go 单测/集成 → 前端契约脚本 → 全量 `go test ./...` + `npm run build`。
4. 文档：README 与架构说明书的"RCA 主功能/5 视图/3 入口"描述同步修正。

---

## 5. 审视结论

**通过（带修改）。** 按 §2 的 6 项调整与 §3 的 8 条约束实施；原始规划中的"前端 E2E"降级为"后端集成 + 前端契约脚本"，并在 Phase 3 重新评估真实浏览器回归。
