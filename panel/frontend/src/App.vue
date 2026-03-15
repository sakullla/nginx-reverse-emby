<template>
  <div id="app">
    <template v-if="!ruleStore.isAuthReady">
      <div class="initial-loading">
        <div class="loader"></div>
        <p>正在初始化面板...</p>
      </div>
    </template>

    <template v-else>
      <StatusMessage />
      <TokenAuth v-if="!ruleStore.isAuthenticated" />

      <template v-else>
        <div class="top-nav-actions">
          <ThemeToggle />
          <button data-testid="logout-button" @click="ruleStore.logout" class="logout-btn" title="退出登录">
            <svg viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9"/></svg>
            <span class="logout-text">退出</span>
          </button>
        </div>

        <header class="app-header">
          <div class="header-content">
            <div class="header-badge">Master / Agent 控制台</div>
            <h1 class="logo-title">Nginx Reverse Proxy</h1>
            <p class="logo-subtitle">统一管理 Agent 节点、代理规则与远程配置下发</p>
          </div>
        </header>

        <main class="container">
          <section class="stats-grid">
            <StatCard
              :value="ruleStore.agents.length"
              label="Agent 节点"
              :icon="icons.nodes"
              variant="primary"
            />
            <StatCard
              :value="ruleStore.onlineAgentsCount"
              label="在线节点"
              :icon="icons.online"
              variant="success"
            />
            <StatCard
              :value="ruleStore.rules.length"
              label="当前节点规则数"
              :icon="icons.rules"
              variant="info"
            />
            <StatCard
              :value="ruleStore.selectedAgent?.status || '未选择'"
              label="当前节点状态"
              :icon="icons.status"
              variant="secondary"
            />
          </section>

          <section class="panel-grid">
            <div class="agent-section glass-card">
              <div class="section-head">
                <div>
                  <h2>Agent 节点</h2>
                  <p>选择一个节点后，即可管理它的代理规则。</p>
                </div>
                <button class="ghost-btn" data-testid="refresh-agents" @click="ruleStore.loadAgents">
                  刷新节点
                </button>
              </div>

              <div v-if="ruleStore.agents.length" class="agent-list">
                <button
                  v-for="agent in ruleStore.agents"
                  :key="agent.id"
                  class="agent-card"
                  :class="{ active: ruleStore.selectedAgentId === agent.id }"
                  :data-testid="`agent-card-${agent.id}`"
                  @click="handleSelectAgent(agent.id)"
                >
                  <div class="agent-card-top">
                    <div>
                      <div class="agent-name">{{ agent.name }}</div>
                      <div class="agent-url">{{ agent.agent_url || (agent.is_local ? '本机内置节点' : 'NAT / 心跳拉取节点') }}</div>
                    </div>
                    <span class="agent-status" :class="agent.status">{{ formatStatus(agent.status) }}</span>
                  </div>

                  <div class="agent-meta">
                    <span>模式 {{ formatMode(agent.mode) }}</span>
                    <span>版本 {{ agent.version || '未知' }}</span>
                    <span>修订 {{ agent.current_revision ?? 0 }} / {{ agent.desired_revision ?? 0 }}</span>
                    <span v-if="agent.last_apply_status">
                      应用结果：{{ formatApplyStatus(agent.last_apply_status) }}
                    </span>
                    <span>{{ agent.last_seen_at ? `最近在线 ${formatTime(agent.last_seen_at)}` : '尚未上报' }}</span>
                  </div>

                  <div v-if="agent.tags?.length" class="agent-tags">
                    <span v-for="tag in agent.tags" :key="tag" class="agent-tag">{{ tag }}</span>
                  </div>

                  <div class="agent-actions">
                    <span v-if="agent.is_local" class="agent-local-tag">本机节点</span>
                    <button
                      v-else
                      class="danger-link"
                      data-testid="remove-agent"
                      @click.stop="handleRemoveAgent(agent)"
                    >
                      移除
                    </button>
                  </div>
                </button>
              </div>

              <div v-else class="empty-card">
                <h3>暂无 Agent 节点</h3>
                <p>请在目标机器执行加入脚本，完成注册后这里会自动显示。</p>
              </div>
            </div>

            <div class="join-section glass-card">
              <div class="section-head compact">
                <div>
                  <h2>加入脚本</h2>
                  <p>在 Agent 机器执行脚本，自动写入配置并注册到当前 Master。</p>
                </div>
              </div>

              <div class="code-card" data-testid="join-command">
                <pre>{{ joinCommand }}</pre>
              </div>

              <div class="join-actions">
                <button class="ghost-btn" data-testid="copy-join-command" @click="copyJoinCommand">复制命令</button>
              </div>

              <ul class="join-tips">
                <li>将 <code>&lt;REGISTER_TOKEN&gt;</code> 替换为 Master 注册令牌。</li>
                <li>将 <code>edge-01</code> 与 <code>--apply-command</code> 替换成实际节点信息。</li>
                <li>Agent 可以位于 NAT 后，只需能主动访问 Master。</li>
                <li>脚本默认会把配置写入 <code>./agent-data/agent.env</code>。</li>
              </ul>
            </div>
          </section>

          <section class="rules-section glass-card">
            <div class="section-head rules-head">
              <div>
                <h2>代理规则</h2>
                <p>
                  当前节点：
                  <strong>{{ ruleStore.selectedAgent?.name || '未选择' }}</strong>
                </p>
              </div>

              <div class="rules-actions">
                <div v-if="ruleStore.hasSelectedAgent && ruleStore.hasRules" class="search-box">
                  <span class="search-icon">
                    <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
                  </span>
                  <input
                    v-model="ruleStore.searchQuery"
                    data-testid="rules-search-input"
                    type="text"
                    placeholder="搜索当前节点规则..."
                    class="search-input"
                  >
                  <button v-if="ruleStore.searchQuery" @click="ruleStore.searchQuery = ''" class="clear-search">
                    ×
                  </button>
                </div>

                <button
                  @click="showAddModal = true"
                  data-testid="add-rule-button"
                  class="primary-btn"
                  :disabled="!ruleStore.hasSelectedAgent"
                >
                  添加规则
                </button>
                <ActionBar />
              </div>
            </div>

            <div class="stats-inline" data-testid="agent-stats" v-if="ruleStore.hasSelectedAgent">
              <span>总请求数：{{ ruleStore.stats.totalRequests || '0' }}</span>
              <span>状态：{{ ruleStore.stats.status || '未知' }}</span>
            </div>

            <RuleList />
          </section>

          <Teleport to="body">
            <BaseModal
              v-model="showAddModal"
              title="新增代理规则"
              :subtitle="`目标节点：${ruleStore.selectedAgent?.name || '未选择'}`"
              :show-default-footer="false"
            >
              <RuleForm @success="showAddModal = false" />
            </BaseModal>
          </Teleport>
        </main>
      </template>
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRuleStore } from './stores/rules'
import StatusMessage from './components/StatusMessage.vue'
import RuleForm from './components/RuleForm.vue'
import ActionBar from './components/ActionBar.vue'
import RuleList from './components/RuleList.vue'
import StatCard from './components/base/StatCard.vue'
import ThemeToggle from './components/base/ThemeToggle.vue'
import TokenAuth from './components/base/TokenAuth.vue'
import BaseModal from './components/base/BaseModal.vue'

const ruleStore = useRuleStore()
const showAddModal = ref(false)
let refreshTimer = null

function ensureRefreshTimer() {
  if (refreshTimer) return
  refreshTimer = window.setInterval(() => {
    ruleStore.refreshClusterStatus()
  }, 10000)
}

const icons = {
  nodes: '<svg viewBox="0 0 24 24"><circle cx="5" cy="7" r="3"/><circle cx="19" cy="7" r="3"/><circle cx="12" cy="17" r="3"/><line x1="7.5" y1="8.5" x2="10.5" y2="14"/><line x1="16.5" y1="8.5" x2="13.5" y2="14"/></svg>',
  online: '<svg viewBox="0 0 24 24"><path d="M5 12l5 5L20 7"/><circle cx="12" cy="12" r="10"/></svg>',
  rules: '<svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="16" y2="17"/></svg>',
  status: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 3"/></svg>'
}

const joinCommand = computed(() => {
  const masterUrl = typeof window !== 'undefined' ? window.location.origin : 'http://master.example.com:8080'
  return `/opt/nginx-reverse-emby/scripts/join-agent.sh --master-url ${masterUrl} --register-token <REGISTER_TOKEN> --agent-name edge-01 --apply-command '/usr/local/bin/nginx-reverse-emby-apply.sh' --tags edge,emby --install-systemd`
})

async function handleSelectAgent(agentId) {
  await ruleStore.selectAgent(agentId)
}

async function handleRemoveAgent(agent) {
  if (!window.confirm(`确认移除节点 “${agent.name}” 吗？`)) return
  try {
    await ruleStore.removeAgent(agent.id)
  } catch (err) {
    // store 已处理错误提示
  }
}

async function copyJoinCommand() {
  try {
    await navigator.clipboard.writeText(joinCommand.value)
    ruleStore.showSuccess('加入脚本已复制到剪贴板')
  } catch (err) {
    ruleStore.showError('复制失败，请手动复制')
  }
}

function formatStatus(status) {
  if (status === 'online') return '在线'
  if (status === 'offline') return '离线'
  return status || '未知'
}

function formatMode(mode) {
  if (mode === 'local') return '本机'
  if (mode === 'pull') return '心跳拉取'
  if (mode === 'direct') return '直连'
  return mode || '未知'
}

function formatApplyStatus(status) {
  if (status === 'success') return '成功'
  if (status === 'error') return '失败'
  return status || '未知'
}

function formatTime(value) {
  try {
    return new Date(value).toLocaleString()
  } catch {
    return value
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

<style>
#app {
  min-height: 100vh;
}

.initial-loading {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  color: var(--color-text-secondary);
}

.loader {
  width: 44px;
  height: 44px;
  border-radius: 50%;
  border: 3px solid var(--color-border);
  border-top-color: var(--color-primary);
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.top-nav-actions {
  position: fixed;
  top: var(--spacing-lg);
  right: var(--spacing-lg);
  display: flex;
  align-items: center;
  gap: var(--spacing-sm);
  z-index: var(--z-fixed, 100);
}

.logout-btn,
.ghost-btn,
.primary-btn,
.danger-link {
  transition: all var(--transition-base);
}

.logout-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 40px;
  padding: 0 var(--spacing-md);
  background: var(--glass-bg);
  backdrop-filter: blur(var(--blur-md));
  color: var(--color-text-secondary);
  border: 1px solid var(--glass-border);
  border-radius: var(--radius-full);
  cursor: pointer;
  box-shadow: var(--shadow-sm);
}

.logout-btn:hover {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.logout-btn svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.app-header {
  padding: 48px 0 24px;
  text-align: center;
}

.header-content {
  display: inline-flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.header-badge {
  padding: 6px 12px;
  border-radius: 999px;
  background: var(--color-primary-bg);
  color: var(--color-primary);
  font-size: 0.85rem;
  font-weight: 700;
}

.logo-title {
  margin: 0;
  font-size: clamp(2rem, 4vw, 2.8rem);
  font-weight: 800;
}

.logo-subtitle {
  margin: 0;
  color: var(--color-text-secondary);
}

.container {
  width: min(1200px, calc(100% - 32px));
  margin: 0 auto 40px;
  display: flex;
  flex-direction: column;
  gap: 24px;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}

.panel-grid {
  display: grid;
  grid-template-columns: 1.5fr 1fr;
  gap: 24px;
}

.glass-card {
  background: var(--glass-bg);
  border: 1px solid var(--glass-border);
  box-shadow: var(--shadow-sm);
  border-radius: var(--radius-xl);
  backdrop-filter: blur(var(--blur-md));
  padding: 20px;
}

.section-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 16px;
  margin-bottom: 18px;
}

.section-head h2 {
  margin: 0 0 6px;
  font-size: 1.1rem;
}

.section-head p {
  margin: 0;
  color: var(--color-text-secondary);
}

.ghost-btn,
.primary-btn {
  height: 40px;
  border-radius: 12px;
  border: 1px solid var(--color-border);
  padding: 0 16px;
  cursor: pointer;
  font-weight: 600;
}

.ghost-btn {
  background: var(--color-bg-secondary);
  color: var(--color-text-primary);
}

.primary-btn {
  background: var(--color-primary);
  color: white;
  border-color: var(--color-primary);
}

.primary-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.agent-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
}

.agent-card {
  width: 100%;
  text-align: left;
  border: 1px solid var(--color-border);
  background: var(--color-bg-card);
  border-radius: 16px;
  padding: 16px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.agent-card.active {
  border-color: var(--color-primary);
  box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.15);
}

.agent-card-top {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}

.agent-name {
  font-weight: 700;
  color: var(--color-text-primary);
}

.agent-url,
.agent-meta {
  color: var(--color-text-secondary);
  font-size: 0.85rem;
  word-break: break-all;
}

.agent-meta {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.agent-status {
  flex-shrink: 0;
  padding: 4px 10px;
  border-radius: 999px;
  font-size: 0.8rem;
  font-weight: 700;
}

.agent-status.online {
  background: rgba(16, 185, 129, 0.12);
  color: #059669;
}

.agent-status.offline {
  background: rgba(239, 68, 68, 0.12);
  color: #dc2626;
}

.agent-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.agent-tag,
.agent-local-tag {
  display: inline-flex;
  align-items: center;
  height: 28px;
  padding: 0 10px;
  border-radius: 999px;
  background: var(--color-bg-secondary);
  font-size: 0.8rem;
  color: var(--color-text-secondary);
}

.agent-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.danger-link {
  background: transparent;
  border: none;
  color: var(--color-danger);
  cursor: pointer;
  font-weight: 600;
  padding: 0;
}

.empty-card {
  padding: 24px;
  border-radius: 16px;
  border: 1px dashed var(--color-border);
  text-align: center;
  color: var(--color-text-secondary);
}

.code-card {
  border-radius: 16px;
  background: #0f172a;
  color: #e2e8f0;
  padding: 16px;
  overflow-x: auto;
}

.code-card pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
  font-family: var(--font-family-mono);
  font-size: 0.85rem;
  line-height: 1.6;
}

.join-actions {
  margin-top: 12px;
}

.join-tips {
  margin: 16px 0 0;
  padding-left: 18px;
  color: var(--color-text-secondary);
}

.join-tips li + li {
  margin-top: 8px;
}

.rules-head {
  align-items: center;
}

.rules-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.search-box {
  position: relative;
  min-width: 280px;
}

.search-input {
  width: 100%;
  height: 40px;
  border-radius: 12px;
  border: 1px solid var(--color-border);
  background: var(--color-bg-card);
  padding: 0 40px 0 38px;
  color: var(--color-text-primary);
}

.search-icon {
  position: absolute;
  left: 12px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--color-text-muted);
}

.search-icon svg,
.clear-search {
  width: 16px;
  height: 16px;
}

.search-icon svg {
  stroke: currentColor;
  stroke-width: 2.4;
  fill: none;
}

.clear-search {
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  border: none;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: 18px;
  line-height: 1;
}

.stats-inline {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 16px;
  color: var(--color-text-secondary);
  font-size: 0.9rem;
}

@media (max-width: 1024px) {
  .stats-grid,
  .panel-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  .container {
    width: min(100% - 20px, 100%);
  }

  .top-nav-actions {
    position: static;
    justify-content: flex-end;
    padding: 12px 12px 0;
  }

  .section-head,
  .rules-head {
    flex-direction: column;
    align-items: stretch;
  }

  .rules-actions {
    width: 100%;
  }

  .search-box {
    width: 100%;
    min-width: 0;
  }
}
</style>
