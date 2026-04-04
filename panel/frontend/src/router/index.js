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

// Auth guard - redirect to /login if token is invalid; allow through if server has no auth configured
router.beforeEach(async (to) => {
  // Allow login route through
  if (to.name === 'login') return true

  const token = localStorage.getItem('panel_token')
  if (!token) {
    // No token stored — probe /api/info to check if the server requires auth
    try {
      const res = await fetch('/panel-api/info')
      if (res.ok) {
        // Server responded without auth → auth is disabled, allow through
        return true
      }
      // Got a non-401 error (e.g. 404, 500) — allow through
      return true
    } catch {
      // Network error — allow through so the app can show its own error state
      return true
    }
  }

  // Token exists — verify it
  try {
    const valid = await verifyToken(token)
    if (!valid) {
      localStorage.removeItem('panel_token')
      return { name: 'login' }
    }
    return true
  } catch {
    // Network error during verify — allow through (server may be auth-disabled)
    return true
  }
})

export default router
