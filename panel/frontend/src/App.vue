<template>
  <div id="app">
    <!-- Loading Screen -->
    <div v-if="!ruleStore.isAuthReady" class="loading-screen">
      <div class="loading-logo">
        <div class="loading-logo__ring"></div>
        <div class="loading-logo__core">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
          </svg>
        </div>
      </div>
      <p class="loading-text">正在连接控制台...</p>
    </div>

    <!-- Token Auth -->
    <TokenAuth v-else-if="!ruleStore.isAuthenticated" />

    <!-- Main Dashboard -->
    <template v-else>
      <div class="app-shell">
        <!-- Mobile Sidebar Overlay -->
        <div v-if="sidebarOpen" class="sidebar-overlay" @click="sidebarOpen = false"></div>

        <!-- Top Navigation Bar -->
        <nav class="topbar">
          <div class="topbar__left">
            <button class="topbar__hamburger" @click="sidebarOpen = !sidebarOpen">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="3" y1="6" x2="21" y2="6"/>
                <line x1="3" y1="12" x2="21" y2="12"/>
                <line x1="3" y1="18" x2="21" y2="18"/>
              </svg>
            </button>
            <div class="topbar__brand">
              <div class="topbar__logo">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
                </svg>
              </div>
              <div class="topbar__title">
                <span class="topbar__name">Nginx Proxy</span>
                <span class="topbar__badge">Master</span>
              </div>
            </div>
          </div>

          <div class="topbar__center">
            <div class="topbar__nav">
              <button class="topbar__nav-item topbar__nav-item--active">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
                  <rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/>
                </svg>
                <span>仪表盘</span>
              </button>
              <button class="topbar__nav-item" @click="showJoinModal = true">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
                  <circle cx="8.5" cy="7" r="4"/>
                  <line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/>
                </svg>
                <span>加入节点</span>
              </button>
            </div>
          </div>

          <div class="topbar__actions">
            <ThemeSelector />
            <div class="topbar__divider"></div>
            <button @click="ruleStore.logout" class="topbar__action" title="退出登录">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                <polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/>
              </svg>
            </button>
          </div>
        </nav>

        <!-- Main Layout -->
        <div class="app-layout">
          <!-- Sidebar -->
          <aside class="sidebar" :class="{ 'sidebar--open': sidebarOpen }">
            <div class="sidebar__section">
              <div class="sidebar__section-header">
                <span class="sidebar__section-title">Agent 节点</span>
                <button @click="ruleStore.loadAgents" class="sidebar__section-action" title="刷新">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polyline points="23 4 23 10 17 10"/>
                    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
                  </svg>
                </button>
              </div>

              <div class="sidebar__agents">
                <div
                  v-for="agent in ruleStore.agents"
                  :key="agent.id"
                  class="sidebar__agent"
                  :class="{ 'sidebar__agent--active': ruleStore.selectedAgentId === agent.id }"
                  @click="handleSelectAgent(agent.id); sidebarOpen = false"
                >
                  <div class="sidebar__agent-indicator" :class="agent.status === 'online' ? 'sidebar__agent-indicator--online' : 'sidebar__agent-indicator--offline'"></div>
                  <div class="sidebar__agent-info">
                    <div class="sidebar__agent-name">{{ agent.name }}</div>
                    <div class="sidebar__agent-meta">{{ agent.agent_url || '本机' }}</div>
                  </div>
                  <div class="sidebar__agent-count" v-if="agent.id === ruleStore.selectedAgentId">
                    {{ ruleStore.rules.length }}
                  </div>
                </div>

                <div v-if="!ruleStore.agents.length" class="sidebar__empty">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                    <ellipse cx="12" cy="5" rx="9" ry="3"/>
                    <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
                    <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
                  </svg>
                  <span>暂无节点</span>
                </div>
              </div>
            </div>

            <div class="sidebar__footer">
              <div class="sidebar__status">
                <div class="sidebar__status-dot" :class="ruleStore.selectedAgent?.status === 'online' ? 'sidebar__status-dot--online' : 'sidebar__status-dot--offline'"></div>
                <span class="sidebar__status-text">
                  {{ ruleStore.selectedAgent ? (ruleStore.selectedAgent.status === 'online' ? '服务正常运行' : '节点离线') : '未选择节点' }}
                </span>
              </div>
            </div>
          </aside>

          <!-- Content Area -->
          <main class="content">
            <!-- Content Header -->
            <div class="content__header">
              <div class="content__header-left">
                <div class="content__breadcrumb">
                  <span class="content__breadcrumb-item">控制台</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polyline points="9 18 15 12 9 6"/>
                  </svg>
                  <span class="content__breadcrumb-item content__breadcrumb-item--active">
                    {{ ruleStore.selectedAgent?.name || '代理规则' }}
                  </span>
                </div>
                <h2 class="content__title">
                  {{ ruleStore.selectedAgent?.name || '代理规则管理' }}
                </h2>
                <p class="content__subtitle">
                  {{ ruleStore.hasSelectedAgent ? `共 ${ruleStore.rules.length} 条规则，${ruleStore.filteredRules.length} 条显示` : '请选择左侧节点管理规则' }}
                </p>
              </div>
              <div class="content__header-right">
                <div v-if="ruleStore.hasSelectedAgent && ruleStore.hasRules" class="content__search">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="8"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input
                    v-model="ruleStore.searchQuery"
                    type="text"
                    class="content__search-input"
                    placeholder="搜索规则..."
                  >
                </div>
                <button
                  class="btn btn--primary"
                  :disabled="!ruleStore.hasSelectedAgent"
                  @click="showAddModal = true"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="12" y1="5" x2="12" y2="19"/>
                    <line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                  <span>添加规则</span>
                </button>
              </div>
            </div>

            <!-- Stats Row -->
            <div class="stats-row">
              <div class="stat-pill">
                <div class="stat-pill__icon stat-pill__icon--servers">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
                    <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
                    <line x1="6" y1="6" x2="6.01" y2="6"/>
                    <line x1="6" y1="18" x2="6.01" y2="18"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <span class="stat-pill__value">{{ ruleStore.agents.length }}</span>
                  <span class="stat-pill__label">节点</span>
                </div>
              </div>
              <div class="stat-pill">
                <div class="stat-pill__icon stat-pill__icon--online">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
                    <polyline points="22 4 12 14.01 9 11.01"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <span class="stat-pill__value">{{ ruleStore.onlineAgentsCount }}</span>
                  <span class="stat-pill__label">在线</span>
                </div>
              </div>
              <div class="stat-pill">
                <div class="stat-pill__icon stat-pill__icon--rules">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <span class="stat-pill__value">{{ ruleStore.rules.length }}</span>
                  <span class="stat-pill__label">规则</span>
                </div>
              </div>
              <div class="stat-pill">
                <div class="stat-pill__icon stat-pill__icon--active">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <span class="stat-pill__value">{{ activeRulesCount }}</span>
                  <span class="stat-pill__label">启用</span>
                </div>
              </div>
            </div>

            <!-- Rules Content -->
            <div class="rules-section">
              <RuleList />
            </div>
          </main>
        </div>
      </div>

      <!-- Add Rule Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showAddModal"
          title="添加代理规则"
          :subtitle="ruleStore.selectedAgent?.name ? `添加到: ${ruleStore.selectedAgent.name}` : ''"
        >
          <RuleForm @success="showAddModal = false" />
        </BaseModal>
      </Teleport>

      <!-- Join Script Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showJoinModal"
          title="下载 Agent 加入脚本"
        >
          <div class="space-y-4">
            <p class="text-sm text-secondary">
              从 GitHub 下载最新的 Agent 加入脚本并安装到本机。
            </p>
            <div class="bg-subtle p-4 rounded-lg">
              <code class="text-xs text-tertiary break-all">{{ joinScriptUrl }}</code>
            </div>
            <div class="flex justify-end gap-3">
              <button class="btn btn--secondary" @click="showJoinModal = false">取消</button>
              <button class="btn btn--primary" :disabled="downloading" @click="downloadScript">
                <span v-if="downloading" class="spinner spinner--sm mr-2"></span>
                {{ downloading ? '下载中...' : '下载脚本' }}
              </button>
            </div>
            <pre v-if="installOutput" class="bg-subtle p-4 rounded-lg text-xs text-secondary overflow-auto max-h-48">{{ installOutput }}</pre>
          </div>
        </BaseModal>
      </Teleport>

      <!-- Status Messages -->
      <StatusMessage />
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRuleStore } from './stores/rules'
import RuleForm from './components/RuleForm.vue'
import RuleList from './components/RuleList.vue'
import ThemeSelector from './components/base/ThemeSelector.vue'
import TokenAuth from './components/base/TokenAuth.vue'
import BaseModal from './components/base/BaseModal.vue'
import StatusMessage from './components/StatusMessage.vue'

const ruleStore = useRuleStore()
const showAddModal = ref(false)
const showJoinModal = ref(false)
const downloading = ref(false)
const installOutput = ref('')
const sidebarOpen = ref(false)
let refreshTimer = null

const activeRulesCount = computed(() => {
  return ruleStore.rules.filter(r => r.enabled).length
})

const joinScriptUrl = computed(() => {
  return 'https://raw.githubusercontent.com/12976/nginx-reverse-emby/main/scripts/join-agent.sh'
})

function ensureRefreshTimer() {
  if (refreshTimer) return
  refreshTimer = window.setInterval(() => {
    ruleStore.refreshClusterStatus()
  }, 10000)
}

async function handleSelectAgent(agentId) {
  await ruleStore.selectAgent(agentId)
}

async function downloadScript() {
  downloading.value = true
  installOutput.value = ''
  try {
    const response = await fetch(joinScriptUrl.value)
    if (!response.ok) throw new Error('下载失败')
    const scriptContent = await response.text()
    installOutput.value = `# 脚本已下载 (${scriptContent.length} 字节)
# 请在目标机器上执行以下命令安装：

curl -fsSL ${joinScriptUrl.value} | bash -s -- --master-url ${window.location.origin} --register-token <YOUR_TOKEN>

# 或者保存脚本后手动执行：
# curl -fsSL ${joinScriptUrl.value} -o join-agent.sh
# chmod +x join-agent.sh
# ./join-agent.sh --master-url ${window.location.origin} --register-token <YOUR_TOKEN>`
  } catch (err) {
    installOutput.value = `错误: ${err.message}`
  } finally {
    downloading.value = false
  }
}

onMounted(async () => {
  await ruleStore.checkAuth()
})

watch(
  () => ruleStore.isAuthenticated,
  async (nextValue, prevValue) => {
    if (nextValue && !prevValue) {
      await ruleStore.initialize()
      ensureRefreshTimer()
    }
  }
)

onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<style scoped>
/* ==========================================
   Loading Screen
   ========================================== */
.loading-screen {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-6);
  background: var(--theme-bg);
}

.loading-logo {
  position: relative;
  width: 64px;
  height: 64px;
}

.loading-logo__ring {
  position: absolute;
  inset: 0;
  border: 3px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-right-color: var(--color-primary-hover);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.loading-logo__core {
  position: absolute;
  inset: 50%;
  transform: translate(-50%, -50%);
  color: var(--color-primary);
  animation: pulse 1.5s ease-in-out infinite;
}

.loading-text {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  letter-spacing: 0.05em;
}

@keyframes spin { to { transform: rotate(360deg); } }

/* ==========================================
   App Shell
   ========================================== */
.app-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

/* ==========================================
   Sidebar Overlay (Mobile)
   ========================================== */
.sidebar-overlay {
  display: none;
}

/* ==========================================
   Top Navigation Bar
   ========================================== */
.topbar {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-5);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-default);
  backdrop-filter: blur(16px);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
}

.topbar__left {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__hamburger {
  display: none;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  border: none;
  background: transparent;
}

.topbar__brand {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
}

.topbar__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.topbar__name {
  font-size: var(--text-base);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
}

.topbar__badge {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  letter-spacing: 0.03em;
}

.topbar__center {
  display: flex;
  align-items: center;
}

.topbar__nav {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  background: var(--color-bg-subtle);
  padding: var(--space-1);
  border-radius: var(--radius-xl);
}

.topbar__nav-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: none;
  background: transparent;
  font-family: inherit;
  white-space: nowrap;
}

.topbar__nav-item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__nav-item--active {
  color: var(--color-text-inverse);
  background: var(--gradient-primary);
  box-shadow: var(--shadow-sm);
}

.topbar__actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__divider {
  width: 1px;
  height: 24px;
  background: var(--color-border-default);
}

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: none;
  background: transparent;
}

.topbar__action:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* ==========================================
   App Layout (Sidebar + Content)
   ========================================== */
.app-layout {
  display: flex;
  flex: 1;
  min-height: 0;
}

/* ==========================================
   Sidebar
   ========================================== */
.sidebar {
  width: 280px;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-default);
  backdrop-filter: blur(12px);
  flex-shrink: 0;
  overflow: hidden;
}

.sidebar__section {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

.sidebar__section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.sidebar__section-title {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.08em;
}

.sidebar__section-action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: none;
  background: transparent;
}

.sidebar__section-action:hover {
  color: var(--color-primary);
  background: var(--color-bg-hover);
}

.sidebar__agents {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.sidebar__agent {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-xl);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: 1.5px solid transparent;
}

.sidebar__agent:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-default);
  transform: translateX(2px);
}

.sidebar__agent--active {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.sidebar__agent-indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sidebar__agent-indicator--online {
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-50);
  animation: pulse 2s ease-in-out infinite;
}

.sidebar__agent-indicator--offline {
  background: var(--color-text-muted);
}

.sidebar__agent-info {
  flex: 1;
  min-width: 0;
}

.sidebar__agent-name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar__agent--active .sidebar__agent-name {
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

.sidebar__agent-meta {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar__agent-count {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  flex-shrink: 0;
}

.sidebar__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.sidebar__footer {
  padding: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
}

.sidebar__status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
}

.sidebar__status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sidebar__status-dot--online {
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-50);
}

.sidebar__status-dot--offline {
  background: var(--color-text-muted);
}

.sidebar__status-text {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

/* ==========================================
   Content Area
   ========================================== */
.content {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: var(--space-6);
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.content__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.content__header-left {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.content__breadcrumb {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.content__breadcrumb-item--active {
  color: var(--color-text-secondary);
}

.content__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.content__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.content__header-right {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-shrink: 0;
}

.content__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  transition: all var(--duration-normal) var(--ease-default);
  backdrop-filter: blur(8px);
}

.content__search:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.content__search svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.content__search-input {
  border: none;
  background: transparent;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  width: 180px;
  outline: none;
  font-family: inherit;
}

.content__search-input::placeholder {
  color: var(--color-text-muted);
}

/* ==========================================
   Stats Row
   ========================================== */
.stats-row {
  display: flex;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.stat-pill {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  backdrop-filter: blur(8px);
  transition: all var(--duration-normal) var(--ease-bounce);
}

.stat-pill:hover {
  border-color: var(--color-border-strong);
  transform: translateY(-2px);
  box-shadow: var(--shadow-sm);
}

.stat-pill__icon {
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-pill__icon--servers {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-pill__icon--online {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-pill__icon--rules {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
}

.stat-pill__icon--active {
  background: linear-gradient(135deg, rgba(251,146,60,0.12), rgba(251,191,36,0.12));
  color: var(--color-warning);
}

.stat-pill__data {
  display: flex;
  flex-direction: column;
}

.stat-pill__value {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1;
}

.stat-pill__label {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-top: 2px;
}

/* ==========================================
   Rules Section
   ========================================== */
.rules-section {
  flex: 1;
  min-width: 0;
}

/* ==========================================
   Utility Classes
   ========================================== */
.space-y-4 > * + * { margin-top: var(--space-4); }
.flex { display: flex; }
.justify-end { justify-content: flex-end; }
.gap-3 { gap: var(--space-3); }
.mr-2 { margin-right: var(--space-2); }
.bg-subtle { background: var(--color-bg-subtle); }
.p-4 { padding: var(--space-4); }
.rounded-lg { border-radius: var(--radius-lg); }
.text-xs { font-size: var(--text-xs); }
.text-sm { font-size: var(--text-sm); }
.text-secondary { color: var(--color-text-secondary); }
.text-tertiary { color: var(--color-text-tertiary); }
.break-all { word-break: break-all; }
.overflow-auto { overflow: auto; }
.max-h-48 { max-height: 12rem; }

/* ==========================================
   Responsive: 4K (2560px+)
   ========================================== */
@media (min-width: 2560px) {
  .topbar {
    height: 64px;
    padding: 0 var(--space-8);
  }

  .sidebar {
    width: 340px;
  }

  .content {
    padding: var(--space-8);
    gap: var(--space-6);
  }

  .content__title {
    font-size: var(--text-2xl);
  }

  .stat-pill {
    padding: var(--space-4) var(--space-5);
  }

  .stat-pill__icon {
    width: 44px;
    height: 44px;
  }

  .stat-pill__value {
    font-size: var(--text-2xl);
  }
}

/* ==========================================
   Responsive: Large Desktop (1440px - 2559px)
   ========================================== */
@media (min-width: 1440px) and (max-width: 2559px) {
  .sidebar {
    width: 300px;
  }

  .content {
    padding: var(--space-6);
  }
}

/* ==========================================
   Responsive: Desktop (1024px - 1439px)
   ========================================== */
@media (max-width: 1439px) {
  .sidebar {
    width: 260px;
  }
}

/* ==========================================
   Responsive: Tablet (769px - 1023px)
   ========================================== */
@media (max-width: 1023px) {
  .topbar__nav {
    display: none;
  }

  .sidebar {
    position: fixed;
    top: 56px;
    left: 0;
    bottom: 0;
    z-index: var(--z-fixed);
    transform: translateX(-100%);
    transition: transform var(--duration-normal) var(--ease-default);
    width: 280px;
  }

  .sidebar--open {
    transform: translateX(0);
  }

  .sidebar-overlay {
    display: block;
    position: fixed;
    inset: 0;
    top: 56px;
    background: rgba(0, 0, 0, 0.3);
    backdrop-filter: blur(4px);
    z-index: calc(var(--z-fixed) - 1);
  }

  .topbar__hamburger {
    display: flex;
  }

  .content {
    padding: var(--space-5);
  }

  .stats-row {
    gap: var(--space-2);
  }
}

/* ==========================================
   Responsive: Mobile (481px - 768px)
   ========================================== */
@media (max-width: 768px) {
  .topbar {
    padding: 0 var(--space-3);
  }

  .topbar__title {
    display: none;
  }

  .topbar__hamburger {
    display: flex;
  }

  .content {
    padding: var(--space-4);
    gap: var(--space-4);
  }

  .content__header {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3);
  }

  .content__header-right {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-2);
  }

  .content__search {
    width: 100%;
  }

  .content__search-input {
    width: 100%;
    flex: 1;
  }

  .content__title {
    font-size: var(--text-lg);
  }

  .stats-row {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: var(--space-2);
  }

  .stat-pill {
    padding: var(--space-2-5) var(--space-3);
  }

  .stat-pill__icon {
    width: 32px;
    height: 32px;
  }

  .stat-pill__value {
    font-size: var(--text-lg);
  }
}

/* ==========================================
   Responsive: Small Mobile (<= 480px)
   ========================================== */
@media (max-width: 480px) {
  .topbar {
    height: 52px;
    padding: 0 var(--space-2);
  }

  .topbar__logo {
    width: 32px;
    height: 32px;
  }

  .topbar__actions {
    gap: var(--space-2);
  }

  .topbar__divider {
    display: none;
  }

  .content {
    padding: var(--space-3);
    gap: var(--space-3);
  }

  .content__breadcrumb {
    display: none;
  }

  .content__title {
    font-size: var(--text-base);
  }

  .content__subtitle {
    font-size: var(--text-xs);
  }

  .stats-row {
    grid-template-columns: repeat(2, 1fr);
    gap: var(--space-2);
  }

  .stat-pill {
    padding: var(--space-2);
    gap: var(--space-2);
  }

  .stat-pill__icon {
    width: 28px;
    height: 28px;
  }

  .stat-pill__icon svg {
    width: 14px;
    height: 14px;
  }

  .stat-pill__value {
    font-size: var(--text-base);
  }

  .stat-pill__label {
    font-size: 10px;
  }
}
</style>
