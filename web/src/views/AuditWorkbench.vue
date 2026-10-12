<template>
  <div class="workbench-container" :class="{ 'is-immersive': workbenchUIStore.isImmersive }">
    <!-- 1. 顶栏操作区 (任务选择、新建/分析/导出按钮) -->
    <WorkbenchHeader
      ref="headerRef"
      v-show="!workbenchUIStore.isImmersive"
      @task-change="handleTaskChange"
      @task-created="handleTaskCreated"
      @refresh-logs="fetchLogs"
      @open-files="taskFilesDialogRef?.open"
      @reanalyze="handleReanalyzeTask"
      @open-import="importDialogRef?.open"
    />

    <!-- 2. 主视图模式导航栏 (WORKBENCH / DEVICES / MULTI_TIMELINE / RCA / MULTI_REPORT) -->
    <WorkbenchNavBar
      v-if="currentTaskId && !workbenchUIStore.isImmersive"
      v-model="currentViewMode"
    />

    <!-- 3. 子视图区域 (非主工作台审计模式时按需挂载并保活) -->
    <template v-if="currentTaskId && currentViewMode !== VIEW_MODE.WORKBENCH">
      <div class="workbench-sub-view">
        <DeviceManager
          v-if="currentViewMode === VIEW_MODE.DEVICES"
          :ref="subViewRefs.deviceManagerRef"
          :task-id="currentTaskId"
          @devices-updated="handleDeviceUpdated"
        />
        <MultiDeviceTimeline
          v-else-if="currentViewMode === VIEW_MODE.MULTI_TIMELINE"
          :ref="subViewRefs.timelineRef"
          :task-id="currentTaskId"
          @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
        />
        <RcaCenter
          v-else-if="currentViewMode === VIEW_MODE.RCA"
          :ref="subViewRefs.rcaCenterRef"
          :task-id="currentTaskId"
          @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
          @task-updated="handleTaskMetadataUpdated"
        />
        <MultiDeviceReport
          v-else-if="currentViewMode === VIEW_MODE.MULTI_REPORT"
          :ref="subViewRefs.multiReportRef"
          :task-id="currentTaskId"
          @jump-to-log="(id) => handleJumpToLog(id, (m) => currentViewMode = m)"
        />
      </div>
    </template>

    <!-- 4. 核心工作台主视口 (三栏审计模式) -->
    <div v-else class="workbench-main-view">
      <!-- 4.1 空任务待导入引导卡片 -->
      <WorkbenchEmptyGuide
        v-if="isTaskEmpty"
        :task-name="currentTask?.task_name"
        @open-picker="() => importDialogRef?.openWithPicker()"
        @open-text="importDialogRef?.open('text')"
      />

      <!-- 4.2 经典三栏协同审计区 -->
      <div v-else class="workbench-body">
        <!-- 左栏：筛选面板 + 日志卡片流与 Zen 自适应网格 -->
        <WorkbenchLogStream
          ref="logStreamRef"
          :task-devices="taskDevices"
          :selected-log="selectedLog"
          :latest-log-time="latestLogTimestamp"
          :log-records="logRecords"
          :total-logs="totalLogs"
          :loading="loadingLogs"
          :has-active-filter="hasActiveFilter"
          @filter-change="onFilterChange"
          @filter-reset="handleResetFilters"
          @open-advanced-filter="advFilterDrawerRef?.open"
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
          @view-rca="currentViewMode = VIEW_MODE.RCA"
          @tag-changed="fetchLogs"
        />

        <!-- 右栏：官方知识库指南与 RCA 因果拓扑 (严格保持 v-show 保活激活 Tab) -->
        <LogKnowledgePanel
          v-show="!workbenchUIStore.isImmersive"
          :log="selectedLog"
          :matched-rca="matchedRCA"
        />
      </div>

      <!-- 5. 全屏沉浸式居中双栏深度解析弹窗 -->
      <AuditLogDetailModal
        v-model:visible="showLogDetailModal"
        :log="selectedLog"
        :task-id="currentTaskId"
        :current-index="currentLogIndex"
        :total-count="logRecords.length"
        :matched-rca="matchedRCA"
        @prev="handlePrevLog"
        @next="handleNextLog"
        @add-tag="fetchLogs"
        @remove-tag="fetchLogs"
        @delete-tag="fetchLogs"
      />

      <!-- 6. 快捷打标签弹窗 (空格键触发) -->
      <QuickTagModal
        v-model:visible="showQuickTagModal"
        :log="quickTagTargetLog"
        :task-id="currentTaskId"
        @tag-added="onQuickTagAdded"
        @tag-removed="onQuickTagRemoved"
      />
    </div>

    <!-- 7. 业务弹窗群 (已分析文件清单、导入向导、高级筛选、分析进度) -->
    <TaskFilesDialog
      ref="taskFilesDialogRef"
      :task-id="currentTaskId"
      @open-import="importDialogRef?.open"
      @file-deleted="handleFileDeleted"
    />

    <TaskImportDialog
      ref="importDialogRef"
      :task-id="currentTaskId"
      :task-name="currentTask?.task_name"
      @job-started="openProgressModalWithId"
      @import-settled="handleLogImportCompleted"
    />

    <AdvancedFilterDrawer
      ref="advFilterDrawerRef"
      :task-devices="taskDevices"
      @apply="fetchLogs"
      @reset="fetchLogs"
    />

    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      @completed="handleLogImportCompleted"
    />
  </div>
</template>

<script setup>
import { ref, provide, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'
import { VIEW_MODE } from '@/constants/viewModes'
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
import TaskFilesDialog from '@/components/workbench/TaskFilesDialog.vue'
import TaskImportDialog from '@/components/workbench/TaskImportDialog.vue'

// 通用组件
import DeviceManager from '@/components/DeviceManager.vue'
import MultiDeviceTimeline from '@/components/MultiDeviceTimeline.vue'
import RcaCenter from '@/components/RcaCenter.vue'
import MultiDeviceReport from '@/components/MultiDeviceReport.vue'
import AuditLogDetailModal from '@/components/AuditLogDetailModal.vue'
import QuickTagModal from '@/components/QuickTagModal.vue'
import AdvancedFilterDrawer from '@/components/AdvancedFilterDrawer.vue'
import ImportProgressModal from '@/components/ImportProgressModal.vue'

const route = useRoute()
const taskStore = useTaskStore()
const workbenchUIStore = useWorkbenchUIStore()

// 1. 子视图路由与实例刷新
const { currentViewMode, subViewRefs, refreshActiveSubView } = useSubViewRouter()

// 2. 实例引用与弹窗状态
const headerRef = ref(null)
const taskFilesDialogRef = ref(null)
const importDialogRef = ref(null)
const advFilterDrawerRef = ref(null)
const logStreamRef = ref(null)

const showLogDetailModal = ref(false)
const showQuickTagModal = ref(false)
const quickTagTargetLog = ref(null)
const showProgressModal = ref(false)
const currentJobId = ref('')

// 3. RCA 状态轮询与重新分析
const { checkAndPollRcaStatus, stopRcaPolling } = useRcaPolling({
  onTaskUpdated: (updated) => handleTaskMetadataUpdated(updated),
  onRcaCompleted: async (taskId) => { await taskStore.fetchRCA(taskId) }
})

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
  checkAndPollRcaStatus,
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

// 5. 富文本解析上下文提供 (WeakMap 缓存驱动)
provide(LOG_DETAIL_CONTEXT_KEY, {
  selectedLog,
  contextualizeMode,
  formattedDetail
})

// 6. 全局键盘流与按键守卫 (收敛至 hasActiveOverlay 单点防护)
useWorkbenchHotkeys({
  isWorkbenchView: () => currentViewMode.value === VIEW_MODE.WORKBENCH,
  hasActiveTask: () => Boolean(currentTaskId.value),
  hasActiveOverlay: () => Boolean(
    showLogDetailModal.value ||
    showQuickTagModal.value ||
    advFilterDrawerRef.value?.visible ||
    importDialogRef.value?.visible ||
    showProgressModal.value ||
    taskFilesDialogRef.value?.visible ||
    headerRef.value?.showNewTaskDialog
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
  onEscape: () => {
    if (showQuickTagModal.value) showQuickTagModal.value = false
    else if (showLogDetailModal.value) showLogDetailModal.value = false
    else if (workbenchUIStore.isImmersive) {
      workbenchUIStore.exitImmersive()
      logStreamRef.value?.scrollToLog(selectedLog.value?.id)
    }
  }
})

// 7. 交互胶水方法
const handleTaskCreated = async ({ taskId, needImport }) => {
  await handleTaskChange(taskId)
  if (needImport) {
    importDialogRef.value?.open('paths')
  }
}

const openProgressModalWithId = (jobId) => {
  currentJobId.value = jobId
  showProgressModal.value = true
}

const handleReanalyzeTask = () => {
  if (!currentTaskId.value || !currentTask.value) return
  reanalyze(currentTask.value)
}

// 8. 视图模式与路由联动
watch(currentViewMode, async (newMode, oldMode) => {
  if (!currentTaskId.value) return
  if (newMode === VIEW_MODE.RCA) {
    stopRcaPolling()
  } else if (oldMode === VIEW_MODE.RCA) {
    checkAndPollRcaStatus(currentTask.value, currentTaskId.value)
  }
  if (newMode === VIEW_MODE.DEVICES) {
    await taskStore.fetchTaskDevices(currentTaskId.value)
    subViewRefs.deviceManagerRef.value?.fetchDevices?.()
  } else if (newMode === VIEW_MODE.WORKBENCH) {
    await fetchLogs()
    await taskStore.fetchTaskFiles(currentTaskId.value)
  }
})

watch(() => route.params.id, async (newId, oldId) => {
  if (newId === oldId) return
  if (newId) await handleTaskChange(newId)
})

onMounted(async () => {
  await fetchTasks()
  if (currentTaskId.value) await handleTaskChange(currentTaskId.value)
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
