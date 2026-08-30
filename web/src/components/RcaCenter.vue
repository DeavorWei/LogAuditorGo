<template>
  <div class="rca-center-container" v-loading="loading">
    <!-- 顶部 KPI 概览看板 -->
    <div class="rca-overview-bar">
      <div class="bar-left">
        <div class="kpi-item">
          <span class="kpi-val text-primary">{{ rcaList.length }}</span>
          <span class="kpi-label">识别联动故障事件</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-danger">{{ countByLevel('CRITICAL') }}</span>
          <span class="kpi-label">CRITICAL 紧急级</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-warning">{{ countByLevel('HIGH') }}</span>
          <span class="kpi-label">HIGH 高危级</span>
        </div>
        <div class="kpi-divider"></div>
        <div class="kpi-item">
          <span class="kpi-val text-success">{{ totalCorrelatedLogs }}</span>
          <span class="kpi-label">覆盖关联衍生日志</span>
        </div>
      </div>
      <div class="bar-right">
        <el-button-group>
          <el-button icon="Refresh" @click="fetchRCAEvents">刷新联动分析</el-button>
        </el-button-group>
      </div>
    </div>

    <!-- 无联动事件空状态 -->
    <el-card v-if="!loading && rcaList.length === 0" shadow="never" class="empty-card">
      <el-empty description="当前任务尚未检测到协议级故障联动事件">
        <template #extra>
          <p class="empty-tip">
            RCA 根因分析引擎会自动在 300s 滑动时间窗口内匹配网络协议故障传播有向图 (如: 物理口Down → BFD超时 → 动态路由邻居断开 → 路由撤销)。当日志中存在联动异常时将在此处集中展现。
          </p>
        </template>
      </el-empty>
    </el-card>

    <!-- 核心两栏联动分析工作台 -->
    <div v-else class="rca-workbench-body">
      <!-- 左栏：联动事件列表 (340px) -->
      <div class="col-event-list">
        <div class="list-header">
          <span>联动事件索引 ({{ filteredRcaList.length }})</span>
          <el-select v-model="levelFilter" size="small" style="width: 110px;" placeholder="级别筛选">
            <el-option label="全部级别" value="ALL" />
            <el-option label="CRITICAL" value="CRITICAL" />
            <el-option label="HIGH" value="HIGH" />
            <el-option label="MEDIUM" value="MEDIUM" />
          </el-select>
        </div>

        <div class="event-cards-scroll">
          <div
            v-for="ev in filteredRcaList"
            :key="ev.id"
            :class="['rca-item-card', { active: selectedRCA?.id === ev.id }]"
            @click="selectRCA(ev)"
          >
            <div class="item-card-header">
              <span :class="['level-tag', ev.impact_level?.toLowerCase()]">
                {{ ev.impact_level || 'HIGH' }}
              </span>
              <span class="confidence-tag">
                置信度 {{ Math.round((ev.confidence || 0.8) * 100) }}%
              </span>
            </div>

            <div class="item-card-title">
              <strong>[{{ ev.root_module }}/{{ ev.root_brief }}]</strong>
              <span>{{ ev.root_cause_summary || '协议级连环故障联动' }}</span>
            </div>

            <div class="item-card-meta">
              <span>🕒 {{ ev.root_timestamp }}</span>
              <span v-if="ev.root_device_name" class="device-pill" :style="{ backgroundColor: ev.root_device_color || '#3B82F6' }">
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
              <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 6px;">
                <span :class="['level-tag', selectedRCA.impact_level?.toLowerCase()]">
                  {{ selectedRCA.impact_level || 'HIGH' }}
                </span>
                <h3 style="margin: 0; font-size: 16px; color: #0f172a;">
                  [{{ selectedRCA.root_module }}/{{ selectedRCA.root_brief }}] 根因联动故障推断
                </h3>
              </div>
              <p style="margin: 0; font-size: 13px; color: #64748b;">
                始发于 {{ selectedRCA.root_timestamp }}，触发了 {{ selectedRCA.correlated_count }} 条衍生告警，整体置信度为 {{ Math.round((selectedRCA.confidence || 0.8) * 100) }}%
              </p>
            </div>

            <div>
              <el-button type="primary" plain size="small" icon="Position" @click="jumpToAuditStream(selectedRCA.root_log_id)">
                在审计流中定位根因
              </el-button>
            </div>
          </div>

          <!-- 子 Tab: 拓扑图、时序链明细、处置指南 -->
          <el-tabs v-model="activeDetailTab" class="detail-tabs">
            <!-- Tab 1: 故障因果拓扑图 -->
            <el-tab-pane label="🌐 故障传播因果拓扑 (DAG)" name="dag">
              <div class="graph-box">
                <RcaGraph :rcaEvent="selectedRCA" />
              </div>
            </el-tab-pane>

            <!-- Tab 2: 级联时序传播链 -->
            <el-tab-pane label="⏱️ 级联时序传播链明细" name="timeline">
              <div class="cascade-timeline-wrap">
                <div class="step-card root-step">
                  <div class="step-badge root">
                    <span>1. 根因始发源</span>
                  </div>
                  <div class="step-content">
                    <div class="step-header">
                      <div class="step-header-left">
                        <span class="time-text">{{ selectedRCA.root_timestamp }}</span>
                        <span class="delay-tag zero">+0ms (始发)</span>
                        <span v-if="selectedRCA.root_device_name" class="device-pill" :style="{ backgroundColor: selectedRCA.root_device_color || '#3B82F6' }">
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
                    <span>{{ idx + 2 }}. 级联衍生</span>
                  </div>
                  <div class="step-content">
                    <div class="step-header">
                      <div class="step-header-left">
                        <span class="time-text">{{ impact.timestamp }}</span>
                        <span class="delay-tag">+{{ impact.delay_ms }}ms</span>
                        <span v-if="impact.device_name" class="device-pill" :style="{ backgroundColor: impact.device_color || '#3B82F6' }">
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
                      {{ impact.raw_log || `[${impact.module}/${impact.brief}] 衍生告警事件` }}
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

            <!-- Tab 3: 官方专家处置指引 -->
            <el-tab-pane label="💡 官方排查与根因处置指南" name="guide">
              <div class="guide-content-box">
                <div class="guide-block">
                  <h4>💥 根因推断综述</h4>
                  <div class="summary-text">{{ selectedRCA.root_cause_summary }}</div>
                </div>

                <div class="guide-block">
                  <h4>🛠️ 华为官方推荐排查处置步骤</h4>
                  <div class="action-text">{{ selectedRCA.recommended_action || '请检查根因节点接口物理状态与协议配置。' }}</div>
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
import { ref, computed, onMounted, watch } from 'vue'
import { Position } from '@element-plus/icons-vue'
import api from '@/api'
import RcaGraph from '@/components/RcaGraph.vue'
import { useRequest } from '@/composables/useRequest'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['jump-to-log'])

const loading = ref(false)
const rcaList = ref([])
const selectedRCA = ref(null)
const levelFilter = ref('ALL')
const activeDetailTab = ref('dag')

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

const fetchRCAEvents = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const res = await runFetchRCA(props.taskId)
    // 竞态守卫：请求被 newer 请求取消时返回 undefined，直接丢弃
    if (!res || res.code !== 0) return
    rcaList.value = res.data || []
    if (rcaList.value.length > 0) {
      // 默认选中第一个
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
    fetchRCAEvents()
  }
})

onMounted(() => {
  fetchRCAEvents()
})

// 供父组件主动刷新（替代 :key 强制重挂载，避免丢失选中项与滚动位置）
defineExpose({
  refresh: fetchRCAEvents
})
</script>

<style scoped>
.rca-center-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.rca-overview-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #ffffff;
  padding: 14px 20px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
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
  font-size: 12px;
  color: #64748b;
}
.kpi-divider {
  width: 1px;
  height: 28px;
  background: #e2e8f0;
}
.text-primary { color: #0284c7; }
.text-danger { color: #dc2626; }
.text-warning { color: #ea580c; }
.text-success { color: #16a34a; }

.empty-card {
  border-radius: 8px;
  padding: 30px;
}
.empty-tip {
  max-width: 540px;
  font-size: 13px;
  color: #64748b;
  line-height: 1.6;
  margin-top: 8px;
}

.rca-workbench-body {
  display: flex;
  gap: 16px;
  min-height: calc(100vh - 230px);
}
.col-event-list {
  width: 360px;
  flex-shrink: 0;
  background: #ffffff;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
}
.list-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 16px;
  border-bottom: 1px solid #f1f5f9;
  font-weight: 600;
  font-size: 14px;
  color: #0f172a;
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
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 12px;
  cursor: pointer;
  background: #fafafa;
  transition: all 0.2s ease;
}
.rca-item-card:hover {
  border-color: #3b82f6;
  background: #ffffff;
  box-shadow: 0 2px 4px rgba(0,0,0,0.04);
}
.rca-item-card.active {
  border-color: #0284c7;
  background: #f0f9ff;
  border-left: 4px solid #0284c7;
}
.item-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}
.level-tag {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}
.level-tag.critical { background: #fee2e2; color: #991b1b; }
.level-tag.high { background: #ffedd5; color: #9a3412; }
.level-tag.medium { background: #fef9c3; color: #854d0e; }
.confidence-tag {
  font-size: 11px;
  color: #166534;
  background: #dcfce7;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 600;
}
.item-card-title {
  font-size: 13px;
  color: #1e293b;
  line-height: 1.4;
  margin-bottom: 6px;
}
.item-card-title strong {
  color: #0369a1;
  margin-right: 4px;
}
.item-card-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 11px;
  color: #64748b;
}
.device-pill {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 11px;
  font-weight: 600;
  color: #fff;
}
.item-chain-preview {
  margin-top: 8px;
  padding-top: 6px;
  border-top: 1px dashed #e2e8f0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px;
  font-size: 11px;
}
.chain-mod-item {
  color: #475569;
  font-weight: 600;
}
.chain-arrow {
  color: #94a3b8;
  margin: 0 2px;
}
.chain-count {
  color: #ea580c;
  margin-left: 4px;
}

.col-event-detail {
  flex: 1;
  min-width: 0;
}
.detail-card {
  border-radius: 8px;
  height: 100%;
}
.detail-header-row {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding-bottom: 12px;
  border-bottom: 1px solid #e2e8f0;
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
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px 16px;
  background: #ffffff;
}
.step-card.root-step {
  border-color: #fca5a5;
  background: #fff5f5;
  border-left: 4px solid #ef4444;
}
.step-card.impact-step {
  border-left: 4px solid #f97316;
}
.step-badge {
  flex-shrink: 0;
  width: 100px;
  font-size: 12px;
  font-weight: 700;
}
.step-badge.root { color: #dc2626; }
.step-badge.impact { color: #ea580c; }

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
  font-size: 12px;
  color: #334155;
}
.delay-tag {
  background: #ffedd5;
  color: #9a3412;
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 600;
}
.delay-tag.zero {
  background: #fee2e2;
  color: #991b1b;
}
.mod-brief {
  font-size: 13px;
  color: #0f172a;
}
.code-tag {
  font-family: monospace;
  font-size: 12px;
  background: #f1f5f9;
  padding: 2px 6px;
  border-radius: 4px;
  color: #475569;
}
.step-raw-log {
  font-family: monospace;
  font-size: 12px;
  color: #334155;
  background: #f8fafc;
  border: 1px solid #f1f5f9;
  padding: 8px 12px;
  border-radius: 4px;
  word-break: break-all;
}
.step-params {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.param-badge {
  background: #eff6ff;
  border: 1px solid #dbeafe;
  color: #1e40af;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
}

.guide-content-box {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 10px 0;
}
.guide-block h4 {
  font-size: 14px;
  font-weight: 600;
  color: #0f172a;
  margin-bottom: 8px;
}
.summary-text {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 12px 16px;
  font-size: 13px;
  color: #334155;
  line-height: 1.6;
  white-space: pre-line;
  word-break: break-word;
}
.action-text {
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  border-radius: 6px;
  padding: 14px 16px;
  font-size: 13px;
  color: #1e40af;
  line-height: 1.6;
  white-space: pre-line;
  word-break: break-word;
}
</style>
