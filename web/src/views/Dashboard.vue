<template>
  <div class="dashboard-page">
    <!-- 顶部概览指标卡 -->
    <el-row :gutter="16" class="stats-row">
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card stat-blue">
          <div class="stat-icon"><el-icon><Reading /></el-icon></div>
          <div class="stat-info">
            <div class="stat-val">{{ stats.total_knowledge || 0 }}</div>
            <div class="stat-title">已收录故障知识条目</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card stat-emerald">
          <div class="stat-icon"><el-icon><FolderOpened /></el-icon></div>
          <div class="stat-info">
            <div class="stat-val">{{ stats.total_documents || 0 }}</div>
            <div class="stat-title">已导入官方产品文档</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card stat-amber">
          <div class="stat-icon"><el-icon><List /></el-icon></div>
          <div class="stat-info">
            <div class="stat-val">{{ stats.total_tasks || 0 }}</div>
            <div class="stat-title">累计审计任务数</div>
          </div>
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card stat-purple">
          <div class="stat-icon"><el-icon><DataAnalysis /></el-icon></div>
          <div class="stat-info">
            <div class="stat-val">{{ stats.total_logs_analyzed || 0 }}</div>
            <div class="stat-title">累计解析日志总量</div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 图表与快速操作区 -->
    <el-row :gutter="16" class="content-row">
      <el-col :span="15">
        <el-card shadow="hover" class="chart-card">
          <template #header>
            <div class="card-header">
              <span>📊 知识库各网络模块知识分布 (Top 10)</span>
            </div>
          </template>
          <div ref="moduleChartRef" style="width: 100%; height: 340px;"></div>
        </el-card>
      </el-col>
      <el-col :span="9">
        <el-card shadow="hover" class="action-card">
          <template #header>
            <div class="card-header">
              <span>🚀 快速开始</span>
            </div>
          </template>
          <div class="quick-actions">
            <div class="action-item" @click="showNewTaskDialog = true">
              <el-icon class="action-icon" style="color: #3b82f6;"><UploadFilled /></el-icon>
              <div>
                <div class="action-title">新建日志审计任务</div>
                <div class="action-desc">上传设备 Syslog 文本或日志文件进行智能分析与根因诊断</div>
              </div>
            </div>
            <div class="action-item" @click="$router.push('/documents')">
              <el-icon class="action-icon" style="color: #10b981;"><FolderAdd /></el-icon>
              <div>
                <div class="action-title">导入华为 HDX 产品文档</div>
                <div class="action-desc">导入官方 HDX 文档压缩包或本地文件夹以扩充故障知识库</div>
              </div>
            </div>
            <div class="action-item" @click="$router.push('/knowledge')">
              <el-icon class="action-icon" style="color: #8b5cf6;"><Search /></el-icon>
              <div>
                <div class="action-title">检索官方故障知识库</div>
                <div class="action-desc">根据报错简名、Trap OID 或关键词检索处理指导</div>
              </div>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 新建任务弹窗 -->
    <el-dialog v-model="showNewTaskDialog" title="新建日志审计任务" width="600px">
      <el-form label-position="top">
        <el-form-item label="任务名称">
          <el-input v-model="newTaskForm.taskName" placeholder="例如: Core-SW-01故障排查-20260415" />
        </el-form-item>
        <el-form-item label="设备类型">
          <!-- WEB-16: 选项统一取自常量，与 AuditWorkbench 保持同一口径 -->
          <el-select v-model="newTaskForm.deviceType" style="width: 100%;">
            <el-option
              v-for="opt in DEVICE_TYPE_OPTIONS"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="粘贴 Syslog 日志文本">
          <el-input
            v-model="newTaskForm.content"
            type="textarea"
            :rows="6"
            placeholder="粘贴设备日志，例如: Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042]: BGP session authentication failed..."
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showNewTaskDialog = false">取消</el-button>
        <el-button type="primary" :loading="creatingTask" @click="handleCreateTask">立即分析</el-button>
      </template>
    </el-dialog>

    <!-- 后台分析进度追踪弹窗（WEB-04） -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      @completed="handleTaskCreateCompleted"
      @failed="handleTaskCreateFailed"
    />
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
// UI-09: 改用按需引入的 echarts 入口，避免把全量图表类型打进首屏
import echarts from '@/plugins/echarts'
import api from '@/api'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import { TASK_DEVICE_TYPE_OPTIONS as DEVICE_TYPE_OPTIONS, DEFAULT_TASK_DEVICE_TYPE as DEFAULT_DEVICE_TYPE } from '@/constants/deviceTypes'

const router = useRouter()
const stats = ref({})
const moduleChartRef = ref(null)
let chartInstance = null

const showNewTaskDialog = ref(false)
const creatingTask = ref(false)
const newTaskForm = ref({
  taskName: '',
  deviceType: DEFAULT_DEVICE_TYPE,
  content: ''
})

// 后台分析进度追踪（WEB-04）：createTask 默认异步返回 job_id
const showProgressModal = ref(false)
const currentJobId = ref('')
const pendingTaskId = ref('')

const fetchStats = async () => {
  try {
    const res = await api.getStats()
    if (res.code === 0) {
      stats.value = res.data
      renderModuleChart(res.data.top_modules || [])
    }
  } catch (e) {
    // handled
  }
}

const renderModuleChart = (topModules) => {
  if (!moduleChartRef.value) return
  if (!chartInstance) {
    chartInstance = echarts.init(moduleChartRef.value)
  }

  const names = topModules.map(m => m.module)
  const values = topModules.map(m => m.count)

  const option = {
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
    grid: { left: '3%', right: '4%', bottom: '3%', top: '6%', containLabel: true },
    xAxis: { type: 'category', data: names, axisLabel: { interval: 0, rotate: 25 } },
    yAxis: { type: 'value', name: '知识条目数' },
    series: [
      {
        data: values,
        type: 'bar',
        barWidth: '45%',
        itemStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: '#38bdf8' },
            { offset: 1, color: '#0284c7' }
          ]),
          borderRadius: [4, 4, 0, 0]
        }
      }
    ]
  }
  chartInstance.setOption(option)
}

const handleCreateTask = async () => {
  if (!newTaskForm.value.content.trim()) {
    ElMessage.warning('请输入待审计的日志内容')
    return
  }

  creatingTask.value = true
  try {
    const res = await api.createTask({
      task_name: newTaskForm.value.taskName,
      device_type: newTaskForm.value.deviceType,
      content: newTaskForm.value.content
    })
    if (res.code !== 0) return

    const taskId = res.data?.task_id
    const jobId = res.data?.job_id
    showNewTaskDialog.value = false

    // WEB-04: api.createTask 默认 is_async = true，后端返回时分析仍在后台跑。
    // 原实现直接提示"任务创建并分析完成！"并跳转，用户进入工作台看到的是空/PENDING 数据，
    // 极易误判为创建失败。这里改为：有 job_id 就打开进度弹窗，真正完成后再跳转。
    if (jobId) {
      pendingTaskId.value = taskId || ''
      currentJobId.value = jobId
      showProgressModal.value = true
      ElMessage.info('任务已创建，正在后台分析中...')
      return
    }

    ElMessage.success('任务创建并分析完成！')
    if (taskId) router.push(`/audit/${taskId}`)
  } finally {
    creatingTask.value = false
  }
}

// 后台分析完成：提示并跳转到对应任务的工作台
const handleTaskCreateCompleted = () => {
  showProgressModal.value = false
  ElMessage.success('任务创建并分析完成！')
  if (pendingTaskId.value) {
    router.push(`/audit/${pendingTaskId.value}`)
    pendingTaskId.value = ''
  }
}

const handleTaskCreateFailed = (payload) => {
  const reason = typeof payload === 'string' ? payload : payload?.error || payload?.message
  ElMessage.error('任务分析失败' + (reason ? ': ' + reason : ''))
}

// WEB-07: 原实现注册的是匿名函数且从不移除，每次进入仪表盘都会泄漏一个闭包监听器，
// 反复进出后 resize 会触发 N 次已 dispose 实例的调用。这里改为具名 + 防抖 + 成对移除。
let resizeTimer = null
const handleResize = () => {
  if (resizeTimer) clearTimeout(resizeTimer)
  resizeTimer = setTimeout(() => {
    try {
      chartInstance?.resize()
    } catch (e) {
      // 实例已 dispose 时静默忽略
    }
  }, 150)
}

onMounted(() => {
  fetchStats()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  if (resizeTimer) clearTimeout(resizeTimer)
  chartInstance?.dispose()
})
</script>

<style scoped>
.dashboard-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.stat-card {
  border-radius: 8px;
  border-left: 4px solid transparent;
}
.stat-blue { border-left-color: #3b82f6; }
.stat-emerald { border-left-color: #10b981; }
.stat-amber { border-left-color: #f59e0b; }
.stat-purple { border-left-color: #8b5cf6; }

:deep(.el-card__body) {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 18px 20px;
}

.stat-icon {
  font-size: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #64748b;
}
.stat-val {
  font-size: 26px;
  font-weight: 700;
  color: #0f172a;
}
.stat-title {
  font-size: 13px;
  color: #64748b;
  margin-top: 4px;
}

.chart-card, .action-card {
  border-radius: 8px;
  height: 420px;
}
.card-header {
  font-weight: 600;
  font-size: 15px;
}
.quick-actions {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.action-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
}
.action-item:hover {
  background: #f1f5f9;
  border-color: #cbd5e1;
  transform: translateY(-1px);
}
.action-icon {
  font-size: 28px;
}
.action-title {
  font-size: 14px;
  font-weight: 600;
  color: #1e293b;
}
.action-desc {
  font-size: 12px;
  color: #64748b;
  margin-top: 2px;
}
</style>
