<template>
  <div id="app">
    <!-- Loading Screen -->
    <div v-if="!ruleStore.isAuthReady" class="loading-screen">
      <div class="loader"></div>
      <p>加载中...</p>
    </div>

    <!-- Token Auth -->
    <TokenAuth v-else-if="!ruleStore.isAuthenticated" />

    <!-- Main Dashboard -->
    <template v-else>
      <div class="main-container">
      <!-- Header -->
      <header class="dashboard-header">
        <div class="header-left">
          <h1 class="logo">Nginx Proxy</h1>
          <p class="subtitle">Master / Agent 控制台</p>
        </div>
        <div class="header-actions">
          <ThemeToggle />
          <button @click="ruleStore.logout" class="btn btn--ghost">
            退出
          </button>
        </div>
      </header>

      <!-- Stats Cards -->
      <section class="stats-grid">
        <div class="stat-card">
          <div class="stat-card__icon stat-card__icon--blue">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <ellipse cx="12" cy="5" rx="9" ry="3"/>
              <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
              <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
            </svg>
          </div>
          <div class="stat-card__content">
            <div class="stat-card__value">{{ ruleStore.agents.length }}</div>
            <div class="stat-card__label">Agent 节点</div>
          </div>
        </div>

        <div class="stat-card">
          <div class="stat-card__icon stat-card__icon--green">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
              <polyline points="22 4 12 14.01 9 11.01"/>
            </svg>
          </div>
          <div class="stat-card__content">
            <div class="stat-card__value">{{ ruleStore.onlineAgentsCount }}</div>
            <div class="stat-card__label">在线节点</div>
          </div>
        </div>

        <div class="stat-card">
          <div class="stat-card__icon stat-card__icon--purple">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
            </svg>
          </div>
          <div class="stat-card__content">
            <div class="stat-card__value">{{ ruleStore.rules.length }}</div>
            <div class="stat-card__label">代理规则</div>
          </div>
        </div>

        <div class="stat-card">
          <div class="stat-card__icon stat-card__icon--orange">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="12" y1="16" x2="12" y2="12"/>
              <line x1="12" y1="8" x2="12.01" y2="8"/>
            </svg>
          </div>
          <div class="stat-card__content">
            <div class="stat-card__value">{{ selectedAgentStatus }}</div>
            <div class="stat-card__label">当前状态</div>
          </div>
        </div>
      </section>

      <!-- Main Content Grid -->
      <div class="content-grid">
        <!-- Agents Panel -->
        <div class="panel">
          <div class="panel__header">
            <div>
              <h2 class="panel__title">Agent 节点</h2>
              <p class="panel__subtitle">选择节点管理规则</p>
            </div>
            <button @click="ruleStore.loadAgents" class="btn btn--ghost btn--icon tooltip">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="23 4 23 10 17 10"/>
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
              </svg>
              <span class="tooltip__content">刷新</span>
            </button>
          </div>
          <div class="panel__body">
            <div v-if="ruleStore.agents.length" class="agent-list">
              <div
                v-for="agent in ruleStore.agents"
                :key="agent.id"
                class="agent-item"
                :class="{ 'agent-item--active': ruleStore.selectedAgentId === agent.id }"
                @click="handleSelectAgent(agent.id)"
              >
                <div class="agent-item__icon">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
                    <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
                    <line x1="6" y1="6" x2="6.01" y2="6"/>
                    <line x1="6" y1="18" x2="6.01" y2="18"/>
                  </svg>
                </div>
                <div class="agent-item__content">
                  <div class="agent-item__name">{{ agent.name }}</div>
                  <div class="agent-item__url">{{ agent.agent_url || '本机节点' }}</div>
                </div>
                <span 
                  class="badge"
                  :class="agent.status === 'online' ? 'badge--success' : 'badge--danger'"
                >
                  {{ agent.status === 'online' ? '在线' : '离线' }}
                </span>
              </div>
            </div>
            <div v-else class="empty-state">
              <div class="empty-state__icon">
                <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                  <ellipse cx="12" cy="5" rx="9" ry="3"/>
                  <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
                  <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
                </svg>
              </div>
              <div class="empty-state__title">暂无节点</div>
              <div class="empty-state__description">点击下载脚本添加新节点</div>
            </div>
          </div>
        </div>

        <!-- Rules Panel -->
        <div class="panel">
          <div class="panel__header">
            <div>
              <h2 class="panel__title">{{ ruleStore.selectedAgent?.name || '未选择节点' }}</h2>
              <p class="panel__subtitle">
                {{ ruleStore.hasSelectedAgent ? `${ruleStore.filteredRules.length} 条规则` : '请先选择一个节点' }}
              </p>
            </div>
            <div class="panel__actions">
              <ActionBar />
              <button
                class="btn btn--icon btn--ghost tooltip"
                :disabled="!ruleStore.hasSelectedAgent"
                @click="showJoinModal = true"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
                  <polyline points="7 10 12 15 17 10"/>
                  <line x1="12" y1="15" x2="12" y2="3"/>
                </svg>
                <span class="tooltip__content">下载加入脚本</span>
              </button>
              <button
                class="btn btn--primary"
                :disabled="!ruleStore.hasSelectedAgent"
                @click="showAddModal = true"
              >
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <line x1="12" y1="5" x2="12" y2="19"/>
                  <line x1="5" y1="12" x2="19" y2="12"/>
                </svg>
                添加规则
              </button>
            </div>
          </div>
          <div class="panel__body">
            <!-- Search -->
            <div v-if="ruleStore.hasSelectedAgent && ruleStore.hasRules" class="search-bar">
              <div class="input-wrapper">
                <span class="input-wrapper__icon">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="8"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                </span>
                <input
                  v-model="ruleStore.searchQuery"
                  type="text"
                  class="input"
                  placeholder="搜索规则..."
                >
              </div>
            </div>
            <RuleList />
          </div>
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
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRuleStore } from './stores/rules'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'
import ThemeToggle from './components/base/ThemeToggle.vue'
import TokenAuth from './components/base/TokenAuth.vue'
import BaseModal from './components/base/BaseModal.vue'
import StatusMessage from './components/StatusMessage.vue'

const ruleStore = useRuleStore()
const showAddModal = ref(false)
const showJoinModal = ref(false)
const downloading = ref(false)
const installOutput = ref('')
let refreshTimer = null

const selectedAgentStatus = computed(() => {
  if (!ruleStore.selectedAgent) return '未选择'
  return ruleStore.selectedAgent.status === 'online' ? '在线' : '离线'
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
    if (!response.ok) {
      throw new Error('下载失败')
    }
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
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
  }
})
</script>

<style scoped>
/* Loading Screen */
.loading-screen {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  background: var(--color-bg-canvas);
}

.loader {
  width: 32px;
  height: 32px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

/* Header */
.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--space-6);
}

.header-left {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.logo {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
  letter-spacing: -0.02em;
}

.subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin: 0;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

/* Stats Grid */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--space-5);
  margin-bottom: var(--space-6);
}

.stat-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  display: flex;
  align-items: center;
  gap: var(--space-4);
  transition: all var(--duration-fast) var(--ease-default);
}

.stat-card:hover {
  box-shadow: var(--shadow-md);
  transform: translateY(-2px);
}

.stat-card__icon {
  width: 48px;
  height: 48px;
  border-radius: var(--radius-xl);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-card__icon--blue {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-card__icon--green {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-card__icon--purple {
  background: #faf5ff;
  color: #9333ea;
}

.stat-card__icon--orange {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.stat-card__content {
  flex: 1;
  min-width: 0;
}

.stat-card__value {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: var(--leading-tight);
}

.stat-card__label {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  margin-top: var(--space-1);
}

/* Content Grid */
.content-grid {
  display: grid;
  grid-template-columns: 320px 1fr;
  gap: var(--space-6);
}

/* Panel */
.panel {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
}

.panel__title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin: 0;
}

.panel__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: var(--space-1) 0 0;
}

.panel__actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.panel__body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

/* Agent List */
.agent-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.agent-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.agent-item:hover {
  background: var(--color-bg-hover);
}

.agent-item--active {
  background: var(--color-primary-subtle);
}

.agent-item__icon {
  width: 40px;
  height: 40px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.agent-item--active .agent-item__icon {
  background: var(--color-primary-200);
  color: var(--color-primary);
}

.agent-item__content {
  flex: 1;
  min-width: 0;
}

.agent-item__name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-item__url {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-top: var(--space-0-5);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Search Bar */
.search-bar {
  margin-bottom: var(--space-4);
}

/* Empty State */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-10) var(--space-6);
  text-align: center;
}

.empty-state__icon {
  color: var(--color-text-muted);
  margin-bottom: var(--space-4);
}

.empty-state__title {
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  margin-bottom: var(--space-2);
}

.empty-state__description {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

/* Utility Classes */
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

/* Main Container - 限制最大宽度 */
.main-container {
  max-width: var(--container-max);
  margin: 0 auto;
  padding: var(--space-6);
  width: 100%;
}

/* Responsive */
@media (max-width: 1280px) {
  .stats-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (max-width: 1024px) {
  .content-grid {
    grid-template-columns: 280px 1fr;
  }
}

@media (max-width: 768px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }

  .content-grid {
    grid-template-columns: 1fr;
  }

  .dashboard-header {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--space-4);
  }

  .panel__actions {
    flex-wrap: wrap;
  }
}
</style>
