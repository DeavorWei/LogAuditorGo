<template>
  <el-drawer
    v-model="visible"
    title="高级筛选与规则圈选"
    size="640px"
    destroy-on-close
    class="advanced-filter-drawer"
  >
    <div class="drawer-container">
      <!-- 基础筛选叠加提示条 -->
      <div v-if="baseFilterSummary.length > 0" class="base-filter-alert">
        <el-alert type="info" :closable="false" show-icon>
          <template #title>
            <span class="alert-title">当前与基础筛选条件为叠加 (AND) 关系：</span>
          </template>
          <div class="base-tags">
            <el-tag
              v-for="(item, idx) in baseFilterSummary"
              :key="idx"
              size="small"
              type="info"
              effect="plain"
              class="base-tag-item"
            >
              {{ item.label }}: {{ item.value }}
            </el-tag>
          </div>
        </el-alert>
      </div>

      <!-- 顶层组合逻辑切换 -->
      <div class="logic-bar">
        <span class="logic-label">条件组合关系：</span>
        <el-radio-group v-model="localAdvanced.logic" size="small" @change="debouncedPreview">
          <el-radio-button label="AND">全部满足 (AND)</el-radio-button>
          <el-radio-button label="OR">满足任一 (OR)</el-radio-button>
        </el-radio-group>
      </div>

      <!-- 条件列表构造器 -->
      <div class="conditions-list">
        <div
          v-for="(cond, index) in localAdvanced.conditions"
          :key="index"
          class="condition-row"
        >
          <div class="row-index">#{{ index + 1 }}</div>

          <!-- 字段选择 -->
          <el-select
            v-model="cond.field"
            placeholder="字段"
            size="default"
            class="field-select"
            @change="onFieldChange(cond)"
          >
            <el-option-group label="核心报文">
              <el-option label="原始报文 (raw_log)" value="raw_log" />
              <el-option label="消息正文 (message_body)" value="message_body" />
              <el-option label="事件简名 (brief)" value="brief" />
              <el-option label="模块名 (module)" value="module" />
              <el-option label="主机名 (hostname)" value="hostname" />
              <el-option label="插槽槽位 (slot_info)" value="slot_info" />
            </el-option-group>
            <el-option-group label="动态参数 (KV)">
              <el-option label="参数提取 (parameters)" value="parameters" />
            </el-option-group>
            <el-option-group label="审计元数据">
              <el-option label="来源文件 (source_file)" value="source_file" />
              <el-option label="严重级别 (severity)" value="severity" />
              <el-option label="匹配分层 (match_tier)" value="match_tier" />
              <el-option label="置信度 (match_confidence)" value="match_confidence" />
              <el-option label="知识库ID (knowledge_id)" value="knowledge_id" />
            </el-option-group>
          </el-select>

          <!-- 操作符选择 -->
          <el-select
            v-model="cond.op"
            placeholder="操作符"
            size="default"
            class="op-select"
            @change="debouncedPreview"
          >
            <el-option
              v-for="op in getAvailableOps(cond.field)"
              :key="op.value"
              :label="op.label"
              :value="op.value"
            />
          </el-select>

          <!-- 值输入框 -->
          <div class="value-input-wrapper">
            <el-input
              v-model="cond.value"
              :placeholder="getValuePlaceholder(cond)"
              clearable
              size="default"
              @input="onValueInput(cond)"
            />
            <!-- 正则提示或错误校验 -->
            <div v-if="getRegexError(cond)" class="regex-error-tip">
              {{ getRegexError(cond) }}
            </div>
          </div>

          <!-- 删除单行按钮 -->
          <el-button
            type="danger"
            link
            class="delete-btn"
            @click="removeCondition(index)"
          >
            <el-icon><Delete /></el-icon>
          </el-button>
        </div>

        <!-- 空条件引导 -->
        <div v-if="localAdvanced.conditions.length === 0" class="empty-conditions">
          <p class="empty-tip">暂无高级条件，点击下方按钮添加条件</p>
        </div>

        <!-- 添加条件按钮 -->
        <div class="add-bar">
          <el-button
            type="primary"
            plain
            size="small"
            :disabled="localAdvanced.conditions.length >= 10"
            @click="addCondition"
          >
            + 添加筛选条件 ({{ localAdvanced.conditions.length }}/10)
          </el-button>
        </div>
      </div>

      <!-- 实时匹配预览 -->
      <div class="preview-box">
        <div class="preview-info">
          <span>🎯 当前条件匹配预览：</span>
          <span v-if="previewLoading" class="preview-loading">计算中...</span>
          <span v-else class="preview-count">
            <strong>{{ previewTotal }}</strong> 条日志
          </span>
        </div>
        <div class="preview-hint">
          * 预览结果已实时叠加左栏的设备、时间、级别等基础过滤条件
        </div>
      </div>
    </div>

    <!-- 底部操作条 -->
    <template #footer>
      <div class="drawer-footer">
        <el-button @click="clearAdvanced">清空条件</el-button>
        <el-button
          type="warning"
          :disabled="previewTotal <= 0"
          @click="openBatchTagDialog"
        >
          🏷 给这 {{ previewTotal }} 条打标签…
        </el-button>
        <el-button type="primary" @click="applyFilters">
          应用筛选并查看
        </el-button>
      </div>
    </template>

    <!-- 批量打标确认弹窗 -->
    <el-dialog
      v-model="batchTagDialogVisible"
      title="按当前筛选结果批量打标签"
      width="440px"
      append-to-body
      destroy-on-close
    >
      <div class="batch-tag-body">
        <p class="batch-tag-tip">
          将为当前筛选命中的 <strong>{{ previewTotal }}</strong> 条日志添加指定标签：
        </p>

        <el-form label-position="top">
          <el-form-item label="选择已有标签">
            <el-select
              v-model="selectedTagId"
              placeholder="选择已有标签"
              style="width: 100%"
              filterable
            >
              <el-option
                v-for="t in tagStore.tags"
                :key="t.id"
                :label="t.name"
                :value="t.id"
              >
                <div style="display: flex; align-items: center; justify-content: space-between">
                  <span>{{ t.name }}</span>
                  <span
                    :style="{
                      display: 'inline-block',
                      width: '12px',
                      height: '12px',
                      borderRadius: '50%',
                      backgroundColor: t.color || '#409EFF'
                    }"
                  />
                </div>
              </el-option>
            </el-select>
          </el-form-item>

          <el-divider>或快速新建标签</el-divider>

          <el-form-item label="新标签名称">
            <el-input
              v-model="newTagName"
              placeholder="如: BGP振荡 / 待复核 / 核心接口"
              maxlength="32"
            />
          </el-form-item>
          <el-form-item label="标签颜色">
            <el-color-picker v-model="newTagColor" :predefine="predefineColors" />
          </el-form-item>
        </el-form>
      </div>
      <template #footer>
        <el-button @click="batchTagDialogVisible = false">取消</el-button>
        <el-button
          type="primary"
          :loading="taggingLoading"
          @click="confirmBatchTag"
        >
          确认打标
        </el-button>
      </template>
    </el-dialog>
  </el-drawer>
</template>

<script setup>
import { ref, reactive, computed, watch } from 'vue'
import { Delete } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import useFilterStore from '@/stores/filter'
import useTagStore from '@/stores/tag'

const props = defineProps({
  taskId: {
    type: String,
    required: true
  }
})

const emit = defineEmits(['apply'])

const filterStore = useFilterStore()
const tagStore = useTagStore()

const visible = ref(false)
const previewLoading = ref(false)
const previewTotal = ref(0)

const localAdvanced = reactive({
  logic: 'AND',
  conditions: []
})

// 打开 Drawer
const open = () => {
  const current = filterStore.filters.advanced || { logic: 'AND', conditions: [] }
  localAdvanced.logic = current.logic || 'AND'
  localAdvanced.conditions = JSON.parse(JSON.stringify(current.conditions || []))
  if (localAdvanced.conditions.length === 0) {
    addCondition()
  }
  visible.value = true
  tagStore.fetchTags(props.taskId)
  fetchPreviewCount()
}

defineExpose({ open })

// 添加条件
const addCondition = () => {
  if (localAdvanced.conditions.length >= 10) return
  localAdvanced.conditions.push({
    field: 'raw_log',
    op: 'contains',
    value: ''
  })
  debouncedPreview()
}

// 移除条件
const removeCondition = (index) => {
  localAdvanced.conditions.splice(index, 1)
  debouncedPreview()
}

// 字段与操作符联动配置
const operatorMap = {
  raw_log: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '精确等于', value: 'eq' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' }
  ],
  message_body: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '正则不匹配', value: 'not_regex' },
    { label: '精确等于', value: 'eq' },
    { label: '前缀匹配', value: 'prefix' },
    { label: '后缀匹配', value: 'suffix' }
  ],
  brief: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '多值包含 (逗号分隔)', value: 'in' },
    { label: '多值排除 (逗号分隔)', value: 'not_in' },
    { label: '精确等于', value: 'eq' }
  ],
  module: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '多值包含 (逗号分隔)', value: 'in' },
    { label: '多值排除 (逗号分隔)', value: 'not_in' },
    { label: '精确等于', value: 'eq' }
  ],
  hostname: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '正则匹配', value: 'regex' },
    { label: '精确等于', value: 'eq' }
  ],
  slot_info: [
    { label: '包含', value: 'contains' },
    { label: '不包含', value: 'not_contains' },
    { label: '精确等于', value: 'eq' }
  ],
  source_file: [
    { label: '精确等于', value: 'eq' },
    { label: '包含', value: 'contains' },
    { label: '前缀匹配', value: 'prefix' }
  ],
  parameters: [
    { label: 'KV 包含 (如 PeerIP=10.)', value: 'kv_contains' },
    { label: 'KV 不包含', value: 'kv_not_contains' },
    { label: 'KV 精确等于 (如 PeerIP=10.1.1.1)', value: 'kv_eq' },
    { label: 'KV 正则 (如 PeerIP=^10\\.)', value: 'kv_regex' },
    { label: 'KV 正则不匹配', value: 'kv_not_regex' }
  ],
  severity: [
    { label: '级别 <=', value: 'lte' },
    { label: '级别 >=', value: 'gte' },
    { label: '级别 =', value: 'eq' },
    { label: '多级别 (1,2,3)', value: 'in' }
  ],
  match_tier: [
    { label: '等于 (EXACT/MNEMONIC等)', value: 'eq' },
    { label: '包含于', value: 'in' },
    { label: '不属于', value: 'not_in' }
  ],
  match_confidence: [
    { label: '置信度 >=', value: 'gte' },
    { label: '置信度 <=', value: 'lte' }
  ],
  knowledge_id: [
    { label: '知识ID >', value: 'gt' },
    { label: '知识ID =', value: 'eq' }
  ]
}

const getAvailableOps = (field) => {
  return operatorMap[field] || operatorMap.raw_log
}

const onFieldChange = (cond) => {
  const ops = getAvailableOps(cond.field)
  if (ops.length > 0) {
    cond.op = ops[0].value
  }
  debouncedPreview()
}

const getValuePlaceholder = (cond) => {
  if (cond.field === 'parameters') {
    return 'Key=Value，例如: PeerIP=10.1.1.1'
  }
  if (cond.op === 'regex' || cond.op === 'not_regex') {
    return '输入标准 RE2 正则表达式'
  }
  if (cond.op === 'in' || cond.op === 'not_in') {
    return '多个值以英文逗号分隔'
  }
  return '输入筛选值'
}

// 正则校验提示
const getRegexError = (cond) => {
  if (!cond.value) return ''
  if (cond.op === 'regex' || cond.op === 'not_regex' || cond.op === 'kv_regex' || cond.op === 'kv_not_regex') {
    let pat = cond.value
    if (cond.field === 'parameters') {
      const parts = pat.split('=')
      if (parts.length < 2) {
        return '参数格式必须为 Key=Regex'
      }
      pat = parts.slice(1).join('=')
    }
    // 检测 RE2 不支持的语法
    if (pat.includes('(?<=') || pat.includes('(?<!') || pat.includes('(?=')) {
      return '提示: SQLite RE2 引擎不支持零宽断言/环视'
    }
    try {
      new RegExp(pat)
    } catch (e) {
      return `语法错误: ${e.message}`
    }
  }
  return ''
}

const onValueInput = (cond) => {
  debouncedPreview()
}

// 基础筛选概要
const baseFilterSummary = computed(() => {
  const f = filterStore.filters
  const list = []
  if (f.keyword) list.push({ label: '关键字', value: f.keyword })
  if (f.severity !== null && f.severity !== undefined) list.push({ label: '严重级别 ≤', value: f.severity })
  if (f.deviceId) list.push({ label: '设备ID', value: f.deviceId })
  if (f.module) list.push({ label: '模块', value: f.module })
  if (f.timeStart || f.timeEnd) {
    list.push({ label: '时间范围', value: `${f.timeStart || '始'} ~ ${f.timeEnd || '末'}` })
  }
  if (Array.isArray(f.tagIds) && f.tagIds.length > 0) {
    list.push({ label: '标签数', value: `${f.tagIds.length} 个` })
  }
  return list
})

// 防抖实时预览
let timer = null
const debouncedPreview = () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    fetchPreviewCount()
  }, 350)
}

const fetchPreviewCount = async () => {
  if (!props.taskId) return
  previewLoading.value = true
  try {
    const validConds = localAdvanced.conditions.filter(
      c => c.field && c.op && c.value !== undefined && c.value !== '' && !getRegexError(c)
    )
    const baseBody = filterStore.toLogQueryBody(1, 1)
    baseBody.advanced = {
      logic: localAdvanced.logic || 'AND',
      conditions: validConds
    }

    const res = await api.queryTaskLogsUnified(props.taskId, baseBody)
    if (res.code === 0) {
      previewTotal.value = res.data.total || 0
    }
  } catch (e) {
    console.error('Fetch preview count failed:', e)
  } finally {
    previewLoading.value = false
  }
}

// 清空高级筛选
const clearAdvanced = () => {
  localAdvanced.conditions = []
  filterStore.filters.advanced = { logic: 'AND', conditions: [] }
  debouncedPreview()
}

// 应用筛选
const applyFilters = () => {
  const validConds = localAdvanced.conditions.filter(
    c => c.field && c.op && c.value !== undefined && c.value !== '' && !getRegexError(c)
  )
  filterStore.filters.advanced = {
    logic: localAdvanced.logic || 'AND',
    conditions: validConds
  }
  filterStore.resetPagination()
  visible.value = false
  emit('apply')
}

// 批量打标
const batchTagDialogVisible = ref(false)
const selectedTagId = ref(null)
const newTagName = ref('')
const newTagColor = ref('#F56C6C')
const taggingLoading = ref(false)
const predefineColors = [
  '#F56C6C', '#E6A23C', '#67C23A', '#409EFF', '#909399', '#795548', '#9C27B0'
]

const openBatchTagDialog = () => {
  selectedTagId.value = tagStore.tags.length > 0 ? tagStore.tags[0].id : null
  newTagName.value = ''
  batchTagDialogVisible.value = true
}

const confirmBatchTag = async () => {
  let targetTagId = selectedTagId.value
  taggingLoading.value = true
  try {
    // 若输入了新标签名，优先创建新标签
    if (newTagName.value.trim()) {
      const created = await tagStore.createTag(props.taskId, {
        name: newTagName.value.trim(),
        color: newTagColor.value
      })
      if (!created) return
      targetTagId = created.id
    }

    if (!targetTagId) {
      ElMessage.warning('请选择或输入要打的标签')
      return
    }

    const validConds = localAdvanced.conditions.filter(
      c => c.field && c.op && c.value !== undefined && c.value !== '' && !getRegexError(c)
    )
    const baseBody = filterStore.toLogQueryBody(1, 50)
    baseBody.advanced = {
      logic: localAdvanced.logic || 'AND',
      conditions: validConds
    }

    const res = await api.batchTagLogs(props.taskId, targetTagId, baseBody)
    if (res.code === 0) {
      ElMessage.success(`成功为 ${res.data.tagged_count} 条日志添加标签`)
      batchTagDialogVisible.value = false
      // 自动把该标签加入当前筛选勾选，形成闭环
      if (!filterStore.filters.tagIds.includes(targetTagId)) {
        filterStore.filters.tagIds.push(targetTagId)
      }
      await tagStore.fetchTags(props.taskId)
      applyFilters()
    }
  } catch (e) {
    ElMessage.error(e.message || '批量打标失败')
  } finally {
    taggingLoading.value = false
  }
}
</script>

<style scoped>
.drawer-container {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.base-filter-alert {
  margin-bottom: 4px;
}
.alert-title {
  font-size: 13px;
  font-weight: 600;
}
.base-tags {
  margin-top: 6px;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.base-tag-item {
  font-size: 12px;
}
.logic-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  background: var(--el-fill-color-light, #f5f7fa);
  padding: 8px 12px;
  border-radius: 6px;
}
.logic-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-regular, #606266);
}
.conditions-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.condition-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  background: #fff;
  border: 1px solid var(--el-border-color-lighter, #ebeef5);
  padding: 8px 10px;
  border-radius: 6px;
  transition: border-color 0.2s;
}
.condition-row:hover {
  border-color: var(--el-color-primary-light-5, #a0cfff);
}
.row-index {
  font-size: 12px;
  font-weight: 600;
  color: #909399;
  line-height: 32px;
  width: 20px;
}
.field-select {
  width: 175px;
  flex-shrink: 0;
}
.op-select {
  width: 140px;
  flex-shrink: 0;
}
.value-input-wrapper {
  flex: 1;
  display: flex;
  flex-direction: column;
}
.regex-error-tip {
  font-size: 11px;
  color: #f56c6c;
  margin-top: 3px;
  line-height: 1.2;
}
.delete-btn {
  line-height: 32px;
  padding: 0 4px;
}
.empty-conditions {
  text-align: center;
  padding: 24px 0;
  color: #909399;
  font-size: 13px;
  border: 1px dashed var(--el-border-color, #dcdfe6);
  border-radius: 6px;
}
.add-bar {
  display: flex;
  justify-content: flex-start;
}
.preview-box {
  background: #fdf6ec;
  border: 1px solid #faecd8;
  padding: 12px 16px;
  border-radius: 6px;
  margin-top: 8px;
}
.preview-info {
  font-size: 14px;
  color: #e6a23c;
  display: flex;
  align-items: center;
}
.preview-count {
  font-size: 16px;
  color: #f56c6c;
  margin-left: 4px;
}
.preview-loading {
  font-size: 13px;
  color: #909399;
  margin-left: 6px;
}
.preview-hint {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
}
.drawer-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
}
.batch-tag-tip {
  font-size: 13px;
  color: #606266;
  margin-bottom: 12px;
}
</style>
