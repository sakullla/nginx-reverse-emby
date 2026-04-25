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

    <!-- Card View -->
    <div v-else-if="view === 'card' && filteredAgents.length" class="agent-grid">
      <AgentCard
        v-for="agent in filteredAgents"
        :key="agent.id"
        :agent="agent"
        @click="router.push(`/agents/${agent.id}`)"
        @rename="startRename(agent)"
        @delete="startDelete(agent)"
      />
    </div>

    <!-- List View -->
    <AgentTable
      v-else-if="view === 'list' && filteredAgents.length"
      :agents="filteredAgents"
      :clickable="true"
      @click="agent => router.push(`/agents/${agent.id}`)"
      @rename="startRename"
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

    <!-- Rename Modal -->
    <Teleport to="body">
      <div v-if="renamingAgent" class="modal-overlay">
        <div class="modal">
          <div class="modal__header">重命名节点</div>
          <div class="modal__body">
            <div class="form-group">
              <label>节点名称</label>
              <input v-model="newAgentName" class="input-base" placeholder="输入节点名称" @keyup.enter="confirmRename" />
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="renamingAgent = null">取消</button>
            <button class="btn btn-primary" @click="confirmRename">保存</button>
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
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents, useRenameAgent, useDeleteAgent } from '../hooks/useAgents'
import { useAgentFilters } from '../hooks/useAgentFilters'
import AgentFilterBar from '../components/AgentFilterBar.vue'
import AgentCard from '../components/AgentCard.vue'
import AgentTable from '../components/AgentTable.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import { fetchSystemInfo, applyConfig } from '../api'
import { useAgent } from '../context/AgentContext'
import { messageStore } from '../stores/messages'

const router = useRouter()
const { selectedAgentId } = useAgent()

const { data, isLoading } = useAgents()
const renameAgent = useRenameAgent()
const deleteAgent = useDeleteAgent()
const agents = computed(() => data.value ?? [])

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

const showJoinModal = ref(false)
const selectedPlatform = ref('linux')
const copied = ref(false)
const renamingAgent = ref(null)
const newAgentName = ref('')
const deletingAgent = ref(null)
const applying = ref(false)

// Search
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

async function handleApply() {
  if (!selectedAgentId.value || applying.value) return
  applying.value = true
  try {
    await applyConfig(selectedAgentId.value)
  } finally {
    applying.value = false
  }
}

const systemInfo = ref(null)
fetchSystemInfo().then(info => { systemInfo.value = info }).catch(() => {})

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
    setTimeout(() => { copied.value = false }, 1500)
  } catch (err) {
    console.error('Copy failed:', err)
    messageStore.error('复制失败，请手动选择复制')
  }
}

function startRename(agent) {
  renamingAgent.value = agent
  newAgentName.value = agent.name
}

function confirmRename() {
  if (renamingAgent.value && newAgentName.value.trim()) {
    renameAgent.mutate({ agentId: renamingAgent.value.id, name: newAgentName.value.trim() })
  }
  renamingAgent.value = null
  newAgentName.value = ''
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
.agents-page { max-width: 1200px; margin: 0 auto; }
.agents-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.agents-page__header-left { flex: 1; min-width: 0; }
.agents-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.agents-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.agents-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }

/* Search wrapper */
.search-wrapper { display: flex; align-items: center; position: relative; }
.search-icon-btn { display: none; }
.search-input { flex: 1; min-width: 0; padding: 0.5rem 2rem 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s, width 0.2s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); width: 280px; }
.search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 18px; height: 18px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; flex-shrink: 0; padding: 0; position: absolute; right: 8px; z-index: 2; }

@media (max-width: 640px) {
  .search-wrapper { width: 36px; height: 36px; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); cursor: pointer; display: flex; align-items: center; justify-content: center; position: relative; }
  .search-icon-btn { display: flex; color: var(--color-text-secondary); }
  .search-input { position: absolute; left: 0; top: 0; width: 200px; height: 36px; opacity: 0; pointer-events: none; transition: opacity 0.2s, width 0.2s; }
  .search-wrapper:focus-within { width: 200px; }
  .search-wrapper:focus-within .search-input { opacity: 1; pointer-events: auto; border-color: var(--color-primary); }
  .search-wrapper:focus-within .clear-btn { opacity: 1; pointer-events: auto; }
  .clear-btn { opacity: 0; pointer-events: none; position: absolute; right: 8px; z-index: 2; transition: opacity 0.2s; }
}

/* Card grid */
.agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }

.agents-page__empty, .agents-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }

.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* Modals */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(500px, 90vw); overflow: hidden; }
.modal--lg { width: min(640px, 92vw); }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); display: flex; justify-content: space-between; align-items: center; }
.modal__close { background: none; border: none; font-size: 1rem; cursor: pointer; color: var(--color-text-muted); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.join-tabs { display: flex; gap: 0.5rem; }
.join-tab { flex: 1; padding: 0.5rem; border: none; border-radius: var(--radius-lg); background: var(--color-bg-subtle); color: var(--color-text-secondary); font-size: 0.875rem; cursor: pointer; transition: all 0.15s; font-family: inherit; }
.join-tab.active { background: var(--color-primary); color: white; }
.join-command { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-subtle); border-radius: var(--radius-lg); font-family: var(--font-mono); font-size: 0.8125rem; overflow-x: auto; }
.join-command code { flex: 1; word-break: break-all; overflow-x: auto; white-space: pre; color: var(--color-text-primary); line-height: 1.6; }
.join-steps { counter-reset: step; list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.join-steps li { font-size: 0.875rem; color: var(--color-text-secondary); padding-left: 1.25rem; position: relative; }
.join-steps li::before { content: counter(step) "."; counter-increment: step; position: absolute; left: 0; color: var(--color-primary); font-weight: 600; }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.input-base { width: 100%; padding: 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; box-sizing: border-box; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
</style>
