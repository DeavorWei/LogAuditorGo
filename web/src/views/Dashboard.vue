<template>
  <div class="dashboard-page">
    <!-- 顶部概览指标卡 (带加载骨架屏) -->
    <el-skeleton :loading="statsLoading" animated>
      <template #template>
        <el-row :gutter="16" class="stats-row">
          <el-col v-for="i in 4" :key="i" :span="6">
            <el-skeleton-item variant="rect" style="height: 108px; border-radius: var(--radius-lg);" />
          </el-col>
        </el-row>
      </template>
      <template #default>
        <el-row :gutter="16" class="stats-row">
          <el-col :span="6">
            <StatCard
              :icon="Reading"
              :value="stats.total_knowledge || 0"
              title="已收录故障知识条目"
              tone="brand"
            />
          </el-col>
          <el-col :span="6">
            <StatCard
              :icon="FolderOpened"
              :value="stats.total_documents || 0"
              title="已导入官方产品文档"
              tone="success"
            />
          </el-col>
          <el-col :span="6">
            <StatCard
              :icon="List"
              :value="stats.total_tasks || 0"
              title="累计审计任务数"
              tone="warning"
            />
          </el-col>
          <el-col :span="6">
            <StatCard
              :icon="DataAnalysis"
              :value="stats.total_logs_analyzed || 0"
              title="累计解析日志总量"
              tone="purple"
            />
          </el-col>
        </el-row>
      </template>
    </el-skeleton>

    <!-- 图表与快速操作区 -->
    <el-row :gutter="16" class="content-row">
      <el-col :span="15">
        <el-card shadow="hover" class="chart-card">
          <template #header>
            <div class="card-header">
              <el-icon><DataAnalysis /></el-icon>
              <span>知识库各网络模块知识分布 (Top 10)</span>
            </div>
          </template>
          <div ref="moduleChartRef" class="module-chart-box"></div>
        </el-card>
      </el-col>
      <el-col :span="9">
        <el-card shadow="hover" class="action-card">
          <template #header>
            <div class="card-header">
              <el-icon><Position /></el-icon>
              <span>快速开始</span>
            </div>
          </template>
          <div class="quick-actions">
            <button type="button" class="action-item reset" @click="showNewTaskDialog = true">
              <el-icon class="action-icon tone-brand"><UploadFilled /></el-icon>
              <div>
                <div class="action-title">新建日志审计任务</div>
                <div class="action-desc">上传设备 Syslog 文本或日志文件进行智能分析与根因诊断</div>
              </div>
            </button>
            <button type="button" class="action-item reset" @click="$router.push('/documents')">
              <el-icon class="action-icon tone-success"><FolderAdd /></el-icon>
              <div>
                <div class="action-title">导入华为 HDX 产品文档</div>
                <div class="action-desc">导入官方 HDX 文档压缩包或本地文件夹以扩充故障知识库</div>
              </div>
            </button>
            <button type="button" class="action-item reset" @click="$router.push('/knowledge')">
              <el-icon class="action-icon tone-purple"><Search /></el-icon>
              <div>
                <div class="action-title">检索官方故障知识库</div>
                <div class="action-desc">根据报错简名、Trap OID 或关键词检索处理指导</div>
              </div>
            </button>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 新建任务弹窗 (统一使用 NewTaskDialog) -->
    <NewTaskDialog
      v-model="showNewTaskDialog"
      :loading="creatingTask"
      show-content
      @confirm="handleCreateTask"
    />

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
import {
  Reading,
  FolderOpened,
  List,
  DataAnalysis,
  Position,
  UploadFilled,
  FolderAdd,
  Search
} from '@element-plus/icons-vue'
// UI-09: 改用按需引入的 echarts 入口，避免把全量图表类型打进首屏
import echarts from '@/plugins/echarts'
import api from '@/api'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import StatCard from '@/components/StatCard.vue'
import NewTaskDialog from '@/components/NewTaskDialog.vue'
import { TASK_DEVICE_TYPE_OPTIONS as DEVICE_TYPE_OPTIONS, DEFAULT_TASK_DEVICE_TYPE as DEFAULT_DEVICE_TYPE } from '@/constants/deviceTypes'

const router = useRouter()
const stats = ref({})
const statsLoading = ref(true)
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
  statsLoading.value = true
  try {
    const res = await api.getStats()
    if (res.code === 0) {
      stats.value = res.data
      renderModuleChart(res.data.top_modules || [])
    }
  } catch (e) {
    // handled
  } finally {
    statsLoading.value = false
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
          // [ECharts豁免项] Canvas 上下文渐变色，对应系统令牌 --brand-400 (#38bdf8) 与 --brand-600 (#0284c7)
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

const handleCreateTask = async (formData) => {
  const content = (formData?.content ?? newTaskForm.value.content).trim()
  const taskName = formData?.taskName ?? newTaskForm.value.taskName
  const deviceType = formData?.deviceType ?? newTaskForm.value.deviceType

  if (!content) {
    ElMessage.warning('请输入待审计的日志内容')
    return
  }

  creatingTask.value = true
  try {
    const res = await api.createTask({
      task_name: taskName,
      device_type: deviceType,
      content: content
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
.chart-card, .action-card {
  border-radius: var(--radius-lg);
  height: 420px;
}
.card-header {
  font-weight: 600;
  font-size: 15px;
  display: flex;
  align-items: center;
  gap: 6px;
}
.module-chart-box {
  width: 100%;
  height: 340px;
}
.quick-actions {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.action-item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px;
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all 0.2s ease;
}
.action-item:hover {
  background: var(--gray-100);
  border-color: var(--gray-300);
  transform: translateY(-1px);
}
.action-icon {
  font-size: 28px;
}
.action-icon.tone-brand {
  color: var(--brand-500);
}
.action-icon.tone-success {
  color: var(--success-strong);
}
.action-icon.tone-purple {
  color: var(--purple-500);
}
.action-title {
  font-size: var(--fs-lg);
  font-weight: 600;
  color: var(--gray-800);
}
.action-desc {
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  margin-top: 2px;
}
</style>
