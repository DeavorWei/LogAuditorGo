<template>
  <AdvancedFilterModal
    ref="modalRef"
    v-bind="$attrs"
    :task-id="taskId"
    :task-devices="taskDevices"
    @apply="(...args) => $emit('apply', ...args)"
    @reset="(...args) => $emit('reset', ...args)"
  />
</template>

<script setup>
import { ref, computed } from 'vue'
import AdvancedFilterModal from './AdvancedFilterModal.vue'

defineProps({
  taskId: {
    type: String,
    default: ''
  },
  taskDevices: {
    type: Array,
    default: () => []
  }
})

defineEmits(['apply', 'reset'])

const modalRef = ref(null)

const open = (...args) => modalRef.value?.open(...args)
const close = (...args) => modalRef.value?.close(...args)
const openWithCondition = (...args) => modalRef.value?.openWithCondition(...args)
const openWithTimePivot = (...args) => modalRef.value?.openWithTimePivot(...args)

defineExpose({
  open,
  close,
  openWithCondition,
  openWithTimePivot,
  visible: computed(() => modalRef.value?.visible)
})
</script>
