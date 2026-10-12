<template>
  <div class="col-right">
    <div v-if="log" class="knowledge-container">
      <el-tabs v-model="activeTab" class="custom-tabs">
        <el-tab-pane label="官方知识与处理步骤" name="knowledge">
          <div v-if="log.knowledge" class="kb-content">
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
                <span class="badge-tier">匹配层级: {{ log.match_tier }}</span>
                <span class="badge-conf">置信度: {{ (log.match_confidence * 100).toFixed(0) }}%</span>
                <span v-if="contextualizeMode && detail.matchedParamCount > 0" class="badge-ctx">
                  已将现场 {{ detail.matchedParamCount }} 个参数动态注入至排查步骤
                </span>
              </div>
            </div>

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

            <!-- 官方建议处理步骤 -->
            <div class="kb-block is-action">
              <div class="kb-subtitle">
                <el-icon><Tools /></el-icon>
                <span>官方处理排错步骤</span>
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
                <span class="c-val">记录日志生成槽位与运行环境、设备型号及固件版本、防篡改哈希校验（Digest）或文件归档记录，用于日志审计溯源与文件完整性校验</span>
              </div>
              <div class="comment-item">
                <span class="c-label">运维建议：</span>
                <span class="c-val">系统已自动归档为注释性记录，运维人员无需进行故障排查或处置</span>
              </div>
            </div>
          </div>
          <div v-else class="empty-kb">
            <el-empty description="该日志未命中官方知识库，可在知识库中心使用语义搜索进行检索" />
          </div>
        </el-tab-pane>

        <el-tab-pane label="根因传播拓扑 (RCA)" name="rca">
          <div v-if="matchedRca" class="rca-tab-content">
            <RcaGraph :rcaEvent="matchedRca" />
            <div class="rca-guide">
              <div class="guide-title"><el-icon><InfoFilled /></el-icon> 根因处置指南</div>
              <div>{{ matchedRca.recommended_action }}</div>
            </div>
          </div>
          <div v-else class="empty-kb">
            <el-empty description="当前选中日志未触发协议级连环故障传播链路" />
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
    <div v-else class="empty-state">
      <el-empty description="请选择日志查看官方故障知识库与排查建议" />
    </div>
  </div>
</template>

<script setup>
import { ref, computed, inject } from 'vue'
import { InfoFilled, Reading, Search, Tools, Warning, Document } from '@element-plus/icons-vue'
import RcaGraph from '@/components/RcaGraph.vue'
import { LOG_DETAIL_CONTEXT_KEY } from '@/utils/logFormatter'

defineProps({
  log: {
    type: Object,
    default: null
  },
  matchedRca: {
    type: Object,
    default: null
  }
})

const activeTab = ref('knowledge')

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

.custom-tabs {
  height: 100%;
  display: flex;
  flex-direction: column;
}

:deep(.el-tabs__content) {
  flex: 1;
  overflow-y: auto;
  padding-top: 8px;
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

.badge-tier, .badge-conf {
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

.rc-graph-container {
  height: 380px;
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
</style>
