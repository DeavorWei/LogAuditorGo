import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import api from '../api'

/**
 * 产品功能开关（v1.1）。
 *
 * 只承载"影响 UI 可见性"的开关，不缓存业务数据：
 *  - rca：关联事件（实验性）。默认关闭；关闭时工作台不拉取 RCA 事件、
 *    不显示任何 RCA 入口；设置页开关变更后立即生效（后端读配置快照）。
 */
export const useFeatureStore = defineStore('features', () => {
  const DEFAULT_FEATURES = Object.freeze({
    rca: false,
    native_file_dialog: true,
    browser_upload: false,
    lan_mode: false,
    masking: false
  })

  const features = ref({ ...DEFAULT_FEATURES })
  const loaded = ref(false)
  const loading = ref(false)

  const rcaEnabled = computed(() => !!features.value.rca)
  const nativeFileDialogEnabled = computed(() => !!features.value.native_file_dialog)

  const fetchFeatures = async (force = false) => {
    if (loaded.value && !force) return features.value
    loading.value = true
    try {
      const res = await api.getFeatures()
      if (res && res.code === 0 && res.data) {
        features.value = { ...DEFAULT_FEATURES, ...(res.data.features || {}) }
        loaded.value = true
      }
      return features.value
    } catch (e) {
      // 开关读取失败时保持默认（RCA 关闭），UI 不会因后端异常暴露实验能力
      console.error('Fetch features failed:', e)
      return features.value
    } finally {
      loading.value = false
    }
  }

  const setRcaEnabled = async (enabled) => {
    const res = await api.updateFeatures({ rca: !!enabled })
    if (res && res.code === 0 && res.data?.features) {
      features.value = { ...features.value, ...res.data.features }
      loaded.value = true
    }
    return features.value
  }

  return {
    features,
    loaded,
    loading,
    rcaEnabled,
    nativeFileDialogEnabled,
    fetchFeatures,
    setRcaEnabled
  }
})

export default useFeatureStore
