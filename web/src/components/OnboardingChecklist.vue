<template>
  <el-card v-if="store.checklistVisible" shadow="never" class="checklist-card">
    <template #header>
      <div class="checklist-header">
        <div class="title">
          <el-icon color="var(--brand-600)"><Guide /></el-icon>
          <span>开始使用（{{ store.completedCount }}/4）</span>
        </div>
        <div class="actions">
          <el-button link type="primary" size="small" @click="$emit('open-guide')">查看引导</el-button>
          <el-button link type="info" size="small" @click="handleDismiss">不再显示</el-button>
        </div>
      </div>
    </template>

    <div class="checklist-items">
      <button
        v-for="item in items"
        :key="item.key"
        type="button"
        class="checklist-item reset"
        :class="{ done: isDone(item.key) }"
        @click="handleClick(item)"
      >
        <el-icon class="check-icon">
          <CircleCheckFilled v-if="isDone(item.key)" />
          <CircleCheck v-else />
        </el-icon>
        <span class="item-label">{{ item.label }}</span>
        <span class="item-action">{{ isDone(item.key) ? '已完成' : item.action }}</span>
      </button>
    </div>
  </el-card>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { CircleCheck, CircleCheckFilled, Guide } from '@element-plus/icons-vue'
import { useOnboardingStore } from '@/stores/onboarding'

defineEmits(['open-guide'])

const router = useRouter()
const store = useOnboardingStore()

const items = [
  { key: 1, label: '导入 HDX 产品文档', action: '去导入', route: '/documents' },
  { key: 2, label: '创建排障任务', action: '去创建', route: '/audit/new' },
  { key: 3, label: '导入日志并分析', action: '去导入', route: '/audit/new' },
  { key: 4, label: '查看诊断摘要', action: '去查看', route: '/tasks' }
]

const isDone = (key) => (store.state.completed_steps || []).includes(key)

const handleClick = async (item) => {
  if (!isDone(item.key)) {
    await store.markStep(item.key)
  }
  router.push(item.route)
}

const handleDismiss = async () => {
  await store.dismiss()
  ElMessage.info('已关闭首页清单，可在“?”帮助中重新打开引导')
}
</script>

<style scoped>
.checklist-card {
  border-radius: var(--radius-lg);
}

.checklist-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}

.title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-weight: 600;
}

.actions {
  display: flex;
  gap: 8px;
}

.checklist-items {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 8px;
}

.checklist-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--border-base);
  border-radius: var(--radius-base);
  background: var(--bg-subtle);
  cursor: pointer;
  text-align: left;
}

.checklist-item.done {
  border-color: var(--success-border, #b7eb8f);
  background: var(--success-bg, #f6ffed);
}

.check-icon {
  color: var(--gray-400);
}

.checklist-item.done .check-icon {
  color: var(--success-strong);
}

.item-label {
  flex: 1;
  font-size: var(--fs-sm);
  color: var(--text-primary);
}

.item-action {
  font-size: var(--fs-xs);
  color: var(--brand-600);
}
</style>
