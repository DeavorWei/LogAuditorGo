<template>
  <div class="col-left" :class="{ 'zen-mode': workbenchUIStore.isImmersive }">
    <!-- 筛选面板 -->
    <AuditFilterBar
      :task-devices="taskDevices"
      :selected-log="selectedLog"
      :latest-log-time="latestLogTime"
      @change="$emit('filter-change')"
      @reset="$emit('filter-reset')"
    />

    <!-- 日志流顶部操作条：视图切换 / 注释 / 直方图 / 高级筛选 -->
    <div class="log-stream-toolbar">
      <span class="log-total-text">共 {{ totalLogs }} 条日志</span>
      <div class="toolbar-right">
        <el-checkbox
          :model-value="filterStore.filters.showComments"
          size="small"
          class="comment-toggle"
          @change="onToggleComments"
        >
          显示注释<template v-if="commentCount > 0">（{{ commentCount }}）</template>
        </el-checkbox>
        <el-button size="small" text :icon="Histogram" @click="showHistogram = !showHistogram">
          时间分布
        </el-button>
        <el-radio-group v-model="viewMode" size="small" @change="onViewModeChange">
          <el-radio-button value="detail">明细</el-radio-button>
          <el-radio-button value="aggregate">聚合</el-radio-button>
        </el-radio-group>
        <el-badge
          :value="filterStore.activeAdvancedCount()"
          :hidden="filterStore.activeAdvancedCount() === 0"
          type="danger"
        >
          <el-button
            size="small"
            :type="filterStore.activeAdvancedCount() > 0 ? 'primary' : 'default'"
            plain
            class="adv-filter-btn"
            @click="$emit('open-advanced-filter')"
          >
            <el-icon><Opportunity /></el-icon>
            <span>高级筛选</span>
          </el-button>
        </el-badge>
      </div>
    </div>

    <!-- 时间直方图（v1.4）：点击柱状图按时间段筛选 -->
    <LogHistogram
      v-if="showHistogram && taskId"
      :task-id="taskId"
      :query="filterStore.toLogQueryBody(1, 100)"
      :refresh-key="refreshKey"
      @select-range="onRangeSelect"
    />

    <!-- 聚合视图（v1.4）：同模块/助记符/主机在时间窗内折叠 -->
    <div v-if="viewMode === 'aggregate'" class="aggregate-list" v-loading="aggregateLoading">
      <div
        v-for="group in aggregateGroups"
        :key="`${group.module}-${group.brief}-${group.hostname}-${group.window_start}`"
        class="aggregate-card"
        role="button"
        tabindex="0"
        @click="onGroupClick(group)"
        @keydown.enter.space.prevent="onGroupClick(group)"
      >
        <div class="aggregate-head">
          <SeverityTag :severity="group.max_severity" />
          <span class="aggregate-title">{{ group.module }}/{{ group.brief }}</span>
          <el-tag size="small" type="info">{{ group.count }} 条</el-tag>
        </div>
        <div class="aggregate-meta">
          <span v-if="group.hostname">{{ group.hostname }}</span>
          <span>{{ formatTime(group.first_seen) }} ~ {{ formatTime(group.last_seen) }}</span>
        </div>
      </div>
      <el-empty v-if="!aggregateLoading && aggregateGroups.length === 0" description="当前筛选下没有可聚合的日志" />
      <p class="aggregate-tip">聚合按“模块/助记符/主机名 + 300s 时间窗”折叠；点击卡片可查看该组明细。</p>
    </div>

    <!-- 日志流列表 (支持经典单列与 Zen Mode 自适应多列网格) -->
    <div
      v-else
      ref="listContainerRef"
      class="log-stream-list"
      :class="{ 'is-zen-grid': workbenchUIStore.isImmersive }"
      :style="workbenchUIStore.isImmersive ? { '--zen-cols': workbenchUIStore.ui.zenColumns || 3 } : {}"
      v-loading="loading"
    >
      <AuditLogCard
        v-for="rec in logRecords"
        :key="rec.id"
        :record="rec"
        :active="selectedLog && selectedLog.id === rec.id"
        @click="$emit('select', rec)"
        @hover-enter="$emit('hover-enter', rec, $event)"
        @hover-leave="$emit('hover-leave', rec, $event)"
        @pivot="$emit('pivot', $event)"
      />
      <el-empty
        v-if="!loading && logRecords.length === 0"
        :description="hasActiveFilter ? '未找到符合条件的日志' : '暂无匹配日志'"
      >
        <el-button v-if="hasActiveFilter" type="primary" size="small" plain @click="$emit('filter-reset')">
          清空筛选条件
        </el-button>
      </el-empty>
    </div>

    <!-- 分页栏（聚合视图下隐藏） -->
    <div v-if="viewMode === 'detail'" class="pagination-bar">
      <div class="pagination-center">
        <el-pagination
          :current-page="filterStore.filters.page"
          :page-size="filterStore.filters.pageSize"
          :total="totalLogs"
          layout="prev, pager, next"
          small
          @update:current-page="onPageChange"
        />
      </div>
      <div class="page-size-selector">
        <el-select
          :model-value="filterStore.filters.pageSize"
          size="small"
          class="page-size-select"
          title="调整单页显示日志数量"
          @update:model-value="onPageSizeChange"
        >
          <el-option label="50 条/页" :value="50" />
          <el-option label="100 条/页" :value="100" />
          <el-option label="200 条/页" :value="200" />
          <el-option label="300 条/页" :value="300" />
          <el-option label="500 条/页" :value="500" />
        </el-select>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Histogram, Opportunity } from '@element-plus/icons-vue'
import AuditFilterBar from '@/components/AuditFilterBar.vue'
import AuditLogCard from '@/components/AuditLogCard.vue'
import LogHistogram from '@/components/workbench/LogHistogram.vue'
import SeverityTag from '@/components/SeverityTag.vue'
import api from '@/api'
import { formatTime } from '@/utils/format'
import { useFilterStore } from '@/stores/filter'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'

const props = defineProps({
  taskDevices: {
    type: Array,
    default: () => []
  },
  selectedLog: {
    type: Object,
    default: null
  },
  latestLogTime: {
    type: [String, Number, Date],
    default: null
  },
  logRecords: {
    type: Array,
    default: () => []
  },
  totalLogs: {
    type: Number,
    default: 0
  },
  loading: {
    type: Boolean,
    default: false
  },
  hasActiveFilter: {
    type: Boolean,
    default: false
  },
  taskId: {
    type: String,
    default: ''
  },
  commentCount: {
    type: Number,
    default: 0
  }
})

const emit = defineEmits([
  'filter-change',
  'filter-reset',
  'open-advanced-filter',
  'pivot',
  'select',
  'hover-enter',
  'hover-leave',
  'page-change',
  'size-change'
])

const filterStore = useFilterStore()
const workbenchUIStore = useWorkbenchUIStore()
const listContainerRef = ref(null)

const route = useRoute()
const showHistogram = ref(route.query.histogram === '1')
const viewMode = ref(route.query.mode === 'aggregate' ? 'aggregate' : 'detail')
const aggregateGroups = ref([])
const aggregateLoading = ref(false)

// 筛选条件变化时刷新直方图/聚合（JSON 作为轻量变更键）
const refreshKey = computed(() => JSON.stringify(filterStore.toLogQueryBody(1, 100)))

const onToggleComments = (checked) => {
  filterStore.filters.showComments = Boolean(checked)
  filterStore.filters.page = 1
  emit('filter-change')
}

const onRangeSelect = ({ start, end }) => {
  if (!start || !end) return
  filterStore.filters.timeStart = start
  filterStore.filters.timeEnd = end
  filterStore.filters.page = 1
  emit('filter-change')
}

const fetchAggregates = async () => {
  if (!props.taskId || viewMode.value !== 'aggregate') return
  aggregateLoading.value = true
  try {
    const res = await api.getAggregatedLogs(props.taskId, filterStore.toLogQueryBody(1, 100), 300, 100)
    if (res.code === 0) {
      aggregateGroups.value = res.data?.groups || []
    }
  } catch (e) {
    console.error('Fetch aggregated logs failed:', e)
  } finally {
    aggregateLoading.value = false
  }
}

const onViewModeChange = (mode) => {
  if (mode === 'aggregate') {
    fetchAggregates()
  }
}

const onGroupClick = (group) => {
  // 用 keyword 的 module/brief 语法直接筛选（后端 applyKeywordFilter 支持）
  filterStore.filters.keyword = group.brief ? `${group.module}/${group.brief}` : group.module
  filterStore.filters.page = 1
  viewMode.value = 'detail'
  emit('filter-change')
}

watch(refreshKey, () => {
  if (viewMode.value === 'aggregate') fetchAggregates()
})

onMounted(() => {
  if (viewMode.value === 'aggregate') fetchAggregates()
})

watch(() => props.taskId, () => {
  if (viewMode.value === 'aggregate') fetchAggregates()
})

const onPageChange = (page) => {
  filterStore.filters.page = page
  emit('page-change', page)
}

const onPageSizeChange = (size) => {
  filterStore.filters.pageSize = size
  filterStore.filters.page = 1
  emit('size-change', size)
}

const scrollToLog = (logId) => {
  nextTick(() => {
    if (logId === undefined || logId === null) return
    const el = document.querySelector(`.audit-log-card[data-log-id="${logId}"]`) || document.querySelector('.audit-log-card.active')
    if (el) {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  })
}

defineExpose({
  scrollToLog
})
</script>

<style scoped>
.col-left {
  width: var(--wb-col-left, 28%);
  border-right: 1px solid var(--border-base);
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--bg-subtle);
  transition: width 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

.col-left.zen-mode {
  width: 100%;
  border-right: none;
}

.log-stream-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 2px 10px;
  height: 26px;
  min-height: 26px;
  background: var(--bg-subtle);
  border-bottom: 1px solid var(--border-base);
}

.log-total-text {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  font-weight: 500;
  line-height: 1;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.comment-toggle {
  font-size: var(--fs-xs);
}

.aggregate-list {
  flex: 1;
  overflow-y: auto;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.aggregate-card {
  padding: 10px 12px;
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  background: var(--gray-0);
  cursor: pointer;
  transition: all 0.15s ease;
}

.aggregate-card:hover {
  border-color: var(--brand-400);
  transform: translateY(-1px);
}

.aggregate-head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.aggregate-title {
  flex: 1;
  font-weight: 700;
  color: var(--text-primary);
}

.aggregate-meta {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-top: 4px;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.aggregate-tip {
  margin: 6px 0 0;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  text-align: center;
}

.adv-filter-btn {
  height: 24px;
  padding: 0 8px;
  font-size: var(--fs-xs);
  line-height: 22px;
  border-radius: var(--radius-xs);
}

.log-stream-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.log-stream-list.is-zen-grid {
  display: grid;
  grid-template-columns: repeat(var(--zen-cols, 3), minmax(0, 1fr));
  gap: 8px;
  align-content: start;
}

.pagination-bar {
  padding: 4px 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-top: 1px solid var(--border-base);
  background: var(--gray-0);
  flex-shrink: 0;
}

.pagination-center {
  flex: 1;
  display: flex;
  justify-content: center;
}

.page-size-selector {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}

.page-size-select {
  width: 96px;
}
</style>
