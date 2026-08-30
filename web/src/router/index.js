import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    name: 'Dashboard',
    component: () => import('@/views/Dashboard.vue')
  },
  {
    path: '/audit',
    name: 'AuditWorkbench',
    component: () => import('@/views/AuditWorkbench.vue')
  },
  {
    path: '/audit/:id',
    name: 'AuditTaskDetail',
    component: () => import('@/views/AuditWorkbench.vue')
  },
  {
    path: '/knowledge',
    name: 'KnowledgeCenter',
    component: () => import('@/views/KnowledgeCenter.vue')
  },
  {
    path: '/documents',
    name: 'Documents',
    component: () => import('@/views/Documents.vue')
  },
  {
    path: '/tasks',
    name: 'Tasks',
    component: () => import('@/views/Tasks.vue')
  },
  {
    path: '/settings',
    name: 'Settings',
    component: () => import('@/views/Settings.vue')
  },
  {
    // WEB-09: catch-all 兜底路由，必须放在最后，
    // 否则非法 URL 会渲染成空白页面且没有任何提示。
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('@/views/NotFound.vue')
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  // 路由切换后回到顶部，避免长列表翻页后跳转/返回停留在原滚动位置
  scrollBehavior() {
    return { top: 0 }
  }
})

export default router
