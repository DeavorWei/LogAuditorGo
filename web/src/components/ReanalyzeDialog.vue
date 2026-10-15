<template>
  <el-dialog
    :model-value="modelValue"
    title="重新分析与任务设备配置"
    class="dlg dlg-md dlg-top"
    destroy-on-close
    @update:model-value="val => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-alert
      type="info"
      :closable="false"
      show-icon
      class="mb-3"
      title="修改设备类型将影响知识库版本分档匹配规则，可单独保存属性或立即触发全量重新分析。"
    />
    <el-form label-position="top">
      <el-form-item label="任务名称">
        <el-input v-model="form.taskName" placeholder="任务名称" />
      </el-form-item>
      <el-form-item label="设备类型（产品族）">
        <el-select v-model="form.deviceType" class="full-width-select" style="width: 100%;">
          <el-option
            v-for="opt in TASK_DEVICE_TYPE_OPTIONS"
            :key="opt.value"
            :label="opt.label"
            :value="opt.value"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="设备软件版本（可选，例如 V200R024C00）">
        <el-input
          v-model="form.deviceVersion"
          placeholder="例如: V200R024C00，用于版本分档加权打分"
        />
      </el-form-item>
    </el-form>

    <template #footer>
      <div style="display: flex; justify-content: space-between; align-items: center; width: 100%;">
        <el-button
          type="info"
          plain
          :loading="saving"
          @click="handleSaveOnly"
        >
          仅保存属性
        </el-button>
        <div>
          <el-button @click="$emit('update:modelValue', false)">取消</el-button>
          <el-button
            type="warning"
            :loading="saving"
            @click="handleSaveAndReanalyze"
          >
            保存并重新分析
          </el-button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { TASK_DEVICE_TYPE_OPTIONS, DEFAULT_TASK_DEVICE_TYPE } from '@/constants/deviceTypes'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  task: { type: Object, default: () => ({}) }
})

const emit = defineEmits(['update:modelValue', 'confirm', 'updated'])

const form = ref({
  taskName: '',
  deviceType: DEFAULT_TASK_DEVICE_TYPE,
  deviceVersion: ''
})
const saving = ref(false)

const onOpen = () => {
  form.value = {
    taskName: props.task?.task_name || '',
    deviceType: props.task?.device_type || DEFAULT_TASK_DEVICE_TYPE,
    deviceVersion: props.task?.device_version || ''
  }
}

const handleSaveOnly = async () => {
  if (!props.task?.task_id) return
  saving.value = true
  try {
    const res = await api.updateTask(props.task.task_id, {
      task_name: form.value.taskName,
      device_type: form.value.deviceType,
      device_version: form.value.deviceVersion
    })
    if (res.code === 0) {
      ElMessage.success('任务设备属性已更新')
      emit('updated', res.data)
      emit('update:modelValue', false)
    } else {
      ElMessage.error(res.message || '更新失败')
    }
  } catch (err) {
    ElMessage.error('更新失败: ' + (err?.message || '网络异常'))
  } finally {
    saving.value = false
  }
}

const handleSaveAndReanalyze = () => {
  emit('confirm', {
    task_name: form.value.taskName,
    device_type: form.value.deviceType,
    device_version: form.value.deviceVersion
  })
  emit('update:modelValue', false)
}
</script>

<style scoped>
.mb-3 {
  margin-bottom: 16px;
}
</style>
