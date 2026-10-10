import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'logauditorgo:workbench-ui'

/**
 * 工作台 UI 布局偏好持久化 store。
 * 与 filter.js 严格物理隔离，保证重置筛选条件不会连带重置全屏或折叠状态。
 */
export const useWorkbenchUIStore = defineStore('workbenchUI', () => {
  const load = () => {
    const fallback = {
      isFilterCollapsed: true, // 默认收起筛选面板，仅显示第 1 行
      isZenMode: false         // 默认关闭沉浸式全屏
    }
    try {
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
      return { ...fallback, ...stored }
    } catch (e) {
      return fallback
    }
  }

  const ui = ref(load())

  watch(
    ui,
    (v) => {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(v))
      } catch (e) {
        // 隐私模式下 localStorage 可能不可用，静默忽略
      }
    },
    { deep: true }
  )

  const toggleFilterCollapse = () => {
    ui.value.isFilterCollapsed = !ui.value.isFilterCollapsed
  }

  const toggleZenMode = () => {
    ui.value.isZenMode = !ui.value.isZenMode
  }

  const setFilterCollapsed = (val) => {
    ui.value.isFilterCollapsed = val
  }

  const setZenMode = (val) => {
    ui.value.isZenMode = val
  }

  return {
    ui,
    toggleFilterCollapse,
    toggleZenMode,
    setFilterCollapsed,
    setZenMode
  }
})
