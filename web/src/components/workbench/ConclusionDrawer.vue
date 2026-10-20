<template>
  <el-drawer
    :model-value="modelValue"
    title="案例结论"
    size="46%"
    destroy-on-close
    @update:model-value="(val) => $emit('update:modelValue', val)"
    @open="loadTask"
  >
    <div class="conclusion-body" v-loading="loading">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="结论会进入案例库与导出报告"
        description="建议写清：现象、定位过程、根因/疑似根因、处置动作与遗留风险。草稿由系统按统计生成，可自由修改。"
        class="tip"
      />

      <el-form label-position="top">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="案例状态">
              <el-select v-model="form.caseStatus" class="w-full">
                <el-option
                  v-for="opt in CASE_STATUS_OPTIONS"
                  :key="opt.value"
                  :label="opt.label"
                  :value="opt.value"
                />
              </el-select>
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="工单号 / 外部引用">
              <el-input v-model="form.externalRef" maxlength="128" placeholder="例如 INC-20260415-001" />
            </el-form-item>
          </el-col>
        </el-row>

        <el-form-item label="结论">
          <el-input
            v-model="form.conclusion"
            type="textarea"
            :rows="12"
            maxlength="8000"
            show-word-limit
            placeholder="写清现象、定位过程、根因判断与处置结果；支持 Markdown。"
          />
          <div class="conclusion-actions">
            <el-button size="small" :icon="MagicStick" :loading="drafting" @click="generateDraft">
              生成草稿
            </el-button>
            <span class="hint">草稿根据任务统计生成，不会自动覆盖已保存结论</span>
          </div>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.notes" type="textarea" :rows="3" maxlength="2000" placeholder="现场信息、遗留风险、后续动作等" />
        </el-form-item>

        <el-form-item label="标签（逗号分隔）">
          <el-input v-model="form.caseTags" maxlength="512" placeholder="例如：BGP,链路中断,已恢复" />
        </el-form-item>
      </el-form>
    </div>

    <template #footer>
      <el-button @click="$emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="saving" @click="save">保存结论</el-button>
    </template>
  </el-drawer>
</template>

<script setup>
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { MagicStick } from '@element-plus/icons-vue'
import api from '@/api'
import { CASE_STATUS_OPTIONS } from '@/constants/caseStatus'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  taskId: { type: String, default: '' },
  task: { type: Object, default: null }
})

const emit = defineEmits(['update:modelValue', 'saved'])

const loading = ref(false)
const saving = ref(false)
const drafting = ref(false)

const form = ref({
  caseStatus: '',
  conclusion: '',
  notes: '',
  externalRef: '',
  caseTags: ''
})

const applyTask = (task) => {
  if (!task) return
  form.value = {
    caseStatus: task.case_status || '',
    conclusion: task.conclusion || '',
    notes: task.notes || '',
    externalRef: task.external_ref || '',
    caseTags: task.case_tags || ''
  }
}

let loadSeq = 0
const loadTask = async () => {
  const seq = ++loadSeq
  const taskId = props.taskId
  applyTask(props.task)
  if (!taskId) return
  loading.value = true
  try {
    const res = await api.getTask(taskId)
    // 快速切换任务时丢弃旧响应，避免 A 的结论覆盖 B 的表单
    if (seq !== loadSeq || taskId !== props.taskId) return
    if (res.code === 0) applyTask(res.data)
  } catch (e) {
    // 保留 props 中的已有值，错误由拦截器提示
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

const generateDraft = async () => {
  if (!props.taskId) return
  if (form.value.conclusion.trim()) {
    try {
      await ElMessageBox.confirm(
        '当前已有结论内容，生成草稿会覆盖编辑框中的内容（不会直接保存）。是否继续？',
        '生成结论草稿',
        { type: 'warning', confirmButtonText: '替换', cancelButtonText: '取消' }
      )
    } catch (e) {
      return
    }
  }
  drafting.value = true
  try {
    const res = await api.getConclusionDraft(props.taskId)
    if (res.code === 0 && res.data?.markdown) {
      form.value.conclusion = res.data.markdown
      ElMessage.success('草稿已生成，请核对后保存')
    }
  } finally {
    drafting.value = false
  }
}

const save = async () => {
  if (!props.taskId) return
  saving.value = true
  try {
    const res = await api.updateTaskCase(props.taskId, {
      case_status: form.value.caseStatus || '',
      conclusion: form.value.conclusion,
      notes: form.value.notes,
      external_ref: form.value.externalRef,
      case_tags: form.value.caseTags
    })
    if (res.code === 0) {
      emit('saved', res.data)
      emit('update:modelValue', false)
    }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.conclusion-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tip {
  margin-bottom: 8px;
}

.w-full {
  width: 100%;
}

.conclusion-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 6px;
}

.hint {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}
</style>
