/**
 * 前端 UI 契约脚本 (v1.1 Phase 0)。
 *
 * 背景：项目中暂无浏览器 E2E 基础设施（Playwright 需要联网下载浏览器二进制），
 * 因此把"四条红路线"里最容易回归的纯展示契约抽成 Node 断言：
 *  - 三 Tab 信息架构与旧视图值迁移；
 *  - 任务状态中文化（不得把英文枚举透出，分析中不得使用危险色）；
 *  - 关联事件证据强度（0/缺失必须显示"证据不足"而不是 80%）；
 *  - 影响等级筛选包含 LOW；
 *  - 匹配层级中文释义。
 *
 * 运行：npm run test:unit
 */
import assert from 'node:assert/strict'

import {
  VIEW_MODE,
  VIEW_MODE_TABS,
  normalizeViewMode,
  isValidViewMode
} from '../src/constants/viewModes.js'
import {
  TASK_STATUS,
  TASK_STATUS_OPTIONS,
  taskStatusMeta
} from '../src/constants/taskStatus.js'
import {
  formatRcaConfidence,
  impactLevelLabel,
  IMPACT_LEVEL_OPTIONS
} from '../src/utils/rca.js'
import { matchTierMeta } from '../src/constants/matchTiers.js'
import { CASE_STATUS, CASE_STATUS_OPTIONS, caseStatusMeta } from '../src/constants/caseStatus.js'

let passed = 0
const check = (name, fn) => {
  try {
    fn()
    passed++
    console.log('  ok - ' + name)
  } catch (err) {
    console.error('  FAIL - ' + name)
    throw err
  }
}

console.log('[1] 工作台三 Tab 与旧视图迁移')
check('一级 Tab 恰好 3 个', () => {
  assert.equal(VIEW_MODE_TABS.length, 3)
  assert.deepEqual(VIEW_MODE_TABS.map((t) => t.value), [VIEW_MODE.LOGS, VIEW_MODE.TIMELINE, VIEW_MODE.REPORT])
  assert.deepEqual(VIEW_MODE_TABS.map((t) => t.label), ['日志分析', '事件时间线', '分析报告'])
})
check('旧五视图迁移到三 Tab', () => {
  assert.equal(normalizeViewMode('workbench'), VIEW_MODE.LOGS)
  assert.equal(normalizeViewMode('multi-timeline'), VIEW_MODE.TIMELINE)
  assert.equal(normalizeViewMode('multi-report'), VIEW_MODE.REPORT)
  assert.equal(normalizeViewMode('devices'), VIEW_MODE.LOGS)
  assert.equal(normalizeViewMode('rca'), VIEW_MODE.LOGS)
})
check('非法/空值回退日志分析且不返回 undefined', () => {
  for (const value of [undefined, null, '', 'unknown', 42, {}]) {
    assert.equal(normalizeViewMode(value), VIEW_MODE.LOGS)
  }
  assert.equal(isValidViewMode('devices'), false)
  assert.equal(isValidViewMode(VIEW_MODE.REPORT), true)
})

console.log('[2] 任务状态中文化')
check('四种状态均为中文且不含英文枚举', () => {
  const labels = TASK_STATUS_OPTIONS.map((o) => o.label)
  assert.deepEqual(labels, ['待导入', '分析中', '已完成', '失败'])
  for (const status of Object.values(TASK_STATUS)) {
    const meta = taskStatusMeta(status)
    assert.ok(meta.label)
    assert.ok(!/^[A-Z_]+$/.test(meta.label), '状态不得直接透出英文枚举: ' + meta.label)
  }
})
check('分析中使用品牌色而不是危险色', () => {
  assert.equal(taskStatusMeta(TASK_STATUS.PROCESSING).tagType, 'primary')
  assert.equal(taskStatusMeta(TASK_STATUS.FAILED).tagType, 'danger')
  assert.equal(taskStatusMeta(TASK_STATUS.COMPLETED).tagType, 'success')
})
check('未知状态回退为中文“未知”', () => {
  assert.equal(taskStatusMeta('SOMETHING_ELSE').label, '未知')
})

console.log('[3] 关联事件证据强度与等级筛选')
check('置信度为 0/缺失时显示证据不足，不再伪造 80%', () => {
  assert.equal(formatRcaConfidence(0), '证据不足')
  assert.equal(formatRcaConfidence(undefined), '证据不足')
  assert.equal(formatRcaConfidence(null), '证据不足')
  assert.equal(formatRcaConfidence('not-a-number'), '证据不足')
})
check('正常置信度格式化并封顶 100%', () => {
  assert.equal(formatRcaConfidence(0.8), '80%')
  assert.equal(formatRcaConfidence(1), '100%')
  assert.equal(formatRcaConfidence(1.3), '100%')
})
check('影响等级筛选包含 LOW', () => {
  const values = IMPACT_LEVEL_OPTIONS.map((o) => o.value)
  assert.deepEqual(values, ['ALL', 'CRITICAL', 'HIGH', 'MEDIUM', 'LOW'])
  assert.equal(impactLevelLabel('LOW'), '低')
  assert.equal(impactLevelLabel('UNKNOWN'), '未知')
})

console.log('[4] 匹配层级中文释义')
check('匹配层级全部具备中文标签', () => {
  assert.equal(matchTierMeta('EXACT').label, '精确匹配')
  assert.equal(matchTierMeta('TEMPLATE').label, '模板匹配')
  assert.equal(matchTierMeta('FAMILY_SUSPECT').tagType, 'danger')
  assert.equal(matchTierMeta('UNMATCHED').label, '暂无官方知识')
  assert.equal(matchTierMeta(undefined).label, '未知')
})

console.log('[5] 案例状态（v1.2）')
check('案例状态全部中文化且含未标注', () => {
  assert.deepEqual(CASE_STATUS_OPTIONS.map((o) => o.label), ['未标注', '排查中', '已定位', '已解决', '已归档'])
  assert.equal(caseStatusMeta(CASE_STATUS.LOCATED).label, '已定位')
  assert.equal(caseStatusMeta('').label, '未标注')
  assert.equal(caseStatusMeta(undefined).tagType, 'info')
})

console.log(`\nUI contract check passed: ${passed} assertions`)
