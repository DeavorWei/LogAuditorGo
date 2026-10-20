# Phase 2 方案审视：知识闭环与引导

> 审视对象：`Phase2-知识闭环与引导-规划.md`
> 审视时间：实施前
> 审视方法：对照 §8.2 与 F10~F12 逐项核对；重点审查“本地知识同表复用”与“备份/恢复”两类高风险改动

---

## 1. 总体结论

**方向成立，但两处设计必须先改：本地知识不能走现有“孤儿清理”，备份不能直接拷贝正在写入的库。** 其余按 7 项调整后实施。

---

## 2. 必须调整的事项

### 2.1 本地知识必须豁免“孤儿知识清理”

- 现有 `knowledge.DeleteDocuments` 会删除“没有被任何版本映射引用”的知识条目。本地知识如果只存在于 `knowledges` 表、没有版本映射，删除任意文档都会被误删。
- **调整**：
  1. 本地知识**始终**写入一条 `KnowledgeVersionMapping`（产品族取用户填写值，版本可空），保证不被孤儿清理命中；
  2. 孤儿清理 SQL 额外排除 `source = 'local'`（双保险）；
  3. 删除知识条目的公开 API 拒绝删除 `source='local'`，统一走本地知识端点。

### 2.2 本地知识需要独立的内存索引，而不是混入 exactMap 碰运气

- 现有 `exactMap` 是 `MODULE:BRIEF → *Knowledge`，若本地与官方同键，覆盖顺序取决于加载顺序，不可控。
- **调整**：`MatchEngine.Reload()` 额外构建：
  - `localExact map[string]*Knowledge`（source=local，module+brief）
  - `overrides map[string]uint`（module+brief → official knowledge ID）
  匹配顺序固定为 **override(0.90) → localExact(0.95) → 官方 Tier1~4**；命中本地时返回本地实体，前端按 `LOCAL_EXACT/LOCAL_MAPPING` 展示。

### 2.3 覆盖规则必须校验目标知识存在且启用

- 允许把 module/brief 绑定到已被删除/禁用的知识，会让匹配结果指向不存在条目。
- **调整**：创建覆盖时校验 `knowledge_id` 存在且 `source != 'local'`（本地知识直接精确命中，无需覆盖）；`Reload` 时跳过失效映射；删除知识时级联删除相关覆盖。

### 2.4 TaskDigest 只做“检索辅助”，不作为权威统计

- 规划要求摘要/热点/检索都依赖 digest，但 digest 更新失败会造成数据不一致。
- **调整**：
  - digest 字段全部为“辅助检索/聚合”用途，任务详情与诊断摘要仍实时查任务库；
  - 更新时机：导入完成、重分析完成、删除文件后；失败仅告警；
  - `/knowledge/unmatched-hotspots` 明确标注“基于最近 200 个案例的聚合”，并返回 `digest_updated_at`。

### 2.5 备份必须在“长任务空闲 + 任务库关闭”后执行

- 直接复制 `knowledge.db` 可能漏掉 WAL 中未 checkpoint 的数据；复制正在写入的 Bleve 目录可能得到损坏索引。
- **调整**：
  1. 备份前检查进度中心是否有运行中的导入/重分析/重建任务，有则返回 409；
  2. `knowledge.db` 用 `VACUUM INTO <temp>` 生成一致快照（失败回退到 checkpoint+copy）；
  3. `storage.CloseAllTaskDBs()` 后复制任务库目录（用户触发、短时占用；后续请求会按需重开）；
  4. Bleve 目录在关闭索引句柄后复制（`indexer.Close()` 不可行——会影响在线服务；改为文件级 best-effort，诊断包中提示“索引建议先用重建功能自愈”）；
  5. 备份写入 `backups/<name>.zip.tmp` 成功后原子改名，保留 3 份。

### 2.6 恢复必须“下次启动生效”，且失败可回滚

- 在线替换数据库/索引会导致当前进程持有旧句柄、状态不一致。
- **调整**：
  1. `POST /system/restore` 只做校验（zip 结构、条目白名单、路径穿越、单文件/总大小上限）并写 `restore.pending` + `restore.zip`；
  2. 启动时在打开任何 DB 之前应用：解压到 `restore_staging` → 当前数据改名为 `pre_restore_<ts>` → 原子替换 → 失败回滚；
  3. 恢复后写 `restore.log`（时间、来源备份、结果），设置页可查看最近一次恢复记录；
  4. 恢复前自动生成一份 `pre_restore` 备份。

### 2.7 首启引导自动弹出会干扰自动化与截图

- 无头走查/测试首次打开任意页面都会被引导遮挡。
- **调整**：
  - 引导仅在 `GET /system/onboarding` 返回“从未完成且未跳过/关闭”时弹出；
  - 自动化脚本先 `PUT /system/onboarding {dismissed:true}`；
  - “暂时跳过”记录 `skipped=true`，不再自动弹出，但首页 checklist 保留。

---

## 3. 补充约束

1. **术语**：本地知识 UI 统一标注“本地知识（非官方）”，不得与官方文档混淆。
2. **CSV 格式**：列固定为 `module,brief,product,version,severity,meaning,cause,action,reference`，导入时逐行校验，错误行汇总返回，不整批失败。
3. **收藏/最近**：最近浏览最多 50 条，收藏无上限但列表分页；详情 GET 记录最近浏览属于本地工具可接受副作用。
4. **分面口径**：产品/版本/严重度分面受当前 module/type 过滤约束；keyword 只影响结果列表（Bleve 与 SQL 混合过滤成本高），UI 需注明。
5. **诊断包**：只含配置、最近 3 个日志、版本、DB/索引状态、操作系统信息；不含任务日志内容与知识正文。
6. **帮助抽屉**：快捷键与当前工作台一致（Space/↑↓/F/Shift+F/Esc/Tab），避免文档漂移。
7. **HDX 指引**：链接 `https://support.huawei.com/enterprise`，明确“需企业账号”，不做自动下载。
8. **知识中心 UI 复杂度**：新增 Tab 采用 `el-tabs`，保持单页；本地知识与未匹配热点均复用现有卡片/表格样式。

---

## 4. 调整后的实施顺序

1. 数据与匹配：Knowledge.Source + 本地知识/覆盖表 + matcher 优先级 + 孤儿清理豁免。
2. 任务摘要索引与跨任务检索；未匹配热点聚合。
3. 引导状态 + 前端引导/checklist/帮助/HDX 卡片。
4. 备份/诊断/恢复（marker 启动应用 + 测试）。
5. 知识分面/排序/收藏/最近/高亮。
6. 测试与文档，最后统一提交。

---

## 5. 审视结论

**通过（带修改）。** 按 §2 的 7 项调整与 §3 的 8 条约束实施；本地知识与备份/恢复是本期最高风险点，必须配套单测与失败回滚路径。
