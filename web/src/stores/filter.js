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
    // 排序维度与方向：默认按真实日志发生时间正序 (time_asc)
    sortBy: 'time',
    order: 'asc',
    // 高级筛选 (Phase 1)
    advanced: {
      logic: 'AND',
      conditions: []
    },
    // 标签多维筛选
    tagIds: [],
    tagLogic: 'any'
  })

  const sanitizeSort = (loaded) => {
    const valid = (loaded.sortBy === 'time' && (loaded.order === 'asc' || loaded.order === 'desc')) ||
                  (loaded.sortBy === 'id' && loaded.order === 'asc')
    if (!valid) {
      loaded.sortBy = 'time'
      loaded.order = 'asc'
    }
    if (!loaded.advanced) {
      loaded.advanced = { logic: 'AND', conditions: [] }
    }
    if (!Array.isArray(loaded.tagIds)) {
      loaded.tagIds = []
    }
    if (!loaded.tagLogic) {
      loaded.tagLogic = 'any'
    }
    return loaded
  }

  const load = () => {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return defaults()
      const parsed = JSON.parse(raw)
      delete parsed.sourceFile
      delete parsed.tagIds
      delete parsed.deviceId
      return sanitizeSort({ ...defaults(), ...parsed })
    } catch (e) {
      return defaults()
    }
  }

  const filters = ref(load())

  // 变更即落盘（浅序列化，全部字段都是可 JSON 化的标量）。
  // page / pageSize 属于"本次会话的浏览位置"，tagIds / deviceId 属于任务专属数据，
  // 均不参与跨任务全局持久化，彻底杜绝切任务时的筛选态串味 (P0-4)。
  watch(
    filters,
    (val) => {
      try {
        const { page, pageSize, tagIds, deviceId, ...persisted } = val
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

  /** 重置任务专属的筛选维度 (deviceId, tagIds 等) */
  const clearTaskScopedFilters = () => {
    filters.value.deviceId = null
    filters.value.tagIds = []
    filters.value.tagLogic = 'any'
  }

  /** 仅重置分页位置，保留用户已配置的筛选条件 */
  const resetPagination = () => {
    filters.value.page = 1
  }

  /**
   * 把筛选条件转换为后端统一请求体 (POST /tasks/:id/logs/query)
   */
  const toLogQueryBody = (page, pageSize) => {
    const f = filters.value
    const body = {
      page: page ?? f.page,
      page_size: pageSize ?? f.pageSize,
      sort_by: f.sortBy || 'time',
      order: f.order || 'asc'
    }
    if (f.keyword) body.keyword = f.keyword
    if (f.severity !== null && f.severity !== undefined) body.severity = f.severity
    if (f.matched !== null && f.matched !== undefined) body.matched = !!f.matched
    if (f.deviceId) body.device_id = f.deviceId
    if (f.module) body.module = f.module
    if (f.timeStart) body.time_start = f.timeStart
    if (f.timeEnd) body.time_end = f.timeEnd

    // 高级筛选组
    if (f.advanced && Array.isArray(f.advanced.conditions) && f.advanced.conditions.length > 0) {
      const validConditions = f.advanced.conditions.filter(c => c.field && c.op && c.value !== undefined && c.value !== '')
      if (validConditions.length > 0) {
        body.advanced = {
          logic: f.advanced.logic || 'AND',
          conditions: validConditions
        }
      }
    }

    // 标签筛选
    if (Array.isArray(f.tagIds) && f.tagIds.length > 0) {
      body.tag_ids = f.tagIds
      body.tag_logic = f.tagLogic || 'any'
    }

    return body
  }

  /**
   * 兼容旧 query params
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

  const activeAdvancedCount = () => {
    const conds = filters.value.advanced?.conditions
    if (!Array.isArray(conds)) return 0
    return conds.filter(c => c.field && c.op && c.value !== undefined && c.value !== '').length
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
    if (Array.isArray(f.tagIds) && f.tagIds.length > 0) n++
    n += activeAdvancedCount()
    return n
  }

  return {
    filters,
    resetFilters,
    clearTaskScopedFilters,
    resetPagination,
    toLogQueryBody,
    toLogQueryParams,
    activeAdvancedCount,
    activeFilterCount
  }
})

export default useFilterStore
