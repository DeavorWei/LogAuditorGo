<template>
  <div class="tasks-page">
    <el-card shadow="never" class="tasks-header-card">
      <div class="header-content">
        <div>
          <h2 class="tasks-title">历史日志审计任务</h2>
          <p class="tasks-subtitle">每次日志审计任务使用独立物理 SQLite 数据库进行隔离存储，支持一键导出离线分析报告</p>
        </div>
        <el-button type="primary" icon="Plus" @click="$router.push('/audit')">新建审计任务</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="table-card">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="table-tip"
        title="日志匹配按各设备的型号与版本逐台进行：请在任务的【设备管理】中配置型号（可填具体型号如 AC6508）与软件版本，修改后点击【重新分析】生效。"
      />
      <el-table :data="taskList" v-loading="loading" style="width: 100%;" border>
        <el-table-column prop="task_name" label="任务名称" min-width="180">
          <template #default="{ row }">
            <button type="button" class="link-like" @click="openTask(row.task_id)">{{ row.task_name }}</button>
          </template>
        </el-table-column>
        <el-table-column prop="device_type" label="设备类型" width="130" />
        <el-table-column prop="device_count" label="设备数" width="80" align="center">
          <template #default="{ row }">
            <el-tag size="small" :type="row.device_count > 1 ? 'primary' : 'info'">{{ row.device_count || 0 }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="file_count" label="文件数" width="80" align="center">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.file_count || (row.log_count > 0 ? 1 : 0) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="log_count" label="日志条数" width="100" align="center" />
        <el-table-column prop="matched_count" label="知识匹配数" width="100" align="center">
          <template #default="{ row }">
            <span class="matched-count-text">{{ row.matched_count }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="rca_count" label="根因事件" width="90" align="center">
          <template #default="{ row }">
            <el-tag size="small" :type="row.rca_count > 0 ? 'danger' : 'info'">{{ row.rca_count }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'COMPLETED' ? 'success' : (row.status === 'PENDING' ? 'warning' : 'danger')">
              {{ row.status === 'PENDING' ? '待导入' : row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="start_time" label="创建时间" width="160">
          <template #default="{ row }">
            {{ formatTime(row.start_time) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="360" align="center">
          <template #default="{ row }">
            <el-button v-if="row.status === 'PENDING'" type="primary" size="small" @click="openTask(row.task_id)">导入日志</el-button>
            <el-button v-else type="primary" link size="small" @click="openTask(row.task_id)">查看审计</el-button>
            <el-button v-if="row.status !== 'PENDING'" type="warning" link size="small" :disabled="row.log_count === 0" @click="handleReanalyze(row)">重新分析</el-button>
            <el-button type="info" link size="small" @click="openRenameDialog(row)">重命名</el-button>
            <el-button type="success" link size="small" :disabled="row.status === 'PENDING'" @click="exportHTML(row.task_id)">导出报告</el-button>
            <el-button v-if="row.device_count > 1" type="warning" link size="small" :disabled="row.status === 'PENDING'" @click="exportMultiHTML(row.task_id)">多设备报告</el-button>
            <el-popconfirm title="确定彻底删除该任务数据库吗？" @confirm="handleDelete(row.task_id)">
              <template #reference>
                <el-button type="danger" link size="small">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 重新分析全流程进度追踪弹窗 -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      @completed="handleProgressComplete"
      @failed="handleProgressFailed"
    />

    <!-- 任务重命名弹窗：任务级 device_type/device_version 已不参与匹配，仅保留任务名可编辑 -->
    <el-dialog v-model="showRenameDialog" title="重命名任务" width="420px" destroy-on-close>
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="任务名称">
          <el-input
            v-model="renameForm.taskName"
            maxlength="64"
            show-word-limit
            placeholder="请输入新的任务名称"
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
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import api from '@/api'
import { formatTime as sharedFormatTime } from '@/utils/format'
import { useReanalyze } from '@/composables/useReanalyze'

const router = useRouter()
const loading = ref(false)
const taskList = ref([])
const showProgressModal = ref(false)
const currentJobId = ref('')

// 任务重命名（取代原"编辑属性"弹窗：型号/版本已下沉到设备管理，任务级只剩名称可编辑）
const showRenameDialog = ref(false)
const renaming = ref(false)
const renameTarget = ref(null)
const renameForm = ref({ taskName: '' })

const fetchTasks = async () => {
  loading.value = true
  try {
    const res = await api.getTasks()
    if (res.code === 0) {
      taskList.value = res.data
    }
  } finally {
    loading.value = false
  }
}

const openTask = (taskId) => {
  router.push(`/audit/${taskId}`)
}

const exportHTML = async (taskId) => {
  try {
    await api.downloadTaskReport(taskId, 'html')
    ElMessage.success('报告导出完成')
  } catch (e) {}
}

const exportMultiHTML = async (taskId) => {
  try {
    await api.downloadMultiDeviceReport(taskId, 'html')
    ElMessage.success('多设备报告导出完成')
  } catch (e) {}
}

// WEB-16: 与 AuditWorkbench 共用同一套"重新分析"编排，消除逐行重复与行为分叉
const { reanalyze: handleReanalyze } = useReanalyze({
  onJobStarted: (jobId) => {
    currentJobId.value = jobId
    showProgressModal.value = true
  },
  onSettled: async () => {
    await fetchTasks()
  }
})

// 去阻断：重新分析一键直达（`handleReanalyze` 即 useReanalyze 返回的编排函数），
// 只做一次轻量确认，不再弹型号选择框。
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
      ElMessage.success('任务已重命名')
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
// 此前误写为 @complete，导致重新分析完成后回调永不触发、列表长期停留在旧状态。
const handleProgressComplete = () => {
  ElMessage.success('重新分析已全部完成！')
  fetchTasks()
}

const handleProgressFailed = (payload) => {
  const reason = typeof payload === 'string' ? payload : payload?.error || payload?.message
  ElMessage.error('重新分析失败' + (reason ? ': ' + reason : ''))
  fetchTasks()
}

const handleDelete = async (taskId) => {
  try {
    const res = await api.deleteTask(taskId)
    if (res.code === 0) {
      ElMessage.success('任务删除成功')
      fetchTasks()
    }
  } catch (e) {}
}

// WEB-16: 复用统一实现，补齐 Go time.Time 零值保护（本地实现缺少该保护）
const formatTime = sharedFormatTime

onMounted(() => {
  fetchTasks()
})
</script>

<style scoped>
.tasks-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.tasks-header-card {
  border-radius: var(--radius-lg);
}
.header-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.table-card {
  border-radius: var(--radius-lg);
}
.table-tip {
  margin-bottom: 12px;
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
.matched-count-text {
  color: var(--success-strong);
  font-weight: bold;
}
</style>
