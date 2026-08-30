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
          <span class="counter-text-group">
            <span v-if="isRunning" class="elapsed-text">已运行 {{ elapsedText }}</span>
            <span v-if="snapshot.total > 0" class="counter-text">
              {{ snapshot.current }} / {{ snapshot.total }}
            </span>
          </span>
        </div>

        <!-- 批量处理时的整体进度：第 N/M 个 -->
        <div v-if="snapshot.overall_label" class="progress-overall">
          <span class="overall-icon">📦</span>
          <span class="overall-text">{{ snapshot.overall_label }}</span>
        </div>
      </div>

      <!-- 2. 全阶段步骤流 -->
      <div v-if="snapshot.stages && snapshot.stages.length > 0" class="stages-flow-box">
        <div class="stages-row">
          <div
            v-for="(st, idx) in snapshot.stages"
            :key="st.key"
            class="stage-item"
            :class="`stage-${getStepStatus(st)}`"
          >
            <div class="stage-head">
              <span
                class="stage-line"
                :class="{ 'is-hidden': idx === 0 }"
              ></span>
              <span class="stage-bullet">
                <el-icon v-if="getStepStatus(st) === 'success'" class="bullet-icon"><Check /></el-icon>
                <el-icon v-else-if="getStepStatus(st) === 'error'" class="bullet-icon"><CloseBold /></el-icon>
                <span v-else class="bullet-index">{{ idx + 1 }}</span>
              </span>
              <span
                class="stage-line"
                :class="{ 'is-hidden': idx === snapshot.stages.length - 1 }"
              ></span>
            </div>
            <div class="stage-name">{{ st.name }}</div>
            <div class="stage-meta">{{ getStepDescription(st) }}</div>
          </div>
        </div>
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
          <span v-if="isCanceling" class="hint-warn">⏹ 正在发送终止请求...</span>
          <span v-else-if="isRunning" class="hint-text">💡 任务在后台持续执行，您可以关闭窗口；关闭后可从顶栏任务徽标随时回到此窗口</span>
          <span v-else-if="isCompleted" class="hint-success">🎉 全部阶段已成功完成，正在自动载入最新数据...</span>
          <span v-else-if="isFailed" class="hint-error">❌ 处理出现异常，请查看上方日志排查</span>
        </div>
        <div class="footer-buttons">
          <!-- UI-02: 长耗时任务可主动终止，不再只能干等或强杀进程 -->
          <el-popconfirm
            v-if="isRunning"
            title="确定要终止该任务吗？服务端会在当前阶段结束后停止。"
            confirm-button-text="终止任务"
            cancel-button-text="继续等待"
            confirm-button-type="danger"
            @confirm="handleCancelJob"
          >
            <template #reference>
              <el-button type="danger" plain :loading="isCanceling">终止任务</el-button>
            </template>
          </el-popconfirm>
          <el-button v-if="isRunning" @click="handleRunInBackground">后台运行</el-button>
          <el-button v-if="isCompleted" type="success" icon="Check" @click="handleFinishNow">
            立即进入工作台
          </el-button>
          <!-- UX: 失败态提供重试与复制错误日志的快捷入口 -->
          <el-button v-if="isFailed" @click="copyErrorLog">复制错误日志</el-button>
          <el-button v-if="isFailed" type="danger" @click="handleClose">关闭</el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { Check, CloseBold } from '@element-plus/icons-vue'
import { ElMessage, ElNotification } from 'element-plus'
import api from '@/api'
import { useProgressStore } from '@/stores/progress'

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

// UI-01: 接入全局长任务追踪 store，让顶栏徽标感知到"这个任务还在跑"
const progressStore = useProgressStore()

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

// UI-02: 终止任务的进行中标记
const isCanceling = ref(false)
// UX: 已运行时长
const elapsedSeconds = ref(0)

let eventSource = null
let pollTimer = null
let autoCloseTimer = null
// UI-03: 在途 HTTP 轮询请求的取消控制器
let pollAbortController = null
// UI-03: 终态幂等标记。
// SSE 与即时 HTTP 查询会同时回填同一份快照，旧实现会让 completed 被 emit 两次，
// 父组件随之重复刷新列表并弹出两条一模一样的 Toast。
let settled = false
let startTimestamp = 0
let elapsedTimer = null

const elapsedText = computed(() => {
  const s = elapsedSeconds.value
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  return `${m}m${String(s % 60).padStart(2, '0')}s`
})

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

  // 重置本轮追踪的终态标记
  settled = false
  startTimestamp = Date.now()
  elapsedSeconds.value = 0
  startElapsedTimer()

  // UI-01: 登记到全局 store，顶栏据此展示"运行中任务"徽标
  progressStore.registerJob(jobId, { message: props.title || '任务处理中' })

  // 1. 初始化 SSE
  try {
    eventSource = api.createProgressStream(
      jobId,
      (data) => {
        handleSnapshotUpdate(data)
      },
      (err) => {
        // UI-16: 原实现在此处有两条完全相同的 `// 1. 初始化 SSE` 注释，属于复制粘贴残留
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

  // 2. 发起一次即时 HTTP 查询作为首屏快速呈现。
  //    UI-03: 传入 signal，弹窗关闭/任务切换时可立即取消在途请求，
  //    避免"旧请求的响应覆盖新任务的状态"。
  fetchProgressOnce(jobId)
}

// 轮询退避参数 (UI-10)。
//
// 旧实现固定 800ms 间隔，且"连续 3 次失败（约 2.4s）就判定任务失败"——
// 一次短暂的网络抖动，或一个还没来得及注册完成的 job，都会被误判为失败。
// 这里改为指数退避 800ms → 1s → 2s → 4s（上限 5s），并配合总超时 30min，
// 只有真正拿不到进度且持续超时才判失败。
const POLL_BASE_DELAY = 800
const POLL_MAX_DELAY = 5000
const POLL_MAX_FAILS = 8

let pollFailCount = 0
let pollDelay = POLL_BASE_DELAY
const isBackgroundRunning = ref(false)

const startElapsedTimer = () => {
  stopElapsedTimer()
  elapsedTimer = setInterval(() => {
    if (!startTimestamp) return
    elapsedSeconds.value = Math.floor((Date.now() - startTimestamp) / 1000)
  }, 1000)
}

const stopElapsedTimer = () => {
  if (elapsedTimer) {
    clearInterval(elapsedTimer)
    elapsedTimer = null
  }
}

const fetchProgressOnce = async (jobId) => {
  if (pollAbortController) {
    pollAbortController.abort()
  }
  pollAbortController = new AbortController()
  try {
    const res = await api.getProgress(jobId, { signal: pollAbortController.signal })
    if (res?.code === 0 && res.data) {
      handleSnapshotUpdate(res.data)
    }
  } catch (e) {
    // 主动取消不视为失败
    if (e?.name === 'CanceledError' || e?.code === 'ERR_CANCELED') return
    if (e?.response?.status === 404 && !settled) {
      handleJobFailed('任务进度不存在或已过期')
    }
  }
}

const startPolling = (jobId) => {
  if (pollTimer) return
  pollFailCount = 0
  pollDelay = POLL_BASE_DELAY

  const tick = async () => {
    try {
      if (pollAbortController) pollAbortController.abort()
      pollAbortController = new AbortController()
      const res = await api.getProgress(jobId, { signal: pollAbortController.signal })
      if (res?.code === 0 && res.data) {
        pollFailCount = 0
        pollDelay = POLL_BASE_DELAY
        handleSnapshotUpdate(res.data)
        return
      }
      pollFailCount++
    } catch (e) {
      if (e?.name === 'CanceledError' || e?.code === 'ERR_CANCELED') return
      pollFailCount++
      // 404 说明 job 已被清理，无需继续退避重试
      if (e?.response?.status === 404) {
        stopPolling()
        handleJobFailed('任务进度已失效或已被清理')
        return
      }
    }
    if (pollFailCount >= POLL_MAX_FAILS) {
      stopPolling()
      handleJobFailed('进度查询持续失败，请检查服务是否可用')
    }
  }

  // 指数退避：失败次数越多，下一次间隔越长，避免在网络抖动期间高频打服务端
  const scheduleNext = () => {
    const delay = pollFailCount > 0
      ? Math.min(POLL_MAX_DELAY, POLL_BASE_DELAY * Math.pow(2, Math.min(pollFailCount, 3)))
      : POLL_BASE_DELAY
    pollTimer = setTimeout(async () => {
      pollTimer = null
      await tick()
      if (pollTimer === null && !settled) scheduleNext()
    }, delay)
  }
  scheduleNext()
}

const handleJobFailed = (reason) => {
  // UI-03: 幂等——已终态时不再重复置失败、不再重复 emit
  if (settled) return
  settled = true
  stopTracking()
  if (props.jobId) progressStore.finishJob(props.jobId, 'failed', reason)
  snapshot.value = {
    ...snapshot.value,
    status: 'failed',
    error: reason
  }
  emit('failed', reason)
}

const stopPolling = () => {
  if (pollTimer) {
    clearTimeout(pollTimer)
    pollTimer = null
  }
  // UI-03: 取消仍在途的轮询请求，杜绝"响应回来时任务已经切换"
  if (pollAbortController) {
    pollAbortController.abort()
    pollAbortController = null
  }
}

const stopTracking = () => {
  if (eventSource) {
    eventSource.close()
    eventSource = null
  }
  stopPolling()
  stopElapsedTimer()
  if (autoCloseTimer) {
    clearTimeout(autoCloseTimer)
    autoCloseTimer = null
  }
}

const handleSnapshotUpdate = (data) => {
  if (!data) return

  // UI-03: 终态幂等——SSE 与 HTTP 轮询会重复投递同一份终态快照，
  // 旧实现会让 completed 被 emit 两次，父组件重复刷新并弹出重复 Toast。
  if (settled) return

  snapshot.value = data
  if (data.logs && data.logs.length > 0) {
    logs.value = data.logs
    scrollTerminalToBottom()
  }

  // 检查是否完成
  if (data.status === 'completed') {
    settled = true
    // UI-01: 同步全局状态，顶栏徽标随即消失
    if (props.jobId) progressStore.finishJob(props.jobId, 'completed', data.message || '已完成')
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
    // 只 emit 一次
    emit('completed', data.result || data)
    // 延时自动关闭（幂等：重复进入也不会重复触发）
    if (props.autoCloseDelay > 0 && !autoCloseTimer) {
      autoCloseTimer = setTimeout(() => {
        handleClose()
      }, props.autoCloseDelay)
    }
  } else if (data.status === 'failed') {
    settled = true
    if (props.jobId) progressStore.finishJob(props.jobId, 'failed', data.error || '处理失败')
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

/**
 * 终止任务 (UI-02)。
 *
 * 后端是协作式取消：接口返回只代表"取消请求已受理"，
 * 真正的停止由业务循环在下一个阶段检查点完成，
 * 因此这里不立即置终态，而是继续追踪直到服务端把状态置为 failed。
 */
const handleCancelJob = async () => {
  if (!props.jobId || isCanceling.value) return
  isCanceling.value = true
  try {
    const ok = await api.cancelProgress(props.jobId)
    if (ok) {
      ElNotification({
        title: '已请求终止',
        message: '服务端将在当前处理阶段结束后停止任务',
        type: 'warning',
        duration: 3000
      })
      // 立即拉一次进度，尽快把状态刷新为已终止
      await fetchProgressOnce(props.jobId)
    }
  } catch (e) {
    // 409 表示任务已结束，无需再终止
    if (e?.response?.status !== 409) {
      ElMessage.error(e?.response?.data?.message || '终止任务失败，请稍后重试')
    }
  } finally {
    isCanceling.value = false
  }
}

// UX: 失败态一键复制错误日志，便于直接贴到工单
const copyErrorLog = async () => {
  const text = [
    `任务: ${props.title || '日志分析'}`,
    `JobID: ${props.jobId}`,
    `错误: ${snapshot.value.error || '未知错误'}`,
    '--- 日志 ---',
    ...(logs.value || []).map((l) => `[${l.timestamp}] [${l.level}] ${l.message}`)
  ].join('\n')
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('错误日志已复制到剪贴板')
  } catch (e) {
    ElMessage.warning('当前环境不支持自动复制，请手动选中日志内容')
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
//
// UI-01: 关闭窗口**不再停止追踪**——旧实现的 handleClose 会直接 stopTracking()，
// 运行中点 X 就等于放弃了这个任务，长任务结果永久丢失且无处回看。
// 现在"后台运行"与"关闭窗口"行为一致：继续追踪，完成时推送通知。
const handleRunInBackground = () => {
  isBackgroundRunning.value = true
  visible.value = false
  emit('update:modelValue', false)
  emit('closed')
}

const handleFinishNow = () => {
  if (autoCloseTimer) {
    clearTimeout(autoCloseTimer)
    autoCloseTimer = null
  }
  // UI-03: completed 已在 handleSnapshotUpdate 中 emit 过一次，
  // 这里不再重复触发，否则父组件会二次刷新列表并弹出重复 Toast。
  handleClose()
}

const handleClose = () => {
  // UI-01: 任务仍在运行时关闭弹窗 = 转入后台运行，追踪继续，
  // 完成后仍会推送通知；只有任务已终态时才真正停止追踪。
  if (isRunning.value) {
    handleRunInBackground()
    return
  }
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

/**
 * UI-01: 响应顶栏徽标的"重新打开进度窗口"请求。
 * 只有当前追踪的正是该 jobId 的实例才响应，
 * 避免多个视图的弹窗实例被同时拉起。
 */
const handleReopenRequest = (event) => {
  const jobId = event?.detail?.jobId
  if (!jobId || jobId !== props.jobId) return
  if (!visible.value) {
    visible.value = true
    emit('update:modelValue', true)
  }
}

onMounted(() => {
  window.addEventListener('logauditorgo:reopen-progress', handleReopenRequest)
})

onUnmounted(() => {
  window.removeEventListener('logauditorgo:reopen-progress', handleReopenRequest)
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

.counter-text-group {
  display: inline-flex;
  align-items: center;
  gap: 10px;
}

.counter-text {
  font-family: monospace;
  font-weight: 600;
  color: #0284c7;
}

.elapsed-text {
  color: #94a3b8;
  font-family: monospace;
}

/* 批量处理的整体进度：第 N/M 个 */
.progress-overall {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed #cbd5e1;
}

.overall-icon {
  font-size: 13px;
  line-height: 1;
}

.overall-text {
  font-size: 12px;
  font-weight: 600;
  color: #0284c7;
}

/* 全阶段步骤流 */
.stages-flow-box {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 16px 12px 12px 12px;
}

.stages-row {
  display: flex;
  align-items: flex-start;
}

.stage-item {
  flex: 1;
  min-width: 0;
  text-align: center;
}

.stage-head {
  display: flex;
  align-items: center;
}

.stage-line {
  flex: 1;
  height: 2px;
  background: #e2e8f0;
}

.stage-line.is-hidden {
  visibility: hidden;
}

.stage-bullet {
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #e2e8f0;
  color: #94a3b8;
  font-size: 11px;
  font-weight: 600;
}

.bullet-icon {
  font-size: 12px;
}

/*
 * 阶段名与耗时行均采用固定高度：
 * 无论阶段名是一行还是换行成两行，各列总高度都保持一致，
 * 从而避免长名称换行后因列高差产生多余的空白行。
 */
.stage-name {
  height: 32px;
  line-height: 16px;
  margin-top: 8px;
  padding: 0 2px;
  font-size: 12px;
  color: #94a3b8;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  word-break: break-all;
}

.stage-meta {
  height: 15px;
  line-height: 15px;
  margin-top: 2px;
  font-size: 11px;
  color: #94a3b8;
  white-space: nowrap;
  overflow: hidden;
}

.stage-process .stage-bullet {
  background: #0284c7;
  color: #ffffff;
  box-shadow: 0 0 0 3px rgba(2, 132, 199, 0.18);
}

.stage-process .stage-name {
  color: #0284c7;
  font-weight: 600;
}

.stage-success .stage-bullet {
  background: #16a34a;
  color: #ffffff;
}

.stage-success .stage-name {
  color: #16a34a;
}

.stage-error .stage-bullet {
  background: #dc2626;
  color: #ffffff;
}

.stage-error .stage-name {
  color: #dc2626;
  font-weight: 600;
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
.hint-warn {
  color: #b45309;
  font-weight: 500;
}

.footer-buttons {
  display: flex;
  gap: 8px;
}
</style>
