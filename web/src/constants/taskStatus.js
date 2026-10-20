/**
 * 任务（案例）状态的中文语义与视觉映射。
 *
 * 背景（规划 §4.2.2 / UX-18）：v1.0 把后端枚举 COMPLETED / PROCESSING 直接透出，
 * PROCESSING 还被当成 danger 红色标签，用户既看不懂状态，也会误判"分析中=失败"。
 *
 * 约定：
 *  - 颜色语义：待导入=灰、分析中=品牌蓝（不是危险色）、已完成=绿、失败=红；
 *  - 标签文案与颜色必须解耦，色弱用户依赖文字即可判断；
 *  - 该模块保持纯 JS，便于 Node 脚本直接断言。
 */

export const TASK_STATUS = Object.freeze({
  PENDING: 'PENDING',
  PROCESSING: 'PROCESSING',
  COMPLETED: 'COMPLETED',
  FAILED: 'FAILED'
})

const STATUS_META = Object.freeze({
  [TASK_STATUS.PENDING]: {
    label: '待导入',
    tagType: 'info',
    description: '任务已创建，尚未导入日志'
  },
  [TASK_STATUS.PROCESSING]: {
    label: '分析中',
    tagType: 'primary',
    busy: true,
    description: '正在解析日志并匹配知识库'
  },
  [TASK_STATUS.COMPLETED]: {
    label: '已完成',
    tagType: 'success',
    description: '日志解析与知识匹配已完成'
  },
  [TASK_STATUS.FAILED]: {
    label: '失败',
    tagType: 'danger',
    description: '任务执行失败，可查看失败原因后重试'
  }
})

/** 状态筛选下拉选项（保持后端枚举顺序语义：进行中在前，终态在后）。 */
export const TASK_STATUS_OPTIONS = Object.freeze([
  { value: TASK_STATUS.PENDING, label: STATUS_META[TASK_STATUS.PENDING].label },
  { value: TASK_STATUS.PROCESSING, label: STATUS_META[TASK_STATUS.PROCESSING].label },
  { value: TASK_STATUS.COMPLETED, label: STATUS_META[TASK_STATUS.COMPLETED].label },
  { value: TASK_STATUS.FAILED, label: STATUS_META[TASK_STATUS.FAILED].label }
])

/**
 * 返回状态元信息；未知状态回退为"未知"，绝不把英文枚举直接展示给用户。
 */
export function taskStatusMeta(status) {
  return STATUS_META[status] || { label: '未知', tagType: 'info', description: '' }
}

/** 是否处于终态（可导出/归档）。 */
export function isTaskTerminal(status) {
  return status === TASK_STATUS.COMPLETED || status === TASK_STATUS.FAILED
}
