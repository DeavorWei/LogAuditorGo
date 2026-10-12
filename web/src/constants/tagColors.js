/**
 * 标签系统预设颜色板
 */
export const PREDEFINE_TAG_COLORS = [
  '#F56C6C', // 红色 (高危/阻断)
  '#E6A23C', // 橙色 (告警/异常)
  '#67C23A', // 绿色 (正常/恢复)
  '#409EFF', // 蓝色 (信息/常规)
  '#909399', // 灰色 (次要/已忽略)
  '#795548', // 棕色 (基础设施)
  '#9C27B0'  // 紫色 (协议/特殊)
]

export const DEFAULT_TAG_COLOR = '#409EFF'

/**
 * 设备拓扑与多设备时间线分类预设色板（12 色分类色板，用于设备区分）
 */
export const DEVICE_COLOR_PRESETS = [
  '#3B82F6', '#10B981', '#F59E0B', '#EF4444',
  '#8B5CF6', '#EC4899', '#14B8A6', '#F97316',
  '#6366F1', '#84CC16', '#06B6D4', '#64748B'
]

export const DEFAULT_DEVICE_COLOR = '#3B82F6'

export default PREDEFINE_TAG_COLORS
