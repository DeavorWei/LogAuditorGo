<template>
  <div class="workbench-container" :class="{ 'is-immersive': workbenchUIStore.isImmersive }">
    <!-- 1. 顶栏：任务选择 + 状态/时间/设备 + 导入/导出/设备/高级/更多 -->
    <WorkbenchHeader
      ref="headerRef"
      v-show="!workbenchUIStore.isImmersive"
      @task-change="handleTaskChangeAndRoute"
      @new-task="goToNewTask"
      @task-deleted="handleTaskDeleted"
      @refresh-logs="fetchLogs"
      @open-files="taskFilesDialogRef?.open"
      @reanalyze="handleReanalyzeTask"
      @open-import="openImportWizard"
      @open-devices="showDeviceDrawer = true"
      @open-advanced="showAdvancedDrawer = true"
      @open-report="currentViewMode = VIEW_MODE.REPORT"
      @open-conclusion="showConclusionDrawer = true"
      @open-summary="goToSummary"
    />

    <!-- 2. 一级 Tab：日志分析 / 事件时间线 / 分析报告 -->
    <WorkbenchNavBar
      v-if="currentTaskId && !workbenchUIStore.isImmersive"
      v-model="currentViewMode"
    />

    <!-- 3. 事件时间线 -->
    <div v-if="currentTaskId && currentViewMode === VIEW_MODE.TIMELINE" class="workbench-sub-view">
      <MultiDeviceTimeline
        :ref="subViewRefs.timelineRef"
        :task-id="currentTaskId"
        @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
      />
    </div>

    <!-- 4. 分析报告（原"多设备对比诊断报告"一级视图已合并至此） -->
    <div v-else-if="currentTaskId && currentViewMode === VIEW_MODE.REPORT" class="workbench-sub-view">
      <ReportCenter
        :ref="subViewRefs.reportRef"
        :task-id="currentTaskId"
        :task="currentTask"
        @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
      />
    </div>

    <!-- 5. 日志分析主视口（三栏） -->
    <div v-else class="workbench-main-view">
      <!-- 5.1 空任务待导入引导卡片 -->
      <WorkbenchEmptyGuide
        v-if="isTaskEmpty"
        :task-name="currentTask?.task_name"
        @open-picker="openImportWizard"
        @open-text="openImportWizard"
      />

      <!-- 5.2 经典三栏协同审计区 -->
      <div v-else class="workbench-body">
        <!-- 左栏：筛选面板 + 日志卡片流与 Zen 自适应网格 -->
        <WorkbenchLogStream
          ref="logStreamRef"
          :task-id="currentTaskId"
          :comment-count="commentCount"
          :task-devices="taskDevices"
          :selected-log="selectedLog"
          :latest-log-time="latestLogTimestamp"
          :log-records="logRecords"
          :total-logs="totalLogs"
          :loading="loadingLogs"
          :has-active-filter="hasActiveFilter"
          @filter-change="onFilterChange"
          @filter-reset="handleResetFilters"
          @open-advanced-filter="advFilterModalRef?.open"
          @pivot="handleLogPivot"
          @select="handleLogCardClick"
          @hover-enter="handleLogHoverEnter"
          @hover-leave="handleLogHoverLeave"
          @page-change="fetchLogs"
          @size-change="fetchLogs"
        />

        <!-- 中栏：结构化报文解析与参数卡片 (严格保持 v-show 保活滚动位置) -->
        <LogDetailPanel
          v-show="!workbenchUIStore.isImmersive"
          :log="selectedLog"
          :task-id="currentTaskId"
          :rca-events="rcaEvents"
          :matched-rca="matchedRCA"
          :rca-enabled="featureStore.rcaEnabled"
          @view-rca="openRcaFromDetail"
          @tag-changed="fetchLogs"
        />

        <!-- 右栏：官方知识 + 参数注入 + 未匹配行动建议（RCA Tab 已移除） -->
        <LogKnowledgePanel
          v-show="!workbenchUIStore.isImmersive"
          :log="selectedLog"
          :task-id="currentTaskId"
        />
      </div>

      <!-- 6. 全屏沉浸式居中双栏深度解析弹窗 -->
      <AuditLogDetailModal
        v-model:visible="showLogDetailModal"
        :log="selectedLog"
        :task-id="currentTaskId"
        :current-index="currentLogIndex"
        :total-count="logRecords.length"
        :matched-rca="matchedRCA"
        :rca-enabled="featureStore.rcaEnabled"
        @prev="handlePrevLog"
        @next="handleNextLog"
        @add-tag="fetchLogs"
        @remove-tag="fetchLogs"
        @delete-tag="fetchLogs"
      />

      <!-- 7. 快捷打标签弹窗 (空格键触发) -->
      <QuickTagModal
        v-model:visible="showQuickTagModal"
        :log="quickTagTargetLog"
        :task-id="currentTaskId"
        @tag-added="onQuickTagAdded"
        @tag-removed="onQuickTagRemoved"
      />
    </div>

    <!-- 8. 设备抽屉（原一级视图） -->
    <WorkbenchDeviceDrawer
      v-model="showDeviceDrawer"
      :task-id="currentTaskId"
      @devices-updated="handleDeviceUpdated"
    />

    <!-- 9. 高级抽屉：专家筛选 / 关联事件（实验性）/ 原始字段与诊断 -->
    <WorkbenchAdvancedDrawer
      ref="advancedDrawerRef"
      v-model="showAdvancedDrawer"
      :task-id="currentTaskId"
      :task="currentTask"
      @open-advanced-filter="advFilterModalRef?.open"
      @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
      @task-updated="handleTaskMetadataUpdated"
    />

    <!-- 10. 业务弹窗群（已分析文件清单、导入向导、高级筛选、分析进度） -->
    <TaskFilesDialog
      ref="taskFilesDialogRef"
      :task-id="currentTaskId"
      @open-import="openImportWizard"
      @file-deleted="handleFileDeleted"
    />

    <ImportWizard
      ref="importWizardRef"
      v-model="showImportWizard"
      :task-id="currentTaskId"
      :task-name="currentTask?.task_name"
      :create-mode="importCreateMode"
      @task-created="handleWizardTaskCreated"
      @job-started="handleImportJobStarted"
    />

    <ConclusionDrawer
      v-model="showConclusionDrawer"
      :task-id="currentTaskId"
      :task="currentTask"
      @saved="handleConclusionSaved"
    />

    <AdvancedFilterModal
      ref="advFilterModalRef"
      :task-devices="taskDevices"
      @apply="fetchLogs"
      @reset="fetchLogs"
    />

    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      @completed="handleProgressCompleted"
    />
  </div>
</template>

<script setup>
import { ref, provide, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useTaskStore } from '@/stores/task'
import { useFeatureStore } from '@/stores/features'
import { useFilterStore } from '@/stores/filter'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'
import { VIEW_MODE, normalizeViewMode } from '@/constants/viewModes'
import { LOG_DETAIL_CONTEXT_KEY } from '@/utils/logFormatter'
import { useSubViewRouter } from '@/composables/useSubViewRouter'
import { useWorkbenchHotkeys } from '@/composables/useWorkbenchHotkeys'
import { useRcaPolling } from '@/composables/useRcaPolling'
import { useReanalyze } from '@/composables/useReanalyze'
import { useWorkbenchTaskContext } from '@/composables/useWorkbenchTaskContext'

// 专有子组件
import WorkbenchHeader from '@/components/workbench/WorkbenchHeader.vue'
import WorkbenchNavBar from '@/components/workbench/WorkbenchNavBar.vue'
import WorkbenchEmptyGuide from '@/components/workbench/WorkbenchEmptyGuide.vue'
import WorkbenchLogStream from '@/components/workbench/WorkbenchLogStream.vue'
import LogDetailPanel from '@/components/workbench/LogDetailPanel.vue'
import LogKnowledgePanel from '@/components/workbench/LogKnowledgePanel.vue'
import WorkbenchDeviceDrawer from '@/components/workbench/WorkbenchDeviceDrawer.vue'
import WorkbenchAdvancedDrawer from '@/components/workbench/WorkbenchAdvancedDrawer.vue'
import ReportCenter from '@/components/workbench/ReportCenter.vue'
import TaskFilesDialog from '@/components/workbench/TaskFilesDialog.vue'
import ImportWizard from '@/components/workbench/ImportWizard.vue'
import ConclusionDrawer from '@/components/workbench/ConclusionDrawer.vue'

// 通用组件
import MultiDeviceTimeline from '@/components/MultiDeviceTimeline.vue'
import AuditLogDetailModal from '@/components/AuditLogDetailModal.vue'
import QuickTagModal from '@/components/QuickTagModal.vue'
import AdvancedFilterModal from '@/components/AdvancedFilterModal.vue'
import ImportProgressModal from '@/components/ImportProgressModal.vue'

const route = useRoute()
const router = useRouter()
const taskStore = useTaskStore()
const featureStore = useFeatureStore()
const filterStore = useFilterStore()
const workbenchUIStore = useWorkbenchUIStore()

// 1. 子视图路由与实例刷新
const { currentViewMode, subViewRefs, refreshActiveSubView } = useSubViewRouter()

// 2. 实例引用与弹窗状态
const headerRef = ref(null)
const taskFilesDialogRef = ref(null)
const importWizardRef = ref(null)
const advFilterModalRef = ref(null)
const advancedDrawerRef = ref(null)
const logStreamRef = ref(null)

const showLogDetailModal = ref(false)
const showQuickTagModal = ref(false)
const quickTagTargetLog = ref(null)
const showProgressModal = ref(false)
const currentJobId = ref('')
const showDeviceDrawer = ref(false)
const showAdvancedDrawer = ref(false)
const showImportWizard = ref(false)
const showConclusionDrawer = ref(false)
// 导入完成后是否自动跳转诊断摘要（重新分析不跳转）
const goSummaryAfterImport = ref(false)
const importCreateMode = ref(false)

// 3. RCA 状态轮询（仅在实验室开关开启时生效）与重新分析
const { checkAndPollRcaStatus, stopRcaPolling } = useRcaPolling({
  onTaskUpdated: (updated) => handleTaskMetadataUpdated(updated),
  onRcaCompleted: async (taskId) => {
    if (featureStore.rcaEnabled) await taskStore.fetchRCA(taskId)
  }
})

const gatedCheckAndPollRcaStatus = (task, taskId) => {
  if (featureStore.rcaEnabled) checkAndPollRcaStatus(task, taskId)
}

const { reanalyze } = useReanalyze({
  onJobStarted: (jobId) => {
    currentJobId.value = jobId
    showProgressModal.value = true
  },
  onSettled: async (data) => {
    await handleLogImportCompleted(data)
  }
})

// 4. 任务与日志流生命周期编排 (收敛至 Composable)
const taskContext = useWorkbenchTaskContext({
  route,
  logStreamRef,
  subViewRefs,
  refreshActiveSubView,
  checkAndPollRcaStatus: gatedCheckAndPollRcaStatus,
  stopRcaPolling,
  onLogClickImmersive: () => { showLogDetailModal.value = true }
})

const {
  currentTaskId,
  currentTask,
  taskFiles,
  taskDevices,
  rcaEvents,
  logRecords,
  totalLogs,
  commentCount,
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
} = taskContext

const handleLogPivot = ({ type, record }) => {
  if (!record) return
  switch (type) {
    case 'filter-in-module':
      advFilterModalRef.value?.openWithCondition({ field: 'module', op: 'eq', value: record.module }, true)
      ElMessage.success(`已设置模块筛选: ${record.module}`)
      break
    case 'filter-out-module':
      advFilterModalRef.value?.openWithCondition({ field: 'module', op: 'not_in', value: record.module }, true)
      ElMessage.success(`已排除模块: ${record.module}`)
      break
    case 'filter-in-host':
      advFilterModalRef.value?.openWithCondition({ field: 'hostname', op: 'eq', value: record.hostname }, true)
      ElMessage.success(`已筛选主机: ${record.hostname}`)
      break
    case 'pivot-time':
      advFilterModalRef.value?.openWithTimePivot(record.timestamp, 5, true)
      ElMessage.success('已锁定前后 5 分钟时间窗口')
      break
  }
}

// 5. 富文本解析上下文提供 (WeakMap 缓存驱动)
provide(LOG_DETAIL_CONTEXT_KEY, {
  selectedLog,
  contextualizeMode,
  formattedDetail
})

// 6. 全局键盘流与按键守卫 (收敛至 hasActiveOverlay 单点防护)
useWorkbenchHotkeys({
  isWorkbenchView: () => currentViewMode.value === VIEW_MODE.LOGS,
  hasActiveTask: () => Boolean(currentTaskId.value),
  hasActiveOverlay: () => Boolean(
    showLogDetailModal.value ||
    showQuickTagModal.value ||
    advFilterModalRef.value?.visible ||
    showImportWizard.value ||
    showProgressModal.value ||
    taskFilesDialogRef.value?.visible ||
    showDeviceDrawer.value ||
    showAdvancedDrawer.value ||
    showImportWizard.value ||
    showConclusionDrawer.value
  ),
  onSpace: () => {
    const target = hoveredLog.value || selectedLog.value
    if (target) {
      quickTagTargetLog.value = target
      showQuickTagModal.value = true
      return true
    }
    return false
  },
  onPrevLog: () => {
    if (currentLogIndex.value > 0) {
      handlePrevLog()
      return true
    }
    return false
  },
  onNextLog: () => {
    if (currentLogIndex.value < logRecords.value.length - 1) {
      handleNextLog()
      return true
    }
    return false
  },
  onToggleZenMode: () => {
    workbenchUIStore.toggleZenMode()
    logStreamRef.value?.scrollToLog(selectedLog.value?.id)
  },
  onToggleBrowserFullscreen: () => {
    workbenchUIStore.toggleBrowserFullscreen()
  },
  onOpenAdvancedFilter: () => {
    advFilterModalRef.value?.open()
  },
  onEscape: () => {
    if (showQuickTagModal.value) showQuickTagModal.value = false
    else if (showLogDetailModal.value) showLogDetailModal.value = false
    else if (advFilterModalRef.value?.visible) {
      advFilterModalRef.value?.close?.()
    }
    else if (showAdvancedDrawer.value) showAdvancedDrawer.value = false
    else if (showDeviceDrawer.value) showDeviceDrawer.value = false
    else if (workbenchUIStore.isImmersive) {
      workbenchUIStore.exitImmersive()
      logStreamRef.value?.scrollToLog(selectedLog.value?.id)
    }
  }
})

// 7. 交互胶水方法
const openImportWizard = () => {
  importCreateMode.value = false
  showImportWizard.value = true
}

const goToNewTask = () => {
  if (route.path === '/audit/new') {
    importCreateMode.value = true
    showImportWizard.value = true
    return
  }
  router.push('/audit/new')
}

const handleWizardTaskCreated = async (taskId) => {
  importCreateMode.value = false
  await fetchTasks()
  await handleTaskChange(taskId)
  router.replace({ path: `/audit/${taskId}` })
}

const handleImportJobStarted = async ({ jobId, taskId }) => {
  if (taskId && taskId !== currentTaskId.value) {
    await fetchTasks()
    await handleTaskChange(taskId)
    router.replace({ path: `/audit/${taskId}` })
  }
  goSummaryAfterImport.value = Boolean(jobId)
  if (jobId) {
    openProgressModalWithId(jobId)
  } else {
    goToSummary()
  }
}

const handleProgressCompleted = async (result) => {
  await handleLogImportCompleted(result)
  if (goSummaryAfterImport.value) {
    goSummaryAfterImport.value = false
    goToSummary()
  }
}

const goToSummary = () => {
  if (!currentTaskId.value) return
  router.push({ path: `/audit/${currentTaskId.value}/summary` })
}

const handleConclusionSaved = async (updated) => {
  if (updated) handleTaskMetadataUpdated(updated)
  await fetchTasks()
  ElMessage.success('结论已保存')
}

const handleTaskDeleted = async (taskId) => {
  await fetchTasks()
  if (taskStore.currentTaskId === taskId) {
    const next = taskStore.taskList[0]
    if (next) {
      router.replace({ path: `/audit/${next.task_id}` })
      await handleTaskChange(next.task_id)
    } else {
      taskStore.currentTaskId = ''
      taskStore.currentTask = null
      router.replace({ path: '/audit' })
    }
  }
}

const openProgressModalWithId = (jobId) => {
  currentJobId.value = jobId
  showProgressModal.value = true
}

// 详情面板的"查看关联事件"入口：打开高级抽屉并展开关联事件
const openRcaFromDetail = () => {
  if (!featureStore.rcaEnabled) {
    ElMessage.info('关联事件（实验性）默认关闭，可在系统设置 → 实验室功能中开启')
    return
  }
  showAdvancedDrawer.value = true
}

// 去阻断：一键直达重新分析（确认弹窗由 useReanalyze 统一提供），不再弹型号选择框。
// 匹配上下文由各设备的 device_type / device_version 逐台决议。
const handleReanalyzeTask = () => {
  if (!currentTaskId.value || !currentTask.value) return
  reanalyze(currentTask.value)
}

// 8. 深链接（v1.4）：筛选/视图/选中日志写入 URL；URL 携带 view 时恢复
if (typeof route.query.view === 'string' && route.query.view) {
  currentViewMode.value = normalizeViewMode(route.query.view)
}

let routeSyncTimer = null
const syncQueryToRoute = () => {
  if (!currentTaskId.value) return
  const next = { ...route.query }
  const setOrDelete = (key, value) => {
    if (value === null || value === undefined || value === '') {
      delete next[key]
    } else {
      next[key] = String(value)
    }
  }
  setOrDelete('view', currentViewMode.value === VIEW_MODE.LOGS ? '' : currentViewMode.value)
  setOrDelete('keyword', filterStore.filters.keyword)
  setOrDelete('module', filterStore.filters.module)
  setOrDelete('severity', filterStore.filters.severity)
  setOrDelete('matched', filterStore.filters.matched)
  setOrDelete('device_id', filterStore.filters.deviceId)
  setOrDelete('log', selectedLog.value?.id)
  if (JSON.stringify(next) === JSON.stringify(route.query)) return
  router.replace({ path: route.path, query: next })
}

watch(
  () => filterStore.filters,
  () => {
    if (routeSyncTimer) clearTimeout(routeSyncTimer)
    routeSyncTimer = setTimeout(syncQueryToRoute, 300)
  },
  { deep: true }
)
watch(() => selectedLog.value?.id, () => {
  if (routeSyncTimer) clearTimeout(routeSyncTimer)
  routeSyncTimer = setTimeout(syncQueryToRoute, 250)
})

// 浏览器前进/后退或手改 URL 时，按新 query 重新应用筛选并查询。
watch(() => route.query, () => {
  if (!currentTaskId.value) return
  const before = JSON.stringify(filterStore.toLogQueryBody(1, 1))
  applyRouteQueryFilters()
  if (JSON.stringify(filterStore.toLogQueryBody(1, 1)) !== before) {
    fetchLogs()
  }
}, { deep: true })

onBeforeUnmount(() => {
  if (routeSyncTimer) clearTimeout(routeSyncTimer)
})

// 9. 视图模式与路由联动
watch(currentViewMode, async (newMode) => {
  if (!currentTaskId.value) return
  if (routeSyncTimer) clearTimeout(routeSyncTimer)
  routeSyncTimer = setTimeout(syncQueryToRoute, 300)
  if (newMode === VIEW_MODE.LOGS) {
    await fetchLogs()
    await taskStore.fetchTaskFiles(currentTaskId.value)
  }
})

watch(() => route.params.id, async (newId, oldId) => {
  if (newId === oldId) return
  // 切换任务时 handleTaskChange 已先更新 store，再由这里 replace URL；
  // 避免 URL 变化反向触发第二次全量加载。
  if (newId && newId !== currentTaskId.value) await handleTaskChange(newId)
})

// 筛选项/视图切换后统一写回 URL；Header 切换任务时也同步地址栏。
const handleTaskChangeAndRoute = async (taskId) => {
  await handleTaskChange(taskId)
  if (taskId && route.params.id !== taskId) {
    await router.replace({ path: `/audit/${taskId}`, query: route.query })
  }
}

// 同一组件实例内从 /audit/:id 切换到 /audit/new 时不会重新 mount，
// 这里补齐"新建向导"的打开逻辑。
watch(() => route.path, (path) => {
  if (path === '/audit/new') {
    importCreateMode.value = true
    showImportWizard.value = true
  }
})

onMounted(async () => {
  await featureStore.fetchFeatures()
  await fetchTasks()
  // 路由是任务选择的唯一真源：深链接/刷新/新标签页必须按 :id 加载，
  // 不能沿用 store 中上一次的任务。
  const routeTaskId = typeof route.params.id === 'string' ? route.params.id : ''
  if (routeTaskId) {
    const target = taskStore.taskList.find((item) => item.task_id === routeTaskId)
    if (!target) {
      ElMessage.error('任务不存在或已被删除')
      router.replace('/tasks')
      return
    }
    taskStore.currentTaskId = routeTaskId
    taskStore.currentTask = target
    await handleTaskChange(routeTaskId)
  } else if (currentTaskId.value) {
    await handleTaskChange(currentTaskId.value)
  }
  // 三个入口统一到向导：/audit/new 打开工作台即进入"新建任务"向导
  if (route.path === '/audit/new') {
    importCreateMode.value = true
    showImportWizard.value = true
  }
})
</script>

<style scoped>
.workbench-container {
  height: calc(100vh - var(--app-header-h) - var(--app-main-pad-y) * 2);
  display: flex;
  flex-direction: column;
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  overflow: hidden;
  transition: all 0.15s ease;
}

.workbench-container.is-immersive {
  height: 100vh;
  border-radius: 0;
  box-shadow: none;
  border: none;
}

.workbench-sub-view {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 12px 16px;
}

.workbench-main-view {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.workbench-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

@media (max-width: 1600px) {
  :root {
    --wb-col-left: 32%;
    --wb-col-mid: 34%;
    --wb-col-right: 34%;
  }
}

@media (max-width: 1279px) {
  .workbench-body {
    flex-direction: column;
    overflow-y: auto;
    --wb-col-left: 100%;
    --wb-col-mid: 100%;
    --wb-col-right: 100%;
  }
  .workbench-body :deep(.col-left) {
    max-height: 48vh;
    border-right: none;
    border-bottom: 1px solid var(--border-base);
  }
  .workbench-body :deep(.col-middle),
  .workbench-body :deep(.col-right) {
    min-width: 0;
    flex: 1 0 auto;
    border-right: none;
    border-bottom: 1px solid var(--border-base);
  }
}
</style>
