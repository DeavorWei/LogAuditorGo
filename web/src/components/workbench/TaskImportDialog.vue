<template>
  <div>
    <!-- 导入 / 补充导入日志弹窗 -->
    <el-dialog
      v-model="visible"
      :title="`导入日志 - ${taskName || ''}`"
      width="620px"
      destroy-on-close
    >
      <el-tabs v-model="importTab" type="border-card">
        <!-- 标签页 1: 从本机目录导入 -->
        <el-tab-pane label="从本机目录导入" name="dir">
          <div class="path-import-pane">
            <el-icon size="40" color="#16a34a"><FolderAdd /></el-icon>
            <div class="pane-title">选择存放日志文件的目录</div>
            <p class="pane-desc">
              目录由服务端进程直接读取，不经过浏览器上传。可一次选择多个目录，
              并按扩展名递归收集其中所有日志文件。
            </p>
            <el-button type="success" size="small" @click="openPicker('dir')">选择日志目录</el-button>
          </div>
        </el-tab-pane>

        <!-- 标签页 2: 从本机文件导入 -->
        <el-tab-pane label="从本机文件导入" name="files">
          <div class="path-import-pane">
            <el-icon size="40" color="#0284c7"><Files /></el-icon>
            <div class="pane-title">选择一个或多个日志文件</div>
            <p class="pane-desc">
              直接选择服务端本机上的日志文件，支持 .log / .txt / .syslog 等常见格式。
            </p>
            <el-button type="primary" size="small" @click="openPicker('file')">选择日志文件</el-button>
          </div>
        </el-tab-pane>

        <!-- 标签页 3: 粘贴 Syslog 文本 -->
        <el-tab-pane label="输入日志文本" name="text">
          <el-form label-position="top">
            <el-form-item label="自定义日志文件名 (可选)">
              <el-input v-model="manualFileName" placeholder="例如: manual_syslog.txt" />
            </el-form-item>
            <el-form-item label="Syslog 报文内容">
              <el-input
                v-model="manualLogText"
                type="textarea"
                :rows="7"
                placeholder="粘贴 Syslog 原始日志报文文本..."
              />
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>

      <!-- 已选路径清单 -->
      <div v-if="selectedPaths.length > 0 && importTab !== 'text'" class="pending-paths-box">
        <div class="pending-paths-header">
          <span>已选择 <strong>{{ selectedPaths.length }}</strong> 个路径</span>
          <el-button type="danger" link size="small" @click="selectedPaths = []">清空</el-button>
        </div>
        <div class="pending-paths-list">
          <div v-for="(p, idx) in selectedPaths" :key="p" class="pending-path-item">
            <span class="path-value" :title="p">📁 {{ p }}</span>
            <el-icon class="del-btn" @click="removePath(idx)"><Close /></el-icon>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="!canStartImport"
          @click="handleCheckAndStartImport"
        >
          开始导入并分析
        </el-button>
      </template>
    </el-dialog>

    <!-- 服务端路径选择器 -->
    <ServerPathPicker
      v-model:visible="showPathPicker"
      :mode="pickerMode"
      :exts="logExts"
      @confirm="onPickerConfirm"
    />

    <!-- 同名文件冲突处理弹窗 -->
    <el-dialog v-model="showConflictDialog" title="⚠️ 检测到同名日志文件" width="520px">
      <div class="conflict-dialog-body">
        <p style="margin-bottom: 12px; line-height: 1.6; color: #334155;">
          当前任务中已存在以下 <strong>{{ conflictingFileNames.length }}</strong> 个同名日志文件：
        </p>
        <div class="conflict-file-list">
          <div v-for="name in conflictingFileNames" :key="name" class="conflict-item">
            ⚠️ <strong>{{ name }}</strong>
          </div>
        </div>
        <p style="margin-top: 14px; font-size: 13px; color: #64748b;">
          请选择冲突文件的处理方式：
        </p>
      </div>
      <template #footer>
        <el-button @click="showConflictDialog = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          @click="executeImportWithConflict('rename')"
        >
          自动重命名共存 (推荐)
        </el-button>
        <el-button
          type="warning"
          :loading="submitting"
          @click="executeImportWithConflict('skip')"
        >
          跳过同名文件
        </el-button>
        <el-button
          type="danger"
          :loading="submitting"
          @click="executeImportWithConflict('overwrite')"
        >
          覆盖已有文件
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { ElMessage } from 'element-plus'
import { FolderAdd, Files, Close } from '@element-plus/icons-vue'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import api from '@/api'
import { useTaskStore } from '@/stores/task'

const props = defineProps({
  taskId: {
    type: String,
    default: ''
  },
  taskName: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['job-started', 'import-settled'])

const visible = ref(false)
const importTab = ref('dir')
const manualFileName = ref('')
const manualLogText = ref('')
const selectedPaths = ref([])
const showPathPicker = ref(false)
const pickerMode = ref('dir')
const logExts = ['.log', '.txt', '.syslog', '.gz']
const submitting = ref(false)

const showConflictDialog = ref(false)
const conflictingFileNames = ref([])

const taskStore = useTaskStore()
const taskFiles = computed(() => taskStore.taskFiles)

const open = (initialTab = 'dir') => {
  selectedPaths.value = []
  manualLogText.value = ''
  manualFileName.value = ''
  importTab.value = initialTab
  visible.value = true
}

const openPicker = (mode) => {
  pickerMode.value = mode
  importTab.value = mode === 'dir' ? 'dir' : 'files'
  showPathPicker.value = true
}

const openWithPicker = (mode) => {
  open(mode === 'dir' ? 'dir' : 'files')
  openPicker(mode)
}

const onPickerConfirm = (paths) => {
  if (Array.isArray(paths)) {
    const set = new Set([...selectedPaths.value, ...paths])
    selectedPaths.value = Array.from(set)
  }
}

const removePath = (index) => {
  selectedPaths.value.splice(index, 1)
}

const pathBaseName = (p) => {
  if (!p) return ''
  const segs = String(p).replace(/\\/g, '/').split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

const isConflictFile = (fileName) => {
  return taskFiles.value.some((tf) => tf.file_name === fileName)
}

const canStartImport = computed(() => {
  if (importTab.value === 'text') return manualLogText.value.trim() !== ''
  return selectedPaths.value.length > 0
})

const handleCheckAndStartImport = () => {
  if (importTab.value === 'text') {
    if (!manualLogText.value.trim()) {
      ElMessage.warning('请输入 Syslog 报文文本')
      return
    }
    const fname = manualFileName.value.trim() || 'manual_input.txt'
    if (isConflictFile(fname)) {
      conflictingFileNames.value = [fname]
      showConflictDialog.value = true
      return
    }
    executeImportWithConflict('overwrite')
    return
  }

  if (selectedPaths.value.length === 0) {
    ElMessage.warning('请选择至少一个日志目录或日志文件')
    return
  }

  const conflicts = []
  if (importTab.value === 'files') {
    for (const p of selectedPaths.value) {
      const name = pathBaseName(p)
      if (isConflictFile(name)) {
        conflicts.push(name)
      }
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('rename')
  }
}

const executeImportWithConflict = async (conflictMode) => {
  if (!props.taskId) return
  submitting.value = true
  showConflictDialog.value = false

  try {
    let res
    if (importTab.value === 'text') {
      res = await api.importTaskLogsText(props.taskId, {
        content: manualLogText.value,
        fileName: manualFileName.value.trim() || 'manual_input.txt',
        conflictMode
      })
    } else {
      res = await api.importTaskLogsByPaths(props.taskId, {
        paths: selectedPaths.value,
        exts: logExts,
        recursive: true,
        conflictMode
      })
    }

    if (res.code === 0) {
      visible.value = false
      selectedPaths.value = []
      manualLogText.value = ''
      manualFileName.value = ''

      if (res.data?.job_id) {
        emit('job-started', res.data.job_id)
      } else {
        emit('import-settled', res.data)
      }
    } else {
      ElMessage.error(res.message || '导入任务失败，请检查文件格式或重试')
    }
  } catch (e) {
    console.error('Import task logs failed:', e)
    ElMessage.error('导入请求异常，请检查网络或后端服务')
  } finally {
    submitting.value = false
  }
}

const isOpen = computed(() => visible.value || showConflictDialog.value || showPathPicker.value)

defineExpose({
  visible: isOpen,
  open,
  openWithPicker
})
</script>

<style scoped>
.path-import-pane { padding: 24px 16px; text-align: center; background: #f8fafc; border-radius: 8px; }
.pane-title { font-size: 15px; font-weight: 600; color: #1e293b; margin: 8px 0 4px; }
.pane-desc { font-size: 13px; color: #64748b; line-height: 1.5; max-width: 460px; margin: 0 auto 14px; }
.pending-paths-box { margin-top: 12px; border: 1px solid #e2e8f0; border-radius: 6px; padding: 8px 12px; background: #fff; }
.pending-paths-header { display: flex; justify-content: space-between; align-items: center; font-size: 13px; color: #475569; margin-bottom: 6px; }
.pending-paths-list { max-height: 130px; overflow-y: auto; display: flex; flex-direction: column; gap: 6px; }
.pending-path-item { display: flex; justify-content: space-between; align-items: center; background: #f1f5f9; padding: 5px 8px; border-radius: 4px; font-size: 12px; }
.path-value { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 480px; color: #0f172a; }
.del-btn { cursor: pointer; color: #94a3b8; transition: color 0.2s; }
.del-btn:hover { color: #ef4444; }
.conflict-dialog-body { padding: 4px 0; }
.conflict-file-list { max-height: 150px; overflow-y: auto; background: #fef2f2; border: 1px solid #fecaca; border-radius: 6px; padding: 6px 10px; display: flex; flex-direction: column; gap: 4px; }
.conflict-item { font-size: 13px; color: #991b1b; }
</style>
