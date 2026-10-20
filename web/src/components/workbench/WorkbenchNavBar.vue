<template>
  <div class="workbench-nav-bar">
    <el-radio-group
      :model-value="modelValue"
      size="default"
      @update:model-value="(val) => $emit('update:modelValue', val)"
    >
      <el-radio-button :value="VIEW_MODE.LOGS">
        <el-icon><Document /></el-icon>
        <span>日志分析</span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.TIMELINE">
        <el-icon><Histogram /></el-icon>
        <span>事件时间线</span>
      </el-radio-button>
      <el-radio-button :value="VIEW_MODE.REPORT">
        <el-icon><DataAnalysis /></el-icon>
        <span>分析报告</span>
      </el-radio-button>
    </el-radio-group>
  </div>
</template>

<script setup>
import { Document, Histogram, DataAnalysis } from '@element-plus/icons-vue'
import { VIEW_MODE } from '@/constants/viewModes'

defineProps({
  modelValue: {
    type: String,
    default: VIEW_MODE.LOGS
  }
})

defineEmits(['update:modelValue'])
</script>

<style scoped>
.workbench-nav-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--gray-0);
  padding: 8px 16px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border-base);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
  flex-shrink: 0;
}

.workbench-nav-bar :deep(.el-radio-button__inner) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 32px;
  line-height: 32px;
  padding: 0 16px;
  box-sizing: border-box;
}

.workbench-nav-bar :deep(.el-radio-button__inner .el-icon:first-child) {
  margin-right: 4px;
  vertical-align: middle;
}

</style>
