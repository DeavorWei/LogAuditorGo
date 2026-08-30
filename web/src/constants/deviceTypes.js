/**
 * 设备类型常量 (WEB-16)。
 *
 * 背景：设备类型下拉原先在 Dashboard（5 项）、AuditWorkbench（3 项）、
 * DeviceManager（7 项）各写一份，选项数量与 value 互不一致——
 * 同一个任务在不同页面被归成不同类型，直接影响版本分档匹配的准确性 (PARSE-04)。
 *
 * 统一口径的判定依据（后端 `matcher/scoring.go: scoreVersion` 实际识别的产品族）：
 *   CLOUDENGINE / HISECENGINE / USG / NETENGINE / CAMPUS
 *
 * 因此这里拆成**语义不同的两组**，不强行合并：
 *   1. TASK_DEVICE_TYPES   —— 任务级设备类型，会作为 product 传入匹配引擎影响版本分档；
 *   2. DEVICE_FORM_TYPES   —— 设备条目的形态分类，仅用于展示与分组，不参与匹配。
 */

/** 任务级设备类型（影响知识库版本分档匹配） */
export const TASK_DEVICE_TYPE = Object.freeze({
  CLOUD_ENGINE: 'CloudEngine',
  HISEC_ENGINE_USG: 'HiSecEngine-USG',
  CAMPUS_SWITCH: 'Campus-Switch',
  NET_ENGINE: 'NetEngine',
  HUAWEI_VRP: 'Huawei-VRP'
})

export const DEFAULT_TASK_DEVICE_TYPE = TASK_DEVICE_TYPE.CLOUD_ENGINE

/**
 * 任务设备类型下拉选项。
 * 以 Dashboard 的 5 项为全集：AuditWorkbench 原先只有 3 项，
 * 属于历史遗漏，会导致园区交换机与核心路由器的任务无法选中正确类型。
 */
export const TASK_DEVICE_TYPE_OPTIONS = Object.freeze([
  { label: 'CloudEngine 数据中心交换机', value: TASK_DEVICE_TYPE.CLOUD_ENGINE },
  { label: 'HiSecEngine 防火墙 (USG)', value: TASK_DEVICE_TYPE.HISEC_ENGINE_USG },
  { label: 'Campus 园区交换机 (S系列)', value: TASK_DEVICE_TYPE.CAMPUS_SWITCH },
  { label: 'NetEngine 核心路由器 (NE系列)', value: TASK_DEVICE_TYPE.NET_ENGINE },
  { label: '通用华为 VRP 设备', value: TASK_DEVICE_TYPE.HUAWEI_VRP }
])

/** 设备条目形态分类（仅展示用，不参与版本匹配） */
export const DEVICE_FORM_TYPE = Object.freeze({
  ROUTER: 'Router',
  SWITCH: 'Switch',
  FIREWALL: 'Firewall',
  CLOUD_ENGINE: 'CloudEngine',
  NET_ENGINE: 'NetEngine',
  HISEC_ENGINE_USG: 'HiSecEngine-USG',
  OTHER: 'Other'
})

export const DEFAULT_DEVICE_FORM_TYPE = DEVICE_FORM_TYPE.SWITCH

export const DEVICE_FORM_TYPE_OPTIONS = Object.freeze([
  { label: 'Router (路由器)', value: DEVICE_FORM_TYPE.ROUTER },
  { label: 'Switch (交换机)', value: DEVICE_FORM_TYPE.SWITCH },
  { label: 'Firewall (防火墙)', value: DEVICE_FORM_TYPE.FIREWALL },
  { label: 'CloudEngine (数据中心交换机)', value: DEVICE_FORM_TYPE.CLOUD_ENGINE },
  { label: 'NetEngine (核心路由器)', value: DEVICE_FORM_TYPE.NET_ENGINE },
  { label: 'USG (安全网关)', value: DEVICE_FORM_TYPE.HISEC_ENGINE_USG },
  { label: 'Other (其他网络设备)', value: DEVICE_FORM_TYPE.OTHER }
])

/** 由 value 取展示名；取不到时原样返回，避免下拉/详情显示空白 */
export const deviceTypeLabel = (value) => {
  const hit =
    TASK_DEVICE_TYPE_OPTIONS.find((o) => o.value === value) ||
    DEVICE_FORM_TYPE_OPTIONS.find((o) => o.value === value)
  return hit ? hit.label : value || '-'
}
