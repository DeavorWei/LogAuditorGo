<template>
  <div class="documents-page">
    <!-- 顶部文档管理状态栏 -->
    <el-card shadow="never" class="doc-header-card">
      <div class="header-content">
        <div class="header-left">
          <h2 style="font-size: 16px; margin-bottom: 4px; display: flex; align-items: center; gap: 8px;">
            <span>华为官方产品文档知识库管理</span>
            <el-tag size="small" type="primary" effect="dark">
              已导入 {{ docList.length }} 个文档包
            </el-tag>
          </h2>
          <div class="doc-stats-summary">
            <span class="stats-item">累计叶子日志: <strong>{{ totalLogsCount }}</strong> 条</span>
            <span class="stats-divider">|</span>
            <span class="stats-item">累计叶子告警: <strong>{{ totalAlarmsCount }}</strong> 条</span>
            <span class="stats-divider">|</span>
            <span class="stats-desc">支持通过选择本地文件夹或 HDX 压缩包导入并自动跨版本去重</span>
          </div>
        </div>
        <div class="header-right">
          <el-input
            v-model="searchKeyword"
            placeholder="搜索文档名称/LibID/型号..."
            prefix-icon="Search"
            clearable
            size="small"
            style="width: 240px; margin-right: 8px;"
          />
          <el-button icon="Refresh" size="small" :loading="loading" @click="fetchDocs">刷新</el-button>
          <el-button type="primary" icon="Upload" size="small" @click="openImportDialog">
            导入 HDX 文档
          </el-button>
        </div>
      </div>
    </el-card>

    <!-- 空文档知识库引导卡片 (docList 为空时展示) -->
    <div v-if="!loading && docList.length === 0" class="empty-doc-guide">
      <el-card shadow="never" class="guide-card">
        <div class="guide-header">
          <el-icon size="48" color="#0284c7"><FolderOpened /></el-icon>
          <h3>知识库尚未导入华为官方 HDX 产品文档</h3>
          <p>请选择以下方式之一，将华为官方 HDX 产品文档导入知识库，系统将自动递归抽取叶子日志/告警并完成跨版本去重与 RCA 拓扑关联：</p>
        </div>

        <div class="guide-actions">
          <div class="action-tile" @click="openGuidePicker('files')">
            <el-icon size="32" color="#0284c7"><Files /></el-icon>
            <h4>选择 HDX 压缩包</h4>
            <p>直接选择服务端本机上的 .hdx 官方产品文档压缩包，支持多选</p>
            <el-button type="primary" size="small">选择 HDX 压缩包 (.hdx)</el-button>
          </div>

          <div class="action-tile" @click="openGuidePicker('dir')">
            <el-icon size="32" color="#16a34a"><FolderAdd /></el-icon>
            <h4>选择 HDX 文档目录</h4>
            <p>选择解压后的 HDX 文档目录，或包含多个文档包的归档父目录</p>
            <el-button type="success" size="small">选择 HDX 文档目录</el-button>
          </div>
        </div>
      </el-card>
    </div>

    <!-- 文档表格 (已有数据时展示) -->
    <el-card v-else shadow="never" class="table-card">
      <el-table :data="filteredDocList" v-loading="loading" style="width: 100%;" border>
        <el-table-column prop="lib_id" label="LibID" width="120">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">{{ row.lib_id }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="lib_name" label="产品文档全称" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span style="font-weight: 600; color: #0284c7;">📖 {{ row.lib_name }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="product_type" label="适用产品型号" width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.product_type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="product_version" label="软件版本" width="140">
          <template #default="{ row }">
            <span style="font-family: monospace; font-weight: bold;">{{ row.product_version }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="issue_date" label="发布日期" width="120" align="center" />
        <el-table-column prop="log_count" label="叶子日志数" width="110" align="center">
          <template #default="{ row }">
            <span style="color: #0284c7; font-weight: bold;">{{ row.log_count }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="alarm_count" label="叶子告警数" width="110" align="center">
          <template #default="{ row }">
            <span style="color: #ea580c; font-weight: bold;">{{ row.alarm_count }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="imported_at" label="导入时间" width="160" align="center">
          <template #default="{ row }">
            {{ formatTime(row.imported_at) }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="100" align="center">
          <template #default="{ row }">
            <el-popconfirm title="确定删除该文档及其所有版本映射吗？" @confirm="handleDelete(row.id)">
              <template #reference>
                <el-button type="danger" link size="small">删除</el-button>
              </template>
            </el-popconfirm>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 导入 HDX 文档弹窗 -->
    <el-dialog v-model="showImportDialog" title="导入华为官方 HDX 产品文档知识库" width="640px">
      <el-tabs v-model="importTab" type="border-card">
        <!-- 标签页 1: 从本机目录导入 -->
        <el-tab-pane label="从本机目录导入" name="dir">
          <div class="path-import-pane">
            <el-icon size="40" color="#16a34a"><FolderAdd /></el-icon>
            <div class="pane-title">选择包含 HDX 文档包的目录</div>
            <p class="pane-desc">
              目录由服务端进程直接读取，不经过浏览器上传，因此可安全处理数十万文件、数 GB 的超大目录。
              可一次选择多个目录，系统将自动递归发现其中所有包含 profile.xml 的文档包。
            </p>
            <el-button type="success" size="small" @click="openPicker('dir')">选择 HDX 文档目录</el-button>
          </div>
        </el-tab-pane>

        <!-- 标签页 2: 从本机 HDX 压缩包导入 -->
        <el-tab-pane label="从本机压缩包导入" name="files">
          <div class="path-import-pane">
            <el-icon size="40" color="#0284c7"><Files /></el-icon>
            <div class="pane-title">选择一个或多个 .hdx 压缩包</div>
            <p class="pane-desc">
              由服务端流式读取包内的日志与告警页面，全程不解压、不占用临时磁盘空间，
              导入更快，且原始压缩包保持原样。
            </p>
            <el-button type="primary" size="small" @click="openPicker('file')">选择 HDX 压缩包 (.hdx)</el-button>
          </div>
        </el-tab-pane>
      </el-tabs>

      <!-- 已选路径清单 -->
      <div v-if="selectedPaths.length > 0" class="pending-paths-box">
        <div class="pending-paths-header">
          <span>已选择 <strong>{{ selectedPaths.length }}</strong> 个路径</span>
          <el-button type="danger" link size="small" @click="selectedPaths = []">清空</el-button>
        </div>
        <div class="pending-paths-list">
          <div v-for="(p, idx) in selectedPaths" :key="p" class="pending-path-item">
            <span class="path-value" :title="p">📁 {{ p }}</span>
            <el-tag v-if="isConflictDocFile(pathBaseName(p))" type="danger" size="small">可能已存在同名/版本</el-tag>
            <el-icon class="del-btn" @click="removePath(idx)"><Close /></el-icon>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="showImportDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" :disabled="!selectedPaths.length" @click="handleCheckAndStartImport">
          开始导入并入库
        </el-button>
      </template>
    </el-dialog>

    <!-- 同名 / 已存在文档冲突处理弹窗 -->
    <el-dialog v-model="showConflictDialog" title="⚠️ 检测到已有同名/已导入文档" width="540px">
      <div class="conflict-dialog-body">
        <p style="margin-bottom: 12px; line-height: 1.6; color: #334155;">
          知识库中已存在以下 <strong>{{ conflictingFileNames.length }}</strong> 个可能重名的文档：
        </p>
        <div class="conflict-file-list">
          <div v-for="name in conflictingFileNames" :key="name" class="conflict-item">
            ⚠️ <strong>{{ name }}</strong>
          </div>
        </div>
        <p style="margin-top: 14px; font-size: 13px; color: #64748b;">
          请选择冲突文档的处理方式：
        </p>
      </div>
      <template #footer>
        <el-button @click="showConflictDialog = false">取消</el-button>
        <el-button type="warning" :loading="submitting" @click="executeImportWithConflict('skip')">
          跳过已存在文档 (仅导入新文档)
        </el-button>
        <el-button type="danger" :loading="submitting" @click="executeImportWithConflict('overwrite')">
          覆盖已有文档 (重新解析并更新版本映射)
        </el-button>
      </template>
    </el-dialog>

    <!-- 全流程阶段进度实时追踪弹窗 -->
    <ImportProgressModal
      v-model="showProgressModal"
      :job-id="currentJobId"
      title="华为官方 HDX 产品文档知识库导入流水线"
      @completed="handleImportCompleted"
    />

    <!-- 服务端本地路径选择器（文档导入专用） -->
    <ServerPathPicker
      v-model="selectedPaths"
      v-model:visible="showPathPicker"
      :mode="pickerMode"
      :exts="pickerMode === 'file' ? hdxExts : []"
      :multiple="true"
      favorite-key="hdx-documents"
      :title="pickerMode === 'dir' ? '选择 HDX 文档目录' : '选择 HDX 压缩包'"
    />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { FolderOpened, Files, FolderAdd, Close, Search, Refresh, Upload } from '@element-plus/icons-vue'
import api from '@/api'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import { formatTime as sharedFormatTime } from '@/utils/format'

const loading = ref(false)
const docList = ref([])
const searchKeyword = ref('')

// 导入相关状态
const showImportDialog = ref(false)
const importTab = ref('dir')
const submitting = ref(false)

// 服务端本地路径选择相关
const selectedPaths = ref([])
const showPathPicker = ref(false)
const pickerMode = ref('dir')
const hdxExts = ['.hdx']

// 进度追踪相关
const showProgressModal = ref(false)
const currentJobId = ref('')

// 冲突处理相关
const showConflictDialog = ref(false)
const conflictingFileNames = ref([])

// 计算属性
const totalLogsCount = computed(() => {
  return docList.value.reduce((sum, d) => sum + (d.log_count || 0), 0)
})

const totalAlarmsCount = computed(() => {
  return docList.value.reduce((sum, d) => sum + (d.alarm_count || 0), 0)
})

const filteredDocList = computed(() => {
  if (!searchKeyword.value.trim()) return docList.value
  const kw = searchKeyword.value.trim().toLowerCase()
  return docList.value.filter(d =>
    (d.lib_id && d.lib_id.toLowerCase().includes(kw)) ||
    (d.lib_name && d.lib_name.toLowerCase().includes(kw)) ||
    (d.product_type && d.product_type.toLowerCase().includes(kw)) ||
    (d.product_version && d.product_version.toLowerCase().includes(kw))
  )
})

const fetchDocs = async () => {
  loading.value = true
  try {
    const res = await api.getDocuments()
    if (res.code === 0) {
      docList.value = res.data || []
    }
  } finally {
    loading.value = false
  }
}

// 导入弹窗打开与重置
const openImportDialog = () => {
  selectedPaths.value = []
  importTab.value = 'dir'
  showImportDialog.value = true
}

// 打开服务端本地路径选择器（dir: 目录模式，file: .hdx 压缩包模式）
const openPicker = mode => {
  pickerMode.value = mode
  importTab.value = mode
  showPathPicker.value = true
}

// 引导区域快捷触发：直接拉起对应模式的路径选择器
const openGuidePicker = mode => {
  selectedPaths.value = []
  showImportDialog.value = true
  openPicker(mode)
}

const removePath = index => {
  selectedPaths.value.splice(index, 1)
}

// 取路径的末级名称（兼容 Windows 与 Unix 分隔符）
const pathBaseName = p => {
  if (!p) return ''
  const segs = String(p).replace(/\\/g, '/').split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

// 判断待上传文件是否与已导入文档冲突
const isConflictDocFile = (fileName) => {
  const cleanName = fileName.replace(/\.hdx$/i, '').toLowerCase()
  return docList.value.some(d => {
    const libIdMatch = d.lib_id && cleanName.includes(d.lib_id.toLowerCase())
    const libNameMatch = d.lib_name && cleanName.includes(d.lib_name.toLowerCase())
    return libIdMatch || libNameMatch
  })
}

// 检查冲突并启动导入
const handleCheckAndStartImport = () => {
  if (selectedPaths.value.length === 0) {
    ElMessage.warning('请选择至少一个 HDX 文档目录或压缩包')
    return
  }

  // 检查是否有同名/重叠冲突文档
  const conflicts = []
  for (const p of selectedPaths.value) {
    const name = pathBaseName(p)
    if (isConflictDocFile(name)) {
      conflicts.push(name)
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('overwrite')
  }
}

// 执行导入：仅提交路径，由服务端直接读取并解析，走全流程阶段进度追踪
const executeImportWithConflict = async (conflictMode) => {
  submitting.value = true
  showConflictDialog.value = false

  try {
    const res = await api.importDocumentsByPaths(selectedPaths.value, conflictMode, true)
    if (res.code === 0) {
      showImportDialog.value = false
      selectedPaths.value = []

      // 开启全流程阶段进度实时追踪
      if (res.data?.job_id) {
        currentJobId.value = res.data.job_id
        showProgressModal.value = true
      } else {
        showImportSuccessFeedback(res.data)
        await fetchDocs()
      }
    }
  } finally {
    submitting.value = false
  }
}

// 导入完成回调，平滑刷新列表
const handleImportCompleted = (result) => {
  showImportSuccessFeedback(result)
  fetchDocs()
}

const showImportSuccessFeedback = (stats) => {
  if (!stats) {
    ElMessage.success('导入完成！')
    return
  }
  const total = stats.total_documents || 1
  const skipped = stats.skipped_docs ? stats.skipped_docs.length : 0
  const imported = stats.imported_docs ? stats.imported_docs.length : (total - skipped)

  if (imported > 0) {
    let msg = `导入成功！已入库 ${imported} 个文档包`
    if (skipped > 0) {
      msg += ` (跳过 ${skipped} 个已存在文档)`
    }
    msg += `，累计提取叶子日志 ${stats.leaf_log_count} 条，告警 ${stats.leaf_alarm_count} 条，新增唯一知识 ${stats.unique_knowledge_added} 条`
    ElMessage.success({ message: msg, duration: 5000 })
  } else if (skipped > 0) {
    ElMessage.info({ message: `所选文档均已存在，已跳过 ${skipped} 个文档包`, duration: 4000 })
  } else {
    ElMessage.success('文档导入完成！')
  }
}

const handleDelete = async (id) => {
  try {
    const res = await api.deleteDocument(id)
    if (res.code === 0) {
      ElMessage.success('文档删除成功')
      fetchDocs()
    }
  } catch (e) {}
}

// WEB-16: 复用统一实现（同时补齐 Go time.Time 零值保护）
const formatTime = sharedFormatTime

onMounted(() => {
  fetchDocs()
})
</script>

<style scoped>
.documents-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.doc-header-card {
  border-radius: 8px;
}

.header-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-left {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.doc-stats-summary {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #64748b;
}

.stats-item strong {
  color: #0284c7;
  font-weight: 600;
}

.stats-divider {
  color: #cbd5e1;
}

.stats-desc {
  color: #94a3b8;
}

.header-right {
  display: flex;
  align-items: center;
}

.table-card {
  border-radius: 8px;
}

/* 空知识库引导卡片 */
.empty-doc-guide {
  padding: 10px 0;
}

.guide-card {
  border-radius: 8px;
  background: #f8fafc;
  border: 1px dashed #cbd5e1;
}

.guide-header {
  text-align: center;
  padding: 20px 0 16px 0;
}

.guide-header h3 {
  font-size: 18px;
  color: #1e293b;
  margin: 12px 0 6px 0;
}

.guide-header p {
  font-size: 13px;
  color: #64748b;
}

.guide-actions {
  display: flex;
  gap: 20px;
  justify-content: center;
  padding: 16px 20px 24px 20px;
}

.action-tile {
  flex: 1;
  max-width: 320px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 20px 16px;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: space-between;
}

.action-tile:hover {
  border-color: #0284c7;
  box-shadow: 0 4px 12px rgba(2, 132, 199, 0.12);
  transform: translateY(-2px);
}

.action-tile h4 {
  font-size: 15px;
  color: #0f172a;
  margin: 10px 0 6px 0;
}

.action-tile p {
  font-size: 12px;
  color: #64748b;
  line-height: 1.4;
  margin-bottom: 14px;
  min-height: 34px;
}

/* 导入弹窗样式 */
.path-import-pane {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 24px 20px;
  text-align: center;
  background: #f8fafc;
}

.pane-title {
  font-size: 14px;
  font-weight: 600;
  color: #1e293b;
  margin: 10px 0 6px 0;
}

.pane-desc {
  font-size: 12px;
  color: #64748b;
  line-height: 1.7;
  margin: 0 auto 14px auto;
  max-width: 470px;
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
  max-height: 160px;
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

.pending-file-item .file-size {
  color: #94a3b8;
  font-size: 11px;
}

.pending-file-item .del-btn {
  cursor: pointer;
  color: #94a3b8;
  transition: color 0.2s;
}

.pending-file-item .del-btn:hover {
  color: #ef4444;
}

/* 冲突弹窗 */
.conflict-dialog-body {
  font-size: 14px;
}

.conflict-file-list {
  max-height: 140px;
  overflow-y: auto;
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 6px;
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.conflict-item {
  font-size: 12px;
  color: #991b1b;
}
</style>
