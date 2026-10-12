import { onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'

/**
 * RCA 异步分析状态自适应轮询与生命周期管理
 */
export function useRcaPolling(options = {}) {
  const { onTaskUpdated = () => {}, onRcaCompleted = () => {} } = options

  let rcaPollTimer = null
  let rcaPollFailCount = 0

  const stopRcaPolling = () => {
    if (rcaPollTimer) {
      clearInterval(rcaPollTimer)
      rcaPollTimer = null
    }
    rcaPollFailCount = 0
  }

  const checkAndPollRcaStatus = (currentTask, currentTaskId) => {
    stopRcaPolling()
    if (
      currentTask &&
      (currentTask.rca_status === 'QUEUED' || currentTask.rca_status === 'RUNNING')
    ) {
      rcaPollTimer = setInterval(async () => {
        const pollingId = typeof currentTaskId === 'function' ? currentTaskId() : currentTaskId
        if (!pollingId) return
        try {
          const res = await api.getTask(pollingId)
          rcaPollFailCount = 0
          const currentIdNow = typeof currentTaskId === 'function' ? currentTaskId() : currentTaskId
          if (pollingId !== currentIdNow) return
          if (res && res.code === 0) {
            onTaskUpdated(res.data)
            if (res.data.rca_status !== 'QUEUED' && res.data.rca_status !== 'RUNNING') {
              stopRcaPolling()
              await onRcaCompleted(pollingId)
            }
          }
        } catch (e) {
          rcaPollFailCount++
          if (rcaPollFailCount >= 5) {
            stopRcaPolling()
            ElMessage.warning('轮询任务状态失败次数过多，已暂停自动轮询')
          }
        }
      }, 3000)
    }
  }

  onUnmounted(() => {
    stopRcaPolling()
  })

  return {
    stopRcaPolling,
    checkAndPollRcaStatus
  }
}

export default useRcaPolling
