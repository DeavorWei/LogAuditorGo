<template>
  <div class="summary-page">
    <el-skeleton :loading="loading" animated :rows="8">
      <template #default>
        <el-result
          v-if="error"
          icon="error"
          title="诊断摘要加载失败"
          :sub-title="error"
        >
          <template #extra>
            <el-button type="primary" @click="fetchSummary">重试</el-button>
            <el-button @click="goWorkbench">返回工作台</el-button>
          </template>
        </el-result>

        <template v-else-if="summary">
          <!-- 头部 -->
          <el-card shadow="never" class="summary-header-card">
            <div class="summary-header">
              <div class="header-main">
                <div class="title-row">
                  <h2>{{ summary.task_name }}</h2>
                  <TaskStatusTag :status="summary.task_status" />
                  <el-tag size="small" :type="caseMeta.tagType">{{ caseMeta.label }}</el-tag>
                </div>
                <div class="meta-row">
                  <span>{{ summary.overview.log_count }} 行日志</span>
                  <span>{{ summary.overview.device_count }} 台设备</span>
                  <span>{{ summary.overview.file_count }} 个文件</span>
                  <span v-if="timeRange">{{ timeRange }}</span>
                </div>
              </div>
              <div class="header-actions">
                <el-button @click="goWorkbench">返回工作台</el-button>
                <el-button :icon="EditPen" @click="showConclusion = true">
                  {{ summary.conclusion ? '编辑结论' : '写结论' }}
                </el-button>
                <el-button type="primary" :icon="ArrowRight" @click="startInvestigation">开始逐条排查</el-button>
              </div>
            </div>
            <div v-if="summary.conclusion" class="conclusion-preview">
              <strong>当前结论：</strong>{{ summary.conclusion }}
            </div>
          </el-card>

          <!-- 概览指标 -->
          <div class="metric-grid">
            <StatCard :value="summary.overview.log_count" title="日志行数" tone="brand" />
            <StatCard :value="summary.overview.matched_count" title="命中官方知识" tone="success" />
            <StatCard :value="`${summary.overview.match_rate.toFixed(1)}%`" title="知识覆盖率" tone="purple" />
            <StatCard :value="`${summary.overview.parse_rate.toFixed(1)}%`" title="解析成功率" tone="warning" />
          </div>

          <div class="summary-columns">
            <!-- 匹配质量 -->
            <el-card shadow="never" class="section-card">
              <template #header>
                <div class="card-title"><el-icon><Aim /></el-icon><span>匹配质量分布</span></div>
              </template>
              <div v-if="summary.match_quality.length === 0" class="muted">暂无数据</div>
              <div v-for="item in summary.match_quality" :key="item.tier" class="tier-row">
                <div class="tier-head">
                  <el-tag size="small" :type="tierMeta(item.tier).tagType">{{ tierMeta(item.tier).label }}</el-tag>
                  <span class="tier-count">{{ item.count }} 条 · {{ percent(item.count) }}</span>
                </div>
                <el-progress :percentage="percentNumber(item.count)" :show-text="false" :stroke-width="8" />
              </div>
            </el-card>

            <!-- 严重度分布 -->
            <el-card shadow="never" class="section-card">
              <template #header>
                <div class="card-title"><el-icon><Warning /></el-icon><span>严重度分布</span></div>
              </template>
              <div class="severity-grid">
                <div v-for="item in severityRows" :key="item.severity" class="severity-cell">
                  <div class="sev-level">{{ severityText(item.severity) }}</div>
                  <div class="sev-count">{{ item.count }}</div>
                </div>
              </div>
            </el-card>
          </div>

          <!-- Top 模块 -->
          <el-card shadow="never" class="section-card">
            <template #header>
              <div class="card-title"><el-icon><DataAnalysis /></el-icon><span>最值得看：高频模块 Top 5</span></div>
            </template>
            <el-table :data="summary.top_modules" size="small">
              <el-table-column prop="module" label="模块" min-width="160" />
              <el-table-column prop="count" label="条数" width="100" align="right" />
              <el-table-column label="最高级别" width="140">
                <template #default="{ row }"><SeverityTag :severity="row.max_severity" /></template>
              </el-table-column>
              <el-table-column label="操作" width="160" align="center">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="openModule(row.module)">查看日志</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-card>

          <!-- 未匹配热点 -->
          <el-card shadow="never" class="section-card">
            <template #header>
              <div class="card-title">
                <el-icon><Warning /></el-icon>
                <span>知识盲区：未匹配热点 Top 10</span>
                <el-tooltip content="未命中不代表日志无意义，只表示暂无对应官方条目" placement="top">
                  <el-icon class="hint-icon"><InfoFilled /></el-icon>
                </el-tooltip>
              </div>
            </template>
            <el-table v-if="summary.unmatched_hotspots.length" :data="summary.unmatched_hotspots" size="small">
              <el-table-column label="模块 / 助记符" min-width="220">
                <template #default="{ row }">
                  <strong>{{ row.module }}</strong>/{{ row.brief }}
                </template>
              </el-table-column>
              <el-table-column prop="count" label="次数" width="90" align="right" />
              <el-table-column label="首次出现" width="170">
                <template #default="{ row }">{{ formatTime(row.first_seen) }}</template>
              </el-table-column>
              <el-table-column label="最后出现" width="170">
                <template #default="{ row }">{{ formatTime(row.last_seen) }}</template>
              </el-table-column>
              <el-table-column label="操作" width="160" align="center">
                <template #default="{ row }">
                  <el-button link type="primary" size="small" @click="openUnmatched(row)">查看日志</el-button>
                </template>
              </el-table-column>
            </el-table>
            <el-empty v-else description="本任务没有未匹配日志" :image-size="70" />
          </el-card>

          <!-- 下一步建议 -->
          <el-card shadow="never" class="section-card suggestion-card">
            <template #header>
              <div class="card-title"><el-icon><Position /></el-icon><span>下一步建议</span></div>
            </template>
            <ol class="suggestion-list">
              <li v-for="(item, index) in summary.suggestions" :key="index">{{ item }}</li>
            </ol>
          </el-card>
        </template>
      </template>
    </el-skeleton>

    <ConclusionDrawer
      v-model="showConclusion"
      :task-id="taskId"
      :task="taskForDrawer"
      @saved="onConclusionSaved"
    />
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  Aim, ArrowRight, DataAnalysis, EditPen, InfoFilled, Position, Warning
} from '@element-plus/icons-vue'
import StatCard from '@/components/StatCard.vue'
import TaskStatusTag from '@/components/TaskStatusTag.vue'
import SeverityTag from '@/components/SeverityTag.vue'
import ConclusionDrawer from '@/components/workbench/ConclusionDrawer.vue'
import api from '@/api'
import { formatTime } from '@/utils/format'
import { matchTierMeta } from '@/constants/matchTiers'
import { caseStatusMeta } from '@/constants/caseStatus'

const route = useRoute()
const router = useRouter()

const taskId = computed(() => String(route.params.id || ''))
const loading = ref(false)
const error = ref('')
const summary = ref(null)
const showConclusion = ref(false)

const caseMeta = computed(() => caseStatusMeta(summary.value?.case_status))

const taskForDrawer = computed(() => ({
  ...(summary.value || {}),
  task_id: taskId.value,
  task_name: summary.value?.task_name || '',
  conclusion: summary.value?.conclusion || '',
  case_status: summary.value?.case_status || ''
}))

const timeRange = computed(() => {
  const range = summary.value?.overview
  if (!range?.time_start || !range?.time_end) return ''
  return `${formatTime(range.time_start)} ~ ${formatTime(range.time_end)}`
})

const tierMeta = (tier) => matchTierMeta(tier)

const percentNumber = (count) => {
  const total = summary.value?.overview?.log_count || 0
  if (!total) return 0
  return Math.round((count / total) * 1000) / 10
}
const percent = (count) => `${percentNumber(count)}%`

const severityRows = computed(() => {
  const map = new Map((summary.value?.severity_distribution || []).map((item) => [item.severity, item.count]))
  return Array.from({ length: 9 }, (_, severity) => ({ severity, count: map.get(severity) || 0 }))
})

function severityText(severity) {
  if (severity <= 2) return `严重 Lv.${severity}`
  if (severity <= 4) return `错误 Lv.${severity}`
  if (severity <= 5) return `警告 Lv.${severity}`
  return `提示 Lv.${severity}`
}

const fetchSummary = async () => {
  if (!taskId.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await api.getTaskSummary(taskId.value)
    if (res.code === 0) {
      summary.value = res.data
    } else {
      error.value = res.message || '加载失败'
    }
  } catch (e) {
    error.value = e?.message || '网络异常'
  } finally {
    loading.value = false
  }
}

const goWorkbench = () => router.push(`/audit/${taskId.value}`)

const startInvestigation = () => {
  const query = {}
  if (summary.value?.first_high_log_id) query.log = summary.value.first_high_log_id
  router.push({ path: `/audit/${taskId.value}`, query })
}

const openModule = (module) => {
  router.push({ path: `/audit/${taskId.value}`, query: { module } })
}

const openUnmatched = (row) => {
  router.push({ path: `/audit/${taskId.value}`, query: { module: row.module, matched: 'false' } })
}

const onConclusionSaved = async () => {
  ElMessage.success('结论已保存')
  await fetchSummary()
}

onMounted(fetchSummary)
watch(taskId, fetchSummary)
</script>

<style scoped>
.summary-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.summary-header-card,
.section-card {
  border-radius: var(--radius-lg);
}

.summary-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.title-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.title-row h2 {
  margin: 0;
  font-size: var(--fs-xl);
  color: var(--text-primary);
}

.meta-row {
  display: flex;
  gap: 14px;
  margin-top: 8px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  flex-wrap: wrap;
}

.header-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.conclusion-preview {
  margin-top: 14px;
  padding: 10px 12px;
  border-radius: var(--radius-base);
  background: var(--bg-subtle);
  font-size: var(--fs-sm);
  line-height: 1.7;
  color: var(--text-primary);
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 12px;
}

.summary-columns {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

@media (max-width: 1100px) {
  .summary-columns {
    grid-template-columns: 1fr;
  }
}

.card-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.hint-icon {
  color: var(--text-secondary);
}

.tier-row {
  margin-bottom: 12px;
}

.tier-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}

.tier-count {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.severity-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(90px, 1fr));
  gap: 8px;
}

.severity-cell {
  background: var(--bg-subtle);
  border-radius: var(--radius-base);
  padding: 8px;
  text-align: center;
}

.sev-level {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.sev-count {
  margin-top: 4px;
  font-size: var(--fs-lg);
  font-weight: 700;
  color: var(--text-primary);
}

.suggestion-list {
  margin: 0;
  padding-left: 20px;
  line-height: 2;
  font-size: var(--fs-md);
  color: var(--text-primary);
}

.muted {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
