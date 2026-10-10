<template>
  <div
    :class="[
      'audit-log-card',
      sevColorClass,
      { active: active }
    ]"
    :data-log-id="record.id"
    @click="$emit('click', record)"
    @mouseenter="$emit('hover-enter', record, $event)"
    @mouseleave="$emit('hover-leave', record)"
  >
    <!-- 头部: 级别徽标 + 模块/简名 + 匹配标签 -->
    <div class="log-card-header">
      <span :class="['sev-tag', sevClass]">Lv.{{ record.severity }}</span>
      <span class="log-mod" :title="`${record.module}/${record.brief}`">
        {{ record.module }}/{{ record.brief }}
      </span>
      <span v-if="record.knowledge_id > 0" class="match-tag" :title="`知识库匹配: ${record.match_tier}`">
        {{ record.match_tier }}
      </span>
      <span v-else-if="record.module === 'COMMENT'" class="comment-tag">注释</span>
    </div>

    <!-- 中部: 事件语义解析摘要 (或原始报文回退)，两行严格截断 -->
    <div
      class="log-card-msg"
      :title="record.event_summary || record.raw_log"
    >
      {{ record.event_summary || record.raw_log }}
    </div>

    <!-- 底部: 时戳 + 主机名 + 来源文件 + 槽位 -->
    <div class="log-card-footer">
      <span class="log-time">{{ displayTime }}</span>
      <div class="log-meta-tags">
        <span v-if="record.hostname" class="host-tag" :title="`主机: ${record.hostname}`">
          {{ record.hostname }}
        </span>
        <span v-if="record.source_file" class="file-tag" :title="`来源文件: ${record.source_file}`">
          📄 {{ record.source_file }}
        </span>
        <span v-if="record.slot_info" class="slot-tag" :title="`槽位: ${record.slot_info}`">
          {{ record.slot_info }}
        </span>
      </div>
    </div>

    <!-- 卡片标签色块 -->
    <div v-if="record.tags && record.tags.length > 0" class="log-card-tags">
      <el-tag
        v-for="tag in record.tags.slice(0, 3)"
        :key="tag.id"
        size="small"
        effect="dark"
        :color="tag.color || '#409EFF'"
        class="card-tag-pill"
      >
        {{ tag.name }}
      </el-tag>
      <el-tooltip
        v-if="record.tags.length > 3"
        :content="record.tags.slice(3).map(t => t.name).join(', ')"
        placement="top"
      >
        <el-tag size="small" type="info" class="card-tag-pill">+{{ record.tags.length - 3 }}</el-tag>
      </el-tooltip>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatTime } from '@/utils/format'

const props = defineProps({
  record: {
    type: Object,
    required: true
  },
  active: {
    type: Boolean,
    default: false
  }
})

defineEmits(['click', 'hover-enter', 'hover-leave'])

// 级别背景色与文字样式类
const sevClass = computed(() => {
  const sev = props.record.severity
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
})

// 左侧 4px 严重度指示边条色彩映射 (建议 5)
const sevColorClass = computed(() => {
  if (props.record.module === 'COMMENT') return 'sev-comment'
  const sev = props.record.severity
  if (sev <= 2) return 'sev-crit-bar'
  if (sev <= 4) return 'sev-err-bar'
  if (sev <= 6) return 'sev-warn-bar'
  return 'sev-info-bar'
})

const displayTime = computed(() => {
  const r = props.record
  if (!r) return '-'
  if (r.module === 'COMMENT' && (!r.timestamp || String(r.timestamp).startsWith('0001-01-01'))) {
    return '— (注释行无时戳)'
  }
  return formatTime(r.timestamp)
})
</script>

<style scoped>
.audit-log-card {
  position: relative;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 8px 10px 8px 14px; /* 左内边距为指示边条留足间隙 */
  cursor: pointer;
  transition: all 0.15s ease;
  min-height: 112px;
  max-height: 124px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  overflow: hidden;
  min-width: 0; /* 阻止 Grid 轨道撑破 */
}

/* 4px 严重度彩色指示竖条 (Left Accent Strip) */
.audit-log-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 4px;
  background-color: var(--sev-bar-color, #94a3b8);
  border-top-left-radius: 6px;
  border-bottom-left-radius: 6px;
  transition: width 0.15s ease;
}

.audit-log-card:hover::before {
  width: 6px; /* 悬浮微动效，凸显当前聚焦 */
}

/* 严重度色条色彩变量 */
.audit-log-card.sev-crit-bar {
  --sev-bar-color: #ef4444; /* Lv.0 ~ Lv.2 紧急深红 */
}
.audit-log-card.sev-err-bar {
  --sev-bar-color: #f59e0b; /* Lv.3 ~ Lv.4 告警橙黄 */
}
.audit-log-card.sev-warn-bar {
  --sev-bar-color: #3b82f6; /* Lv.5 ~ Lv.6 通知亮蓝 */
}
.audit-log-card.sev-info-bar {
  --sev-bar-color: #94a3b8; /* Lv.7 调试信息浅灰 */
}
.audit-log-card.sev-comment {
  --sev-bar-color: #64748b; /* 注释行石板灰 */
}

.audit-log-card:hover {
  border-color: #94a3b8;
  background: #f8fafc;
  transform: translateY(-1px);
  box-shadow: 0 4px 8px -2px rgba(0, 0, 0, 0.05);
}

.audit-log-card.active {
  border-color: #38bdf8;
  background: #f0f9ff;
  box-shadow: 0 0 0 1px #38bdf8;
}

.log-card-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}

.sev-tag {
  font-size: 10px;
  padding: 1px 4px;
  border-radius: 3px;
  font-weight: 700;
  flex-shrink: 0;
}
.sev-crit { background: #fee2e2; color: #991b1b; }
.sev-err  { background: #ffedd5; color: #9a3412; }
.sev-warn { background: #fef9c3; color: #854d0e; }
.sev-info { background: #f1f5f9; color: #475569; }

.log-mod {
  font-weight: 600;
  color: #0f172a;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.match-tag {
  background: #dcfce7;
  color: #166534;
  font-size: 10px;
  padding: 1px 4px;
  border-radius: 3px;
  flex-shrink: 0;
}

.comment-tag {
  background: #f1f5f9;
  color: #475569;
  border: 1px solid #cbd5e1;
  font-size: 10px;
  padding: 1px 4px;
  border-radius: 3px;
  flex-shrink: 0;
}

.log-card-msg {
  font-size: 11px;
  color: #475569;
  margin: 3px 0;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  line-height: 1.45;
  height: 32px;
  word-break: break-all;
}

.log-card-footer {
  font-size: 10px;
  color: #94a3b8;
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
  margin-top: 2px;
}

.log-time {
  font-family: monospace;
  color: #64748b;
  flex-shrink: 0;
}

.log-meta-tags {
  display: flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
}

.host-tag {
  background: #e0f2fe;
  color: #0369a1;
  padding: 0 4px;
  border-radius: 2px;
  max-width: 90px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-tag {
  background: #f1f5f9;
  color: #475569;
  padding: 0 4px;
  border-radius: 2px;
  max-width: 110px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.slot-tag {
  background: #e2e8f0;
  padding: 0 4px;
  border-radius: 2px;
  color: #475569;
}

.log-card-tags {
  display: flex;
  gap: 4px;
  margin-top: 4px;
  flex-wrap: nowrap;
  overflow: hidden;
}

.card-tag-pill {
  font-size: 10px;
  height: 18px;
  line-height: 16px;
  padding: 0 4px;
  border: none;
}
</style>
