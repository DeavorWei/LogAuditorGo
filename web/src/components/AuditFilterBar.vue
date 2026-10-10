<template>
  <div class="audit-filter-bar" :class="{ 'is-collapsed': ui.isFilterCollapsed }">
    <!-- 行 1: 核心检索与操作常驻行 (始终显示) -->
    <div class="filter-row filter-row-primary">
      <el-input
        v-model="filter.keyword"
        placeholder="搜索报文/模块/简名 (如 RM/ROUTE_DELETE)..."
        prefix-icon="Search"
        clearable
        size="small"
        class="keyword-input"
        @change="handleKeywordChange"
        @clear="handleKeywordChange"
      />
      <el-button
        v-if="hasActiveFilter"
        size="small"
        type="info"
        plain
        title="重置所有筛选条件"
        class="reset-filter-btn"
        @click="handleResetFilters"
      >
        重置
      </el-button>

      <!-- 全屏切换按钮 (拆分下拉菜单，兼顾 Zen Mode 与浏览器物理全屏) -->
      <el-dropdown trigger="click" @command="handleFullscreenCommand">
        <el-button-group class="fullscreen-btn-group">
          <el-tooltip :content="ui.isZenMode ? '退出沉浸全屏 (Esc)' : '沉浸式全屏工作台 (F)'" placement="bottom">
            <el-button
              size="small"
              :type="ui.isZenMode ? 'primary' : 'default'"
              icon="FullScreen"
              class="fullscreen-main-btn"
              @click.stop="handleToggleZenMode"
            />
          </el-tooltip>
          <el-button
            size="small"
            :type="ui.isZenMode ? 'primary' : 'default'"
            icon="ArrowDown"
            class="fullscreen-arrow-btn"
          />
        </el-button-group>
        <template #dropdown>
          <el-dropdown-menu>
            <el-dropdown-item command="zen">
              <el-icon><Monitor /></el-icon>
              <span>应用内全宽沉浸视图 (F)</span>
              <span v-if="ui.isZenMode" class="dropdown-tag">当前</span>
            </el-dropdown-item>
            <el-dropdown-item command="browser_fullscreen">
              <el-icon><FullScreen /></el-icon>
              <span>浏览器物理全屏 (Shift+F)</span>
              <span v-if="isBrowserFullscreen" class="dropdown-tag">开启</span>
            </el-dropdown-item>
          </el-dropdown-menu>
        </template>
      </el-dropdown>
    </div>

    <!-- 建议 1: 收起态下的微型过滤胶囊栏 (Active Filter Chips) -->
    <div v-if="ui.isFilterCollapsed && activeFilterChips.length > 0" class="active-filter-chips-bar">
      <span class="chips-label">已生效:</span>
      <div class="chips-scroll-container">
        <el-tooltip
          v-for="chip in visibleChips"
          :key="chip.key"
          :content="chip.tooltip"
          placement="top"
        >
          <el-tag
            size="small"
            closable
            :type="chip.tagType || 'info'"
            class="filter-chip-item"
            @close="removeSingleFilter(chip.key)"
          >
            <span class="chip-text">{{ chip.label }}</span>
          </el-tag>
        </el-tooltip>

        <!-- 溢出项汇总 Popover -->
        <el-popover v-if="overflowChips.length > 0" trigger="hover" placement="bottom-start" width="auto">
          <template #reference>
            <el-tag size="small" type="info" class="filter-chip-item overflow-tag">
              +{{ overflowChips.length }}项 ▾
            </el-tag>
          </template>
          <div class="overflow-chips-popover">
            <el-tooltip
              v-for="chip in overflowChips"
              :key="chip.key"
              :content="chip.tooltip"
              placement="top"
            >
              <el-tag
                size="small"
                closable
                :type="chip.tagType || 'info'"
                class="filter-chip-item in-popover"
                @close="removeSingleFilter(chip.key)"
              >
                {{ chip.label }}
              </el-tag>
            </el-tooltip>
          </div>
        </el-popover>
      </div>
    </div>

    <!-- 折叠内容包裹容器 (由动画控制展开收缩，移除 v-show 保证贝塞尔过渡) -->
    <div class="filter-collapsible-wrapper">
      <!-- 行 2: 时间与排序三等分行 (需求 1，平均平分列宽) -->
      <div class="filter-row filter-row-time-triplet">
        <el-select
          v-model="currentSortOption"
          placeholder="排序方式"
          size="small"
          class="triplet-item"
          @change="onSortOptionChange"
        >
          <el-option label="⏰ 时间正序" value="time_asc" />
          <el-option label="⏰ 时间倒序" value="time_desc" />
          <el-option label="📄 原始入库顺序" value="id_asc" />
        </el-select>
        <el-date-picker
          v-model="filter.timeStart"
          type="datetime"
          placeholder="起始时间"
          size="small"
          class="triplet-item"
          format="YYYY-MM-DD HH:mm:ss"
          value-format="YYYY-MM-DDTHH:mm:ssZ"
          :shortcuts="computedStartTimeShortcuts"
          clearable
          @change="emitFilterChange"
        />
        <el-date-picker
          v-model="filter.timeEnd"
          type="datetime"
          placeholder="截止时间"
          size="small"
          class="triplet-item"
          format="YYYY-MM-DD HH:mm:ss"
          value-format="YYYY-MM-DDTHH:mm:ssZ"
          :shortcuts="computedEndTimeShortcuts"
          clearable
          @change="emitFilterChange"
        />
      </div>

      <!-- 行 3: 设备/级别/匹配状态属性行 (需求 2) -->
      <div class="filter-row filter-row-attributes">
        <el-select
          v-if="taskDevices.length > 1"
          v-model="filter.deviceId"
          placeholder="设备筛选"
          clearable
          size="small"
          class="attr-item"
          @change="emitFilterChange"
        >
          <el-option label="全部设备" :value="null" />
          <el-option
            v-for="d in taskDevices"
            :key="d.id"
            :label="`${d.device_name} (${d.log_count}条)`"
            :value="d.id"
          />
        </el-select>
        <el-select
          v-model="filter.severity"
          placeholder="级别过滤"
          clearable
          size="small"
          class="attr-item"
          @change="emitFilterChange"
        >
          <el-option label="全部级别" :value="null" />
          <el-option label="<=2 (紧急/告警)" :value="2" />
          <el-option label="<=4 (错误及以上)" :value="4" />
          <el-option label="<=6 (通知及以上)" :value="6" />
        </el-select>
        <el-select
          v-model="filter.matched"
          placeholder="匹配状态"
          clearable
          size="small"
          class="attr-item"
          @change="emitFilterChange"
        >
          <el-option label="全部状态" :value="null" />
          <el-option label="已匹配知识库" :value="true" />
          <el-option label="未匹配" :value="false" />
        </el-select>
      </div>

      <!-- 行 4: 标签筛选 + 逻辑切换 + 管理入口 -->
      <div class="filter-row filter-row-tags">
        <el-select
          v-model="filter.tagIds"
          multiple
          collapse-tags
          collapse-tags-tooltip
          clearable
          placeholder="🏷 按标签筛选"
          size="small"
          class="tag-select-item"
          @change="emitFilterChange"
        >
          <el-option
            v-for="t in tagStore.tags"
            :key="t.id"
            :label="`${t.name} (${t.log_count || 0})`"
            :value="t.id"
          >
            <div style="display: flex; align-items: center; justify-content: space-between">
              <span>{{ t.name }}</span>
              <span
                :style="{
                  display: 'inline-block',
                  width: '10px',
                  height: '10px',
                  borderRadius: '50%',
                  backgroundColor: t.color || '#409EFF'
                }"
              />
            </div>
          </el-option>
        </el-select>
        <el-tooltip
          v-if="filter.tagIds && filter.tagIds.length > 1"
          :content="filter.tagLogic === 'all' ? '当前模式: 必须同时满足所有选中标签 (AND)' : '当前模式: 满足任一选中标签即可 (OR)'"
          placement="top"
        >
          <el-button
            size="small"
            :type="filter.tagLogic === 'all' ? 'primary' : 'default'"
            plain
            class="tag-logic-btn"
            @click="toggleTagLogic"
          >
            {{ filter.tagLogic === 'all' ? '全部' : '任一' }}
          </el-button>
        </el-tooltip>
        <el-button
          size="small"
          type="info"
          plain
          title="管理标签"
          class="tag-manage-btn"
          @click="$emit('open-tag-manager')"
        >
          管理
        </el-button>
      </div>
    </div>

    <!-- 需求 3: 底部悬浮展开/收缩控制条 (带生效条件计数角标) -->
    <div class="filter-toggle-pill-bar" @click="handleToggleCollapse">
      <div class="pill-handle">
        <el-icon class="pill-icon">
          <ArrowDown v-if="ui.isFilterCollapsed" />
          <ArrowUp v-else />
        </el-icon>
        <span class="pill-label">{{ ui.isFilterCollapsed ? '展开完整筛选' : '收起筛选' }}</span>
        <span v-if="ui.isFilterCollapsed && hiddenActiveFilterCount > 0" class="pill-badge">
          {{ hiddenActiveFilterCount }} 项生效
        </span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { useFilterStore } from '@/stores/filter'
import { useTagStore } from '@/stores/tag'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'
import { ElMessage } from 'element-plus'
import { ArrowDown, ArrowUp, FullScreen, Monitor } from '@element-plus/icons-vue'

const props = defineProps({
  taskDevices: {
    type: Array,
    default: () => []
  },
  taskMeta: {
    type: Object,
    default: () => null
  },
  selectedLog: {
    type: Object,
    default: () => null
  },
  latestLogTime: {
    type: [String, Number, Date],
    default: null
  }
})

const emit = defineEmits([
  'change',
  'reset',
  'open-tag-manager'
])

const filterStore = useFilterStore()
const tagStore = useTagStore()
const workbenchUIStore = useWorkbenchUIStore()

const filter = computed(() => filterStore.filters)
const ui = computed(() => workbenchUIStore.ui)

// 排序方式双向绑定映射 (保持 filter.sortBy + filter.order 契约)
const currentSortOption = computed({
  get() {
    const { sortBy, order } = filterStore.filters
    if (sortBy === 'time' && order === 'asc') return 'time_asc'
    if (sortBy === 'time' && order === 'desc') return 'time_desc'
    if (sortBy === 'id' && order === 'asc') return 'id_asc'
    return 'time_asc'
  },
  set(val) {
    if (val === 'time_asc') {
      filterStore.filters.sortBy = 'time'
      filterStore.filters.order = 'asc'
    } else if (val === 'time_desc') {
      filterStore.filters.sortBy = 'time'
      filterStore.filters.order = 'desc'
    } else if (val === 'id_asc') {
      filterStore.filters.sortBy = 'id'
      filterStore.filters.order = 'asc'
    }
  }
})

const onSortOptionChange = () => {
  emit('change')
}

const emitFilterChange = () => {
  emit('change')
}

const handleKeywordChange = () => {
  emit('change')
}

const handleResetFilters = () => {
  filterStore.resetFilters()
  emit('reset')
}

const toggleTagLogic = () => {
  filter.value.tagLogic = filter.value.tagLogic === 'all' ? 'any' : 'all'
  emit('change')
}

const handleToggleCollapse = () => {
  workbenchUIStore.toggleFilterCollapse()
}

const handleToggleZenMode = () => {
  workbenchUIStore.toggleZenMode()
}

// 浏览器物理全屏联动支持 (建议 4)
const isBrowserFullscreen = ref(typeof document !== 'undefined' && !!document.fullscreenElement)

const handleFullscreenCommand = async (cmd) => {
  if (cmd === 'zen') {
    handleToggleZenMode()
  } else if (cmd === 'browser_fullscreen') {
    try {
      if (!document.fullscreenElement) {
        await document.documentElement.requestFullscreen()
        isBrowserFullscreen.value = true
      } else {
        await document.exitFullscreen()
        isBrowserFullscreen.value = false
      }
    } catch (e) {
      ElMessage.info('当前浏览器限制全屏操作，您可直接按键盘 F11 开启物理全屏')
    }
  }
}

const onBrowserFullscreenChange = () => {
  isBrowserFullscreen.value = !!document.fullscreenElement
}

onMounted(() => {
  document.addEventListener('fullscreenchange', onBrowserFullscreenChange)
})

onBeforeUnmount(() => {
  document.removeEventListener('fullscreenchange', onBrowserFullscreenChange)
})

// 计算是否有全局活跃过滤条件 (与 workbench 统一口径)
const hasActiveFilter = computed(() => {
  return filterStore.activeFilterCount() > 0 || currentSortOption.value !== 'time_asc'
})

// 计算收起状态下隐藏行 (行 2 ~ 行 4) 的活跃条件数
const hiddenActiveFilterCount = computed(() => {
  const f = filterStore.filters
  let count = 0
  if (currentSortOption.value !== 'time_asc') count++
  if (f.timeStart || f.timeEnd) count++
  if (f.severity !== null) count++
  if (f.matched !== null) count++
  if (f.deviceId !== null) count++
  if (f.tagIds && f.tagIds.length > 0) count++
  return count
})

// 建议 1: 计算活跃过滤胶囊 Chips
const activeFilterChips = computed(() => {
  const f = filterStore.filters
  const chips = []

  // 时间区间
  if (f.timeStart || f.timeEnd) {
    let timeLabel = '时间区间'
    let tooltip = ''
    if (f.timeStart && f.timeEnd) {
      const s = f.timeStart.substring(5, 16).replace('T', ' ')
      const e = f.timeEnd.substring(5, 16).replace('T', ' ')
      timeLabel = `⏰ ${s} ~ ${e}`
      tooltip = `时间区间: ${f.timeStart} 至 ${f.timeEnd}`
    } else if (f.timeStart) {
      timeLabel = `⏰ 从 ${f.timeStart.substring(5, 16).replace('T', ' ')}`
      tooltip = `起始时间: ${f.timeStart} 之后`
    } else if (f.timeEnd) {
      timeLabel = `⏰ 至 ${f.timeEnd.substring(5, 16).replace('T', ' ')}`
      tooltip = `截止时间: ${f.timeEnd} 之前`
    }
    chips.push({
      key: 'time',
      label: timeLabel,
      tooltip,
      tagType: 'primary'
    })
  }

  // 严重级别
  if (f.severity !== null) {
    chips.push({
      key: 'severity',
      label: `⚠️ 级别≤${f.severity}`,
      tooltip: `严重度过滤: Level ${f.severity} 及以上紧急告警`,
      tagType: f.severity <= 2 ? 'danger' : 'warning'
    })
  }

  // 匹配状态
  if (f.matched !== null) {
    chips.push({
      key: 'matched',
      label: f.matched ? '📚 已匹配' : '未匹配',
      tooltip: f.matched ? '知识库匹配状态: 仅显示已命中官方知识库日志' : '知识库匹配状态: 仅显示未命中知识库日志',
      tagType: f.matched ? 'success' : 'info'
    })
  }

  // 设备筛选
  if (f.deviceId !== null && props.taskDevices.length > 1) {
    const dev = props.taskDevices.find(d => d.id === f.deviceId)
    const name = dev ? dev.device_name : '指定设备'
    chips.push({
      key: 'deviceId',
      label: `🖥 ${name}`,
      tooltip: `设备过滤: ${name}`,
      tagType: 'info'
    })
  }

  // 标签筛选
  if (f.tagIds && f.tagIds.length > 0) {
    if (f.tagIds.length === 1) {
      const tag = tagStore.tags.find(t => t.id === f.tagIds[0])
      const tagName = tag ? tag.name : '标签'
      chips.push({
        key: 'tagIds',
        label: `🏷 ${tagName}`,
        tooltip: `标签过滤: ${tagName}`,
        tagType: 'info'
      })
    } else {
      const tagNames = f.tagIds.map(id => tagStore.tags.find(t => t.id === id)?.name || id).join(', ')
      chips.push({
        key: 'tagIds',
        label: `🏷 ${f.tagIds.length}个标签 (${f.tagLogic === 'all' ? '全部' : '任一'})`,
        tooltip: `标签过滤 (${f.tagLogic === 'all' ? 'AND 与逻辑' : 'OR 或逻辑'}): ${tagNames}`,
        tagType: 'info'
      })
    }
  }

  // 排序方式偏离默认
  if (currentSortOption.value !== 'time_asc') {
    chips.push({
      key: 'sort',
      label: currentSortOption.value === 'time_desc' ? '↕ 时间倒序' : '📄 物理入库序',
      tooltip: `排序方式: ${currentSortOption.value === 'time_desc' ? '按时间倒序排查最新事件' : '按物理文件入库顺序'}`,
      tagType: 'info'
    })
  }

  return chips
})

// 最多展示前 3 个胶囊，超出部分折入 Popover
const visibleChips = computed(() => activeFilterChips.value.slice(0, 3))
const overflowChips = computed(() => activeFilterChips.value.slice(3))

// 单个胶囊原子清除方法
const removeSingleFilter = (key) => {
  if (key === 'time') {
    filterStore.filters.timeStart = null
    filterStore.filters.timeEnd = null
  } else if (key === 'severity') {
    filterStore.filters.severity = null
  } else if (key === 'matched') {
    filterStore.filters.matched = null
  } else if (key === 'deviceId') {
    filterStore.filters.deviceId = null
  } else if (key === 'tagIds') {
    filterStore.filters.tagIds = []
  } else if (key === 'sort') {
    currentSortOption.value = 'time_asc'
  }
  emit('change')
}

// 建议 2: 动态数据锚点基准计算引擎 (§6.2)
const getAnchorTimestamp = () => {
  // 1. 优先使用传入的最新日志时戳或 taskMeta
  const metaTime = props.latestLogTime ||
    props.taskMeta?.latest_timestamp ||
    props.taskMeta?.end_time ||
    props.taskMeta?.max_time ||
    props.taskMeta?.finish_time
  if (metaTime) {
    const d = new Date(metaTime)
    if (!isNaN(d.getTime())) return d
  }
  // 2. 局部选中日志时戳兜底
  if (props.selectedLog && props.selectedLog.timestamp && !String(props.selectedLog.timestamp).startsWith('0001-01-01')) {
    const d = new Date(props.selectedLog.timestamp)
    if (!isNaN(d.getTime())) return d
  }
  // 3. 系统当前时间兜底
  return new Date()
}

const computedStartTimeShortcuts = computed(() => {
  const anchor = getAnchorTimestamp()
  const shortcuts = [
    {
      text: '⚡ 闪断前15m',
      value: () => new Date(anchor.getTime() - 15 * 60 * 1000)
    },
    {
      text: '🌙 昨夜18:00',
      value: () => {
        const d = new Date(anchor)
        d.setDate(d.getDate() - 1)
        d.setHours(18, 0, 0, 0)
        return d
      }
    },
    {
      text: '1小时前',
      value: () => new Date(anchor.getTime() - 3600 * 1000)
    },
    {
      text: '24小时前',
      value: () => new Date(anchor.getTime() - 24 * 3600 * 1000)
    },
    {
      text: '3天前',
      value: () => new Date(anchor.getTime() - 3 * 24 * 3600 * 1000)
    }
  ]

  // 场景化快捷项：聚焦选中的日志发生时间前 30 分钟 (建议 2)
  if (props.selectedLog && props.selectedLog.timestamp) {
    shortcuts.unshift({
      text: '🎯 聚焦日志前30m',
      value: () => {
        const t = new Date(props.selectedLog.timestamp).getTime()
        return new Date(t - 30 * 60 * 1000)
      }
    })
  }

  return shortcuts
})

const computedEndTimeShortcuts = computed(() => {
  const anchor = getAnchorTimestamp()
  const shortcuts = [
    {
      text: '⚡ 任务最新',
      value: () => new Date(anchor)
    },
    {
      text: '🌙 今晨09:00',
      value: () => {
        const d = new Date(anchor)
        d.setHours(9, 0, 0, 0)
        return d
      }
    },
    {
      text: '今天结束',
      value: () => {
        const d = new Date(anchor)
        d.setHours(23, 59, 59, 999)
        return d
      }
    },
    {
      text: '现实现在',
      value: () => new Date()
    }
  ]

  // 场景化快捷项：聚焦选中的日志发生时间后 30 分钟 (建议 2)
  if (props.selectedLog && props.selectedLog.timestamp) {
    shortcuts.unshift({
      text: '🎯 聚焦日志后30m',
      value: () => {
        const t = new Date(props.selectedLog.timestamp).getTime()
        return new Date(t + 30 * 60 * 1000)
      }
    })
  }

  return shortcuts
})
</script>

<style scoped>
.audit-filter-bar {
  padding: 8px 10px;
  border-bottom: 1px solid #e2e8f0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex-shrink: 0;
  background: #f8fafc;
  transition: all 0.2s ease;
}

.filter-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

/* 行 1: 核心常驻行 */
.filter-row-primary {
  display: flex;
  align-items: center;
  gap: 6px;
}

.keyword-input {
  flex: 1;
}

.reset-filter-btn {
  padding: 0 8px;
  font-size: 12px;
}

.fullscreen-btn-group {
  display: inline-flex;
}

.fullscreen-main-btn {
  padding: 0 7px;
}

.fullscreen-arrow-btn {
  padding: 0 4px;
}

.dropdown-tag {
  margin-left: 8px;
  font-size: 10px;
  color: #3b82f6;
  background: #eff6ff;
  padding: 1px 4px;
  border-radius: 2px;
}

/* 过滤胶囊栏 */
.active-filter-chips-bar {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 0;
  font-size: 11px;
}

.chips-label {
  color: #64748b;
  font-size: 11px;
  flex-shrink: 0;
}

.chips-scroll-container {
  display: flex;
  align-items: center;
  gap: 4px;
  overflow-x: hidden;
  flex-wrap: nowrap;
}

.filter-chip-item {
  font-size: 11px;
  height: 22px;
  line-height: 20px;
  padding: 0 6px;
  max-width: 160px;
}

.filter-chip-item .chip-text {
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
  vertical-align: middle;
}

.overflow-tag {
  cursor: pointer;
}

.overflow-chips-popover {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  max-width: 260px;
}

/* 折叠容器与各行 (修复 P1-1) */
.filter-collapsible-wrapper {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 240px;
  opacity: 1;
  overflow: hidden;
  transition: max-height 0.25s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.2s ease, margin 0.2s ease;
}

.audit-filter-bar.is-collapsed .filter-collapsible-wrapper {
  max-height: 0;
  opacity: 0;
  margin-top: 0;
  margin-bottom: 0;
  pointer-events: none;
}

/* 行 2: 时间与排序三等分行 */
.filter-row-time-triplet {
  display: flex;
  gap: 6px;
}

.triplet-item {
  flex: 1;
  min-width: 0;
}

:deep(.triplet-item.el-date-editor) {
  --el-date-editor-width: 100%;
  width: 100% !important;
}

:deep(.triplet-item .el-input__wrapper) {
  padding-left: 4px;
  padding-right: 4px;
}

:deep(.triplet-item .el-input__inner) {
  font-size: 11px;
}

/* 行 3: 设备/级别/匹配状态属性行 */
.filter-row-attributes {
  display: flex;
  gap: 6px;
}

.attr-item {
  flex: 1;
  min-width: 0;
}

/* 行 4: 标签筛选行 */
.filter-row-tags {
  display: flex;
  gap: 6px;
  align-items: center;
}

.tag-select-item {
  flex: 1;
  min-width: 0;
}

.tag-logic-btn {
  padding: 0 8px;
  flex-shrink: 0;
}

.tag-manage-btn {
  padding: 0 8px;
  flex-shrink: 0;
}

/* 底部悬浮展开/收缩控制条 */
.filter-toggle-pill-bar {
  display: flex;
  justify-content: center;
  align-items: center;
  padding-top: 2px;
  cursor: pointer;
  user-select: none;
}

.pill-handle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 2px 14px;
  border-radius: 9999px;
  background: #f1f5f9;
  border: 1px solid #cbd5e1;
  color: #475569;
  font-size: 11px;
  transition: all 0.2s ease;
}

.pill-handle:hover {
  background: #e2e8f0;
  border-color: #94a3b8;
  color: #0284c7;
  transform: translateY(1px);
}

.pill-icon {
  font-size: 11px;
}

.pill-badge {
  font-size: 10px;
  background: #38bdf8;
  color: #ffffff;
  padding: 0 5px;
  border-radius: 9999px;
  margin-left: 2px;
  font-weight: 600;
}
</style>
