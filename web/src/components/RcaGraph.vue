<template>
  <div class="rca-graph-container">
    <div ref="chartRef" class="echarts-box"></div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch, onUnmounted } from 'vue'
// UI-09: 改用按需引入的 echarts 入口（仅注册 GraphChart 及必要组件）
import echarts from '@/plugins/echarts'

const props = defineProps({
  rcaEvent: {
    type: Object,
    default: () => null
  }
})

const chartRef = ref(null)
let chartInstance = null

const renderChart = () => {
  if (!chartRef.value || !props.rcaEvent) return

  if (!chartInstance) {
    chartInstance = echarts.init(chartRef.value)
  }

  let impactEvents = []
  try {
    if (props.rcaEvent.impact_events_json) {
      impactEvents = JSON.parse(props.rcaEvent.impact_events_json)
    }
  } catch (e) {
    impactEvents = []
  }

  const rootName = `根因: ${props.rcaEvent.root_module}/${props.rcaEvent.root_brief} (#${props.rcaEvent.root_log_id})`
  const nodes = [
    {
      name: rootName,
      category: 0,
      symbolSize: 55,
      itemStyle: { color: '#ef4444' }
    }
  ]

  const nodeIdMap = new Map()
  nodeIdMap.set(props.rcaEvent.root_log_id, rootName)

  const links = []
  const categories = [{ name: '根因事件' }, { name: '衍生事件' }]

  impactEvents.forEach((ev) => {
    const nodeName = `[+${ev.delay_ms}ms] ${ev.module}/${ev.brief} (#${ev.log_id})`
    nodeIdMap.set(ev.log_id, nodeName)
    nodes.push({
      name: nodeName,
      category: 1,
      symbolSize: 42,
      itemStyle: { color: '#f97316' }
    })
  })

  // 根据 from_log_id 构建真实的 DAG 有向边
  impactEvents.forEach((ev) => {
    const targetName = nodeIdMap.get(ev.log_id)
    let sourceName = rootName
    if (ev.from_log_id && nodeIdMap.has(ev.from_log_id)) {
      sourceName = nodeIdMap.get(ev.from_log_id)
    }

    links.push({
      source: sourceName,
      target: targetName,
      label: {
        show: true,
        formatter: `+${ev.delay_ms}ms`,
        fontSize: 11
      },
      lineStyle: {
        width: 2,
        curveness: 0.1,
        color: '#fb923c'
      }
    })
  })

  const option = {
    title: {
      text: '故障传播因果拓扑图 (RCA DAG)',
      subtext: props.rcaEvent.root_cause_summary,
      left: 'center',
      textStyle: { fontSize: 14, color: '#1e293b' },
      subtextStyle: { fontSize: 12, color: '#64748b' }
    },
    tooltip: {},
    legend: {
      data: categories.map(c => c.name),
      bottom: 10
    },
    series: [
      {
        type: 'graph',
        layout: 'force',
        data: nodes,
        links: links,
        categories: categories,
        roam: true,
        edgeSymbol: ['none', 'arrow'],
        edgeSymbolSize: 8,
        label: {
          show: true,
          position: 'right',
          fontSize: 12
        },
        force: {
          repulsion: 300,
          edgeLength: [100, 180]
        }
      }
    ]
  }

  chartInstance.setOption(option, true)
}

const handleResize = () => {
  chartInstance?.resize()
}

watch(() => props.rcaEvent, renderChart, { deep: true })

onMounted(() => {
  renderChart()
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  window.removeEventListener('resize', handleResize)
  chartInstance?.dispose()
})
</script>

<style scoped>
.rca-graph-container {
  width: 100%;
  height: 380px;
}
.echarts-box {
  width: 100%;
  height: 100%;
}
</style>
