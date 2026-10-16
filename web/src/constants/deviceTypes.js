/**
 * 设备型号常量 (WEB-16 / 设备型号解耦)。
 *
 * 语义变更（重要）：
 *   自"设备型号解耦"改造后，日志匹配的上下文**只**由设备行（devices.device_type /
 *   devices.device_version）决定，不再使用任务级 device_type。
 *   因此本模块的两套口径收敛为：
 *     1. TASK_DEVICE_TYPE_OPTIONS —— 任务级展示元数据（保留仅为兼容旧 API 与旧任务展示，不参与匹配）；
 *     2. DEVICE_PRODUCT_OPTIONS   —— 设备行型号（产品族或具体型号），**决定匹配哪一族官方文档**。
 *
 * 判定依据仍是后端 matcher/scoring.go 的产品族规则：
 *   CLOUDENGINE / HISECENGINE / CAMPUS / NETENGINE / WLAN
 */

/** 任务级设备类型（遗留展示元数据，不参与匹配） */
export const TASK_DEVICE_TYPE = Object.freeze({
  CLOUD_ENGINE: 'CloudEngine',
  HISEC_ENGINE_USG: 'HiSecEngine-USG',
  CAMPUS_SWITCH: 'Campus-Switch',
  NET_ENGINE: 'NetEngine',
  WLAN_AC: 'WLAN',
  HUAWEI_VRP: 'Huawei-VRP'
})

// 中性兜底：不再默认 CloudEngine，避免多设备混合故障域下的系统性产品偏置
export const DEFAULT_TASK_DEVICE_TYPE = TASK_DEVICE_TYPE.HUAWEI_VRP
export const DEFAULT_DEVICE_TYPE = DEFAULT_TASK_DEVICE_TYPE

/**
 * 任务级设备类型下拉选项（遗留，仅展示）。
 * 以 Dashboard 的 5 项为全集：AuditWorkbench 原先只有 3 项，
 * 属于历史遗漏，会导致园区交换机与核心路由器的任务无法选中正确类型。
 */
export const TASK_DEVICE_TYPE_OPTIONS = Object.freeze([
  { label: 'CloudEngine 数据中心交换机', value: TASK_DEVICE_TYPE.CLOUD_ENGINE },
  { label: 'WLAN / AirEngine 无线控制器 (AC/AP)', value: TASK_DEVICE_TYPE.WLAN_AC },
  { label: 'HiSecEngine 防火墙 (USG)', value: TASK_DEVICE_TYPE.HISEC_ENGINE_USG },
  { label: 'Campus 园区交换机 (S系列)', value: TASK_DEVICE_TYPE.CAMPUS_SWITCH },
  { label: 'NetEngine 核心路由器 (NE系列)', value: TASK_DEVICE_TYPE.NET_ENGINE },
  { label: '通用华为 VRP 设备', value: TASK_DEVICE_TYPE.HUAWEI_VRP }
])
export const DEVICE_TYPE_OPTIONS = TASK_DEVICE_TYPE_OPTIONS

/**
 * 设备行型号选项（产品族或具体型号）。
 *
 * 取代原 DEVICE_FORM_TYPE_OPTIONS：device_type 不再是 Router/Switch/Firewall
 * 形态分类，而是直接决定该设备日志匹配哪一族官方文档，且可通过 allow-create
 * 直接填写具体型号（如 AC6508 / CE16800 / S12700），命中 120/150 版本分档。
 */
export const DEVICE_PRODUCT_OPTIONS = Object.freeze([
  { label: 'CloudEngine 数据中心交换机', value: TASK_DEVICE_TYPE.CLOUD_ENGINE },
  { label: 'WLAN / AirEngine 无线控制器 (AC/AP)', value: TASK_DEVICE_TYPE.WLAN_AC },
  { label: 'HiSecEngine 防火墙 (USG)', value: TASK_DEVICE_TYPE.HISEC_ENGINE_USG },
  { label: 'Campus 园区交换机 (S系列)', value: TASK_DEVICE_TYPE.CAMPUS_SWITCH },
  { label: 'NetEngine 核心路由器 (NE系列)', value: TASK_DEVICE_TYPE.NET_ENGINE },
  { label: '通用华为 VRP 设备', value: TASK_DEVICE_TYPE.HUAWEI_VRP }
])

/** 新建设备的默认型号：中性兜底，绝不偏向任一产品族 */
export const DEFAULT_DEVICE_PRODUCT = TASK_DEVICE_TYPE.HUAWEI_VRP

/**
 * 历史遗留形态分类的中文名。
 *
 * 老版本自动创建的设备型号被写死为 Router/Switch，这些行仍存在于存量任务库中，
 * 展示时统一映射为可读中文，避免直接透出英文 value。
 */
const LEGACY_FORM_TYPE_LABELS = Object.freeze({
  Router: 'Router (路由器，历史遗留)',
  Switch: 'Switch (交换机，历史遗留)',
  Firewall: 'Firewall (防火墙，历史遗留)',
  Other: 'Other (其他网络设备，历史遗留)'
})

/**
 * 由 value 取展示名。
 *
 * 覆盖范围（与后端 matcher.ResolveSpecificModelOrFamily 的产出对齐）：
 *   1. 规范产品族串（CloudEngine / Campus-Switch / HiSecEngine-USG / NetEngine / WLAN / Huawei-VRP），
 *      嗅探产出的大小写不保证一致（如 CLOUDENGINE），故按大小写不敏感匹配；
 *   2. 历史遗留形态分类（Router / Switch / Firewall / Other）；
 *   3. 具体型号（AC6508 / CE16800 …）原样返回——原样展示的信息量最大，不会造成误解。
 */
export const deviceTypeLabel = (value) => {
  const v = (value || '').trim()
  if (!v) return '-'
  const upper = v.toUpperCase()
  const hit =
    DEVICE_PRODUCT_OPTIONS.find((o) => o.value.toUpperCase() === upper) ||
    TASK_DEVICE_TYPE_OPTIONS.find((o) => o.value.toUpperCase() === upper)
  if (hit) return hit.label
  if (LEGACY_FORM_TYPE_LABELS[v]) return LEGACY_FORM_TYPE_LABELS[v]
  return v
}
