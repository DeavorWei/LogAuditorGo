<template>
  <el-drawer
    :model-value="modelValue"
    title="设备管理"
    size="78%"
    destroy-on-close
    @update:model-value="(val) => $emit('update:modelValue', val)"
  >
    <div class="device-drawer-body">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="设备型号决定知识匹配上下文"
        description="系统按日志 Hostname 自动归属设备并嗅探型号；手工修改设备型号/版本后需执行“重新分析”才会重新匹配。"
        class="drawer-tip"
      />
      <DeviceManager
        v-if="modelValue && taskId"
        :task-id="taskId"
        @devices-updated="(devices) => $emit('devices-updated', devices)"
      />
      <el-empty v-else description="请先选择一个排障任务" />
    </div>
  </el-drawer>
</template>

<script setup>
import DeviceManager from '@/components/DeviceManager.vue'

defineProps({
  modelValue: { type: Boolean, default: false },
  taskId: { type: String, default: '' }
})

defineEmits(['update:modelValue', 'devices-updated'])
</script>

<style scoped>
.device-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
}

.drawer-tip {
  flex-shrink: 0;
}
</style>
