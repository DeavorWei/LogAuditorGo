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
  error => {
    const msg = error.response?.data?.message || error.response?.data?.error || error.message || '网络请求失败'
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
  getTaskRCA(taskId) {
    return request.get(`/tasks/${taskId}/rca`)
  },
  deleteTask(taskId) {
    return request.delete(`/tasks/${taskId}`)
  }
}
