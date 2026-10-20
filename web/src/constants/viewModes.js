/**
 * 工作台视图模式常量 (v1.1 IA 收敛)。
 *
 * 背景：
 *  - v1.0 有 5 个一级视图（workbench / devices / multi-timeline / rca / multi-report），
 *    把用户分流得过于分散，RCA 还同时出现在顶部视图和右栏 Tab；
 *  - v1.1 收敛为 3 个 Tab：日志分析 / 事件时间线 / 分析报告；
 *  - 设备管理改为 Header"设备"按钮打开的抽屉，不再占用视图；
 *  - RCA 收进"高级"抽屉，仅在设置页实验室功能开启后可见。
 *
 * 旧值迁移：localStorage 里可能残留 v1.0 的视图值，normalizeViewMode 负责
 * 映射到新枚举，避免用户升级后回到工作台看到空白视图。
 */

export const VIEW_MODE = Object.freeze({
  LOGS: 'logs',
  TIMELINE: 'timeline',
  REPORT: 'report'
})

/** v1.0 五视图 → v1.1 三 Tab 的迁移映射。 */
export const LEGACY_VIEW_MODE_MAP = Object.freeze({
  workbench: VIEW_MODE.LOGS,
  'multi-timeline': VIEW_MODE.TIMELINE,
  'multi-report': VIEW_MODE.REPORT,
  // 设备与 RCA 不再是视图：回落到日志分析，用户可通过对应按钮打开抽屉
  devices: VIEW_MODE.LOGS,
  rca: VIEW_MODE.LOGS
})

export const DEFAULT_VIEW_MODE = VIEW_MODE.LOGS

/** 一级 Tab 定义（顺序即渲染顺序）。 */
export const VIEW_MODE_TABS = Object.freeze([
  { value: VIEW_MODE.LOGS, label: '日志分析', icon: 'Document' },
  { value: VIEW_MODE.TIMELINE, label: '事件时间线', icon: 'Histogram' },
  { value: VIEW_MODE.REPORT, label: '分析报告', icon: 'DataAnalysis' }
])

/**
 * 判断是否为当前合法视图模式。
 */
export const isValidViewMode = (mode) => Object.values(VIEW_MODE).includes(mode)

/**
 * 把外部输入（路由 / localStorage / 旧版本持久化）归一化为合法视图模式。
 * 非法值与旧值一律回退到"日志分析"，绝不返回 undefined 造成空白页。
 */
export function normalizeViewMode(mode) {
  if (isValidViewMode(mode)) return mode
  if (typeof mode === 'string' && LEGACY_VIEW_MODE_MAP[mode]) {
    return LEGACY_VIEW_MODE_MAP[mode]
  }
  return DEFAULT_VIEW_MODE
}
