import { createRouter, createWebHistory } from 'vue-router'
import { verifyToken } from '../api'

const AppShell = () => import('../components/layout/AppShell.vue')

const routes = [
  {
    path: '/login',
    name: 'login',
    component: () => import('../pages/LoginPage.vue'),
    meta: { title: '登录' }
  },
  {
    path: '/',
    component: AppShell,
    children: [
      {
        path: '',
        name: 'dashboard',
        component: () => import('../pages/DashboardPage.vue'),
        meta: { title: '首页' }
      },
      {
        path: 'agents',
        name: 'agents',
        component: () => import('../pages/AgentsPage.vue'),
        meta: { title: '节点管理' }
      },
      {
        path: 'agents/:id',
        name: 'agent-detail',
        component: () => import('../pages/AgentDetailPage.vue'),
        meta: { title: '节点详情' }
      },
      {
        path: 'rules',
        name: 'rules',
        component: () => import('../pages/RulesPage.vue'),
        meta: { title: 'HTTP 规则' }
      },
      {
        path: 'rules/:id',
        name: 'rule-detail',
        component: () => import('../pages/RuleDetailPage.vue'),
        meta: { title: '规则详情' }
      },
      {
        path: 'l4',
        name: 'l4',
        component: () => import('../pages/L4RulesPage.vue'),
        meta: { title: 'L4 规则' }
      },
      {
        path: 'l4/:id',
        name: 'l4-detail',
        component: () => import('../pages/L4RulesPage.vue'),
        meta: { title: 'L4 规则详情' }
      },
      {
        path: 'certs',
        name: 'certs',
        component: () => import('../pages/CertsPage.vue'),
        meta: { title: '证书' }
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../pages/SettingsPage.vue'),
        meta: { title: '设置' }
      }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

// Auth guard - redirect to /login if no valid token
router.beforeEach(async (to) => {
  // Allow login route through
  if (to.name === 'login') return true

  const token = localStorage.getItem('panel_token')
  if (!token) {
    return { name: 'login' }
  }

  try {
    const valid = await verifyToken(token)
    if (!valid) {
      localStorage.removeItem('panel_token')
      return { name: 'login' }
    }
    return true
  } catch {
    // Network error or server unreachable - allow through in case server has no auth configured
    return true
  }
})

export default router
