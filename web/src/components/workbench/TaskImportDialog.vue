<template>
  <div>
    <!-- 导入 / 补充导入日志弹窗（样式基准：导入 HDX 文档弹窗） -->
    <el-dialog
      v-model="visible"
      :title="`导入日志 - ${taskName || ''}`"
      width="1000px"
      top="6vh"
      destroy-on-close
    >
      <el-tabs v-model="importTab" type="border-card">
        <!-- 标签页 1: 从本机导入（文件与目录合并为一个入口） -->
        <el-tab-pane label="从本机导入" name="paths">
          <div class="imp-dialog-content">
            <!-- 来源行：手动输入路径 + 浏览选择 -->
            <div class="imp-source-row">
              <el-input
                v-model="manualPathInput"
                placeholder="输入或粘贴日志文件 / 目录的绝对路径，如 D:\Logs\switch01.log 或 D:\Logs"
                clearable
                @keyup.enter="addManualPath"
              >
                <template #prepend>
                  <el-icon><Folder /></el-icon>
                  <span style="margin-left: 4px;">路径</span>
                </template>
              </el-input>
              <el-button type="primary" plain icon="FolderOpened" @click="openPicker">
                浏览文件 / 目录
              </el-button>
              <el-button
                type="primary"
                :disabled="!manualPathInput.trim()"
                :loading="statting"
                @click="addManualPath"
              >
                <el-icon style="margin-right: 4px"><Plus /></el-icon>
                添加到列表
              </el-button>
            </div>

            <!-- 规则提示条 -->
            <div class="imp-tip-bar">
              <el-icon color="#0284c7"><InfoFilled /></el-icon>
              <span>
                文件与目录均由服务端进程直接读取，不经过浏览器上传；目录会按扩展名递归收集其中所有日志文件，当前支持
                <strong>.log / .txt / .syslog / .gz</strong>。
              </span>
            </div>

            <!-- 已选清单：统计摘要 + 明细 -->
            <div class="imp-result-container">
              <div v-if="!selectedPaths.length" class="imp-state-box">
                <el-icon size="48" color="#94a3b8"><FolderOpened /></el-icon>
                <div class="imp-state-title">请选择日志文件或目录</div>
                <div class="imp-state-desc">
                  点击上方「浏览文件 / 目录」在服务端文件系统中选择，或直接粘贴绝对路径后点击「添加到列表」。
                  可同时选择多个文件与多个目录，一次完成导入。
                </div>
              </div>

              <div v-else class="imp-selected-box is-embedded">
                <div class="imp-selected-header">
                  <span>
                    已选 <strong>{{ selectedPaths.length }}</strong> 个路径
                    <span class="imp-count-badge is-file">{{ selectedFileCount }} 个文件</span>
                    <span class="imp-count-badge is-dir">{{ selectedDirCount }} 个目录</span>
                  </span>
                  <el-button link type="danger" size="small" @click="clearPaths">清空</el-button>
                </div>
                <div class="imp-selected-list">
                  <div v-for="(p, idx) in selectedPaths" :key="p.path" class="imp-selected-item">
                    <span class="imp-selected-type" :class="p.is_dir ? 'is-dir' : 'is-file'">
                      {{ p.is_dir ? '目录' : '文件' }}
                    </span>
                    <el-icon :color="p.is_dir ? '#d97706' : '#0284c7'">
                      <Folder v-if="p.is_dir" /><Document v-else />
                    </el-icon>
                    <span class="imp-selected-name" :title="p.path">{{ pathBaseName(p.path) }}</span>
                    <span class="imp-selected-path" :title="p.path">{{ p.path }}</span>
                    <span class="imp-selected-size">{{ p.is_dir ? '递归收集' : formatSize(p.size) }}</span>
                    <el-icon class="imp-selected-remove" @click="removePath(idx)"><Close /></el-icon>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </el-tab-pane>

        <!-- 标签页 2: 粘贴 Syslog 文本 -->
        <el-tab-pane label="输入日志文本" name="text">
          <el-form label-position="top">
            <el-form-item label="自定义日志文件名 (可选)">
              <el-input v-model="manualFileName" placeholder="例如: manual_syslog.txt" />
            </el-form-item>
            <el-form-item label="Syslog 报文内容">
              <el-input
                v-model="manualLogText"
                type="textarea"
                :rows="12"
                placeholder="粘贴 Syslog 原始日志报文文本..."
              />
              <div class="text-meta-row">
                <span>{{ manualLogText.length }} 字符</span>
                <span v-if="!manualLogText.trim()">内容为空时无法导入</span>
              </div>
            </el-form-item>
          </el-form>
        </el-tab-pane>
      </el-tabs>

      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="!canStartImport"
          @click="handleCheckAndStartImport"
        >
          开始导入并分析 ({{ pendingCount }})
        </el-button>
      </template>
    </el-dialog>

    <!-- 服务端路径选择器（文件与目录均可选） -->
    <ServerPathPicker
      v-model:visible="showPathPicker"
      mode="both"
      :exts="logExts"
      favorite-key="task-logs"
      title="选择日志文件或目录"
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
import { FolderOpened, Folder, Document, Close, InfoFilled, Plus } from '@element-plus/icons-vue'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import api from '@/api'
import { useTaskStore } from '@/stores/task'
import { formatSize as sharedFormatSize } from '@/utils/format'

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
// paths: 从本机导入（文件 + 目录合并入口）；text: 手动输入日志文本
const importTab = ref('paths')
const manualFileName = ref('')
const manualLogText = ref('')
const manualPathInput = ref('')
// 已选路径清单，元素为 { path, is_dir, size }
const selectedPaths = ref([])
const showPathPicker = ref(false)
const logExts = ['.log', '.txt', '.syslog', '.gz']
const submitting = ref(false)
const statting = ref(false)

const showConflictDialog = ref(false)
const conflictingFileNames = ref([])

const taskStore = useTaskStore()
const taskFiles = computed(() => taskStore.taskFiles)

const open = (initialTab = 'paths') => {
  selectedPaths.value = []
  manualLogText.value = ''
  manualFileName.value = ''
  manualPathInput.value = ''
  importTab.value = initialTab === 'text' ? 'text' : 'paths'
  visible.value = true
}

const openPicker = () => {
  showPathPicker.value = true
}

// 兼容旧调用方传入的 'dir' / 'files' 等模式值，统一收敛为 files+dirs 合并入口
const openWithPicker = () => {
  open('paths')
  openPicker()
}

// fsStat 单次上限 500 条，超出时分批校验，避免触发后端限制
const STAT_CHUNK = 400

const statPaths = async paths => {
  const result = []
  for (let i = 0; i < paths.length; i += STAT_CHUNK) {
    const chunk = paths.slice(i, i + STAT_CHUNK)
    const res = await api.fsStat(chunk)
    if (res.code === 0) {
      result.push(...(res.data?.entries || []))
    }
  }
  return result
}

const normPath = p => (p ? String(p).replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase() : '')

// 将路径并入已选清单：自动识别文件/目录与大小，去重
const mergePaths = async paths => {
  const incoming = (paths || []).filter(p => p && !selectedPaths.value.some(x => normPath(x.path) === normPath(p)))
  if (!incoming.length) return
  statting.value = true
  try {
    const entries = await statPaths(incoming)
    const byPath = new Map(entries.map((e, idx) => [normPath(e.path), e]))
    const missing = []
    const added = []
    for (let i = 0; i < incoming.length; i++) {
      const p = incoming[i]
      const e = byPath.get(normPath(p)) || entries[i]
      if (e && e.exists) {
        added.push({ path: e.path, is_dir: !!e.is_dir, size: e.size || 0 })
      } else {
        missing.push(p)
      }
    }
    if (added.length) {
      selectedPaths.value = selectedPaths.value.concat(added)
    }
    if (missing.length) {
      ElMessage.warning(`有 ${missing.length} 个路径不存在或不可访问：${missing[0]}${missing.length > 1 ? ' 等' : ''}`)
    }
  } catch (e) {
    console.error('Stat paths failed:', e)
    ElMessage.error('路径校验失败，请检查路径是否正确')
  } finally {
    statting.value = false
  }
}

const onPickerConfirm = paths => {
  mergePaths(Array.isArray(paths) ? paths : [paths])
}

const addManualPath = async () => {
  const p = (manualPathInput.value || '').trim()
  if (!p) {
    ElMessage.warning('请输入文件或目录的绝对路径')
    return
  }
  if (selectedPaths.value.some(x => normPath(x.path) === normPath(p))) {
    ElMessage.info('该路径已在列表中')
    return
  }
  await mergePaths([p])
  if (selectedPaths.value.some(x => normPath(x.path) === normPath(p))) {
    manualPathInput.value = ''
  }
}

const removePath = index => {
  selectedPaths.value.splice(index, 1)
}

const clearPaths = () => {
  selectedPaths.value = []
}

const pathBaseName = p => {
  if (!p) return ''
  const segs = String(p).replace(/\\/g, '/').split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

const isConflictFile = fileName => {
  return taskFiles.value.some(tf => tf.file_name === fileName)
}

const selectedFileCount = computed(() => selectedPaths.value.filter(p => !p.is_dir).length)
const selectedDirCount = computed(() => selectedPaths.value.filter(p => p.is_dir).length)

const pendingCount = computed(() =>
  importTab.value === 'text' ? (manualLogText.value.trim() ? 1 : 0) : selectedPaths.value.length
)

const canStartImport = computed(() => {
  if (submitting.value || statting.value) return false
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
    ElMessage.warning('请选择至少一个日志文件或目录')
    return
  }

  // 目录由服务端递归收集，冲突在收集后按 conflictMode 处理；
  // 这里只对直接选择的文件做同名预检，提前告知用户。
  const conflicts = []
  for (const p of selectedPaths.value) {
    if (!p.is_dir && isConflictFile(pathBaseName(p.path))) {
      conflicts.push(pathBaseName(p.path))
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('rename')
  }
}

const executeImportWithConflict = async conflictMode => {
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
        paths: selectedPaths.value.map(p => p.path),
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
      manualPathInput.value = ''

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

const formatSize = bytes => sharedFormatSize(bytes, '')

defineExpose({
  visible: isOpen,
  open,
  openWithPicker
})
</script>

<style scoped>
.text-meta-row {
  display: flex;
  justify-content: space-between;
  font-size: var(--fs-sm);
  color: var(--gray-400);
  margin-top: 4px;
}

.conflict-dialog-body {
  padding: 4px 0;
}

.conflict-file-list {
  max-height: 150px;
  overflow-y: auto;
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  border-radius: var(--radius-md);
  padding: 6px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.conflict-item {
  font-size: var(--fs-md);
  color: var(--danger-text);
}
</style>
