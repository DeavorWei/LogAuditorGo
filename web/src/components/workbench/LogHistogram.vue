<template>
  <div class="log-histogram" v-loading="loading">
    <div class="histogram-header">
      <div class="histogram-title">
        <el-icon><Histogram /></el-icon>
        <span>日志时间分布（按严重度堆叠）</span>
        <span v-if="total > 0" class="histogram-total">共 {{ total }} 条</span>
      </div>
      <span class="histogram-hint">点击柱状图筛选该时间段</span>
    </div>
    <div ref="chartRef" class="histogram-chart"></div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Histogram } from '@element-plus/icons-vue'
import echarts from '@/plugins/echarts'
import api from '@/api'

const props = defineProps({
  taskId: { type: String, default: '' },
  query: { type: Object, default: () => ({}) },
  refreshKey: { type: String, default: '' }
})

const emit = defineEmits(['select-range'])

const chartRef = ref(null)
const loading = ref(false)
const total = ref(0)
let chart = null

const queryBody = computed(() => {
  const { page, page_size, ...rest } = props.query || {}
  void page
  void page_size
  return rest
})

const fetchHistogram = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const res = await api.getLogHistogram(props.taskId, queryBody.value, 48)
    if (res.code === 0 && res.data) {
      total.value = res.data.total || 0
      await nextTick()
      render(res.data.buckets || [])
    }
  } catch (e) {
    console.error('Fetch histogram failed:', e)
  } finally {
    loading.value = false
  }
}

const render = (buckets) => {
  if (!chartRef.value) return
  if (!chart) chart = echarts.init(chartRef.value)
  const labels = buckets.map((item) => {
    const start = String(item.start || '').replace('T', ' ').slice(5, 16)
    return start
  })
  chart.setOption({
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (params) => {
        const items = params.map((p) => `${p.seriesName}: ${p.value}`).join('<br/>')
        return `${params[0]?.axisValue}<br/>${items}`
      }
    },
    legend: { data: ['严重', '错误', '警告', '提示'], right: 10, top: 0, itemWidth: 12, itemHeight: 8 },
    grid: { left: 8, right: 12, top: 26, bottom: 4, containLabel: true },
    xAxis: { type: 'category', data: labels, axisLabel: { fontSize: 10, hideOverlap: true } },
    yAxis: { type: 'value', minInterval: 1, splitLine: { lineStyle: { type: 'dashed' } } },
    series: [
      { name: '严重', type: 'bar', stack: 'total', data: buckets.map((b) => b.severe), itemStyle: { color: '#ef4444' } },
      { name: '错误', type: 'bar', stack: 'total', data: buckets.map((b) => b.error), itemStyle: { color: '#f97316' } },
      { name: '警告', type: 'bar', stack: 'total', data: buckets.map((b) => b.warning), itemStyle: { color: '#f59e0b' } },
      { name: '提示', type: 'bar', stack: 'total', data: buckets.map((b) => b.info), itemStyle: { color: '#3b82f6' } }
    ]
  }, true)

  chart.off('click')
  chart.on('click', (params) => {
    const bucket = buckets[params.dataIndex]
    if (!bucket) return
    emit('select-range', { start: bucket.start, end: bucket.end })
    ElMessage.success('已按所选时间段筛选')
  })
}

const handleResize = () => {
  try {
    chart?.resize()
  } catch (e) {
    // disposed
  }
}

onMounted(() => {
  fetchHistogram()
  window.addEventListener('resize', handleResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  chart?.dispose()
  chart = null
})

watch(() => props.taskId, fetchHistogram)
watch(() => props.refreshKey, fetchHistogram)
</script>

<style scoped>
.log-histogram {
  background: var(--gray-0);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 8px 10px 4px;
  margin: 0 12px 8px;
}

.histogram-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.histogram-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
  color: var(--text-primary);
}

.histogram-total {
  font-weight: 400;
  color: var(--text-secondary);
}

.histogram-hint {
  color: var(--text-secondary);
}

.histogram-chart {
  width: 100%;
  height: 140px;
}
</style>
