<template>
  <el-dialog
    :model-value="modelValue"
    title="开始使用 LogAuditorGo"
    width="760px"
    class="dlg dlg-lg onboarding-dialog"
    :close-on-click-modal="false"
    @update:model-value="(val) => $emit('update:modelValue', val)"
    @open="onOpen"
  >
    <p class="intro">
      用 4 步完成第一次日志排障：先让产品认识官方知识，再导入日志，最后从诊断摘要开始排查。
      任何一步都可以暂时跳过，首页会保留清单。
    </p>

    <el-steps :active="store.completedCount" finish-status="success" align-center class="steps">
      <el-step title="导入 HDX" />
      <el-step title="创建任务" />
      <el-step title="导入日志" />
      <el-step title="查看摘要" />
    </el-steps>

    <div class="step-list">
      <div
        v-for="step in steps"
        :key="step.key"
        class="step-item"
        :class="{ done: isDone(step.key) }"
      >
        <div class="step-index">
          <el-icon v-if="isDone(step.key)"><CircleCheckFilled /></el-icon>
          <span v-else>{{ step.key }}</span>
        </div>
        <div class="step-content">
          <div class="step-title">{{ step.title }}</div>
          <div class="step-desc">{{ step.desc }}</div>
          <HdxHelpCard v-if="step.key === 1" compact class="step-hdx" @import-doc="go(step, '/documents')" />
        </div>
        <div class="step-actions">
          <el-button v-if="!isDone(step.key)" size="small" @click="markDone(step.key)">标记完成</el-button>
          <el-button size="small" type="primary" plain @click="go(step, step.route)">
            {{ step.action }}
          </el-button>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="handleSkip">暂时跳过</el-button>
      <el-button @click="handleDismiss">不再提示</el-button>
      <el-button type="primary" @click="close">开始使用</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { CircleCheckFilled } from '@element-plus/icons-vue'
import HdxHelpCard from '@/components/HdxHelpCard.vue'
import { useOnboardingStore } from '@/stores/onboarding'

defineProps({
  modelValue: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue'])
const router = useRouter()
const store = useOnboardingStore()

const steps = [
  { key: 1, title: '导入华为 HDX 产品文档', desc: '官方知识是匹配与“官方含义/原因/步骤”的来源；没有知识库时匹配率会是 0。', action: '去导入文档', route: '/documents' },
  { key: 2, title: '创建排障任务', desc: '一次排障一个任务；设备型号会按日志 Hostname 自动识别，无需手选。', action: '新建任务', route: '/audit/new' },
  { key: 3, title: '导入日志', desc: '用 Windows 原生对话框选择本机日志文件/目录，或粘贴文本；先看预览再分析。', action: '选择日志', route: '/audit/new' },
  { key: 4, title: '查看诊断摘要', desc: '导入完成后先看概览、高频模块、未匹配热点与下一步建议，再逐条排查。', action: '查看案例库', route: '/tasks' }
]

const isDone = (key) => (store.state.completed_steps || []).includes(key)

const onOpen = async () => {
  await store.fetch(true)
}

const markDone = async (key) => {
  await store.markStep(key)
  ElMessage.success(`第 ${key} 步已标记完成`)
}

const go = async (step, route) => {
  if (!isDone(step.key)) {
    await store.markStep(step.key)
  }
  emit('update:modelValue', false)
  router.push(route)
}

const handleSkip = async () => {
  await store.skip()
  emit('update:modelValue', false)
  ElMessage.info('已暂时跳过，首页清单可随时继续')
}

const handleDismiss = async () => {
  await store.dismiss()
  emit('update:modelValue', false)
}

const close = () => emit('update:modelValue', false)

onMounted(() => {
  store.fetch()
})
</script>

<style scoped>
.intro {
  margin: 0 0 16px;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: 1.8;
}

.steps {
  margin-bottom: 16px;
}

.step-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.step-item {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px;
  border: 1px solid var(--border-base);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
}

.step-item.done {
  border-color: var(--success-border, #b7eb8f);
  background: var(--success-bg, #f6ffed);
}

.step-index {
  flex-shrink: 0;
  width: 26px;
  height: 26px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--gray-0);
  border: 1px solid var(--border-base);
  font-weight: 700;
  color: var(--text-secondary);
}

.step-item.done .step-index {
  color: var(--success-strong);
  border-color: var(--success-border, #b7eb8f);
}

.step-content {
  flex: 1;
  min-width: 0;
}

.step-title {
  font-weight: 600;
  color: var(--text-primary);
}

.step-desc {
  margin-top: 2px;
  font-size: var(--fs-sm);
  color: var(--text-secondary);
  line-height: 1.6;
}

.step-hdx {
  margin-top: 8px;
}

.step-actions {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex-shrink: 0;
}
</style>
