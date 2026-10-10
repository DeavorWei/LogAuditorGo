<template>
  <el-dialog
    v-model="dialogVisible"
    :show-close="false"
    append-to-body
    destroy-on-close
    class="audit-detail-modal-dialog"
    width="86vw"
    top="5vh"
    @closed="onDialogClosed"
  >
    <!-- 自定义精致头部 (带上一条/下一条穿梭轮播) -->
    <template #header>
      <div v-if="log" class="modal-custom-header">
        <div class="header-title-box">
          <span :class="['sev-badge', getSevClass(log.severity)]">Lv.{{ log.severity }}</span>
          <span class="header-log-id">#{{ log.id }}</span>
          <span class="header-log-mod">{{ log.module }}/{{ log.brief }}</span>
          <span v-if="log.knowledge_id > 0" class="header-match-tag">
            已匹配 ({{ (log.match_confidence * 100).toFixed(0) }}%)
          </span>
          <span v-else-if="log.module === 'COMMENT'" class="header-comment-tag">注释/元数据</span>
        </div>

        <div class="header-nav-actions">
          <el-tooltip content="上一条日志 (快捷键: ↑ 或 k)" placement="bottom">
            <el-button
              size="small"
              icon="ArrowUp"
              :disabled="currentIndex <= 0"
              @click="handlePrev"
            >
              上一条
            </el-button>
          </el-tooltip>

          <span class="nav-counter">{{ currentIndex + 1 }} / {{ totalCount }}</span>

          <el-tooltip content="下一条日志 (快捷键: ↓ 或 j)" placement="bottom">
            <el-button
              size="small"
              icon="ArrowDown"
              :disabled="currentIndex >= totalCount - 1"
              @click="handleNext"
            >
              下一条
            </el-button>
          </el-tooltip>

          <el-button
            circle
            size="small"
            icon="Close"
            class="close-dialog-btn"
            title="关闭弹窗 (Esc)"
            @click="handleClose"
          />
        </div>
      </div>
    </template>

    <!-- 双栏 50% : 50% 并排内容区 -->
    <div v-if="log" class="modal-dual-container">
      <!-- 左栏 50%: 报文结构化解析与动态参数 -->
      <div class="modal-pane-left">
        <!-- 标签栏 -->
        <div class="log-detail-tags-bar">
          <span class="detail-tags-label">🏷 标签:</span>
          <div class="detail-tags-list">
            <el-tag
              v-for="t in (log.tags || [])"
              :key="t.id"
              size="small"
              closable
              effect="dark"
              :color="t.color || '#409EFF'"
              class="detail-tag-pill"
              @close="$emit('remove-tag', log.id, t.id)"
            >
              {{ t.name }}
            </el-tag>
            <el-dropdown trigger="click" @command="handleAddTag">
              <el-button size="small" type="primary" plain class="add-log-tag-btn">+ 打标签</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item
                    v-for="t in availableTags"
                    :key="t.id"
                    :command="t.id"
                  >
                    <span :style="{ display: 'inline-block', width: '8px', height: '8px', borderRadius: '50%', backgroundColor: t.color || '#409EFF', marginRight: '6px' }"></span>
                    {{ t.name }}
                  </el-dropdown-item>
                  <el-dropdown-item v-if="availableTags.length === 0" disabled>
                    暂无可选新标签
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>

        <!-- 原始 Syslog 报文 -->
        <div class="section-box">
          <div class="box-title">原始 Syslog 报文</div>
          <div class="raw-code">{{ log.raw_log }}</div>
        </div>

        <!-- 事件语义解析摘要 -->
        <div v-if="log.event_summary" class="section-box event-summary-box-wb">
          <div class="box-title">事件语义解析摘要</div>
          <div class="event-summary-highlight-wb">
            <el-icon color="#0284c7" size="18" style="margin-right: 8px; flex-shrink: 0;"><InfoFilled /></el-icon>
            <span class="summary-text-wb">{{ log.event_summary }}</span>
          </div>
        </div>

        <!-- 核心结构化字段 -->
        <div class="section-box">
          <div class="box-title">核心结构化字段</div>
          <el-descriptions :column="2" border size="small">
            <el-descriptions-item label="设备主机名">{{ log.hostname || '-' }}</el-descriptions-item>
            <el-descriptions-item label="时间戳">{{ formatDisplayTime(log) }}</el-descriptions-item>
            <el-descriptions-item label="所属模块">{{ log.module }}</el-descriptions-item>
            <el-descriptions-item label="事件简名">{{ log.brief }}</el-descriptions-item>
            <el-descriptions-item label="日志级别">
              <span :class="['sev-tag', getSevClass(log.severity)]">
                Level {{ log.severity }}{{ log.module === 'COMMENT' ? ' (注释信息)' : '' }}
              </span>
            </el-descriptions-item>
            <el-descriptions-item label="来源文件">{{ log.source_file || '-' }}</el-descriptions-item>
            <el-descriptions-item label="槽位/序列号">{{ log.slot_info || '-' }}</el-descriptions-item>
          </el-descriptions>
        </div>

        <!-- 动态提取参数与文档说明 -->
        <div class="section-box">
          <div class="box-title flex-between">
            <span>动态提取变量与文档说明 (Parameters & Documentation)</span>
            <span v-if="enrichedParameters.length > 0" class="param-count-badge">
              已提取 {{ enrichedParameters.length }} 个变量
              <template v-if="matchedParamCount > 0">（已匹配 {{ matchedParamCount }} 条说明）</template>
            </span>
          </div>
          <div v-if="enrichedParameters && enrichedParameters.length > 0" class="param-grid-enhanced">
            <div
              v-for="p in enrichedParameters"
              :key="p.name"
              :class="['param-card', { 'has-desc': !!p.description }]"
            >
              <div class="param-card-top">
                <span class="p-key">{{ p.name }}</span>
                <el-tooltip v-if="p.description" placement="top" raw-content :content="formatTooltipHtml(p.description)">
                  <span class="p-desc-badge">📖 {{ p.description }}</span>
                </el-tooltip>
              </div>
              <div class="p-val-box">
                <span v-if="p.value !== '' && p.value !== null && p.value !== undefined" class="p-val">{{ p.value }}</span>
                <span v-else class="p-val-empty">&lt;空&gt;</span>
              </div>
            </div>
          </div>
          <div v-else class="empty-hint">
            {{ log.module === 'COMMENT' ? '此条记录为设备系统注释或元数据，非设备故障告警，无需提取业务动态键值。' : '该日志未解析出结构化动态键值变量' }}
          </div>
        </div>

        <!-- 官方日志消息模板实例化 -->
        <div v-if="log.knowledge && log.knowledge.message" class="section-box">
          <div class="box-title flex-between">
            <span>📋 官方日志消息模板实例化</span>
            <el-tag size="small" type="success" effect="plain">变量已注入</el-tag>
          </div>
          <div class="template-box">
            <div class="template-rendered" v-html="renderedTemplateHtml"></div>
            <div class="template-raw-sub">
              <span class="sub-label">官方原始模板:</span>
              <code>{{ log.knowledge.message }}</code>
            </div>
          </div>
        </div>

        <!-- 关联根因预警 -->
        <div v-if="matchedRCA" class="rca-alert">
          <div class="rca-alert-title">🚨 关联根因事件预警</div>
          <div>{{ matchedRCA.root_cause_summary }}</div>
        </div>
      </div>

      <!-- 右栏 50%: 华为官方知识库与 RCA 因果拓扑 -->
      <div class="modal-pane-right">
        <el-tabs v-model="activeTab" class="custom-tabs">
          <el-tab-pane label="官方知识与排查步骤" name="knowledge">
            <div v-if="log.knowledge" class="kb-content">
              <div class="kb-header-card">
                <div class="kb-header-top">
                  <div class="kb-title">{{ log.knowledge.module }}/{{ log.knowledge.brief }}</div>
                  <el-switch
                    v-model="contextualizeMode"
                    size="small"
                    active-text="现场参数注入"
                    inactive-text="原始文档"
                    style="--el-switch-on-color: #10b981;"
                  />
                </div>
                <div class="kb-meta">
                  <span class="badge-tier">匹配层级: {{ log.match_tier }}</span>
                  <span class="badge-conf">置信度: {{ (log.match_confidence * 100).toFixed(0) }}%</span>
                  <span v-if="contextualizeMode && matchedParamCount > 0" class="badge-ctx">
                    ✨ 已将现场 {{ matchedParamCount }} 个参数动态注入至排查步骤
                  </span>
                </div>
              </div>

              <!-- 含义解释 -->
              <div class="kb-block">
                <div class="kb-subtitle">📖 日志/告警含义解释</div>
                <div class="kb-text" v-html="renderedKnowledgeHtml.description"></div>
              </div>

              <!-- 产生原因 -->
              <div class="kb-block">
                <div class="kb-subtitle">🔍 官方可能原因</div>
                <div class="kb-text cause-text" v-html="renderedKnowledgeHtml.cause"></div>
              </div>

              <!-- 官方处理排错步骤 -->
              <div class="kb-block">
                <div class="kb-subtitle">🛠️ 官方处理排错步骤</div>
                <div class="kb-text action-box" v-html="renderedKnowledgeHtml.action"></div>
              </div>

              <!-- 系统影响 -->
              <div v-if="log.knowledge.impact" class="kb-block">
                <div class="kb-subtitle">⚠️ 对系统的影响</div>
                <div class="kb-text" v-html="renderedKnowledgeHtml.impact"></div>
              </div>

              <!-- 官方参数字典与现场实际值对照 -->
              <div v-if="kbParamDefs.length > 0" class="kb-block">
                <div class="kb-subtitle flex-between">
                  <span>📚 官方参数字典与现场实际值对照</span>
                  <span class="dict-count-tag">共 {{ kbParamDefs.length }} 项参数定义</span>
                </div>
                <el-table :data="kbParamDefs" size="small" border style="width: 100%; margin-top: 6px;">
                  <el-table-column prop="name" label="参数名称" width="130">
                    <template #default="{ row }">
                      <span class="dict-pname">{{ row.name }}</span>
                    </template>
                  </el-table-column>
                  <el-table-column prop="description" label="官方含义说明" min-width="140" />
                  <el-table-column label="现场实际值" width="130">
                    <template #default="{ row }">
                      <span v-if="row.actualValue !== undefined" class="dict-pval">{{ row.actualValue }}</span>
                      <span v-else class="dict-pnone">未捕获</span>
                    </template>
                  </el-table-column>
                </el-table>
              </div>
            </div>

            <!-- 注释行提示 -->
            <div v-else-if="log.module === 'COMMENT'" class="comment-kb-card">
              <div class="comment-kb-header">
                <el-icon color="#0284c7" size="18"><InfoFilled /></el-icon>
                <span class="comment-kb-title">💡 注释性日志说明 (Comment / Metadata)</span>
              </div>
              <div class="comment-kb-body">
                <p>网络设备系统注释或日志文件导出元数据行（以 <code>#</code> 开头），非网络故障告警，无需故障排错处置。</p>
              </div>
            </div>

            <!-- 未命中知识库空态 -->
            <div v-else class="empty-kb">
              <el-empty description="该日志未命中官方知识库，可在知识库中心使用语义检索获取背景知识" />
            </div>
          </el-tab-pane>

          <el-tab-pane label="根因传播拓扑 (RCA)" name="rca">
            <div v-if="matchedRCA" class="rca-tab-content">
              <RcaGraph :rcaEvent="matchedRCA" />
              <div class="rca-guide">
                <div class="guide-title">💡 根因处置指南</div>
                <div>{{ matchedRCA.recommended_action }}</div>
              </div>
            </div>
            <div v-else class="empty-kb">
              <el-empty description="当前选中日志未触发协议级连环故障传播链路" />
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { formatTime } from '@/utils/format'
import { ArrowUp, ArrowDown, Close, InfoFilled } from '@element-plus/icons-vue'
import RcaGraph from '@/components/RcaGraph.vue'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  log: {
    type: Object,
    default: () => null
  },
  currentIndex: {
    type: Number,
    default: 0
  },
  totalCount: {
    type: Number,
    default: 0
  },
  matchedRCA: {
    type: Object,
    default: () => null
  },
  renderedTemplateHtml: {
    type: String,
    default: ''
  },
  renderedKnowledgeHtml: {
    type: Object,
    default: () => ({ description: '', cause: '', action: '', impact: '' })
  },
  enrichedParameters: {
    type: Array,
    default: () => []
  },
  matchedParamCount: {
    type: Number,
    default: 0
  },
  kbParamDefs: {
    type: Array,
    default: () => []
  },
  availableTags: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits([
  'update:visible',
  'prev',
  'next',
  'add-tag',
  'remove-tag'
])

const activeTab = ref('knowledge')
const contextualizeMode = ref(true)

const dialogVisible = computed({
  get() {
    return props.visible
  },
  set(val) {
    emit('update:visible', val)
  }
})

const handlePrev = () => {
  if (props.currentIndex > 0) {
    emit('prev')
  }
}

const handleNext = () => {
  if (props.currentIndex < props.totalCount - 1) {
    emit('next')
  }
}

const handleClose = () => {
  emit('update:visible', false)
}

const onDialogClosed = () => {
  emit('update:visible', false)
}

const handleAddTag = (tagId) => {
  emit('add-tag', tagId)
}

const formatDisplayTime = (record) => {
  if (!record) return '-'
  if (record.module === 'COMMENT' && (!record.timestamp || String(record.timestamp).startsWith('0001-01-01'))) {
    return '— (注释行无时戳)'
  }
  return formatTime(record.timestamp)
}

const getSevClass = (sev) => {
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
}

const formatTooltipHtml = (desc) => {
  if (!desc) return ''
  return String(desc).replace(/\n/g, '<br/>')
}

// 键盘无障碍轮播穿梭事件监听
const onKeydown = (e) => {
  if (!props.visible) return

  // 快捷键守卫：输入控件内输入时不触发
  const target = e.target
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
    return
  }

  if (e.key === 'ArrowUp' || e.key === 'k') {
    e.preventDefault()
    handlePrev()
  } else if (e.key === 'ArrowDown' || e.key === 'j') {
    e.preventDefault()
    handleNext()
  } else if (e.key === 'Escape') {
    e.preventDefault()
    handleClose()
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
})
</script>

<style scoped>
:deep(.audit-detail-modal-dialog) {
  border-radius: 10px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  max-height: 90vh;
}

:deep(.audit-detail-modal-dialog .el-dialog__header) {
  padding: 12px 18px;
  margin-right: 0;
  border-bottom: 1px solid #e2e8f0;
  background: #f8fafc;
}

:deep(.audit-detail-modal-dialog .el-dialog__body) {
  padding: 0;
  flex: 1;
  overflow: hidden;
  height: 78vh;
}

.modal-custom-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.header-title-box {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sev-badge {
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
  font-weight: 700;
}
.sev-crit { background: #fee2e2; color: #991b1b; }
.sev-err  { background: #ffedd5; color: #9a3412; }
.sev-warn { background: #fef9c3; color: #854d0e; }
.sev-info { background: #f1f5f9; color: #475569; }

.header-log-id {
  font-weight: 700;
  color: #0f172a;
  font-size: 15px;
}

.header-log-mod {
  font-weight: 600;
  color: #0369a1;
  font-size: 14px;
}

.header-match-tag {
  background: #dcfce7;
  color: #166534;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
}

.header-comment-tag {
  background: #f1f5f9;
  color: #475569;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
}

.header-nav-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nav-counter {
  font-size: 12px;
  color: #64748b;
  font-family: monospace;
  padding: 0 4px;
}

.close-dialog-btn {
  margin-left: 6px;
}

/* 双栏容器 50% : 50% */
.modal-dual-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.modal-pane-left {
  width: 50%;
  border-right: 1px solid #e2e8f0;
  overflow-y: auto;
  padding: 16px;
  background: #ffffff;
}

.modal-pane-right {
  width: 50%;
  overflow-y: auto;
  padding: 16px;
  background: #ffffff;
}

.section-box {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 14px;
}

.box-title {
  font-size: 12px;
  font-weight: 600;
  color: #475569;
  margin-bottom: 8px;
}

.flex-between {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.param-count-badge {
  font-size: 11px;
  font-weight: normal;
  color: #0284c7;
  background: #e0f2fe;
  padding: 1px 6px;
  border-radius: 4px;
}

.raw-code {
  font-family: monospace;
  font-size: 12px;
  background: #1e293b;
  color: #f8fafc;
  padding: 10px;
  border-radius: 4px;
  word-break: break-all;
  line-height: 1.5;
}

.event-summary-box-wb {
  background: #f0f9ff;
  border: 1px solid #bae6fd;
}

.event-summary-highlight-wb {
  display: flex;
  align-items: flex-start;
  font-size: 13px;
  font-weight: 500;
  color: #0369a1;
  line-height: 1.5;
  padding: 4px 0;
}

.summary-text-wb {
  word-break: break-word;
}

.param-grid-enhanced {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 8px;
}

.param-card {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 8px 10px;
}

.param-card-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 4px;
}

.p-key {
  font-weight: 600;
  color: #0f172a;
  font-size: 12px;
}

.p-desc-badge {
  font-size: 10px;
  color: #0284c7;
  cursor: pointer;
}

.p-val {
  font-size: 12px;
  color: #16a34a;
  font-family: monospace;
  word-break: break-all;
}

.p-val-empty {
  font-size: 11px;
  color: #94a3b8;
  font-style: italic;
}

.empty-hint {
  font-size: 12px;
  color: #94a3b8;
  padding: 6px 0;
}

.template-box {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 4px;
  padding: 8px;
}

.template-rendered {
  font-size: 12px;
  color: #1e293b;
  margin-bottom: 6px;
}

.template-raw-sub {
  font-size: 11px;
  color: #64748b;
}

.rca-alert {
  background: #fef2f2;
  border: 1px solid #fecaca;
  color: #991b1b;
  padding: 10px;
  border-radius: 6px;
  font-size: 12px;
  margin-top: 10px;
}

.rca-alert-title {
  font-weight: 700;
  margin-bottom: 4px;
}

/* 标签条 */
.log-detail-tags-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.detail-tags-label {
  font-size: 12px;
  color: #64748b;
  font-weight: 600;
}

.detail-tags-list {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

/* 知识库右栏 */
.kb-content {
  padding: 4px 0;
}

.kb-header-card {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 10px;
  margin-bottom: 12px;
}

.kb-header-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}

.kb-title {
  font-size: 14px;
  font-weight: 700;
  color: #0f172a;
}

.kb-meta {
  display: flex;
  gap: 8px;
  font-size: 11px;
  align-items: center;
}

.badge-tier { background: #e0f2fe; color: #0369a1; padding: 1px 6px; border-radius: 3px; }
.badge-conf { background: #dcfce7; color: #15803d; padding: 1px 6px; border-radius: 3px; }
.badge-ctx  { color: #059669; font-weight: 500; }

.kb-block {
  margin-bottom: 14px;
}

.kb-subtitle {
  font-size: 12px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 6px;
}

.kb-text {
  font-size: 12px;
  line-height: 1.6;
  color: #475569;
}

.dict-pname { font-weight: 600; color: #0284c7; }
.dict-pval { color: #16a34a; font-family: monospace; }
.dict-pnone { color: #94a3b8; font-style: italic; }
.dict-count-tag { font-size: 11px; color: #64748b; }

.comment-kb-card {
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 6px;
  padding: 14px;
}

.comment-kb-header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
  color: #0284c7;
  margin-bottom: 8px;
}

.comment-kb-body {
  font-size: 12px;
  color: #334155;
  line-height: 1.6;
}

.empty-kb, .empty-state {
  padding: 30px 0;
}
</style>
