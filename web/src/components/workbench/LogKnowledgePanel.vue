<template>
  <div class="col-right">
    <div v-if="log" class="knowledge-container">
      <template v-if="log.knowledge">
        <div class="kb-content">
          <div class="kb-header-card">
            <div class="kb-header-top">
              <div class="kb-title">{{ log.knowledge.module }}/{{ log.knowledge.brief }}</div>
              <el-switch
                v-if="contextualizeModeRef"
                v-model="contextualizeModeRef"
                size="small"
                active-text="现场参数注入"
                inactive-text="原始文档"
                style="--el-switch-on-color: var(--success-strong);"
              />
            </div>
            <div class="kb-meta">
              <el-tag size="small" :type="tierMeta.tagType">{{ tierMeta.label }}</el-tag>
              <el-tooltip :content="tierMeta.description" placement="top">
                <span class="badge-conf">匹配置信度 {{ confidenceText }}</span>
              </el-tooltip>
              <span v-if="matchedDocName" class="badge-doc">来源文档：{{ matchedDocName }}</span>
              <span v-if="contextualizeMode && detail.matchedParamCount > 0" class="badge-ctx">
                已将现场 {{ detail.matchedParamCount }} 个参数注入排查步骤
              </span>
            </div>
          </div>

          <!-- 匹配依据（可展开，满足"证据优先于断言"原则） -->
          <el-collapse class="match-basis-collapse">
            <el-collapse-item name="basis">
              <template #title>
                <span class="basis-title"><el-icon><InfoFilled /></el-icon> 匹配依据与可解释性</span>
              </template>
              <div class="basis-grid">
                <div class="basis-row"><span>匹配层级</span><strong>{{ log.match_tier || '-' }}</strong></div>
                <div class="basis-row"><span>匹配置信度</span><strong>{{ confidenceText }}</strong></div>
                <div class="basis-row"><span>命中文档</span><strong>{{ matchedDocName || '未记录' }}</strong></div>
                <div class="basis-row"><span>知识条目 ID</span><strong>{{ log.knowledge_id }}</strong></div>
              </div>
              <p class="basis-tip">
                置信度来自匹配层级与版本贴合度；TEMPLATE / BLEVE 层级属于推断匹配，请结合日志原文确认。
              </p>
            </el-collapse-item>
          </el-collapse>

          <!-- 含义 -->
          <div class="kb-block">
            <div class="kb-subtitle">
              <el-icon><Reading /></el-icon>
              <span>日志/告警含义解释</span>
            </div>
            <div class="kb-text" v-html="detail.renderedKnowledgeHtml.description"></div>
          </div>

          <!-- 官方可能原因 -->
          <div class="kb-block is-cause">
            <div class="kb-subtitle">
              <el-icon><Search /></el-icon>
              <span>官方可能原因</span>
            </div>
            <div class="kb-text cause-text" v-html="detail.renderedKnowledgeHtml.cause"></div>
          </div>

          <!-- 参考排查建议 -->
          <div class="kb-block is-action">
            <div class="kb-subtitle">
              <el-icon><Tools /></el-icon>
              <span>参考排查建议（非官方原文）</span>
            </div>
            <div class="kb-text action-box" v-html="detail.renderedKnowledgeHtml.action"></div>
          </div>

          <!-- 系统影响 -->
          <div v-if="log.knowledge.impact" class="kb-block is-impact">
            <div class="kb-subtitle">
              <el-icon><Warning /></el-icon>
              <span>对系统的影响</span>
            </div>
            <div class="kb-text" v-html="detail.renderedKnowledgeHtml.impact"></div>
          </div>

          <!-- 官方参数定义字典与现场对照表 -->
          <div v-if="detail.kbParamDefs && detail.kbParamDefs.length > 0" class="kb-block">
            <div class="kb-subtitle flex-between">
              <div class="kb-subtitle-left">
                <el-icon><Document /></el-icon>
                <span>官方参数字典与现场实际值对照</span>
              </div>
              <span class="dict-count-tag">共 {{ detail.kbParamDefs.length }} 项参数定义</span>
            </div>
            <el-table :data="detail.kbParamDefs" size="small" border class="kb-dict-table">
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
      </template>

      <!-- 注释行说明 -->
      <div v-else-if="log.module === 'COMMENT'" class="comment-card">
        <div class="comment-title">
          <el-icon color="var(--brand-600)" size="18"><InfoFilled /></el-icon>
          <span>注释性日志说明 (Comment / Metadata)</span>
        </div>
        <div class="comment-body">
          <div class="comment-item">
            <span class="c-label">日志类型：</span>
            <span class="c-val">网络设备系统注释或日志文件导出元数据行（以 <code>#</code> 开头）</span>
          </div>
          <div class="comment-item">
            <span class="c-label">业务影响：</span>
            <span class="c-val">非网络故障、协议异常或系统告警事件，对网络业务及设备运行无负面影响</span>
          </div>
          <div class="comment-item">
            <span class="c-label">典型用途：</span>
            <span class="c-val">记录日志生成槽位与运行环境、设备型号及固件版本、防篡改哈希校验（Digest）或文件归档记录</span>
          </div>
          <div class="comment-item">
            <span class="c-label">运维建议：</span>
            <span class="c-val">系统已自动归档为注释性记录，运维人员无需进行故障排查或处置</span>
          </div>
        </div>
      </div>

      <!-- 未匹配：给出可执行的下一步，而不是一片空白 -->
      <div v-else class="unmatched-card">
        <div class="unmatched-header">
          <el-icon color="var(--warning-strong)" size="20"><Warning /></el-icon>
          <div>
            <div class="unmatched-title">该日志暂无官方知识</div>
            <div class="unmatched-sub">
              {{ log.module || '未知模块' }}/{{ log.brief || '未知助记符' }} 未在已导入的 HDX 文档中找到对应条目。
            </div>
          </div>
        </div>
        <div class="unmatched-actions">
          <el-button type="primary" plain size="small" :icon="Search" @click="goKnowledgeSearch">
            去知识库检索
          </el-button>
          <el-button size="small" :icon="CopyDocument" @click="copyModuleBrief">
            复制模块/助记符
          </el-button>
          <el-button size="small" type="success" plain :icon="Plus" @click="showLocalDialog = true">
            补充本地知识
          </el-button>
          <el-button size="small" :icon="Link" @click="showBindDialog = true">
            绑定已有知识
          </el-button>
        </div>
        <div class="unmatched-tips">
          <div class="tips-title">下一步建议</div>
          <ol>
            <li>确认是否已导入对应产品的 HDX 文档（知识库中心 → 产品文档）。</li>
            <li>用模块或助记符在知识库中检索，确认是否为翻译/版本差异。</li>
            <li>把该模块与助记符记录到交接结论中，便于后续补知识。</li>
          </ol>
        </div>
      </div>
    </div>

    <div v-else class="empty-state">
      <el-empty description="请选择日志查看官方故障知识库与排查建议" />
    </div>

    <LocalKnowledgeDialog
      v-model="showLocalDialog"
      :initial="{ module: log?.module, brief: log?.brief }"
      @saved="onKnowledgeSaved"
    />
    <BindKnowledgeDialog
      v-model="showBindDialog"
      :module="log?.module || ''"
      :brief="log?.brief || ''"
      @saved="onKnowledgeSaved"
    />
  </div>
</template>

<script setup>
import { computed, inject, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  InfoFilled, Reading, Search, Tools, Warning, Document, CopyDocument, Plus, Link
} from '@element-plus/icons-vue'
import { LOG_DETAIL_CONTEXT_KEY } from '@/utils/logFormatter'
import { matchTierMeta } from '@/constants/matchTiers'
import LocalKnowledgeDialog from '@/components/LocalKnowledgeDialog.vue'
import BindKnowledgeDialog from '@/components/BindKnowledgeDialog.vue'

const props = defineProps({
  log: {
    type: Object,
    default: null
  },
  taskId: {
    type: String,
    default: ''
  }
})

const router = useRouter()

const showLocalDialog = ref(false)
const showBindDialog = ref(false)

const onKnowledgeSaved = () => {
  ElMessage.success('知识已保存；重新分析或下次导入时生效')
}

// 注入富文本格式化上下文
const logContext = inject(LOG_DETAIL_CONTEXT_KEY, null)
const contextualizeModeRef = logContext?.contextualizeMode
const contextualizeMode = computed(() => !!contextualizeModeRef?.value)

const detail = computed(() => {
  return logContext?.formattedDetail?.value || {
    renderedKnowledgeHtml: { description: '', cause: '', action: '', impact: '' },
    kbParamDefs: [],
    matchedParamCount: 0
  }
})

const tierMeta = computed(() => matchTierMeta(props.log?.match_tier))

const confidenceText = computed(() => {
  const value = Number(props.log?.match_confidence)
  if (!Number.isFinite(value) || value <= 0) return '未知'
  return `${Math.round(Math.min(value, 1) * 100)}%`
})

const matchedDocName = computed(() => props.log?.matched_doc_name || '')

const goKnowledgeSearch = () => {
  const keyword = [props.log?.module, props.log?.brief].filter(Boolean).join('/')
  router.push({ path: '/knowledge', query: keyword ? { keyword } : {} })
}

const copyModuleBrief = async () => {
  const text = [props.log?.module, props.log?.brief].filter(Boolean).join('/')
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(`已复制：${text}`)
  } catch (e) {
    ElMessage.warning('复制失败，请手动选择文本复制')
  }
}
</script>

<style scoped>
.col-right {
  flex: 0 0 var(--wb-col-right, 36%);
  width: var(--wb-col-right, 36%);
  min-width: 320px;
  display: flex;
  flex-direction: column;
  background: var(--gray-0);
  overflow-y: auto;
  box-sizing: border-box;
}

.knowledge-container {
  padding: 12px 16px;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.kb-content {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.kb-header-card {
  background: var(--bg-subtle);
  border: 1px solid var(--border-base);
  border-radius: var(--radius-lg);
  padding: 12px 14px;
}

.kb-header-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.kb-title {
  font-size: var(--fs-lg);
  font-weight: 700;
  color: var(--gray-900);
}

.kb-meta {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}

.badge-conf, .badge-doc {
  font-size: var(--fs-xs);
  background: var(--border-base);
  color: var(--gray-600);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
}

.badge-ctx {
  font-size: var(--fs-xs);
  color: var(--success-text);
  background: var(--inst-injected-bg);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  font-weight: 500;
}

.match-basis-collapse {
  border-top: 1px solid var(--border-base);
  border-bottom: 1px solid var(--border-base);
}

.basis-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.basis-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
}

.basis-row {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
}

.basis-row strong {
  color: var(--text-primary);
  font-weight: 600;
}

.basis-tip {
  margin: 10px 0 0;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
  line-height: 1.6;
}

.dict-count-tag {
  font-size: var(--fs-xs);
  color: var(--brand-600);
}

.dict-pname {
  font-weight: 600;
  color: var(--brand-600);
}

.dict-pval {
  color: var(--success-text);
  font-weight: 600;
}

.dict-pnone {
  color: var(--gray-400);
  font-style: italic;
}

.kb-subtitle-left {
  display: flex;
  align-items: center;
  gap: 6px;
}

.kb-dict-table {
  width: 100%;
  margin-top: 6px;
}

.unmatched-card {
  border: 1px dashed var(--border-base);
  border-radius: var(--radius-lg);
  padding: 16px;
  background: var(--bg-subtle);
}

.unmatched-header {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}

.unmatched-title {
  font-size: var(--fs-lg);
  font-weight: 700;
  color: var(--text-primary);
}

.unmatched-sub {
  margin-top: 4px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.6;
}

.unmatched-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 14px 0;
}

.unmatched-tips {
  border-top: 1px solid var(--border-base);
  padding-top: 12px;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.tips-title {
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 6px;
}

.unmatched-tips ol {
  margin: 0;
  padding-left: 18px;
  line-height: 1.9;
}
</style>
