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

        <el-table-column prop="device_type" label="设备类型" width="130">
          <template #default="{ row }">
            <el-tag size="small" effect="light">{{ row.device_type || 'Router' }}</el-tag>
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
      <el-form :model="deviceForm" label-width="110px">
        <el-form-item label="设备名称" required>
          <el-input v-model="deviceForm.device_name" placeholder="例如: Router-Core-01" />
        </el-form-item>

        <el-form-item label="设备类型" required>
          <el-select v-model="deviceForm.device_type" style="width: 100%;">
            <el-option label="Router (路由器)" value="Router" />
            <el-option label="Switch (交换机)" value="Switch" />
            <el-option label="Firewall (防火墙)" value="Firewall" />
            <el-option label="CloudEngine (数据中心交换机)" value="CloudEngine" />
            <el-option label="NetEngine (核心路由器)" value="NetEngine" />
            <el-option label="USG (安全网关)" value="HiSecEngine-USG" />
            <el-option label="Other (其他网络设备)" value="Other" />
          </el-select>
        </el-form-item>

        <el-form-item label="匹配 Hostname">
          <el-input v-model="deviceForm.hostname" placeholder="日志报文中的主机名，如 Router-Core-01" />
        </el-form-item>

        <el-form-item label="管理 IP">
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
        <el-tab-pane label="选择文件上传" name="file">
          <el-upload
            ref="uploadRef"
            drag
            multiple
            :auto-upload="false"
            :file-list="fileList"
            :on-change="handleFileChange"
            :on-remove="handleFileRemove"
          >
            <el-icon class="el-icon--upload"><upload-filled /></el-icon>
            <div class="el-upload__text">
              拖拽日志文件到此处，或 <em>点击选取文件</em>
            </div>
            <template #tip>
              <div class="el-upload__tip">
                支持上传单个或多个 .log, .txt, .syslog 日志文件，导入后将直接归属给「{{ targetDevice?.device_name }}」
              </div>
            </template>
          </el-upload>
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

      <template #footer>
        <span class="dialog-footer">
          <el-button @click="showImportDialog = false">取消</el-button>
          <el-button type="primary" :loading="importing" @click="submitImportLogs">
            开始导入分析
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted, defineProps, defineEmits, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Monitor, UploadFilled } from '@element-plus/icons-vue'
import api from '@/api'

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

const deviceForm = ref({
  device_name: '',
  device_type: 'Router',
  hostname: '',
  management_ip: '',
  color: '#3B82F6',
  description: ''
})

const showImportDialog = ref(false)
const targetDevice = ref(null)
const importTab = ref('file')
const fileList = ref([])
const textLogContent = ref('')

const fetchDevices = async () => {
  if (!props.taskId) return
  loading.value = true
  try {
    const res = await api.getDevices(props.taskId)
    if (res.code === 0) {
      deviceList.value = res.data || []
      emit('device-updated', deviceList.value)
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
    device_type: 'Router',
    hostname: '',
    management_ip: '',
    color: defaultColor,
    description: ''
  }
  showDeviceDialog.value = true
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
}

const saveDevice = async () => {
  if (!deviceForm.value.device_name) {
    ElMessage.warning('请输入设备名称')
    return
  }
  submitting.value = true
  try {
    if (isEditing.value && editingDeviceId.value) {
      const res = await api.updateDevice(props.taskId, editingDeviceId.value, deviceForm.value)
      if (res.code === 0) {
        ElMessage.success('设备更新成功')
        showDeviceDialog.value = false
        fetchDevices()
      }
    } else {
      const res = await api.createDevice(props.taskId, deviceForm.value)
      if (res.code === 0) {
        ElMessage.success('设备创建成功')
        showDeviceDialog.value = false
        fetchDevices()
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
      fetchDevices()
    }
  } catch (e) {
    console.error('Delete device failed:', e)
  }
}

const openImportDialog = (row) => {
  targetDevice.value = row
  fileList.value = []
  textLogContent.value = ''
  importTab.value = 'file'
  showImportDialog.value = true
}

const handleFileChange = (file, files) => {
  fileList.value = files
}

const handleFileRemove = (file, files) => {
  fileList.value = files
}

const submitImportLogs = async () => {
  if (!targetDevice.value) return

  if (importTab.value === 'file') {
    if (fileList.value.length === 0) {
      ElMessage.warning('请选择至少一个日志文件')
      return
    }
    const formData = new FormData()
    fileList.value.forEach(f => {
      if (f.raw) formData.append('files', f.raw)
    })
    formData.append('conflict_mode', 'overwrite')
    formData.append('async', 'true')

    importing.value = true
    try {
      const res = await api.importDeviceLogs(props.taskId, targetDevice.value.id, formData, true)
      if (res.code === 0) {
        showImportDialog.value = false
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
  } else {
    if (!textLogContent.value.trim()) {
      ElMessage.warning('请输入日志文本内容')
      return
    }
    importing.value = true
    try {
      const res = await api.importDeviceLogs(props.taskId, targetDevice.value.id, {
        content: textLogContent.value,
        file_name: `${targetDevice.value.device_name}_manual.txt`,
        conflict_mode: 'overwrite',
        async: true
      }, true)
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
</script>

<style scoped>
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
