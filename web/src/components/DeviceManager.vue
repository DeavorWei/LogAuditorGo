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
              <span class="color-dot" :style="{ backgroundColor: row.color || '#3B82F6' }"></span>
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
            <span style="color: #16a34a; font-weight: 600;">{{ row.matched_count }}</span>
          </template>
        </el-table-column>

        <el-table-column prop="description" label="描述" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span style="color: #64748b; font-size: 13px;">{{ row.description || '-' }}</span>
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
        <el-icon size="42" color="#94a3b8"><Monitor /></el-icon>
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

    <!-- 向设备导入日志对话框 -->
    <el-dialog
      v-model="showImportDialog"
      :title="`向设备「${targetDevice?.device_name}」导入日志`"
      width="620px"
      destroy-on-close
    >
      <el-tabs v-model="importTab">
        <el-tab-pane label="从本机目录导入" name="dir">
          <div class="path-import-pane">
            <el-icon size="36" color="#16a34a"><folder-add /></el-icon>
            <div class="pane-title">选择该设备的日志目录</div>
            <p class="pane-desc">
              目录由服务端进程直接读取，不经过浏览器上传。导入后日志将直接归属给「{{ targetDevice?.device_name }}」
            </p>
            <el-button type="success" size="small" @click="openPicker('dir')">选择日志目录</el-button>
          </div>
        </el-tab-pane>

        <el-tab-pane label="从本机文件导入" name="file">
          <div class="path-import-pane">
            <el-icon size="36" color="#0284c7"><upload-filled /></el-icon>
            <div class="pane-title">选择该设备的日志文件</div>
            <p class="pane-desc">
              支持 .log / .txt / .syslog 等常见格式，可同时选择多个文件
            </p>
            <el-button type="primary" size="small" @click="openPicker('file')">选择日志文件</el-button>
          </div>
        </el-tab-pane>

        <el-tab-pane label="直接粘贴文本" name="text">
          <el-input
            v-model="textLogContent"
            type="textarea"
            :rows="8"
            placeholder="直接在此处粘贴 Syslog 原始日志报文文本..."
          />
        </el-tab-pane>
      </el-tabs>

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
        <span class="dialog-footer">
          <el-button @click="showImportDialog = false">取消</el-button>
          <el-button type="primary" :loading="importing" @click="submitImportLogs">
            开始导入分析
          </el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 服务端本地路径选择器（设备日志导入专用） -->
    <ServerPathPicker
      v-model="selectedPaths"
      v-model:visible="showPathPicker"
      :mode="pickerMode"
      :exts="logExts"
      :multiple="true"
      favorite-key="device-logs"
      :title="pickerMode === 'dir' ? '选择日志目录' : '选择日志文件'"
    />
  </div>
</template>

<script setup>
// UI-16: defineProps / defineEmits 是编译器宏，无需从 vue 导入
import { ref, computed, nextTick, onMounted, watch } from 'vue'
import { DEVICE_FORM_TYPE_OPTIONS, DEFAULT_DEVICE_FORM_TYPE, deviceTypeLabel } from '@/constants/deviceTypes'
import { ElMessage } from 'element-plus'
import { Monitor, UploadFilled, FolderAdd, Close } from '@element-plus/icons-vue'
import api from '@/api'
import ServerPathPicker from '@/components/ServerPathPicker.vue'

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

const colorPresets = [
  '#3B82F6', '#10B981', '#F59E0B', '#EF4444',
  '#8B5CF6', '#EC4899', '#14B8A6', '#F97316',
  '#6366F1', '#84CC16', '#06B6D4', '#64748B'
]

const deviceFormRef = ref(null)
const deviceForm = ref({
  device_name: '',
  device_type: DEFAULT_DEVICE_FORM_TYPE,
  hostname: '',
  management_ip: '',
  color: '#3B82F6',
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
const importTab = ref('dir')
const selectedPaths = ref([])
const showPathPicker = ref(false)
const pickerMode = ref('dir')
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
    color: row.color || '#3B82F6',
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
  textLogContent.value = ''
  importTab.value = 'dir'
  showImportDialog.value = true
}

// 打开服务端本地路径选择器（dir: 目录模式，file: 文件模式）
const openPicker = mode => {
  pickerMode.value = mode
  importTab.value = mode
  showPathPicker.value = true
}

const removePath = index => {
  selectedPaths.value.splice(index, 1)
}

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
    ElMessage.warning('请选择至少一个日志目录或日志文件')
    return
  }

  importing.value = true
  try {
    const res = await api.importDeviceLogsByPaths(props.taskId, targetDevice.value.id, {
      paths: selectedPaths.value,
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
/* 路径导入面板 */
.path-import-pane {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 20px 16px;
  text-align: center;
  background: #f8fafc;
}

.pane-title {
  font-size: 14px;
  font-weight: 600;
  color: #1e293b;
  margin: 8px 0 6px 0;
}

.pane-desc {
  font-size: 12px;
  color: #64748b;
  line-height: 1.7;
  margin: 0 auto 14px auto;
  max-width: 440px;
}

/* UI-12: 同名文件冲突策略选择器 */
.conflict-strategy-box {
  margin-top: 14px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  padding: 10px 12px;
  border: 1px dashed #cbd5e1;
  border-radius: 6px;
  background: #f8fafc;
}

.conflict-label {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  flex-shrink: 0;
}

.conflict-hint {
  font-size: 12px;
  color: #64748b;
  flex: 1;
  min-width: 200px;
}

/* 已选路径清单 */
.pending-paths-box {
  margin-top: 14px;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 10px 12px;
  background: #f8fafc;
}

.pending-paths-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: #334155;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #e2e8f0;
}

.pending-paths-list {
  max-height: 140px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.pending-path-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  background: #fff;
  padding: 5px 8px;
  border-radius: 4px;
  border: 1px solid #e2e8f0;
}

.pending-path-item .path-value {
  flex: 1;
  font-weight: 500;
  color: #1e293b;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pending-path-item .del-btn {
  color: #94a3b8;
  cursor: pointer;
  flex-shrink: 0;
}

.pending-path-item .del-btn:hover {
  color: #ef4444;
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
  background: #fff;
  padding: 12px 16px;
  border-radius: 8px;
  border: 1px solid #e2e8f0;
}
.bar-left {
  display: flex;
  align-items: center;
  gap: 12px;
}
.tip-text {
  font-size: 13px;
  color: #64748b;
}
.table-card {
  border-radius: 8px;
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
  background: #f1f5f9;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 12px;
}
.empty-guide {
  padding: 40px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  color: #64748b;
}
.empty-actions {
  display: flex;
  gap: 12px;
  margin-top: 8px;
}
</style>
