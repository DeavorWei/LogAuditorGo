<template>
  <el-dialog
    :model-value="modelValue"
    title="新建日志审计任务"
    class="dlg dlg-md dlg-top"
    destroy-on-close
    @update:model-value="val => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-form label-position="top">
      <el-form-item label="任务名称">
        <el-input
          v-model="form.taskName"
          placeholder="例如: Core-SW-01故障排查-20260415"
        />
      </el-form-item>
      <el-form-item label="设备类型">
        <el-select v-model="form.deviceType" style="width: 100%;">
          <el-option
            v-for="opt in DEVICE_TYPE_OPTIONS"
            :key="opt.value"
            :label="opt.label"
            :value="opt.value"
          />
        </el-select>
      </el-form-item>
      <el-form-item v-if="showContent" label="粘贴 Syslog 日志文本">
        <el-input
          v-model="form.content"
          type="textarea"
          :rows="6"
          placeholder="粘贴设备日志，例如: Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042]: BGP session authentication failed..."
        />
      </el-form-item>
    </el-form>

    <template #footer>
      <slot name="footer" :form="form" :loading="loading" :close="close">
        <el-button @click="close">取消</el-button>
        <el-button
          type="primary"
          :loading="loading"
          @click="$emit('confirm', form)"
        >
          确定
        </el-button>
      </slot>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { DEVICE_TYPE_OPTIONS, DEFAULT_DEVICE_TYPE } from '@/constants/deviceTypes'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  showContent: { type: Boolean, default: false },
  loading: { type: Boolean, default: false },
  initialTaskName: { type: String, default: '' },
  defaultDeviceType: { type: String, default: DEFAULT_DEVICE_TYPE }
})

const emit = defineEmits(['update:modelValue', 'confirm'])

const form = ref({
  taskName: '',
  deviceType: DEFAULT_DEVICE_TYPE,
  content: ''
})

const onOpen = () => {
  form.value.taskName = props.initialTaskName || ''
  form.value.deviceType = props.defaultDeviceType || DEFAULT_DEVICE_TYPE
  form.value.content = ''
}

const close = () => {
  emit('update:modelValue', false)
}
</script>
