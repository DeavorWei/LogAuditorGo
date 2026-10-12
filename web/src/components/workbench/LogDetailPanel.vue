<template>
  <div class="col-middle">
    <!-- RCA 联动告警全局提示条 -->
    <div v-if="rcaEvents && rcaEvents.length > 0" class="rca-banner-alert">
      <div class="banner-left">
        <el-icon color="var(--warning-strong)" size="18"><Aim /></el-icon>
        <span class="banner-text">
          <strong>系统已识别 {{ rcaEvents.length }} 个协议联动事件</strong>
        </span>
      </div>
      <el-button type="warning" link size="small" icon="ArrowRight" @click="$emit('view-rca')">
        查看 RCA 全景分析
      </el-button>
    </div>

    <div v-if="log" class="detail-container">
      <div class="panel-title">
        <span><el-icon><Document /></el-icon> 日志报文结构化解析 (#{{ log.id }})</span>
        <el-tag v-if="log.module === 'COMMENT'" size="small" type="info" effect="plain">
          注释/元数据
        </el-tag>
        <el-tag v-else size="small" :type="log.knowledge_id ? 'success' : 'info'">
          {{ log.knowledge_id ? `知识库已匹配 (${(log.match_confidence * 100).toFixed(0)}%)` : '未匹配' }}
        </el-tag>
      </div>

      <!-- 标签展示与单条打标/摘标操作 -->
      <div class="log-detail-tags-bar">
        <span class="detail-tags-label"><el-icon><PriceTag /></el-icon> 标签:</span>
        <div class="detail-tags-list">
          <el-tag
            v-for="t in (log.tags || [])"
            :key="t.id"
            size="small"
            closable
            effect="dark"
            :color="t.color || DEFAULT_TAG_COLOR"
            class="detail-tag-pill"
            @close="handleRemoveTag(t.id)"
          >
            {{ t.name }}
          </el-tag>
          <el-dropdown trigger="click" @command="handleTagDropdownCommand">
            <el-button size="small" type="primary" plain class="add-log-tag-btn">+ 打标签</el-button>
            <template #dropdown>
              <el-dropdown-menu class="tag-dropdown-menu">
                <el-dropdown-item
                  v-for="t in tagStore.tags"
                  :key="t.id"
                  :command="t.id"
                  class="tag-menu-item"
                >
                  <div class="tag-item-row">
                    <span class="tag-color-dot" :style="{ backgroundColor: t.color || DEFAULT_TAG_COLOR }"></span>
                    <span class="tag-name-text">{{ t.name }}</span>
                    <span v-if="isTagAssigned(t.id)" class="tag-assigned-badge">已添加</span>
                    <span
                      class="tag-delete-x"
                      title="删除该标签"
                      @click.stop="handleDeleteTag(t)"
                    >
                      <el-icon><Close /></el-icon>
                    </span>
                  </div>
                </el-dropdown-item>
                <el-dropdown-item v-if="tagStore.tags.length === 0" disabled>
                  暂无标签
                </el-dropdown-item>
                <el-dropdown-item divided command="__create__" class="create-tag-menu-item">
                  <el-icon color="var(--brand-600)"><Plus /></el-icon>
                  <span style="color: var(--brand-600); font-weight: 500;">+ 新增标签</span>
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>

      <!-- 原始日志 -->
      <div class="section-box">
        <div class="box-title">原始 Syslog 报文</div>
        <div class="raw-code">{{ log.raw_log }}</div>
      </div>

      <!-- 事件语义解析摘要 -->
      <div v-if="log.event_summary" class="section-box event-summary-box-wb">
        <div class="box-title">事件语义解析摘要</div>
        <div class="event-summary-highlight-wb">
          <el-icon color="var(--brand-600)" size="18" style="margin-right: 8px; flex-shrink: 0;"><InfoFilled /></el-icon>
          <span class="summary-text-wb">{{ log.event_summary }}</span>
        </div>
      </div>

      <!-- 结构化字段表格 -->
      <div class="section-box">
        <div class="box-title">核心结构化字段</div>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="设备主机名">{{ log.hostname || '-' }}</el-descriptions-item>
          <el-descriptions-item label="时间戳">{{ formatLogTime(log) }}</el-descriptions-item>
          <el-descriptions-item label="所属模块">{{ log.module }}</el-descriptions-item>
          <el-descriptions-item label="事件简名">{{ log.brief }}</el-descriptions-item>
          <el-descriptions-item label="日志级别">
            <SeverityTag :severity="log.severity" :module="log.module" />
          </el-descriptions-item>
          <el-descriptions-item label="来源文件">{{ log.source_file || '-' }}</el-descriptions-item>
          <el-descriptions-item label="槽位/序列号">{{ log.slot_info || '-' }}</el-descriptions-item>
        </el-descriptions>
      </div>

      <!-- 动态提取参数与文档说明融合 -->
      <div class="section-box">
        <div class="box-title flex-between">
          <span>动态提取变量与文档说明 (Parameters & Documentation)</span>
          <span v-if="detail.enrichedParameters.length > 0" class="param-count-badge">
            已提取 {{ detail.enrichedParameters.length }} 个变量
            <template v-if="detail.matchedParamCount > 0">（已匹配 {{ detail.matchedParamCount }} 条文档说明）</template>
          </span>
        </div>
        <div v-if="detail.enrichedParameters && detail.enrichedParameters.length > 0" class="param-grid-enhanced">
          <div
            v-for="p in detail.enrichedParameters"
            :key="p.name"
            :class="['param-card', { 'has-desc': !!p.description }]"
          >
            <div class="param-card-top">
              <span class="p-key">{{ p.name }}</span>
              <el-tooltip
                v-if="p.description"
                placement="top"
                raw-content
                :content="formatTooltipHtml(p.description)"
              >
                <span class="p-desc-badge"><el-icon><Reading /></el-icon> {{ p.description }}</span>
              </el-tooltip>
            </div>
            <div class="p-val-box">
              <span v-if="p.value !== '' && p.value !== null && p.value !== undefined" class="p-val">{{ p.value }}</span>
              <span v-else class="p-val-empty">&lt;空&gt;</span>
            </div>
          </div>
        </div>
        <div v-else class="empty-hint">
          {{ log.module === 'COMMENT' ? '此条记录为设备系统注释或文件导出元数据，非设备故障告警，无需提取业务动态键值。' : '该日志未解析出结构化动态键值变量' }}
        </div>
      </div>

      <!-- 消息模板动态实例化对照 -->
      <div v-if="log.knowledge && log.knowledge.message" class="section-box">
        <div class="box-title flex-between">
          <span><el-icon><Tickets /></el-icon> 官方日志消息模板实例化 (Template Instantiation)</span>
          <el-tag size="small" type="success" effect="plain">变量已注入</el-tag>
        </div>
        <div class="template-box">
          <div class="template-rendered" v-html="detail.renderedTemplateHtml"></div>
          <div class="template-raw-sub">
            <span class="sub-label">官方原始模板:</span>
            <code>{{ log.knowledge.message }}</code>
          </div>
        </div>
      </div>

      <!-- 关联根因提示 -->
      <div v-if="matchedRca" class="rca-alert">
        <div class="rca-alert-title"><el-icon><WarningFilled /></el-icon> 关联根因事件预警</div>
        <div>{{ matchedRca?.root_cause_summary }}</div>
      </div>
    </div>
    <div v-else class="empty-state">
      <el-empty description="请从左侧日志列表中选择一条日志查看结构化解析" />
    </div>
  </div>
</template>

<script setup>
import { computed, inject, h } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Aim, ArrowRight, InfoFilled, Close, Plus, Document, PriceTag, Reading, Tickets, WarningFilled } from '@element-plus/icons-vue'
import { useTaskStore } from '@/stores/task'
import { useTagStore } from '@/stores/tag'
import { LOG_DETAIL_CONTEXT_KEY, formatTooltipHtml } from '@/utils/logFormatter'
import api from '@/api'
import { PREDEFINE_TAG_COLORS, DEFAULT_TAG_COLOR } from '@/constants/tagColors'
import SeverityTag from '@/components/SeverityTag.vue'

const props = defineProps({
  log: {
    type: Object,
    default: null
  },
  taskId: {
    type: String,
    default: ''
  },
  rcaEvents: {
    type: Array,
    default: () => []
  },
  matchedRca: {
    type: Object,
    default: null
  }
})

const emit = defineEmits(['view-rca', 'tag-changed'])

const taskStore = useTaskStore()
const tagStore = useTagStore()

// 注入富文本格式化上下文
const logContext = inject(LOG_DETAIL_CONTEXT_KEY, null)
const detail = computed(() => {
  return logContext?.formattedDetail?.value || {
    enrichedParameters: [],
    matchedParamCount: 0,
    renderedTemplateHtml: ''
  }
})

const isTagAssigned = (tagId) => {
  if (!props.log || !props.log.tags) return false
  return props.log.tags.some((t) => t.id === tagId)
}

const handleTagDropdownCommand = async (command) => {
  if (command === '__create__') {
    openCreateTagPrompt()
  } else if (typeof command === 'number' || typeof command === 'string') {
    if (isTagAssigned(command)) {
      await handleRemoveTag(command)
    } else {
      await handleAddTag(command)
    }
  }
}

const handleAddTag = async (tagId) => {
  if (!props.taskId || !props.log || !tagId) return
  try {
    const res = await api.addLogTag(props.taskId, props.log.id, tagId)
    if (res.code === 0) {
      ElMessage.success('标签添加成功')
      const tag = tagStore.tags.find((t) => t.id === tagId)
      if (tag) {
        const currentTags = [...(props.log.tags || [])]
        if (!currentTags.some((t) => t.id === tagId)) {
          currentTags.push(tag)
        }
        taskStore.updateLogTags(props.log.id, currentTags)
      }
      await tagStore.fetchTags(props.taskId)
      emit('tag-changed')
    }
  } catch (e) {
    console.error('Add tag failed:', e)
  }
}

const handleRemoveTag = async (tagId) => {
  if (!props.taskId || !props.log || !tagId) return
  try {
    const res = await api.removeLogTag(props.taskId, props.log.id, tagId)
    if (res.code === 0) {
      ElMessage.success('标签已移除')
      const nextTags = (props.log.tags || []).filter((t) => t.id !== tagId)
      taskStore.updateLogTags(props.log.id, nextTags)
      await tagStore.fetchTags(props.taskId)
      emit('tag-changed')
    }
  } catch (e) {
    console.error('Remove tag failed:', e)
  }
}

const handleDeleteTag = async (tag) => {
  try {
    await ElMessageBox.confirm(
      `确定要删除标签「${tag.name}」吗？删除后将自动解除所有已关联日志。`,
      '删除标签确认',
      {
        confirmButtonText: '确定删除',
        cancelButtonText: '取消',
        type: 'warning',
        confirmButtonClass: 'el-button--danger'
      }
    )
    const res = await tagStore.deleteTag(props.taskId, tag.id)
    if (res && res.code === 0) {
      ElMessage.success('标签已删除')
      if (props.log && props.log.tags) {
        const nextTags = props.log.tags.filter((t) => t.id !== tag.id)
        taskStore.updateLogTags(props.log.id, nextTags)
      }
      emit('tag-changed')
    }
  } catch (e) {
    if (e !== 'cancel') {
      console.error('Delete tag failed:', e)
    }
  }
}

const openCreateTagPrompt = () => {
  let tagNameInput = ''
  let selectedColor = PREDEFINE_TAG_COLORS[0]

  ElMessageBox({
    title: '新建标签',
    message: () =>
      h('div', { class: 'create-tag-prompt-box' }, [
        h('div', { style: { marginBottom: '10px' } }, [
          h('label', { style: { display: 'block', fontSize: 'var(--fs-md)', marginBottom: '4px', color: 'var(--text-regular)' } }, '标签名称:'),
          h('input', {
            class: 'el-input__inner',
            style: { width: '100%', height: '32px', boxSizing: 'border-box', border: '1px solid var(--border-base)', borderRadius: 'var(--radius-sm)', padding: '0 8px' },
            placeholder: '输入标签名称...',
            onInput: (e) => { tagNameInput = e.target.value }
          })
        ]),
        h('div', {}, [
          h('label', { style: { display: 'block', fontSize: 'var(--fs-md)', marginBottom: '6px', color: 'var(--text-regular)' } }, '选择颜色:'),
          h('div', { style: { display: 'flex', gap: '8px', flexWrap: 'wrap' } },
            PREDEFINE_TAG_COLORS.map(c =>
              h('span', {
                style: {
                  width: '20px',
                  height: '20px',
                  borderRadius: '50%',
                  backgroundColor: c,
                  display: 'inline-block',
                  cursor: 'pointer',
                  border: selectedColor === c ? '2px solid var(--gray-900)' : '2px solid transparent'
                },
                onClick: () => { selectedColor = c }
              })
            )
          )
        ])
      ]),
    showCancelButton: true,
    confirmButtonText: '创建并添加',
    cancelButtonText: '取消',
    beforeClose: async (action, instance, done) => {
      if (action === 'confirm') {
        const trimmed = (tagNameInput || '').trim()
        if (!trimmed) {
          ElMessage.warning('标签名称不能为空')
          return
        }
        try {
          instance.confirmButtonLoading = true
          const res = await tagStore.createTag(props.taskId, {
            name: trimmed,
            color: selectedColor
          })
          if (res && res.code === 0 && res.data) {
            ElMessage.success('标签创建成功')
            done()
            await handleAddTag(res.data.id)
          } else {
            ElMessage.error(res?.message || '创建标签失败')
          }
        } finally {
          instance.confirmButtonLoading = false
        }
      } else {
        done()
      }
    }
  }).catch(() => {})
}

const formatLogTime = (log) => {
  if (!log) return '-'
  return log.timestamp || '-'
}
</script>

<style scoped>
.col-middle {
  flex: 0 0 var(--wb-col-mid, 36%);
  width: var(--wb-col-mid, 36%);
  min-width: 320px;
  display: flex;
  flex-direction: column;
  background: var(--gray-0);
  border-right: 1px solid var(--border-base);
  overflow-y: auto;
  box-sizing: border-box;
}

.rca-banner-alert {
  display: flex;
  justify-content: space-between;
  align-items: center;
  background: var(--sev-err-bg);
  border-bottom: 1px solid var(--sev-err-border);
  padding: 8px 14px;
}

.banner-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.banner-text {
  font-size: var(--fs-md);
  color: var(--sev-err-text);
}

.detail-container {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.panel-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 15px;
  font-weight: 600;
  color: var(--gray-900);
}

.log-detail-tags-bar {
  display: flex;
  align-items: center;
  gap: 8px;
}

.detail-tags-label {
  font-size: var(--fs-sm);
  color: var(--gray-500);
}

.detail-tags-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
}

.detail-tag-pill {
  border-radius: var(--radius-sm);
}

.event-summary-highlight-wb {
  display: flex;
  align-items: flex-start;
  font-size: var(--fs-md);
  font-weight: 500;
  color: var(--brand-700);
  line-height: 1.5;
}

.summary-text-wb {
  word-break: break-word;
}

.param-count-badge {
  font-size: var(--fs-xs);
  color: var(--brand-600);
}

.empty-hint {
  font-size: var(--fs-sm);
  color: var(--gray-500);
  font-style: italic;
  padding: 6px 0;
}

.template-box {
  background: var(--gray-0);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  padding: 10px 12px;
}

.template-rendered {
  font-size: var(--fs-md);
  line-height: 1.6;
  color: var(--gray-900);
}

.template-raw-sub {
  margin-top: 8px;
  font-size: var(--fs-sm);
  color: var(--gray-500);
}

.template-raw-sub code {
  background: var(--gray-100);
  padding: 2px 6px;
  border-radius: var(--radius-xs);
}

.tag-item-row {
  display: flex;
  align-items: center;
  width: 100%;
  gap: 8px;
}

.tag-color-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tag-name-text {
  flex: 1;
}

.tag-assigned-badge {
  font-size: var(--fs-xs);
  color: var(--success-strong);
}

.tag-delete-x {
  color: var(--gray-400);
  cursor: pointer;
}
.tag-delete-x:hover {
  color: var(--danger-strong);
}
</style>
