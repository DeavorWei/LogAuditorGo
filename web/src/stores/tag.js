import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '@/api'
import { ElMessage } from 'element-plus'

export const useTagStore = defineStore('tag', () => {
  const tags = ref([])
  const loading = ref(false)

  const tagMap = computed(() => {
    const map = new Map()
    for (const t of tags.value) {
      map.set(t.id, t)
    }
    return map
  })

  const fetchTags = async (taskId) => {
    if (!taskId) return
    loading.value = true
    try {
      const res = await api.getTaskTags(taskId)
      if (res.code === 0) {
        tags.value = res.data || []
      }
    } catch (e) {
      console.error('Fetch task tags failed:', e)
    } finally {
      loading.value = false
    }
  }

  const createTag = async (taskId, data) => {
    const res = await api.createTaskTag(taskId, data)
    if (res.code === 0) {
      await fetchTags(taskId)
      ElMessage.success('标签创建成功')
      return res.data
    }
    return null
  }

  const updateTag = async (taskId, tagId, data) => {
    const res = await api.updateTaskTag(taskId, tagId, data)
    if (res.code === 0) {
      await fetchTags(taskId)
      ElMessage.success('标签更新成功')
      return res.data
    }
    return null
  }

  const deleteTag = async (taskId, tagId) => {
    const res = await api.deleteTaskTag(taskId, tagId)
    if (res.code === 0) {
      await fetchTags(taskId)
      ElMessage.success('标签已删除')
      return true
    }
    return false
  }

  return {
    tags,
    loading,
    tagMap,
    fetchTags,
    createTag,
    updateTag,
    deleteTag
  }
})

export default useTagStore
