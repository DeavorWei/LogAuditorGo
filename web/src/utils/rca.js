/**
 * 关联事件（实验性，原 RCA）的展示口径。
 *
 * 背景（规划 §5.4 / UX-07）：
 *  - v1.0 前端写 `ev.confidence || 0.8`，后端置信度为 0（证据不足）时被显示成 80%，
 *    把"没有证据"渲染成了"高置信"；
 *  - 影响等级筛选只提供 ALL/CRITICAL/HIGH/MEDIUM，后端会产出 LOW，
 *    导致 LOW 事件在筛选器里永远不可见。
 *
 * 本模块只做纯展示变换，保持可在 Node 下直接断言。
 */

/** 影响等级全集（含后端会产出的 LOW）。 */
export const IMPACT_LEVELS = Object.freeze(['CRITICAL', 'HIGH', 'MEDIUM', 'LOW'])

const IMPACT_LABELS = Object.freeze({
  CRITICAL: '严重',
  HIGH: '高',
  MEDIUM: '中',
  LOW: '低'
})

/** 筛选器选项（ALL + 全等级）。 */
export const IMPACT_LEVEL_OPTIONS = Object.freeze([
  { value: 'ALL', label: '全部等级' },
  ...IMPACT_LEVELS.map((level) => ({ value: level, label: IMPACT_LABELS[level] }))
])

export function impactLevelLabel(level) {
  return IMPACT_LABELS[level] || '未知'
}

/**
 * 把后端证据强度（0~1）格式化为百分比。
 *
 * - null / undefined / 非数字 / <= 0 → "证据不足"（不再伪造 80%）；
 * - 超过 1 的异常值按 100% 封顶，避免界面出现 130%。
 */
export function formatRcaConfidence(value) {
  const num = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(num) || num <= 0) {
    return '证据不足'
  }
  const clamped = Math.min(num, 1)
  return `${Math.round(clamped * 100)}%`
}
