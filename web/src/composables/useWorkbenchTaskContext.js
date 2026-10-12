import { ref, computed, watch, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { useTaskStore } from '@/stores/task'
import { useFilterStore } from '@/stores/filter'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'
import { useTagStore } from '@/stores/tag'
import { VIEW_MODE } from '@/constants/viewModes'
import { formatLogDetail } from '@/utils/logFormatter'

/**
 * 工作台任务上下文管理与日志流编排 Composable
 */
export function useWorkbenchTaskContext(options = {}) {
  const {
    route,
    logStreamRef,
    subViewRefs,
    refreshActiveSubView = async () => {},
    checkAndPollRcaStatus = () => {},
    stopRcaPolling = () => {},
    onLogClickImmersive = () => {}
  } = options

  const taskStore = useTaskStore()
  const filterStore = useFilterStore()
  const workbenchUIStore = useWorkbenchUIStore()
  const tagStore = useTagStore()

  // 1. 任务与设备基础状态
  const currentTaskId = computed(() => taskStore.currentTaskId)
  const currentTask = computed(() => taskStore.currentTask)
  const taskList = computed(() => taskStore.taskList)
  const taskFiles = computed(() => taskStore.taskFiles)
  const taskDevices = computed(() => taskStore.taskDevices)
  const rcaEvents = computed(() => taskStore.rcaEvents)
  const logRecords = computed(() => taskStore.logRecords)
  const totalLogs = computed(() => taskStore.logTotal)

  // 2. 日志选择与富文本状态
  const loadingLogs = ref(false)
  const selectedLog = ref(null)
  const contextualizeMode = ref(true)

  const formattedDetail = computed(() =>
    formatLogDetail(selectedLog.value, contextualizeMode.value)
  )

  const matchedRCA = computed(() => {
    const logId = selectedLog.value?.id
    return logId ? taskStore.rcaOfLog(logId) : null
  })

  const latestLogTimestamp = computed(() => {
    if (!logRecords.value?.length) return null
    let maxTs = null
    let maxTimeMs = -Infinity
    for (const item of logRecords.value) {
      const ts = item?.timestamp
      if (ts && !String(ts).startsWith('0001-01-01')) {
        const ms = new Date(ts).getTime()
        if (!isNaN(ms) && ms > maxTimeMs) {
          maxTimeMs = ms
          maxTs = ts
        }
      }
    }
    return maxTs
  })

  const isTaskEmpty = computed(() => {
    if (!currentTask.value) return false
    if (currentTask.value.status === 'PENDING') return true
    return (
      typeof currentTask.value.log_count === 'number' &&
      currentTask.value.log_count === 0 &&
      taskFiles.value.length === 0
    )
  })

  const hasActiveFilter = computed(() => {
    return (
      filterStore.activeFilterCount() > 0 ||
      filterStore.filters.sortBy !== 'time' ||
      filterStore.filters.order !== 'asc'
    )
  })

  // 3. 悬浮与索引
  const hoveredLog = ref(null)

  const currentLogIndex = computed(() => {
    if (!selectedLog.value || !logRecords.value.length) return 0
    const idx = logRecords.value.findIndex((r) => r.id === selectedLog.value.id)
    return idx >= 0 ? idx : 0
  })

  // 4. 任务与数据拉取
  const fetchTasks = async () => {
    try {
      const tasks = await taskStore.fetchTasks()
      if (!currentTaskId.value && tasks.length > 0) {
        const routeId = route?.params?.id
        const defaultTask = routeId ? tasks.find((t) => t.task_id === routeId) : tasks[0]
        if (defaultTask) {
          taskStore.currentTaskId = defaultTask.task_id
          taskStore.currentTask = defaultTask
        }
      }
    } catch (e) {
      console.error('Fetch tasks failed:', e)
    }
  }

  const handleTaskChange = async (taskId) => {
    taskStore.currentTaskId = taskId
    taskStore.currentTask = taskList.value.find((t) => t.task_id === taskId) || null
    checkAndPollRcaStatus(currentTask.value, taskId)
    filterStore.clearTaskScopedFilters()
    filterStore.filters.page = 1
    filterStore.filters.timeStart = null
    filterStore.filters.timeEnd = null
    selectedLog.value = null

    await Promise.all([
      taskStore.fetchTaskFiles(taskId),
      taskStore.fetchTaskDevices(taskId),
      taskStore.fetchRCA(taskId),
      tagStore.fetchTags(taskId)
    ])
    await fetchLogs()
  }

  const fetchLogs = async () => {
    if (!currentTaskId.value) return
    loadingLogs.value = true
    try {
      const body = filterStore.toLogQueryBody(
        filterStore.filters.page,
        filterStore.filters.pageSize
      )
      const res = await api.queryTaskLogsUnified(currentTaskId.value, body)
      if (res.code === 0) {
        taskStore.setLogs(res.data?.records || [], res.data?.total || 0)

        if (logRecords.value.length > 0) {
          const found =
            selectedLog.value && logRecords.value.some((r) => r.id === selectedLog.value.id)
          if (!found) {
            selectedLog.value = logRecords.value[0]
          } else {
            const updated = logRecords.value.find((r) => r.id === selectedLog.value.id)
            if (updated) selectedLog.value = updated
          }
        } else {
          selectedLog.value = null
        }
      }
    } catch (e) {
      console.error('Fetch logs failed:', e)
    } finally {
      loadingLogs.value = false
    }
  }

  const onFilterChange = async () => {
    filterStore.filters.page = 1
    await fetchLogs()
  }

  const handleResetFilters = async () => {
    filterStore.resetFilters()
    await fetchLogs()
  }

  // 5. 日志卡片交互
  const handleLogCardClick = (rec) => {
    selectedLog.value = rec
    if (workbenchUIStore.isImmersive) {
      onLogClickImmersive()
    }
  }

  const handleLogHoverEnter = (rec) => {
    hoveredLog.value = rec
  }

  const handleLogHoverLeave = (rec) => {
    if (hoveredLog.value?.id === rec.id) {
      hoveredLog.value = null
    }
  }

  const handlePrevLog = () => {
    if (currentLogIndex.value > 0) {
      selectedLog.value = logRecords.value[currentLogIndex.value - 1]
      logStreamRef.value?.scrollToLog(selectedLog.value?.id)
    }
  }

  const handleNextLog = () => {
    if (currentLogIndex.value < logRecords.value.length - 1) {
      selectedLog.value = logRecords.value[currentLogIndex.value + 1]
      logStreamRef.value?.scrollToLog(selectedLog.value?.id)
    }
  }

  // 6. 快捷打标签
  const onQuickTagAdded = (log, tag) => {
    const recordInList = taskStore.logRecords.find((r) => r.id === log.id)
    if (recordInList) {
      if (!recordInList.tags) recordInList.tags = []
      if (!recordInList.tags.some((t) => t.id === tag.id)) recordInList.tags.push(tag)
    }
    if (selectedLog.value && selectedLog.value.id === log.id) {
      if (!selectedLog.value.tags) selectedLog.value.tags = []
      if (!selectedLog.value.tags.some((t) => t.id === tag.id)) selectedLog.value.tags.push(tag)
    }
    const finalTags =
      recordInList?.tags || (selectedLog.value?.id === log.id ? selectedLog.value.tags : [tag])
    taskStore.updateLogTags(log.id, finalTags)
  }

  const onQuickTagRemoved = (log, tagId) => {
    const recordInList = taskStore.logRecords.find((r) => r.id === log.id)
    if (recordInList && recordInList.tags) {
      recordInList.tags = recordInList.tags.filter((t) => t.id !== tagId)
    }
    if (selectedLog.value && selectedLog.value.id === log.id && selectedLog.value.tags) {
      selectedLog.value.tags = selectedLog.value.tags.filter((t) => t.id !== tagId)
    }
    const finalTags =
      recordInList?.tags || (selectedLog.value?.id === log.id ? selectedLog.value.tags : [])
    taskStore.updateLogTags(log.id, finalTags)
  }

  // 7. 任务与周边联动
  const handleFileDeleted = async ({ file, meta }) => {
    if (selectedLog.value?.source_file === file.file_name) selectedLog.value = null
    if (hoveredLog.value?.source_file === file.file_name) hoveredLog.value = null
    if (filterStore.filters.sourceFile === file.file_name) filterStore.filters.sourceFile = ''

    if (meta) handleTaskMetadataUpdated(meta)
    await taskStore.fetchTaskDevices(currentTaskId.value)
    await refreshActiveSubView()
    await tagStore.fetchTags(currentTaskId.value)
    filterStore.filters.page = 1
    await fetchLogs()
    checkAndPollRcaStatus(currentTask.value, currentTaskId.value)
    if (
      currentTask.value?.rca_status !== 'QUEUED' &&
      currentTask.value?.rca_status !== 'RUNNING'
    ) {
      await taskStore.fetchRCA(currentTaskId.value)
    }
  }

  const handleDeviceUpdated = async (devices) => {
    if (currentTask.value) currentTask.value.device_count = devices ? devices.length : 0
    taskStore.taskDevices = devices || []
    await refreshActiveSubView()
  }

  const handleJumpToLog = async (logId, setCurrentViewMode) => {
    if (!logId || !currentTaskId.value) return
    try {
      const res = await api.queryTaskLogsUnified(currentTaskId.value, {
        ids: [logId],
        page: 1,
        page_size: 1
      })
      if (res.code === 0 && res.data?.records?.length > 0) {
        selectedLog.value = res.data.records[0]
        if (typeof setCurrentViewMode === 'function') {
          setCurrentViewMode(VIEW_MODE.WORKBENCH)
        }
      }
    } catch (e) {
      console.error('Jump to log failed:', e)
    }
  }

  const handleTaskMetadataUpdated = (updatedTask) => {
    if (!updatedTask || updatedTask.task_id !== currentTaskId.value) return
    taskStore.currentTask = updatedTask
    const idx = taskList.value.findIndex((t) => t.task_id === updatedTask.task_id)
    if (idx !== -1) taskList.value[idx] = { ...taskList.value[idx], ...updatedTask }
  }

  const handleLogImportCompleted = async (result) => {
    const rcaMsg =
      result?.rca_status === 'COMPLETED' && result?.rca_count > 0
        ? `，识别出 ${result.rca_count} 个 RCA 根因事件`
        : `。根因分析 (RCA) 已在后台启动推导，可在拓扑面板查看`
    ElMessage.success({
      message: `🎉 日志分析就绪！共处理 ${result?.log_count || 0} 行日志，匹配知识 ${result?.matched_count || 0} 条${rcaMsg}`,
      duration: 5000
    })
    await fetchTasks()
    if (currentTaskId.value) {
      await handleTaskChange(currentTaskId.value)
      await refreshActiveSubView()
    }
  }

  return {
    taskStore,
    currentTaskId,
    currentTask,
    taskList,
    taskFiles,
    taskDevices,
    rcaEvents,
    logRecords,
    totalLogs,
    loadingLogs,
    selectedLog,
    contextualizeMode,
    formattedDetail,
    matchedRCA,
    latestLogTimestamp,
    isTaskEmpty,
    hasActiveFilter,
    hoveredLog,
    currentLogIndex,
    fetchTasks,
    handleTaskChange,
    fetchLogs,
    onFilterChange,
    handleResetFilters,
    handleLogCardClick,
    handleLogHoverEnter,
    handleLogHoverLeave,
    handlePrevLog,
    handleNextLog,
    onQuickTagAdded,
    onQuickTagRemoved,
    handleFileDeleted,
    handleDeviceUpdated,
    handleJumpToLog,
    handleTaskMetadataUpdated,
    handleLogImportCompleted
  }
}

export default useWorkbenchTaskContext
