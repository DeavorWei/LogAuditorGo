<template>
  <div class="multi-device-report-container" v-loading="loading">
    <!-- 顶部操作栏 -->
    <div class="report-header-bar">
      <div class="header-left">
        <h3 style="margin: 0; font-size: 16px; color: #0f172a;">多设备协同诊断与对比分析报告</h3>
        <span style="font-size: 12px; color: #64748b;">
          综合评估多台路由器/交换机的协议交互时序、共性故障与级联事件
        </span>
      </div>
      <div class="header-right">
        <el-button-group>
          <el-button icon="Refresh" @click="fetchReport">刷新报告</el-button>
          <el-button type="success" icon="Download" :loading="exporting" @click="exportHTMLReport">
            导出 HTML 离线报告
          </el-button>
        </el-button-group>
      </div>
    </div>

    <template v-if="reportData">
      <!-- 核心指标看板 -->
      <div class="stats-grid">
        <div class="stat-card">
          <div class="val">{{ reportData.devices?.length || 0 }}</div>
          <div class="label">分析网络设备数</div>
        </div>
        <div class="stat-card" style="border-left-color: #10B981;">
          <div class="val">{{ reportData.total_logs || 0 }}</div>
          <div class="label">联合分析日志总数</div>
        </div>
        <div class="stat-card" style="border-left-color: #8B5CF6;">
          <div class="val">{{ reportData.total_matched || 0 }}</div>
          <div class="label">知识库匹配条数</div>
        </div>
        <div class="stat-card" style="border-left-color: #F97316;">
          <div class="val">{{ reportData.clusters?.length || 0 }}</div>
          <div class="label">时序关联事件簇</div>
        </div>
      </div>

      <!-- 协同诊断与专家推断结论 -->
      <el-card shadow="never" class="section-card">
        <template #header>
          <div class="card-header-title">
            <el-icon color="#0284c7"><Aim /></el-icon>
            <span>协同推断与专家排查结论</span>
          </div>
        </template>
        <div class="conclusion-box">
          {{ reportData.conclusion }}
        </div>
      </el-card>

      <!-- 参与协同分析的网络设备概览 -->
      <el-card shadow="never" class="section-card">
        <template #header>
          <div class="card-header-title">
            <el-icon color="#16a34a"><Monitor /></el-icon>
            <span>参与协同审计设备概况 ({{ reportData.devices?.length || 0 }} 台)</span>
          </div>
        </template>

        <div class="devices-grid">
          <div
            v-for="d in reportData.devices"
            :key="d.device.id"
            class="device-overview-card"
            :style="{ borderTopColor: d.device.color || '#3B82F6' }"
          >
            <div class="device-card-header">
              <span class="device-name">{{ d.device.device_name }}</span>
              <el-tag size="small">{{ d.device.device_type }}</el-tag>
            </div>
            <div class="meta-row">
              <span class="label">Hostname:</span>
              <span class="val">{{ d.device.hostname || '-' }}</span>
            </div>
            <div class="meta-row">
              <span class="label">管理 IP:</span>
              <span class="val">{{ d.device.management_ip || '-' }}</span>
            </div>
            <div class="meta-row">
              <span class="label">日志/匹配:</span>
              <span class="val">{{ d.log_count }} 行 / {{ d.matched_count }} 条匹配</span>
            </div>
            <div v-if="d.top_modules && d.top_modules.length > 0" class="top-modules-row">
              <span class="label">主要协议:</span>
              <div class="modules-tags">
                <span
                  v-for="m in d.top_modules"
                  :key="m.module"
                  class="mod-tag"
                >
                  {{ m.module }} ({{ m.count }})
                </span>
              </div>
            </div>
          </div>
        </div>
      </el-card>

      <!-- 跨设备共性事件与时序关联事件簇 -->
      <el-card v-if="(reportData.common_events && reportData.common_events.length > 0) || (reportData.clusters && reportData.clusters.length > 0)" shadow="never" class="section-card">
        <template #header>
          <div class="card-header-title">
            <el-icon color="#f59e0b"><Connection /></el-icon>
            <span>跨设备共性事件与协同传播簇</span>
          </div>
        </template>

        <div v-if="reportData.common_events && reportData.common_events.length > 0" class="common-events-section">
          <div class="sub-title">在多台设备上同时或相继出现的事件类型:</div>
          <div class="tags-wrap">
            <span
              v-for="ce in reportData.common_events"
              :key="ce"
              class="common-event-tag"
            >
              {{ ce }}
            </span>
          </div>
        </div>

        <div v-if="reportData.clusters && reportData.clusters.length > 0" class="clusters-section">
          <div class="sub-title">时间窗口协同事件簇 (±60s 关联):</div>
          <div class="clusters-list">
            <div
              v-for="(cl, idx) in reportData.clusters"
              :key="idx"
              class="cluster-item-card"
            >
              <div class="cluster-summary">{{ cl.summary }}</div>
              <div class="cluster-devices">
                <span>涉及设备:</span>
                <el-tag
                  v-for="dev in cl.devices"
                  :key="dev"
                  size="small"
                  type="warning"
                  effect="light"
                  style="margin-left: 6px;"
                >
                  {{ dev }}
                </el-tag>
              </div>
            </div>
          </div>
        </div>
      </el-card>
    </template>

    <!--
      UI-15: 原实现只有 loading 态与空态，请求失败时完全静默，
      用户看到的是"空态"而不是"出错了"，会误以为任务真的没有数据。
      这里区分失败态与空态，并给空态补上可执行的引导操作。
    -->
    <el-result
      v-else-if="errorMessage"
      icon="error"
      title="多设备报告加载失败"
      :sub-title="errorMessage"
    >
      <template #extra>
        <el-button type="primary" @click="fetchReport">重试</el-button>
      </template>
    </el-result>

    <el-empty
      v-else-if="!loading"
      description="暂无多设备报告数据，请确保任务已配置设备并导入日志"
    >
      <el-button type="primary" @click="fetchReport">重新加载</el-button>
      <el-button @click="$emit('go-devices')">前往设备管理</el-button>
    </el-empty>
  </div>
</template>

<script setup>
// UI-16: defineProps 是编译器宏，无需从 vue 导入
import { ref, onMounted, watch } from 'vue'
import { Aim, Monitor, Connection } from '@element-plus/icons-vue'
import api from '@/api'
import { ElMessage } from 'element-plus'
import { useRequest } from '@/composables/useRequest'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const loading = ref(false)
const reportData = ref(null)
// UI-15: 显式记录失败原因，让"空态"与"错误态"不再混为一谈
const errorMessage = ref('')

const emit = defineEmits(['go-devices'])

/**
 * WEB-08: 用 useRequest 包裹多设备报告请求，避免 taskId 频繁切换时
 * 旧任务的报告覆盖新任务的报告。
 */
const { run: runFetchReport } = useRequest(api.getMultiDeviceReport, {
  // UI-15: 把失败原因落到组件状态上，渲染"可重试"的错误态而不是静默的空态
  onError: (e) => {
    errorMessage.value = e?.message || '请求失败，请稍后重试'
  }
})

const fetchReport = async () => {
  if (!props.taskId) return
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await runFetchReport(props.taskId, [])
    if (!res || res.code !== 0) return
    reportData.value = res.data
  } finally {
    loading.value = false
  }
}

const exporting = ref(false)
const exportHTMLReport = async () => {
  if (!props.taskId || exporting.value) return
  exporting.value = true
  try {
    await api.downloadMultiDeviceReport(props.taskId, 'html')
    ElMessage.success('多设备报表导出成功')
  } catch (e) {
    // 错误已由拦截器处理
  } finally {
    exporting.value = false
  }
}

watch(() => props.taskId, (newVal) => {
  if (newVal) {
    fetchReport()
  }
})

onMounted(() => {
  fetchReport()
})

// 供父组件主动刷新（替代 :key 强制重挂载，避免丢失展开状态与滚动位置）
defineExpose({
  refresh: fetchReport
})
</script>

<style scoped>
.multi-device-report-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.report-header-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: #fff;
  padding: 12px 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 16px;
}
.stat-card {
  background: #fff;
  padding: 16px 20px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
  border-left: 4px solid #3b82f6;
}
.stat-card .val {
  font-size: 24px;
  font-weight: bold;
  color: #0f172a;
}
.stat-card .label {
  color: #64748b;
  font-size: 13px;
  margin-top: 4px;
}
.section-card {
  border-radius: 8px;
}
.card-header-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  color: #0f172a;
}
.conclusion-box {
  background: #eff6ff;
  border: 1px solid #bfdbfe;
  border-radius: 6px;
  padding: 16px;
  font-size: 14px;
  color: #1e3a8a;
  white-space: pre-line;
  line-height: 1.6;
}
.devices-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
}
.device-overview-card {
  border: 1px solid #e2e8f0;
  border-top: 4px solid #3b82f6;
  border-radius: 6px;
  padding: 14px;
  background: #fafafa;
}
.device-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
}
.device-name {
  font-weight: 600;
  font-size: 15px;
  color: #0f172a;
}
.meta-row {
  font-size: 12px;
  margin-bottom: 4px;
  display: flex;
  gap: 6px;
}
.meta-row .label {
  color: #64748b;
  width: 70px;
  flex-shrink: 0;
}
.meta-row .val {
  color: #1e293b;
  font-family: monospace;
}
.top-modules-row {
  margin-top: 8px;
  font-size: 12px;
}
.top-modules-row .label {
  color: #64748b;
  margin-bottom: 4px;
  display: block;
}
.modules-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.mod-tag {
  background: #f1f5f9;
  border: 1px solid #e2e8f0;
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 11px;
  color: #475569;
}
.common-events-section {
  margin-bottom: 16px;
}
.sub-title {
  font-size: 13px;
  font-weight: 600;
  color: #475569;
  margin-bottom: 8px;
}
.tags-wrap {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.common-event-tag {
  background: #e0e7ff;
  border: 1px solid #c7d2fe;
  color: #3730a3;
  padding: 4px 10px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 500;
}
.clusters-section {
  margin-top: 14px;
}
.clusters-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.cluster-item-card {
  background: #fffbeb;
  border: 1px solid #fef3c7;
  border-left: 4px solid #f59e0b;
  border-radius: 6px;
  padding: 10px 14px;
}
.cluster-summary {
  font-size: 13px;
  color: #92400e;
  font-weight: 500;
  margin-bottom: 4px;
}
.cluster-devices {
  font-size: 12px;
  color: #78350f;
  display: flex;
  align-items: center;
}
</style>
