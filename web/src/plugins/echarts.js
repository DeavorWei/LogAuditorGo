/**
 * ECharts 按需引入入口 (UI-09 / WEB-15)。
 *
 * 背景：Dashboard.vue 与 RcaGraph.vue 原先都是 `import * as echarts from 'echarts'`，
 * 等于把柱状图、折线图、饼图、地图、GL 等所有图表类型与坐标系
 * 全部打进首屏（构建产物中 echarts chunk 超过 1MB）。
 *
 * 本项目实际只用到两类图表：
 *   - BarChart   （仪表盘的模块分布）
 *   - Graph      （RCA 根因传播拓扑图，RcaGraph.vue）
 * 这里只注册这两类及其依赖的组件、渲染器与坐标系，
 * 其余一律不打包。新增图表类型时在 CHARTS / COMPONENTS 中补一行即可。
 */
import * as echarts from 'echarts/core'
import { BarChart, GraphChart } from 'echarts/charts'
import {
  GridComponent,
  TooltipComponent,
  TitleComponent,
  LegendComponent,
  DatasetComponent
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

// 按需注册：未注册的图表类型不会进入构建产物
echarts.use([
  BarChart,
  GraphChart,
  GridComponent,
  TooltipComponent,
  TitleComponent,
  LegendComponent,
  DatasetComponent,
  CanvasRenderer
])

export default echarts
