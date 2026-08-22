<template>
  <div class="documents-page">
    <el-card shadow="never" class="doc-header-card">
      <div class="header-content">
        <div>
          <h2 style="font-size: 16px; margin-bottom: 4px;">华为官方产品文档知识库管理</h2>
          <p style="font-size: 12px; color: #64748b;">支持通过解压目录或 HDX 压缩包导入华为官方产品知识体系（自动递归抽取叶子日志/告警并完成跨版本去重）</p>
        </div>
        <div class="header-actions">
          <el-button type="primary" icon="FolderAdd" @click="showImportDirDialog = true">导入本地文档目录</el-button>
          <el-button type="success" icon="Upload" @click="showUploadZipDialog = true">上传 HDX 压缩包</el-button>
        </div>
      </div>
    </el-card>

    <!-- 文档表格 -->
    <el-card shadow="never" class="table-card">
      <el-table :data="docList" v-loading="loading" style="width: 100%;" border>
        <el-table-column prop="lib_id" label="LibID" width="120">
          <template #default="{ row }">
            <el-tag size="small" effect="plain">{{ row.lib_id }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="lib_name" label="产品文档全称" min-width="220" />
        <el-table-column prop="product_type" label="适用产品型号" width="200" />
        <el-table-column prop="product_version" label="软件版本" width="140" />
        <el-table-column prop="issue_date" label="发布日期" width="120" />
        <el-table-column prop="log_count" label="叶子日志数" width="120" align="center">
          <template #default="{ row }">
            <span style="color: #0284c7; font-weight: bold;">{{ row.log_count }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="alarm_count" label="叶子告警数" width="120" align="center">
          <template #default="{ row }">
            <span style="color: #ea580c; font-weight: bold;">{{ row.alarm_count }}</span>
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

    <!-- 导入本地目录弹窗 -->
    <el-dialog v-model="showImportDirDialog" title="从本地目录导入 HDX 文档" width="580px">
      <el-form label-position="top">
        <el-form-item label="服务器本地文档目录绝对路径">
          <el-input
            v-model="dirPathInput"
            placeholder="例如: d:\Document\Code\LogAuditorGo\原始产品文档"
            clearable
          />
          <div style="font-size: 12px; color: #94a3b8; margin-top: 6px; line-height: 1.5;">
            💡 提示：支持输入单个 HDX 解压目录，或包含多个文档包的父级归档目录。系统将自动递归发现并批量完成去重入库。
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showImportDirDialog = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="handleImportDir">开始导入</el-button>
      </template>
    </el-dialog>

    <!-- 上传 HDX 压缩包弹窗 -->
    <el-dialog v-model="showUploadZipDialog" title="上传 HDX ZIP 压缩包" width="500px">
      <el-upload
        drag
        action="/api/v1/documents/upload"
        :on-success="handleUploadSuccess"
        :on-error="handleUploadError"
        :show-file-list="true"
        accept=".zip,.hdx"
      >
        <el-icon class="el-icon--upload"><upload-filled /></el-icon>
        <div class="el-upload__text">将 HDX 压缩包拖到此处，或<em>点击上传</em></div>
      </el-upload>
    </el-dialog>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'

const loading = ref(false)
const docList = ref([])
const showImportDirDialog = ref(false)
const showUploadZipDialog = ref(false)
const dirPathInput = ref('')
const importing = ref(false)

const fetchDocs = async () => {
  loading.value = true
  try {
    const res = await api.getDocuments()
    if (res.code === 0) {
      docList.value = res.data
    }
  } finally {
    loading.value = false
  }
}

const handleImportDir = async () => {
  if (!dirPathInput.value.trim()) {
    ElMessage.warning('请输入目录路径')
    return
  }
  importing.value = true
  try {
    const res = await api.importDir(dirPathInput.value.trim())
    if (res.code === 0) {
      const docCount = res.data.total_documents || 1
      if (docCount > 1) {
        ElMessage.success(`批量导入成功！共导入 ${docCount} 个文档包，累计解析日志 ${res.data.leaf_log_count} 条，告警 ${res.data.leaf_alarm_count} 条，新增唯一知识 ${res.data.unique_knowledge_added} 条`)
      } else {
        ElMessage.success(`导入成功！解析日志 ${res.data.leaf_log_count} 条，告警 ${res.data.leaf_alarm_count} 条，新增知识 ${res.data.unique_knowledge_added} 条`)
      }
      showImportDirDialog.value = false
      dirPathInput.value = ''
      fetchDocs()
    }
  } finally {
    importing.value = false
  }
}

const handleUploadSuccess = (res) => {
  if (res.code === 0) {
    ElMessage.success('HDX 压缩包解压并导入成功')
    showUploadZipDialog.value = false
    fetchDocs()
  } else {
    ElMessage.error(res.error || '导入失败')
  }
}

const handleUploadError = () => {
  ElMessage.error('文件上传异常')
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
.header-actions {
  display: flex;
  gap: 10px;
}
.table-card {
  border-radius: 8px;
}
</style>
