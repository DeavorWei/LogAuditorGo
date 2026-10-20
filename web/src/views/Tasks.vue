<template>
  <div class="tasks-page">
    <el-card shadow="never" class="tasks-header-card">
      <div class="header-content">
        <div>
          <h2 class="tasks-title">案例库</h2>
          <p class="tasks-subtitle">
            每次排障任务都是一个独立案例：任务库物理隔离，可按名称、状态与时间检索，支持导出离线报告。
          </p>
        </div>
        <el-button type="primary" icon="Plus" @click="$router.push('/audit')">新建排障任务</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="table-card">
      <!-- 检索与筛选 -->
      <div class="filter-bar">
        <el-input
          v-model="filters.keyword"
          placeholder="搜索任务名称 / 任务 ID"
          clearable
          class="keyword-input"
          :prefix-icon="Search"
          @input="onKeywordInput"
          @clear="applyFilters"
        />
        <el-select
          v-model="filters.statuses"
          multiple
          collapse-tags
          clearable
          placeholder="全部状态"
          class="status-select"
          @change="applyFilters"
        >
          <el-option
            v-for="opt in TASK_STATUS_OPTIONS"
            :key="opt.value"
            :label="opt.label"
            :value="opt.value"
          />
        </el-select>
        <el-date-picker
          v-model="filters.range"
          type="datetimerange"
          range-separator="至"
          start-placeholder="创建时间起"
          end-placeholder="创建时间止"
          value-format="YYYY-MM-DD HH:mm:ss"
          class="time-range-picker"
          @change="applyFilters"
        />
        <el-select v-model="filters.sort" class="sort-select" @change="applyFilters">
          <el-option label="最新创建" value="start_time:desc" />
          <el-option label="最早创建" value="start_time:asc" />
          <el-option label="日志最多" value="log_count:desc" />
          <el-option label="匹配最多" value="matched_count:desc" />
        </el-select>
        <el-button :icon="Refresh" @click="fetchTasks">刷新</el-button>
      </div>

      <el-table :data="taskList" v-loading="loading" style="width: 100%;" border>
        <el-table-column prop="task_name" label="案例名称" min-width="220">
          <template #default="{ row }">
            <button type="button" class="link-like" @click="openTask(row.task_id)">{{ row.task_name }}</button>
            <div class="task-id-sub">{{ row.task_id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110" align="center">
          <template #default="{ row }">
            <el-tooltip
              v-if="row.status === TASK_STATUS.FAILED && row.error_message"
              :content="row.error_message"
              placement="top"
            >
              <span><TaskStatusTag :status="row.status" /></span>
            </el-tooltip>
            <TaskStatusTag v-else :status="row.status" />
          </template>
        </el-table-column>
        <el-table-column label="设备 / 日志" width="130" align="center">
          <template #default="{ row }">
            <span class="metric-text">{{ row.device_count || 0 }} 台 / {{ row.log_count || 0 }} 行</span>
          </template>
        </el-table-column>
        <el-table-column label="知识覆盖" width="120" align="center">
          <template #default="{ row }">
            <span class="matched-count-text">{{ matchRateText(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            {{ formatTime(row.start_time) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" align="center" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link size="small" @click="openTask(row.task_id)">
              {{ row.status === TASK_STATUS.PENDING ? '导入日志' : '查看' }}
            </el-button>
            <el-dropdown trigger="click" @command="(cmd) => handleCommand(cmd, row)">
              <el-button type="primary" link size="small">
                更多<el-icon class="el-icon--right"><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="reanalyze" :disabled="row.status === TASK_STATUS.PENDING || !row.log_count">
                    <el-icon><Opportunity /></el-icon><span>重新分析</span>
                  </el-dropdown-item>
                  <el-dropdown-item command="rename">
                    <el-icon><EditPen /></el-icon><span>重命名</span>
                  </el-dropdown-item>
                  <el-dropdown-item command="export_html" :disabled="row.status === TASK_STATUS.PENDING" divided>
                    <el-icon><Document /></el-icon><span>导出 HTML 报告</span>
                  </el-dropdown-item>
                  <el-dropdown-item command="export_csv" :disabled="row.status === TASK_STATUS.PENDING">
                    <el-icon><Tickets /></el-icon><span>导出 CSV 明细</span>
                  </el-dropdown-item>
                  <el-dropdown-item v-if="row.device_count > 1" command="export_multi" :disabled="row.status === TASK_STATUS.PENDING">
                    <el-icon><Monitor /></el-icon><span>多设备报告</span>
                  </el-dropdown-item>
                  <el-dropdown-item command="delete" divided>
                    <el-icon><Delete /></el-icon><span class="danger-text">删除案例</span>
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty :description="emptyText">
            <el-button type="primary" @click="$router.push('/audit')">去创建排障任务</el-button>
          </el-empty>
        </template>
      </el-table>

      <div class="pagination-bar">
        <span class="total-text">共 {{ pagination.total }} 个案例</span>
        <el-pagination
          v-model:current-page="pagination.page"
          v-model:page-size="pagination.pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="pagination.total"
          layout="sizes, prev, pager, next, jumper"
          background
          @current-change="fetchTasks"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>

    <!-- 重新分析全流程进度追踪弹窗 -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      @completed="handleProgressComplete"
      @failed="handleProgressFailed"
    />

    <!-- 任务重命名弹窗 -->
    <el-dialog v-model="showRenameDialog" title="重命名案例" width="420px" destroy-on-close>
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="案例名称">
          <el-input
            v-model="renameForm.taskName"
            maxlength="64"
            show-word-limit
            placeholder="请输入新的案例名称"
            @keyup.enter="submitRename"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showRenameDialog = false">取消</el-button>
        <el-button type="primary" :loading="renaming" @click="submitRename">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Search, Refresh, ArrowDown, Opportunity, EditPen, Document, Tickets, Monitor, Delete
} from '@element-plus/icons-vue'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import TaskStatusTag from '@/components/TaskStatusTag.vue'
import { TASK_STATUS, TASK_STATUS_OPTIONS } from '@/constants/taskStatus'
import { formatTime as sharedFormatTime } from '@/utils/format'
import { useReanalyze } from '@/composables/useReanalyze'
import api from '@/api'

const router = useRouter()
const loading = ref(false)
const taskList = ref([])
const showProgressModal = ref(false)
const currentJobId = ref('')

const filters = ref({
  keyword: '',
  statuses: [],
  range: null,
  sort: 'start_time:desc'
})

const pagination = ref({ page: 1, pageSize: 20, total: 0 })

const emptyText = computed(() => {
  const hasFilter = filters.value.keyword || filters.value.statuses.length || filters.value.range
  return hasFilter ? '没有符合筛选条件的案例' : '暂无排障案例'
})

const formatTime = sharedFormatTime

const matchRateText = (row) => {
  const total = row.log_count || 0
  if (!total) return '—'
  return `${(((row.matched_count || 0) / total) * 100).toFixed(1)}%`
}

let keywordTimer = null
const onKeywordInput = () => {
  if (keywordTimer) clearTimeout(keywordTimer)
  keywordTimer = setTimeout(() => {
    applyFilters()
  }, 300)
}

const applyFilters = () => {
  pagination.value.page = 1
  fetchTasks()
}

const fetchTasks = async () => {
  loading.value = true
  try {
    const [sortBy, order] = (filters.value.sort || 'start_time:desc').split(':')
    const params = {
      page: pagination.value.page,
      page_size: pagination.value.pageSize,
      sort_by: sortBy || 'start_time',
      order: order || 'desc'
    }
    if (filters.value.keyword) params.keyword = filters.value.keyword.trim()
    if (filters.value.statuses?.length) params.status = filters.value.statuses.join(',')
    if (filters.value.range?.length === 2) {
      params.start_from = filters.value.range[0]
      params.start_to = filters.value.range[1]
    }
    const res = await api.queryTasks(params)
    if (res.code === 0) {
      taskList.value = res.data?.items || []
      pagination.value.total = res.data?.total || 0
    }
  } finally {
    loading.value = false
  }
}

const handleSizeChange = () => {
  pagination.value.page = 1
  fetchTasks()
}

const openTask = (taskId) => {
  router.push(`/audit/${taskId}`)
}

const handleCommand = async (cmd, row) => {
  if (cmd === 'reanalyze') {
    await handleReanalyze(row)
  } else if (cmd === 'rename') {
    openRenameDialog(row)
  } else if (cmd === 'export_html') {
    await api.downloadTaskReport(row.task_id, 'html')
    ElMessage.success('报告导出完成')
  } else if (cmd === 'export_csv') {
    await api.downloadTaskReport(row.task_id, 'csv')
    ElMessage.success('明细导出完成')
  } else if (cmd === 'export_multi') {
    await api.downloadMultiDeviceReport(row.task_id, 'html')
    ElMessage.success('多设备报告导出完成')
  } else if (cmd === 'delete') {
    await handleDelete(row)
  }
}

// 任务重命名（型号/版本已下沉到设备管理，任务级只剩名称可编辑）
const showRenameDialog = ref(false)
const renaming = ref(false)
const renameTarget = ref(null)
const renameForm = ref({ taskName: '' })

// WEB-16: 与 AuditWorkbench 共用同一套"重新分析"编排
const { reanalyze: handleReanalyze } = useReanalyze({
  onJobStarted: (jobId) => {
    currentJobId.value = jobId
    showProgressModal.value = true
  },
  onSettled: async () => {
    await fetchTasks()
  }
})

const openRenameDialog = (task) => {
  renameTarget.value = task
  renameForm.value = { taskName: task?.task_name || '' }
  showRenameDialog.value = true
}

const submitRename = async () => {
  const name = (renameForm.value.taskName || '').trim()
  if (!renameTarget.value) return
  if (!name) {
    ElMessage.warning('任务名称不能为空')
    return
  }
  renaming.value = true
  try {
    const res = await api.updateTask(renameTarget.value.task_id, { task_name: name })
    if (res.code === 0) {
      ElMessage.success('案例已重命名')
      showRenameDialog.value = false
      await fetchTasks()
    } else {
      ElMessage.error(res.message || '重命名失败')
    }
  } catch (e) {
    ElMessage.error('重命名失败: ' + (e?.message || '网络异常'))
  } finally {
    renaming.value = false
  }
}

// 注意：事件名必须与 ImportProgressModal 的 defineEmits 保持一致（completed / failed）。
const handleProgressComplete = () => {
  ElMessage.success('重新分析已全部完成！')
  fetchTasks()
}

const handleProgressFailed = (payload) => {
  const reason = typeof payload === 'string' ? payload : payload?.error || payload?.message
  ElMessage.error('重新分析失败' + (reason ? ': ' + reason : ''))
  fetchTasks()
}

const handleDelete = async (row) => {
  try {
    await ElMessageBox.confirm(
      `删除案例「${row.task_name}」会同时删除其全部日志与任务数据库文件，且无法恢复。是否继续？`,
      '确认删除案例',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
  } catch (e) {
    return
  }
  const res = await api.deleteTask(row.task_id)
  if (res.code === 0) {
    ElMessage.success('案例已删除')
    await fetchTasks()
  }
}

onMounted(fetchTasks)
onUnmounted(() => {
  if (keywordTimer) clearTimeout(keywordTimer)
})
</script>

<style scoped>
.tasks-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.tasks-header-card,
.table-card {
  border-radius: var(--radius-lg);
}

.header-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}

.tasks-title {
  font-size: var(--fs-xl);
  margin: 0 0 4px 0;
  color: var(--text-primary);
}

.tasks-subtitle {
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  margin: 0;
}

.filter-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
}

.keyword-input {
  width: 260px;
}

.status-select {
  width: 200px;
}

.time-range-picker {
  width: 340px;
}

.sort-select {
  width: 140px;
}

.task-id-sub {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.metric-text {
  color: var(--text-secondary);
}

.matched-count-text {
  color: var(--success-strong);
  font-weight: 600;
}

.pagination-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 14px;
}

.total-text {
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.danger-text {
  color: var(--danger-strong);
}
</style>
