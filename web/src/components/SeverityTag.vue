<template>
  <span class="sev-tag" :class="tagClass">
    <template v-if="level === 'comment'">注释</template>
    <template v-else>{{ severityLabel }}</template>
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { useSeverity } from '@/composables/useSeverity'
import { severityLabel as semanticSeverityLabel } from '@/utils/severity'

const props = defineProps({
  severity: { type: [Number, String], required: true },
  module: { type: String, default: '' }
})

const { level, tagClass } = useSeverity(
  () => props.severity,
  () => props.module
)

// v1.4: 语义严重度标签（严重/错误/警告/提示 + Lv.N），不再只显示 Lv.N
const severityLabel = computed(() => semanticSeverityLabel(props.severity))
</script>
