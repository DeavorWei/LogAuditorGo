/**
 * 案例状态（v1.2）：任务从"容器"升级为"案例"后的状态文案与颜色。
 *
 * 与后端 model.CaseStatus* 常量一一对应；空值表示旧任务未标注。
 */

export const CASE_STATUS = Object.freeze({
  INVESTIGATING: 'investigating',
  LOCATED: 'located',
  RESOLVED: 'resolved',
  ARCHIVED: 'archived'
})

const CASE_STATUS_META = Object.freeze({
  [CASE_STATUS.INVESTIGATING]: { label: '排查中', tagType: 'warning' },
  [CASE_STATUS.LOCATED]: { label: '已定位', tagType: 'primary' },
  [CASE_STATUS.RESOLVED]: { label: '已解决', tagType: 'success' },
  [CASE_STATUS.ARCHIVED]: { label: '已归档', tagType: 'info' }
})

export const CASE_STATUS_OPTIONS = Object.freeze([
  { value: '', label: '未标注' },
  { value: CASE_STATUS.INVESTIGATING, label: '排查中' },
  { value: CASE_STATUS.LOCATED, label: '已定位' },
  { value: CASE_STATUS.RESOLVED, label: '已解决' },
  { value: CASE_STATUS.ARCHIVED, label: '已归档' }
])

export function caseStatusMeta(status) {
  return CASE_STATUS_META[status] || { label: '未标注', tagType: 'info' }
}
