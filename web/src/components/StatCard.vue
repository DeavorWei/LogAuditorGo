<template>
  <el-card shadow="hover" class="stat-card" :class="`stat-card--${tone}`">
    <div v-if="icon" class="stat-card__icon">
      <el-icon :size="28"><component :is="icon" /></el-icon>
    </div>
    <div class="stat-card__info">
      <div class="stat-card__val">
        {{ value }}<span v-if="unit" class="stat-card__unit">{{ unit }}</span>
      </div>
      <div class="stat-card__title">{{ title }}</div>
    </div>
  </el-card>
</template>

<script setup>
defineProps({
  icon: { type: [Object, Function, String], default: null },
  value: { type: [String, Number], required: true },
  unit: { type: String, default: '' },
  title: { type: String, required: true },
  tone: { type: String, default: 'brand' } // brand | success | warning | danger | purple
})
</script>

<style scoped>
.stat-card {
  border-radius: var(--radius-lg);
  border-left: 4px solid var(--tone-bar);
  background: var(--bg-surface);
  border-color: var(--border-base);
}

.stat-card--brand   { --tone-bar: var(--brand-500); }
.stat-card--success { --tone-bar: var(--success-strong); }
.stat-card--warning { --tone-bar: var(--warning-strong); }
.stat-card--danger  { --tone-bar: var(--danger-strong); }
.stat-card--purple  { --tone-bar: var(--purple-500); }

.stat-card :deep(.el-card__body) {
  display: flex;
  align-items: center;
  gap: var(--sp-8);
  padding: var(--sp-8) var(--sp-9);
}

.stat-card__icon {
  font-size: var(--fs-3xl);
  color: var(--tone-bar);
  display: flex;
  align-items: center;
  justify-content: center;
}

.stat-card__info {
  display: flex;
  flex-direction: column;
}

.stat-card__val {
  font-size: var(--fs-4xl);
  font-weight: 700;
  color: var(--text-primary);
  line-height: var(--lh-tight);
}

.stat-card__unit {
  font-size: var(--fs-md);
  font-weight: 400;
  color: var(--text-secondary);
  margin-left: var(--sp-1);
}

.stat-card__title {
  font-size: var(--fs-md);
  color: var(--text-secondary);
  margin-top: var(--sp-1);
}
</style>
