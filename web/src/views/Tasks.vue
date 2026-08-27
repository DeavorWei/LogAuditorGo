<template>
  <div class="tasks-page">
    <el-card shadow="never" class="tasks-header-card">
      <div class="header-content">
        <div>
          <h2 style="font-size: 16px; margin-bottom: 4px;">历史日志审计任务</h2>
          <p style="font-size: 12px; color: #64748b;">每次日志审计任务使用独立物理 SQLite 数据库进行隔离存储，支持一键导出离线分析报告</p>
        </div>
        <el-button type="primary" icon="Plus" @click="$router.push('/audit')">新建审计任务</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="table-card">
      <el-table :data="taskList" v-loading="loading" style="width: 100%;" border>
        <el-table-column prop="task_name" label="任务名称" min-width="180">
          <template #default="{ row }">
            <span style="font-weight: 600; color: #0284c7; cursor: pointer;" @click="openTask(row.task_id)">{{ row.task_name }}</span>
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
            <span style="color: #16a34a; font-weight: bold;">{{ row.matched_count }}</span>
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
        <el-table-column label="操作" width="320" align="center">
          <template #default="{ row }">
            <el-button v-if="row.status === 'PENDING'" type="primary" size="small" @click="openTask(row.task_id)">导入日志</el-button>
            <el-button v-else type="primary" link size="small" @click="openTask(row.task_id)">查看审计</el-button>
            <el-button v-if="row.status !== 'PENDING'" type="warning" link size="small" :disabled="row.log_count === 0" @click="handleReanalyze(row)">重新分析</el-button>
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
      @complete="handleProgressComplete"
    />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import api from '@/api'

const router = useRouter()
const loading = ref(false)
const taskList = ref([])
const showProgressModal = ref(false)
const currentJobId = ref('')

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

const exportHTML = (taskId) => {
  window.open(`/api/v1/tasks/${taskId}/export?format=html`, '_blank')
}

const exportMultiHTML = (taskId) => {
  window.open(`/api/v1/tasks/${taskId}/multi-device/export?format=html`, '_blank')
}

const handleReanalyze = (row) => {
  ElMessageBox.confirm(
    `确认对任务【${row.task_name}】的全部 ${row.log_count} 行已导入日志重新执行知识库匹配与 RCA 根因拓扑分析吗？`,
    '重新分析确认',
    {
      confirmButtonText: '开始重新分析',
      cancelButtonText: '取消',
      type: 'warning'
    }
  ).then(async () => {
    try {
      const res = await api.reanalyzeTask(row.task_id, true)
      if (res.code === 0 && res.data?.job_id) {
        currentJobId.value = res.data.job_id
        showProgressModal.value = true
      } else {
        ElMessage.success('重新分析请求已提交')
        fetchTasks()
      }
    } catch (e) {
      ElMessage.error('触发重新分析失败: ' + (e.message || '网络异常'))
    }
  }).catch(() => {})
}

const handleProgressComplete = () => {
  ElMessage.success('重新分析已全部完成！')
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

const formatTime = (ts) => {
  if (!ts) return '-'
  return ts.replace('T', ' ').substring(0, 19)
}

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
  border-radius: 8px;
}
.header-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.table-card {
  border-radius: 8px;
}
</style>
