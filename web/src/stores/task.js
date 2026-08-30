import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import api from '../api'

/**
 * 任务工作台的共享状态 (WEB-05)。
 *
 * 背景：Pinia 早已注册但零 store，任务列表 / 当前任务 / 文件 / 设备 / 日志 / RCA
 * 全部散落在 AuditWorkbench.vue 的 20+ 个 ref 里，
 * Tasks ↔ AuditWorkbench ↔ Dashboard 之间无法共享，每次进页面都要全量重拉。
 *
 * 这里把它们上收到 store：
 *  - 任何视图切换回来都能直接命中缓存；
 *  - fetchRCA 时预解析 correlated_log_ids 并建立 Map<logId, rcaEvent> 索引 (WEB-13)，
 *    避免模板里对每条日志都做一次 JSON.parse 线性查找。
 */
export const useTaskStore = defineStore('task', () => {
  const taskList = ref([])
  const currentTaskId = ref('')
  const currentTask = ref(null)
  const taskFiles = ref([])
  const taskDevices = ref([])
  const logRecords = ref([])
  const logTotal = ref(0)
  const rcaEvents = ref([])

  const loading = ref({
    tasks: false,
    files: false,
    devices: false,
    logs: false,
    rca: false
  })

  // WEB-13: logId -> rcaEvent 的预建索引。
  // 旧实现在模板里 `rcaEvents.find(ev => JSON.parse(ev.correlated_log_ids)...)`，
  // 每次渲染对所有 RCA 事件做一次全量 JSON 解析，日志列表一长就明显卡顿。
  const rcaIndexByLogId = ref(new Map())

  const currentTaskName = computed(() => currentTask.value?.task_name || '')
  const hasTask = computed(() => !!currentTaskId.value)
  const deviceOptions = computed(() =>
    taskDevices.value.map((d) => ({
      id: d.id,
      name: d.device_name,
      hostname: d.hostname,
      color: d.color
    }))
  )

  const buildRcaIndex = (events) => {
    const map = new Map()
    for (const ev of events || []) {
      let ids = []
      try {
        ids = JSON.parse(ev.correlated_log_ids || '[]')
      } catch (e) {
        ids = []
      }
      ids.forEach((id) => {
        if (!map.has(id)) map.set(id, ev)
      })
      if (ev.root_log_id) {
        map.set(ev.root_log_id, ev)
      }
    }
    return map
  }

  const fetchTasks = async () => {
    loading.value.tasks = true
    try {
      const res = await api.getTasks()
      taskList.value = res?.data || []
      return taskList.value
    } finally {
      loading.value.tasks = false
    }
  }

  const fetchTaskFiles = async (taskId) => {
    loading.value.files = true
    try {
      const res = await api.getTaskFiles(taskId)
      taskFiles.value = res?.data || []
      return taskFiles.value
    } finally {
      loading.value.files = false
    }
  }

  const fetchTaskDevices = async (taskId) => {
    loading.value.devices = true
    try {
      const res = await api.getDevices(taskId)
      taskDevices.value = res?.data || []
      return taskDevices.value
    } finally {
      loading.value.devices = false
    }
  }

  const fetchRCA = async (taskId) => {
    loading.value.rca = true
    try {
      const res = await api.getTaskRCA(taskId)
      rcaEvents.value = res?.data || []
      rcaIndexByLogId.value = buildRcaIndex(rcaEvents.value)
      return rcaEvents.value
    } finally {
      loading.value.rca = false
    }
  }

  const setLogs = (records, total) => {
    logRecords.value = records || []
    if (typeof total === 'number') logTotal.value = total
  }

  /**
   * 切换任务时并行拉取四个维度的数据 (WEB-12)。
   *
   * 旧实现是 `await files; await devices; await logs; await rca` 四次串行往返，
   * 一次切换要等四个 RTT。这里改为 Promise.all 并发，
   * 但仍把 logs 放在最后以保证"自动选中首条日志"的逻辑拿到最终数据。
   */
  const switchTask = async (taskId, fetchLogsFn) => {
    currentTaskId.value = taskId || ''
    currentTask.value = taskList.value.find((t) => t.task_id === taskId) || null
    if (!taskId) {
      taskFiles.value = []
      taskDevices.value = []
      logRecords.value = []
      logTotal.value = 0
      rcaEvents.value = []
      rcaIndexByLogId.value = new Map()
      return
    }

    await Promise.all([
      fetchTaskFiles(taskId),
      fetchTaskDevices(taskId),
      fetchRCA(taskId)
    ])
    if (typeof fetchLogsFn === 'function') {
      await fetchLogsFn()
    }
  }

  const rcaOfLog = (logId) => rcaIndexByLogId.value.get(logId) || null

  const reset = () => {
    taskList.value = []
    currentTaskId.value = ''
    currentTask.value = null
    taskFiles.value = []
    taskDevices.value = []
    logRecords.value = []
    logTotal.value = 0
    rcaEvents.value = []
    rcaIndexByLogId.value = new Map()
  }

  return {
    taskList,
    currentTaskId,
    currentTask,
    taskFiles,
    taskDevices,
    logRecords,
    logTotal,
    rcaEvents,
    rcaIndexByLogId,
    loading,
    currentTaskName,
    hasTask,
    deviceOptions,
    fetchTasks,
    fetchTaskFiles,
    fetchTaskDevices,
    fetchRCA,
    setLogs,
    switchTask,
    rcaOfLog,
    reset
  }
})

export default useTaskStore
