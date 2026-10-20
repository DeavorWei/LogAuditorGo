# Phase 1 实施方案：主链路打通

> 依据：《产品功能与体验优化分析及演进规划》v1.1 §8.2（Phase 1，第 3~6 周）与 §6.2 F1~F5、F14
> 状态：规划稿（实施前）
> 目标版本：v1.2.0

---

## 1. 阶段定位

Phase 0 把主界面止血、让 RCA 退出主航道；Phase 1 要把**“从本机日志到一条可交接的结论”**这条主链路接通：

1. 用 **Windows 原生文件/文件夹对话框**取代手填绝对路径；
2. 用 **统一导入向导 + 导入预览** 回答“我选了什么、会得到什么”；
3. 导入完成后先看 **诊断摘要**，而不是直接面对几百条日志；
4. 日志详情可看 **匹配质量与匹配依据**；
5. 任务升级为 **案例**（结论/状态/备注/工单号）；
6. 报告升级为 **执行摘要 + 关键证据 + 待补充 + 处置建议 + 局限**，并支持复制摘要与证据包。

成功标志（§8.2）：**新用户 10 分钟内看到第一条有效知识；导入完成即看到摘要。**

---

## 2. 阶段范围

| 纳入 | 不纳入（后续阶段） |
| :-- | :-- |
| 原生文件/文件夹对话框 + Web 路径选择器兜底 | 浏览器上传（明确不做） |
| 统一导入向导（新建/补充导入）+ 导入预览 API | 首启 4 步引导（Phase 2） |
| 诊断摘要页 + 匹配质量/未匹配 Top | 时间直方图/聚合视图（Phase 3） |
| 案例结论/状态/备注/工单号 + 结论草稿 | 跨任务全文日志索引（不做） |
| 报告结构升级、复制摘要、证据包 ZIP、友好文件名 | 脱敏（明确不做） |
| 单文件多 Hostname 逐行归属 + 重分析一致 | 存量任务“重新归属预览”向导（提供 API/脚本入口） |

---

## 3. 后端设计

### 3.1 原生对话框（`internal/nativedialog`）

- `OpenFiles(Options) ([]string, error)` / `OpenFolder(Options) (string, error)`；`Available() bool`。
- Windows：`CoInitializeEx(STA)` + `IFileOpenDialog`（`FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST | FOS_FILEMUSTEXIST | FOS_ALLOWMULTISELECT`，文件夹模式加 `FOS_PICKFOLDERS`），`GetResults → GetItemAt → GetDisplayName(SIGDN_FILESYSPATH)`；`runtime.LockOSThread`。
- 非 Windows / COM 初始化失败：返回 `ErrUnsupported`，前端回退 Web 路径选择器。
- 端点：`POST /api/v1/fs/dialog`（仅回环）body `{mode:"file"|"folder", title, exts:[]}` → `{paths:[], canceled:false}`。
- 安全：返回路径仍经过 `SecurePathGuard` 校验；不读取文件内容。

### 3.2 导入预览（`internal/task/import_preview.go`）

`POST /api/v1/tasks/:id/import/preview`（也支持 `GET` 风格参数复用现有 `logImportRequest`）：

```json
{
  "files": [{"name":"x.log","path":"D:/x.log","size":1024,"lines":120,"sampled":false}],
  "total_files": 1, "total_bytes": 1024, "estimated_logs": 120,
  "devices": [{"hostname":"CE16800-CORE","lines":120}],
  "time_range": {"start":"2026-04-15T14:00:01+08:00","end":"..."},
  "conflicts": ["[CE16800-CORE]_x.log"],
  "estimated_seconds": 2
}
```

约束：
- 单文件最多扫描 `previewMaxScanLines=100000` 行，超出标记 `sampled=true` 并按行数比例外推；
- 最多预览 `previewMaxFiles=200` 个文件，超出部分只统计大小；
- 输出设备按行数降序；冲突名单来自任务库现有 `TaskFile`；
- 预览只读不写，不创建任务/文件记录。

### 3.3 诊断摘要（`internal/task/diagnosis.go`）

`GET /api/v1/tasks/:id/summary`：

- **概览**：日志数、匹配数、匹配率、文件数、设备数、解析成功率、时间范围；
- **匹配质量**：EXACT/MNEMONIC/TEMPLATE/BLEVE/FAMILY_SUSPECT/UNMATCHED 六类计数；
- **严重度分布**：0~8 每级计数；
- **Top 模块**：按条数 Top 5，附最高严重度与样例日志 ID；
- **未匹配热点**：module+brief Top 10，附次数、首末时间、样例日志 ID；
- **下一步建议**：确定性生成 3~5 条（最高级别模块、未匹配热点、未归属设备、知识库为空）；
- `first_high_log_id`：最高严重度且已匹配的日志，供“开始逐条排查”一键定位。

**性能**：全部为任务库单表聚合 SQL（`GROUP BY`），10 万行任务目标 < 300ms；不加载明细到内存。

### 3.4 案例字段（`model.TaskInfo`）

新增列（GORM AutoMigrate，只增不删）：

| 字段 | 说明 |
| :-- | :-- |
| `case_status` | investigating / located / resolved / archived |
| `conclusion` | 结论文本（可编辑） |
| `notes` | 备注 |
| `external_ref` | 工单号/外部引用 |
| `case_tags` | 逗号分隔标签（大小写不敏感去重） |
| `last_opened_at` | 最近打开时间 |

端点：
- `PATCH /tasks/:id/case`：仅更新案例字段，白名单 DTO；
- `GET /tasks/:id/conclusion/draft`：由摘要确定性地生成 Markdown 草稿（不调用模型）；
- `GET /tasks/query` 增加 `case_status` 过滤，关键词扩展到 conclusion/notes/case_tags/external_ref。

### 3.5 单文件多设备归属（F14）

在流式管道 Committer 落库前增加 `assignDevicesInBatch`：

1. 统计本批 `hostname` 去重集合；
2. 对与本文件主 Hostname 不同的主机名，按批首一次 `getOrCreateDevice`（设备名=Hostname，型号由 `ResolveSpecificModelOrFamily` 嗅探）；
3. 仅对“设备上下文与 bundle 不同”的记录重跑 `Match`（构造 `NormalizedLog` 复用匹配引擎与模板参数捕获），更新 `device_id/knowledge_id/match_tier/match_confidence/matched_document_id/parameters_json`；
4. 主 Hostname 与空 Hostname 记录保持原结果，避免无谓开销。

重分析链路：`resolveLineContext` 已按记录 `Hostname` 决议，无需改动；补充测试覆盖“导入与重分析结果一致”。

### 3.6 报告与证据包（F5）

- 模板 `report.html.tmpl` 增加：执行摘要（结论 + 关键发现）、关键证据（Top 事件 + 原始日志）、未匹配/待补充、处置建议（引用知识来源）、方法与局限；
- `GET /tasks/:id/export` 的 `Content-Disposition` 改为 `<任务名>_<YYYYMMDD>.html`（RFC 5987）；
- `GET /tasks/:id/summary/markdown`：可复制摘要；
- `GET /tasks/:id/export/package`：流式 ZIP（report.html + logs.csv + metadata.json + README.txt），不落盘、不脱敏；
- RCA 章节仅在 `features.rca=true` 时渲染，且标注“实验性/非官方”。

---

## 4. 前端设计

| 模块 | 方案 |
| :-- | :-- |
| 导入向导 `ImportWizard.vue` | 步骤：信息 → 来源（原生文件/原生文件夹/粘贴路径/粘贴文本）→ 预览 → 开始分析；新建与补充导入共用；三个入口统一指向 `/audit/new` 或工作台导入按钮 |
| 原生对话框 | 向导内优先调用 `/fs/dialog`；按钮 loading 与超时提示；失败/非 Windows 自动切换 `ServerPathPicker` |
| 诊断摘要 `DiagnosisSummary.vue` | 路由 `/audit/:id/summary`；概览卡、匹配质量条、严重度分布、Top 模块、未匹配热点（跳转过滤日志）、建议、开始排查 |
| 结论抽屉 `ConclusionDrawer.vue` | Header“结论”按钮；案例状态、结论（支持生成草稿）、备注、工单号、标签；保存后 Header 展示一句话结论 |
| 案例库 | 增加案例状态列/筛选、结论摘要列、结论/备注关键词检索 |
| 报告 | 报告 Tab 增加“复制摘要”“导出证据包”；导出菜单文件名由后端决定 |
| 深链接 | 仅实现摘要→日志所需：`?module=&matched=false&log=<id>`；工作台读取后应用筛选/选中 |
| 路由 | `/audit/new`、`/audit/:id/summary`；导入完成后跳摘要 |

---

## 5. 验收标准

| # | 验收项 | 方法 |
| :-- | :-- | :-- |
| 1 | 点击“选择文件/文件夹”弹出 Windows 原生对话框；取消返回 canceled | 手工验证 + 非 Windows 单测 |
| 2 | 原生不可用自动回退 Web 路径选择器 | 前端异常分支 + 代码审查 |
| 3 | 预览显示文件、设备、时间范围、冲突与预计耗时 | API 单测 + 页面核对 |
| 4 | 导入完成 100% 进入诊断摘要；摘要与任务库一致 | 集成测试 + 页面核对 |
| 5 | 摘要首屏 < 1s（10 万行） | 预聚合 SQL + 基准测试 |
| 6 | 单文件双 Hostname 归属正确；重分析结果一致 | Go 单测（真实导入） |
| 7 | 结论可保存/刷新不丢；案例库可按状态/关键词筛选 | API 测试 + 页面核对 |
| 8 | 报告含结论/证据/未匹配/建议/局限；文件名友好；证据包可解压 | 集成测试（解压校验） |
| 9 | 复制摘要包含任务、时间、设备、关键事件、建议、局限 | API 测试 |
| 10 | 现有全量测试与前端构建通过 | `go test ./...` + `npm run build && npm run test:unit` |

---

## 6. 风险与回滚

| 风险 | 缓解 |
| :-- | :-- |
| COM 对话框线程/句柄泄漏 | `LockOSThread` + `CoUninitialize` + 所有 COM 对象 `Release`；失败立即返回并回退 |
| 预览扫描大文件阻塞 | 行数/文件数上限 + 采样标记 + 30s 上下文超时 |
| 逐行重匹配拖慢导入 | 仅对非主 Hostname 记录重跑；按批聚合设备；基准测试守护 |
| 新增案例列影响旧库 | 只增列 + AutoMigrate；旧任务默认 `case_status=""`（UI 显示“未标注”） |
| 报告模板改动导致导出回归 | 保留原字段与布局骨架；新增章节可折叠；集成测试校验关键段落 |
| 证据包内存占用 | `zip.Writer` 直接写 HTTP 响应 + CSV 流式；不组装整包到内存 |
