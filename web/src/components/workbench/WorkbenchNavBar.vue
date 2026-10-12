<template>
  <div class="workbench-nav-bar">
    <el-radio-group
      :model-value="modelValue"
      size="default"
      @update:model-value="(val) => $emit('update:modelValue', val)"
    >
      <el-radio-button :value="VIEW_MODE.WORKBENCH" :label="VIEW_MODE.WORKBENCH">
        <el-icon style="margin-right: 4px; vertical-align: middle;"><Document /></el-icon>
        <span>日志审计工作台</span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.DEVICES" :label="VIEW_MODE.DEVICES">
        <el-icon style="margin-right: 4px; vertical-align: middle;"><Monitor /></el-icon>
        <span>设备管理</span>
        <span v-if="currentTask && currentTask.device_count" class="nav-count-badge">
          {{ currentTask.device_count }}
        </span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.MULTI_TIMELINE" :label="VIEW_MODE.MULTI_TIMELINE">
        <el-icon style="margin-right: 4px; vertical-align: middle;"><Histogram /></el-icon>
        <span>多设备协同时间线</span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.RCA" :label="VIEW_MODE.RCA">
        <el-icon style="margin-right: 4px; vertical-align: middle;"><Aim /></el-icon>
        <span>RCA 故障联动</span>
        <el-tooltip
          v-if="currentTask && (currentTask.rca_status === 'QUEUED' || currentTask.rca_status === 'RUNNING')"
          content="根因拓扑正在后台推导中，工作台已就绪可正常审计"
          placement="top"
        >
          <el-icon class="is-loading" style="margin-left: 6px; color: #409eff; vertical-align: middle;"><Loading /></el-icon>
        </el-tooltip>
        <el-tooltip
          v-else-if="currentTask && currentTask.rca_status === 'TIMEOUT'"
          :content="`根因拓扑分析超时截断，已推导出 ${currentTask.rca_count || 0} 个事件`"
          placement="top"
        >
          <span class="nav-count-badge warning">
            {{ currentTask.rca_count || 0 }}!
          </span>
        </el-tooltip>
        <span
          v-else-if="currentTask && currentTask.rca_count"
          class="nav-count-badge danger"
        >
          {{ currentTask.rca_count }}
        </span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.MULTI_REPORT" :label="VIEW_MODE.MULTI_REPORT">
        <el-icon style="margin-right: 4px; vertical-align: middle;"><DataAnalysis /></el-icon>
        <span>多设备对比诊断报告</span>
      </el-radio-button>
    </el-radio-group>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { Document, Monitor, Histogram, Aim, Loading, DataAnalysis } from '@element-plus/icons-vue'
import { VIEW_MODE } from '@/constants/viewModes'
import { useTaskStore } from '@/stores/task'

defineProps({
  modelValue: {
    type: String,
    default: VIEW_MODE.WORKBENCH
  }
})

defineEmits(['update:modelValue'])

const taskStore = useTaskStore()
const currentTask = computed(() => taskStore.currentTask)
</script>

<style scoped>
.workbench-nav-bar {
  display: flex;
  justify-content: flex-start;
  align-items: center;
  background: #ffffff;
  padding: 8px 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
  flex-shrink: 0;
}

.workbench-nav-bar :deep(.el-radio-button__inner) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 32px;
  line-height: 32px;
  padding: 0 14px;
  box-sizing: border-box;
}

.nav-count-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 16px;
  min-width: 16px;
  padding: 0 4px;
  margin-left: 6px;
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
  border-radius: 8px;
  background-color: #3b82f6;
  color: #ffffff;
  box-sizing: border-box;
}

.nav-count-badge.danger {
  background-color: #ef4444;
}

.nav-count-badge.warning {
  background-color: #f59e0b;
}

.workbench-nav-bar :deep(.el-radio-button.is-active) .nav-count-badge {
  background-color: rgba(255, 255, 255, 0.28);
  color: #ffffff;
}

.workbench-nav-bar :deep(.el-radio-button.is-active) .nav-count-badge.danger {
  background-color: #dc2626;
  color: #ffffff;
}

.workbench-nav-bar :deep(.el-radio-button.is-active) .nav-count-badge.warning {
  background-color: #d97706;
  color: #ffffff;
}
</style>
