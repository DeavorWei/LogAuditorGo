# Phase 3 实施方案：体验与规模化

> 依据：《产品功能与体验优化分析及演进规划》v1.1 §8.2（Phase 3，第 11~14 周）与 W11~W14
> 状态：规划稿（实施前）
> 目标版本：v1.4.0

---

## 1. 阶段定位

Phase 1/2 解决了“能导入、能看摘要、能补知识、能交接”；Phase 3 要让**大规模日志真正可用**：

1. **降噪**：注释行默认隐藏、语义严重度、时间直方图、聚合视图；
2. **可定位**：URL 深链接（视图/筛选/选中日志），刷新/分享不丢上下文；
3. **可扩展**：知识分面（Phase 2 已完成）、性能基准、分页与渲染优化；
4. **可交付**：RCA 只保留冒烟、可用性测试脚本与发布说明。

成功标志（§8.2）：**10 万行任务可用；SUS ≥ 70；RCA 默认关闭且无演进。**

---

## 2. 阶段范围

| 纳入 | 说明/不做 |
| :-- | :-- |
| 注释行默认隐藏 + “显示注释（N）” | 保留后台存储与导出 |
| 语义严重度标签 | 严重/错误/警告/提示 + Lv.N |
| 时间直方图（严重度堆叠） | 点击柱状筛选时间段；不做拖拽刷选（交互成本高） |
| 聚合视图 | module/brief/hostname + 300s 窗口；点击回到明细 |
| 深链接 | view/keyword/module/severity/matched/device/log/comments/mode |
| 分页与渲染优化 | 保留分页；聚合/直方图按需加载；不做完整虚拟滚动（收益/风险比不足） |
| 性能基准 | Go benchmark：10 万行摘要/查询/直方图/聚合；不做 50 万行实测（机器/时间限制，给出方法） |
| 可用性测试 | 提供 5 人任务脚本 + SUS 问卷；**未执行真实用户测试**（无受试者） |
| RCA 只保留冒烟 | 默认关闭、导入不派发、手动可触发 |

---

## 3. 后端设计

### 3.1 注释默认隐藏

- `LogQueryRequestBody.ExcludeComment` / `LogQueryFilter.ExcludeComment`；
- `BuildLogFilterScope` 在 SQL 层追加 `module <> 'COMMENT'`；
- 新增 `GET /tasks/:id/logs/comments/count` 供“显示注释（N）”；
- 默认值在**前端**为隐藏，API 默认仍包含，保持旧调用兼容。

### 3.2 时间直方图

`POST /tasks/:id/logs/histogram?buckets=48`：
- 复用统一筛选 Scope；
- 读取 `timestamp, severity` 两列在内存分桶（10 万行约 3MB），避免 SQLite 时间字符串/时区差异；
- 返回 `buckets[{start,end,severe,error,warning,info,total}]` 与 `bucket_seconds`。

### 3.3 聚合视图

`POST /tasks/:id/logs/aggregated?window_seconds=300&limit=200`：
- 按 `module+brief+hostname+时间窗` 折叠；
- 返回次数、首末时间、最高严重度、样例日志 ID；
- 前端点击组 → keyword `module/brief` 回到明细。

### 3.4 性能基准

`internal/task/benchmark_test.go`：
- 构造 10 万行任务（5 设备、4 模块、严重度 2/3/4）；
- 基准：诊断摘要、日志分页查询、直方图、聚合；
- 以 `-benchtime=1x` 记录单次耗时，写入本阶段审视文档。

### 3.5 RCA 冒烟

- 已有红路线覆盖默认关闭 + 手动触发；
- 新增阶段测试断言：导入后 `RCAStatus=PENDING`、`RcaCount=0`。

---

## 4. 前端设计

| 模块 | 方案 |
| :-- | :-- |
| 注释开关 | 工具栏“显示注释（N）”复选框；写入 `exclude_comment` |
| 语义严重度 | `utils/severity.js` + `SeverityTag` 输出中文语义 |
| 时间直方图 | `LogHistogram.vue` ECharts 堆叠柱；点击柱设置 timeStart/End |
| 聚合视图 | 工具栏“明细/聚合”；卡片显示次数/主机/时间范围；点击回明细 |
| 深链接 | 筛选/视图/选中日志写 URL；`view/histogram/mode/comments/log` 可恢复 |
| 渲染优化 | 分页 + 聚合/直方图按需加载；不引入虚拟滚动库 |

---

## 5. 验收标准

| # | 验收项 | 方法 |
| :-- | :-- | :-- |
| 1 | 注释默认隐藏，可一键显示并显示数量 | API/页面测试 |
| 2 | 严重度显示“错误 Lv.4”等语义标签 | 契约脚本 + 页面 |
| 3 | 直方图总数与筛选后日志数一致；点击可筛选 | Go 测试 + 页面 |
| 4 | 聚合视图把重复日志折叠（2 条 IF_DOWN → 1 组×2） | Go 测试 + 页面 |
| 5 | 刷新/分享 URL 保留视图/筛选/选中日志 | 手工验证 + 代码审查 |
| 6 | 10 万行摘要 < 1s、查询 < 200ms | benchmark 记录 |
| 7 | RCA 默认关闭、导入不自动派发 | 阶段测试 |
| 8 | 全量测试/构建通过 | go test + npm build/test |

---

## 6. 风险

| 风险 | 缓解 |
| :-- | :-- |
| 直方图/聚合全量读取内存 | 仅取必要列；限制 buckets/limit；后续可下推 SQL |
| 深链接与 localStorage 冲突 | URL 优先于持久化，仅在携带参数时覆盖 |
| 聚合口径被误解为“去重丢失日志” | UI 明确“折叠显示”，点击回明细，统计仍按原始行 |
| 性能数据受机器影响 | 记录硬件与单次基准，不作为绝对 SLA |
