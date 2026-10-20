# Phase 2 实施方案：知识闭环与引导

> 依据：《产品功能与体验优化分析及演进规划》v1.1 §8.2（Phase 2，第 7~10 周）与 §6.3 F10~F12
> 状态：规划稿（实施前）
> 目标版本：v1.3.0

---

## 1. 阶段定位

Phase 1 解决了“导入→摘要→结论→报告”；Phase 2 要解决两个留存问题：

1. **知识闭环**：未匹配日志能聚合、能补知识、补完能立即生效；案例能按设备/模块跨任务检索。
2. **新用户可独立完成首次任务**：首启 4 步引导 + 首页 checklist + HDX 获取指引 + “?”帮助抽屉。
3. **数据可恢复**：一键备份、恢复（下次启动生效）、诊断包。

成功标志（§8.2）：**未匹配模块可补知识；案例可跨任务检索；新用户可独立完成首次任务。**

---

## 2. 阶段范围

| 纳入 | 延后/不做 |
| :-- | :-- |
| 本地知识（LOCAL_EXACT）与官方知识绑定覆盖（LOCAL_MAPPING） | 规则平台/LLM 自动补知识（不做） |
| CSV 导入导出、启用/禁用、删除、列表检索 | 全量原始日志跨任务索引（不做） |
| 未匹配热点全局聚合 + 任务摘要索引（TaskDigest） | 实时 Syslog（不做） |
| 案例库按模块/主机名/结论/标签跨任务检索 | 多厂商（不做） |
| 首启 4 步引导（状态存后端）、首页 checklist、“?”帮助抽屉、HDX 获取卡片 | 引导内容多语言（不做） |
| 一键备份、备份列表/下载/删除、下次启动恢复、诊断包 | 云端备份（不做） |
| 知识库产品/版本/严重度分面、排序、收藏/最近浏览、关键词高亮 | 知识全文重排/向量检索（不做） |

---

## 3. 后端设计

### 3.1 本地知识与覆盖（F10）

**数据模型**

- `Knowledge.Source`（`official` / `local`，默认 official）：本地知识与官方知识同表，复用检索/富化/版本映射，避免第二套装配逻辑。
- `KnowledgeOverride`：`(Module, Brief, KnowledgeID, Enabled)`，把未匹配的 module/brief 绑定到已有官方知识。
- 唯一约束：`(source, module, brief, content_hash)` 由现有 content_hash 唯一索引保证。

**匹配优先级**（`matcher.Match`）

1. `LOCAL_MAPPING`（override，置信度 0.90）
2. `LOCAL_EXACT`（本地知识精确命中，0.95）
3. 原 Tier1 EXACT（1.0）
4. Tier2 MNEMONIC / Tier3 TEMPLATE / Tier4 BLEVE

实现：`Reload()` 同时加载 `overrides` 与本地知识标记；命中本地条目时返回对应 `model.Knowledge`，并设置 `MatchTier` 为 `LOCAL_*`。

**API**

- `GET/POST/PUT/DELETE /api/v1/knowledge/local[/:id]`：本地知识 CRUD（启用/禁用）。
- `GET/POST/DELETE /api/v1/knowledge/overrides[/:id]`：绑定覆盖 CRUD。
- `POST /api/v1/knowledge/local/import`（CSV 文本）、`GET /api/v1/knowledge/local/export`（CSV）。
- `GET /api/v1/knowledge/unmatched-hotspots?limit=20`：跨任务未匹配热点（来自 TaskDigest）。

### 3.2 任务摘要索引（TaskDigest，支撑未匹配热点与跨任务检索）

- 新表（全局库）：`task_digest(task_id PK, hostnames, top_modules, unmatched_json, log_count, matched_count, updated_at)`。
- 在导入完成 / 重分析 / 案例字段更新后异步 upsert（失败只告警）。
- `/tasks/query` 的 keyword 额外匹配 `task_digest.hostnames/top_modules`（LEFT JOIN）。
- 未匹配热点：读取最近 200 个 digest，按 module+brief 合并次数并返回关联 task_id。

### 3.3 首启引导状态

- `onboarding.json` 存数据目录：`{completed_steps:[1..4], skipped:bool, dismissed:bool, updated_at}`。
- `GET/PUT /api/v1/system/onboarding`；写失败返回 500（前端可重试）。
- 引导不依赖 localStorage，清缓存不重复触发。

### 3.4 备份 / 恢复 / 诊断（F12）

- `POST /system/backup`：在 `<data>/backups/` 生成 `backup_<ts>.zip`（knowledge.db、bleve、tasks、config、onboarding），保留最近 3 份；流式写文件。
- `GET /system/backups`、`DELETE /system/backups/:name`、`GET /system/backups/:name/download`。
- `POST /system/restore` body `{name}`：校验 zip（目录结构、无路径穿越），写入 `restore.pending` + `restore.zip`，返回“重启后生效”。
- **启动时恢复**：`main()` 在打开数据库前检查 marker → 当前数据改名 `pre_restore_<ts>` → 解压 → 失败回滚。
- `GET /system/diagnostics`：流式 ZIP（配置、最近 3 个日志、DB/索引状态、版本、运行时信息），不含密钥。

### 3.5 知识分面与收藏

- `GET /knowledge/facets`：产品/版本/严重度/类型计数（基于 SQL，受 keyword/module 约束）。
- 搜索排序：`sort=relevance|severity`（Bleve SortBy）。
- `knowledge_favorites(knowledge_id PK, created_at)`、`knowledge_recent(knowledge_id PK, viewed_at)`（全局库，各保留最近 50）。
- `POST/DELETE /knowledge/:id/favorite`、`GET /knowledge/favorites`、`GET /knowledge/recent`；`GetKnowledgeDetail` 记录最近浏览。

---

## 4. 前端设计

| 模块 | 方案 |
| :-- | :-- |
| 首启引导 | `OnboardingDialog.vue` 4 步（导入 HDX→建任务→导入日志→查看摘要），可跳过；首页 checklist 可续做；状态来自后端 |
| 帮助抽屉 | 顶栏 “?”：使用说明、HDX 获取、快捷键、版本信息 |
| HDX 获取卡片 | `HdxHelpCard.vue` 复用于文档页空态/导入弹窗/引导/帮助/知识空态 |
| 未匹配热点 | 知识中心新增“未匹配热点”Tab：跨任务聚合、跳任务日志 |
| 本地知识 | 知识中心“本地知识”Tab：列表/新建/编辑/启停/删除/CSV 导入导出；“绑定到已有知识”弹窗 |
| 补知识入口 | 日志右栏未匹配卡片 → “补充本地知识”（预填 module/brief）；绑定已有知识 |
| 案例库检索 | 关键词提示支持模块/主机名；结果展示匹配来源（可选） |
| 备份/诊断 | 设置页“数据与存储”：创建备份、备份列表/下载/删除、恢复（提示重启）、下载诊断包 |
| 知识分面 | 产品/版本/严重度筛选、排序（相关度/严重度）、关键词高亮、收藏/最近 |

---

## 5. 验收标准

| # | 验收项 | 方法 |
| :-- | :-- | :-- |
| 1 | 1 分钟内把未匹配日志绑定到官方知识，下次匹配立即生效 | API/集成测试 + 手动 |
| 2 | 本地知识 CSV 导入/导出、启停、删除 | API 测试 |
| 3 | 未匹配热点跨任务聚合，能跳转到对应任务日志 | API 测试 + 页面核对 |
| 4 | 案例库输入模块/主机名可找回任务 | API 测试（TaskDigest） |
| 5 | 首启引导自动弹出、可跳过、首页可续做；清缓存不重复 | API 测试 + 页面核对 |
| 6 | HDX 指引在 5 个位置可见 | 页面核对 |
| 7 | 备份包含 4 类数据，保留 3 份；恢复下次启动生效并可回滚 | Go 测试（小数据）+ 手动 |
| 8 | 诊断包可下载且不含密钥 | 测试解压断言 |
| 9 | 知识分面/排序/收藏/最近/高亮可用 | API + 页面核对 |
| 10 | 全量测试/构建通过 | go test + npm build/test |

---

## 6. 风险与回滚

| 风险 | 缓解 |
| :-- | :-- |
| 本地知识污染官方库 | `source` 列区分；知识中心默认显示全部但标注来源；匹配优先级显式 |
| 覆盖规则导致错配 | 覆盖只对完全相同的 module+brief 生效，置信度上限 0.9；可禁用/删除 |
| TaskDigest 与任务库不一致 | 导入/重分析/案例更新后 upsert；失败只告警，查询侧容忍缺失 |
| 恢复覆盖数据 | 恢复只在启动前执行；先自动备份当前数据；失败回滚 |
| 引导打扰老用户 | 仅在从未完成/跳过时弹出；首页可关闭 checklist |
| 备份占空间 | 保留 3 份 + 设置页显示占用 + 可删除 |
