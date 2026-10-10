import { defineStore } from 'pinia'
import { ref, watch, computed } from 'vue'
import { ElMessage } from 'element-plus'

const STORAGE_KEY = 'logauditorgo:workbench-ui'

/**
 * 工作台 UI 布局偏好持久化 store。
 * 与 filter.js 严格物理隔离，保证重置筛选条件不会连带重置全屏或折叠状态。
 */
export const useWorkbenchUIStore = defineStore('workbenchUI', () => {
  const load = () => {
    const fallback = {
      isFilterCollapsed: true, // 默认收起筛选面板，仅显示第 1 行
      isZenMode: false,        // 默认关闭沉浸式全屏
      zenColumns: 3            // 沉浸/全屏模式下默认为 3 栏 (3~7)
    }
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      if (!raw) return fallback
      const stored = JSON.parse(raw)
      const cols = Number(stored?.zenColumns)
      return {
        isFilterCollapsed: typeof stored?.isFilterCollapsed === 'boolean' ? stored.isFilterCollapsed : fallback.isFilterCollapsed,
        isZenMode: typeof stored?.isZenMode === 'boolean' ? stored.isZenMode : fallback.isZenMode,
        zenColumns: Number.isInteger(cols) && cols >= 3 && cols <= 7 ? cols : fallback.zenColumns
      }
    } catch (e) {
      return fallback
    }
  }

  const ui = ref(load())

  // 浏览器物理全屏响应式状态
  const isBrowserFullscreen = ref(typeof document !== 'undefined' && !!document.fullscreenElement)

  if (typeof document !== 'undefined') {
    document.addEventListener('fullscreenchange', () => {
      isBrowserFullscreen.value = !!document.fullscreenElement
    })
  }

  // 统一的沉浸/全屏状态 (沉浸视图或物理全屏模式任一开启即生效)
  const isImmersive = computed(() => ui.value.isZenMode || isBrowserFullscreen.value)

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

  const setZenColumns = (cols) => {
    const num = Math.min(Math.max(Number(cols) || 3, 3), 7)
    ui.value.zenColumns = num
  }

  const toggleBrowserFullscreen = async () => {
    try {
      if (!document.fullscreenElement) {
        await document.documentElement.requestFullscreen()
      } else {
        await document.exitFullscreen()
      }
    } catch (e) {
      ElMessage.info('当前浏览器限制全屏操作，您可直接按键盘 F11 开启物理全屏')
    }
  }

  const exitImmersive = async () => {
    ui.value.isZenMode = false
    if (typeof document !== 'undefined' && document.fullscreenElement) {
      try {
        await document.exitFullscreen()
      } catch (e) {
        // 忽略非用户手势退出异常
      }
    }
  }

  return {
    ui,
    isBrowserFullscreen,
    isImmersive,
    toggleFilterCollapse,
    toggleZenMode,
    setFilterCollapsed,
    setZenMode,
    setZenColumns,
    toggleBrowserFullscreen,
    exitImmersive
  }
})
