import { createRouter, createWebHistory } from 'vue-router'
import { verifyToken } from '../api'
import { clearCredentials, clearSessionToken, getStoredAuthToken } from '../api/authState'

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
        path: 'plugins/marketplace/:pluginId',
        name: 'plugin-marketplace-detail',
        component: () => import('../pages/plugins/PluginMarketplaceDetailPage.vue'),
        meta: { title: '插件市场详情' }
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
        redirect: { name: 'plugins' }
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../pages/SettingsPage.vue'),
        meta: { title: '设置' }
      },
      {
        path: 'access',
        redirect: { name: 'dashboard' }
      },
      {
        path: 'access/users',
        redirect: { name: 'dashboard' }
      },
      {
        path: 'access/resource-groups',
        redirect: { name: 'dashboard' }
      }
    ]
  }
]

// `.content` (inside AppShell) is the real scroll container — the window never
// scrolls — so we keep per-route scroll positions ourselves and restore them
// on history back/forward.
const CONTENT_SELECTOR = '.app-shell .content'
const contentScrollPositions = new Map()

function storeContentScroll(route) {
  const el = document.querySelector(CONTENT_SELECTOR)
  if (el) contentScrollPositions.set(route.fullPath, el.scrollTop)
}

const router = createRouter({
  history: createWebHistory(window.__NRE_PANEL_BASE__ || import.meta.env.BASE_URL || '/'),
  routes,
  scrollBehavior(to, from, savedPosition) {
    // The login page renders outside AppShell — no .content container to scroll.
    if (to.name === 'login') return false
    if (savedPosition) {
      return { el: CONTENT_SELECTOR, top: contentScrollPositions.get(to.fullPath) ?? 0 }
    }
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    return { el: CONTENT_SELECTOR, top: 0 }
  }
})

// Preserve the attempted destination so a successful login returns the user
// to the page they actually wanted instead of always landing on the dashboard.
function loginRedirect(to) {
  return to.fullPath ? { name: 'login', query: { return: to.fullPath } } : { name: 'login' }
}

export async function authGuard(to) {
  // Allow login route through
  if (to.name === 'login') return true

  const token = getStoredAuthToken()
  if (!token) {
    return loginRedirect(to)
  }

  try {
    // Drop leftover panel_session so the API client cannot attach Authorization
    // Bearer and let the backend authenticate the session before X-Panel-Token.
    clearSessionToken()
    const valid = await verifyToken(token)
    if (!valid) {
      clearCredentials()
      return loginRedirect(to)
    }
    return true
  } catch (err) {
    // Only 401 from /auth/verify means the token is invalid/expired — clear it.
    // Transport errors (network) and 5xx should not destroy a valid panel token.
    if (err?.response?.status === 401) {
      clearCredentials()
      return loginRedirect(to)
    }
    // For any other error (5xx, network), allow navigation to proceed so the
    // page can surface the outage to the user rather than blocking the app entirely.
    return true
  }
}

router.beforeEach((to, from) => {
  storeContentScroll(from)
})

router.beforeEach(authGuard)

router.afterEach((to) => {
  const title = to.meta?.title
  document.title = title ? `${title} · Nginx Proxy` : 'Nginx Proxy'
})

export default router
