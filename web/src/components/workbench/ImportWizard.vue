<template>
  <el-dialog
    :model-value="modelValue"
    title="导入日志"
    width="900px"
    class="dlg dlg-lg dlg-top import-wizard"
    destroy-on-close
    :close-on-click-modal="false"
    @update:model-value="(val) => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <el-steps :active="step" finish-status="success" align-center class="wizard-steps">
      <el-step title="任务信息" :description="isExisting ? '已有任务' : '名称与备注'" />
      <el-step title="日志来源" description="原生选择 / 路径 / 粘贴" />
      <el-step title="导入预览" description="文件、设备、冲突" />
    </el-steps>

    <!-- 步骤 1：任务信息（仅新建） -->
    <div v-if="step === 0" class="wizard-panel">
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="任务名称" required>
          <el-input v-model="form.taskName" maxlength="120" show-word-limit placeholder="例如：Core-SW-01 故障排查-20260415" />
        </el-form-item>
        <el-alert
          type="info"
          :closable="false"
          show-icon
          title="设备型号无需在此选择"
          description="系统会按日志 Hostname 自动识别设备型号；如需指定，可在任务创建后的“设备”抽屉中逐台配置。"
        />
      </el-form>
    </div>

    <!-- 步骤 2：日志来源 -->
    <div v-else-if="step === 1" class="wizard-panel">
      <div class="source-grid">
        <button type="button" class="source-tile reset" :class="{ active: form.sourceType === 'file' }" @click="selectSource('file')">
          <el-icon size="26"><Document /></el-icon>
          <strong>选择日志文件</strong>
          <span>调用 Windows 原生文件对话框，可多选 .log/.txt/.syslog/.gz</span>
        </button>
        <button type="button" class="source-tile reset" :class="{ active: form.sourceType === 'folder' }" @click="selectSource('folder')">
          <el-icon size="26"><FolderOpened /></el-icon>
          <strong>选择日志文件夹</strong>
          <span>递归收集目录内全部日志，适合按设备/日期归档的目录</span>
        </button>
        <button type="button" class="source-tile reset" :class="{ active: form.sourceType === 'path' }" @click="selectSource('path')">
          <el-icon size="26"><Position /></el-icon>
          <strong>手动选择路径</strong>
          <span>原生对话框不可用时回退到 Web 路径选择器</span>
        </button>
        <button type="button" class="source-tile reset" :class="{ active: form.sourceType === 'text' }" @click="selectSource('text')">
          <el-icon size="26"><EditPen /></el-icon>
          <strong>粘贴日志文本</strong>
          <span>适合几行到几百行的临时分析</span>
        </button>
      </div>

      <div v-if="form.sourceType !== 'text'" class="source-body">
        <div class="path-actions">
          <el-button type="primary" :icon="FolderOpened" :loading="nativeLoading" @click="pickNative(form.sourceType === 'folder' ? 'folder' : 'file')">
            {{ form.sourceType === 'folder' ? '选择文件夹' : '选择文件' }}
          </el-button>
          <el-button :icon="Position" @click="showPathPicker = true">从本机路径选择器选择</el-button>
          <el-tag v-if="nativeSupported === false" size="small" type="warning">当前环境不支持原生对话框，已回退路径选择器</el-tag>
        </div>

        <div v-if="form.paths.length" class="path-list">
          <el-tag
            v-for="path in form.paths"
            :key="path"
            closable
            type="info"
            class="path-tag"
            @close="removePath(path)"
          >{{ path }}</el-tag>
        </div>
        <el-empty v-else description="尚未选择文件或目录" :image-size="70" />

        <el-collapse class="advanced-source">
          <el-collapse-item title="高级：直接输入绝对路径" name="manual">
            <el-input
              v-model="manualPath"
              placeholder="每行一个绝对路径，例如 D:\logs\core-sw-01.log"
              type="textarea"
              :rows="3"
            />
            <el-button size="small" class="mt-8" @click="addManualPaths">加入路径列表</el-button>
          </el-collapse-item>
        </el-collapse>
      </div>

      <div v-else class="source-body">
        <el-input
          v-model="form.content"
          type="textarea"
          :rows="10"
          placeholder="粘贴 Syslog 文本，例如：Apr 15 2026 14:23:10 HUAWEI-CORE-SW01 %%01BGP/4/BGP_AUTH_FAILED(l)[1042]: BGP session authentication failed..."
        />
        <el-input v-model="form.fileName" class="mt-8" placeholder="来源文件名（可选）" />
      </div>

      <el-alert
        v-if="error"
        type="error"
        show-icon
        :closable="false"
        :title="error"
        class="mt-12"
      />
    </div>

    <!-- 步骤 3：导入预览 -->
    <div v-else class="wizard-panel" v-loading="previewing">
      <template v-if="preview">
        <el-descriptions :column="4" border size="small">
          <el-descriptions-item label="文件数">{{ preview.total_files }}</el-descriptions-item>
          <el-descriptions-item label="日志行数">{{ preview.estimated_logs }}</el-descriptions-item>
          <el-descriptions-item label="设备数">{{ preview.devices?.length || 0 }}</el-descriptions-item>
          <el-descriptions-item label="预计耗时">约 {{ preview.estimated_seconds }} 秒</el-descriptions-item>
        </el-descriptions>

        <div v-if="preview.time_range" class="preview-line">
          时间范围：{{ formatTime(preview.time_range.start) }} ~ {{ formatTime(preview.time_range.end) }}
        </div>

        <el-alert
          v-if="preview.conflicts && preview.conflicts.length"
          type="warning"
          show-icon
          :closable="false"
          class="mt-12"
          :title="`检测到 ${preview.conflicts.length} 个同名日志文件`"
        >
          <div class="conflict-row">
            <span>{{ preview.conflicts.join('、') }}</span>
            <el-radio-group v-model="form.conflictMode" size="small">
              <el-radio-button value="rename">自动重命名</el-radio-button>
              <el-radio-button value="skip">跳过同名</el-radio-button>
              <el-radio-button value="overwrite">覆盖旧文件</el-radio-button>
            </el-radio-group>
          </div>
        </el-alert>

        <div class="preview-columns">
          <el-card shadow="never" class="preview-card">
            <template #header>文件清单</template>
            <el-table :data="preview.files" size="small" max-height="240">
              <el-table-column prop="name" label="文件名" show-overflow-tooltip />
              <el-table-column label="大小" width="90" align="right">
                <template #default="{ row }">{{ formatSize(row.size) }}</template>
              </el-table-column>
              <el-table-column label="行数" width="90" align="right">
                <template #default="{ row }">
                  {{ row.lines }}
                  <el-tooltip v-if="row.sampled" content="文件过大，预览按采样外推" placement="top">
                    <el-tag size="small" type="warning">采样</el-tag>
                  </el-tooltip>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
          <el-card shadow="never" class="preview-card">
            <template #header>检测到的设备</template>
            <div v-if="preview.devices && preview.devices.length" class="device-list">
              <div v-for="dev in preview.devices" :key="dev.hostname" class="device-row">
                <span class="device-name">{{ dev.hostname }}</span>
                <span class="device-count">{{ dev.lines }} 行</span>
              </div>
            </div>
            <el-empty v-else description="未识别到 Hostname（将按文件名归属）" :image-size="60" />
          </el-card>
        </div>

        <p class="preview-note">
          预览只读取文件元数据与采样行，不会写入任务库；点击“开始分析”后再正式解析入库。
          本产品不做浏览器上传，日志始终由后端从本机路径直接读取。
        </p>
      </template>
      <el-empty v-else description="预览加载失败或尚未生成" />
    </div>

    <template #footer>
      <el-button @click="close">取消</el-button>
      <template v-if="step === 0">
        <el-button type="primary" :loading="creating" @click="handleInfoNext">下一步</el-button>
      </template>
      <template v-else-if="step === 1">
        <el-button @click="step = 0" :disabled="isExisting">上一步</el-button>
        <el-button type="primary" :loading="previewing" :disabled="!hasSource" @click="handlePreview">预览并继续</el-button>
      </template>
      <template v-else>
        <el-button @click="step = 1">返回修改</el-button>
        <el-button @click="reloadPreview" :loading="previewing">重新预览</el-button>
        <el-button type="primary" :loading="starting" @click="handleStartImport">开始分析</el-button>
      </template>
    </template>

    <ServerPathPicker
      v-model:visible="showPathPicker"
      mode="both"
      :exts="LOG_EXTS"
      favorite-key="import-wizard"
      title="选择日志文件或目录"
      @confirm="onPickerConfirm"
    />
  </el-dialog>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Document, FolderOpened, Position, EditPen
} from '@element-plus/icons-vue'
import api from '@/api'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import { formatSize, formatTime } from '@/utils/format'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  taskId: { type: String, default: '' },
  taskName: { type: String, default: '' },
  // 新建模式：始终从"任务信息"开始，即使工作台已有当前任务
  createMode: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'task-created', 'job-started'])

const LOG_EXTS = ['log', 'txt', 'syslog', 'gz']

const step = ref(0)
const currentTaskId = ref('')
const creating = ref(false)
const previewing = ref(false)
const starting = ref(false)
const nativeLoading = ref(false)
const nativeSupported = ref(null)
const showPathPicker = ref(false)
const preview = ref(null)
const error = ref('')
const manualPath = ref('')

const form = ref(defaultForm())

function defaultForm() {
  const now = new Date()
  const stamp = now.toISOString().replace(/[-:T.]/g, '').slice(0, 14)
  return {
    taskName: `排障-${stamp}`,
    sourceType: 'file',
    paths: [],
    content: '',
    fileName: '',
    conflictMode: 'rename'
  }
}

const isExisting = computed(() => !props.createMode && Boolean(props.taskId))
const hasSource = computed(() => {
  if (form.value.sourceType === 'text') return form.value.content.trim().length > 0
  return form.value.paths.length > 0
})

const dialogVisible = computed({
  get: () => props.modelValue,
  set: (val) => emit('update:modelValue', val)
})

const onOpen = async () => {
  form.value = defaultForm()
  step.value = 0
  preview.value = null
  error.value = ''
  manualPath.value = ''
  showPathPicker.value = false
  currentTaskId.value = props.createMode ? '' : (props.taskId || '')
  if (!props.createMode && props.taskId) {
    step.value = 1
  } else if (props.taskName && !props.createMode) {
    form.value.taskName = props.taskName
  }
  if (nativeSupported.value === null) {
    try {
      const [capability, features] = await Promise.all([api.getDialogCapability(), api.getFeatures()])
      const enabled = features?.data?.features?.native_file_dialog !== false
      nativeSupported.value = enabled && Boolean(capability?.data?.supported)
    } catch (e) {
      nativeSupported.value = false
    }
  }
}

const close = () => {
  dialogVisible.value = false
}

const handleInfoNext = async () => {
  const name = form.value.taskName.trim()
  if (!name) {
    ElMessage.warning('请填写任务名称')
    return
  }
  creating.value = true
  try {
    const res = await api.createTask({ task_name: name })
    const taskId = res?.data?.task_id
    if (!taskId) throw new Error('创建任务失败')
    currentTaskId.value = taskId
    emit('task-created', taskId)
    step.value = 1
  } catch (e) {
    error.value = e?.message || '创建任务失败'
  } finally {
    creating.value = false
  }
}

const selectSource = (type) => {
  form.value.sourceType = type
  if (type === 'path') {
    showPathPicker.value = true
  }
}

const addPaths = (paths) => {
  const merged = new Set(form.value.paths)
  paths.forEach((p) => {
    if (p && p.trim()) merged.add(p.trim())
  })
  form.value.paths = Array.from(merged)
}

const removePath = (path) => {
  form.value.paths = form.value.paths.filter((p) => p !== path)
}

const addManualPaths = () => {
  const parts = manualPath.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
  addPaths(parts)
  manualPath.value = ''
}

const onPickerConfirm = (paths) => {
  const list = Array.isArray(paths) ? paths : [paths]
  addPaths(list.filter(Boolean))
  showPathPicker.value = false
}

const pickNative = async (mode) => {
  error.value = ''
  if (nativeSupported.value === false) {
    showPathPicker.value = true
    return
  }
  nativeLoading.value = true
  try {
    const res = await api.openNativeDialog({
      mode,
      exts: LOG_EXTS,
      title: mode === 'folder' ? '选择日志文件夹' : '选择日志文件'
    })
    const data = res?.data || {}
    if (data.canceled) return
    addPaths(data.paths || [])
  } catch (e) {
    // 501 = 原生对话框不可用，自动回退，不打扰用户
    if (e?.response?.status === 501 || String(e?.message || '').includes('原生对话框不可用')) {
      nativeSupported.value = false
      showPathPicker.value = true
    } else {
      error.value = e?.message || '打开原生对话框失败，可改用路径选择器'
    }
  } finally {
    nativeLoading.value = false
  }
}

const buildPayload = () => {
  const payload = { conflict_mode: form.value.conflictMode }
  if (form.value.sourceType === 'text') {
    payload.content = form.value.content
    payload.file_name = form.value.fileName || 'manual_input.txt'
  } else {
    payload.paths = form.value.paths
    payload.exts = LOG_EXTS
    payload.recursive = true
  }
  return payload
}

const handlePreview = async () => {
  if (!hasSource.value) {
    ElMessage.warning('请先选择日志文件或粘贴日志文本')
    return
  }
  error.value = ''
  previewing.value = true
  try {
    const res = await api.previewImport(currentTaskId.value, buildPayload())
    preview.value = res?.data || null
    step.value = 2
  } catch (e) {
    error.value = e?.message || '导入预览失败'
  } finally {
    previewing.value = false
  }
}

const reloadPreview = async () => {
  if (!hasSource.value) return
  await handlePreview()
}

const handleStartImport = async () => {
  starting.value = true
  error.value = ''
  try {
    let res
    if (form.value.sourceType === 'text') {
      res = await api.importTaskLogsText(currentTaskId.value, {
        content: form.value.content,
        fileName: form.value.fileName || 'manual_input.txt',
        conflictMode: form.value.conflictMode
      })
    } else {
      res = await api.importTaskLogsByPaths(currentTaskId.value, {
        paths: form.value.paths,
        exts: LOG_EXTS,
        recursive: true,
        conflictMode: form.value.conflictMode
      })
    }
    const jobId = res?.data?.job_id
    if (jobId) {
      emit('job-started', { jobId, taskId: currentTaskId.value })
      close()
    } else {
      ElMessage.success('日志已导入')
      emit('job-started', { jobId: '', taskId: currentTaskId.value })
      close()
    }
  } catch (e) {
    error.value = e?.message || '导入失败'
  } finally {
    starting.value = false
  }
}

defineExpose({
  open: () => { dialogVisible.value = true },
  currentTaskId
})

onMounted(() => {
  // 能力探测延后到首次打开，避免应用启动即请求
})
</script>

<style scoped>
.wizard-steps {
  margin-bottom: 20px;
}

.wizard-panel {
  min-height: 300px;
}

.source-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 16px;
}

.source-tile {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  padding: 14px;
  text-align: left;
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  cursor: pointer;
  transition: all 0.15s ease;
}

.source-tile:hover,
.source-tile.active {
  border-color: var(--brand-400);
  background: var(--brand-50, #eff6ff);
}

.source-tile span {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  line-height: 1.5;
}

.path-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.path-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  max-height: 180px;
  overflow-y: auto;
  padding: 8px;
  border: 1px dashed var(--border-base);
  border-radius: var(--radius-base);
}

.path-tag {
  max-width: 100%;
}

.advanced-source {
  margin-top: 12px;
}

.mt-8 { margin-top: 8px; }
.mt-12 { margin-top: 12px; }

.preview-line {
  margin-top: 10px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.conflict-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 6px;
}

.preview-columns {
  display: grid;
  grid-template-columns: 1.3fr 1fr;
  gap: 12px;
  margin-top: 14px;
}

.preview-card :deep(.el-card__header) {
  padding: 8px 12px;
  font-weight: 600;
}

.device-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 240px;
  overflow-y: auto;
}

.device-row {
  display: flex;
  justify-content: space-between;
  font-size: var(--fs-sm);
  padding: 4px 0;
  border-bottom: 1px dashed var(--border-base);
}

.device-name {
  font-weight: 600;
  color: var(--text-primary);
}

.device-count {
  color: var(--text-secondary);
}

.preview-note {
  margin-top: 12px;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  line-height: 1.7;
}
</style>
