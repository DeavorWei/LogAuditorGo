import { ref, watch } from 'vue'
import { VIEW_MODE, DEFAULT_VIEW_MODE, normalizeViewMode } from '@/constants/viewModes'
import { useFilterStore } from '@/stores/filter'

/**
 * 工作台一级 Tab 切换与实例刷新编排（v1.1 三 Tab）。
 *
 * 设备管理与关联事件不再是视图：设备走 Header 抽屉，
 * 关联事件走"高级"抽屉（实验开关开启后可见）。
 */
export function useSubViewRouter() {
  const filterStore = useFilterStore()

  // 视图模式：优先恢复上次使用的 Tab；旧版本持久化的五视图值由 normalizeViewMode 迁移
  const currentViewMode = ref(normalizeViewMode(filterStore.filters.viewMode))

  // 视图切换即落盘，下次进入工作台直接回到上次的分析视图
  watch(currentViewMode, (mode) => {
    filterStore.filters.viewMode = mode
  })

  const timelineRef = ref(null)
  const reportRef = ref(null)

  const refreshActiveSubView = async () => {
    const targetMap = {
      [VIEW_MODE.TIMELINE]: timelineRef,
      [VIEW_MODE.REPORT]: reportRef
    }
    const target = targetMap[currentViewMode.value]
    try {
      await target?.value?.refresh?.()
    } catch (e) {
      // 子组件尚未实现 refresh() 时静默降级
    }
  }

  return {
    currentViewMode,
    subViewRefs: {
      timelineRef,
      reportRef
    },
    refreshActiveSubView
  }
}

export default useSubViewRouter
