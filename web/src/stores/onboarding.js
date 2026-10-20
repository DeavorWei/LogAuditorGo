import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import api from '../api'

/**
 * 首启引导状态（v1.3）。
 *
 * 状态存后端数据目录（onboarding.json），清浏览器缓存不会重复触发；
 * 前端只做缓存与 UI 呈现。
 */
export const useOnboardingStore = defineStore('onboarding', () => {
  const state = ref({ completed_steps: [], skipped: false, dismissed: false })
  const shouldShow = ref(false)
  const loaded = ref(false)
  const loading = ref(false)

  const completedCount = computed(() => (state.value.completed_steps || []).length)
  const allCompleted = computed(() => completedCount.value >= 4)
  const checklistVisible = computed(() => !state.value.dismissed && !allCompleted.value)

  const apply = (res) => {
    if (!res || !res.data) return
    state.value = {
      completed_steps: res.data.state?.completed_steps || [],
      skipped: Boolean(res.data.state?.skipped),
      dismissed: Boolean(res.data.state?.dismissed)
    }
    shouldShow.value = Boolean(res.data.should_show)
  }

  const fetch = async (force = false) => {
    if (loaded.value && !force) return
    loading.value = true
    try {
      const res = await api.getOnboarding()
      if (res?.code === 0) {
        apply(res)
        loaded.value = true
      }
    } catch (e) {
      console.error('Fetch onboarding failed:', e)
    } finally {
      loading.value = false
    }
  }

  const update = async (patch) => {
    const res = await api.updateOnboarding(patch)
    if (res?.code === 0) apply(res)
    return res
  }

  const markSteps = (steps) => update({ completed_steps: steps })
  const markStep = (step) => markSteps([step])
  const skip = async () => {
    await update({ skipped: true })
    shouldShow.value = false
  }
  const dismiss = async () => {
    await update({ dismissed: true })
    shouldShow.value = false
  }
  const reopen = () => {
    shouldShow.value = true
  }

  return {
    state,
    shouldShow,
    loading,
    loaded,
    completedCount,
    allCompleted,
    checklistVisible,
    fetch,
    update,
    markStep,
    markSteps,
    skip,
    dismiss,
    reopen
  }
})

export default useOnboardingStore
