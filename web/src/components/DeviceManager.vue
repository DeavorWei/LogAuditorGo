<template>
  <div class="device-manager-container">
    <!-- 顶部操作栏 -->
    <div class="action-bar">
      <div class="bar-left">
        <el-tag type="info" size="large" effect="plain">
          共 {{ deviceList.length }} 台设备
        </el-tag>
        <span class="tip-text">
          支持为每台网络设备独立配置并导入对应的 Syslog 日志，或一键按 Hostname 智能归集
        </span>
      </div>
      <div class="bar-right">
        <el-button-group>
          <el-button icon="Refresh" :loading="loading" @click="fetchDevices">刷新设备</el-button>
          <el-button type="success" icon="MagicStick" :loading="autoAssigning" @click="handleAutoAssign">
            按 Hostname 自动识别
          </el-button>
          <el-button type="primary" icon="Plus" @click="openCreateDialog">
            新建设备
          </el-button>
        </el-button-group>
      </div>
    </div>

    <!-- 设备列表表格 -->
    <el-card shadow="never" class="table-card">
      <el-table :data="deviceList" v-loading="loading" style="width: 100%;" border>
        <el-table-column label="设备标识" min-width="160">
          <template #default="{ row }">
            <div class="device-name-cell">
              <span class="color-dot" :style="{ backgroundColor: row.color || DEFAULT_DEVICE_COLOR }"></span>
              <strong>{{ row.device_name }}</strong>
            </div>
          </template>
        </el-table-column>

        <el-table-column prop="device_type" label="设备类型" width="150">
          <template #default="{ row }">
            <!-- WEB-16: 展示统一口径的中文名，而不是直接把 value 透给用户 -->
            <el-tag size="small" effect="light">{{ deviceTypeLabel(row.device_type) }}</el-tag>
          </template>
        </el-table-column>

        <el-table-column prop="hostname" label="匹配 Hostname" width="150">
          <template #default="{ row }">
            <span class="code-text">{{ row.hostname || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="management_ip" label="管理 IP" width="140">
          <template #default="{ row }">
            <span style="font-family: monospace;">{{ row.management_ip || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="log_count" label="已导入日志" width="110" align="center">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.log_count }} 行</el-tag>
          </template>
        </el-table-column>

        <el-table-column prop="matched_count" label="知识匹配数" width="110" align="center">
          <template #default="{ row }">
            <span class="matched-count-val">{{ row.matched_count }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="description" label="描述" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="desc-val">{{ row.description || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="220" align="center" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" size="small" link icon="Upload" @click="openImportDialog(row)">
              导入日志
            </el-button>
            <el-button type="primary" size="small" link icon="Edit" @click="openEditDialog(row)">
              编辑
            </el-button>
            <el-popconfirm title="确定删除该设备吗？关联日志将变为未指定设备" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button type="danger" size="small" link icon="Delete">
                  删除
                </el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>

      <!-- 空状态指引 -->
      <div v-if="!loading && deviceList.length === 0" class="empty-guide">
        <el-icon size="42" color="var(--gray-400)"><Monitor /></el-icon>
        <p>当前任务尚未创建设备</p>
        <div class="empty-actions">
          <el-button type="primary" icon="Plus" @click="openCreateDialog">立即创建设备</el-button>
          <el-button type="success" icon="MagicStick" @click="handleAutoAssign">从已上传日志中识别 Hostname</el-button>
        </div>
      </div>
    </el-card>

    <!-- 新建/编辑设备对话框 -->
    <el-dialog
      v-model="showDeviceDialog"
      :title="isEditing ? '编辑设备信息' : '新建设备'"
      width="540px"
      destroy-on-close
    >
      <el-form ref="deviceFormRef" :model="deviceForm" :rules="deviceFormRules" label-width="110px">
        <el-form-item label="设备名称" prop="device_name" required>
          <el-input v-model="deviceForm.device_name" placeholder="例如: Router-Core-01" />
        </el-form-item>

        <el-form-item label="设备类型" prop="device_type" required>
          <!-- WEB-16: 选项统一取自常量（设备形态分类，与任务的匹配设备类型是两套口径） -->
          <el-select v-model="deviceForm.device_type" style="width: 100%;">
            <el-option
              v-for="opt in DEVICE_FORM_TYPE_OPTIONS"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
        </el-form-item>

        <!--
          UI-12: 补齐表单校验。
          Hostname 用于日志自动归属，含空格或通配写法会导致归属命中错乱；
          管理 IP 原先完全不校验，填成 "192.168.1" 或 "abc" 也能存进库，
          导出报告与拓扑图上会直接显示成脏数据。
        -->
        <el-form-item label="匹配 Hostname" prop="hostname">
          <el-input v-model="deviceForm.hostname" placeholder="日志报文中的主机名，如 Router-Core-01" />
        </el-form-item>

        <el-form-item label="管理 IP" prop="management_ip">
          <el-input v-model="deviceForm.management_ip" placeholder="例如: 192.168.1.1" />
        </el-form-item>

        <el-form-item label="标识颜色">
          <div style="display: flex; align-items: center; gap: 12px;">
            <el-color-picker v-model="deviceForm.color" show-alpha :predefine="colorPresets" />
            <span style="font-family: monospace; font-size: 13px;">{{ deviceForm.color }}</span>
          </div>
        </el-form-item>

        <el-form-item label="设备描述">
          <el-input
            v-model="deviceForm.description"
            type="textarea"
            :rows="2"
            placeholder="填写设备部署位置、角色说明等"
          />
        </el-form-item>
      </el-form>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showDeviceDialog = false">取消</el-button>
          <el-button type="primary" :loading="submitting" @click="saveDevice">保存</el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 向设备导入日志对话框（样式基准：导入 HDX 文档弹窗） -->
    <el-dialog
      v-model="showImportDialog"
      :title="`向设备「${targetDevice?.device_name}」导入日志`"
      width="1000px"
      top="6vh"
      destroy-on-close
    >
      <el-tabs v-model="importTab" type="border-card">
        <!-- 从本机导入：文件与目录合并为一个入口 -->
        <el-tab-pane label="从本机导入" name="paths">
          <div class="imp-dialog-content">
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

            <div class="imp-tip-bar">
              <el-icon color="var(--brand-600)"><InfoFilled /></el-icon>
              <span>
                文件与目录均由服务端进程直接读取，不经过浏览器上传；目录会按扩展名递归收集其中所有日志文件，当前支持
                <strong>.log / .txt / .syslog</strong>。导入后日志将直接归属给「{{ targetDevice?.device_name }}」。
              </span>
            </div>

            <div class="imp-result-container">
              <div v-if="!selectedPaths.length" class="imp-state-box">
                <el-icon size="48" color="var(--gray-400)"><FolderOpened /></el-icon>
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
                    <el-icon :color="p.is_dir ? 'var(--warning-strong)' : 'var(--brand-600)'">
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

            <!--
              UI-12: 冲突策略选择器。
              原先两处导入路径都把 conflictMode 写死为 'overwrite'，
              重复导入会静默覆盖同名文件已解析的结果（含人工确认过的归属），
              且与文档导入页提供的 skip/overwrite/rename 选择体验不一致。
            -->
            <div class="conflict-strategy-box">
              <span class="conflict-label">同名文件冲突策略</span>
              <el-radio-group v-model="importConflictMode" size="small">
                <el-radio-button value="rename">重命名保留</el-radio-button>
                <el-radio-button value="skip">跳过同名</el-radio-button>
                <el-radio-button value="overwrite">覆盖重解析</el-radio-button>
              </el-radio-group>
              <span class="conflict-hint">{{ conflictModeHint }}</span>
            </div>
          </div>
        </el-tab-pane>

        <!-- 直接粘贴文本 -->
        <el-tab-pane label="输入日志文本" name="text">
          <el-input
            v-model="textLogContent"
            type="textarea"
            :rows="14"
            placeholder="直接在此处粘贴 Syslog 原始日志报文文本..."
          />
          <div class="text-meta-row">
            <span>{{ textLogContent.length }} 字符</span>
            <span v-if="!textLogContent.trim()">内容为空时无法导入</span>
          </div>
        </el-tab-pane>
      </el-tabs>

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showImportDialog = false">取消</el-button>
          <el-button type="primary" :loading="importing" :disabled="!canImport" @click="submitImportLogs">
            开始导入分析 ({{ pendingCount }})
          </el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 服务端本地路径选择器（设备日志导入专用，文件与目录均可选） -->
    <ServerPathPicker
      v-model:visible="showPathPicker"
      mode="both"
      :exts="logExts"
      :multiple="true"
      favorite-key="device-logs"
      title="选择日志文件或目录"
      @confirm="onPickerConfirm"
    />
  </div>
</template>

<script setup>
// UI-16: defineProps / defineEmits 是编译器宏，无需从 vue 导入
import { ref, computed, nextTick, onMounted, watch } from 'vue'
import { DEVICE_FORM_TYPE_OPTIONS, DEFAULT_DEVICE_FORM_TYPE, deviceTypeLabel } from '@/constants/deviceTypes'
import { ElMessage } from 'element-plus'
import { Monitor, Folder, FolderOpened, Document, Close, Plus, InfoFilled } from '@element-plus/icons-vue'
import api from '@/api'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import { formatSize as sharedFormatSize, pathBaseName } from '@/utils/format'
import { DEVICE_COLOR_PRESETS, DEFAULT_DEVICE_COLOR } from '@/constants/tagColors'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['device-updated', 'open-progress'])

const loading = ref(false)
const autoAssigning = ref(false)
const submitting = ref(false)
const importing = ref(false)
const deviceList = ref([])

const showDeviceDialog = ref(false)
const isEditing = ref(false)
const editingDeviceId = ref(null)

const colorPresets = DEVICE_COLOR_PRESETS

const deviceFormRef = ref(null)
const deviceForm = ref({
  device_name: '',
  device_type: DEFAULT_DEVICE_FORM_TYPE,
  hostname: '',
  management_ip: '',
  color: DEFAULT_DEVICE_COLOR,
  description: ''
})

/**
 * UI-12: 设备表单校验规则。
 *
 * Hostname 是日志自动归属的唯一依据，允许字母数字与 `.-_`（华为设备命名常规字符），
 * 禁止空格与通配符，避免归属匹配时命中错乱。
 * 管理 IP 支持标准 IPv4，留空表示不填写（该字段非必填）。
 */
const IPV4_PATTERN = /^((25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)\.){3}(25[0-5]|2[0-4]\d|1\d{2}|[1-9]?\d)$/
const HOSTNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/

const deviceFormRules = {
  device_name: [
    { required: true, message: '请填写设备名称', trigger: 'blur' },
    { min: 1, max: 64, message: '设备名称长度需在 1~64 个字符之间', trigger: 'blur' }
  ],
  device_type: [
    { required: true, message: '请选择设备类型', trigger: 'change' }
  ],
  hostname: [
    {
      validator: (_rule, value, callback) => {
        const v = (value || '').trim()
        if (!v) return callback()
        if (!HOSTNAME_PATTERN.test(v)) {
          return callback(new Error('仅支持字母、数字、点、下划线与短横线，且需以字母或数字开头'))
        }
        callback()
      },
      trigger: 'blur'
    }
  ],
  management_ip: [
    {
      validator: (_rule, value, callback) => {
        const v = (value || '').trim()
        if (!v) return callback()
        if (!IPV4_PATTERN.test(v)) {
          return callback(new Error('请填写合法的 IPv4 地址，例如 192.168.1.1'))
        }
        callback()
      },
      trigger: 'blur'
    }
  ]
}

const showImportDialog = ref(false)
const targetDevice = ref(null)
// paths: 从本机导入（文件 + 目录合并入口）；text: 手动输入日志文本
const importTab = ref('paths')
// 已选路径清单，元素为 { path, is_dir, size }
const selectedPaths = ref([])
const manualPathInput = ref('')
const statting = ref(false)
const showPathPicker = ref(false)
const logExts = ['.log', '.txt', '.syslog']
const textLogContent = ref('')

// UI-12: 同名文件冲突策略，默认"重命名保留"（最安全，不会覆盖已有解析结果）
const importConflictMode = ref('rename')

const CONFLICT_MODE_HINTS = {
  rename: '重名文件自动追加后缀保留，已解析结果不受影响（推荐）',
  skip: '遇到重名文件直接跳过，仅导入新文件',
  overwrite: '覆盖同名文件的既有解析结果，可能丢失已确认的归属与标注'
}
const conflictModeHint = computed(() => CONFLICT_MODE_HINTS[importConflictMode.value] || '')

const fetchDevices = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const res = await api.getDevices(props.taskId)
    if (res.code === 0) {
      deviceList.value = res.data || []
    }
  } catch (e) {
    console.error('Fetch devices failed:', e)
  } finally {
    loading.value = false
  }
}

const handleAutoAssign = async () => {
  if (!props.taskId) return
  autoAssigning.value = true
  try {
    const res = await api.autoAssignDevices(props.taskId)
    if (res.code === 0) {
      ElMessage.success(res.message || '已成功从日志中自动识别并创建设备')
      await fetchDevices()
      emit('device-updated', deviceList.value)
    }
  } catch (e) {
    console.error('Auto assign failed:', e)
  } finally {
    autoAssigning.value = false
  }
}

const openCreateDialog = () => {
  isEditing.value = false
  editingDeviceId.value = null
  const defaultColor = colorPresets[deviceList.value.length % colorPresets.length]
  deviceForm.value = {
    device_name: `Device-${deviceList.value.length + 1}`,
    device_type: DEFAULT_DEVICE_FORM_TYPE,
    hostname: '',
    management_ip: '',
    color: defaultColor,
    description: ''
  }
  showDeviceDialog.value = true
  // 打开下次对话框前清掉上一轮的校验高亮
  nextTick(() => deviceFormRef.value?.clearValidate?.())
}

const openEditDialog = (row) => {
  isEditing.value = true
  editingDeviceId.value = row.id
  deviceForm.value = {
    device_name: row.device_name,
    device_type: row.device_type,
    hostname: row.hostname,
    management_ip: row.management_ip,
    color: row.color || DEFAULT_DEVICE_COLOR,
    description: row.description || ''
  }
  showDeviceDialog.value = true
  nextTick(() => deviceFormRef.value?.clearValidate?.())
}

const saveDevice = async () => {
  // UI-12: 提交前走完整表单校验（原先只校验 device_name 非空）
  if (deviceFormRef.value) {
    try {
      await deviceFormRef.value.validate()
    } catch (e) {
      return
    }
  }
  submitting.value = true
  try {
    if (isEditing.value && editingDeviceId.value) {
      const res = await api.updateDevice(props.taskId, editingDeviceId.value, deviceForm.value)
      if (res.code === 0) {
        ElMessage.success('设备更新成功')
        showDeviceDialog.value = false
        await fetchDevices()
        emit('device-updated', deviceList.value)
      }
    } else {
      const res = await api.createDevice(props.taskId, deviceForm.value)
      if (res.code === 0) {
        ElMessage.success('设备创建成功')
        showDeviceDialog.value = false
        await fetchDevices()
        emit('device-updated', deviceList.value)
      }
    }
  } catch (e) {
    console.error('Save device failed:', e)
  } finally {
    submitting.value = false
  }
}

const handleDelete = async (deviceId) => {
  try {
    const res = await api.deleteDevice(props.taskId, deviceId)
    if (res.code === 0) {
      ElMessage.success('设备已删除')
      await fetchDevices()
      emit('device-updated', deviceList.value)
    }
  } catch (e) {
    console.error('Delete device failed:', e)
  }
}

const openImportDialog = (row) => {
  targetDevice.value = row
  selectedPaths.value = []
  manualPathInput.value = ''
  textLogContent.value = ''
  importTab.value = 'paths'
  showImportDialog.value = true
}

// fsStat 单次上限 500 条，超出时分批校验
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

// 打开服务端本地路径选择器（文件与目录均可选）
const openPicker = () => {
  showPathPicker.value = true
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

const selectedFileCount = computed(() => selectedPaths.value.filter(p => !p.is_dir).length)
const selectedDirCount = computed(() => selectedPaths.value.filter(p => p.is_dir).length)

const pendingCount = computed(() =>
  importTab.value === 'text' ? (textLogContent.value.trim() ? 1 : 0) : selectedPaths.value.length
)

const canImport = computed(() =>
  !importing.value &&
  !statting.value &&
  (importTab.value === 'text' ? textLogContent.value.trim() !== '' : selectedPaths.value.length > 0)
)

const formatSize = bytes => sharedFormatSize(bytes, '')

const submitImportLogs = async () => {
  if (!targetDevice.value) return

  if (importTab.value === 'text') {
    if (!textLogContent.value.trim()) {
      ElMessage.warning('请输入日志文本内容')
      return
    }
    importing.value = true
    try {
      const res = await api.importDeviceLogsText(props.taskId, targetDevice.value.id, {
        content: textLogContent.value,
        fileName: `${targetDevice.value.device_name}_manual.txt`,
        conflictMode: importConflictMode.value
      })
      if (res.code === 0) {
        showImportDialog.value = false
        ElMessage.success('文本日志导入分析已启动')
        if (res.data?.job_id) {
          emit('open-progress', res.data.job_id)
        }
        fetchDevices()
      }
    } catch (e) {
      console.error('Import text failed:', e)
    } finally {
      importing.value = false
    }
    return
  }

  if (selectedPaths.value.length === 0) {
    ElMessage.warning('请选择至少一个日志文件或目录')
    return
  }

  importing.value = true
  try {
    const res = await api.importDeviceLogsByPaths(props.taskId, targetDevice.value.id, {
      paths: selectedPaths.value.map(p => p.path),
      exts: logExts,
      recursive: true,
      conflictMode: importConflictMode.value
    })
    if (res.code === 0) {
      showImportDialog.value = false
      selectedPaths.value = []
      ElMessage.success('日志导入分析任务已启动')
      if (res.data?.job_id) {
        emit('open-progress', res.data.job_id)
      }
      fetchDevices()
    }
  } catch (e) {
    console.error('Import logs failed:', e)
  } finally {
    importing.value = false
  }
}

watch(() => props.taskId, (newVal) => {
  if (newVal) {
    fetchDevices()
  }
})

onMounted(() => {
  fetchDevices()
})

defineExpose({
  fetchDevices,
  // 供父组件在设备变更后主动刷新（替代 :key 强制重挂载，避免丢失内部状态）
  refresh: fetchDevices
})
</script>

<style scoped>
/* UI-12: 同名文件冲突策略选择器 */
.conflict-strategy-box {
  margin-top: 14px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 12px;
  border: 1px dashed var(--gray-300);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
}

.conflict-label {
  font-size: var(--fs-md);
  font-weight: 600;
  color: var(--gray-700);
  flex-shrink: 0;
}

.conflict-hint {
  font-size: var(--fs-sm);
  color: var(--gray-500);
  flex: 1;
  min-width: 200px;
}

.text-meta-row {
  display: flex;
  justify-content: space-between;
  font-size: var(--fs-sm);
  color: var(--gray-400);
  margin-top: 4px;
}

.device-manager-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.action-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--gray-0);
  padding: 12px 16px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border-base);
}
.bar-left {
  display: flex;
  align-items: center;
  gap: 12px;
}
.tip-text {
  font-size: var(--fs-md);
  color: var(--gray-500);
}
.table-card {
  border-radius: var(--radius-lg);
}
.device-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.color-dot {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  flex-shrink: 0;
}
.code-text {
  font-family: monospace;
  background: var(--gray-100);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  font-size: var(--fs-sm);
}
.empty-guide {
  padding: 40px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  color: var(--gray-500);
}
.empty-actions {
  display: flex;
  gap: 12px;
  margin-top: 8px;
}
.matched-count-val {
  color: var(--success-text);
  font-weight: 600;
}
.desc-val {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
