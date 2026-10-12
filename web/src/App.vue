<template>
  <el-container class="app-layout">
    <!-- 沉浸模式/全屏模式下：隐藏 Logauditorgo 顶部标题栏这一行 -->
    <el-header v-show="!isImmersiveWorkbench" class="app-header">
      <div class="logo-title">
        <el-icon class="logo-icon"><Platform /></el-icon>
        <span class="brand-name">LogAuditorGo</span>
        <span class="brand-sub">华为网络设备日志智能分析平台</span>
      </div>
      <el-menu
        :default-active="activeRoute"
        mode="horizontal"
        router
        class="nav-menu"
        background-color="#1e293b"
        text-color="#94a3b8"
        active-text-color="#38bdf8"
      >
        <el-menu-item index="/">
          <el-icon><Odometer /></el-icon>
          <span>仪表盘</span>
        </el-menu-item>
        <el-menu-item index="/audit">
          <el-icon><DataAnalysis /></el-icon>
          <span>日志审计工作台</span>
        </el-menu-item>
        <el-menu-item index="/knowledge">
          <el-icon><Reading /></el-icon>
          <span>知识库中心</span>
        </el-menu-item>
        <el-menu-item index="/tasks">
          <el-icon><List /></el-icon>
          <span>历史任务</span>
        </el-menu-item>
        <el-menu-item index="/documents">
          <el-icon><FolderOpened /></el-icon>
          <span>产品文档管理</span>
        </el-menu-item>
        <el-menu-item index="/settings">
          <el-icon><Setting /></el-icon>
          <span>系统设置</span>
        </el-menu-item>
      </el-menu>

      <!--
        UI-01: 常驻的"运行中任务"指示器。
        进度弹窗被关闭后任务仍在后台执行，此前没有任何入口可以回到弹窗，
        长任务结果等于永久丢失。点击该徽标即可一键拉回进度弹窗。
      -->
      <div v-if="progressStore.hasRunningJob" class="running-jobs-indicator">
        <el-tooltip :content="runningTooltip" placement="bottom">
          <el-badge :value="progressStore.runningCount" :max="9" type="warning">
            <el-button circle type="primary" plain @click="reopenProgressDialog">
              <el-icon class="rotating-icon"><Loading /></el-icon>
            </el-button>
          </el-badge>
        </el-tooltip>
      </div>
    </el-header>

    <el-main class="app-main" :class="{ 'is-immersive': isImmersiveWorkbench }">
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { Loading } from '@element-plus/icons-vue'
import { useProgressStore } from '@/stores/progress'
import { useWorkbenchUIStore } from '@/stores/workbenchUI'

const route = useRoute()
const progressStore = useProgressStore()
const workbenchUIStore = useWorkbenchUIStore()

// 沉浸或物理全屏模式下，隐藏工作台顶栏
const isImmersiveWorkbench = computed(() => {
  return route.path.startsWith('/audit') && workbenchUIStore.isImmersive
})

const activeRoute = computed(() => {
  if (route.path.startsWith('/audit')) return '/audit'
  return route.path
})

const runningTooltip = computed(() => {
  const jobs = progressStore.runningJobs
  if (jobs.length === 0) return '暂无运行中的任务'
  const names = jobs.map((j) => j.message || j.jobId).slice(0, 3).join('；')
  return `${jobs.length} 个任务运行中：${names}（点击回到进度窗口）`
})

/**
 * 点击徽标：广播"重新打开进度窗口"事件。
 *
 * 各视图内的 ImportProgressModal 仍持有自己的 jobId 与业务回调，
 * 由它们自行判断是否响应这个请求——这样既保留了视图各自的
 * "完成后刷新列表"逻辑，又不需要把弹窗状态强行集中到 App 层。
 */
const reopenProgressDialog = () => {
  const first = progressStore.runningJobs[0]
  if (!first) return
  window.dispatchEvent(
    new CustomEvent('logauditorgo:reopen-progress', { detail: { jobId: first.jobId } })
  )
}

// 定期清理过期终态记录，避免 store 无界增长
let pruneTimer = null
onMounted(() => {
  pruneTimer = setInterval(() => progressStore.prune(), 60 * 1000)
})
onUnmounted(() => {
  if (pruneTimer) clearInterval(pruneTimer)
})
</script>

<style>
.app-layout {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.app-header {
  height: var(--app-header-h);
  background-color: var(--bg-dark-2);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--sp-10);
  box-shadow: var(--shadow-2);
  z-index: var(--z-sticky);
}

.logo-title {
  display: flex;
  align-items: center;
  gap: var(--sp-5);
  color: var(--gray-0);
}

.logo-icon {
  font-size: var(--fs-4xl);
  color: var(--brand-400);
}

.brand-name {
  font-size: var(--fs-2xl);
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--text-on-dark);
}

.brand-sub {
  font-size: var(--fs-sm);
  color: var(--text-on-dark-secondary);
  padding-left: var(--sp-4);
  border-left: 1px solid var(--gray-600);
}

.nav-menu {
  border-bottom: none !important;
  height: var(--app-header-h);
  flex: 1;
  min-width: 0;
}

/* UI-01: 运行中任务指示器 */
.running-jobs-indicator {
  display: flex;
  align-items: center;
  margin-left: var(--sp-8);
  flex-shrink: 0;
}

.rotating-icon {
  animation: spin 1.6s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.app-main {
  flex: 1;
  padding: var(--app-main-pad-y) var(--app-main-pad-x);
  overflow: auto;
  transition: padding var(--dur-base) var(--ease-standard);
}

.app-main.is-immersive {
  padding: 0 !important;
  overflow: hidden !important;
}
</style>
