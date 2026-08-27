import axios from 'axios'
import { ElMessage } from 'element-plus'

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 300000 // 放宽至 5 分钟
})

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
  getProgress(jobId) {
    return request.get(`/progress/${jobId}`)
  },
  createProgressStream(jobId, onMessage, onError) {
    const es = new EventSource(`/api/v1/progress/${jobId}/stream`)
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

  // 文档管理
  getDocuments() {
    return request.get('/documents')
  },
  importDir(dirPath, conflictMode = 'overwrite', isAsync = true) {
    return request.post('/documents/import-dir', {
      dir_path: dirPath,
      conflict_mode: conflictMode,
      async: isAsync
    })
  },
  uploadHDX(formData, isAsync = true) {
    if (isAsync && formData instanceof FormData && !formData.has('async')) {
      formData.append('async', 'true')
    }
    return request.post('/documents/upload', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
      params: isAsync ? { async: 'true' } : {}
    })
  },
  deleteDocument(id) {
    return request.delete(`/documents/${id}`)
  },

  // 知识库
  searchKnowledge(params) {
    return request.get('/knowledge/search', { params })
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
        params: isAsync ? { async: 'true' } : {}
      })
    }
    const payload = typeof formDataOrJson === 'object' ? { ...formDataOrJson, async: isAsync } : formDataOrJson
    return request.post('/tasks', payload, {
      params: isAsync ? { async: 'true' } : {}
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
  importTaskLogs(taskId, formDataOrJson, isAsync = true) {
    if (formDataOrJson instanceof FormData) {
      if (isAsync && !formDataOrJson.has('async')) {
        formDataOrJson.append('async', 'true')
      }
      return request.post(`/tasks/${taskId}/import`, formDataOrJson, {
        headers: { 'Content-Type': 'multipart/form-data' },
        params: isAsync ? { async: 'true' } : {}
      })
    }
    const payload = typeof formDataOrJson === 'object' ? { ...formDataOrJson, async: isAsync } : formDataOrJson
    return request.post(`/tasks/${taskId}/import`, payload, {
      params: isAsync ? { async: 'true' } : {}
    })
  },
  queryTaskLogs(taskId, params) {
    return request.get(`/tasks/${taskId}/logs`, { params })
  },
  getTaskModules(taskId) {
    return request.get(`/tasks/${taskId}/modules`)
  },
  reanalyzeTask(taskId, isAsync = true) {
    return request.post(`/tasks/${taskId}/reanalyze`, { async: isAsync }, {
      params: isAsync ? { async: 'true' } : {}
    })
  },
  getTaskRCA(taskId) {
    return request.get(`/tasks/${taskId}/rca`)
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
  importDeviceLogs(taskId, deviceId, formDataOrJson, isAsync = true) {
    if (formDataOrJson instanceof FormData) {
      if (isAsync && !formDataOrJson.has('async')) {
        formDataOrJson.append('async', 'true')
      }
      return request.post(`/tasks/${taskId}/devices/${deviceId}/import`, formDataOrJson, {
        headers: { 'Content-Type': 'multipart/form-data' },
        params: isAsync ? { async: 'true' } : {}
      })
    }
    const payload = typeof formDataOrJson === 'object' ? { ...formDataOrJson, async: isAsync } : formDataOrJson
    return request.post(`/tasks/${taskId}/devices/${deviceId}/import`, payload, {
      params: isAsync ? { async: 'true' } : {}
    })
  },
  autoAssignDevices(taskId) {
    return request.post(`/tasks/${taskId}/devices/auto-assign`)
  },

  // 多设备时间线与协同分析
  queryMultiDeviceLogs(taskId, filter) {
    return request.post(`/tasks/${taskId}/multi-device/logs`, filter)
  },
  getDeviceTimeline(taskId, filter) {
    return request.post(`/tasks/${taskId}/multi-device/timeline`, filter)
  },
  getMultiDeviceReport(taskId, deviceIds = []) {
    return request.post(`/tasks/${taskId}/multi-device/report`, { device_ids: deviceIds })
  },

  // 报表导出下载 (基于 Blob，防御重复提交并由拦截器统一弹窗报错)
  async downloadTaskReport(taskId, format = 'html') {
    const res = await request.get(`/tasks/${taskId}/export`, {
      params: { format },
      responseType: 'blob'
    })
    const blob = new Blob([res], { type: 'text/html;charset=utf-8' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `report_${taskId}.${format}`
    link.click()
    window.URL.revokeObjectURL(url)
  },
  async downloadMultiDeviceReport(taskId, format = 'html') {
    const res = await request.get(`/tasks/${taskId}/multi-device/export`, {
      params: { format },
      responseType: 'blob'
    })
    const blob = new Blob([res], { type: 'text/html;charset=utf-8' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `multi_device_report_${taskId}.${format}`
    link.click()
    window.URL.revokeObjectURL(url)
  }
}
