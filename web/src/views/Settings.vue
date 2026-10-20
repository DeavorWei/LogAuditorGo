<template>
  <div class="settings-page">
    <el-tabs v-model="activeTab" class="settings-tabs">
      <!-- 常规：日志策略与文件 -->
      <el-tab-pane label="常规" name="general">
        <el-row :gutter="16" class="stats-row">
          <el-col :span="6">
            <StatCard :icon="Document" :value="formatSize(logStats.current_size || 0)" title="当前活跃日志 (log.log)" tone="brand" />
          </el-col>
          <el-col :span="6">
            <StatCard :icon="Files" :value="logStats.file_count || 0" unit="个" title="日志文件总数 (含转储归档)" tone="success" />
          </el-col>
          <el-col :span="6">
            <StatCard :icon="PieChart" :value="formatSize(logStats.total_size || 0)" :title="`总占用空间 / 配额 ${(logStats.max_size_mb || 1024) >= 1024 ? ((logStats.max_size_mb || 1024) / 1024).toFixed(1) + ' GB' : (logStats.max_size_mb || 1024) + ' MB'}`" tone="warning" />
          </el-col>
          <el-col :span="6">
            <StatCard :icon="Timer" :value="logStats.max_days || 180" unit="天" title="最大保留天数 (超期自动清理)" tone="purple" />
          </el-col>
        </el-row>

        <el-row :gutter="16" class="main-row">
          <el-col :span="10">
            <el-card shadow="hover" class="config-card">
              <template #header>
                <div class="card-header">
                  <span class="card-header-title"><el-icon><Setting /></el-icon> 日志存放与清理策略</span>
                </div>
              </template>

              <el-form label-position="top" :model="logForm">
                <el-form-item label="日志存放目录">
                  <el-input v-model="logForm.dir" disabled>
                    <template #prepend><el-icon><Folder /></el-icon></template>
                  </el-input>
                </el-form-item>

                <el-row :gutter="16">
                  <el-col :span="12">
                    <el-form-item label="最大保留总大小 (MB)">
                      <el-input-number v-model="logForm.max_size_mb" :min="10" :max="102400" :step="128" class="w-full" />
                      <div class="form-hint">约等于 {{ (logForm.max_size_mb / 1024).toFixed(2) }} GB</div>
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item label="最大保留天数 (天)">
                      <el-input-number v-model="logForm.max_days" :min="1" :max="3650" :step="30" class="w-full" />
                      <div class="form-hint">超过 {{ logForm.max_days }} 天的旧日志将被自动清理</div>
                    </el-form-item>
                  </el-col>
                </el-row>

                <el-row :gutter="16">
                  <el-col :span="12">
                    <el-form-item label="日志输出级别">
                      <el-select v-model="logForm.level" class="w-full">
                        <el-option label="DEBUG (详细调试)" value="debug" />
                        <el-option label="INFO (常规信息)" value="info" />
                        <el-option label="WARN (警告级别)" value="warn" />
                        <el-option label="ERROR (仅错误)" value="error" />
                      </el-select>
                    </el-form-item>
                  </el-col>
                  <el-col :span="12">
                    <el-form-item label="日志格式">
                      <el-select v-model="logForm.format" class="w-full">
                        <el-option label="Console (可读文本)" value="console" />
                        <el-option label="JSON (结构化日志)" value="json" />
                      </el-select>
                    </el-form-item>
                  </el-col>
                </el-row>

                <div class="form-actions">
                  <el-button type="primary" :loading="saving" @click="handleSaveConfig">
                    <el-icon><Check /></el-icon><span>保存配置并应用</span>
                  </el-button>
                  <el-button @click="fetchConfig" title="放弃未保存的修改，重新从服务端载入配置">
                    <el-icon><Refresh /></el-icon><span>重新载入</span>
                  </el-button>
                </div>
              </el-form>
            </el-card>
          </el-col>

          <el-col :span="14">
            <el-card shadow="hover" class="files-card">
              <template #header>
                <div class="card-header-flex">
                  <span class="card-header-title"><el-icon><Tickets /></el-icon> 日志文件清单与状态</span>
                  <div class="header-btns">
                    <el-button type="danger" plain size="small" :loading="cleaning" @click="handleCleanLogs">
                      <el-icon><Delete /></el-icon><span>立即执行清理</span>
                    </el-button>
                    <el-button size="small" :loading="loadingLogs" @click="fetchLogs">
                      <el-icon><Refresh /></el-icon><span>刷新</span>
                    </el-button>
                  </div>
                </div>
              </template>

              <el-table :data="logStats.files || []" v-loading="loadingLogs" stripe class="w-full" max-height="520">
                <el-table-column prop="name" label="日志文件名" min-width="240">
                  <template #default="{ row }">
                    <div class="file-name-cell">
                      <el-icon v-if="row.is_active" class="icon-active"><DocumentChecked /></el-icon>
                      <el-icon v-else class="icon-archive"><Document /></el-icon>
                      <span :class="{ 'active-file-text': row.is_active }">{{ row.name }}</span>
                    </div>
                  </template>
                </el-table-column>
                <el-table-column prop="is_active" label="状态" width="110">
                  <template #default="{ row }">
                    <el-tag v-if="row.is_active" type="success" size="small" effect="dark">当前写入</el-tag>
                    <el-tag v-else type="info" size="small">历史转储</el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="size" label="文件大小" width="120">
                  <template #default="{ row }"><span class="file-size-tag">{{ formatSize(row.size) }}</span></template>
                </el-table-column>
                <el-table-column prop="mod_time" label="最后修改时间" width="190">
                  <template #default="{ row }"><span class="time-text">{{ formatTime(row.mod_time) }}</span></template>
                </el-table-column>
              </el-table>
            </el-card>
          </el-col>
        </el-row>
      </el-tab-pane>

      <!-- 数据与存储 -->
      <el-tab-pane label="数据与存储" name="storage">
        <el-card shadow="never" class="section-card">
          <template #header>
            <div class="card-header-flex">
              <span class="card-header-title"><el-icon><Coin /></el-icon> 存储占用概览</span>
              <el-button size="small" :icon="Refresh" :loading="loadingStorage" @click="fetchStorage">刷新</el-button>
            </div>
          </template>
          <el-row :gutter="16" class="storage-counts">
            <el-col :span="8"><StatCard :icon="Reading" :value="storage.counts?.knowledge || 0" title="知识条目" tone="brand" /></el-col>
            <el-col :span="8"><StatCard :icon="FolderOpened" :value="storage.counts?.documents || 0" title="产品文档" tone="success" /></el-col>
            <el-col :span="8"><StatCard :icon="List" :value="storage.counts?.tasks || 0" title="排障案例" tone="warning" /></el-col>
          </el-row>
          <el-table :data="storage.paths || []" v-loading="loadingStorage" size="small" style="width: 100%; margin-top: 16px">
            <el-table-column prop="key" label="数据区" width="140">
              <template #default="{ row }">{{ storageKeyLabel(row.key) }}</template>
            </el-table-column>
            <el-table-column prop="path" label="路径" min-width="280" show-overflow-tooltip />
            <el-table-column label="占用" width="120" align="right">
              <template #default="{ row }">{{ formatSize(row.bytes) }}</template>
            </el-table-column>
            <el-table-column prop="files" label="文件数" width="90" align="right" />
          </el-table>
          <p class="section-tip">
            任务库按案例物理隔离存放；删除案例会同时删除对应数据库文件。日志占用可通过“常规”页的清理策略控制。
          </p>
        </el-card>
      </el-tab-pane>

      <!-- 知识库与索引 -->
      <el-tab-pane label="知识库与索引" name="index">
        <el-card shadow="never" class="section-card">
          <template #header>
            <div class="card-header-flex">
              <span class="card-header-title"><el-icon><Search /></el-icon> 索引健康状态</span>
              <div class="header-btns">
                <el-button size="small" :icon="Refresh" :loading="loadingIndex" @click="fetchIndexStatus">刷新</el-button>
                <el-button type="primary" size="small" :loading="rebuilding" @click="handleRebuildIndex">重建索引</el-button>
              </div>
            </div>
          </template>
          <el-descriptions :column="3" border v-loading="loadingIndex">
            <el-descriptions-item label="索引 mapping 版本">{{ indexStatus.mapping_version ?? '-' }}</el-descriptions-item>
            <el-descriptions-item label="映射是否过期">
              <el-tag :type="indexStatus.mapping_outdated ? 'danger' : 'success'" size="small">
                {{ indexStatus.mapping_outdated ? '需要重建' : '正常' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="索引文档数">{{ indexStatus.indexed_documents ?? 0 }}</el-descriptions-item>
          </el-descriptions>
          <el-alert
            v-if="indexStatus.mapping_outdated"
            type="warning"
            show-icon
            :closable="false"
            class="index-alert"
            title="索引结构与当前版本不一致"
            description="检索结果可能不准确，请点击“重建索引”。重建在后台执行，期间不影响已有检索。"
          />
          <p class="section-tip">
            当知识条目数量与索引文档数长期不一致，或检索召回明显异常时，可执行全量重建。重建会基于知识库数据库重新生成 Bleve 索引。
          </p>
        </el-card>
      </el-tab-pane>

      <!-- 实验室功能 -->
      <el-tab-pane label="实验室功能" name="lab">
        <el-card shadow="never" class="section-card lab-card">
          <template #header>
            <div class="card-header">
              <span class="card-header-title"><el-icon><MagicStick /></el-icon> 实验性能力</span>
            </div>
          </template>

          <div class="lab-item">
            <div class="lab-info">
              <div class="lab-title">启用关联事件（实验性）</div>
              <div class="lab-desc">
                基于 5 条内置协议故障传播规则，在 300s 窗口内聚合关联日志并展示传播拓扑。
                该能力<strong>默认关闭、不自动运行</strong>，开启后仅在工作台“高级”抽屉中提供手动分析入口。
              </div>
              <ul class="lab-limits">
                <li>只覆盖 5 类故障链，长尾场景识别不到；</li>
                <li>按设备隔离分析，不支持跨设备传播；</li>
                <li>“疑似根因/关联起点”和“参考排查建议”均为启发式推断，不是官方文档原文；</li>
                <li>产品决策：只保留、不演进，不进入任务 KPI 与主报告。</li>
              </ul>
            </div>
            <el-switch
              :model-value="featureStore.rcaEnabled"
              :loading="savingFeatures"
              active-text="已开启"
              inactive-text="已关闭"
              style="--el-switch-on-color: var(--warning-strong);"
              @update:model-value="handleToggleRca"
            />
          </div>
        </el-card>
      </el-tab-pane>

      <!-- 关于与诊断 -->
      <el-tab-pane label="关于与诊断" name="about">
        <el-card shadow="never" class="section-card">
          <template #header>
            <div class="card-header">
              <span class="card-header-title"><el-icon><InfoFilled /></el-icon> 版本与运行环境</span>
            </div>
          </template>
          <el-descriptions :column="2" border>
            <el-descriptions-item label="产品版本">{{ storage.version || '-' }}</el-descriptions-item>
            <el-descriptions-item label="配置文件">{{ storage.config_file || '-' }}</el-descriptions-item>
            <el-descriptions-item label="数据目录">{{ storage.paths?.find(p => p.key === 'data')?.path || '-' }}</el-descriptions-item>
            <el-descriptions-item label="任务库目录">{{ storage.paths?.find(p => p.key === 'tasks')?.path || '-' }}</el-descriptions-item>
            <el-descriptions-item label="索引目录">{{ storage.paths?.find(p => p.key === 'bleve_index')?.path || '-' }}</el-descriptions-item>
            <el-descriptions-item label="单实例保护">
              <el-tag size="small" type="success">已启用</el-tag>
              <span class="diag-note">同一数据目录重复启动时只会打开已运行实例</span>
            </el-descriptions-item>
          </el-descriptions>
          <p class="section-tip">
            本产品完全离线运行，不联网、不上传任何日志或知识内容。报告与导出文件包含原始日志，外发前请自行评估敏感性。
          </p>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <ImportProgressModal
      v-model="showReindexProgress"
      :job-id="reindexJobId"
      @completed="handleReindexCompleted"
      @failed="handleReindexFailed"
    />
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Document, Files, PieChart, Timer, Setting, Tickets, Coin, Reading, FolderOpened,
  List, Search, MagicStick, InfoFilled, Check, Refresh, Delete, DocumentChecked, Folder
} from '@element-plus/icons-vue'
import api from '@/api'
import StatCard from '@/components/StatCard.vue'
import ImportProgressModal from '@/components/ImportProgressModal.vue'
import { useFeatureStore } from '@/stores/features'
import { formatTime, formatSize } from '@/utils/format'

const route = useRoute()
const featureStore = useFeatureStore()

const validTabs = ['general', 'storage', 'index', 'lab', 'about']
const activeTab = ref(validTabs.includes(route.query.section) ? route.query.section : 'general')

const logStats = ref({
  dir: '', current_size: 0, total_size: 0, file_count: 0, max_size_mb: 1024, max_days: 180, files: []
})
const logForm = ref({ dir: 'LogAuditorGoData/log', max_size_mb: 1024, max_days: 180, level: 'debug', format: 'console' })

const storage = ref({ paths: [], counts: {}, version: '', config_file: '' })
const indexStatus = ref({ mapping_version: null, mapping_outdated: false, indexed_documents: 0 })

const saving = ref(false)
const cleaning = ref(false)
const loadingLogs = ref(false)
const loadingStorage = ref(false)
const loadingIndex = ref(false)
const rebuilding = ref(false)
const savingFeatures = ref(false)
const showReindexProgress = ref(false)
const reindexJobId = ref('')

const storageKeyLabel = (key) => ({
  data: '数据根目录',
  knowledge_db: '知识库数据库',
  bleve_index: '全文检索索引',
  tasks: '任务库目录',
  uploads: '临时文件',
  logs: '日志目录'
}[key] || key)

const fetchConfig = async () => {
  try {
    const res = await api.getSystemConfig()
    if (res.code === 0 && res.data) {
      if (res.data.config?.log) {
        logForm.value = {
          dir: res.data.config.log.dir || 'LogAuditorGoData/log',
          max_size_mb: res.data.config.log.max_size_mb || 1024,
          max_days: res.data.config.log.max_days || 180,
          level: res.data.config.log.level || 'debug',
          format: res.data.config.log.format || 'console'
        }
      }
      if (res.data.log_stats) logStats.value = res.data.log_stats
    }
  } catch (e) {
    // 错误由请求拦截器统一提示
  }
}

const fetchLogs = async () => {
  loadingLogs.value = true
  try {
    const res = await api.getSystemLogs()
    if (res.code === 0 && res.data) logStats.value = res.data
  } finally {
    loadingLogs.value = false
  }
}

const fetchStorage = async () => {
  loadingStorage.value = true
  try {
    const res = await api.getStorageOverview()
    if (res.code === 0 && res.data) storage.value = res.data
  } finally {
    loadingStorage.value = false
  }
}

const fetchIndexStatus = async () => {
  loadingIndex.value = true
  try {
    const res = await api.getKnowledgeIndexStatus()
    if (res.code === 0 && res.data) indexStatus.value = res.data
  } finally {
    loadingIndex.value = false
  }
}

const handleSaveConfig = async () => {
  saving.value = true
  try {
    const res = await api.updateLogConfig({
      max_size_mb: logForm.value.max_size_mb,
      max_days: logForm.value.max_days,
      level: logForm.value.level,
      format: logForm.value.format
    })
    if (res.code === 0) {
      ElMessage.success('日志策略配置已更新并实时生效！')
      if (res.data?.log_stats) logStats.value = res.data.log_stats
      else fetchLogs()
    }
  } finally {
    saving.value = false
  }
}

const handleCleanLogs = () => {
  ElMessageBox.confirm(
    `确定要立即根据当前配置规则（保留 ${logForm.value.max_days} 天 / 最多 ${logForm.value.max_size_mb} MB）执行过期日志清理吗？`,
    '确认清理日志',
    { confirmButtonText: '立即清理', cancelButtonText: '取消', type: 'warning' }
  ).then(async () => {
    cleaning.value = true
    try {
      const res = await api.cleanSystemLogs()
      if (res.code === 0) {
        ElMessage.success('日志清理完成！')
        if (res.data) logStats.value = res.data
        await fetchStorage()
      }
    } finally {
      cleaning.value = false
    }
  }).catch(() => {})
}

const handleRebuildIndex = async () => {
  try {
    await ElMessageBox.confirm(
      '重建索引会在后台基于知识库数据库重新生成全文索引，期间不影响已有检索，但可能占用较多 CPU/IO。是否继续？',
      '重建知识库索引',
      { type: 'warning', confirmButtonText: '开始重建', cancelButtonText: '取消' }
    )
  } catch (e) {
    return
  }
  rebuilding.value = true
  try {
    const res = await api.rebuildKnowledgeIndex()
    if (res.code === 0) {
      if (res.data?.job_id) {
        reindexJobId.value = res.data.job_id
        showReindexProgress.value = true
      } else {
        ElMessage.success('索引重建完成')
        await fetchIndexStatus()
      }
    }
  } finally {
    rebuilding.value = false
  }
}

const handleReindexCompleted = async () => {
  showReindexProgress.value = false
  ElMessage.success('索引重建完成')
  await Promise.all([fetchIndexStatus(), fetchStorage()])
}

const handleReindexFailed = (payload) => {
  const reason = typeof payload === 'string' ? payload : payload?.error || payload?.message
  ElMessage.error('索引重建失败' + (reason ? ': ' + reason : ''))
}

const handleToggleRca = async (enabled) => {
  if (enabled) {
    try {
      await ElMessageBox.confirm(
        '关联事件（实验性）只覆盖 5 条内置规则、按设备隔离分析，可能误报，且不进入主报告。确认开启？',
        '开启实验性能力',
        { type: 'warning', confirmButtonText: '开启', cancelButtonText: '取消' }
      )
    } catch (e) {
      return
    }
  }
  savingFeatures.value = true
  try {
    await featureStore.setRcaEnabled(enabled)
    ElMessage.success(enabled ? '已开启关联事件（实验性）' : '已关闭关联事件（实验性）')
  } finally {
    savingFeatures.value = false
  }
}

onMounted(async () => {
  await Promise.all([
    fetchConfig(),
    fetchStorage(),
    fetchIndexStatus(),
    featureStore.fetchFeatures()
  ])
})
</script>

<style scoped>
.settings-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.settings-tabs {
  background: var(--gray-0);
  border-radius: var(--radius-lg);
  padding: 8px 16px 16px;
  border: 1px solid var(--border-base);
}

.config-card, .files-card, .section-card {
  border-radius: var(--radius-lg);
}

.section-card {
  margin-bottom: 12px;
}

.card-header {
  font-weight: 600;
  font-size: 15px;
}

.card-header-flex {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  font-weight: 600;
  font-size: 15px;
}

.header-btns {
  display: flex;
  gap: 8px;
}

.form-hint {
  font-size: var(--fs-sm);
  color: var(--gray-400);
  margin-top: 4px;
}

.form-actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
  padding-top: 16px;
  border-top: 1px solid var(--gray-100);
}

.file-name-cell {
  display: flex;
  align-items: center;
  gap: 8px;
  font-family: monospace;
  font-size: var(--fs-md);
}

.icon-active { color: var(--success-strong); font-size: var(--fs-xl); }
.icon-archive { color: var(--gray-500); font-size: var(--fs-xl); }
.active-file-text { font-weight: 600; color: var(--gray-900); }
.file-size-tag { font-family: monospace; font-weight: 500; color: var(--gray-700); }
.time-text { font-size: var(--fs-sm); color: var(--gray-500); }

.w-full { width: 100%; }

.storage-counts {
  margin-bottom: 4px;
}

.section-tip {
  margin: 12px 0 0;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.7;
}

.index-alert {
  margin-top: 12px;
}

.lab-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 24px;
}

.lab-title {
  font-size: var(--fs-lg);
  font-weight: 700;
  color: var(--text-primary);
}

.lab-desc {
  margin-top: 6px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.7;
}

.lab-limits {
  margin: 8px 0 0;
  padding-left: 20px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.8;
}

.diag-note {
  margin-left: 8px;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}
</style>
