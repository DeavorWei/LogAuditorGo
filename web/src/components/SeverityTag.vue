<template>
  <span class="sev-tag" :class="tagClass">
    <template v-if="level === 'comment'">注释</template>
    <template v-else>Lv.{{ severity }}</template>
  </span>
</template>

<script setup>
import { useSeverity } from '@/composables/useSeverity'

const props = defineProps({
  severity: { type: [Number, String], required: true },
  module: { type: String, default: '' }
})

const { level, tagClass } = useSeverity(
  () => props.severity,
  () => props.module
)
</script>
