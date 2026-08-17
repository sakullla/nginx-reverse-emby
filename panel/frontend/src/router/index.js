import { createRouter, createWebHistory } from 'vue-router'
import { verifyToken } from '../api'
import { fetchCurrentActor } from '../api/access'
import { useAuthState } from '../context/useAuthState'

const { clearCredentials } = useAuthState()

const AppShell = () => import('../components/layout/AppShell.vue')
const AccessOverview = () => import('../pages/access/AccessOverview.vue')

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
        meta: { title: '证书中心 · 公网证书' }
      },
      {
        path: 'pki',
        name: 'pki',
        component: () => import('../pages/PkiPage.vue'),
        meta: { title: '证书中心 · 内部 PKI' }
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
        path: 'plugins',
        name: 'plugins',
        component: () => import('../pages/plugins/PluginsPage.vue'),
        meta: { title: '已安装插件' }
      },
      {
        path: 'plugins/marketplace',
        name: 'plugin-marketplace',
        component: () => import('../pages/plugins/PluginMarketplacePage.vue'),
        meta: { title: '插件市场' }
      },
      {
        path: 'plugins/repositories',
        name: 'plugin-repositories',
        component: () => import('../pages/plugins/PluginRepositoriesPage.vue'),
        meta: { title: '插件仓库' }
      },
      {
        path: 'plugins/:id',
        name: 'plugin-detail',
        component: () => import('../pages/plugins/PluginDetailPage.vue'),
        meta: { title: '插件详情' }
      },
      {
        path: 'resource-groups',
        name: 'resource-groups',
        component: () => import('../pages/ResourceGroupsPage.vue'),
        meta: { title: '插件资源组' }
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../pages/SettingsPage.vue'),
        meta: { title: '设置' }
      },
      {
        path: 'access',
        name: 'access',
        component: AccessOverview,
        meta: { title: '访问与安全' }
      },
      {
        path: 'access/users',
        name: 'access-users',
        component: () => import('../pages/access/UsersPage.vue'),
        meta: { title: '用户管理' }
      },
      {
        path: 'access/resource-groups',
        name: 'access-resource-groups',
        component: () => import('../pages/access/ResourceGroupsPage.vue'),
        meta: { title: '资源组' }
      }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(window.__NRE_PANEL_BASE__ || import.meta.env.BASE_URL || '/'),
  routes
})

export async function authGuard(to) {
  // Allow login route through
  if (to.name === 'login') return true

  const sessionToken = localStorage.getItem('panel_session')
  const token = localStorage.getItem('panel_token')
  if (!sessionToken && !token) {
    return { name: 'login' }
  }

  // Token exists — verify it; on failure clear token and redirect to login
  try {
    const valid = sessionToken ? !!(await fetchCurrentActor()) : await verifyToken(token)
    if (!valid) {
      clearCredentials()
      return { name: 'login' }
    }
    return true
  } catch (err) {
    // Only 401 from /auth/verify means the token is invalid/expired — clear it.
    // Transport errors (network) and 5xx should not destroy a valid session.
    if (err?.response?.status === 401) {
      clearCredentials()
      return { name: 'login' }
    }
    // For any other error (5xx, network), allow navigation to proceed so the
    // page can surface the outage to the user rather than blocking the app entirely.
    return true
  }
}

router.beforeEach(authGuard)

export default router
