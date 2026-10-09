import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'logauditorgo:workbench-filters'

/**
 * 工作台筛选条件的持久化 store (WEB-05 / UX 专项)。
 *
 * 背景：筛选条件原本是组件内的局部 ref，切换视图即丢失，
 * 运维同事每次回到工作台都要重新勾选设备与级别。
 * 这里统一持久化到 localStorage，刷新页面也能保持。
 */
export const useFilterStore = defineStore('filter', () => {
  const defaults = () => ({
    // 分页状态：随筛选条件一起集中管理，但不参与持久化（见下方 watch）
    page: 1,
    pageSize: 50,
    keyword: '',
    severity: null,
    matched: null,
    deviceId: null,
    module: '',
    timeStart: null,
    timeEnd: null,
    viewMode: 'workbench',
    // UI-13: RCA 级别筛选，补齐后左栏与右栏联动
    rcaLevel: '',
    // 排序维度与方向：默认按真实日志发生时间正序 (time_asc)
    sortBy: 'time',
    order: 'asc'
  })

  const sanitizeSort = (loaded) => {
    const valid = (loaded.sortBy === 'time' && (loaded.order === 'asc' || loaded.order === 'desc')) ||
                  (loaded.sortBy === 'id' && loaded.order === 'asc')
    if (!valid) {
      loaded.sortBy = 'time'
      loaded.order = 'asc'
    }
    return loaded
  }

  const load = () => {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return defaults()
      const parsed = JSON.parse(raw)
      delete parsed.sourceFile
      return sanitizeSort({ ...defaults(), ...parsed })
    } catch (e) {
      return defaults()
    }
  }

  const filters = ref(load())

  // 变更即落盘（浅序列化，全部字段都是可 JSON 化的标量）。
  // page / pageSize 属于"本次会话的浏览位置"，刷新后应回到第一页，故不持久化。
  watch(
    filters,
    (val) => {
      try {
        const { page, pageSize, ...persisted } = val
        localStorage.setItem(STORAGE_KEY, JSON.stringify(persisted))
      } catch (e) {
        // 隐私模式下 localStorage 可能不可写，忽略即可，不影响功能
      }
    },
    { deep: true }
  )

  const resetFilters = () => {
    filters.value = defaults()
  }

  /** 仅重置分页位置，保留用户已配置的筛选条件 */
  const resetPagination = () => {
    filters.value.page = 1
  }

  /**
   * 把筛选条件转换为后端查询参数。
   * 空值一律不下发，避免把 "" 当成有效过滤条件。
   */
  const toLogQueryParams = (page, pageSize) => {
    const f = filters.value
    const params = { page, page_size: pageSize }
    if (f.keyword) params.keyword = f.keyword
    if (f.severity !== null && f.severity !== undefined) params.severity = f.severity
    if (f.matched !== null && f.matched !== undefined) params.matched = f.matched ? 'true' : 'false'
    if (f.deviceId) params.device_id = f.deviceId
    if (f.module) params.module = f.module
    if (f.timeStart) params.time_start = f.timeStart
    if (f.timeEnd) params.time_end = f.timeEnd
    if (f.sortBy) params.sort_by = f.sortBy
    if (f.order) params.order = f.order
    return params
  }

  const activeFilterCount = () => {
    const f = filters.value
    let n = 0
    if (f.keyword) n++
    if (f.severity !== null && f.severity !== undefined) n++
    if (f.matched !== null && f.matched !== undefined) n++
    if (f.deviceId) n++
    if (f.module) n++
    if (f.timeStart || f.timeEnd) n++
    return n
  }

  return {
    filters,
    resetFilters,
    resetPagination,
    toLogQueryParams,
    activeFilterCount
  }
})

export default useFilterStore
