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
            <span class="stats-desc">支持选择本地文件夹自动扫描 HDX 压缩包与解压文档并批量入库</span>
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
          <p>请选择包含华为官方 HDX 文档的本地文件夹，系统将自动递归扫描其下的所有 .hdx 压缩包与解压文档并完成跨版本去重与知识库构建：</p>
        </div>

        <div class="guide-actions">
          <div class="action-tile action-tile-single" @click="openGuidePicker">
            <el-icon size="36" color="#0284c7"><FolderAdd /></el-icon>
            <h4>选择 HDX 文档所在文件夹</h4>
            <p>支持任意本地文件夹，系统将自动递归扫描目录下的所有 .hdx 压缩包与 profile.xml 解压文档，一键批量入库</p>
            <el-button type="primary">选择文件夹并开始扫描</el-button>
          </div>
        </div>
      </el-card>
    </div>

    <!-- 文档表格与批量操作区 (已有数据时展示) -->
    <el-card v-else shadow="never" class="table-card">
      <!-- 批量操作控制栏 -->
      <div class="batch-action-bar">
        <div class="batch-left">
          <el-button size="small" @click="handleToggleSelectAllDocs">
            {{ isAllDocsSelected ? '取消全选所有' : '全选所有文档 (' + docList.length + ')' }}
          </el-button>
          <span v-if="selectedDocIds.length > 0" class="batch-count-tip">
            已选择 <strong>{{ selectedDocIds.length }}</strong> / {{ docList.length }} 个文档包
          </span>
          <el-button
            v-if="selectedDocIds.length > 0"
            size="small"
            link
            type="primary"
            @click="clearDocSelection"
          >
            清空勾选
          </el-button>
        </div>
        <div class="batch-right">
          <el-button
            v-if="selectedDocIds.length > 0"
            type="danger"
            size="small"
            icon="Delete"
            :loading="deletingBatch"
            @click="handleBatchDelete"
          >
            批量删除 (已选 {{ selectedDocIds.length }} 项)
          </el-button>
        </div>
      </div>

      <el-table
        ref="docTableRef"
        :data="filteredDocList"
        v-loading="loading"
        style="width: 100%;"
        border
        @selection-change="handleDocSelectionChange"
      >
        <el-table-column type="selection" width="45" align="center" />
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

    <!-- 导入 HDX 文档弹窗（统一宽体单页面：选择文件夹自动扫描并导入） -->
    <el-dialog
      v-model="showImportDialog"
      title="导入华为官方 HDX 产品文档知识库"
      width="1000px"
      top="6vh"
      destroy-on-close
    >
      <div class="import-dialog-content">
        <!-- 文件夹选择行 -->
        <div class="folder-select-row">
          <el-input
            v-model="selectedFolder"
            placeholder="输入或粘贴包含 HDX 文档的本地文件夹绝对路径，如 D:\HuaweiHDX..."
            clearable
            @keyup.enter="handleScan"
          >
            <template #prepend>
              <el-icon><Folder /></el-icon>
              <span style="margin-left: 4px;">文件夹</span>
            </template>
          </el-input>
          <el-button type="primary" plain icon="FolderOpened" @click="openServerFolderPicker">
            浏览文件夹
          </el-button>
          <el-button
            type="primary"
            icon="Search"
            :loading="scanning"
            :disabled="!selectedFolder.trim()"
            @click="handleScan"
          >
            扫描
          </el-button>
        </div>

        <!-- 提示条 -->
        <div class="folder-select-tip">
          <el-icon color="#0284c7"><InfoFilled /></el-icon>
          <span>选择文件夹后，系统将自动递归扫描该目录下的所有 <strong>.hdx 官方压缩包</strong> 与包含 <strong>profile.xml</strong> 的解压文档目录。</span>
        </div>

        <!-- 扫描结果区域 -->
        <div class="scan-result-container">
          <!-- 正在扫描 -->
          <div v-if="scanning" class="scan-state-box scanning-box">
            <el-icon class="is-loading" size="36" color="#0284c7"><Loading /></el-icon>
            <div class="state-title">正在递归扫描文件夹...</div>
            <div class="state-desc">正在解析目录结构与 HDX 压缩包内的 profile.xml 索引，请稍候</div>
          </div>

          <!-- 尚未选择或扫描 -->
          <div v-else-if="!scannedDone" class="scan-state-box unscanned-box">
            <el-icon size="48" color="#94a3b8"><FolderOpened /></el-icon>
            <div class="state-title">请选择包含 HDX 文档的文件夹</div>
            <div class="state-desc">点击上方「浏览文件夹」或粘贴路径后点击「扫描」，系统将自动发现全部待入库文档并默认全选</div>
          </div>

          <!-- 扫描完成但未发现文档 -->
          <div v-else-if="scannedItems.length === 0" class="scan-state-box empty-box">
            <el-empty description="该文件夹下未检测到包含 profile.xml 的文档目录或 .hdx 压缩包" :image-size="80">
              <el-button size="small" @click="openServerFolderPicker">重新选择文件夹</el-button>
            </el-empty>
          </div>

          <!-- 扫描完成并发现文档列表 -->
          <div v-else class="scanned-items-view">
            <!-- 统计摘要与全选控制 -->
            <div class="scan-summary-bar">
              <div class="summary-left">
                <span>共扫描发现 <strong>{{ scannedItems.length }}</strong> 个文档包</span>
                <span class="count-badge archive-badge">{{ archiveCount }} 个压缩包</span>
                <span class="count-badge dir-badge">{{ dirCount }} 个解压目录</span>
                <span class="selected-tip">（已勾选 <strong>{{ selectedItems.length }}</strong> / {{ scannedItems.length }} 项）</span>
                <el-button size="small" type="primary" link @click="toggleSelectAllScanned">
                  {{ isAllScannedSelected ? '取消全选' : '全选所有' }}
                </el-button>
              </div>
              <div class="summary-right" v-if="scannedItems.length > 5">
                <el-input
                  v-model="scanKeyword"
                  size="small"
                  placeholder="过滤文档名/型号/LibID..."
                  prefix-icon="Search"
                  clearable
                  style="width: 200px;"
                />
              </div>
            </div>

            <!-- 扫描条目表格 -->
            <el-table
              ref="scanTableRef"
              :data="filteredScannedItems"
              size="small"
              border
              max-height="440px"
              @selection-change="handleScanSelectionChange"
              class="scan-table"
            >
              <el-table-column type="selection" width="45" align="center" />
              <el-table-column label="类型" width="115" align="center">
                <template #default="{ row }">
                  <el-tag :type="row.type === 'archive' ? 'primary' : 'success'" size="small">
                    {{ row.type === 'archive' ? 'HDX 压缩包' : '解压目录' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="文档信息" min-width="260" show-overflow-tooltip>
                <template #default="{ row }">
                  <div class="doc-title-cell">
                    <span class="doc-name-text">{{ row.lib_name || row.name }}</span>
                    <div class="doc-meta-sub" v-if="row.lib_id || row.product_type">
                      <span v-if="row.lib_id" class="meta-tag">{{ row.lib_id }}</span>
                      <span v-if="row.product_type" class="meta-tag product-tag">{{ row.product_type }}</span>
                      <span v-if="row.product_version" class="meta-tag ver-tag">{{ row.product_version }}</span>
                    </div>
                  </div>
                </template>
              </el-table-column>
              <el-table-column prop="path" label="路径 / 文件名" min-width="220" show-overflow-tooltip>
                <template #default="{ row }">
                  <span class="path-cell-text" :title="row.path">{{ pathBaseName(row.path) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="大小" width="95" align="right">
                <template #default="{ row }">
                  <span style="color: #64748b; font-size: 11px;">{{ formatSize(row.size) }}</span>
                </template>
              </el-table-column>
              <el-table-column label="状态" width="120" align="center">
                <template #default="{ row }">
                  <el-tag v-if="row.exists_in_kb" type="warning" size="small" effect="plain">已入库同名/版本</el-tag>
                  <el-tag v-else type="success" size="small" effect="plain">全新文档</el-tag>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="showImportDialog = false">取消</el-button>
        <el-button
          type="primary"
          :loading="submitting"
          :disabled="!selectedItems.length || scanning"
          @click="handleCheckAndStartImport"
        >
          开始导入并入库 ({{ selectedItems.length }})
        </el-button>
      </template>
    </el-dialog>

    <!-- 同名 / 已存在文档冲突处理弹窗 -->
    <el-dialog v-model="showConflictDialog" title="⚠️ 检测到已有同名/已导入文档" width="540px">
      <div class="conflict-dialog-body">
        <p style="margin-bottom: 12px; line-height: 1.6; color: #334155;">
          所选文档中有 <strong>{{ conflictingFileNames.length }}</strong> 个在知识库中可能已存在同名或同版本记录：
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

    <!-- 服务端本地路径选择器（目录选择） -->
    <ServerPathPicker
      v-model:visible="showPathPicker"
      mode="dir"
      :multiple="false"
      favorite-key="hdx-documents"
      title="选择包含 HDX 文档的文件夹"
      @confirm="onPathPickerConfirm"
    />
  </div>
</template>

<script setup>
import { ref, computed, nextTick, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  FolderOpened,
  FolderAdd,
  Folder,
  Search,
  Refresh,
  Upload,
  InfoFilled,
  Loading,
  Delete
} from '@element-plus/icons-vue'
import api from '@/api'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import ServerPathPicker from '@/components/ServerPathPicker.vue'
import { formatTime as sharedFormatTime, formatSize as sharedFormatSize } from '@/utils/format'

const loading = ref(false)
const docList = ref([])
const searchKeyword = ref('')
const docTableRef = ref(null)

// 主列表多选与批量删除
const selectedDocIds = ref([])
const deletingBatch = ref(false)

// 统一导入弹窗相关状态
const showImportDialog = ref(false)
const selectedFolder = ref('')
const scanning = ref(false)
const scannedDone = ref(false)
const scannedItems = ref([])
const selectedItems = ref([])
const scanKeyword = ref('')
const scanTableRef = ref(null)
const submitting = ref(false)

// 服务端本地目录选择器弹窗
const showPathPicker = ref(false)

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

const archiveCount = computed(() => {
  return scannedItems.value.filter(i => i.type === 'archive').length
})

const dirCount = computed(() => {
  return scannedItems.value.filter(i => i.type === 'directory').length
})

const isAllScannedSelected = computed(() => {
  return scannedItems.value.length > 0 && selectedItems.value.length === scannedItems.value.length
})

const isAllDocsSelected = computed(() => {
  return docList.value.length > 0 && selectedDocIds.value.length === docList.value.length
})

const filteredScannedItems = computed(() => {
  if (!scanKeyword.value.trim()) return scannedItems.value
  const kw = scanKeyword.value.trim().toLowerCase()
  return scannedItems.value.filter(i =>
    (i.name && i.name.toLowerCase().includes(kw)) ||
    (i.lib_name && i.lib_name.toLowerCase().includes(kw)) ||
    (i.lib_id && i.lib_id.toLowerCase().includes(kw)) ||
    (i.product_type && i.product_type.toLowerCase().includes(kw))
  )
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
      // 校验现有选中集合，移除已不存在的 ID
      const validIds = new Set(docList.value.map(d => d.id))
      selectedDocIds.value = selectedDocIds.value.filter(id => validIds.has(id))
    }
  } finally {
    loading.value = false
  }
}

// 主文档表格勾选变化
const handleDocSelectionChange = selection => {
  selectedDocIds.value = selection.map(d => d.id)
}

// 全选/取消全选所有文档（全局跨所有数据）
const handleToggleSelectAllDocs = () => {
  if (isAllDocsSelected.value) {
    selectedDocIds.value = []
    docTableRef.value?.clearSelection()
  } else {
    selectedDocIds.value = docList.value.map(d => d.id)
    filteredDocList.value.forEach(row => {
      docTableRef.value?.toggleRowSelection(row, true)
    })
  }
}

const clearDocSelection = () => {
  selectedDocIds.value = []
  docTableRef.value?.clearSelection()
}

// 批量删除文档
const handleBatchDelete = async () => {
  if (selectedDocIds.value.length === 0) return

  try {
    await ElMessageBox.confirm(
      `确定要批量删除选中的 ${selectedDocIds.value.length} 个华为官方产品文档及其关联的所有版本映射吗？此操作不可逆！`,
      '批量删除文档确认',
      {
        confirmButtonText: '确定删除',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger'
      }
    )
  } catch {
    return
  }

  deletingBatch.value = true
  try {
    const res = await api.batchDeleteDocuments(selectedDocIds.value)
    if (res.code === 0) {
      ElMessage.success(`已成功批量删除 ${res.data?.deleted_count || selectedDocIds.value.length} 个产品文档`)
      clearDocSelection()
      await fetchDocs()
    }
  } finally {
    deletingBatch.value = false
  }
}

// 导入弹窗打开
const openImportDialog = () => {
  showImportDialog.value = true
}

// 引导区域快捷触发：打开导入弹窗并直接拉起路径选择器
const openGuidePicker = () => {
  showImportDialog.value = true
  openServerFolderPicker()
}

// 打开目录选择器
const openServerFolderPicker = () => {
  showPathPicker.value = true
}

// 目录选择器确认回调：填入输入框并自动触发扫描
const onPathPickerConfirm = paths => {
  if (paths && paths.length > 0) {
    selectedFolder.value = paths[0]
    handleScan()
  }
}

// 触发扫描：扫描完成后自动全选所有有效文档条目
const handleScan = async () => {
  const folder = selectedFolder.value.trim()
  if (!folder) {
    ElMessage.warning('请输入或选择本地文件夹路径')
    return
  }

  scanning.value = true
  scannedDone.value = false
  scannedItems.value = []
  selectedItems.value = []
  scanKeyword.value = ''

  try {
    const res = await api.scanHDXDocuments(folder)
    if (res.code === 0) {
      const items = res.data?.items || []
      scannedItems.value = items
      scannedDone.value = true

      // 关键：扫描完成后默认全选所有条目
      selectedItems.value = [...items]

      nextTick(() => {
        if (scanTableRef.value) {
          items.forEach(row => {
            scanTableRef.value.toggleRowSelection(row, true)
          })
        }
      })

      if (items.length === 0) {
        ElMessage.info('该文件夹下未检测到包含 profile.xml 的文档目录或 .hdx 压缩包')
      }
    }
  } finally {
    scanning.value = false
  }
}

// 全选/取消全选扫描结果
const toggleSelectAllScanned = () => {
  if (isAllScannedSelected.value) {
    selectedItems.value = []
    scanTableRef.value?.clearSelection()
  } else {
    selectedItems.value = [...scannedItems.value]
    scannedItems.value.forEach(row => {
      scanTableRef.value?.toggleRowSelection(row, true)
    })
  }
}

// 扫描表格多选变动回调
const handleScanSelectionChange = selection => {
  selectedItems.value = selection
}

// 取路径的末级名称（兼容 Windows 与 Unix 分隔符）
const pathBaseName = p => {
  if (!p) return ''
  const segs = String(p).replace(/\\/g, '/').split('/').filter(Boolean)
  return segs.length ? segs[segs.length - 1] : p
}

// 检查冲突并启动导入
const handleCheckAndStartImport = () => {
  if (selectedItems.value.length === 0) {
    ElMessage.warning('请勾选至少一个待导入的文档包')
    return
  }

  // 检查是否有同名/已存在冲突文档
  const conflicts = []
  for (const item of selectedItems.value) {
    if (item.exists_in_kb) {
      conflicts.push(item.lib_name || item.name)
    }
  }

  if (conflicts.length > 0) {
    conflictingFileNames.value = conflicts
    showConflictDialog.value = true
  } else {
    executeImportWithConflict('overwrite')
  }
}

// 执行导入：提交选中的文件/目录路径
const executeImportWithConflict = async conflictMode => {
  submitting.value = true
  showConflictDialog.value = false

  const pathsToImport = selectedItems.value.map(i => i.path)

  try {
    const res = await api.importDocumentsByPaths(pathsToImport, conflictMode, true)
    if (res.code === 0) {
      showImportDialog.value = false
      selectedItems.value = []

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
const handleImportCompleted = result => {
  showImportSuccessFeedback(result)
  fetchDocs()
}

const showImportSuccessFeedback = stats => {
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

const handleDelete = async id => {
  try {
    const res = await api.deleteDocument(id)
    if (res.code === 0) {
      ElMessage.success('文档删除成功')
      fetchDocs()
    }
  } catch (e) {}
}

const formatTime = sharedFormatTime
const formatSize = bytes => sharedFormatSize(bytes, '')

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

/* 批量操作控制栏 */
.batch-action-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-bottom: none;
  border-radius: 6px 6px 0 0;
}

.batch-left {
  display: flex;
  align-items: center;
  gap: 12px;
}

.batch-count-tip {
  font-size: 13px;
  color: #475569;
}

.batch-count-tip strong {
  color: #0284c7;
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
  justify-content: center;
  padding: 12px 20px 24px 20px;
}

.action-tile-single {
  width: 100%;
  max-width: 480px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  padding: 24px 20px;
  text-align: center;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  flex-direction: column;
  align-items: center;
}

.action-tile-single:hover {
  border-color: #0284c7;
  box-shadow: 0 4px 14px rgba(2, 132, 199, 0.12);
  transform: translateY(-2px);
}

.action-tile-single h4 {
  font-size: 16px;
  color: #0f172a;
  margin: 12px 0 6px 0;
}

.action-tile-single p {
  font-size: 13px;
  color: #64748b;
  line-height: 1.5;
  margin-bottom: 16px;
}

/* 统一导入弹窗样式 (1000px 宽体大弹窗) */
.import-dialog-content {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.folder-select-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.folder-select-row .el-input {
  flex: 1;
}

.folder-select-tip {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: #475569;
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 6px;
  padding: 8px 14px;
}

.scan-result-container {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  min-height: 380px;
  display: flex;
  flex-direction: column;
}

.scan-state-box {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  text-align: center;
  flex: 1;
}

.state-title {
  font-size: 15px;
  font-weight: 600;
  color: #1e293b;
  margin-top: 14px;
}

.state-desc {
  font-size: 13px;
  color: #64748b;
  margin-top: 6px;
  max-width: 480px;
  line-height: 1.6;
}

.scanned-items-view {
  display: flex;
  flex-direction: column;
  padding: 12px;
  gap: 10px;
}

.scan-summary-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 13px;
  color: #334155;
  padding: 2px 4px;
}

.summary-left {
  display: flex;
  align-items: center;
  gap: 10px;
}

.count-badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: 4px;
  font-weight: 500;
}

.archive-badge {
  background: #e0f2fe;
  color: #0369a1;
}

.dir-badge {
  background: #dcfce7;
  color: #15803d;
}

.selected-tip {
  font-size: 13px;
  color: #475569;
}

.scan-table {
  border-radius: 6px;
  overflow: hidden;
  background: #fff;
}

.doc-title-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.doc-name-text {
  font-weight: 600;
  color: #0284c7;
  line-height: 1.4;
}

.doc-meta-sub {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.meta-tag {
  font-size: 11px;
  font-family: monospace;
  background: #f1f5f9;
  color: #475569;
  padding: 1px 5px;
  border-radius: 3px;
}

.product-tag {
  background: #f0fdf4;
  color: #166534;
}

.ver-tag {
  background: #fef3c7;
  color: #92400e;
}

.path-cell-text {
  color: #334155;
  font-family: monospace;
  font-size: 12px;
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
