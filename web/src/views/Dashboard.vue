<template>
  <div class="dashboard-page">
    <!-- 首启 checklist：跳过引导后仍可继续（v1.3） -->
    <OnboardingChecklist @open-guide="onboardingStore.reopen()" />

    <!-- 今日概览：最近任务与需要关注的案例 -->
    <el-row :gutter="16" class="content-row">
      <el-col :span="15">
        <el-card shadow="hover" class="recent-card">
          <template #header>
            <div class="card-header">
              <div class="card-header-left">
                <el-icon><Clock /></el-icon>
                <span>最近排障案例</span>
              </div>
              <el-button link type="primary" @click="$router.push('/tasks')">查看全部案例</el-button>
            </div>
          </template>

          <div v-if="attentionText" class="attention-bar">
            <el-icon><Warning /></el-icon>
            <span>{{ attentionText }}</span>
            <el-button link type="warning" size="small" @click="$router.push('/tasks')">去处理</el-button>
          </div>

          <el-skeleton :loading="recentLoading" :rows="4" animated>
            <template #default>
              <el-table v-if="recentTasks.length" :data="recentTasks" size="small" style="width: 100%">
                <el-table-column prop="task_name" label="案例" min-width="180">
                  <template #default="{ row }">
                    <button type="button" class="link-like" @click="$router.push(`/audit/${row.task_id}`)">
                      {{ row.task_name }}
                    </button>
                  </template>
                </el-table-column>
                <el-table-column label="状态" width="100" align="center">
                  <template #default="{ row }">
                    <TaskStatusTag :status="row.status" />
                  </template>
                </el-table-column>
                <el-table-column label="日志 / 匹配" width="130" align="center">
                  <template #default="{ row }">
                    {{ row.log_count || 0 }} / {{ row.matched_count || 0 }}
                  </template>
                </el-table-column>
                <el-table-column label="创建时间" width="160">
                  <template #default="{ row }">{{ formatTime(row.start_time) }}</template>
                </el-table-column>
              </el-table>
              <el-empty v-else description="还没有排障案例">
                <el-button type="primary" @click="openNewTask">创建第一个排障任务</el-button>
              </el-empty>
            </template>
          </el-skeleton>
        </el-card>
      </el-col>

      <el-col :span="9">
        <el-card shadow="hover" class="action-card">
          <template #header>
            <div class="card-header">
              <div class="card-header-left">
                <el-icon><Position /></el-icon>
                <span>快速开始</span>
              </div>
            </div>
          </template>
          <div class="quick-actions">
            <button type="button" class="action-item reset" @click="openNewTask">
              <el-icon class="action-icon tone-brand"><UploadFilled /></el-icon>
              <div>
                <div class="action-title">新建排障任务</div>
                <div class="action-desc">粘贴 Syslog 文本或选择本机日志文件，自动解析并匹配官方知识</div>
              </div>
            </button>
            <button type="button" class="action-item reset" @click="$router.push('/documents')">
              <el-icon class="action-icon tone-success"><FolderAdd /></el-icon>
              <div>
                <div class="action-title">导入华为 HDX 产品文档</div>
                <div class="action-desc">导入官方 HDX 压缩包或解压目录，知识库越全，匹配越准</div>
              </div>
            </button>
            <button type="button" class="action-item reset" @click="$router.push('/knowledge')">
              <el-icon class="action-icon tone-purple"><Search /></el-icon>
              <div>
                <div class="action-title">检索官方故障知识库</div>
                <div class="action-desc">按模块、助记符、Trap OID 或关键词检索处理指导</div>
              </div>
            </button>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <!-- 累计指标：降级为知识库概览 -->
    <el-card shadow="never" class="overview-card">
      <template #header>
        <div class="card-header">
          <div class="card-header-left">
            <el-icon><DataAnalysis /></el-icon>
            <span>知识库与累计使用概览</span>
          </div>
        </div>
      </template>
      <el-skeleton :loading="statsLoading" animated>
        <template #template>
          <el-row :gutter="16">
            <el-col v-for="i in 4" :key="i" :span="6">
              <el-skeleton-item variant="rect" style="height: 84px; border-radius: var(--radius-lg);" />
            </el-col>
          </el-row>
        </template>
        <template #default>
          <el-row :gutter="16">
            <el-col :span="6">
              <StatCard :icon="Reading" :value="stats.total_knowledge || 0" title="已收录故障知识条目" tone="brand" />
            </el-col>
            <el-col :span="6">
              <StatCard :icon="FolderOpened" :value="stats.total_documents || 0" title="已导入官方产品文档" tone="success" />
            </el-col>
            <el-col :span="6">
              <StatCard :icon="List" :value="stats.total_tasks || 0" title="累计排障案例" tone="warning" />
            </el-col>
            <el-col :span="6">
              <StatCard :icon="DataAnalysis" :value="stats.total_logs_analyzed || 0" title="累计解析日志总量" tone="purple" />
            </el-col>
          </el-row>
        </template>
      </el-skeleton>
    </el-card>

    <!-- 知识模块分布 -->
    <el-card shadow="hover" class="chart-card">
      <template #header>
        <div class="card-header">
          <div class="card-header-left">
            <el-icon><DataAnalysis /></el-icon>
            <span>知识库各网络模块分布 (Top 10)</span>
          </div>
        </div>
      </template>
      <template v-if="(stats.top_modules || []).length">
        <div ref="moduleChartRef" class="module-chart-box"></div>
      </template>
      <el-empty v-else description="知识库还没有数据，导入 HDX 文档后即可看到模块分布">
        <el-button type="primary" icon="FolderAdd" @click="$router.push('/documents')">去导入 HDX 文档</el-button>
      </el-empty>
    </el-card>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import {
  Reading, FolderOpened, List, DataAnalysis, Position, UploadFilled, FolderAdd, Search,
  Clock, Warning
} from '@element-plus/icons-vue'
// UI-09: 改用按需引入的 echarts 入口，避免把全量图表类型打进首屏
import echarts from '@/plugins/echarts'
import api from '@/api'
import StatCard from '@/components/StatCard.vue'
import TaskStatusTag from '@/components/TaskStatusTag.vue'
import OnboardingChecklist from '@/components/OnboardingChecklist.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import { formatTime } from '@/utils/format'

const router = useRouter()
const onboardingStore = useOnboardingStore()
const stats = ref({})
const statsLoading = ref(true)
const moduleChartRef = ref(null)
let chartInstance = null

const recentTasks = ref([])
const recentLoading = ref(false)
const statusCounts = ref({ processing: 0, failed: 0, pending: 0 })


const attentionText = computed(() => {
  const { processing, failed, pending } = statusCounts.value
  const parts = []
  if (processing > 0) parts.push(`${processing} 个案例分析中`)
  if (failed > 0) parts.push(`${failed} 个案例失败`)
  if (pending > 0) parts.push(`${pending} 个案例待导入日志`)
  return parts.length ? `需要关注：${parts.join('，')}` : ''
})

const fetchStats = async () => {
  statsLoading.value = true
  try {
    const res = await api.getStats()
    if (res.code === 0) {
      stats.value = res.data
      await nextTick()
      renderModuleChart(res.data.top_modules || [])
    }
  } catch (e) {
    // 错误由请求拦截器统一提示
  } finally {
    statsLoading.value = false
  }
}

const fetchRecentTasks = async () => {
  recentLoading.value = true
  try {
    const res = await api.queryTasks({ page: 1, page_size: 5, sort_by: 'start_time', order: 'desc' })
    if (res.code === 0) recentTasks.value = res.data?.items || []
  } finally {
    recentLoading.value = false
  }
}

const fetchStatusCounts = async () => {
  const fetchCount = async (status) => {
    try {
      const res = await api.queryTasks({ page: 1, page_size: 1, status, sort_by: 'start_time', order: 'desc' })
      return res.code === 0 ? (res.data?.total || 0) : 0
    } catch (e) {
      return 0
    }
  }
  const [processing, failed, pending] = await Promise.all([
    fetchCount('PROCESSING'),
    fetchCount('FAILED'),
    fetchCount('PENDING')
  ])
  statusCounts.value = { processing, failed, pending }
}

const renderModuleChart = (topModules) => {
  if (!moduleChartRef.value || !topModules.length) return
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

const openNewTask = () => {
  // 三个入口统一：进入工作台并打开同一套导入向导（规划 F1）
  router.push('/audit/new')
}

// WEB-07: 具名 + 防抖 + 成对移除的 resize 监听
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

onMounted(async () => {
  await Promise.all([fetchStats(), fetchRecentTasks(), fetchStatusCounts()])
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

.recent-card,
.action-card,
.overview-card,
.chart-card {
  border-radius: var(--radius-lg);
}

.action-card {
  height: 100%;
}

.card-header {
  font-weight: 600;
  font-size: 15px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
}

.card-header-left {
  display: flex;
  align-items: center;
  gap: 6px;
}

.module-chart-box {
  width: 100%;
  height: 320px;
}

.attention-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  margin-bottom: 10px;
  border-radius: var(--radius-base);
  background: var(--warning-bg, #fdf6ec);
  color: var(--warning-text, #b88230);
  font-size: var(--fs-sm);
}

.quick-actions {
  display: flex;
  flex-direction: column;
  gap: 14px;
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
  text-align: left;
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
