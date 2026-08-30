import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import api from '../api'

/**
 * 全局长任务追踪 store (UI-01)。
 *
 * 背景：进度弹窗原本是"局部组件状态"。用户在任务运行中点 X 关闭后，
 * SSE 与轮询全部停止，而服务端其实还在跑——
 * 结果就是"长任务结果永久丢失，且没有任何入口可以回看"。
 *
 * 这里把"正在运行的任务"提升为全局状态：
 *  - 顶栏可以常驻一个徽标，点击即可重新拉起进度弹窗；
 *  - 弹窗关闭只影响 UI 呈现，不会中断任务本身。
 */
export const useProgressStore = defineStore('progress', () => {
  /** 当前正在追踪的作业 */
  const activeJobs = ref(new Map())
  /** 当前正在弹窗中展示的 jobId；为空表示弹窗关闭但任务可能仍在后台跑 */
  const visibleJobId = ref('')
  /** 弹窗可见性 */
  const dialogVisible = ref(false)

  const runningJobs = computed(() =>
    Array.from(activeJobs.value.values()).filter(
      (j) => j.status === 'running' || j.status === 'pending'
    )
  )
  const runningCount = computed(() => runningJobs.value.length)
  const hasRunningJob = computed(() => runningCount.value > 0)

  const visibleJob = computed(() =>
    visibleJobId.value ? activeJobs.value.get(visibleJobId.value) || null : null
  )

  const openDialog = (jobId) => {
    if (jobId) visibleJobId.value = jobId
    dialogVisible.value = true
  }

  /**
   * closeDialog 只隐藏弹窗，不终止任务 (UI-01)。
   * 任务仍保留在 activeJobs 中，顶栏徽标会持续提示并可一键拉回。
   */
  const closeDialog = () => {
    dialogVisible.value = false
  }

  const registerJob = (jobId, meta = {}) => {
    if (!jobId) return
    activeJobs.value.set(jobId, {
      jobId,
      status: 'running',
      percent: 0,
      message: meta.message || '任务已启动',
      taskId: meta.taskId || '',
      jobType: meta.jobType || '',
      stage: '',
      startedAt: Date.now()
    })
    openDialog(jobId)
  }

  const updateJob = (jobId, patch = {}) => {
    const existing = activeJobs.value.get(jobId)
    if (!existing) return
    activeJobs.value.set(jobId, { ...existing, ...patch })
  }

  const finishJob = (jobId, status, message) => {
    const existing = activeJobs.value.get(jobId)
    if (!existing) return
    activeJobs.value.set(jobId, {
      ...existing,
      status,
      percent: status === 'completed' ? 100 : existing.percent,
      message: message || existing.message,
      finishedAt: Date.now()
    })
  }

  /**
   * 终止任务 (UI-02)。
   * 后端是协作式取消：调用后服务端会在当前阶段结束后停止。
   */
  const cancelJob = async (jobId) => {
    try {
      await api.cancelProgress(jobId)
      updateJob(jobId, { message: '已发送终止请求，任务将在当前阶段结束后停止...' })
      return true
    } catch (e) {
      return false
    }
  }

  /**
   * 清理已完成/失败的作业记录，避免 Map 无界增长。
   * 保留最近 finishedTTL 毫秒内的终态记录，供顶栏展示"刚刚完成"。
   */
  const prune = (finishedTTL = 5 * 60 * 1000) => {
    const now = Date.now()
    for (const [id, job] of activeJobs.value.entries()) {
      if (job.status === 'completed' || job.status === 'failed') {
        if (job.finishedAt && now - job.finishedAt > finishedTTL) {
          activeJobs.value.delete(id)
          if (visibleJobId.value === id) visibleJobId.value = ''
        }
      }
    }
  }

  const reset = () => {
    activeJobs.value = new Map()
    visibleJobId.value = ''
    dialogVisible.value = false
  }

  return {
    activeJobs,
    visibleJobId,
    dialogVisible,
    runningJobs,
    runningCount,
    hasRunningJob,
    visibleJob,
    openDialog,
    closeDialog,
    registerJob,
    updateJob,
    finishJob,
    cancelJob,
    prune,
    reset
  }
})

export default useProgressStore
