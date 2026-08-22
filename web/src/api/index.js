import axios from 'axios'
import { ElMessage } from 'element-plus'

const request = axios.create({
  baseURL: '/api/v1',
  timeout: 60000
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
  importDir(dirPath, conflictMode = 'overwrite') {
    return request.post('/documents/import-dir', { dir_path: dirPath, conflict_mode: conflictMode })
  },
  uploadHDX(formData) {
    return request.post('/documents/upload', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }
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
  createTask(formDataOrJson) {
    if (formDataOrJson instanceof FormData) {
      return request.post('/tasks', formDataOrJson, {
        headers: { 'Content-Type': 'multipart/form-data' }
      })
    }
    return request.post('/tasks', formDataOrJson)
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
  importTaskLogs(taskId, formDataOrJson) {
    if (formDataOrJson instanceof FormData) {
      return request.post(`/tasks/${taskId}/import`, formDataOrJson, {
        headers: { 'Content-Type': 'multipart/form-data' }
      })
    }
    return request.post(`/tasks/${taskId}/import`, formDataOrJson)
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

