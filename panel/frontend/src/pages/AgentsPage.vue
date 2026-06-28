<template>
  <div class="agents-page">
    <div class="agents-page__header">
      <div class="agents-page__header-left">
        <h1 class="agents-page__title">节点管理</h1>
        <p class="agents-page__subtitle">{{ agents.length }} 个节点 · {{ onlineCount }} 在线 · 累计 {{ totalHttpRules }} HTTP 规则 · 累计 {{ totalL4Rules }} L4 规则</p>
      </div>
      <div class="agents-page__header-right">
        <div class="search-wrapper" v-if="agents.length" @click="focusSearch">
          <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input ref="searchInputRef" v-model="searchQuery" name="agent-search" class="search-input" placeholder="搜索节点名称 / IP / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <button v-if="selectedAgentId" class="btn btn-secondary" :disabled="applying" @click="handleApply">
          <svg v-if="!applying" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 1 1-6.219-8.56"/></svg>
          {{ applying ? '推送中...' : '推送配置' }}
        </button>
        <button class="btn btn-primary" @click="showJoinModal = true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          加入节点
        </button>
      </div>
    </div>

    <!-- Filter Bar -->
    <AgentFilterBar
      v-model:view="view"
      v-model:status-filter="statusFilter"
      v-model:mode-filter="modeFilter"
      v-model:tag-filter="tagFilter"
      v-model:sort-field="sortField"
      v-model:sort-order="sortOrder"
      :available-tags="availableTags"
      :has-active-filters="hasActiveFilters"
      @clear-filters="clearFilters"
      @toggle-sort-order="toggleSortOrder"
    />

    <!-- Empty with filters -->
    <div v-if="agents.length && !filteredAgents.length" class="agents-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有符合筛选条件的节点</p>
      <button class="btn btn-secondary" @click="clearFilters">清除筛选</button>
    </div>

    <!-- Monitor View -->
    <div v-else-if="view === 'monitor' && filteredAgents.length" class="agent-grid">
      <AgentMonitorCard
        v-for="agent in filteredAgents"
        :key="agent.id"
        :agent="agent"
        @details="agent => router.push(`/agents/${agent.id}`)"
      />
    </div>

    <!-- List View -->
    <AgentTable
      v-else-if="view === 'list' && filteredAgents.length"
      :agents="filteredAgents"
      :clickable="true"
      @click="agent => router.push(`/agents/${agent.id}`)"
      @rename="startEdit"
      @delete="startDelete"
    />

    <div v-if="!agents.length && !isLoading" class="agents-page__empty">
      <p>暂无节点</p>
    </div>

    <div v-if="isLoading" class="agents-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Join Modal -->
    <Teleport to="body">
      <div v-if="showJoinModal" class="modal-overlay">
        <div class="modal modal--lg">
          <div class="modal__header">
            <span>加入 Agent 节点</span>
            <button class="modal__close" @click="showJoinModal = false">✕</button>
          </div>
          <div class="modal__body">
            <div class="join-tabs">
              <button v-for="p in platforms" :key="p.id" class="join-tab" :class="{ active: selectedPlatform === p.id }" @click="selectedPlatform = p.id">{{ p.label }}</button>
            </div>
            <div class="join-command">
              <code>{{ getCurrentCommand() }}</code>
              <button class="btn btn-primary btn-sm" :class="{ 'btn--copied': copied }" @click="copyCommand">{{ copied ? '已复制' : '复制' }}</button>
            </div>
            <ol class="join-steps">
              <li v-for="step in getCurrentSteps()" :key="step">{{ step }}</li>
            </ol>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Edit Modal -->
    <Teleport to="body">
      <div v-if="editingAgent" class="modal-overlay">
        <div class="modal">
          <div class="modal__header">编辑节点</div>
          <div class="modal__body">
            <div class="form-group">
              <label>节点名称</label>
              <input v-model="editName" class="input-base" placeholder="输入节点名称" @keyup.enter="confirmEdit" />
            </div>
            <div v-if="!editingAgent?.is_local" class="form-group">
              <label>出网代理</label>
              <input
                v-model="editOutboundProxy"
                class="input-base"
                placeholder="socks://user:pass@127.0.0.1:1080"
                @keyup.enter="confirmEdit"
              />
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="editingAgent = null">取消</button>
            <button class="btn btn-primary" :disabled="updateAgent.isPending.value" @click="confirmEdit">保存</button>
          </div>
        </div>
      </div>
    </Teleport>

    <DeleteConfirmDialog
      :show="!!deletingAgent"
      title="确认删除节点"
      message="删除后该节点将立即下线，相关的规则和配置将无法恢复。"
      :name="deletingAgent?.name"
      confirm-text="确认删除"
      :loading="deleteAgent.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingAgent = null"
    />
  </div>
</template>

<script setup>
import { ref, computed, watch, onScopeDispose } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents, useUpdateAgent, useDeleteAgent } from '../hooks/useAgents'
import { useAgentMonitorStream } from '../hooks/useAgentMonitorStream'
import { mergeAgentsWithMonitor } from '../utils/agentMonitor.js'
import { useAgentFilters } from '../hooks/useAgentFilters'
import AgentFilterBar from '../components/AgentFilterBar.vue'
import AgentMonitorCard from '../components/AgentMonitorCard.vue'
import AgentTable from '../components/AgentTable.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import { fetchSystemInfo, applyConfig } from '../api'
import { useAgent } from '../context/AgentContext'
import { messageStore } from '../stores/messages'

const router = useRouter()
const { selectedAgentId } = useAgent()

const { data, isLoading } = useAgents()
const updateAgent = useUpdateAgent()
const deleteAgent = useDeleteAgent()

const monitorStreamEnabled = ref(false)
const { data: monitorAgents } = useAgentMonitorStream({ enabled: monitorStreamEnabled })

const agents = computed(() => mergeAgentsWithMonitor(data.value, monitorAgents.value))

// Filter/sort state
const {
  view,
  statusFilter,
  modeFilter,
  tagFilter,
  sortField,
  sortOrder,
  searchQuery,
  availableTags,
  filteredAgents,
  hasActiveFilters,
  clearFilters,
  toggleSortOrder
} = useAgentFilters(agents)

watch(view, () => {
  monitorStreamEnabled.value = view.value === 'monitor'
}, { immediate: true })

const showJoinModal = ref(false)
const selectedPlatform = ref('linux')
const copied = ref(false)
const editingAgent = ref(null)
const editName = ref('')
const editOutboundProxy = ref('')
const deletingAgent = ref(null)
const applying = ref(false)

// Scope disposal guard for async callbacks and timers
let disposed = false
let copyTimeout = null

function clearCopyTimeout() {
  if (copyTimeout) {
    clearTimeout(copyTimeout)
    copyTimeout = null
  }
}

onScopeDispose(() => {
  disposed = true
  clearCopyTimeout()
})

// Search
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

async function handleApply() {
  if (!selectedAgentId.value || applying.value) return
  applying.value = true
  try {
    await applyConfig(selectedAgentId.value)
  } finally {
    if (!disposed) {
      applying.value = false
    }
  }
}

const systemInfo = ref(null)
fetchSystemInfo().then(info => {
  if (!disposed) {
    systemInfo.value = info
  }
}).catch(() => {})

const joinCommands = computed(() => {
  const origin = typeof window !== 'undefined' ? window.location.origin : ''
  const token = systemInfo.value?.master_register_token || 'YOUR_TOKEN'
  const base = `${origin}/panel-api`
  return {
    linux: `curl -fsSL ${base}/public/join-agent.sh | sh -s -- --register-token ${token} --install-systemd`,
    macos: `curl -fsSL ${base}/public/join-agent.sh | sh -s -- --register-token ${token} --install-launchd`,
    windows: 'Windows 目前请参考 README 手工安装 Go agent 并完成注册'
  }
})

const platforms = computed(() => [
  { id: 'linux', label: 'Linux', command: joinCommands.value.linux, steps: ['在目标主机上执行命令', '脚本会下载 Go nre-agent 二进制', '自动注册并安装 systemd 服务', '节点上线后会出现在列表中'] },
  { id: 'macos', label: 'macOS', command: joinCommands.value.macos, steps: ['在目标主机上执行命令', '脚本会下载 Go nre-agent 二进制', '自动注册并安装 launchd 服务'] },
  { id: 'windows', label: 'Windows', command: joinCommands.value.windows, steps: ['准备单独构建或发布的 nre-agent.exe', '获取控制面的 register token 或已生成的 agent_token', '在 Windows 服务或计划任务中启动 agent 并确保可访问控制面'] }
])

const onlineCount = computed(() => agents.value.filter(a => a.status === 'online').length)

const totalHttpRules = computed(() => {
  return (agents.value || []).reduce((sum, a) => sum + (a.http_rules_count || 0), 0)
})
const totalL4Rules = computed(() => {
  return (agents.value || []).reduce((sum, a) => sum + (a.l4_rules_count || 0), 0)
})

function getCurrentCommand() {
  return platforms.value.find(p => p.id === selectedPlatform.value)?.command || ''
}

function getCurrentSteps() {
  return platforms.value.find(p => p.id === selectedPlatform.value)?.steps || []
}

async function copyCommand() {
  const text = getCurrentCommand()
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.style.position = 'fixed'
      textarea.style.left = '-999999px'
      document.body.appendChild(textarea)
      textarea.select()
      const success = document.execCommand('copy')
      document.body.removeChild(textarea)
      if (!success) throw new Error('execCommand failed')
    }
    messageStore.success('已复制到剪贴板')
    copied.value = true
    clearCopyTimeout()
    copyTimeout = setTimeout(() => {
      copyTimeout = null
      if (!disposed) {
        copied.value = false
      }
    }, 1500)
  } catch (err) {
    console.error('Copy failed:', err)
    messageStore.error('复制失败，请手动选择复制')
  }
}

function startEdit(agent) {
  editingAgent.value = agent
  editName.value = agent.name
  editOutboundProxy.value = agent.is_local ? '' : agent.outbound_proxy_url || ''
}

async function confirmEdit() {
  if (!editingAgent.value) return
  const payload = {}
  const name = editName.value.trim()
  if (name && name !== editingAgent.value.name) {
    payload.name = name
  }
  if (!editingAgent.value.is_local) {
    try {
      const proxyPayload = buildOutboundProxyPayload(
        editingAgent.value.outbound_proxy_url,
        editOutboundProxy.value
      )
      Object.assign(payload, proxyPayload)
    } catch (error) {
      messageStore.warning(error.message, '出网代理密码已隐藏')
      editingAgent.value = null
      editName.value = ''
      editOutboundProxy.value = ''
      return
    }
  }
  if (Object.keys(payload).length > 0) {
    await updateAgent.mutateAsync({
      agentId: editingAgent.value.id,
      payload
    })
  }
  editingAgent.value = null
  editName.value = ''
  editOutboundProxy.value = ''
}

function startDelete(agent) {
  deletingAgent.value = agent
}

function confirmDelete() {
  if (deletingAgent.value) {
    deleteAgent.mutate(deletingAgent.value.id)
  }
  deletingAgent.value = null
}
</script>

<style scoped>
.agents-page {
  max-width: 1280px;
  margin: 0 auto;
  animation: fadeInUp var(--duration-normal) var(--ease-default) both;
}

.agents-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}

.agents-page__header-left { flex: 1; min-width: 0; }

.agents-page__header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.agents-page__title {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 0.25rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.agents-page__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

/* Card grid */
.agent-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 1rem;
}

@media (min-width: 1280px) {
  .agent-grid { grid-template-columns: repeat(auto-fill, minmax(340px, 1fr)); }
}

.agents-page__empty,
.agents-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
  animation: fadeIn 0.3s var(--ease-default) both;
}

/* Page-specific: join modal internals (modal overlay/base are in utilities.css) */
.join-tabs { display: flex; gap: 0.5rem; }
.join-tab {
  flex: 1;
  padding: 0.5rem;
  border: none;
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
}
.join-tab.active { background: var(--color-primary); color: white; }
.join-command {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  overflow-x: auto;
}
.join-command code { flex: 1; word-break: break-all; overflow-x: auto; white-space: pre; color: var(--color-text-primary); line-height: 1.6; }
.join-steps { counter-reset: step; list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.join-steps li { font-size: 0.875rem; color: var(--color-text-secondary); padding-left: 1.25rem; position: relative; }
.join-steps li::before { content: counter(step) "."; counter-increment: step; position: absolute; left: 0; color: var(--color-primary); font-weight: 600; }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.input-base {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-surface);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}
.input-base:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

@media (max-width: 640px) {
  .agents-page__header {
    flex-direction: column;
    align-items: flex-start;
  }
  .agents-page__header-right {
    width: 100%;
  }
  .agents-page__title {
    font-size: 1.25rem;
  }
  .agents-page__subtitle {
    font-size: 0.75rem;
  }
  .agent-grid {
    grid-template-columns: 1fr;
  }
}
</style>
