import axios from 'axios'
import { ElMessage } from 'element-plus'

const request = axios.create({
  baseURL: '/api/v1',
  // WEB-17: 原实现所有接口共用 300s 超时，连 /system/stats 这种毫秒级接口
  // 也要等 5 分钟才判定失败。这里按接口类别设置差异化的超时。
  timeout: 30000
})

// 长耗时操作（导入 / 重分析 / 文档导入）单独放宽超时
const LONG_TIMEOUT = 300000
// 导出可能涉及百万行流式生成，给到 10 分钟
const EXPORT_TIMEOUT = 600000

/**
 * 统一的请求配置合并。
 * 调用方可传入 { signal }（AbortController）实现请求取消 (WEB-08)。
 */
const withTimeout = (timeoutMs, extra = {}) => ({ timeout: timeoutMs, ...extra })

request.interceptors.response.use(
  response => {
    const res = response.data
    if (res && typeof res.code === 'number' && res.code !== 0) {
      const msg = res.message || '操作失败'
      ElMessage.error(msg)
      return Promise.reject(new Error(msg))
    }
    return res
  },
  async error => {
    // WEB-08: 主动取消（AbortController）不属于错误，静默抛出交由调用方的竞态守卫处理
    if (axios.isCancel(error) || error?.code === 'ERR_CANCELED' || error?.name === 'CanceledError') {
      return Promise.reject(error)
    }
    let msg = error.message || '网络请求失败'
    if (error.response?.data instanceof Blob) {
      try {
        const text = await error.response.data.text()
        const json = JSON.parse(text)
        if (json.message) msg = json.message
      } catch (e) {}
    } else if (error.response?.data?.message) {
      msg = error.response.data.message
    }
    ElMessage.error(msg)
    return Promise.reject(error)
  }
)

export default {
  // 进度追踪与 SSE 实时订阅
  getProgress(jobId, options = {}) {
    return request.get(`/progress/${jobId}`, options)
  },
  // UI-02: 终止一个正在运行的长耗时任务
  cancelProgress(jobId) {
    return request.delete(`/progress/${jobId}`)
  },
  createProgressStream(jobId, onMessage, onError) {
    // WEB-17: 复用 axios 实例的 baseURL，避免硬编码绝对路径导致部署在非根路径时失效
    const base = request.defaults.baseURL || '/api/v1'
    const es = new EventSource(`${base}/progress/${jobId}/stream`)
    es.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (onMessage) onMessage(data)
      } catch (e) {
        console.error('Failed to parse progress SSE event data:', e)
      }
    }
    es.onerror = (err) => {
      if (onError) onError(err)
    }
    return es
  },

  // 服务端本地文件系统浏览（只读，仅限本机回环访问）
  fsRoots() {
    return request.get('/fs/roots')
  },
  fsBrowse({ path, exts, keyword, dirsOnly, offset, limit }, options = {}) {
    const params = { path }
    if (exts && exts.length) params.exts = exts.join(',')
    if (keyword) params.keyword = keyword
    if (dirsOnly) params.dirs_only = 'true'
    if (offset) params.offset = offset
    if (limit) params.limit = limit
    return request.get('/fs/browse', { params, ...options })
  },
  fsStat(paths) {
    return request.post('/fs/stat', { paths })
  },

  // 系统统计与配置
  getStats() {
    return request.get('/system/stats')
  },
  getSystemConfig() {
    return request.get('/system/config')
  },
  updateLogConfig(data) {
    return request.put('/system/config/log', data)
  },
  getSystemLogs() {
    return request.get('/system/logs')
  },
  cleanSystemLogs() {
    return request.post('/system/logs/clean')
  },
  // KB-01: 索引健康状态（是否需要重建）
  getKnowledgeIndexStatus() {
    return request.get('/system/knowledge-index/status')
  },
  // KB-01: 触发全量重建索引
  rebuildKnowledgeIndex() {
    return request.post('/knowledge/reindex', {}, { params: { async: 'true' } })
  },

  // 文档管理
  getDocuments() {
    return request.get('/documents')
  },
  // 按服务端本地路径导入 HDX 文档（支持一次提交多个目录或压缩包路径）
  importDocumentsByPaths(paths, conflictMode = 'overwrite', isAsync = true) {
    return request.post('/documents/import-dir', {
      paths: Array.isArray(paths) ? paths : [paths],
      conflict_mode: conflictMode,
      async: isAsync
    }, withTimeout(LONG_TIMEOUT))
  },
  deleteDocument(id) {
    return request.delete(`/documents/${id}`)
  },

  // 知识库
  searchKnowledge(params, options = {}) {
    return request.get('/knowledge/search', { params, ...options })
  },
  getKnowledgeDetail(id) {
    return request.get(`/knowledge/${id}`)
  },

  // 任务管理
  createTask(formDataOrJson, isAsync = true) {
    if (formDataOrJson instanceof FormData) {
      if (isAsync && !formDataOrJson.has('async')) {
        formDataOrJson.append('async', 'true')
      }
      return request.post('/tasks', formDataOrJson, {
        headers: { 'Content-Type': 'multipart/form-data' },
        params: isAsync ? { async: 'true' } : {},
        timeout: LONG_TIMEOUT
      })
    }
    const payload = typeof formDataOrJson === 'object' ? { ...formDataOrJson, async: isAsync } : formDataOrJson
    return request.post('/tasks', payload, {
      params: isAsync ? { async: 'true' } : {},
      timeout: LONG_TIMEOUT
    })
  },
  getTasks() {
    return request.get('/tasks')
  },
  getTask(id) {
    return request.get(`/tasks/${id}`)
  },
  getTaskFiles(taskId) {
    return request.get(`/tasks/${taskId}/files`)
  },
  // 按服务端本地路径导入日志（支持一次提交多个目录或文件路径）
  importTaskLogsByPaths(taskId, { paths, exts, recursive = true, conflictMode = 'rename', deviceVersion }, isAsync = true) {
    return request.post(`/tasks/${taskId}/import`, {
      paths,
      exts,
      recursive,
      conflict_mode: conflictMode,
      // PARSE-04: 允许补录设备软件版本以启用版本分档匹配
      ...(deviceVersion ? { device_version: deviceVersion } : {}),
      async: isAsync
    }, withTimeout(LONG_TIMEOUT, { params: isAsync ? { async: 'true' } : {} }))
  },
  // 以文本形式导入日志
  importTaskLogsText(taskId, { content, fileName, conflictMode = 'rename', deviceVersion }, isAsync = true) {
    return request.post(`/tasks/${taskId}/import`, {
      content,
      file_name: fileName,
      conflict_mode: conflictMode,
      ...(deviceVersion ? { device_version: deviceVersion } : {}),
      async: isAsync
    }, withTimeout(LONG_TIMEOUT, { params: isAsync ? { async: 'true' } : {} }))
  },
  queryTaskLogs(taskId, params, options = {}) {
    return request.get(`/tasks/${taskId}/logs`, { params, ...options })
  },
  getTaskModules(taskId) {
    return request.get(`/tasks/${taskId}/modules`)
  },
  reanalyzeTask(taskId, isAsync = true) {
    return request.post(`/tasks/${taskId}/reanalyze`, { async: isAsync }, {
      params: isAsync ? { async: 'true' } : {},
      timeout: LONG_TIMEOUT
    })
  },
  // WEB-08: 补齐 options 透传，供 useRequest 注入 AbortSignal 实现请求取消
  getTaskRCA(taskId, options = {}) {
    return request.get(`/tasks/${taskId}/rca`, options)
  },
  deleteTask(taskId) {
    return request.delete(`/tasks/${taskId}`)
  },

  // 设备管理
  createDevice(taskId, data) {
    return request.post(`/tasks/${taskId}/devices`, data)
  },
  getDevices(taskId) {
    return request.get(`/tasks/${taskId}/devices`)
  },
  getDevice(taskId, deviceId) {
    return request.get(`/tasks/${taskId}/devices/${deviceId}`)
  },
  updateDevice(taskId, deviceId, data) {
    return request.put(`/tasks/${taskId}/devices/${deviceId}`, data)
  },
  deleteDevice(taskId, deviceId) {
    return request.delete(`/tasks/${taskId}/devices/${deviceId}`)
  },
  // 按服务端本地路径向指定设备导入日志
  importDeviceLogsByPaths(taskId, deviceId, { paths, exts, recursive = true, conflictMode = 'rename' }, isAsync = true) {
    return request.post(`/tasks/${taskId}/devices/${deviceId}/import`, {
      paths,
      exts,
      recursive,
      conflict_mode: conflictMode,
      async: isAsync
    }, withTimeout(LONG_TIMEOUT, { params: isAsync ? { async: 'true' } : {} }))
  },
  // 以文本形式向指定设备导入日志
  importDeviceLogsText(taskId, deviceId, { content, fileName, conflictMode = 'rename' }, isAsync = true) {
    return request.post(`/tasks/${taskId}/devices/${deviceId}/import`, {
      content,
      file_name: fileName,
      conflict_mode: conflictMode,
      async: isAsync
    }, withTimeout(LONG_TIMEOUT, { params: isAsync ? { async: 'true' } : {} }))
  },
  autoAssignDevices(taskId) {
    return request.post(`/tasks/${taskId}/devices/auto-assign`)
  },

  // 多设备时间线与协同分析
  queryMultiDeviceLogs(taskId, filter, options = {}) {
    return request.post(`/tasks/${taskId}/multi-device/logs`, filter, options)
  },
  getDeviceTimeline(taskId, filter, options = {}) {
    return request.post(`/tasks/${taskId}/multi-device/timeline`, filter, options)
  },
  getMultiDeviceReport(taskId, deviceIds = [], options = {}) {
    return request.post(`/tasks/${taskId}/multi-device/report`, { device_ids: deviceIds }, options)
  },

  // 报表导出下载 (基于 Blob，防御重复提交并由拦截器统一弹窗报错)
  //
  // ARCH-07: 新增 csv 格式——后端已实现游标流式导出，内存占用恒定，
  // 适合大任务的全量留痕；html / json 仍受服务端上限约束。
  async downloadTaskReport(taskId, format = 'html') {
    const mimeTypes = {
      html: 'text/html;charset=utf-8',
      json: 'application/json;charset=utf-8',
      csv: 'text/csv;charset=utf-8'
    }
    const res = await request.get(`/tasks/${taskId}/export`, {
      params: { format },
      responseType: 'blob',
      timeout: EXPORT_TIMEOUT
    })
    // WEB-17: 不再固定写死 text/html，按实际格式设置 MIME，避免 JSON/CSV 被错误处理
    const blob = new Blob([res], { type: mimeTypes[format] || 'application/octet-stream' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `report_${taskId}.${format}`
    link.click()
    window.URL.revokeObjectURL(url)
  },
  async downloadMultiDeviceReport(taskId, format = 'html') {
    const mimeTypes = {
      html: 'text/html;charset=utf-8',
      json: 'application/json;charset=utf-8',
      csv: 'text/csv;charset=utf-8'
    }
    const res = await request.get(`/tasks/${taskId}/multi-device/export`, {
      params: { format },
      responseType: 'blob',
      timeout: EXPORT_TIMEOUT
    })
    const blob = new Blob([res], { type: mimeTypes[format] || 'application/octet-stream' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `multi_device_report_${taskId}.${format}`
    link.click()
    window.URL.revokeObjectURL(url)
  }
}
