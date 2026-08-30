/**
 * 工作台视图模式常量 (WEB-16)。
 *
 * 背景：'workbench' / 'devices' / 'multi-timeline' / 'rca' / 'multi-report'
 * 五个魔法字符串原先硬编码在 AuditWorkbench.vue 的 10 余处，
 * 拼错一个字母就会静默渲染成空白视图，且无法被静态检查发现。
 */

export const VIEW_MODE = Object.freeze({
  WORKBENCH: 'workbench',
  DEVICES: 'devices',
  MULTI_TIMELINE: 'multi-timeline',
  RCA: 'rca',
  MULTI_REPORT: 'multi-report'
})

export const DEFAULT_VIEW_MODE = VIEW_MODE.WORKBENCH

/**
 * 判断是否为合法视图模式。
 * 用于校验来自路由参数 / localStorage 的外部输入，
 * 非法值回退到默认视图，避免出现"什么都没渲染"的空白页面。
 */
export const isValidViewMode = (mode) => Object.values(VIEW_MODE).includes(mode)
