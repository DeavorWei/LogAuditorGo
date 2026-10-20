<template>
  <el-tag :type="meta.tagType" :size="size" effect="light" class="task-status-tag">
    <el-icon v-if="meta.busy" class="is-loading status-icon"><Loading /></el-icon>
    <span>{{ meta.label }}</span>
  </el-tag>
</template>

<script setup>
import { computed } from 'vue'
import { Loading } from '@element-plus/icons-vue'
import { taskStatusMeta } from '@/constants/taskStatus'

const props = defineProps({
  status: {
    type: String,
    default: ''
  },
  size: {
    type: String,
    default: 'small'
  }
})

const meta = computed(() => taskStatusMeta(props.status))
</script>

<style scoped>
.task-status-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.status-icon {
  font-size: 12px;
}
</style>
