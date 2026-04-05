import { createRouter, createWebHistory } from 'vue-router'
import { verifyToken } from '../api'
import { useAuthState } from '../context/useAuthState'

const { clearToken } = useAuthState()

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
        path: 'l4',
        name: 'l4',
        component: () => import('../pages/L4RulesPage.vue'),
        meta: { title: 'L4 规则' }
      },
      {
        path: 'certs',
        name: 'certs',
        component: () => import('../pages/CertsPage.vue'),
        meta: { title: '证书' }
      },
      {
        path: 'relay-listeners',
        name: 'relay-listeners',
        component: () => import('../pages/RelayListenersPage.vue'),
        meta: { title: 'Relay 监听器' }
      },
      {
        path: 'versions',
        name: 'versions',
        component: () => import('../pages/VersionsPage.vue'),
        meta: { title: '版本策略' }
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

// Auth guard - redirect to /login if token is missing or invalid
router.beforeEach(async (to) => {
  // Allow login route through
  if (to.name === 'login') return true

  const token = localStorage.getItem('panel_token')
  if (!token) {
    return { name: 'login' }
  }

  // Token exists — verify it; on failure clear token and redirect to login
  try {
    const valid = await verifyToken(token)
    if (!valid) {
      localStorage.removeItem('panel_token')
      clearToken()
      return { name: 'login' }
    }
    return true
  } catch (err) {
    // Only 401 from /auth/verify means the token is invalid/expired — clear it.
    // Transport errors (network) and 5xx should not destroy a valid session.
    if (err?.response?.status === 401) {
      localStorage.removeItem('panel_token')
      clearToken()
      return { name: 'login' }
    }
    // For any other error (5xx, network), allow navigation to proceed so the
    // page can surface the outage to the user rather than blocking the app entirely.
    return true
  }
})

export default router
