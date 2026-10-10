<template>
  <Teleport to="body">
    <div
      v-if="visible && log"
      class="quicklook-popover-mask"
      :style="positionStyle"
    >
      <div class="quicklook-card">
        <div class="quicklook-header">
          <div class="header-left">
            <span :class="['sev-tag', sevClass]">Lv.{{ log.severity }}</span>
            <span class="log-title">{{ log.module }}/{{ log.brief }}</span>
            <span v-if="log.hostname" class="host-pill">{{ log.hostname }}</span>
          </div>
          <div class="header-right">
            <span class="tip-text">松开空格关闭 · 单击打开深度双栏解析</span>
          </div>
        </div>

        <!-- 原始报文 (300 字符安全截断，§6.3) -->
        <div class="quicklook-body">
          <div class="section-label">原始 Syslog 报文 (前 300 字符):</div>
          <div class="raw-code-box">{{ truncatedRawLog }}</div>

          <!-- 提取关键变量胶囊 (§6.3) -->
          <div v-if="keyParams.length > 0" class="quicklook-params-row">
            <span class="params-label">关键变量:</span>
            <div class="params-pills">
              <span v-for="p in keyParams" :key="p.key" class="param-pill">
                <span class="p-key">{{ p.key }}:</span>
                <span class="p-val">{{ p.value }}</span>
              </span>
            </div>
          </div>

          <!-- 语义摘要预览 -->
          <div v-else-if="log.event_summary" class="summary-box">
            <span class="summary-label">语义摘要:</span>
            <span class="summary-text">{{ log.event_summary }}</span>
          </div>
        </div>

        <div class="quicklook-footer">
          <span class="footer-time">⏰ {{ log.timestamp ? String(log.timestamp).replace('T', ' ').substring(0, 19) : '-' }}</span>
          <span v-if="log.source_file" class="footer-file">📄 {{ log.source_file }}</span>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  log: {
    type: Object,
    default: () => null
  },
  anchorPos: {
    type: Object,
    default: () => null
  }
})

const sevClass = computed(() => {
  if (!props.log) return 'sev-info'
  const sev = props.log.severity
  if (sev <= 2) return 'sev-crit'
  if (sev <= 4) return 'sev-err'
  if (sev <= 5) return 'sev-warn'
  return 'sev-info'
})

// 原始报文前 300 字符截断
const truncatedRawLog = computed(() => {
  if (!props.log?.raw_log) return ''
  const text = props.log.raw_log
  if (text.length <= 300) return text
  return text.slice(0, 300) + '... (已截断，单击卡片查看完整解析)'
})

// 提取关键变量胶囊 (§6.3)
const keyParams = computed(() => {
  if (!props.log) return []
  let raw = props.log.parameters_json
  if (!raw && props.log.params) raw = props.log.params
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch (e) {
      return []
    }
  }
  if (!raw || typeof raw !== 'object') return []
  return Object.entries(raw).slice(0, 6).map(([key, value]) => ({ key, value }))
})

// 动态悬浮定位算法 (鼠标上方 12px + 边界碰撞检测)
const positionStyle = computed(() => {
  const cardW = 640
  const cardH = 260

  if (props.anchorPos && typeof props.anchorPos.x === 'number' && typeof props.anchorPos.y === 'number') {
    const { x, y } = props.anchorPos
    let left = x - cardW / 2
    if (left < 16) left = 16
    if (left + cardW > (window.innerWidth - 16)) {
      left = Math.max(16, window.innerWidth - cardW - 16)
    }

    let top = y - cardH - 12
    if (top < 16) {
      top = y + 16
    }

    return {
      position: 'fixed',
      left: `${left}px`,
      top: `${top}px`,
      zIndex: 3500
    }
  }

  // 兜底居中
  return {
    position: 'fixed',
    top: '50%',
    left: '50%',
    transform: 'translate(-50%, -50%)',
    zIndex: 3500
  }
})
</script>

<style scoped>
.quicklook-popover-mask {
  pointer-events: none; /* 穿透，不阻断鼠标点击事件 */
  animation: ql-fade-in 0.1s ease-out;
}

@keyframes ql-fade-in {
  from { opacity: 0; transform: scale(0.98); }
  to { opacity: 1; transform: scale(1); }
}

.quicklook-card {
  width: 640px;
  max-width: 90vw;
  background: #0f172a;
  color: #f8fafc;
  border: 1px solid #334155;
  border-radius: 8px;
  box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.1);
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.quicklook-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid #1e293b;
  padding-bottom: 8px;
}

.header-left {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sev-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 3px;
  font-weight: 700;
}
.sev-crit { background: #ef4444; color: #fff; }
.sev-err  { background: #f59e0b; color: #fff; }
.sev-warn { background: #eab308; color: #000; }
.sev-info { background: #475569; color: #fff; }

.log-title {
  font-size: 13px;
  font-weight: 600;
  color: #38bdf8;
}

.host-pill {
  font-size: 11px;
  background: #1e293b;
  color: #94a3b8;
  padding: 1px 6px;
  border-radius: 3px;
}

.tip-text {
  font-size: 11px;
  color: #64748b;
}

.section-label {
  font-size: 11px;
  color: #94a3b8;
  margin-bottom: 4px;
}

.raw-code-box {
  font-family: monospace;
  font-size: 12px;
  background: #020617;
  color: #38bdf8;
  border: 1px solid #1e293b;
  padding: 10px;
  border-radius: 4px;
  line-height: 1.5;
  word-break: break-all;
  max-height: 180px;
  overflow-y: auto;
}

.quicklook-params-row {
  margin-top: 8px;
  display: flex;
  align-items: center;
  gap: 6px;
}

.params-label {
  font-size: 11px;
  color: #94a3b8;
  flex-shrink: 0;
}

.params-pills {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.param-pill {
  font-size: 11px;
  background: #1e293b;
  border: 1px solid #334155;
  border-radius: 4px;
  padding: 2px 6px;
  display: inline-flex;
  gap: 4px;
}

.param-pill .p-key {
  color: #38bdf8;
  font-weight: 600;
}

.param-pill .p-val {
  color: #34d399;
  font-family: monospace;
}

.summary-box {
  margin-top: 8px;
  background: #1e293b;
  padding: 6px 10px;
  border-radius: 4px;
  font-size: 12px;
  display: flex;
  gap: 6px;
}

.summary-label {
  color: #38bdf8;
  font-weight: 600;
  flex-shrink: 0;
}

.summary-text {
  color: #e2e8f0;
}

.quicklook-footer {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: #64748b;
  border-top: 1px solid #1e293b;
  padding-top: 6px;
}
</style>
