<template>
  <div class="knowledge-page">
    <el-tabs v-model="activeTab" class="kb-tabs" @tab-change="onTabChange">
      <!-- ============ 知识检索 ============ -->
      <el-tab-pane label="知识检索" name="search">
        <el-card shadow="never" class="search-card">
          <el-form :inline="true" class="search-form">
            <el-form-item label="关键词">
              <el-input
                v-model="query.keyword"
                placeholder="日志简名、Trap OID、原因或排错关键词..."
                clearable
                class="keyword-input"
                @keyup.enter="handleSearch"
              />
            </el-form-item>
            <el-form-item label="模块">
              <el-input v-model="query.module" placeholder="BGP / AAA" clearable class="module-input" />
            </el-form-item>
            <el-form-item label="类型">
              <el-select v-model="query.entryType" clearable placeholder="全部" class="small-select">
                <el-option label="Syslog 日志" value="LOG" />
                <el-option label="SNMP 告警" value="ALARM" />
              </el-select>
            </el-form-item>
            <el-form-item label="产品">
              <el-select v-model="query.product" clearable filterable placeholder="全部" class="mid-select">
                <el-option v-for="f in facets.products" :key="f.value" :label="`${f.value} (${f.count})`" :value="f.value" />
              </el-select>
            </el-form-item>
            <el-form-item label="版本">
              <el-select v-model="query.version" clearable filterable placeholder="全部" class="mid-select">
                <el-option v-for="f in facets.versions" :key="f.value" :label="`${f.value} (${f.count})`" :value="f.value" />
              </el-select>
            </el-form-item>
            <el-form-item label="级别">
              <el-select v-model="query.severity" clearable placeholder="全部" class="small-select">
                <el-option v-for="sev in 8" :key="sev" :label="`Lv.${sev}`" :value="sev" />
              </el-select>
            </el-form-item>
            <el-form-item label="排序">
              <el-select v-model="query.sort" class="small-select" @change="handleSearch">
                <el-option label="相关度" value="relevance" />
                <el-option label="严重度" value="severity" />
              </el-select>
            </el-form-item>
            <el-form-item>
              <el-radio-group v-model="query.view" size="small" @change="handleViewChange">
                <el-radio-button value="all">全部</el-radio-button>
                <el-radio-button value="favorites">收藏</el-radio-button>
                <el-radio-button value="recent">最近</el-radio-button>
              </el-radio-group>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" icon="Search" @click="handleSearch">检索</el-button>
              <el-button @click="resetSearch">重置</el-button>
            </el-form-item>
          </el-form>
        </el-card>

        <div class="results-container" v-loading="loading">
          <div v-if="total > 0" class="results-header">
            <span>{{ resultTitle }}（共 {{ total }} 条）</span>
            <span class="results-hint">本地知识标注“本地”，非官方原文</span>
          </div>

          <el-result v-if="searchError && !loading" icon="error" title="知识库检索失败" :sub-title="searchError" class="state-block">
            <template #extra>
              <el-button type="primary" @click="fetchKnowledge">重试</el-button>
              <el-button @click="resetSearch">清空条件</el-button>
            </template>
          </el-result>

          <div v-else class="knowledge-grid">
            <el-card
              v-for="k in knowledgeList"
              :key="k.knowledge_id || k.id"
              shadow="hover"
              class="kb-card"
              role="button"
              tabindex="0"
              @click="openDetail(k)"
              @keydown.enter.space.prevent="openDetail(k)"
            >
              <div class="kb-card-top">
                <el-tag size="small" :type="k.entry_type === 'ALARM' ? 'warning' : 'primary'">{{ k.entry_type === 'ALARM' ? '告警' : '日志' }}</el-tag>
                <el-tag v-if="k.source === 'local'" size="small" type="danger" effect="plain">本地</el-tag>
                <span class="kb-mod">{{ k.module }} / {{ k.brief }}</span>
                <el-button
                  :icon="isFavorite(k) ? StarFilled : Star"
                  circle
                  size="small"
                  text
                  class="fav-btn"
                  @click.stop="toggleFavorite(k)"
                />
              </div>
              <div class="kb-card-desc" v-html="highlight(k.description || k.message)"></div>
              <div v-if="k.cause" class="kb-card-cause"><strong>可能原因:</strong> {{ k.cause }}</div>
              <div class="kb-card-action"><strong>处理建议:</strong> {{ k.action }}</div>
              <div class="kb-card-footer">
                <SeverityTag :severity="k.severity" />
                <span class="kb-updated">{{ formatTime(k.updated_at) }}</span>
              </div>
            </el-card>
          </div>

          <el-empty
            v-if="!loading && !searchError && knowledgeList.length === 0"
            :description="emptyDescription"
            class="state-block"
          >
            <div class="empty-actions">
              <el-button v-if="query.view !== 'all'" @click="switchToAll">查看全部知识</el-button>
              <el-button @click="$router.push('/documents')">去导入 HDX 文档</el-button>
            </div>
            <HdxHelpCard class="empty-hdx" @import-doc="$router.push('/documents')" />
          </el-empty>

          <div v-if="query.view === 'all' && total > 0" class="pagination-bar">
            <el-pagination
              v-model:current-page="query.page"
              :page-size="query.pageSize"
              :total="total"
              layout="total, prev, pager, next"
              @current-change="fetchKnowledge"
            />
          </div>
        </div>
      </el-tab-pane>

      <!-- ============ 未匹配热点 ============ -->
      <el-tab-pane label="未匹配热点" name="hotspots">
        <el-card shadow="never" class="section-card" v-loading="hotspotsLoading">
          <template #header>
            <div class="section-header">
              <div class="title">
                <el-icon><Warning /></el-icon>
                <span>跨任务未匹配热点</span>
                <el-tag size="small" type="info">基于最近 {{ hotspotDigestCount }} 个案例</el-tag>
              </div>
              <el-button size="small" :icon="Refresh" @click="fetchHotspots">刷新</el-button>
            </div>
          </template>

          <el-table v-if="hotspots.length" :data="hotspots" size="small">
            <el-table-column label="模块 / 助记符" min-width="240">
              <template #default="{ row }">
                <strong>{{ row.module }}</strong>/{{ row.brief }}
              </template>
            </el-table-column>
            <el-table-column prop="count" label="出现次数" width="110" align="right" />
            <el-table-column prop="task_count" label="涉及案例" width="110" align="right" />
            <el-table-column label="最近出现" width="180">
              <template #default="{ row }">{{ formatTime(row.last_seen) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="200" align="center">
              <template #default="{ row }">
                <el-button link type="primary" size="small" @click="openHotspotTask(row)">
                  查看案例日志
                </el-button>
                <el-button link type="success" size="small" @click="openLocalFor(row)">
                  补本地知识
                </el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-else-if="!hotspotsLoading" description="暂无跨任务未匹配热点（先完成几次日志导入）" :image-size="80" />

          <div class="hotspot-footer">
            <HdxHelpCard compact @import-doc="$router.push('/documents')" />
          </div>
        </el-card>
      </el-tab-pane>

      <!-- ============ 本地知识 ============ -->
      <el-tab-pane label="本地知识" name="local">
        <el-card shadow="never" class="section-card">
          <template #header>
            <div class="section-header">
              <div class="title">
                <el-icon><EditPen /></el-icon>
                <span>本地知识（非官方）</span>
                <el-tag size="small" type="danger" effect="plain">LOCAL_EXACT · 0.95</el-tag>
              </div>
              <div class="header-actions">
                <el-input
                  v-model="localKeyword"
                  size="small"
                  placeholder="搜索模块/助记符/含义"
                  clearable
                  class="local-search"
                  @keyup.enter="fetchLocal"
                  @clear="fetchLocal"
                />
                <el-button size="small" :icon="Refresh" @click="fetchLocal">刷新</el-button>
                <el-button size="small" type="primary" :icon="Plus" @click="openCreateLocal">新建</el-button>
                <el-button size="small" @click="showImportCSV = true">导入 CSV</el-button>
                <el-button size="small" @click="api.exportLocalKnowledgeCSV()">导出 CSV</el-button>
              </div>
            </div>
          </template>

          <el-table v-if="localItems.length" :data="localItems" size="small">
            <el-table-column label="模块 / 助记符" min-width="200">
              <template #default="{ row }">
                <strong>{{ row.module }}</strong>/{{ row.brief }}
              </template>
            </el-table-column>
            <el-table-column label="产品 / 版本" width="180">
              <template #default="{ row }">
                {{ row.versions?.[0]?.product_type || '通用' }}
                <span v-if="row.versions?.[0]?.product_version"> / {{ row.versions[0].product_version }}</span>
              </template>
            </el-table-column>
            <el-table-column label="级别" width="130">
              <template #default="{ row }"><SeverityTag :severity="row.severity" /></template>
            </el-table-column>
            <el-table-column label="含义" min-width="260" show-overflow-tooltip>
              <template #default="{ row }">{{ row.description }}</template>
            </el-table-column>
            <el-table-column label="更新时间" width="170">
              <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
            </el-table-column>
            <el-table-column label="操作" width="150" align="center">
              <template #default="{ row }">
                <el-button link type="primary" size="small" @click="openEditLocal(row)">编辑</el-button>
                <el-button link type="danger" size="small" @click="removeLocal(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="还没有本地知识。可在未匹配日志详情中一键补充。" :image-size="80">
            <el-button type="primary" @click="openCreateLocal">新建本地知识</el-button>
          </el-empty>
        </el-card>

        <el-card shadow="never" class="section-card">
          <template #header>
            <div class="section-header">
              <div class="title">
                <el-icon><Link /></el-icon>
                <span>匹配覆盖（LOCAL_MAPPING · 0.90）</span>
              </div>
              <el-button size="small" :icon="Refresh" @click="fetchOverrides">刷新</el-button>
            </div>
          </template>
          <p class="section-desc">
            把某个未匹配的模块/助记符绑定到已有官方知识，下次解析时直接使用该知识。只对完全相同的 module+brief 生效。
          </p>
          <el-table v-if="overrides.length" :data="overrides" size="small">
            <el-table-column label="来源 module/brief" min-width="220">
              <template #default="{ row }"><strong>{{ row.module }}</strong>/{{ row.brief }}</template>
            </el-table-column>
            <el-table-column label="目标官方知识" min-width="260" show-overflow-tooltip>
              <template #default="{ row }">{{ row.target_module }}/{{ row.target_brief }} · {{ row.target_description }}</template>
            </el-table-column>
            <el-table-column label="启用" width="90" align="center">
              <template #default="{ row }">
                <el-switch :model-value="row.enabled" size="small" @update:model-value="(val) => toggleOverride(row, val)" />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="100" align="center">
              <template #default="{ row }">
                <el-button link type="danger" size="small" @click="removeOverride(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="还没有匹配覆盖" :image-size="70" />
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <!-- 知识详情抽屉 -->
    <el-drawer v-model="showDrawer" title="知识详情" size="50%">
      <div v-if="selectedKnowledge" class="drawer-content">
        <div class="drawer-header">
          <div class="drawer-title-row">
            <h2>{{ selectedKnowledge.module }} / {{ selectedKnowledge.brief }}</h2>
            <el-tag v-if="selectedKnowledge.source === 'local'" type="danger" effect="plain">本地知识</el-tag>
            <el-button
              :icon="favoriteIds.has(selectedKnowledge.id) ? StarFilled : Star"
              circle
              text
              @click="toggleFavorite(selectedKnowledge)"
            />
          </div>
          <div class="drawer-tags">
            <el-tag>{{ selectedKnowledge.entry_type === 'ALARM' ? '告警' : '日志' }}</el-tag>
            <SeverityTag :severity="selectedKnowledge.severity" />
            <el-tag v-if="selectedKnowledge.trap_oid" type="warning">OID: {{ selectedKnowledge.trap_oid }}</el-tag>
            <el-tag v-if="selectedKnowledge.mib_name" type="info">MIB: {{ selectedKnowledge.mib_name }}</el-tag>
          </div>
        </div>

        <div class="detail-section">
          <h3><el-icon><Tickets /></el-icon> 模板与解释</h3>
          <div class="detail-box">{{ selectedKnowledge.message }}</div>
          <div v-if="selectedKnowledge.description" class="detail-text">{{ selectedKnowledge.description }}</div>
        </div>
        <div v-if="selectedKnowledge.impact" class="detail-section">
          <h3><el-icon><Warning /></el-icon> 系统影响</h3>
          <div class="detail-box impact-box">{{ selectedKnowledge.impact }}</div>
        </div>
        <div class="detail-section">
          <h3><el-icon><Search /></el-icon> 官方可能原因</h3>
          <div class="detail-box cause-box">{{ selectedKnowledge.cause || '暂无详细原因记录' }}</div>
        </div>
        <div class="detail-section">
          <h3><el-icon><Tools /></el-icon> 参考排查建议（非官方原文）</h3>
          <div class="detail-box action-box">{{ selectedKnowledge.action || '按标准网络排错规范处理' }}</div>
        </div>
        <div v-if="selectedKnowledge.reference_url" class="detail-section">
          <h3><el-icon><Link /></el-icon> 参考链接</h3>
          <a :href="selectedKnowledge.reference_url" target="_blank" rel="noopener">{{ selectedKnowledge.reference_url }}</a>
        </div>
        <div v-if="selectedKnowledge.versions && selectedKnowledge.versions.length" class="detail-section">
          <h3><el-icon><Files /></el-icon> 适用产品与版本（{{ selectedKnowledge.versions.length }}）</h3>
          <el-table :data="selectedKnowledge.versions" size="small" border>
            <el-table-column prop="product_type" label="产品系列" />
            <el-table-column prop="product_version" label="软件版本" />
            <el-table-column prop="topic_id" label="Topic ID" />
          </el-table>
        </div>
      </div>
    </el-drawer>

    <LocalKnowledgeDialog
      v-model="showLocalDialog"
      :initial="localInitial"
      :editing="localEditing"
      @saved="onLocalSaved"
    />

    <BindKnowledgeDialog
      v-model="showBindDialog"
      :module="bindTarget.module"
      :brief="bindTarget.brief"
      @saved="onOverrideSaved"
    />

    <el-dialog v-model="showImportCSV" title="导入本地知识 CSV" width="640px">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="列顺序"
        description="module,brief,product,version,severity,entry_type,message,description,cause,action,impact,reference"
        class="csv-tip"
      />
      <el-input v-model="csvContent" type="textarea" :rows="12" placeholder="粘贴 CSV 内容..." />
      <template #footer>
        <el-button @click="showImportCSV = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="importCSV">开始导入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Tickets, Warning, Search, Tools, Files, EditPen, Link, Plus, Refresh, Star, StarFilled
} from '@element-plus/icons-vue'
import api from '@/api'
import SeverityTag from '@/components/SeverityTag.vue'
import HdxHelpCard from '@/components/HdxHelpCard.vue'
import LocalKnowledgeDialog from '@/components/LocalKnowledgeDialog.vue'
import BindKnowledgeDialog from '@/components/BindKnowledgeDialog.vue'
import { formatTime } from '@/utils/format'

const route = useRoute()
const router = useRouter()

const initialTab = typeof route.query.tab === 'string' && ['search', 'hotspots', 'local'].includes(route.query.tab)
  ? route.query.tab
  : 'search'
const activeTab = ref(initialTab)
const loading = ref(false)
const hotspotsLoading = ref(false)
const searchError = ref('')
const knowledgeList = ref([])
const total = ref(0)
const showDrawer = ref(false)
const selectedKnowledge = ref(null)
const favoriteIds = ref(new Set())

const facets = ref({ products: [], versions: [], severities: [], entry_types: [] })

const query = ref({
  keyword: typeof route.query.keyword === 'string' ? route.query.keyword : '',
  module: '',
  entryType: '',
  product: '',
  version: '',
  severity: null,
  sort: 'relevance',
  view: 'all',
  page: 1,
  pageSize: 18
})

const localItems = ref([])
const localKeyword = ref('')
const showLocalDialog = ref(false)
const localEditing = ref(false)
const localInitial = ref({})
const overrides = ref([])
const showBindDialog = ref(false)
const bindTarget = ref({ module: '', brief: '' })
const showImportCSV = ref(false)
const csvContent = ref('')
const importing = ref(false)

const hotspots = ref([])
const hotspotDigestCount = ref(0)

const resultTitle = computed(() => {
  if (query.value.view === 'favorites') return '收藏的知识'
  if (query.value.view === 'recent') return '最近浏览'
  return '检索结果'
})

const emptyDescription = computed(() => {
  if (query.value.view === 'favorites') return '还没有收藏知识'
  if (query.value.view === 'recent') return '还没有最近浏览记录'
  if (query.value.keyword || query.value.module) return '未检索到匹配的故障知识'
  return '知识库还没有可供检索的内容'
})

const isFavorite = (item) => favoriteIds.value.has(item.knowledge_id || item.id)

const escapeHtml = (text) => String(text || '')
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')

const highlight = (text) => {
  const escaped = escapeHtml(text)
  const keyword = query.value.keyword.trim()
  if (!keyword) return escaped
  const safeKeyword = escapeHtml(keyword).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return escaped.replace(new RegExp(safeKeyword, 'gi'), (match) => `<mark>${match}</mark>`)
}

const fetchFacets = async () => {
  try {
    const res = await api.getKnowledgeFacets({ module: query.value.module, entry_type: query.value.entryType })
    if (res.code === 0) {
      facets.value = res.data || { products: [], versions: [], severities: [], entry_types: [] }
    }
  } catch (e) {
    // 分面失败不影响检索
  }
}

const fetchFavorites = async () => {
  const res = await api.getKnowledgeFavorites()
  if (res.code === 0) {
    favoriteIds.value = new Set((res.data || []).map((item) => item.id))
  }
}

const fetchKnowledge = async () => {
  loading.value = true
  searchError.value = ''
  try {
    if (query.value.view === 'favorites') {
      const res = await api.getKnowledgeFavorites()
      knowledgeList.value = res.data || []
      total.value = knowledgeList.value.length
      return
    }
    if (query.value.view === 'recent') {
      const res = await api.getKnowledgeRecent()
      knowledgeList.value = res.data || []
      total.value = knowledgeList.value.length
      return
    }
    const res = await api.searchKnowledge({
      keyword: query.value.keyword,
      module: query.value.module,
      entry_type: query.value.entryType,
      product: query.value.product,
      version: query.value.version,
      severity: query.value.severity || undefined,
      sort: query.value.sort,
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
    console.error('Fetch knowledge failed:', e)
    knowledgeList.value = []
    total.value = 0
    searchError.value = e?.message || '检索失败，请稍后重试'
  } finally {
    loading.value = false
  }
}

const handleSearch = () => {
  query.value.view = 'all'
  query.value.page = 1
  fetchKnowledge()
  fetchFacets()
}

const handleViewChange = () => {
  query.value.page = 1
  fetchKnowledge()
}

const switchToAll = () => {
  query.value.view = 'all'
  handleViewChange()
}

const resetSearch = () => {
  query.value = {
    keyword: '',
    module: '',
    entryType: '',
    product: '',
    version: '',
    severity: null,
    sort: 'relevance',
    view: 'all',
    page: 1,
    pageSize: 18
  }
  fetchKnowledge()
  fetchFacets()
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
    ElMessage.error('知识详情加载失败：' + (e?.message || '未知错误'))
  }
}

const toggleFavorite = async (item) => {
  const id = item.knowledge_id || item.id || item.knowledge_id
  if (!id) return
  try {
    if (favoriteIds.value.has(id)) {
      await api.unfavoriteKnowledge(id)
      favoriteIds.value.delete(id)
      ElMessage.success('已取消收藏')
    } else {
      await api.favoriteKnowledge(id)
      favoriteIds.value.add(id)
      ElMessage.success('已收藏')
    }
    if (query.value.view === 'favorites') await fetchKnowledge()
  } catch (e) {
    // 拦截器已提示
  }
}

// ---------- 本地知识 ----------
const fetchLocal = async () => {
  const res = await api.listLocalKnowledge(localKeyword.value)
  if (res.code === 0) localItems.value = res.data || []
}

const openCreateLocal = () => {
  localEditing.value = false
  localInitial.value = {}
  showLocalDialog.value = true
}

const openEditLocal = (row) => {
  localEditing.value = true
  localInitial.value = { ...row }
  showLocalDialog.value = true
}

const openLocalFor = (row) => {
  localEditing.value = false
  localInitial.value = { module: row.module, brief: row.brief }
  showLocalDialog.value = true
}

const onLocalSaved = async () => {
  await fetchLocal()
  handleSearch()
}

const removeLocal = async (row) => {
  try {
    await ElMessageBox.confirm(`删除本地知识 ${row.module}/${row.brief}？删除后匹配立即失效。`, '确认删除', { type: 'warning' })
  } catch (e) {
    return
  }
  const res = await api.deleteLocalKnowledge(row.id)
  if (res.code === 0) {
    ElMessage.success('已删除')
    await fetchLocal()
    handleSearch()
  }
}

const importCSV = async () => {
  if (!csvContent.value.trim()) {
    ElMessage.warning('请粘贴 CSV 内容')
    return
  }
  importing.value = true
  try {
    const res = await api.importLocalKnowledgeCSV(csvContent.value)
    if (res.code === 0) {
      const data = res.data || {}
      ElMessage.success(`导入完成：新增 ${data.created || 0}，更新 ${data.updated || 0}，失败 ${data.failed || 0}`)
      if (data.errors?.length) {
        ElMessage.warning(data.errors.slice(0, 3).join('；'))
      }
      showImportCSV.value = false
      csvContent.value = ''
      await fetchLocal()
    }
  } finally {
    importing.value = false
  }
}

// ---------- 覆盖 ----------
const fetchOverrides = async () => {
  const res = await api.listKnowledgeOverrides()
  if (res.code === 0) overrides.value = res.data || []
}

const toggleOverride = async (row, enabled) => {
  const res = await api.updateKnowledgeOverride(row.id, { enabled })
  if (res.code === 0) {
    row.enabled = enabled
    ElMessage.success(enabled ? '已启用' : '已禁用')
  }
}

const removeOverride = async (row) => {
  try {
    await ElMessageBox.confirm(`删除 ${row.module}/${row.brief} 的匹配覆盖？`, '确认删除', { type: 'warning' })
  } catch (e) {
    return
  }
  const res = await api.deleteKnowledgeOverride(row.id)
  if (res.code === 0) {
    ElMessage.success('已删除')
    fetchOverrides()
  }
}

const onOverrideSaved = () => {
  fetchOverrides()
  handleSearch()
}

const openBind = (module = '', brief = '') => {
  bindTarget.value = { module, brief }
  showBindDialog.value = true
}

// ---------- 热点 ----------
const fetchHotspots = async () => {
  hotspotsLoading.value = true
  try {
    const res = await api.getGlobalUnmatchedHotspots(20)
    if (res.code === 0) {
      hotspots.value = res.data?.items || []
      hotspotDigestCount.value = res.data?.digest_count || 0
    }
  } finally {
    hotspotsLoading.value = false
  }
}

const openHotspotTask = (row) => {
  const taskId = row.task_ids?.[0]
  if (!taskId) return
  router.push({ path: `/audit/${taskId}`, query: { module: row.module, matched: 'false' } })
}

const onTabChange = (name) => {
  if (name === 'local') {
    fetchLocal()
    fetchOverrides()
  } else if (name === 'hotspots') {
    fetchHotspots()
  }
}

onMounted(async () => {
  await Promise.all([fetchFacets(), fetchFavorites(), fetchKnowledge()])
  if (activeTab.value !== 'search') {
    onTabChange(activeTab.value)
  }
})
</script>

<style scoped>
.knowledge-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.kb-tabs {
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  padding: 8px 16px 16px;
  border: 1px solid var(--border-base);
}

.search-card,
.section-card {
  border-radius: var(--radius-lg);
}

.search-form :deep(.el-form-item) {
  margin-bottom: 8px;
}

.keyword-input { width: 260px; }
.module-input { width: 130px; }
.small-select { width: 120px; }
.mid-select { width: 170px; }

.results-container {
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  padding: 16px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.05);
  margin-top: 12px;
}

.results-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: var(--fs-lg);
  font-weight: 600;
  color: var(--gray-600);
  margin-bottom: 12px;
}

.results-hint {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  font-weight: 400;
}

.knowledge-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.kb-card {
  border-radius: var(--radius-md);
  cursor: pointer;
  border: 1px solid var(--border-base);
  transition: all 0.2s ease;
}

.kb-card:hover {
  border-color: var(--brand-400);
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
  font-size: var(--fs-lg);
  color: var(--gray-900);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.fav-btn {
  color: var(--warning-strong);
}

.kb-card-desc {
  font-size: var(--fs-sm);
  color: var(--gray-700);
  margin-bottom: 6px;
  line-height: 1.5;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
}

.kb-card-desc :deep(mark) {
  background: var(--warning-bg, #fdf6ec);
  color: var(--warning-text, #b88230);
  padding: 0 2px;
}

.kb-card-cause,
.kb-card-action {
  font-size: var(--fs-xs);
  color: var(--gray-500);
  margin-top: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.kb-card-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 10px;
}

.kb-updated {
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.state-block {
  padding: 24px 0;
}

.empty-actions {
  display: flex;
  justify-content: center;
  gap: 10px;
  margin-bottom: 14px;
}

.empty-hdx {
  max-width: 620px;
  margin: 0 auto;
  text-align: left;
}

.pagination-bar {
  margin-top: 16px;
  display: flex;
  justify-content: flex-end;
}

.section-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.section-header .title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.local-search {
  width: 220px;
}

.section-desc {
  margin: 0 0 12px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.hotspot-footer {
  margin-top: 14px;
}

.csv-tip {
  margin-bottom: 10px;
}

.drawer-content {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.drawer-title-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.drawer-title-row h2 {
  margin: 0;
  font-size: var(--fs-xl);
}

.drawer-tags {
  margin-top: 8px;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.detail-section h3 {
  font-size: var(--fs-lg);
  color: var(--gray-700);
  margin-bottom: 8px;
}

.detail-box {
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  font-size: var(--fs-md);
  line-height: 1.6;
  color: var(--gray-800);
}

.cause-box {
  background: var(--warning-bg);
  border-color: var(--warning-border);
  color: var(--warning-text);
}

.action-box {
  background: var(--success-bg);
  border-color: var(--success-border);
  color: var(--action-text);
}

.impact-box {
  background: var(--danger-bg);
  border-color: var(--danger-border);
  color: var(--danger-text);
}
</style>
