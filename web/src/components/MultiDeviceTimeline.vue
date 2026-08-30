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
          placeholder="协议/模块 (动态加载)"
          clearable
          style="width: 240px;"
          @change="fetchTimeline"
        >
          <el-option
            v-for="mod in availableModules"
            :key="mod"
            :label="getModuleLabel(mod)"
            :value="mod"
          />
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
          <!--
            UI-04: 原实现只显示服务端 total，却不说明页面实际只渲染了 500 条，
            造成"共 N 条"与实际展示条数严重不符。这里把截断事实显式呈现。
          -->
          <span v-if="timelineTruncated">
            共汇总 <strong>{{ totalEvents }}</strong> 条时序事件，当前仅展示前
            <strong>{{ timelineEvents.length }}</strong> 条
            <el-tooltip content="请通过设备、模块、级别或时间范围收敛筛选条件后查看其余事件">
              <el-tag type="warning" size="small" effect="plain">已截断</el-tag>
            </el-tooltip>
          </span>
          <span v-else>
            共汇总 <strong>{{ totalEvents }}</strong> 条时序事件
          </span>
          <span v-if="selectedDeviceIds.length > 0" class="sub-summary">
            (已选择 {{ selectedDeviceIds.length }} 台设备，按绝对时间升序排列)
          </span>
        </div>
        <div style="display: flex; align-items: center; gap: 10px;">
          <el-radio-group v-model="viewMode" size="small">
            <el-radio-button label="timeline">时间线视图</el-radio-button>
            <el-radio-button label="table">明细列表</el-radio-button>
          </el-radio-group>
          <el-button type="success" size="small" :icon="Download" @click="exportCSV">
            导出 CSV
          </el-button>
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
            :timestamp="formatEventTime(ev)"
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
                  <el-tag v-else-if="ev.module === 'COMMENT'" size="small" type="info" effect="plain">
                    注释/元数据
                  </el-tag>
                </div>
              </div>

              <!-- 结构化解析事件摘要 -->
              <div class="event-summary-box">
                <div class="summary-line">
                  <span class="summary-badge">解析摘要</span>
                  <!-- UI-06: 使用预计算的摘要缓存，避免每次重渲染都重算全部事件 -->
                  <span class="summary-content">{{ ev._summary || formatEventSummary(ev) }}</span>
                </div>
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
              <span style="font-family: monospace;">{{ formatEventTime(row) }}</span>
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

          <el-table-column label="事件解析摘要" min-width="280">
            <template #default="{ row }">
              <div class="table-summary">{{ formatEventSummary(row) }}</div>
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
            <div><strong>发生时间:</strong> {{ formatEventTime(selectedEvent) }}</div>
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
        <div v-else-if="selectedEvent.module === 'COMMENT'" class="drawer-section">
          <h4>💡 注释性日志说明</h4>
          <div class="kb-card">
            <div class="kb-item">
              <strong>日志性质:</strong>
              <p>网络设备系统注释或日志文件导出元数据（以 <code>#</code> 开头），非故障告警事件。</p>
            </div>
            <div class="kb-item">
              <strong>典型作用:</strong>
              <p>记录日志生成槽位与环境、设备型号及固件版本、防篡改哈希校验（Digest）或文件归档记录，用于溯源与完整性校验。</p>
            </div>
            <div class="kb-item action-box">
              <strong>运维建议:</strong>
              <p>系统已将其归档为信息性记录，无需进行故障排查或处置。</p>
            </div>
          </div>
        </div>
      </template>
    </el-drawer>
  </div>
</template>

<script setup>
// UI-16: defineProps 是编译器宏，无需从 vue 导入
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Download } from '@element-plus/icons-vue'
import api from '@/api'
import { useRequest } from '@/composables/useRequest'
import { formatTime as sharedFormatTime } from '@/utils/format'

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
const availableModules = ref([])

const moduleNameMap = {
  'OSPF': 'OSPF (动态路由协议)',
  'BGP': 'BGP (边界网关协议)',
  'BFD': 'BFD (双向转发检测)',
  'IFNET': 'IFNET (接口链路)',
  'PORT': 'PORT (物理端口)',
  'ISIS': 'ISIS (中间系统路由)',
  'LAG': 'LAG / Eth-Trunk (链路聚合)',
  'ETH-TRUNK': 'Eth-Trunk (链路聚合)',
  'AAA': 'AAA (认证计费)',
  'RADIUS': 'RADIUS (认证服务器)',
  'SSH': 'SSH (远程管理)',
  'STP': 'STP / RSTP / MSTP (生成树)',
  'MSTP': 'MSTP (多生成树)',
  'RSTP': 'RSTP (快速生成树)',
  'ARP': 'ARP (地址解析)',
  'VRRP': 'VRRP (虚拟路由冗余)',
  'MPLS': 'MPLS (多协议标签交换)',
  'LDP': 'LDP (标签分发)',
  'SYS': 'SYS (系统底层)',
  'SHELL': 'SHELL (管理配置)',
  'DEVM': 'DEVM (设备硬件管理)',
  'INFO': 'INFO (信息中心)'
}

const getModuleLabel = (mod) => {
  const upper = (mod || '').toUpperCase()
  return moduleNameMap[upper] || mod
}

const fetchModules = async () => {
  if (!props.taskId) return
  try {
    const res = await api.getTaskModules(props.taskId)
    if (res.code === 0 && Array.isArray(res.data)) {
      availableModules.value = res.data
    }
  } catch (e) {
    console.error('Fetch task modules failed:', e)
  }
}

const viewMode = ref('timeline')
const timelineEvents = ref([])
const totalEvents = ref(0)

// UI-04: 时间线是否被截断（服务端总数 > 本次实际渲染条数）
const timelineTruncated = computed(
  () => totalEvents.value > timelineEvents.value.length
)
// UI-05: CSV 导出范围说明，用于提示文案，避免"导出 500 条却号称全量"
const exportScopeText = computed(() =>
  timelineTruncated.value
    ? `${timelineEvents.value.length} / 共 ${totalEvents.value} 条`
    : `${timelineEvents.value.length} 条`
)

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

// 单次拉取的时间线条数上限。
//
// UI-04: 原实现固定 `page_size: 500` 且头部显示"共汇总 N 条"（N 来自服务端 total），
// 于是大任务会出现"提示 12000 条、实际只渲染 500 条"的严重不一致，
// 运维据此归档会漏数据。这里保留服务端上限，但把截断事实显式告知用户。
const TIMELINE_PAGE_SIZE = 500

/**
 * UI-06: 事件摘要的预计算。
 *
 * 原实现在模板里直接调用 `formatEventSummary(ev)`，
 * 而该函数内含 8 个协议分支 + 参数归一化正则循环，
 * 每次组件重渲染都会对全部事件重算一遍——
 * 切视图或勾选设备时明显卡顿。
 * 这里在数据落库时算一次并缓存到 `_summary` 字段。
 */
const withSummary = (ev) => {
  if (!ev || ev._summary) return ev
  ev._summary = formatEventSummary(ev)
  return ev
}

/**
 * 拉取时间线。
 *
 * UI-07 / WEB-08: 接入统一的 useRequest 竞态守卫。
 * 原先连续点选设备/级别时并发请求会乱序返回，
 * 最后一次响应的结果未必对应最后一次筛选条件，出现"数据与条件不符"。
 */
// 失败提示由 api 拦截器统一弹出，这里不再重复 toast
const { run: runFetchTimeline } = useRequest(api.queryMultiDeviceLogs)

const fetchTimeline = async () => {
  if (!props.taskId) return

  const payload = {
    device_ids: selectedDeviceIds.value,
    modules: filter.value.modules,
    severity: filter.value.severity,
    keyword: filter.value.keyword,
    time_start: filter.value.time_start,
    time_end: filter.value.time_end,
    page: 1,
    page_size: TIMELINE_PAGE_SIZE,
    asc_order: true
  }

  loading.value = true
  try {
    const res = await runFetchTimeline(props.taskId, payload)
    // 被 newer 请求取消时返回 undefined，直接丢弃
    if (!res || res.code !== 0) return
    timelineEvents.value = (res.data?.events || []).map(withSummary)
    totalEvents.value = res.data?.total || 0
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

const formatEventSummary = (ev) => {
  if (!ev) return '-'
  // 优先使用后端统一解析引擎生成的中文事件摘要
  if (ev.event_summary) {
    return ev.event_summary
  }

  const mod = (ev.module || '').toUpperCase()
  const brief = (ev.brief || '').toUpperCase()
  const params = ev.parameters || {}

  // 辅助查找参数（不区分大小写与下划线）
  const getParam = (...keys) => {
    for (const k of keys) {
      if (params[k] !== undefined && params[k] !== '') return params[k]
      const normK = k.toLowerCase().replace(/[-_\s]/g, '')
      for (const [pk, pv] of Object.entries(params)) {
        if (pk.toLowerCase().replace(/[-_\s]/g, '') === normK && pv !== '') {
          return pv
        }
      }
    }
    return ''
  }

  // 1. BGP 协议
  if (mod === 'BGP') {
    const peer = getParam('bgpPeerRemoteAddr', 'PeerID', 'PeerRemoteAddr', 'Neighbor', 'PeerAddr', 'RemoteAddr')
    const local = getParam('bgpPeerLocalAddr', 'LocalAddr', 'LocalAddress', 'LocalIP')
    const reason = getParam('NotifyReason', 'Reason', 'ErrorCode', 'ErrorSubCode', 'bgpPeerLastState', 'LastState')

    if (brief.includes('DOWN') || brief.includes('BACKWARD') || brief.includes('RESET') || brief.includes('HOLD_TIME')) {
      let desc = `BGP邻居中断: 对端[${peer || '未捕获'}]`
      if (local) desc += `, 本端[${local}]`
      desc += `，中断原因: ${reason || 'HoldTimer超时或对端重置会话'}`
      return desc
    }
    if (brief.includes('ESTABLISHED') || brief.includes('FORWARD') || brief.includes('UP')) {
      let desc = `BGP邻居建立: 对端[${peer || '未捕获'}]`
      if (local) desc += `, 本端[${local}]`
      desc += `，状态已转换为 ESTABLISHED`
      return desc
    }
    if (brief.includes('FLAP') || brief.includes('DAMP')) {
      return `BGP路由震荡: 对端[${peer || '未捕获'}] 路由频繁抖动`
    }
    return `BGP事件 [${ev.brief}]: 对端[${peer || '未知'}]${reason ? '，原因: ' + reason : ''}`
  }

  // 2. OSPF 协议
  if (mod === 'OSPF') {
    const nbr = getParam('RouterID', 'NbrRouterId', 'NeighborRouterId', 'Neighbor', 'NbrIp')
    const iface = getParam('InterfaceName', 'Interface', 'IfName', 'PortName')
    const reason = getParam('Reason', 'EventReason', 'NbrState', 'State')

    if (brief.includes('DOWN') || brief.includes('ADJCHANGE') || brief.includes('RESET')) {
      return `OSPF邻居中断: 接口[${iface || '未捕获'}] 邻居Router-ID[${nbr || '未捕获'}]，原因: ${reason || '邻居失效超时或接口Down'}`
    }
    if (brief.includes('FULL') || brief.includes('UP') || brief.includes('ESTABLISHED')) {
      return `OSPF邻居建立: 接口[${iface || '未捕获'}] 与邻居Router-ID[${nbr || '未捕获'}] 达到 Full 状态`
    }
    return `OSPF事件 [${ev.brief}]: 接口[${iface || '未捕获'}]${nbr ? '，邻居: ' + nbr : ''}${reason ? '，原因: ' + reason : ''}`
  }

  // 3. BFD 协议
  if (mod === 'BFD') {
    const peer = getParam('PeerAddr', 'Destination', 'PeerIP', 'DstIP', 'SessId')
    const iface = getParam('InterfaceName', 'Interface', 'IfName')
    const diag = getParam('Diag', 'Diagnostic', 'Reason', 'DiagCode')

    if (brief.includes('DOWN') || brief.includes('FAIL') || brief.includes('TIMEOUT')) {
      return `BFD会话中断: 对端[${peer || '未捕获'}]${iface ? ' 接口[' + iface + ']' : ''}，检测原因: ${diag || '链路回显超时或对端Down'}`
    }
    if (brief.includes('UP') || brief.includes('ESTABLISHED')) {
      return `BFD会话建立: 对端[${peer || '未捕获'}] 双向连通状态恢复正常`
    }
    return `BFD状态变更 [${ev.brief}]: 对端[${peer || '未知'}]${diag ? '，诊断: ' + diag : ''}`
  }

  // 4. IFNET / PORT 接口协议
  if (mod === 'IFNET' || mod === 'PORT' || mod === 'ETHBASE') {
    const iface = getParam('InterfaceName', 'Interface', 'IfName', 'PortName')
    const reason = getParam('Reason', 'ErrorReason', 'LineProtocolStatus', 'Cause')

    if (brief.includes('DOWN') || brief.includes('ERRORDOWN') || brief.includes('FAIL')) {
      return `接口链路中断: 接口[${iface || '未捕获'}] 状态变更为 DOWN，原因: ${reason || '物理光电信号丢失或人为关闭'}`
    }
    if (brief.includes('UP')) {
      return `接口链路恢复: 接口[${iface || '未捕获'}] 物理与协议状态已转换为 UP`
    }
    return `接口事件 [${ev.brief}]: 接口[${iface || '未知'}]${reason ? '，原因: ' + reason : ''}`
  }

  // 5. ISIS 协议
  if (mod === 'ISIS') {
    const nbr = getParam('NeighborSystemId', 'SystemId', 'Neighbor', 'NbrId')
    const iface = getParam('InterfaceName', 'Interface', 'IfName')
    const reason = getParam('Reason', 'CircuitId')

    if (brief.includes('DOWN') || brief.includes('RESET')) {
      return `ISIS邻居中断: 接口[${iface || '未捕获'}] 邻居System-ID[${nbr || '未捕获'}]，原因: ${reason || 'HoldTime超时'}`
    }
    if (brief.includes('UP') || brief.includes('ESTABLISHED')) {
      return `ISIS邻居建立: 接口[${iface || '未捕获'}] 与邻居[${nbr || '未捕获'}] 邻接关系正常`
    }
    return `ISIS事件 [${ev.brief}]: 邻居[${nbr || '未知'}]${reason ? '，原因: ' + reason : ''}`
  }

  // 6. LAG / Eth-Trunk 链路聚合
  if (mod === 'LAG' || mod === 'TRUNK' || mod === 'ETH-TRUNK') {
    const trunk = getParam('TrunkId', 'LagName', 'EthTrunk', 'TrunkName')
    const port = getParam('PortName', 'Interface', 'IfName', 'MemberPort')
    const reason = getParam('Reason', 'Cause')

    if (brief.includes('DOWN') || brief.includes('DEL') || brief.includes('REMOVE')) {
      return `聚合链路告警: 聚合组[${trunk || 'Eth-Trunk'}] 成员端口[${port || '未捕获'}] 异常退出，原因: ${reason || '物理状态Down'}`
    }
    if (brief.includes('UP') || brief.includes('ADD')) {
      return `聚合链路变动: 聚合组[${trunk || 'Eth-Trunk'}] 成员端口[${port || '未捕获'}] 成功加入聚合`
    }
    return `链路聚合事件 [${ev.brief}]: ${trunk || 'Trunk'}${port ? ' 端口: ' + port : ''}`
  }

  // 7. AAA / RADIUS / 安全认证
  if (mod === 'AAA' || mod === 'RADIUS' || mod === 'HWTACACS') {
    const server = getParam('ServerIP', 'ServerAddr', 'Server', 'RadiusServer')
    const user = getParam('UserName', 'User', 'Account')
    const reason = getParam('Reason', 'FailReason', 'ErrorCode')

    if (brief.includes('DOWN') || brief.includes('TIMEOUT') || brief.includes('UNREACHABLE')) {
      return `AAA服务器异常: 认证服务器[${server || '未捕获'}] 状态不可达/无响应，原因: ${reason || '网络中断或服务停止'}`
    }
    if (brief.includes('FAIL') || brief.includes('DENY') || brief.includes('REJECT')) {
      return `AAA认证失败: 用户[${user || '未捕获'}] 认证被拒绝${server ? ' (服务器: ' + server + ')' : ''}，原因: ${reason || '密码错误或策略限制'}`
    }
    return `AAA/RADIUS事件 [${ev.brief}]: ${user ? '用户: ' + user : ''}${server ? ' 服务器: ' + server : ''}`
  }

  // 8. 硬件与环境 (DEVM / FAN / POWER / CPU)
  if (mod === 'DEVM' || mod === 'FAN' || mod === 'POWER' || mod === 'ENVIRONMENT') {
    const component = getParam('EntityName', 'Slot', 'SubSlot', 'FanId', 'PowerId', 'CpuId')
    const reason = getParam('Reason', 'CurrentState', 'Threshold')
    return `设备硬件告警 [${ev.brief}]: 部件[${component || '未知部件'}]，状态/原因: ${reason || ev.message_body || '硬件指标异常'}`
  }

  // 通用兜底：如果存在提取到的结构化参数，拼接展示
  if (params && Object.keys(params).length > 0) {
    const pStr = Object.entries(params)
      .slice(0, 4)
      .map(([k, v]) => `${k}: ${v}`)
      .join(' | ')
    return `[${ev.module}/${ev.brief}] ${pStr}`
  }

  return ev.message_body || ev.raw_log || '-'
}

const formatParamsArray = (paramsObj) => {
  if (!paramsObj) return []
  return Object.keys(paramsObj).map(k => ({
    key: k,
    val: paramsObj[k]
  }))
}

// WEB-16: 复用统一实现，零值展示由占位符统一控制
const formatTime = (ts) => sharedFormatTime(ts, '无法解析')
const formatEventTime = (ev) => {
  if (!ev) return '-'
  if (ev.module === 'COMMENT' && (!ev.timestamp || String(ev.timestamp).startsWith('0001-01-01'))) {
    return '— (注释行)'
  }
  return formatTime(ev.timestamp)
}

const severityClass = (sev) => {
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
}

// 导出为 CSV 格式
const exportCSV = () => {
  if (!timelineEvents.value || timelineEvents.value.length === 0) {
    ElMessage.warning('当前时间线无数据可导出')
    return
  }

  const headers = [
    '发生时间',
    '所属设备',
    '主机名',
    '级别',
    '模块',
    '事件简名',
    '解析事件摘要',
    '知识库匹配',
    '来源文件'
  ]

  const rows = timelineEvents.value.map(ev => [
    formatTime(ev.timestamp),
    ev.device_name || '',
    ev.hostname || '',
    ev.severity ?? '',
    ev.module || '',
    ev.brief || '',
    ev._summary || formatEventSummary(ev),
    ev.knowledge_id ? '是' : '否',
    ev.source_file || ''
  ])

  const sanitizeCSVCell = (cell) => {
    let str = String(cell ?? '')
    // 防范 CSV/Excel 宏与公式注入攻击（当单元格以 =、+、-、@、制表符等开头时增加单引号转义）
    if (/^[=+\-@\t\r]/.test(str)) {
      str = "'" + str
    }
    return `"${str.replace(/"/g, '""')}"`
  }

  const BOM = '\uFEFF' // Excel UTF-8 兼容BOM
  const csvBody = [headers, ...rows]
    .map(row => row.map(sanitizeCSVCell).join(','))
    .join('\r\n')

  const blob = new Blob([BOM + csvBody], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  const dateStr = new Date().toISOString().replace(/[-:T.]/g, '').substring(0, 14)
  link.href = url
  link.setAttribute('download', `多设备时序协同分析_${props.taskId}_${dateStr}.csv`)
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
  // UI-05: 明确导出范围。旧文案只说"导出 N 条"，
  // 与头部"共汇总 M 条"矛盾，运维据此归档会漏数据。
  ElMessage.success(
    `成功导出 ${exportScopeText.value}时序事件为 CSV 文件${
      timelineTruncated.value ? '（仅含当前已加载部分，请收敛筛选条件后分批导出）' : ''
    }`
  )
}

// 注：在途请求的取消已由 useRequest 内部的 onScopeDispose 统一处理 (WEB-08)

watch(() => props.taskId, (newVal) => {
  if (newVal) {
    fetchDevices()
    fetchModules()
  }
})

onMounted(() => {
  fetchDevices()
  fetchModules()
})

// 供父组件主动刷新（替代 :key 强制重挂载，避免丢失筛选条件与滚动位置）
defineExpose({
  refresh: async () => {
    await fetchDevices()
    await fetchTimeline()
  }
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
  max-height: calc(100vh - 280px);
  overflow-y: auto;
  padding: 10px 20px 20px 10px;
}
.table-stream {
  max-height: calc(100vh - 280px);
  overflow-y: auto;
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
.event-summary-box {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-left: 3px solid #3b82f6;
  border-radius: 4px;
  padding: 8px 12px;
  margin: 4px 0 6px 0;
}
.summary-line {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.summary-badge {
  font-size: 10px;
  font-weight: 700;
  color: #2563eb;
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  padding: 1px 6px;
  border-radius: 3px;
  white-space: nowrap;
  flex-shrink: 0;
}
.summary-content {
  font-size: 13px;
  font-weight: 600;
  color: #0f172a;
  line-height: 1.5;
}
.table-summary {
  font-size: 12px;
  color: #0f172a;
  font-weight: 500;
  line-height: 1.4;
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
