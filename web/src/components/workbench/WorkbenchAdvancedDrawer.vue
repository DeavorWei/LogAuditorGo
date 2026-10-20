<template>
  <el-drawer
    :model-value="modelValue"
    title="高级"
    size="76%"
    destroy-on-close
    @update:model-value="(val) => $emit('update:modelValue', val)"
  >
    <div class="advanced-drawer-body">
      <!-- 专家筛选 -->
      <el-card shadow="never" class="adv-card">
        <template #header>
          <div class="card-title">
            <el-icon><Filter /></el-icon>
            <span>专家筛选</span>
            <el-tag size="small" type="info">渐进披露</el-tag>
          </div>
        </template>
        <p class="card-desc">
          支持多条件组合、正则、KV 参数、标签组合与预设保存。默认筛选已覆盖关键词、级别、匹配状态、设备与时间范围，
          只有需要精确圈选复杂条件时才需要使用专家筛选。
        </p>
        <div class="card-actions">
          <el-button type="primary" plain :icon="Filter" @click="$emit('open-advanced-filter')">
            打开专家筛选
          </el-button>
          <el-button :icon="Download" :disabled="!taskId" @click="exportFilteredCSV">
            导出当前筛选结果 (CSV)
          </el-button>
        </div>
      </el-card>

      <!-- 关联事件（实验性） -->
      <el-card shadow="never" class="adv-card">
        <template #header>
          <div class="card-title">
            <el-icon><Aim /></el-icon>
            <span>关联事件（实验性）</span>
            <el-tag size="small" :type="rcaEnabled ? 'warning' : 'info'">
              {{ rcaEnabled ? '已开启' : '默认关闭' }}
            </el-tag>
          </div>
        </template>

        <template v-if="!rcaEnabled">
          <p class="card-desc">
            该能力仅内置 5 条协议故障传播规则、按设备隔离分析，结果可能误报，因此默认关闭且不自动运行。
            开启后仅在此抽屉内出现，需要手动点击“分析关联事件”才会计算。
          </p>
          <div class="card-actions">
            <el-button type="warning" plain :icon="Setting" @click="goToLabSettings">
              前往系统设置 → 实验室功能
            </el-button>
          </div>
        </template>

        <template v-else>
          <p class="card-desc">
            实验性能力：不自动运行、不进入任务 KPI 与主报告。「疑似根因/关联起点」与「参考排查建议」均为启发式推断，不是官方文档原文。
          </p>
          <el-collapse v-model="activeCollapse" class="adv-collapse">
            <el-collapse-item name="rca">
              <template #title>
                <span class="collapse-title">查看关联事件分析</span>
              </template>
              <RcaCenter
                v-if="taskId && activeCollapse.includes('rca')"
                :task-id="taskId"
                @jump-to-log="(id) => $emit('jump-to-log', id)"
                @task-updated="(task) => $emit('task-updated', task)"
              />
            </el-collapse-item>
          </el-collapse>
        </template>
      </el-card>

      <!-- 原始字段与诊断 -->
      <el-card shadow="never" class="adv-card">
        <template #header>
          <div class="card-title">
            <el-icon><Tickets /></el-icon>
            <span>任务原始字段与诊断信息</span>
          </div>
        </template>
        <el-descriptions :column="2" size="small" border>
          <el-descriptions-item label="任务 ID">{{ task?.task_id || '-' }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ statusLabel }}</el-descriptions-item>
          <el-descriptions-item label="设备数">{{ task?.device_count || 0 }}</el-descriptions-item>
          <el-descriptions-item label="日志 / 匹配">
            {{ task?.log_count || 0 }} / {{ task?.matched_count || 0 }}
          </el-descriptions-item>
          <el-descriptions-item label="关联事件状态">
            {{ task?.rca_status || '-' }}
          </el-descriptions-item>
          <el-descriptions-item label="创建时间">{{ formatTime(task?.start_time) }}</el-descriptions-item>
          <el-descriptions-item label="完成时间">{{ formatTime(task?.finish_time) }}</el-descriptions-item>
          <el-descriptions-item label="失败原因">
            {{ task?.error_message || '无' }}
          </el-descriptions-item>
        </el-descriptions>
      </el-card>
    </div>
  </el-drawer>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Filter, Aim, Tickets, Setting, Download } from '@element-plus/icons-vue'
import RcaCenter from '@/components/RcaCenter.vue'
import api from '@/api'
import { useFeatureStore } from '@/stores/features'
import { useFilterStore } from '@/stores/filter'
import { taskStatusMeta } from '@/constants/taskStatus'
import { formatTime } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  taskId: { type: String, default: '' },
  task: { type: Object, default: null }
})

defineEmits(['update:modelValue', 'open-advanced-filter', 'jump-to-log', 'task-updated'])

const router = useRouter()
const featureStore = useFeatureStore()
const filterStore = useFilterStore()

const rcaEnabled = computed(() => featureStore.rcaEnabled)
const activeCollapse = ref([])

const statusLabel = computed(() => taskStatusMeta(props.task?.status).label)

const goToLabSettings = () => {
  router.push({ path: '/settings', query: { section: 'lab' } })
}

const exportFilteredCSV = async () => {
  if (!props.taskId) return
  try {
    await api.downloadTaskReport(props.taskId, 'csv', filterStore.toLogQueryBody())
    ElMessage.success('已导出当前筛选结果')
  } catch (e) {
    console.error('Export filtered CSV failed:', e)
  }
}
</script>

<style scoped>
.advanced-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.adv-card :deep(.el-card__header) {
  padding: 10px 14px;
}

.card-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  color: var(--text-primary);
}

.card-desc {
  margin: 0 0 12px;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: 1.7;
}

.card-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.adv-collapse {
  margin-top: 8px;
}

.collapse-title {
  font-weight: 600;
  color: var(--brand-600);
}
</style>
