<template>
  <div class="multi-device-timeline-container">
    <!-- 顶部设备选择与筛选面板 -->
    <el-card shadow="never" class="filter-card">
      <div class="device-selector-row">
        <div class="selector-label">
          <strong>参与分析设备:</strong>
          <span style="color: #64748b; font-size: 12px; margin-left: 8px;">(勾选 2~5 台路由器/交换机进行跨设备时序对比)</span>
        </div>
        <div class="device-checkbox-group">
          <el-checkbox
            :indeterminate="isIndeterminate"
            v-model="checkAll"
            @change="handleCheckAllChange"
          >
            全选 ({{ deviceList.length }})
          </el-checkbox>
          <el-checkbox-group v-model="selectedDeviceIds" @change="handleDeviceSelectionChange">
            <el-checkbox
              v-for="d in deviceList"
              :key="d.id"
              :label="d.id"
              class="device-checkbox-item"
            >
              <span class="device-pill" :style="{ backgroundColor: d.color || '#3B82F6' }">
                {{ d.device_name }} ({{ d.log_count }}条)
              </span>
            </el-checkbox>
          </el-checkbox-group>
        </div>
      </div>

      <el-divider style="margin: 12px 0;" />

      <!-- 二级过滤参数栏 -->
      <div class="filters-row">
        <el-input
          v-model="filter.keyword"
          placeholder="搜索报文/简名/接口..."
          prefix-icon="Search"
          clearable
          style="width: 220px;"
          @change="fetchTimeline"
        />

        <el-select
          v-model="filter.modules"
          multiple
          collapse-tags
          collapse-tags-tooltip
          placeholder="协议/模块 (如 OSPF, BGP)"
          clearable
          style="width: 240px;"
          @change="fetchTimeline"
        >
          <el-option label="OSPF (动态路由协议)" value="OSPF" />
          <el-option label="BGP (边界网关协议)" value="BGP" />
          <el-option label="BFD (双向转发检测)" value="BFD" />
          <el-option label="IFNET / PORT (接口链路)" value="IFNET" />
          <el-option label="ISIS (中间系统路由)" value="ISIS" />
          <el-option label="LAG / TRUNK (链路聚合)" value="LAG" />
          <el-option label="AAA / RADIUS (安全认证)" value="AAA" />
        </el-select>

        <el-select
          v-model="filter.severity"
          placeholder="日志级别"
          clearable
          style="width: 140px;"
          @change="fetchTimeline"
        >
          <el-option label="所有级别" :value="null" />
          <el-option label="≤ 2 (紧急/严重)" :value="2" />
          <el-option label="≤ 4 (错误)" :value="4" />
          <el-option label="≤ 5 (警告)" :value="5" />
          <el-option label="≤ 6 (通知)" :value="6" />
        </el-select>

        <el-date-picker
          v-model="dateRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
          value-format="YYYY-MM-DD HH:mm:ss"
          style="width: 320px;"
          @change="handleDateRangeChange"
        />

        <el-button type="primary" icon="Search" :loading="loading" @click="fetchTimeline">
          查询时间线
        </el-button>
        <el-button icon="RefreshLeft" @click="resetFilters">重置</el-button>
      </div>
    </el-card>

    <!-- 联合时间线主视图 -->
    <el-card shadow="never" class="timeline-card" v-loading="loading">
      <div class="timeline-header">
        <div class="header-summary">
          <span>共汇总 <strong>{{ totalEvents }}</strong> 条时序事件</span>
          <span v-if="selectedDeviceIds.length > 0" class="sub-summary">
            (已选择 {{ selectedDeviceIds.length }} 台设备，按绝对时间升序排列)
          </span>
        </div>
        <div>
          <el-radio-group v-model="viewMode" size="small">
            <el-radio-button label="timeline">时间线视图</el-radio-button>
            <el-radio-button label="table">明细列表</el-radio-button>
          </el-radio-group>
        </div>
      </div>

      <!-- 空状态 -->
      <el-empty
        v-if="!loading && timelineEvents.length === 0"
        description="未检索到符合条件的多设备时间线日志，请调整上方筛选条件或勾选设备"
      />

      <!-- 视图 1：时序时间线流 -->
      <div v-else-if="viewMode === 'timeline'" class="timeline-stream">
        <el-timeline>
          <el-timeline-item
            v-for="(ev, index) in timelineEvents"
            :key="ev.log_id || index"
            :timestamp="formatTime(ev.timestamp)"
            placement="top"
            :color="ev.device_color || '#3B82F6'"
            size="large"
          >
            <div class="event-card" @click="showEventDetail(ev)">
              <div class="event-header">
                <div class="event-header-left">
                  <span class="device-pill" :style="{ backgroundColor: ev.device_color || '#3B82F6' }">
                    {{ ev.device_name }}
                  </span>
                  <span :class="['sev-badge', severityClass(ev.severity)]">
                    Lv.{{ ev.severity }}
                  </span>
                  <strong class="event-title">{{ ev.module }}/{{ ev.brief }}</strong>
                </div>
                <div class="event-header-right">
                  <span v-if="ev.hostname" class="hostname-tag">{{ ev.hostname }}</span>
                  <el-tag v-if="ev.knowledge_id" size="small" type="success" effect="plain">
                    已匹配知识
                  </el-tag>
                </div>
              </div>

              <div class="raw-log-box">
                {{ ev.raw_log || ev.message_body }}
              </div>

              <!-- 提取的关键动态变量参数徽章 -->
              <div v-if="ev.parameters && Object.keys(ev.parameters).length > 0" class="params-badge-row">
                <span v-for="(v, k) in ev.parameters" :key="k" class="param-badge">
                  <strong>{{ k }}:</strong> {{ v }}
                </span>
              </div>
            </div>
          </el-timeline-item>
        </el-timeline>
      </div>

      <!-- 视图 2：表格列表视图 -->
      <div v-else class="table-stream">
        <el-table :data="timelineEvents" border style="width: 100%;">
          <el-table-column label="发生时间" width="165">
            <template #default="{ row }">
              <span style="font-family: monospace;">{{ formatTime(row.timestamp) }}</span>
            </template>
          </el-table-column>

          <el-table-column label="所属设备" width="150">
            <template #default="{ row }">
              <span class="device-pill" :style="{ backgroundColor: row.device_color || '#3B82F6' }">
                {{ row.device_name }}
              </span>
            </template>
          </el-table-column>

          <el-table-column label="级别" width="70" align="center">
            <template #default="{ row }">
              <span :class="['sev-badge', severityClass(row.severity)]">{{ row.severity }}</span>
            </template>
          </el-table-column>

          <el-table-column label="模块 / 简名" width="160">
            <template #default="{ row }">
              <strong>{{ row.module }}</strong>/{{ row.brief }}
            </template>
          </el-table-column>

          <el-table-column prop="raw_log" label="日志报文" min-width="260">
            <template #default="{ row }">
              <div class="table-code">{{ row.raw_log }}</div>
            </template>
          </el-table-column>

          <el-table-column label="操作" width="90" align="center">
            <template #default="{ row }">
              <el-button size="small" type="primary" link @click="showEventDetail(row)">
                详情
              </el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </el-card>

    <!-- 事件详情抽屉 -->
    <el-drawer
      v-model="drawerVisible"
      title="时序事件深度解析与官方排查指引"
      size="480px"
      destroy-on-close
    >
      <template v-if="selectedEvent">
        <div class="drawer-section">
          <h4>📌 基础事件元信息</h4>
          <div class="meta-grid">
            <div><strong>发生时间:</strong> {{ formatTime(selectedEvent.timestamp) }}</div>
            <div>
              <strong>所属设备:</strong>
              <span class="device-pill" :style="{ backgroundColor: selectedEvent.device_color || '#3B82F6', marginLeft: '6px' }">
                {{ selectedEvent.device_name }}
              </span>
            </div>
            <div><strong>Host / IP:</strong> {{ selectedEvent.hostname || '-' }}</div>
            <div><strong>模块 / 简名:</strong> {{ selectedEvent.module }} / {{ selectedEvent.brief }}</div>
            <div><strong>日志级别:</strong> Lv.{{ selectedEvent.severity }}</div>
            <div><strong>来源文件:</strong> {{ selectedEvent.source_file || '-' }}</div>
          </div>
        </div>

        <div class="drawer-section">
          <h4>📝 原始报文</h4>
          <div class="drawer-code">{{ selectedEvent.raw_log }}</div>
        </div>

        <div v-if="selectedEvent.parameters && Object.keys(selectedEvent.parameters).length > 0" class="drawer-section">
          <h4>🧩 动态提取参数</h4>
          <el-table :data="formatParamsArray(selectedEvent.parameters)" border size="small">
            <el-table-column prop="key" label="参数名称" width="140" />
            <el-table-column prop="val" label="提取现场值" />
          </el-table>
        </div>

        <div v-if="knowledgeDetail" class="drawer-section">
          <h4>📖 华为官方知识库释义与建议</h4>
          <div class="kb-card">
            <div class="kb-item">
              <strong>含义解释:</strong>
              <p>{{ knowledgeDetail.description || '-' }}</p>
            </div>
            <div class="kb-item">
              <strong>可能原因:</strong>
              <p>{{ knowledgeDetail.cause || '-' }}</p>
            </div>
            <div class="kb-item action-box">
              <strong>官方推荐处置步骤:</strong>
              <p>{{ knowledgeDetail.action || '-' }}</p>
            </div>
          </div>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, onMounted, defineProps, watch } from 'vue'
import api from '@/api'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const loading = ref(false)
const deviceList = ref([])
const selectedDeviceIds = ref([])
const checkAll = ref(true)
const isIndeterminate = ref(false)

const viewMode = ref('timeline')
const timelineEvents = ref([])
const totalEvents = ref(0)

const dateRange = ref([])
const filter = ref({
  keyword: '',
  modules: [],
  severity: null,
  time_start: null,
  time_end: null
})

const drawerVisible = ref(false)
const selectedEvent = ref(null)
const knowledgeDetail = ref(null)

const fetchDevices = async () => {
  if (!props.taskId) return
  try {
    const res = await api.getDevices(props.taskId)
    if (res.code === 0) {
      deviceList.value = res.data || []
      // 默认全选设备
      selectedDeviceIds.value = deviceList.value.map(d => d.id)
      checkAll.value = true
      isIndeterminate.value = false
      if (selectedDeviceIds.value.length > 0) {
        fetchTimeline()
      }
    }
  } catch (e) {
    console.error('Fetch devices failed:', e)
  }
}

const handleCheckAllChange = (val) => {
  selectedDeviceIds.value = val ? deviceList.value.map(d => d.id) : []
  isIndeterminate.value = false
  fetchTimeline()
}

const handleDeviceSelectionChange = (value) => {
  const checkedCount = value.length
  checkAll.value = checkedCount === deviceList.value.length
  isIndeterminate.value = checkedCount > 0 && checkedCount < deviceList.value.length
  fetchTimeline()
}

const handleDateRangeChange = (val) => {
  if (val && val.length === 2) {
    filter.value.time_start = val[0]
    filter.value.time_end = val[1]
  } else {
    filter.value.time_start = null
    filter.value.time_end = null
  }
  fetchTimeline()
}

const resetFilters = () => {
  filter.value = {
    keyword: '',
    modules: [],
    severity: null,
    time_start: null,
    time_end: null
  }
  dateRange.value = []
  selectedDeviceIds.value = deviceList.value.map(d => d.id)
  checkAll.value = true
  isIndeterminate.value = false
  fetchTimeline()
}

const fetchTimeline = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const payload = {
      device_ids: selectedDeviceIds.value,
      modules: filter.value.modules,
      severity: filter.value.severity,
      keyword: filter.value.keyword,
      time_start: filter.value.time_start,
      time_end: filter.value.time_end,
      page: 1,
      page_size: 500,
      asc_order: true
    }
    const res = await api.queryMultiDeviceLogs(props.taskId, payload)
    if (res.code === 0) {
      timelineEvents.value = res.data?.events || []
      totalEvents.value = res.data?.total || 0
    }
  } catch (e) {
    console.error('Fetch multi device timeline failed:', e)
  } finally {
    loading.value = false
  }
}

const showEventDetail = async (ev) => {
  selectedEvent.value = ev
  knowledgeDetail.value = null
  drawerVisible.value = true

  if (ev.knowledge_id) {
    try {
      const res = await api.getKnowledgeDetail(ev.knowledge_id)
      if (res.code === 0) {
        knowledgeDetail.value = res.data
      }
    } catch (e) {
      console.error('Fetch knowledge detail failed:', e)
    }
  }
}

const formatParamsArray = (paramsObj) => {
  if (!paramsObj) return []
  return Object.keys(paramsObj).map(k => ({
    key: k,
    val: paramsObj[k]
  }))
}

const formatTime = (ts) => {
  if (!ts) return '-'
  return ts.replace('T', ' ').substring(0, 19)
}

const severityClass = (sev) => {
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
}

watch(() => props.taskId, (newVal) => {
  if (newVal) {
    fetchDevices()
  }
})

onMounted(() => {
  fetchDevices()
})
</script>

<style scoped>
.multi-device-timeline-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.filter-card {
  border-radius: 8px;
}
.device-selector-row {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.device-checkbox-group {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.device-pill {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 12px;
  font-size: 11px;
  font-weight: 600;
  color: #fff;
}
.filters-row {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: center;
}
.timeline-card {
  border-radius: 8px;
}
.timeline-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
  padding-bottom: 12px;
  border-bottom: 1px solid #e2e8f0;
}
.header-summary {
  font-size: 14px;
  color: #0f172a;
}
.sub-summary {
  color: #64748b;
  font-size: 13px;
  margin-left: 8px;
}
.timeline-stream {
  padding: 10px 20px 20px 10px;
}
.event-card {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 12px 16px;
  background: #ffffff;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 1px 2px rgba(0,0,0,0.03);
}
.event-card:hover {
  border-color: #3b82f6;
  box-shadow: 0 4px 6px -1px rgba(0,0,0,0.08);
}
.event-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}
.event-header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}
.event-header-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
.event-title {
  font-size: 14px;
  color: #0f172a;
}
.hostname-tag {
  font-family: monospace;
  font-size: 12px;
  color: #64748b;
  background: #f1f5f9;
  padding: 2px 6px;
  border-radius: 4px;
}
.raw-log-box {
  font-family: monospace;
  font-size: 12px;
  color: #334155;
  background: #f8fafc;
  padding: 8px 12px;
  border-radius: 6px;
  border: 1px solid #f1f5f9;
  word-break: break-all;
}
.params-badge-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}
.param-badge {
  background: #eff6ff;
  border: 1px solid #dbeafe;
  color: #1e40af;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
}
.sev-badge {
  display: inline-block;
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 11px;
  font-weight: bold;
}
.sev-crit { background: #fee2e2; color: #991b1b; }
.sev-err { background: #ffedd5; color: #9a3412; }
.sev-warn { background: #fef9c3; color: #854d0e; }
.sev-info { background: #e0f2fe; color: #075985; }

.table-code {
  font-family: monospace;
  font-size: 12px;
  color: #334155;
  word-break: break-all;
}

.drawer-section {
  margin-bottom: 20px;
}
.drawer-section h4 {
  font-size: 14px;
  font-weight: 600;
  color: #0f172a;
  margin-bottom: 8px;
}
.meta-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  font-size: 13px;
  background: #f8fafc;
  padding: 12px;
  border-radius: 6px;
}
.drawer-code {
  font-family: monospace;
  font-size: 12px;
  background: #1e293b;
  color: #f8fafc;
  padding: 12px;
  border-radius: 6px;
  word-break: break-all;
}
.kb-card {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  font-size: 13px;
}
.kb-item p {
  margin: 4px 0 0 0;
  color: #334155;
  line-height: 1.5;
}
.action-box {
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  padding: 10px;
  border-radius: 4px;
  color: #1e40af;
}
.action-box p {
  color: #1e3a8a;
}
</style>
