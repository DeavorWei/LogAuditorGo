<template>
  <el-dialog
    v-model="visible"
    :title="dialogTitle"
    :width="modalWidth"
    :close-on-click-modal="false"
    :close-on-press-escape="isCompleted || isFailed"
    :show-close="true"
    append-to-body
    class="import-progress-dialog"
    @close="handleClose"
  >
    <div class="progress-modal-container">
      <!-- 1. 顶部状态与综合进度条 -->
      <div class="progress-main-card">
        <div class="progress-status-header">
          <div class="status-left">
            <span class="pulse-indicator" :class="statusClass"></span>
            <span class="stage-name-text">{{ currentStageName }}</span>
          </div>
          <div class="status-right">
            <el-tag :type="tagType" effect="dark" size="small">
              {{ statusText }}
            </el-tag>
          </div>
        </div>

        <div class="progress-bar-wrap">
          <el-progress
            :percentage="percentValue"
            :status="progressStatus"
            :stroke-width="14"
            striped
            :striped-flow="isRunning"
            :duration="20"
          />
        </div>

        <div class="progress-detail-desc">
          <span>{{ snapshot.message || '正在准备执行任务...' }}</span>
          <span v-if="snapshot.total > 0" class="counter-text">
            {{ snapshot.current }} / {{ snapshot.total }}
          </span>
        </div>
      </div>

      <!-- 2. 全阶段步骤流 (Steps) -->
      <div v-if="snapshot.stages && snapshot.stages.length > 0" class="stages-flow-box">
        <el-steps :active="activeStepIndex" finish-status="success" align-center size="small">
          <el-step
            v-for="st in snapshot.stages"
            :key="st.key"
            :title="st.name"
            :status="getStepStatus(st)"
            :description="getStepDescription(st)"
          />
        </el-steps>
      </div>

      <!-- 3. 实时终端控制台日志 (Terminal Logs) -->
      <div class="terminal-container">
        <div class="terminal-header">
          <div class="terminal-title">
            <span class="dot red"></span>
            <span class="dot yellow"></span>
            <span class="dot green"></span>
            <span class="title-text">执行控制台日志流 (Real-time Pipeline Logs)</span>
          </div>
          <div class="terminal-actions">
            <el-checkbox v-model="autoScroll" size="small">自动滚动</el-checkbox>
            <el-button link size="small" @click="clearLogs">清屏</el-button>
          </div>
        </div>
        <div ref="terminalBodyRef" class="terminal-body">
          <div v-if="logs.length === 0" class="terminal-empty">等待日志输出...</div>
          <div
            v-for="(log, idx) in logs"
            :key="idx"
            :class="['log-row', `log-${log.level || 'info'}`]"
          >
            <span class="log-time">[{{ log.timestamp }}]</span>
            <span class="log-msg">{{ log.message }}</span>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="modal-footer">
        <div class="footer-left-hint">
          <span v-if="isRunning" class="hint-text">💡 导入处理已在后台高速执行，您可以关闭窗口后台运行</span>
          <span v-else-if="isCompleted" class="hint-success">🎉 全部阶段已成功完成，正在自动载入最新数据...</span>
          <span v-else-if="isFailed" class="hint-error">❌ 处理出现异常，请查看上方日志排查</span>
        </div>
        <div class="footer-buttons">
          <el-button v-if="isRunning" @click="handleRunInBackground">后台运行</el-button>
          <el-button v-if="isCompleted" type="success" icon="Check" @click="handleFinishNow">
            立即进入工作台
          </el-button>
          <el-button v-if="isFailed" type="danger" @click="handleClose">关闭</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch, onUnmounted, nextTick } from 'vue'
import { Check } from '@element-plus/icons-vue'
import { ElNotification } from 'element-plus'
import api from '@/api'

const props = defineProps({
  modelValue: {
    type: Boolean,
    default: false
  },
  jobId: {
    type: String,
    default: ''
  },
  title: {
    type: String,
    default: ''
  },
  modalWidth: {
    type: String,
    default: '680px'
  },
  autoCloseDelay: {
    type: Number,
    default: 1500
  }
})

const emit = defineEmits(['update:modelValue', 'completed', 'failed', 'closed'])

const visible = ref(props.modelValue)
const snapshot = ref({
  status: 'running',
  current_stage: '',
  stage_index: 0,
  total_stages: 0,
  current: 0,
  total: 0,
  percent: 0,
  message: '',
  stages: [],
  logs: [],
  error: '',
  result: null
})

const logs = ref([])
const autoScroll = ref(true)
const terminalBodyRef = ref(null)

let eventSource = null
let pollTimer = null
let autoCloseTimer = null

const dialogTitle = computed(() => {
  if (props.title) return props.title
  if (snapshot.value.job_type === 'hdx') {
    return '华为官方 HDX 产品文档知识库导入进度'
  }
  return '日志审计与 RCA 根因分析处理流水线'
})

const isRunning = computed(() => snapshot.value.status === 'running')
const isCompleted = computed(() => snapshot.value.status === 'completed')
const isFailed = computed(() => snapshot.value.status === 'failed')

const currentStageName = computed(() => {
  if (isCompleted.value) return '全部处理完成'
  if (isFailed.value) return '处理失败'
  return snapshot.value.current_stage || '处理中...'
})

const percentValue = computed(() => {
  const p = Math.round((snapshot.value.percent || 0) * 10) / 10
  return Math.min(100, Math.max(0, p))
})

const statusClass = computed(() => {
  if (isCompleted.value) return 'status-completed'
  if (isFailed.value) return 'status-failed'
  return 'status-running'
})

const tagType = computed(() => {
  if (isCompleted.value) return 'success'
  if (isFailed.value) return 'danger'
  return 'primary'
})

const statusText = computed(() => {
  if (isCompleted.value) return '已完成'
  if (isFailed.value) return '处理失败'
  return `${percentValue.value}%`
})

const progressStatus = computed(() => {
  if (isCompleted.value) return 'success'
  if (isFailed.value) return 'exception'
  return ''
})

const activeStepIndex = computed(() => {
  if (isCompleted.value) {
    return (snapshot.value.stages?.length || 0) + 1
  }
  return snapshot.value.stage_index >= 0 ? snapshot.value.stage_index : 0
})

const getStepStatus = (st) => {
  if (st.status === 'completed') return 'success'
  if (st.status === 'running') return 'process'
  if (st.status === 'failed') return 'error'
  return 'wait'
}

const getStepDescription = (st) => {
  if (st.duration_ms > 0) {
    if (st.duration_ms < 1000) return `${st.duration_ms}ms`
    return `${(st.duration_ms / 1000).toFixed(1)}s`
  }
  if (st.status === 'running' && st.total > 0) {
    return `${st.current}/${st.total}`
  }
  return ''
}

// 启动连接与监听
const startTracking = (jobId) => {
  stopTracking()
  if (!jobId) return

  // 1. 初始化 SSE
  // 1. 初始化 SSE
  try {
    eventSource = api.createProgressStream(
      jobId,
      (data) => {
        handleSnapshotUpdate(data)
      },
      (err) => {
        console.warn('[ImportProgressModal] SSE connection interrupted, falling back to HTTP polling:', err)
        // 显式断开断线/404 的 SSE，避免原生 EventSource 不断在后台重连请求 (L-15, H-09)
        if (eventSource) {
          eventSource.close()
          eventSource = null
        }
        startPolling(jobId)
      }
    )
  } catch (e) {
    startPolling(jobId)
  }

  // 2. 发起一次即时 HTTP 查询作为首屏快速呈现
  api.getProgress(jobId).then((res) => {
    if (res.code === 0 && res.data) {
      handleSnapshotUpdate(res.data)
    }
  }).catch((e) => {
    if (e.response?.status === 404) {
      handleJobFailed('任务进度不存在或已过期')
    }
  })
}

let pollFailCount = 0
const isBackgroundRunning = ref(false)

const startPolling = (jobId) => {
  if (pollTimer) return
  pollFailCount = 0
  pollTimer = setInterval(async () => {
    try {
      const res = await api.getProgress(jobId)
      if (res.code === 0 && res.data) {
        pollFailCount = 0
        handleSnapshotUpdate(res.data)
        if (res.data.status === 'completed' || res.data.status === 'failed') {
          stopPolling()
        }
      } else {
        pollFailCount++
        if (pollFailCount >= 3) {
          handleJobFailed('进度查询异常或任务未找到')
        }
      }
    } catch (e) {
      pollFailCount++
      if (pollFailCount >= 3 || e.response?.status === 404) {
        handleJobFailed('任务进度已失效或网络中断')
      }
    }
  }, 800)
}

const handleJobFailed = (reason) => {
  stopTracking()
  snapshot.value = {
    ...snapshot.value,
    status: 'failed',
    error: reason
  }
  emit('failed', reason)
}

const stopPolling = () => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

const stopTracking = () => {
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
  stopPolling()
  if (autoCloseTimer) {
    clearTimeout(autoCloseTimer)
    autoCloseTimer = null
  }
}

const handleSnapshotUpdate = (data) => {
  if (!data) return
  snapshot.value = data
  if (data.logs && data.logs.length > 0) {
    logs.value = data.logs
    scrollTerminalToBottom()
  }

  // 检查是否完成
  if (data.status === 'completed') {
    stopTracking()
    if (isBackgroundRunning.value) {
      ElNotification({
        title: '审计分析完成',
        message: `后台任务【${props.title || '日志分析'}】已处理完毕，工作台已同步刷新`,
        type: 'success',
        duration: 4000
      })
      isBackgroundRunning.value = false
    }
    emit('completed', data.result || data)
    // 延时自动关闭
    if (props.autoCloseDelay > 0) {
      autoCloseTimer = setTimeout(() => {
        handleClose()
      }, props.autoCloseDelay)
    }
  } else if (data.status === 'failed') {
    stopTracking()
    if (isBackgroundRunning.value) {
      ElNotification({
        title: '分析失败',
        message: `后台任务【${props.title || '日志分析'}】处理失败: ${data.error || '未知错误'}`,
        type: 'error',
        duration: 5000
      })
      isBackgroundRunning.value = false
    }
    emit('failed', data.error || data.message)
  }
}

const scrollTerminalToBottom = () => {
  if (!autoScroll.value) return
  nextTick(() => {
    if (terminalBodyRef.value) {
      terminalBodyRef.value.scrollTop = terminalBodyRef.value.scrollHeight
    }
  })
}

const clearLogs = () => {
  logs.value = []
}

// 用户点击“后台运行”：隐藏弹窗，保持后台跟踪监听并在完成时推送 (H-08)
const handleRunInBackground = () => {
  isBackgroundRunning.value = true
  visible.value = false
  emit('update:modelValue', false)
  emit('closed')
}

const handleFinishNow = () => {
  if (autoCloseTimer) clearTimeout(autoCloseTimer)
  emit('completed', snapshot.value.result || snapshot.value)
  handleClose()
}

const handleClose = () => {
  isBackgroundRunning.value = false
  stopTracking()
  visible.value = false
  emit('update:modelValue', false)
  emit('closed')
}

watch(
  () => props.modelValue,
  (val) => {
    visible.value = val
    if (val) {
      isBackgroundRunning.value = false
      if (props.jobId) {
        startTracking(props.jobId)
      }
    } else if (!isBackgroundRunning.value) {
      stopTracking()
    }
  }
)

watch(
  () => props.jobId,
  (newJobId) => {
    if (visible.value && newJobId) {
      startTracking(newJobId)
    }
  }
)

onUnmounted(() => {
  stopTracking()
})
</script>

<style scoped>
.import-progress-dialog :deep(.el-dialog__body) {
  padding: 16px 20px;
}

.progress-modal-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

/* 顶部进度主卡片 */
.progress-main-card {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 14px 16px;
}

.progress-status-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
}

.status-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.pulse-indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
}

.status-running {
  background-color: #0284c7;
  box-shadow: 0 0 0 0 rgba(2, 132, 199, 0.7);
  animation: pulse-blue 1.6s infinite;
}

.status-completed {
  background-color: #16a34a;
  box-shadow: 0 0 8px rgba(22, 163, 74, 0.5);
}

.status-failed {
  background-color: #dc2626;
  box-shadow: 0 0 8px rgba(220, 38, 38, 0.5);
}

@keyframes pulse-blue {
  0% {
    transform: scale(0.95);
    box-shadow: 0 0 0 0 rgba(2, 132, 199, 0.7);
  }
  70% {
    transform: scale(1);
    box-shadow: 0 0 0 8px rgba(2, 132, 199, 0);
  }
  100% {
    transform: scale(0.95);
    box-shadow: 0 0 0 0 rgba(2, 132, 199, 0);
  }
}

.stage-name-text {
  font-size: 15px;
  font-weight: 600;
  color: #1e293b;
}

.progress-bar-wrap {
  margin: 6px 0;
}

.progress-detail-desc {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: #64748b;
  margin-top: 6px;
}

.counter-text {
  font-family: monospace;
  font-weight: 600;
  color: #0284c7;
}

/* 阶段步骤条 */
.stages-flow-box {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 14px 10px 10px 10px;
}

/* 控制台终端 */
.terminal-container {
  background: #0f172a;
  border-radius: 8px;
  border: 1px solid #1e293b;
  overflow: hidden;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.terminal-header {
  background: #1e293b;
  padding: 6px 12px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid #334155;
}

.terminal-title {
  display: flex;
  align-items: center;
  gap: 6px;
}

.terminal-title .dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.dot.red { background: #ef4444; }
.dot.yellow { background: #f59e0b; }
.dot.green { background: #10b981; }

.title-text {
  font-size: 11px;
  color: #94a3b8;
  font-family: monospace;
  margin-left: 4px;
}

.terminal-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.terminal-actions :deep(.el-checkbox__label) {
  color: #94a3b8;
  font-size: 11px;
}

.terminal-body {
  height: 150px;
  overflow-y: auto;
  padding: 8px 12px;
  font-family: 'Consolas', 'Fira Code', monospace;
  font-size: 12px;
  line-height: 1.6;
  color: #f1f5f9;
}

.terminal-empty {
  color: #475569;
  font-style: italic;
  padding: 10px 0;
  text-align: center;
}

.log-row {
  display: flex;
  gap: 8px;
  word-break: break-all;
}

.log-time {
  color: #64748b;
  flex-shrink: 0;
}

.log-info .log-msg { color: #e2e8f0; }
.log-success .log-msg { color: #4ade80; font-weight: 500; }
.log-warning .log-msg { color: #fbbf24; }
.log-error .log-msg { color: #f87171; font-weight: 500; }

/* 底部操作区 */
.modal-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}

.footer-left-hint {
  font-size: 12px;
}

.hint-text {
  color: #64748b;
}
.hint-success {
  color: #16a34a;
  font-weight: 500;
}
.hint-error {
  color: #dc2626;
  font-weight: 500;
}

.footer-buttons {
  display: flex;
  gap: 8px;
}
</style>
