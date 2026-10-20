<template>
  <div class="rca-center-container" v-loading="loading">
    <!-- 实验性能力免责声明（规划 §5.5：只保留、不演进） -->
    <el-alert
      type="warning"
      show-icon
      :closable="false"
      class="experimental-banner"
    >
      <template #title>
        <span class="alert-title-strong">关联事件（实验性）· 只保留、不演进</span>
      </template>
      <template #default>
        <div class="experimental-body">
          <span>
            当前仅内置 5 条协议故障传播规则，按设备隔离分析，结果可能误报或漏报；
            页面中的“疑似根因/关联起点”与“参考排查建议”均为启发式推断，
            <strong>不是官方文档原文</strong>，请结合官方知识库与现场信息判断。
          </span>
        </div>
      </template>
    </el-alert>

    <!-- 顶部 KPI 概览看板 -->
    <div class="rca-overview-bar">
      <div class="bar-left">
        <div class="kpi-item">
          <span class="kpi-val text-primary">{{ rcaList.length }}</span>
          <span class="kpi-label">识别关联事件</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-danger">{{ countByLevel('CRITICAL') }}</span>
          <span class="kpi-label">严重级</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-warning">{{ countByLevel('HIGH') }}</span>
          <span class="kpi-label">高级</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-success">{{ totalCorrelatedLogs }}</span>
          <span class="kpi-label">覆盖关联日志</span>
        </div>
      </div>
      <div class="bar-right">
        <el-button-group>
          <el-button icon="Refresh" @click="fetchTaskAndRCA">刷新关联事件</el-button>
          <el-button
            type="warning"
            plain
            icon="Opportunity"
            :loading="reanalyzing"
            :disabled="isRcaRunning"
            @click="handleReanalyzeRCA"
          >
            分析关联事件
          </el-button>
        </el-button-group>
      </div>
    </div>

    <!-- 超时截断警示栏 -->
    <el-alert
      v-if="taskInfo?.rca_status === 'TIMEOUT'"
      type="warning"
      show-icon
      :closable="false"
      class="timeout-alert-bar"
    >
      <template #title>
        <span class="alert-title-strong">关联事件分析达到超时上限（已保存部分结果）</span>
      </template>
      <template #default>
        <div class="alert-action-body">
          <span>
            由于分析样本极大，本次分析在达到超时上限时安全中止。当前拓扑仅包含至
            <strong>{{ rcaAnalyzedUntilText }}</strong> 前的故障链路，后续时段可能存在未识别事件。
          </span>
          <el-button type="warning" size="small" plain @click="handleReanalyzeRCA" :loading="reanalyzing">
            重新分析
          </el-button>
        </div>
      </template>
    </el-alert>

    <!-- 失败状态提示栏 -->
    <el-alert
      v-else-if="taskInfo?.rca_status === 'FAILED'"
      type="error"
      show-icon
      :closable="false"
      class="timeout-alert-bar"
    >
      <template #title>
        <span class="alert-title-strong">关联事件分析异常中断</span>
      </template>
      <template #default>
        <div class="alert-action-body">
          <span>{{ taskInfo.rca_error_message || '计算过程发生异常' }}</span>
          <el-button type="danger" size="small" plain @click="handleReanalyzeRCA" :loading="reanalyzing">
            点击重试
          </el-button>
        </div>
      </template>
    </el-alert>

    <!-- 重新计算中的顶部轻量浮层警示条 (已有历史事件) -->
    <el-alert
      v-if="isRcaRunning && rcaList.length > 0"
      type="info"
      show-icon
      :closable="false"
      class="timeout-alert-bar"
    >
      <template #title>
        <span class="alert-title-strong">关联事件正在后台重新分析中...</span>
      </template>
      <template #default>
        <span>
          当前展示为上一轮分析结果。系统正在后台重新推导因果拓扑，计算完成后将自动刷新展示最新结果。
        </span>
      </template>
    </el-alert>

    <!-- 首次计算中的骨架屏 (无历史事件) -->
    <el-card v-if="isRcaRunning && rcaList.length === 0" shadow="never" class="running-card">
      <div class="running-header">
        <el-icon class="is-loading" size="44" color="var(--brand-600)"><Loading /></el-icon>
        <h3 class="running-title">关联事件正在后台分析中...</h3>
        <p v-if="taskInfo?.rca_status === 'QUEUED'" class="running-tip">
          当前任务正在排队等待并发计算槽位，工作台已就绪可正常审计浏览日志。
        </p>
        <p v-else class="running-tip">
          正在基于全量时序关联与协议故障传播模型计算因果拓扑，完成后将自动刷新展示。
        </p>
      </div>
      <el-skeleton :rows="7" animated class="running-skeleton" />
    </el-card>

    <!-- 无联动事件空状态 -->
    <el-card v-else-if="!loading && !isRcaRunning && rcaList.length === 0" shadow="never" class="empty-card">
      <el-empty description="当前任务尚未检测到协议级关联事件">
        <p class="empty-tip">
          关联事件引擎会在 300s 滑动时间窗口内匹配内置的协议故障传播规则（如：物理口 Down → BFD 超时 → 路由邻居断开 → 路由撤销）。该能力为实验性，仅作排查线索参考。
        </p>
      </el-empty>
    </el-card>

    <!-- 核心两栏联动分析工作台 -->
    <div v-else-if="rcaList.length > 0" class="rca-workbench-body">
      <!-- 左栏：联动事件列表 (340px) -->
      <div class="col-event-list">
        <div class="list-header">
          <span>关联事件索引 ({{ filteredRcaList.length }})</span>
          <el-select v-model="levelFilter" size="small" class="level-filter-select" placeholder="级别筛选">
            <el-option
              v-for="opt in IMPACT_LEVEL_OPTIONS"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </div>

        <div class="event-cards-scroll">
          <div
            v-for="ev in filteredRcaList"
            :key="ev.id"
            :class="['rca-item-card', { active: selectedRCA?.id === ev.id }]"
            role="button"
            tabindex="0"
            @click="selectRCA(ev)"
            @keydown.enter.space.prevent="selectRCA(ev)"
          >
            <div class="item-card-header">
              <span :class="['level-tag', (ev.impact_level || 'HIGH').toLowerCase()]">
                {{ impactLevelLabel(ev.impact_level) }}
              </span>
              <span class="confidence-tag">
                证据强度 {{ formatRcaConfidence(ev.confidence) }}
              </span>
            </div>

            <div class="item-card-title">
              <strong>[{{ ev.root_module }}/{{ ev.root_brief }}]</strong>
              <span>{{ ev.root_cause_summary || '协议级连环故障联动' }}</span>
            </div>

            <div class="item-card-meta">
              <span><el-icon><Clock /></el-icon> {{ ev.root_timestamp }}</span>
              <span v-if="ev.root_device_name" class="device-pill" :style="{ backgroundColor: ev.root_device_color || 'var(--brand-500)' }">
                {{ ev.root_device_name }}
              </span>
            </div>

            <!-- 传播链简略预览 -->
            <div v-if="ev.modules_involved && ev.modules_involved.length > 0" class="item-chain-preview">
              <span v-for="(m, idx) in ev.modules_involved" :key="m" class="chain-mod-item">
                {{ m }}<span v-if="idx < ev.modules_involved.length - 1" class="chain-arrow">→</span>
              </span>
              <span class="chain-count">({{ ev.correlated_count }}条衍生)</span>
            </div>
          </div>
        </div>
      </div>

      <!-- 右栏：故障因果全景工作台 (弹性伸缩) -->
      <div v-if="selectedRCA" class="col-event-detail">
        <el-card shadow="never" class="detail-card">
          <!-- 头部标题栏 -->
          <div class="detail-header-row">
            <div>
              <div class="rca-header-title-box">
                <span :class="['level-tag', (selectedRCA.impact_level || 'HIGH').toLowerCase()]">
                  {{ impactLevelLabel(selectedRCA.impact_level) }}
                </span>
                <h3 class="rca-root-title">
                  [{{ selectedRCA.root_module }}/{{ selectedRCA.root_brief }}] 疑似根因 / 关联起点
                </h3>
              </div>
              <p class="rca-root-subtitle">
                始发于 {{ selectedRCA.root_timestamp }}，关联 {{ selectedRCA.correlated_count }} 条衍生日志，证据强度 {{ formatRcaConfidence(selectedRCA.confidence) }}（启发式推断，非官方结论）
              </p>
            </div>

            <div>
              <el-button type="primary" plain size="small" icon="Position" @click="jumpToAuditStream(selectedRCA.root_log_id)">
                在日志流中定位
              </el-button>
            </div>
          </div>

          <!-- 子 Tab: 拓扑图、时序链明细、处置指南 -->
          <el-tabs v-model="activeDetailTab" class="detail-tabs">
            <!-- Tab 1: 关联拓扑图 -->
            <el-tab-pane label="关联拓扑 (DAG)" name="dag">
              <div class="graph-box">
                <RcaGraph :rcaEvent="selectedRCA" />
              </div>
            </el-tab-pane>

            <!-- Tab 2: 关联时序明细 -->
            <el-tab-pane label="关联时序明细" name="timeline">
              <div class="cascade-timeline-wrap">
                <div class="step-card root-step">
                  <div class="step-badge root">
                    <span>1. 关联起点</span>
                  </div>
                  <div class="step-content">
                    <div class="step-header">
                      <div class="step-header-left">
                        <span class="time-text">{{ selectedRCA.root_timestamp }}</span>
                        <span class="delay-tag zero">+0ms (起点)</span>
                        <span v-if="selectedRCA.root_device_name" class="device-pill" :style="{ backgroundColor: selectedRCA.root_device_color || 'var(--brand-500)' }">
                          {{ selectedRCA.root_device_name }}
                        </span>
                        <strong class="mod-brief">{{ selectedRCA.root_module }}/{{ selectedRCA.root_brief }}</strong>
                      </div>
                      <div class="step-header-right">
                        <span v-if="selectedRCA.root_hostname" class="code-tag">{{ selectedRCA.root_hostname }}</span>
                        <el-button type="primary" link size="small" @click="jumpToAuditStream(selectedRCA.root_log_id)">查看日志</el-button>
                      </div>
                    </div>

                    <div v-if="selectedRCA.root_log" class="step-raw-log">
                      {{ selectedRCA.root_log.raw_log }}
                    </div>

                    <div v-if="selectedRCA.root_parameters && Object.keys(selectedRCA.root_parameters).length > 0" class="step-params">
                      <span v-for="(v, k) in selectedRCA.root_parameters" :key="k" class="param-badge">
                        <strong>{{ k }}:</strong> {{ v }}
                      </span>
                    </div>
                  </div>
                </div>

                <!-- 衍生事件阶梯 -->
                <div
                  v-for="(impact, idx) in selectedRCA.impact_details"
                  :key="impact.log_id || idx"
                  class="step-card impact-step"
                >
                  <div class="step-badge impact">
                    <span>{{ idx + 2 }}. 关联日志</span>
                  </div>
                  <div class="step-content">
                    <div class="step-header">
                      <div class="step-header-left">
                        <span class="time-text">{{ impact.timestamp }}</span>
                        <span class="delay-tag">+{{ impact.delay_ms }}ms</span>
                        <span v-if="impact.device_name" class="device-pill" :style="{ backgroundColor: impact.device_color || 'var(--brand-500)' }">
                          {{ impact.device_name }}
                        </span>
                        <strong class="mod-brief">{{ impact.module }}/{{ impact.brief }}</strong>
                      </div>
                      <div class="step-header-right">
                        <span v-if="impact.hostname" class="code-tag">{{ impact.hostname }}</span>
                        <el-button type="primary" link size="small" @click="jumpToAuditStream(impact.log_id)">查看日志</el-button>
                      </div>
                    </div>

                    <div class="step-raw-log">
                      {{ impact.raw_log || `[${impact.module}/${impact.brief}] 关联日志事件` }}
                    </div>

                    <div v-if="impact.parameters && Object.keys(impact.parameters).length > 0" class="step-params">
                      <span v-for="(v, k) in impact.parameters" :key="k" class="param-badge">
                        <strong>{{ k }}:</strong> {{ v }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </el-tab-pane>

            <!-- Tab 3: 参考排查建议 -->
            <el-tab-pane label="参考排查建议" name="guide">
              <div class="guide-content-box">
                <div class="guide-block">
                  <h4><el-icon><WarningFilled /></el-icon> 关联事件综述（启发式）</h4>
                  <div class="summary-text">{{ selectedRCA.root_cause_summary }}</div>
                </div>

                <div class="guide-block">
                  <h4><el-icon><Tools /></el-icon> 参考排查建议（非官方原文）</h4>
                  <div class="action-text">{{ selectedRCA.recommended_action || '请检查关联起点设备的接口物理状态与协议配置。' }}</div>
                </div>
              </div>
            </el-tab-pane>
          </el-tabs>
        </el-card>
      </div>
    </div>
  </div>
</template>

<script setup>
// UI-16: defineProps / defineEmits 是编译器宏，无需从 vue 导入
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { Position, Loading, Opportunity, Refresh, Clock, WarningFilled, Tools } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import RcaGraph from '@/components/RcaGraph.vue'
import { formatRcaConfidence, impactLevelLabel, IMPACT_LEVEL_OPTIONS } from '@/utils/rca'
import { useRequest } from '@/composables/useRequest'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['jump-to-log', 'task-updated'])

const loading = ref(false)
const reanalyzing = ref(false)
const taskInfo = ref(null)
const rcaList = ref([])
const selectedRCA = ref(null)
const levelFilter = ref('ALL')
const activeDetailTab = ref('dag')

const formatDateTime = (val) => {
  if (!val) return ''
  const d = new Date(val)
  if (isNaN(d.getTime())) return String(val)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

const isRcaRunning = computed(() => {
  const st = taskInfo.value?.rca_status
  return st === 'QUEUED' || st === 'RUNNING'
})

const rcaAnalyzedUntilText = computed(() => {
  if (!taskInfo.value?.rca_analyzed_until) return '未知时序边界'
  return formatDateTime(taskInfo.value.rca_analyzed_until)
})

const totalCorrelatedLogs = computed(() => {
  return rcaList.value.reduce((acc, cur) => acc + (cur.correlated_count || 0), 0)
})

const countByLevel = (level) => {
  return rcaList.value.filter(e => (e.impact_level || 'HIGH').toUpperCase() === level).length
}

const filteredRcaList = computed(() => {
  if (levelFilter.value === 'ALL') return rcaList.value
  return rcaList.value.filter(e => (e.impact_level || 'HIGH').toUpperCase() === levelFilter.value)
})

/**
 * WEB-08: 用 useRequest 包裹 RCA 列表请求。
 *
 * 原实现没有任何取消机制：父组件 refresh 与 taskId 切换可能并发触发两次拉取，
 * 慢请求后返回会覆盖新任务的 RCA 结果，用户看到的根因链与当前任务不符。
 */
// 失败提示由 api 拦截器统一弹出，这里不再重复 toast
const { run: runFetchRCA } = useRequest(api.getTaskRCA)

let pollTimer = null
let pollFailCount = 0

const stopPolling = () => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  pollFailCount = 0
}

const startPolling = () => {
  stopPolling()
  pollTimer = setInterval(async () => {
    await loadTaskInfo()
    if (!isRcaRunning.value) {
      stopPolling()
      fetchRCAEvents()
    }
  }, 3000)
}

const loadTaskInfo = async () => {
  const reqTaskId = props.taskId
  if (!reqTaskId) return
  try {
    const res = await api.getTask(reqTaskId)
    pollFailCount = 0
    if (reqTaskId !== props.taskId) return
    if (res && res.code === 0) {
      taskInfo.value = res.data
      emit('task-updated', res.data)
    }
  } catch (e) {
    pollFailCount++
    if (pollFailCount >= 5) {
      stopPolling()
      ElMessage.warning('轮询任务状态失败次数过多，已暂停自动轮询')
    }
  }
}

const fetchRCAEvents = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const res = await runFetchRCA(props.taskId)
    if (!res || res.code !== 0) return
    rcaList.value = res.data || []
    if (rcaList.value.length > 0) {
      if (!selectedRCA.value || !rcaList.value.find(e => e.id === selectedRCA.value.id)) {
        selectedRCA.value = rcaList.value[0]
      } else {
        selectedRCA.value = rcaList.value.find(e => e.id === selectedRCA.value.id)
      }
    } else {
      selectedRCA.value = null
    }
  } finally {
    loading.value = false
  }
}

const fetchTaskAndRCA = async () => {
  await loadTaskInfo()
  if (isRcaRunning.value) {
    startPolling()
  } else {
    stopPolling()
  }
  await fetchRCAEvents()
}

const handleReanalyzeRCA = async () => {
  if (!props.taskId) return
  reanalyzing.value = true
  try {
    const res = await api.reanalyzeTaskRCA(props.taskId)
    if (res && res.code === 0) {
      ElMessage.success('已触发关联事件分析，后台排队计算中')
      await loadTaskInfo()
      startPolling()
    }
  } catch (err) {
    ElMessage.error(err.message || '关联事件分析失败')
  } finally {
    reanalyzing.value = false
  }
}

const selectRCA = (ev) => {
  selectedRCA.value = ev
}

/**
 * UI-13: 级别筛选后校正选中项。
 *
 * 原实现只过滤左栏列表，右栏详情仍指向被过滤掉的事件，
 * 用户会看到"左栏没有这一条，右栏却在展示它"的脱节状态。
 * 这里在筛选结果变化时自动校正：选中项若不在结果内则切到首条（或清空）。
 */
watch(filteredRcaList, (list) => {
  if (!list || list.length === 0) {
    selectedRCA.value = null
    return
  }
  const stillVisible =
    selectedRCA.value && list.some((e) => e.id === selectedRCA.value.id)
  if (!stillVisible) {
    selectedRCA.value = list[0]
  }
})

const jumpToAuditStream = (logId) => {
  if (logId) {
    emit('jump-to-log', logId)
  }
}

watch(() => props.taskId, (newVal) => {
  if (newVal) {
    fetchTaskAndRCA()
  }
})

onMounted(() => {
  fetchTaskAndRCA()
})

onUnmounted(() => {
  stopPolling()
})

defineExpose({
  refresh: fetchTaskAndRCA
})
</script>

<style scoped>
.rca-center-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.experimental-banner {
  border-radius: var(--radius-lg);
}

.experimental-body {
  font-size: var(--fs-sm);
  line-height: 1.6;
}
.rca-overview-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--gray-0);
  padding: 14px 20px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border-base);
}
.bar-left {
  display: flex;
  align-items: center;
  gap: 24px;
}
.kpi-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.kpi-val {
  font-size: 20px;
  font-weight: 700;
}
.kpi-label {
  font-size: var(--fs-sm);
  color: var(--gray-500);
}
.kpi-divider {
  width: 1px;
  height: 28px;
  background: var(--border-base);
}
.text-primary { color: var(--brand-600); }
.text-danger { color: var(--danger-strong); }
.text-warning { color: var(--sev-err-bar); }
.text-success { color: var(--success-strong); }

.timeout-alert-bar {
  margin-bottom: 2px;
  border-radius: var(--radius-lg);
}
.running-card {
  border-radius: var(--radius-lg);
  padding: 40px 30px;
  text-align: center;
}
.running-header {
  display: flex;
  flex-direction: column;
  align-items: center;
}
.running-tip {
  max-width: 540px;
  font-size: var(--fs-md);
  color: var(--gray-500);
  line-height: 1.6;
  margin-top: 4px;
}

.empty-card {
  border-radius: var(--radius-lg);
  padding: 30px;
}
.empty-tip {
  max-width: 540px;
  font-size: var(--fs-md);
  color: var(--gray-500);
  line-height: 1.6;
  margin-top: 8px;
}

.rca-workbench-body {
  display: flex;
  gap: 16px;
  min-height: calc(100vh - var(--app-header-h) - var(--app-main-pad-y) * 2 - 138px);
}
.col-event-list {
  width: 360px;
  flex-shrink: 0;
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border-base);
  display: flex;
  flex-direction: column;
}
.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid var(--gray-100);
  font-weight: 600;
  font-size: var(--fs-lg);
  color: var(--gray-900);
}
.event-cards-scroll {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.rca-item-card {
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 12px;
  cursor: pointer;
  background: var(--bg-subtle);
  transition: all 0.2s ease;
}
.rca-item-card:hover {
  border-color: var(--brand-500);
  background: var(--gray-0);
  box-shadow: 0 2px 4px rgba(0,0,0,0.04);
}
.rca-item-card.active {
  border-color: var(--brand-600);
  background: var(--brand-50);
  border-left: 4px solid var(--brand-600);
}
.item-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}
.level-tag {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 1px var(--sp-2);
  border-radius: var(--radius-xs);
  font-size: var(--fs-xs);
  font-weight: 700;
  text-transform: uppercase;
}
.level-tag.critical { background: var(--sev-crit-bg); color: var(--sev-crit-text); border: 1px solid var(--sev-crit-border); }
.level-tag.high { background: var(--sev-err-bg); color: var(--sev-err-text); border: 1px solid var(--sev-err-border); }
.level-tag.medium { background: var(--sev-warn-bg); color: var(--sev-warn-text); border: 1px solid var(--sev-warn-border); }
.confidence-tag {
  font-size: var(--fs-xs);
  color: var(--action-text);
  background: var(--inst-injected-bg);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  font-weight: 600;
}
.item-card-title {
  font-size: var(--fs-md);
  color: var(--gray-800);
  line-height: 1.4;
  margin-bottom: 6px;
}
.item-card-title strong {
  color: var(--brand-700);
  margin-right: 4px;
}
.item-card-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: var(--fs-xs);
  color: var(--gray-500);
}
.device-pill {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: var(--fs-xs);
  font-weight: 600;
  color: var(--gray-0);
}
.item-chain-preview {
  margin-top: 8px;
  padding-top: 6px;
  border-top: 1px dashed var(--border-base);
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px;
  font-size: var(--fs-xs);
}
.chain-mod-item {
  color: var(--gray-600);
  font-weight: 600;
}
.chain-arrow {
  color: var(--gray-400);
  margin: 0 2px;
}
.chain-count {
  color: var(--sev-err-bar);
  margin-left: 4px;
}

.col-event-detail {
  flex: 1;
  min-width: 0;
}
.detail-card {
  border-radius: var(--radius-lg);
  height: 100%;
}
.detail-header-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-base);
}
.detail-tabs {
  margin-top: 12px;
}
.graph-box {
  min-height: 480px;
}

.cascade-timeline-wrap {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 10px 0;
}
.step-card {
  display: flex;
  gap: 12px;
  border: 1px solid var(--border-base);
  border-radius: var(--radius-lg);
  padding: 12px 16px;
  background: var(--gray-0);
}
.step-card.root-step {
  border-color: var(--danger-border);
  background: var(--danger-bg);
  border-left: 4px solid var(--danger-strong);
}
.step-card.impact-step {
  border-left: 4px solid var(--sev-err-bar);
}
.step-badge {
  flex-shrink: 0;
  width: 100px;
  font-size: var(--fs-sm);
  font-weight: 700;
}
.step-badge.root { color: var(--danger-strong); }
.step-badge.impact { color: var(--sev-err-bar); }

.step-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.step-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.step-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.time-text {
  font-family: monospace;
  font-size: var(--fs-sm);
  color: var(--gray-700);
}
.delay-tag {
  background: var(--sev-err-bg);
  color: var(--sev-err-text);
  font-size: var(--fs-xs);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  font-weight: 600;
}
.delay-tag.zero {
  background: var(--sev-crit-bg);
  color: var(--danger-text);
}
.mod-brief {
  font-size: var(--fs-md);
  color: var(--gray-900);
}
.code-tag {
  font-family: monospace;
  font-size: var(--fs-sm);
  background: var(--gray-100);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  color: var(--gray-600);
}
.step-raw-log {
  font-family: monospace;
  font-size: var(--fs-sm);
  color: var(--gray-700);
  background: var(--bg-subtle);
  border: 1px solid var(--gray-100);
  padding: 8px 12px;
  border-radius: var(--radius-sm);
  word-break: break-all;
}
.step-params {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.param-badge {
  background: var(--brand-50);
  border: 1px solid var(--brand-100);
  color: var(--brand-800);
  font-size: var(--fs-xs);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
}

.guide-content-box {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 10px 0;
}
.guide-block h4 {
  font-size: var(--fs-lg);
  font-weight: 600;
  color: var(--gray-900);
  margin-bottom: 8px;
}
.summary-text {
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 12px 16px;
  font-size: var(--fs-md);
  color: var(--gray-700);
  line-height: 1.6;
  white-space: pre-line;
  word-break: break-word;
}
.action-text {
  background: var(--brand-50);
  border: 1px solid var(--brand-200);
  border-radius: var(--radius-md);
  padding: 14px 16px;
  font-size: var(--fs-md);
  color: var(--brand-800);
  line-height: 1.6;
  white-space: pre-line;
  word-break: break-word;
}
.alert-title-strong {
  font-weight: 600;
}
.alert-action-body {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}
.running-title {
  margin: 12px 0 6px 0;
  color: var(--gray-900);
}
.rca-header-title-box {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.rca-root-title {
  margin: 0;
  font-size: var(--fs-xl);
  color: var(--gray-900);
}
.rca-root-subtitle {
  margin: 0;
  font-size: var(--fs-md);
  color: var(--text-secondary);
}

.running-skeleton {
  margin-top: 24px;
}

.level-filter-select {
  width: 110px;
}
</style>
