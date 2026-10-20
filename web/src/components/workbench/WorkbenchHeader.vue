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
        <el-button size="small" circle :icon="Plus" class="new-task-btn" @click="$emit('new-task')" />
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

        <!-- 结论摘要：一句话可点击编辑（规划 §7.2 Header） -->
        <el-tooltip :content="currentTask.conclusion || '点击填写案例结论'" placement="bottom">
          <span class="conclusion-chip" @click="$emit('open-conclusion')">
            <el-icon><EditPen /></el-icon>
            <span class="conclusion-text">{{ conclusionText }}</span>
          </span>
        </el-tooltip>
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
            <el-dropdown-item command="package">
              <el-icon><Box /></el-icon><span>导出证据包 (ZIP)</span>
            </el-dropdown-item>
            <el-dropdown-item command="copy_summary">
              <el-icon><CopyDocument /></el-icon><span>复制交接摘要</span>
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
            <el-dropdown-item command="summary">
              <el-icon><Odometer /></el-icon><span>诊断摘要</span>
            </el-dropdown-item>
            <el-dropdown-item command="conclusion">
              <el-icon><EditPen /></el-icon><span>编辑案例结论</span>
            </el-dropdown-item>
            <el-dropdown-item command="refresh" divided>
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
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Monitor, Operation, Upload, Download, ArrowDown, Plus, Refresh, Document,
  DataAnalysis, Tickets, Files, Opportunity, Delete, MoreFilled, EditPen,
  Box, CopyDocument, Odometer
} from '@element-plus/icons-vue'
import { useTaskStore } from '@/stores/task'
import { useFilterStore } from '@/stores/filter'
import { TASK_STATUS } from '@/constants/taskStatus'
import { formatTime } from '@/utils/format'
import api from '@/api'
import TaskStatusTag from '@/components/TaskStatusTag.vue'

const emit = defineEmits([
  'task-change',
  'new-task',
  'task-deleted',
  'refresh-logs',
  'open-files',
  'reanalyze',
  'open-import',
  'open-devices',
  'open-advanced',
  'open-report',
  'open-conclusion',
  'open-summary'
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

const conclusionText = computed(() => {
  const text = (currentTask.value?.conclusion || '').replace(/\s+/g, ' ').trim()
  if (!text) return '写结论'
  return text.length > 26 ? text.slice(0, 26) + '…' : text
})

// 导出
const exportingReport = ref(false)
const handleExportCommand = async (cmd) => {
  if (!taskStore.currentTaskId || exportingReport.value) return
  if (cmd === 'open-report') {
    emit('open-report')
    return
  }
  if (cmd === 'copy_summary') {
    await copySummary()
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
    } else if (cmd === 'package') {
      await api.downloadEvidencePackage(taskStore.currentTaskId, currentTask.value?.task_name)
      ElMessage.success('已导出证据包')
    }
  } catch (e) {
    console.error('Export report failed:', e)
  } finally {
    exportingReport.value = false
  }
}

const copySummary = async () => {
  try {
    const markdown = await api.getSummaryMarkdown(taskStore.currentTaskId)
    if (!markdown) {
      ElMessage.warning('摘要内容为空')
      return
    }
    await navigator.clipboard.writeText(markdown)
    ElMessage.success('交接摘要已复制到剪贴板')
  } catch (e) {
    ElMessage.warning('复制失败，请在“分析报告”中查看摘要')
  }
}

const handleMoreCommand = async (cmd) => {
  if (cmd === 'summary') {
    emit('open-summary')
  } else if (cmd === 'conclusion') {
    emit('open-conclusion')
  } else if (cmd === 'refresh') {
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

const onTaskChange = (taskId) => {
  emit('task-change', taskId)
}
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
  flex-wrap: wrap;
}

.task-select {
  width: 240px;
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

.conclusion-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 320px;
  padding: 3px 8px;
  border-radius: var(--radius-sm);
  border: 1px dashed var(--brand-400);
  color: var(--brand-600);
  font-size: var(--fs-sm);
  cursor: pointer;
  white-space: nowrap;
}

.conclusion-chip:hover {
  background: var(--brand-50, #eff6ff);
}

.conclusion-text {
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 280px;
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
  .header-right .btn-text {
    display: none;
  }
}
</style>
