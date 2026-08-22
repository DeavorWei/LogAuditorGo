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
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router
