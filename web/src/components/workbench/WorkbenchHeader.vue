<template>
  <div class="workbench-header">
    <div class="header-left">
      <el-select
        :model-value="taskStore.currentTaskId"
        placeholder="选择审计任务"
        style="width: 280px;"
        @update:model-value="onTaskChange"
      >
        <el-option
          v-for="t in taskStore.taskList"
          :key="t.task_id"
          :label="`${t.task_name} (${t.task_id.substring(0, 8)})`"
          :value="t.task_id"
        />
      </el-select>
      <template v-if="currentTask">
        <span class="task-badge">设备: {{ currentTask.device_type }}</span>
        <el-tag v-if="currentTask.status === 'PENDING'" type="warning" size="small" effect="dark">
          待导入日志
        </el-tag>
        <span v-else class="task-badge">
          匹配率: {{ ((currentTask.matched_count / (currentTask.log_count || 1)) * 100).toFixed(1) }}%
        </span>
        <el-button
          size="small"
          type="info"
          plain
          icon="Files"
          style="margin-left: 4px;"
          @click="$emit('open-files')"
        >
          已导入 {{ taskFiles.length }} 个文件 ({{ currentTask.log_count || 0 }} 行)
        </el-button>
      </template>
    </div>

    <div class="header-right">
      <el-button-group>
        <el-button
          icon="Refresh"
          :disabled="!taskStore.currentTaskId"
          @click="$emit('refresh-logs')"
        >
          刷新日志
        </el-button>
        <el-button
          type="warning"
          icon="Opportunity"
          :disabled="!taskStore.currentTaskId || currentTask?.status === 'PENDING' || currentTask?.log_count === 0"
          @click="$emit('reanalyze')"
        >
          重新分析
        </el-button>
        <el-button
          type="primary"
          icon="Upload"
          :disabled="!taskStore.currentTaskId"
          @click="$emit('open-import')"
        >
          {{ currentTask?.status === 'PENDING' || currentTask?.log_count === 0 ? '导入日志' : '补充导入' }}
        </el-button>
        <el-dropdown
          trigger="click"
          :disabled="!taskStore.currentTaskId || currentTask?.status === 'PENDING'"
          @command="handleExportCommand"
        >
          <el-button
            type="success"
            icon="Download"
            :loading="exportingReport"
            :disabled="!taskStore.currentTaskId || currentTask?.status === 'PENDING'"
          >
            导出报告 <el-icon class="el-icon--right"><ArrowDown /></el-icon>
          </el-button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="subset_html">
                📄 导出筛选结果 (HTML)
              </el-dropdown-item>
              <el-dropdown-item command="subset_csv">
                📊 导出筛选结果 (CSV)
              </el-dropdown-item>
              <el-dropdown-item divided command="full_html">
                📦 导出全量报告 (HTML)
              </el-dropdown-item>
              <el-dropdown-item command="full_csv">
                📑 导出全量明细 (CSV)
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button type="primary" plain icon="Plus" @click="openNewTaskDialog">新建任务</el-button>
      </el-button-group>
    </div>

    <!-- 新建任务弹窗 (内聚管理) -->
    <el-dialog v-model="showNewTaskDialog" title="新建日志审计任务" width="620px">
      <el-form label-position="top">
        <el-form-item label="任务名称">
          <el-input v-model="newTaskForm.taskName" placeholder="例如: Core-SW-01排查-20260415" />
        </el-form-item>
        <el-form-item label="设备类型">
          <el-select v-model="newTaskForm.deviceType" style="width: 100%;">
            <el-option
              v-for="opt in DEVICE_TYPE_OPTIONS"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showNewTaskDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="handleCreateEmptyTask">仅创建任务 (稍后导入)</el-button>
        <el-button type="success" :loading="submitting" @click="handleCreateAndOpenImport">创建并立即导入日志</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { Files, Opportunity, Upload, Download, ArrowDown, Plus, Refresh } from '@element-plus/icons-vue'
import { useTaskStore } from '@/stores/task'
import { useFilterStore } from '@/stores/filter'
import api from '@/api'
import { TASK_DEVICE_TYPE_OPTIONS as DEVICE_TYPE_OPTIONS, DEFAULT_TASK_DEVICE_TYPE as DEFAULT_DEVICE_TYPE } from '@/constants/deviceTypes'

const emit = defineEmits([
  'task-change',
  'task-created',
  'refresh-logs',
  'open-files',
  'reanalyze',
  'open-import'
])

const taskStore = useTaskStore()
const filterStore = useFilterStore()
const currentTask = computed(() => taskStore.currentTask)
const taskFiles = computed(() => taskStore.taskFiles)

// 导出状态
const exportingReport = ref(false)

const handleExportCommand = async (cmd) => {
  if (!taskStore.currentTaskId || exportingReport.value) return
  exportingReport.value = true
  try {
    if (cmd === 'subset_html') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'html', filterStore.toLogQueryBody())
      ElMessage.success('已成功导出当前筛选子集 HTML 报告')
    } else if (cmd === 'subset_csv') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'csv', filterStore.toLogQueryBody())
      ElMessage.success('已成功导出当前筛选子集 CSV 明细')
    } else if (cmd === 'full_html') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'html')
      ElMessage.success('已成功导出全量任务 HTML 报告')
    } else if (cmd === 'full_csv') {
      await api.downloadTaskReport(taskStore.currentTaskId, 'csv')
      ElMessage.success('已成功导出全量任务 CSV 明细')
    }
  } catch (e) {
    console.error('Export report failed:', e)
  } finally {
    exportingReport.value = false
  }
}

// 新建任务弹窗与表单
const showNewTaskDialog = ref(false)
const submitting = ref(false)
const newTaskForm = ref({ taskName: '', deviceType: DEFAULT_DEVICE_TYPE })

const openNewTaskDialog = () => {
  const nowStr = new Date().toISOString().replace(/[-:T.]/g, '').substring(0, 14)
  newTaskForm.value.taskName = `Audit-${nowStr}`
  newTaskForm.value.deviceType = DEFAULT_DEVICE_TYPE
  showNewTaskDialog.value = true
}

const handleCreateEmptyTask = async () => {
  submitting.value = true
  try {
    const res = await api.createTask({
      task_name: newTaskForm.value.taskName,
      device_type: newTaskForm.value.deviceType
    })
    if (res.code === 0) {
      ElMessage.success('空任务创建成功，可随时导入日志')
      showNewTaskDialog.value = false
      await taskStore.fetchTasks()
      emit('task-created', { taskId: res.data.task_id, needImport: false })
    }
  } finally {
    submitting.value = false
  }
}

const handleCreateAndOpenImport = async () => {
  submitting.value = true
  try {
    const res = await api.createTask({
      task_name: newTaskForm.value.taskName,
      device_type: newTaskForm.value.deviceType
    })
    if (res.code === 0) {
      showNewTaskDialog.value = false
      await taskStore.fetchTasks()
      emit('task-created', { taskId: res.data.task_id, needImport: true })
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
  height: 52px;
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border-base);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  flex-shrink: 0;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.task-badge {
  font-size: var(--fs-sm);
  color: var(--gray-500);
  background: var(--border-base);
  padding: 3px 8px;
  border-radius: var(--radius-sm);
}

.header-right {
  display: flex;
  align-items: center;
}
</style>
