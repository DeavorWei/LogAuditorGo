<template>
  <el-dialog
    v-model="dialogVisible"
    title="快速打标签"
    width="460px"
    destroy-on-close
    append-to-body
    class="quick-tag-modal"
    @opened="onDialogOpened"
    @closed="onDialogClosed"
  >
    <template #header>
      <div class="modal-custom-header">
        <span class="header-title">🏷 快速打标签</span>
        <span v-if="log" class="header-subtitle">
          #{{ log.id }}
          <span class="header-log-brief">{{ log.module }}/{{ log.brief }}</span>
        </span>
      </div>
    </template>

    <div v-if="log" class="quick-tag-content">
      <!-- 当前日志已有标签区 -->
      <div class="current-tags-section">
        <span class="section-label">当前标签:</span>
        <div class="current-tags-list">
          <template v-if="logTags && logTags.length > 0">
            <el-tag
              v-for="t in logTags"
              :key="t.id"
              size="small"
              closable
              effect="dark"
              :color="t.color || '#409EFF'"
              class="current-tag-chip"
              @close="handleRemoveTag(t.id)"
            >
              {{ t.name }}
            </el-tag>
          </template>
          <span v-else class="empty-tags-hint">暂无标签</span>
        </div>
      </div>

      <!-- 搜索或新建标签输入框 (一框两用，回车极速创建打标) -->
      <div class="search-create-box">
        <el-input
          ref="inputRef"
          v-model="inputKeyword"
          placeholder="搜索已有标签，或输入新名称按 Enter 创建打标..."
          clearable
          size="default"
          maxlength="32"
          class="tag-input-field"
          @keyup.enter="handleEnterKey"
        >
          <template #prefix>
            <el-icon class="input-search-icon"><Search /></el-icon>
          </template>
          <template #append>
            <el-button
              type="primary"
              :disabled="!canCreateNew"
              :loading="creatingTag"
              @click="handleCreateAndAdd"
            >
              + 创建并打标
            </el-button>
          </template>
        </el-input>
      </div>

      <!-- 快捷标签选择网格 -->
      <div class="available-tags-section">
        <div class="available-header">
          <span class="section-label">选择标签:</span>
          <span class="tags-count-hint">共 {{ filteredTags.length }} 个</span>
        </div>

        <div class="available-tags-grid">
          <div
            v-for="tag in filteredTags"
            :key="tag.id"
            :class="['tag-pick-item', { 'is-assigned': isTagAssigned(tag.id) }]"
            @click="handleToggleTag(tag)"
          >
            <span
              class="tag-color-badge"
              :style="{ backgroundColor: tag.color || '#409EFF' }"
            />
            <span class="tag-name-text">{{ tag.name }}</span>
            <span v-if="isTagAssigned(tag.id)" class="tag-status-check">✓</span>
            <span v-else class="tag-count-sub">{{ tag.log_count || 0 }}</span>
          </div>

          <div v-if="filteredTags.length === 0" class="no-tags-tip">
            <span v-if="inputKeyword.trim()">
              未找到匹配标签，按 <strong>Enter</strong> 键可直接创建 <strong>「{{ inputKeyword.trim() }}」</strong> 并打标
            </span>
            <span v-else>暂无可选择的标签，请直接输入名称创建</span>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="quick-tag-footer">
        <span class="footer-tip">💡 提示: 点击标签或按 Enter 快速打标 · Esc 关闭</span>
        <el-button size="small" type="primary" @click="dialogVisible = false">
          完成
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, computed, nextTick } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { useTagStore } from '@/stores/tag'
import api from '@/api'

const PREDEFINE_TAG_COLORS = [
  '#409EFF', '#67C23A', '#E6A23C', '#F56C6C',
  '#909399', '#0284c7', '#10b981', '#f97316',
  '#8b5cf6', '#ec4899', '#14b8a6', '#6366f1'
]

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  log: {
    type: Object,
    default: () => null
  },
  taskId: {
    type: [String, Number],
    default: null
  }
})

const emit = defineEmits(['update:visible', 'tag-added', 'tag-removed'])

const tagStore = useTagStore()
const inputRef = ref(null)
const inputKeyword = ref('')
const creatingTag = ref(false)

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const logTags = computed(() => {
  return props.log?.tags || []
})

const isTagAssigned = (tagId) => {
  return logTags.value.some(t => t.id === tagId)
}

// 过滤现有标签
const filteredTags = computed(() => {
  const q = inputKeyword.value.trim().toLowerCase()
  if (!q) return tagStore.tags
  return tagStore.tags.filter(t => t.name.toLowerCase().includes(q))
})

// 判断输入内容是否为新标签
const canCreateNew = computed(() => {
  const name = inputKeyword.value.trim()
  if (!name) return false
  return !tagStore.tags.some(t => t.name.toLowerCase() === name.toLowerCase())
})

const onDialogOpened = () => {
  inputKeyword.value = ''
  nextTick(() => {
    inputRef.value?.focus()
  })
}

const onDialogClosed = () => {
  inputKeyword.value = ''
}

// 点击标签进行添加或移除
const handleToggleTag = async (tag) => {
  if (!props.log || !props.taskId) return
  if (isTagAssigned(tag.id)) {
    await handleRemoveTag(tag.id)
  } else {
    await handleAddExistingTag(tag)
  }
}

// 为日志打上已有标签
const handleAddExistingTag = async (tag) => {
  if (!props.log || !props.taskId) return
  try {
    const res = await api.addLogTag(props.taskId, props.log.id, tag.id)
    if (res.code === 0) {
      ElMessage.success(`已打上标签「${tag.name}」`)
      if (!props.log.tags) props.log.tags = []
      if (!props.log.tags.some(t => t.id === tag.id)) {
        props.log.tags.push(tag)
      }
      emit('tag-added', props.log, tag)
      await tagStore.fetchTags(props.taskId)
    }
  } catch (e) {
    // 错误统一由 api 处理
  }
}

// 从当前日志移除标签
const handleRemoveTag = async (tagId) => {
  if (!props.log || !props.taskId) return
  try {
    const res = await api.removeLogTag(props.taskId, props.log.id, tagId)
    if (res.code === 0) {
      ElMessage.success('已移除标签')
      if (props.log.tags) {
        props.log.tags = props.log.tags.filter(t => t.id !== tagId)
      }
      emit('tag-removed', props.log, tagId)
      await tagStore.fetchTags(props.taskId)
    }
  } catch (e) {
    // 错误统一由 api 处理
  }
}

// 回车快速处理逻辑：已有则打标，新名称则创建并打标
const handleEnterKey = async () => {
  const text = inputKeyword.value.trim()
  if (!text) return

  // 1. 如果匹配已有标签
  const exactMatch = tagStore.tags.find(t => t.name.toLowerCase() === text.toLowerCase())
  if (exactMatch) {
    if (isTagAssigned(exactMatch.id)) {
      ElMessage.info(`该日志已包含标签「${exactMatch.name}」`)
    } else {
      await handleAddExistingTag(exactMatch)
    }
    inputKeyword.value = ''
    return
  }

  // 2. 如果是全新标签，自动创建并打标
  await handleCreateAndAdd()
}

// 创建新标签并直接打标
const handleCreateAndAdd = async () => {
  const tagName = inputKeyword.value.trim()
  if (!tagName || !props.taskId || !props.log) return
  if (creatingTag.value) return

  creatingTag.value = true
  try {
    const colorIndex = tagStore.tags.length % PREDEFINE_TAG_COLORS.length
    const color = PREDEFINE_TAG_COLORS[colorIndex] || '#409EFF'

    const newTag = await tagStore.createTag(props.taskId, {
      name: tagName,
      color,
      remark: ''
    })

    if (newTag) {
      await handleAddExistingTag(newTag)
      inputKeyword.value = ''
    }
  } finally {
    creatingTag.value = false
  }
}
</script>

<style scoped>
.quick-tag-modal :deep(.el-dialog__header) {
  padding: 12px 16px;
  margin-right: 0;
  border-bottom: 1px solid #f1f5f9;
}

.quick-tag-modal :deep(.el-dialog__body) {
  padding: 14px 16px;
}

.quick-tag-modal :deep(.el-dialog__footer) {
  padding: 10px 16px;
  border-top: 1px solid #f1f5f9;
}

.modal-custom-header {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.header-title {
  font-size: 15px;
  font-weight: 600;
  color: #0f172a;
}

.header-subtitle {
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  color: #64748b;
}

.header-log-brief {
  color: #0284c7;
  font-weight: 600;
  margin-left: 4px;
}

.quick-tag-content {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 已有标签栏 */
.current-tags-section {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  min-height: 26px;
}

.section-label {
  font-size: 12px;
  color: #64748b;
  font-weight: 600;
  white-space: nowrap;
  line-height: 24px;
}

.current-tags-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  flex: 1;
}

.current-tag-chip {
  font-weight: 500;
  border: none;
  color: #ffffff;
}

.empty-tags-hint {
  font-size: 12px;
  color: #94a3b8;
  line-height: 24px;
}

/* 搜索/新建框 */
.search-create-box {
  display: flex;
  gap: 6px;
}

.input-search-icon {
  color: #94a3b8;
}

/* 可选标签列表 */
.available-tags-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.available-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.tags-count-hint {
  font-size: 11px;
  color: #94a3b8;
}

.available-tags-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: 190px;
  overflow-y: auto;
  padding: 4px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
}

.tag-pick-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 8px;
  background: #ffffff;
  border: 1px solid #cbd5e1;
  border-radius: 4px;
  cursor: pointer;
  user-select: none;
  font-size: 12px;
  transition: all 0.15s ease;
}

.tag-pick-item:hover {
  border-color: #3b82f6;
  background: #eff6ff;
  transform: translateY(-1px);
}

.tag-pick-item.is-assigned {
  background: #f0fdf4;
  border-color: #86efac;
  color: #166534;
}

.tag-color-badge {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tag-name-text {
  font-weight: 500;
  color: #1e293b;
  max-width: 130px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tag-pick-item.is-assigned .tag-name-text {
  color: #15803d;
}

.tag-status-check {
  font-size: 11px;
  font-weight: 700;
  color: #16a34a;
}

.tag-count-sub {
  font-size: 10px;
  color: #94a3b8;
}

.no-tags-tip {
  width: 100%;
  padding: 16px 8px;
  text-align: center;
  font-size: 12px;
  color: #64748b;
  line-height: 1.6;
}

.quick-tag-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.footer-tip {
  font-size: 11px;
  color: #94a3b8;
}
</style>
