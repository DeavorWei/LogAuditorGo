import { ref, watch } from 'vue'
import { VIEW_MODE, DEFAULT_VIEW_MODE, isValidViewMode } from '@/constants/viewModes'
import { useFilterStore } from '@/stores/filter'

/**
 * 工作台子视图模式路由与实例刷新编排
 */
export function useSubViewRouter() {
  const filterStore = useFilterStore()

  // 视图模式：优先恢复上次使用的视图，并做合法性校验
  const currentViewMode = ref(
    isValidViewMode(filterStore.filters.viewMode)
      ? filterStore.filters.viewMode
      : DEFAULT_VIEW_MODE
  )

  // 视图切换即落盘，下次进入工作台直接回到上次的分析视图
  watch(currentViewMode, (mode) => {
    filterStore.filters.viewMode = mode
  })

  // 四个子视图的组件实例引用
  const deviceManagerRef = ref(null)
  const timelineRef = ref(null)
  const rcaCenterRef = ref(null)
  const multiReportRef = ref(null)

  // 主动刷新当前激活的子视图
  const refreshActiveSubView = async () => {
    const targetMap = {
      [VIEW_MODE.DEVICES]: deviceManagerRef,
      [VIEW_MODE.MULTI_TIMELINE]: timelineRef,
      [VIEW_MODE.RCA]: rcaCenterRef,
      [VIEW_MODE.MULTI_REPORT]: multiReportRef
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
      deviceManagerRef,
      timelineRef,
      rcaCenterRef,
      multiReportRef
    },
    refreshActiveSubView
  }
}

export default useSubViewRouter
