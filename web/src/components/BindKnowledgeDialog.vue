<template>
  <el-dialog
    :model-value="modelValue"
    title="绑定到已有官方知识"
    width="620px"
    :close-on-click-modal="false"
    @update:model-value="(val) => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="匹配覆盖（LOCAL_MAPPING，置信度 0.90）"
      description="把当前日志的模块/助记符绑定到一条已有官方知识；下次解析时该日志会直接使用这条知识。"
      class="tip"
    />
    <el-form label-position="top">
      <el-row :gutter="12">
        <el-col :span="12">
          <el-form-item label="模块">
            <el-input v-model="module" placeholder="例如 BGP" />
          </el-form-item>
        </el-col>
        <el-col :span="12">
          <el-form-item label="助记符">
            <el-input v-model="brief" placeholder="例如 PEER_BACKWARD" />
          </el-form-item>
        </el-col>
      </el-row>
      <el-form-item label="选择官方知识">
        <el-select
          v-model="selectedId"
          filterable
          remote
          clearable
          :remote-method="search"
          :loading="searching"
          placeholder="输入模块/助记符/关键词搜索官方知识"
          class="w-full"
        >
          <el-option
            v-for="item in options"
            :key="item.knowledge_id || item.id"
            :label="`${item.module}/${item.brief} · ${item.description || item.message || ''}`"
            :value="item.knowledge_id || item.id"
          />
        </el-select>
      </el-form-item>
      <p class="hint">只支持绑定官方知识；本地知识已经是精确匹配，无需绑定。</p>
    </el-form>

    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="saving" :disabled="!selectedId" @click="save">保存绑定</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  module: { type: String, default: '' },
  brief: { type: String, default: '' }
})

const emit = defineEmits(['update:modelValue', 'saved'])

const selectedId = ref(null)
const options = ref([])
const searching = ref(false)
const saving = ref(false)

const module = ref('')
const brief = ref('')

const onOpen = () => {
  module.value = props.module || ''
  brief.value = props.brief || ''
  selectedId.value = null
  options.value = []
  search(`${module.value} ${brief.value}`.trim())
}

const search = async (keyword) => {
  if (!keyword) return
  searching.value = true
  try {
    const res = await api.searchKnowledge({ keyword, page: 1, page_size: 20 })
    if (res.code === 0) {
      options.value = (res.data?.hits || []).filter((item) => item.source !== 'local')
    }
  } finally {
    searching.value = false
  }
}

const save = async () => {
  if (!module.value.trim() || !brief.value.trim() || !selectedId.value) {
    ElMessage.warning('请填写模块/助记符并选择目标知识')
    return
  }
  saving.value = true
  try {
    const res = await api.createKnowledgeOverride({
      module: module.value,
      brief: brief.value,
      knowledge_id: selectedId.value
    })
    if (res.code === 0) {
      ElMessage.success('匹配覆盖已保存，下次解析即时生效')
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

.hint {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  margin: 0;
}
</style>
