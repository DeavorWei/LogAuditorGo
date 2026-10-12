<template>
  <el-dialog
    v-model="visible"
    title="已导入日志文件清单"
    width="780px"
    align-center
    destroy-on-close
    class="task-files-dialog"
  >
    <div class="files-dialog-content">
      <div class="files-dialog-summary">
        <div class="summary-stats">
          <span class="stat-pill">
            共 <strong>{{ taskFiles.length }}</strong> 个日志文件
          </span>
          <span class="stat-pill">
            累计 <strong>{{ (currentTask?.log_count ?? totalFileLines).toLocaleString() }}</strong> 行日志
          </span>
          <span class="stat-pill">
            总大小 <strong>{{ totalFilesSize }}</strong>
          </span>
        </div>
        <el-button type="primary" size="small" icon="Upload" @click="handleOpenImport">
          补充导入日志
        </el-button>
      </div>

      <el-table
        :data="taskFiles"
        border
        stripe
        size="small"
        style="width: 100%;"
        max-height="400px"
        empty-text="暂无已导入日志文件"
      >
        <el-table-column prop="file_name" label="文件名" min-width="190" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="task-file-cell"><el-icon><Document /></el-icon> {{ row.file_name }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="line_count" label="日志行数" width="105" align="center">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ (row.line_count || 0).toLocaleString() }} 行</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="file_size" label="大小" width="90" align="center">
          <template #default="{ row }">{{ formatSize(row.file_size) }}</template>
        </el-table-column>
        <el-table-column prop="created_at" label="导入时间" width="140" align="center">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="85" align="center" fixed="right">
          <template #default="{ row }">
            <el-button
              type="danger"
              link
              size="small"
              icon="Delete"
              :loading="deletingFileId === row.id"
              @click="handleDelete(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>
    <template #footer>
      <div class="dialog-footer">
        <el-button @click="visible = false">关闭</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Upload, Delete, Document } from '@element-plus/icons-vue'
import api from '@/api'
import { useTaskStore } from '@/stores/task'
import { formatTime, formatSize } from '@/utils/format'

const props = defineProps({
  taskId: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['open-import', 'file-deleted'])

const visible = ref(false)
const deletingFileId = ref(null)

const taskStore = useTaskStore()
const taskFiles = computed(() => taskStore.taskFiles)
const currentTask = computed(() => taskStore.currentTask)

const totalFileLines = computed(() => {
  return taskFiles.value.reduce((acc, f) => acc + (f.line_count || 0), 0)
})

const totalFilesSize = computed(() => {
  const bytes = taskFiles.value.reduce((acc, f) => acc + (f.file_size || 0), 0)
  return formatSize(bytes)
})

const open = () => {
  visible.value = true
}

const close = () => {
  visible.value = false
}

const handleOpenImport = () => {
  emit('open-import')
}

const handleDelete = async (row) => {
  if (!row || !props.taskId) return
  try {
    await ElMessageBox.confirm(
      `确定要删除日志文件「${row.file_name}」吗？\n删除后将同步移除该文件的 ${row.line_count || 0} 行日志记录，并更新设备日志统计及 RCA 分析结果。此操作无法撤销。`,
      '删除日志文件确认',
      {
        confirmButtonText: '确认删除',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
        distinguishCancelAndClose: true
      }
    )
  } catch (action) {
    return
  }

  deletingFileId.value = row.id
  try {
    const res = await api.deleteTaskFile(props.taskId, row.id)
    if (res && res.code === 0) {
      ElMessage.success(`文件「${row.file_name}」已成功删除`)
      await taskStore.fetchTaskFiles(props.taskId)
      emit('file-deleted', { file: row, meta: res.data })
      if (taskFiles.value.length === 0) {
        visible.value = false
      }
    } else {
      ElMessage.error(res?.message || '删除日志文件失败')
    }
  } catch (err) {
    ElMessage.error(err.message || '删除日志文件失败')
  } finally {
    deletingFileId.value = null
  }
}

defineExpose({
  visible,
  open,
  close
})
</script>

<style scoped>
.files-dialog-content {
  padding: 4px 0 12px 0;
}

.files-dialog-summary {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 10px 14px;
  margin-bottom: 12px;
}

.summary-stats {
  display: flex;
  gap: 16px;
}

.stat-pill {
  font-size: var(--fs-md);
  color: var(--gray-600);
}

.stat-pill strong {
  color: var(--gray-900);
  font-size: var(--fs-lg);
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
}

.task-file-cell {
  font-weight: 600;
  color: var(--brand-600);
  display: inline-flex;
  align-items: center;
  gap: var(--sp-2);
}
</style>
