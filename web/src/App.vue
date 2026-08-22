<template>
  <el-container class="app-layout">
    <el-header class="app-header">
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
      </el-menu>
    </el-header>

    <el-main class="app-main">
      <router-view />
    </el-main>
  </el-container>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const activeRoute = computed(() => {
  if (route.path.startsWith('/audit')) return '/audit'
  return route.path
})
</script>

<style>
* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}
html, body, #app {
  height: 100%;
  width: 100%;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  background-color: #f1f5f9;
  color: #1e293b;
}

.app-layout {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.app-header {
  height: 60px;
  background-color: #1e293b;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  z-index: 100;
}

.logo-title {
  display: flex;
  align-items: center;
  gap: 10px;
  color: #ffffff;
}

.logo-icon {
  font-size: 24px;
  color: #38bdf8;
}

.brand-name {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: #f8fafc;
}

.brand-sub {
  font-size: 12px;
  color: #94a3b8;
  padding-left: 8px;
  border-left: 1px solid #475569;
}

.nav-menu {
  border-bottom: none !important;
  height: 60px;
}

.app-main {
  flex: 1;
  padding: 16px 20px;
  overflow: auto;
}
</style>
