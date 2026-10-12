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

    <!-- 日志流顶部高级筛选操作条 -->
    <div class="log-stream-toolbar">
      <span class="log-total-text">共 {{ totalLogs }} 条日志</span>
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

    <!-- 日志流列表 (支持经典单列与 Zen Mode 自适应多列网格) -->
    <div
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

    <!-- 分页栏 -->
    <div class="pagination-bar">
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
import { ref, nextTick } from 'vue'
import { Opportunity } from '@element-plus/icons-vue'
import AuditFilterBar from '@/components/AuditFilterBar.vue'
import AuditLogCard from '@/components/AuditLogCard.vue'
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
  }
})

const emit = defineEmits([
  'filter-change',
  'filter-reset',
  'open-advanced-filter',
  'select',
  'hover-enter',
  'hover-leave',
  'page-change',
  'size-change'
])

const filterStore = useFilterStore()
const workbenchUIStore = useWorkbenchUIStore()
const listContainerRef = ref(null)

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
