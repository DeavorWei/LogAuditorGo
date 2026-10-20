<template>
  <el-dialog
    :model-value="modelValue"
    :title="title"
    class="dlg dlg-md dlg-top"
    destroy-on-close
    @update:model-value="val => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-form label-position="top">
      <el-form-item label="任务名称">
        <el-input
          v-model="form.taskName"
          placeholder="例如: Core-SW-01 故障排查-20260415"
          maxlength="120"
          show-word-limit
        />
      </el-form-item>
      <el-form-item v-if="showContent" label="粘贴 Syslog 日志文本">
        <el-input
          v-model="form.content"
          type="textarea"
          :rows="6"
          placeholder="粘贴设备日志，例如: Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042]: BGP session authentication failed..."
        />
      </el-form-item>
      <!--
        产品决策（规划 §6.6）：任务级"设备类型"不参与任何匹配，
        匹配上下文由设备行逐台决议，因此新建任务不再要求选择设备类型。
        API 仍兼容该字段（旧任务/脚本可继续传），只是 UI 不再暴露。
      -->
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="设备型号无需在此选择"
        description="系统会按日志中的 Hostname 自动识别设备型号；如需指定，可在任务创建后的“设备”抽屉中逐台配置。"
      />
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

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  showContent: { type: Boolean, default: false },
  loading: { type: Boolean, default: false },
  initialTaskName: { type: String, default: '' },
  title: { type: String, default: '新建排障任务' }
})

const emit = defineEmits(['update:modelValue', 'confirm'])

const form = ref({
  taskName: '',
  content: ''
})

const onOpen = () => {
  form.value.taskName = props.initialTaskName || ''
  form.value.content = ''
}

const close = () => {
  emit('update:modelValue', false)
}
</script>

<style scoped>
.full-width-select {
  width: 100%;
}
</style>
