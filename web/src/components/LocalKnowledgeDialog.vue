<template>
  <el-dialog
    :model-value="modelValue"
    :title="editing ? '编辑本地知识' : '补充本地知识'"
    width="640px"
    :close-on-click-modal="false"
    @update:model-value="(val) => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="本地知识（非官方）"
      description="本地知识优先于官方匹配（置信度 0.95），仅保存在本机；请勿将其误认为华为官方原文。"
      class="tip"
    />
    <el-form label-position="top">
      <el-row :gutter="12">
        <el-col :span="12">
          <el-form-item label="模块" required>
            <el-input v-model="form.module" placeholder="例如 BGP" :disabled="editing" />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item label="助记符 / 事件简名" required>
            <el-input v-model="form.brief" placeholder="例如 PEER_BACKWARD" :disabled="editing" />
          </el-form-item>
        </el-col>
      </el-row>
      <el-row :gutter="12">
        <el-col :span="8">
          <el-form-item label="产品/系列">
            <el-input v-model="form.product_family" placeholder="通用 / CE16800" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="软件版本">
            <el-input v-model="form.product_version" placeholder="可选" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="严重级别">
            <el-select v-model="form.severity" class="w-full">
              <el-option v-for="opt in severityOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
          </el-form-item>
        </el-col>
      </el-row>
      <el-form-item label="日志含义">
        <el-input v-model="form.description" type="textarea" :rows="3" placeholder="这条日志代表什么？" />
      </el-form-item>
      <el-form-item label="可能原因">
        <el-input v-model="form.cause" type="textarea" :rows="2" />
      </el-form-item>
      <el-form-item label="处理建议">
        <el-input v-model="form.action" type="textarea" :rows="3" />
      </el-form-item>
      <el-row :gutter="12">
        <el-col :span="12">
          <el-form-item label="参考链接">
            <el-input v-model="form.reference" placeholder="可选，http(s)://" />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item label="日志类型">
            <el-select v-model="form.entry_type" class="w-full">
              <el-option label="Syslog 日志" value="LOG" />
              <el-option label="SNMP Trap 告警" value="ALARM" />
            </el-select>
          </el-form-item>
        </el-col>
      </el-row>
    </el-form>

    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存本地知识</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  initial: { type: Object, default: () => ({}) },
  editing: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'saved'])

const saving = ref(false)

const severityOptions = [
  { value: 1, label: '1 · 严重' },
  { value: 2, label: '2 · 严重' },
  { value: 3, label: '3 · 错误' },
  { value: 4, label: '4 · 错误' },
  { value: 5, label: '5 · 警告' },
  { value: 6, label: '6 · 提示' },
  { value: 7, label: '7 · 提示' },
  { value: 8, label: '8 · 提示' }
]

const defaultForm = () => ({
  id: 0,
  module: '',
  brief: '',
  product_family: '通用',
  product_version: '',
  severity: 4,
  entry_type: 'LOG',
  description: '',
  cause: '',
  action: '',
  impact: '',
  reference: ''
})

const form = ref(defaultForm())

const onOpen = () => {
  const initial = props.initial || {}
  form.value = {
    ...defaultForm(),
    ...initial,
    id: initial.id || 0,
    product_family: initial.product_family || (initial.versions?.[0]?.product_type || '通用'),
    product_version: initial.product_version || (initial.versions?.[0]?.product_version || ''),
    description: initial.description || '',
    cause: initial.cause || '',
    action: initial.action || '',
    reference: initial.reference_url || '',
    entry_type: initial.entry_type || 'LOG',
    severity: initial.severity || 4
  }
}

const save = async () => {
  if (!form.value.module.trim() || !form.value.brief.trim()) {
    ElMessage.warning('模块与助记符为必填')
    return
  }
  saving.value = true
  try {
    const payload = {
      module: form.value.module,
      brief: form.value.brief,
      product_family: form.value.product_family,
      product_version: form.value.product_version,
      severity: form.value.severity,
      entry_type: form.value.entry_type,
      description: form.value.description,
      cause: form.value.cause,
      action: form.value.action,
      impact: form.value.impact,
      reference: form.value.reference
    }
    const res = props.editing && form.value.id
      ? await api.updateLocalKnowledge(form.value.id, payload)
      : await api.createLocalKnowledge(payload)
    if (res.code === 0) {
      ElMessage.success('本地知识已保存，匹配已即时生效')
      emit('saved', res.data)
      emit('update:modelValue', false)
    }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.tip {
  margin-bottom: 12px;
}

.w-full {
  width: 100%;
}
</style>
