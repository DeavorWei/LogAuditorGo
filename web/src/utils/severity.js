/**
 * 严重级别的语义标签（v1.4）。
 *
 * 华为日志级别 0~8：0 最严重，8 最轻。产品侧统一使用
 * 严重 / 错误 / 警告 / 提示 四档 + Lv.N，避免只显示数字导致误读。
 */
export function severityLabel(severity) {
  const value = Number(severity)
  if (!Number.isFinite(value)) return '未知'
  if (value <= 2) return `严重 Lv.${value}`
  if (value <= 4) return `错误 Lv.${value}`
  if (value <= 5) return `警告 Lv.${value}`
  if (value <= 8) return `提示 Lv.${value}`
  return `未知 Lv.${value}`
}

export function severityLevel(severity, module = '') {
  if (module === 'COMMENT') return 'comment'
  const value = Number(severity)
  if (!Number.isFinite(value)) return 'info'
  if (value <= 2) return 'crit'
  if (value <= 4) return 'err'
  if (value <= 6) return 'warn'
  return 'info'
}
