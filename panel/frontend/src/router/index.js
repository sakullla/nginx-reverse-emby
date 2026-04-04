import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    name: 'dashboard',
    component: () => import('../pages/DashboardPage.vue'),
    meta: { title: '首页' }
  },
  {
    path: '/agents',
    name: 'agents',
    component: () => import('../pages/AgentsPage.vue'),
    meta: { title: '节点管理' }
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('../pages/RulesPage.vue'),
    meta: { title: 'HTTP 规则' }
  },
  {
    path: '/rules/:id',
    name: 'rule-detail',
    component: () => import('../pages/RuleDetailPage.vue'),
    meta: { title: '规则详情' }
  },
  {
    path: '/l4',
    name: 'l4',
    component: () => import('../pages/L4RulesPage.vue'),
    meta: { title: 'L4 规则' }
  },
  {
    path: '/l4/:id',
    name: 'l4-detail',
    component: () => import('../pages/L4RulesPage.vue'),
    meta: { title: 'L4 规则详情' }
  },
  {
    path: '/certs',
    name: 'certs',
    component: () => import('../pages/CertsPage.vue'),
    meta: { title: '证书' }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('../pages/SettingsPage.vue'),
    meta: { title: '设置' }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router
