<template>
  <div class="report-center" v-loading="loadingFacets">
    <!-- 顶部操作栏 -->
    <div class="report-header-bar">
      <div class="header-left">
        <h3 class="report-title">分析报告</h3>
        <span class="report-subtitle">
          汇总当前任务的指标、关键发现与导出入口；完整证据链与结论章节在后续阶段补齐。
        </span>
      </div>
      <div class="header-right">
        <el-button icon="Refresh" @click="refresh">刷新</el-button>
        <el-button :icon="CopyDocument" @click="copySummary">复制交接摘要</el-button>
        <el-button :icon="Box" @click="exportPackage">导出证据包</el-button>
        <el-button type="success" icon="Download" :loading="exporting" @click="exportHTML">
          导出 HTML 报告
        </el-button>
      </div>
    </div>

    <!-- 指标看板 -->
    <div class="stats-grid">
      <StatCard :value="task?.log_count || 0" title="日志行数" tone="brand" />
      <StatCard :value="task?.device_count || 0" title="涉及设备" tone="success" />
      <StatCard :value="`${matchRate}%`" title="知识覆盖率" tone="purple" />
      <StatCard :value="task?.matched_count || 0" title="命中知识条数" tone="warning" />
    </div>

    <!-- 执行摘要 -->
    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header-title">
          <el-icon color="var(--brand-600)"><Document /></el-icon>
          <span>执行摘要</span>
        </div>
      </template>
      <div class="summary-text">{{ autoSummary }}</div>
      <div v-if="unmatchedCount > 0" class="summary-hint">
        <el-icon><Warning /></el-icon>
        <span>
          本次有 {{ unmatchedCount }} 行日志未命中官方知识（占 {{ unmatchedRate }}%），
          可在知识库中心查看未匹配模块，或在日志详情中补充本地知识。
        </span>
      </div>
    </el-card>

    <div class="report-columns">
      <!-- 关键发现 -->
      <el-card shadow="never" class="section-card">
        <template #header>
          <div class="card-header-title">
            <el-icon color="var(--warning-strong)"><Aim /></el-icon>
            <span>关键发现（按模块聚合 Top 5）</span>
          </div>
        </template>
        <div v-if="topModules.length === 0" class="empty-inline">
          暂无模块统计数据，导入日志后自动生成。
        </div>
        <div v-else class="module-list">
          <div v-for="item in topModules" :key="item.value" class="module-row">
            <div class="module-name">{{ item.value || '未知模块' }}</div>
            <div class="module-bar">
              <div class="module-bar-inner" :style="{ width: barWidth(item.count) }"></div>
            </div>
            <div class="module-count">{{ item.count }} 条</div>
          </div>
        </div>
      </el-card>

      <!-- 交接与导出 -->
      <el-card shadow="never" class="section-card">
        <template #header>
          <div class="card-header-title">
            <el-icon color="var(--success-strong)"><Download /></el-icon>
            <span>交接与导出</span>
          </div>
        </template>
        <div class="export-list">
          <div class="export-item">
            <div class="export-info">
              <strong>全量 HTML 报告</strong>
              <span>包含任务指标、RCA 摘要（实验开时）、日志明细表</span>
            </div>
            <el-button size="small" @click="exportHTML">导出</el-button>
          </div>
          <div class="export-item">
            <div class="export-info">
              <strong>全量 CSV 明细</strong>
              <span>按行导出全部日志，便于二次分析</span>
            </div>
            <el-button size="small" @click="exportCSV">导出</el-button>
          </div>
          <div class="export-item">
            <div class="export-info">
              <strong>当前筛选结果</strong>
              <span>沿用工作台左栏筛选条件导出子集</span>
            </div>
            <el-button size="small" @click="exportFiltered">导出</el-button>
          </div>
        </div>
      </el-card>
    </div>

    <!-- 多设备对比报告（原一级视图，现合并到分析报告） -->
    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header-title between">
          <div class="title-left">
            <el-icon color="var(--brand-600)"><Monitor /></el-icon>
            <span>多设备对比诊断</span>
          </div>
          <el-button link type="primary" @click="showMultiDevice = !showMultiDevice">
            {{ showMultiDevice ? '收起' : '展开' }}
          </el-button>
        </div>
      </template>
      <p v-if="!showMultiDevice" class="muted-text">
        跨设备时序归并、共性故障与级联事件分析，可单独导出多设备 HTML 报告。
      </p>
      <MultiDeviceReport
        v-else
        :key="`multi-${taskId}`"
        :task-id="taskId"
        @jump-to-log="(id) => $emit('jump-to-log', id)"
      />
    </el-card>

    <!-- 方法与局限 -->
    <el-card shadow="never" class="section-card">
      <template #header>
        <div class="card-header-title">
          <el-icon><InfoFilled /></el-icon>
          <span>方法与局限</span>
        </div>
      </template>
      <ul class="limitation-list">
        <li>知识匹配基于华为官方 HDX 文档的四级匹配，未命中不代表日志无意义，只表示暂无对应官方条目。</li>
        <li>任务指标来自任务库实时聚合；导出文件包含原始日志内容，外发前请自行评估敏感性（本产品不做脱敏）。</li>
        <li>关联事件（实验性）默认不进入主报告，且其“参考排查建议”并非官方原文。</li>
      </ul>
    </el-card>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Document, Aim, Download, Monitor, InfoFilled, Warning, CopyDocument, Box } from '@element-plus/icons-vue'
import StatCard from '@/components/StatCard.vue'
import MultiDeviceReport from '@/components/MultiDeviceReport.vue'
import api from '@/api'
import { useFilterStore } from '@/stores/filter'
import { formatTime } from '@/utils/format'

const props = defineProps({
  taskId: { type: String, default: '' },
  task: { type: Object, default: null }
})

defineEmits(['jump-to-log'])

const filterStore = useFilterStore()

const loadingFacets = ref(false)
const exporting = ref(false)
const showMultiDevice = ref(false)
const topModules = ref([])

const matchRate = computed(() => {
  const total = props.task?.log_count || 0
  if (!total) return '0.0'
  return (((props.task?.matched_count || 0) / total) * 100).toFixed(1)
})

const unmatchedCount = computed(() => {
  const total = props.task?.log_count || 0
  return Math.max(total - (props.task?.matched_count || 0), 0)
})

const unmatchedRate = computed(() => {
  const total = props.task?.log_count || 0
  if (!total) return '0.0'
  return ((unmatchedCount.value / total) * 100).toFixed(1)
})

const autoSummary = computed(() => {
  const task = props.task
  if (!task) return '请选择任务后查看报告。'
  const range = task.start_time
    ? `${formatTime(task.start_time)} 至 ${formatTime(task.finish_time, '进行中')}`
    : '时间范围未知'
  const parts = [
    `任务「${task.task_name}」共导入 ${task.log_count || 0} 行日志，涉及 ${task.device_count || 0} 台设备，时间范围 ${range}。`,
    `其中 ${task.matched_count || 0} 行命中官方知识库，知识覆盖率 ${matchRate.value}%。`
  ]
  const top = topModules.value[0]
  if (top && top.value) {
    parts.push(`出现频次最高的模块为 ${top.value}（${top.count} 条）。`)
  }
  if (unmatchedCount.value > 0) {
    parts.push(`仍有 ${unmatchedCount.value} 行日志暂无官方知识，建议在知识库中心补充或绑定本地知识。`)
  }
  return parts.join('')
})

const maxCount = computed(() => Math.max(...topModules.value.map((m) => m.count), 1))
const barWidth = (count) => `${Math.max((count / maxCount.value) * 100, 4)}%`

const fetchFacets = async () => {
  if (!props.taskId) return
  loadingFacets.value = true
  try {
    const res = await api.getTaskFacets(props.taskId, 'module')
    const list = Array.isArray(res?.data) ? res.data : []
    topModules.value = list
      .filter((item) => item && item.value)
      .sort((a, b) => (b.count || 0) - (a.count || 0))
      .slice(0, 5)
  } catch (e) {
    console.error('Fetch report facets failed:', e)
    topModules.value = []
  } finally {
    loadingFacets.value = false
  }
}

const copySummary = async () => {
  if (!props.taskId) return
  try {
    const markdown = await api.getSummaryMarkdown(props.taskId)
    if (!markdown) {
      ElMessage.warning('摘要内容为空')
      return
    }
    await navigator.clipboard.writeText(markdown)
    ElMessage.success('交接摘要已复制到剪贴板')
  } catch (e) {
    ElMessage.warning('复制失败，请检查浏览器剪贴板权限')
  }
}

const exportPackage = async () => {
  if (!props.taskId || exporting.value) return
  exporting.value = true
  try {
    await api.downloadEvidencePackage(props.taskId, props.task?.task_name)
    ElMessage.success('证据包已导出')
  } finally {
    exporting.value = false
  }
}

const exportHTML = async () => {
  if (!props.taskId || exporting.value) return
  exporting.value = true
  try {
    await api.downloadTaskReport(props.taskId, 'html')
    ElMessage.success('已导出 HTML 报告')
  } finally {
    exporting.value = false
  }
}

const exportCSV = async () => {
  if (!props.taskId || exporting.value) return
  exporting.value = true
  try {
    await api.downloadTaskReport(props.taskId, 'csv')
    ElMessage.success('已导出 CSV 明细')
  } finally {
    exporting.value = false
  }
}

const exportFiltered = async () => {
  if (!props.taskId || exporting.value) return
  exporting.value = true
  try {
    await api.downloadTaskReport(props.taskId, 'csv', filterStore.toLogQueryBody())
    ElMessage.success('已导出当前筛选结果')
  } finally {
    exporting.value = false
  }
}

const refresh = async () => {
  await fetchFacets()
}

onMounted(fetchFacets)
watch(() => props.taskId, fetchFacets)

defineExpose({ refresh })
</script>

<style scoped>
.report-center {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 4px;
}

.report-header-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.report-title {
  margin: 0;
  font-size: var(--fs-xl);
  color: var(--text-primary);
}

.report-subtitle {
  margin-left: 12px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 12px;
}

.section-card :deep(.el-card__header) {
  padding: 10px 16px;
}

.card-header-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.card-header-title.between {
  justify-content: space-between;
}

.title-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.summary-text {
  font-size: var(--fs-md);
  line-height: 1.9;
  color: var(--text-primary);
}

.summary-hint {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  margin-top: 10px;
  padding: 8px 10px;
  border-radius: var(--radius-base);
  background: var(--warning-bg, #fdf6ec);
  color: var(--warning-text, #b88230);
  font-size: var(--fs-sm);
  line-height: 1.6;
}

.report-columns {
  display: grid;
  grid-template-columns: 1.2fr 1fr;
  gap: 16px;
}

@media (max-width: 1100px) {
  .report-columns {
    grid-template-columns: 1fr;
  }
}

.module-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.module-row {
  display: grid;
  grid-template-columns: 120px 1fr 70px;
  align-items: center;
  gap: 10px;
  font-size: var(--fs-sm);
}

.module-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
  color: var(--text-primary);
}

.module-bar {
  height: 8px;
  border-radius: 999px;
  background: var(--bg-subtle);
  overflow: hidden;
}

.module-bar-inner {
  height: 100%;
  border-radius: 999px;
  background: linear-gradient(90deg, var(--brand-500), var(--brand-300, #79bbff));
}

.module-count {
  text-align: right;
  color: var(--text-secondary);
}

.export-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.export-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding-bottom: 10px;
  border-bottom: 1px dashed var(--border-base);
}

.export-item:last-child {
  border-bottom: none;
  padding-bottom: 0;
}

.export-info {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.export-info span {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.empty-inline,
.muted-text {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.limitation-list {
  margin: 0;
  padding-left: 20px;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: 1.9;
}
</style>
