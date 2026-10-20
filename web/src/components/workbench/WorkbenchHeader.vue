<template>
  <div class="workbench-header">
    <div class="header-left">
      <el-select
        :model-value="taskStore.currentTaskId"
        placeholder="选择排障任务"
        class="task-select"
        filterable
        @update:model-value="onTaskChange"
      >
        <el-option
          v-for="t in taskStore.taskList"
          :key="t.task_id"
          :label="`${t.task_name} (${t.task_id.substring(0, 8)})`"
          :value="t.task_id"
        />
      </el-select>
      <el-tooltip content="新建排障任务" placement="bottom">
        <el-button size="small" circle :icon="Plus" class="new-task-btn" @click="openNewTaskDialog" />
      </el-tooltip>

      <template v-if="currentTask">
        <TaskStatusTag :status="currentTask.status" />
        <el-tooltip :content="`已导入 ${taskFiles.length} 个文件`" placement="bottom">
          <span class="task-meta clickable" @click="$emit('open-files')">
            {{ currentTask.device_count || 0 }} 台设备 · {{ currentTask.log_count || 0 }} 行
          </span>
        </el-tooltip>
        <el-tooltip content="已命中官方知识库的日志占比" placement="bottom">
          <span class="task-meta">知识覆盖 {{ matchRate }}%</span>
        </el-tooltip>
        <span v-if="timeRange" class="task-meta">{{ timeRange }}</span>
      </template>
    </div>

    <div class="header-right">
      <el-button
        type="primary"
        :icon="Upload"
        :disabled="!taskStore.currentTaskId"
        @click="$emit('open-import')"
      >
        <span class="btn-text">{{ isTaskEmpty ? '导入日志' : '补充导入' }}</span>
      </el-button>

      <el-dropdown
        trigger="click"
        :disabled="!taskStore.currentTaskId || isTaskEmpty"
        @command="handleExportCommand"
      >
        <el-button
          type="success"
          :icon="Download"
          :loading="exportingReport"
          :disabled="!taskStore.currentTaskId || isTaskEmpty"
        >
          <span class="btn-text">导出</span><el-icon class="el-icon--right"><ArrowDown /></el-icon>
        </el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="subset_html">
              <el-icon><Document /></el-icon><span>导出筛选结果 (HTML)</span>
            </el-dropdown-item>
            <el-dropdown-item command="subset_csv">
              <el-icon><DataAnalysis /></el-icon><span>导出筛选结果 (CSV)</span>
            </el-dropdown-item>
            <el-dropdown-item divided command="full_html">
              <el-icon><Files /></el-icon><span>导出全量报告 (HTML)</span>
            </el-dropdown-item>
            <el-dropdown-item command="full_csv">
              <el-icon><Tickets /></el-icon><span>导出全量明细 (CSV)</span>
            </el-dropdown-item>
            <el-dropdown-item divided command="open-report">
              <el-icon><DataAnalysis /></el-icon><span>前往分析报告</span>
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>

      <el-tooltip content="设备管理" placement="bottom">
        <el-button :icon="Monitor" :disabled="!taskStore.currentTaskId" @click="$emit('open-devices')">
          <span class="btn-text">设备</span>
          <span v-if="currentTask?.device_count" class="btn-count">{{ currentTask.device_count }}</span>
        </el-button>
      </el-tooltip>

      <el-tooltip content="专家筛选、关联事件（实验性）、原始字段与诊断信息" placement="bottom">
        <el-button :icon="Operation" :disabled="!taskStore.currentTaskId" @click="$emit('open-advanced')">
          <span class="btn-text">高级</span>
        </el-button>
      </el-tooltip>

      <el-dropdown trigger="click" :disabled="!taskStore.currentTaskId" @command="handleMoreCommand">
        <el-button :icon="MoreFilled" :disabled="!taskStore.currentTaskId">
          <span class="btn-text">更多</span><el-icon class="el-icon--right"><ArrowDown /></el-icon>
        </el-button>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="refresh">
              <el-icon><Refresh /></el-icon><span>刷新日志</span>
            </el-dropdown-item>
            <el-dropdown-item command="files">
              <el-icon><Files /></el-icon><span>已导入文件</span>
            </el-dropdown-item>
            <el-dropdown-item command="reanalyze" :disabled="isTaskEmpty">
              <el-icon><Opportunity /></el-icon><span>重新分析</span>
            </el-dropdown-item>
            <el-dropdown-item divided command="delete">
              <el-icon><Delete /></el-icon><span class="danger-text">删除任务</span>
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>

    <NewTaskDialog v-model="showNewTaskDialog" :initial-task-name="newTaskName">
      <template #footer="{ form, close }">
        <el-button @click="close">取消</el-button>
        <el-button :loading="submitting" @click="handleCreateEmptyTask(form)">仅创建任务</el-button>
        <el-button type="primary" :loading="submitting" @click="handleCreateAndOpenImport(form)">创建并导入日志</el-button>
      </template>
    </NewTaskDialog>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Monitor, Operation, Upload, Download, ArrowDown, Plus, Refresh, Document,
  DataAnalysis, Tickets, Files, Opportunity, Delete, MoreFilled
} from '@element-plus/icons-vue'
import { useTaskStore } from '@/stores/task'
import { useFilterStore } from '@/stores/filter'
import { TASK_STATUS } from '@/constants/taskStatus'
import { formatTime } from '@/utils/format'
import api from '@/api'
import NewTaskDialog from '@/components/NewTaskDialog.vue'
import TaskStatusTag from '@/components/TaskStatusTag.vue'

const emit = defineEmits([
  'task-change',
  'task-created',
  'task-deleted',
  'refresh-logs',
  'open-files',
  'reanalyze',
  'open-import',
  'open-devices',
  'open-advanced',
  'open-report'
])

const taskStore = useTaskStore()
const filterStore = useFilterStore()
const currentTask = computed(() => taskStore.currentTask)
const taskFiles = computed(() => taskStore.taskFiles)

const isTaskEmpty = computed(() => {
  if (!currentTask.value) return true
  if (currentTask.value.status === TASK_STATUS.PENDING) return true
  return !currentTask.value.log_count
})

const matchRate = computed(() => {
  const total = currentTask.value?.log_count || 0
  if (!total) return 0
  return (((currentTask.value?.matched_count || 0) / total) * 100).toFixed(1)
})

const timeRange = computed(() => {
  const task = currentTask.value
  if (!task) return ''
  const start = formatTime(task.start_time, '')
  const end = formatTime(task.finish_time, '')
  if (!start) return ''
  return end ? `${start} ~ ${end}` : `${start} 起`
})

// 导出
const exportingReport = ref(false)
const handleExportCommand = async (cmd) => {
  if (!taskStore.currentTaskId || exportingReport.value) return
  if (cmd === 'open-report') {
    emit('open-report')
    return
  }
  exportingReport.value = true
  try {
    if (cmd === 'subset_html') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'html', filterStore.toLogQueryBody())
      ElMessage.success('已导出当前筛选子集 HTML 报告')
    } else if (cmd === 'subset_csv') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'csv', filterStore.toLogQueryBody())
      ElMessage.success('已导出当前筛选子集 CSV 明细')
    } else if (cmd === 'full_html') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'html')
      ElMessage.success('已导出全量任务 HTML 报告')
    } else if (cmd === 'full_csv') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'csv')
      ElMessage.success('已导出全量任务 CSV 明细')
    }
  } catch (e) {
    console.error('Export report failed:', e)
  } finally {
    exportingReport.value = false
  }
}

const handleMoreCommand = async (cmd) => {
  if (cmd === 'refresh') {
    emit('refresh-logs')
  } else if (cmd === 'files') {
    emit('open-files')
  } else if (cmd === 'reanalyze') {
    emit('reanalyze')
  } else if (cmd === 'delete') {
    await handleDeleteTask()
  }
}

const handleDeleteTask = async () => {
  const task = currentTask.value
  if (!task) return
  try {
    await ElMessageBox.confirm(
      `删除任务「${task.task_name}」会同时删除其全部日志、标签与任务数据库文件，且无法恢复。是否继续？`,
      '确认删除任务',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch (e) {
    return
  }
  await api.deleteTask(task.task_id)
  ElMessage.success('任务已删除')
  emit('task-deleted', task.task_id)
}

// 新建任务
const showNewTaskDialog = ref(false)
const submitting = ref(false)
const newTaskName = ref('')

const openNewTaskDialog = () => {
  const nowStr = new Date().toISOString().replace(/[-:T.]/g, '').substring(0, 14)
  newTaskName.value = `排障-${nowStr}`
  showNewTaskDialog.value = true
}

const createTask = async (form) => {
  const res = await api.createTask({ task_name: form?.taskName || newTaskName.value })
  return res?.data?.task_id
}

const handleCreateEmptyTask = async (form) => {
  submitting.value = true
  try {
    const taskId = await createTask(form)
    if (taskId) {
      ElMessage.success('任务创建成功，可随时导入日志')
      showNewTaskDialog.value = false
      await taskStore.fetchTasks()
      emit('task-created', { taskId, needImport: false })
    }
  } finally {
    submitting.value = false
  }
}

const handleCreateAndOpenImport = async (form) => {
  submitting.value = true
  try {
    const taskId = await createTask(form)
    if (taskId) {
      showNewTaskDialog.value = false
      await taskStore.fetchTasks()
      emit('task-created', { taskId, needImport: true })
    }
  } finally {
    submitting.value = false
  }
}

const onTaskChange = (taskId) => {
  emit('task-change', taskId)
}

defineExpose({
  openNewTaskDialog,
  showNewTaskDialog
})
</script>

<style scoped>
.workbench-header {
  min-height: 52px;
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border-base);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 16px;
  flex-wrap: wrap;
  flex-shrink: 0;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.task-select {
  width: 260px;
}

.new-task-btn {
  flex-shrink: 0;
}

.task-meta {
  font-size: var(--fs-sm);
  color: var(--gray-600);
  background: var(--border-base);
  padding: 3px 8px;
  border-radius: var(--radius-sm);
  white-space: nowrap;
}

.task-meta.clickable {
  cursor: pointer;
}

.task-meta.clickable:hover {
  color: var(--brand-600);
}

.header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.btn-count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 4px;
  margin-left: 4px;
  font-size: var(--fs-xs);
  border-radius: var(--radius-lg);
  background: var(--brand-500);
  color: var(--gray-0);
}

.danger-text {
  color: var(--danger-strong);
}

@media (max-width: 1400px) {
  .header-action-group .btn-text,
  .header-right .btn-text {
    display: none;
  }
}
</style>
