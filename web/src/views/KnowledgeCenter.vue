<template>
  <div class="knowledge-page">
    <el-card shadow="never" class="search-card">
      <el-form :inline="true" class="search-form">
        <el-form-item label="关键词检索">
          <el-input
            v-model="query.keyword"
            placeholder="输入日志简名、Trap OID、原因或排错关键词..."
            clearable
            style="width: 320px;"
            @keyup.enter="handleSearch"
          />
        </el-form-item>
        <el-form-item label="模块过滤">
          <el-input v-model="query.module" placeholder="例如: BGP, AAA, IFNET" clearable style="width: 160px;" />
        </el-form-item>
        <el-form-item label="知识类型">
          <el-select v-model="query.entryType" clearable placeholder="全部类型" style="width: 140px;">
            <el-option label="Syslog 日志参考" value="LOG" />
            <el-option label="SNMP Trap 告警" value="ALARM" />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" icon="Search" @click="handleSearch">检索知识库</el-button>
          <el-button @click="resetSearch">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <!-- 知识列表 -->
    <div class="results-container" v-loading="loading">
      <div class="results-header">
        <span>检索结果 (共 {{ total }} 条)</span>
      </div>

      <div class="knowledge-grid">
        <el-card
          v-for="k in (knowledgeList || [])"
          :key="k.knowledge_id || k.id"
          shadow="hover"
          class="kb-card"
          @click="openDetail(k)"
        >
          <div class="kb-card-top">
            <el-tag size="small" :type="k.entry_type === 'ALARM' ? 'warning' : 'primary'">{{ k.entry_type }}</el-tag>
            <span class="kb-mod">{{ k.module }} / {{ k.brief }}</span>
            <el-tag size="small" type="danger" effect="plain">Level {{ k.severity }}</el-tag>
          </div>
          <div class="kb-card-desc">{{ k.description || k.message }}</div>
          <div v-if="k.cause" class="kb-card-cause"><strong>可能原因:</strong> {{ k.cause }}</div>
          <div class="kb-card-action"><strong>处理步骤:</strong> {{ k.action }}</div>
        </el-card>
      </div>

      <el-empty v-if="!loading && (!knowledgeList || knowledgeList.length === 0)" description="未检索到匹配的故障知识" />

      <div class="pagination-bar">
        <el-pagination
          v-model:current-page="query.page"
          :page-size="query.pageSize"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="fetchKnowledge"
        />
      </div>
    </div>

    <!-- 知识详情抽屉 -->
    <el-drawer v-model="showDrawer" title="官方故障知识详情" size="50%">
      <div v-if="selectedKnowledge" class="drawer-content">
        <div class="drawer-header">
          <h2>{{ selectedKnowledge.module }} / {{ selectedKnowledge.brief }}</h2>
          <div class="drawer-tags">
            <el-tag>{{ selectedKnowledge.entry_type }}</el-tag>
            <el-tag type="danger">Severity: {{ selectedKnowledge.severity }}</el-tag>
            <el-tag v-if="selectedKnowledge.trap_oid" type="warning">OID: {{ selectedKnowledge.trap_oid }}</el-tag>
            <el-tag v-if="selectedKnowledge.mib_name" type="info">MIB: {{ selectedKnowledge.mib_name }}</el-tag>
          </div>
        </div>

        <div class="detail-section">
          <h3>📋 模板与解释</h3>
          <div class="detail-box">{{ selectedKnowledge.message }}</div>
          <div v-if="selectedKnowledge.description" class="detail-text">{{ selectedKnowledge.description }}</div>
        </div>

        <div v-if="selectedKnowledge.impact" class="detail-section">
          <h3>⚠️ 系统影响</h3>
          <div class="detail-box impact-box">{{ selectedKnowledge.impact }}</div>
        </div>

        <div class="detail-section">
          <h3>🔍 官方可能原因</h3>
          <div class="detail-box cause-box">{{ selectedKnowledge.cause || '暂无详细原因记录' }}</div>
        </div>

        <div class="detail-section">
          <h3>🛠️ 官方处理排错步骤</h3>
          <div class="detail-box action-box">{{ selectedKnowledge.action || '按标准网络排错规范处理' }}</div>
        </div>

        <div v-if="selectedKnowledge.versions && selectedKnowledge.versions.length" class="detail-section">
          <h3>📦 适用产品与版本映射 ({{ selectedKnowledge.versions.length }} 个版本)</h3>
          <el-table :data="selectedKnowledge.versions" size="small" border>
            <el-table-column prop="product_type" label="产品系列" />
            <el-table-column prop="product_version" label="软件版本" />
            <el-table-column prop="topic_id" label="Topic ID" />
          </el-table>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'

const loading = ref(false)
const knowledgeList = ref([])
const total = ref(0)
const showDrawer = ref(false)
const selectedKnowledge = ref(null)
// UI-15: 检索失败原因，用于在空态之上叠加可重试的错误态
const searchError = ref('')

const query = ref({
  keyword: '',
  module: '',
  entryType: '',
  page: 1,
  pageSize: 18
})

const handleSearch = () => {
  query.value.page = 1
  fetchKnowledge()
}

const resetSearch = () => {
  query.value.keyword = ''
  query.value.module = ''
  query.value.entryType = ''
  query.value.page = 1
  fetchKnowledge()
}

const fetchKnowledge = async () => {
  loading.value = true
  searchError.value = ''
  try {
    const res = await api.searchKnowledge({
      keyword: query.value.keyword,
      module: query.value.module,
      entry_type: query.value.entryType,
      page: query.value.page,
      page_size: query.value.pageSize
    })
    if (res.code === 0 && res.data) {
      knowledgeList.value = res.data.hits || []
      total.value = res.data.total || 0
    } else {
      knowledgeList.value = []
      total.value = 0
    }
  } catch (e) {
    // UI-15: 原实现 `catch (e) {}` 完全静默——检索失败与"确实没有匹配知识"
    // 在界面上长得一模一样，用户无法判断是该换关键词还是该找运维。
    console.error('Fetch knowledge failed:', e)
    knowledgeList.value = []
    total.value = 0
    searchError.value = e?.message || '检索失败，请稍后重试'
    ElMessage.error(searchError.value)
  } finally {
    loading.value = false
  }
}

const openDetail = async (k) => {
  const kid = k.knowledge_id || k.id
  try {
    const res = await api.getKnowledgeDetail(kid)
    if (res.code === 0) {
      selectedKnowledge.value = res.data
      showDrawer.value = true
    }
  } catch (e) {
    // UI-15: 详情拉取失败同样不再静默吞掉
    ElMessage.error('知识详情加载失败：' + (e?.message || '未知错误'))
  }
}

onMounted(() => {
  fetchKnowledge()
})
</script>

<style scoped>
.knowledge-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.search-card {
  border-radius: 8px;
}
.search-form {
  margin-bottom: -18px;
}
.results-container {
  background: #fff;
  border-radius: 8px;
  padding: 16px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.05);
}
.results-header {
  font-size: 14px;
  font-weight: 600;
  color: #475569;
  margin-bottom: 12px;
}
.knowledge-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 16px;
}
.kb-card {
  border-radius: 6px;
  cursor: pointer;
  border: 1px solid #e2e8f0;
  transition: all 0.2s ease;
}
.kb-card:hover {
  border-color: #38bdf8;
  transform: translateY(-2px);
}
.kb-card-top {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.kb-mod {
  font-weight: 700;
  font-size: 14px;
  color: #0f172a;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.kb-card-desc {
  font-size: 12px;
  color: #334155;
  margin-bottom: 6px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.kb-card-cause, .kb-card-action {
  font-size: 11px;
  color: #64748b;
  margin-top: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.pagination-bar {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}

.drawer-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.drawer-tags {
  margin-top: 8px;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.detail-section h3 {
  font-size: 14px;
  color: #334155;
  margin-bottom: 8px;
}
.detail-box {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 10px 12px;
  font-size: 13px;
  line-height: 1.6;
  color: #1e293b;
}
.cause-box {
  background: #fffbeb;
  border-color: #fde68a;
  color: #92400e;
}
.action-box {
  background: #f0fdf4;
  border-color: #bbf7d0;
  color: #166534;
}
.impact-box {
  background: #fef2f2;
  border-color: #fecaca;
  color: #991b1b;
}
</style>
