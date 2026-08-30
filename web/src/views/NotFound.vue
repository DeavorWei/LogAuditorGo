<template>
  <!-- WEB-09: 原实现没有 catch-all 路由，访问非法 URL 会渲染空白页面且无任何提示 -->
  <div class="not-found-page">
    <el-result icon="warning" title="404" :sub-title="`未找到页面：${route.fullPath}`">
      <template #extra>
        <el-button type="primary" @click="goHome">返回仪表盘</el-button>
        <el-button @click="goBack">返回上一页</el-button>
      </template>
    </el-result>
  </div>
</template>

<script setup>
import { useRoute, useRouter } from 'vue-router'

const route = useRoute()
const router = useRouter()

const goHome = () => router.push('/')
const goBack = () => {
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push('/')
  }
}
</script>

<style scoped>
.not-found-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 60vh;
}
</style>
