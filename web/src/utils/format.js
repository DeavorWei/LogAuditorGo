/**
 * 通用格式化工具 (WEB-16)。
 *
 * 背景：`formatTime` 原先在 Tasks / Documents / Settings / AuditWorkbench /
 * MultiDeviceTimeline 五处各写一份，其中 Tasks 版缺少 Go `time.Time` 零值
 * （`0001-01-01T00:00:00Z`）保护，会直接把占位时间显示给用户；
 * `formatSize` 也在 AuditWorkbench 与 ServerPathPicker 各存在一份，且单位档位不一致。
 * 这里统一为唯一实现。
 */

// Go time.Time 零值序列化后的常见形态
const ZERO_TIME_PREFIXES = ['0001-01-01']

/**
 * 将 ISO 时间串格式化为 `YYYY-MM-DD HH:mm:ss`。
 * 零值或空值返回占位符，避免把 0001-01-01 当成真实时间展示出来。
 *
 * @param {string} ts 时间字符串
 * @param {string} placeholder 零值/空值的占位文案
 * @returns {string}
 */
export function formatTime(ts, placeholder = '-') {
  if (!ts) return placeholder
  const s = String(ts)
  if (ZERO_TIME_PREFIXES.some((p) => s.startsWith(p))) return placeholder
  return s.replace('T', ' ').substring(0, 19)
}

/**
 * 字节数转为人类可读单位（B / KB / MB / GB）。
 *
 * @param {number|string} bytes
 * @param {string} empty 空值占位；传 '' 时返回空串（用于列表里"目录不显示大小"的场景）
 * @returns {string}
 */
export function formatSize(bytes, empty = '0 B') {
  if (bytes === null || bytes === undefined || bytes === '') return empty
  const b = Number(bytes)
  if (Number.isNaN(b) || b <= 0) return empty
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(1) + ' MB'
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}

export default { formatTime, formatSize }
