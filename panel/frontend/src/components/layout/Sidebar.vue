<template>
  <aside class="sidebar" :class="{ 'sidebar--collapsed': collapsed }">
    <!-- Navigation links -->
    <nav class="sidebar__nav" v-show="!collapsed">
      <RouterLink to="/" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="7" height="9"/><rect x="14" y="3" width="7" height="5"/><rect x="14" y="12" width="7" height="9"/><rect x="3" y="16" width="7" height="5"/>
        </svg>
        <span>首页</span>
      </RouterLink>
      <RouterLink to="/rules" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
        <span>HTTP 规则</span>
      </RouterLink>
      <RouterLink to="/l4" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
          <line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>
        </svg>
        <span>L4 规则</span>
      </RouterLink>
      <RouterLink to="/certs" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
          <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
        </svg>
        <span>证书</span>
      </RouterLink>
      <RouterLink to="/settings" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
        <span>设置</span>
      </RouterLink>
    </nav>

    <!-- Collapsed nav icons -->
    <nav class="sidebar__nav sidebar__nav--collapsed" v-show="collapsed">
      <RouterLink to="/" class="sidebar__nav-icon" title="首页" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="3" width="7" height="9"/><rect x="14" y="3" width="7" height="5"/><rect x="14" y="12" width="7" height="9"/><rect x="3" y="16" width="7" height="5"/>
        </svg>
      </RouterLink>
      <RouterLink to="/rules" class="sidebar__nav-icon" title="HTTP 规则" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </RouterLink>
      <RouterLink to="/l4" class="sidebar__nav-icon" title="L4 规则" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
        </svg>
      </RouterLink>
      <RouterLink to="/certs" class="sidebar__nav-icon" title="证书" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
        </svg>
      </RouterLink>
      <RouterLink to="/settings" class="sidebar__nav-icon" title="设置" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </RouterLink>
    </nav>

    <!-- Agent section -->
    <div class="sidebar__divider" v-show="!collapsed"></div>

    <div class="sidebar__section" v-show="!collapsed">
      <div class="sidebar__section-header">
        <span class="sidebar__section-title">节点</span>
        <div class="sidebar__section-header-actions">
          <button @click="loadAgents()" class="sidebar__section-action" title="刷新">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="23 4 23 10 17 10"/>
              <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
            </svg>
          </button>
          <button @click="collapsed = !collapsed" class="sidebar__section-action sidebar__collapse-btn" title="收起">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="15 18 9 12 15 6"/>
            </svg>
          </button>
        </div>
      </div>

      <div v-if="agents.length" class="sidebar__search">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <input
          v-model="searchQuery"
          type="text"
          class="sidebar__search-input"
          placeholder="搜索节点..."
        >
        <button v-if="searchQuery" class="sidebar__search-clear" @click="searchQuery = ''">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </div>

      <div class="sidebar__agents">
        <div
          v-for="agent in filteredAgents"
          :key="agent.id"
          class="sidebar__agent"
          :class="{ 'sidebar__agent--active': selectedAgentId === agent.id }"
          @click="selectAgent(agent.id)"
        >
          <div class="sidebar__agent-indicator" :class="`sidebar__agent-indicator--${getStatus(agent)}`"></div>
          <div class="sidebar__agent-info">
            <div class="sidebar__agent-name">{{ agent.name }}</div>
            <div class="sidebar__agent-meta">
              <span class="sidebar__agent-mode-icon">
                <svg v-if="agent.mode === 'local'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
                </svg>
                <svg v-else-if="agent.mode === 'master'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
                </svg>
                <svg v-else width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <polyline points="8 17 12 21 16 17"/><line x1="12" y1="3" x2="12" y2="21"/>
                </svg>
              </span>
              <span>{{ getHostname(agent.agent_url) || agent.last_seen_ip || '' }}</span>
            </div>
          </div>
          <div class="sidebar__agent-actions" @click.stop>
            <button class="sidebar__agent-action" title="重命名" @click="startRename(agent)">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
              </svg>
            </button>
            <button v-if="!agent.is_local" class="sidebar__agent-action sidebar__agent-action--danger" title="删除" @click="startDelete(agent)">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="3 6 5 6 21 6"/>
                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
              </svg>
            </button>
          </div>
        </div>

        <div v-if="!agents.length" class="sidebar__empty">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
          <span>暂无节点</span>
        </div>
      </div>
    </div>

    <!-- Rename Modal -->
    <Teleport to="body">
      <div v-if="renamingAgent" class="modal-overlay" @click.self="renamingAgent = null">
        <div class="modal">
          <div class="modal__header">重命名节点</div>
          <div class="modal__body">
            <input v-model="newName" class="input-base" placeholder="新名称" @keydown.enter="confirmRename">
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="renamingAgent = null">取消</button>
            <button class="btn btn-primary" @click="confirmRename">确认</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <div v-if="deletingAgent" class="modal-overlay" @click.self="deletingAgent = null">
        <div class="modal">
          <div class="modal__header">确认删除</div>
          <div class="modal__body">
            <p>确定删除节点 <strong>{{ deletingAgent.name }}</strong>？</p>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="deletingAgent = null">取消</button>
            <button class="btn btn-danger" @click="confirmDelete">删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </aside>
</template>

<script setup>
import { ref, computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useAgents, useRenameAgent, useDeleteAgent } from '../../hooks/useAgents'
import { useAgent } from '../../context/AgentContext'

const { data: _agentsData, refetch: loadAgents } = useAgents()
const renameAgent = useRenameAgent()
const deleteAgent = useDeleteAgent()
const agents = computed(() => _agentsData.value ?? [])
const { selectedAgentId, selectAgent } = useAgent()

const collapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')
const searchQuery = ref('')
const renamingAgent = ref(null)
const newName = ref('')
const deletingAgent = ref(null)

const filteredAgents = computed(() => {
  if (!searchQuery.value) return agents.value
  const q = searchQuery.value.toLowerCase()
  return agents.value.filter(a =>
    a.name.toLowerCase().includes(q) ||
    (a.agent_url || '').toLowerCase().includes(q)
  )
})

function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function startRename(agent) {
  renamingAgent.value = agent
  newName.value = agent.name
}

function confirmRename() {
  if (!newName.value.trim() || !renamingAgent.value) return
  renameAgent.mutate({ agentId: renamingAgent.value.id, name: newName.value.trim() })
  renamingAgent.value = null
}

function startDelete(agent) {
  deletingAgent.value = agent
}

function confirmDelete() {
  if (!deletingAgent.value) return
  deleteAgent.mutate(deletingAgent.value.id)
  deletingAgent.value = null
}
</script>

<style scoped>
.sidebar {
  width: 260px;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-default);
  flex-shrink: 0;
  overflow: hidden;
  transition: width 0.25s;
}
.sidebar--collapsed {
  width: 64px;
}
.sidebar__section {
  flex: 1;
  overflow-y: auto;
  padding: 1rem;
}
.sidebar__section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.75rem;
}
.sidebar__section-title {
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
}
.sidebar__section-header-actions {
  display: flex;
  gap: 0.25rem;
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
  transition: all 0.25s;
  border: none;
  background: transparent;
}
.sidebar__section-action:hover {
  color: var(--color-primary);
  background: var(--color-bg-hover);
}
.sidebar__collapse-btn svg {
  transition: transform 0.25s;
}
.sidebar__search {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  margin-bottom: 0.75rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
}
.sidebar__search svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}
.sidebar__search-input {
  flex: 1;
  border: none;
  background: transparent;
  outline: none;
  font-size: 0.875rem;
  color: var(--color-text-primary);
  font-family: inherit;
}
.sidebar__search-input::placeholder {
  color: var(--color-text-muted);
}
.sidebar__search-clear {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: none;
  background: var(--color-bg-hover);
  color: var(--color-text-secondary);
  cursor: pointer;
}
.sidebar__agents {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.sidebar__agent {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem;
  border-radius: var(--radius-xl);
  cursor: pointer;
  transition: all 0.15s;
  border: 1.5px solid transparent;
}
.sidebar__agent:hover {
  background: var(--color-bg-hover);
}
.sidebar__agent--active {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
}
.sidebar__agent-indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}
.sidebar__agent-indicator--online {
  background: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-subtle);
}
.sidebar__agent-indicator--pending {
  background: var(--color-warning);
}
.sidebar__agent-indicator--failed {
  background: var(--color-danger);
}
.sidebar__agent-indicator--offline {
  background: var(--color-text-muted);
}
.sidebar__agent-info {
  flex: 1;
  min-width: 0;
}
.sidebar__agent-name {
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__agent--active .sidebar__agent-name {
  color: var(--color-primary);
  font-weight: 600;
}
.sidebar__agent-meta {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  display: flex;
  align-items: center;
  gap: 3px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--font-mono);
}
.sidebar__agent-mode-icon {
  display: flex;
  align-items: center;
  opacity: 0.7;
}
.sidebar__agent-actions {
  display: flex;
  gap: 0.25rem;
  opacity: 0;
  transition: opacity 0.15s;
}
.sidebar__agent:hover .sidebar__agent-actions {
  opacity: 1;
}
.sidebar__agent-action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.15s;
}
.sidebar__agent-action:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}
.sidebar__agent-action--danger:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.sidebar__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  padding: 1.5rem;
  color: var(--color-text-muted);
  font-size: 0.875rem;
}
/* Navigation */
.sidebar__nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 0.75rem 0.75rem 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.sidebar__nav-item {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  font-size: 0.875rem;
  font-weight: 500;
  transition: all 0.15s;
}
.sidebar__nav-item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.sidebar__nav-item--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.sidebar__nav--collapsed {
  flex-direction: row;
  justify-content: center;
  padding: 0.5rem;
  gap: 4px;
  border-bottom: none;
  flex-wrap: wrap;
}
.sidebar__nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  transition: all 0.15s;
}
.sidebar__nav-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.sidebar__nav-icon--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.sidebar__divider {
  height: 1px;
  background: var(--color-border-subtle);
  margin: 0 0.75rem;
}

/* Modals */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.5);
  backdrop-filter: blur(4px);
  z-index: var(--z-modal);
  display: flex;
  align-items: center;
  justify-content: center;
}
.modal {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xl);
  width: min(400px, 90vw);
  overflow: hidden;
}
.modal__header {
  padding: 1rem 1.25rem;
  font-weight: 600;
  font-size: 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.modal__body {
  padding: 1.25rem;
}
.modal__footer {
  padding: 1rem 1.25rem;
  display: flex;
  justify-content: flex-end;
  gap: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
}
.btn {
  padding: 0.5rem 1rem;
  border-radius: var(--radius-lg);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.15s;
  border: none;
  font-family: inherit;
}
.btn-primary {
  background: var(--gradient-primary);
  color: white;
}
.btn-secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border: 1px solid var(--color-border-default);
}
.btn-danger {
  background: var(--color-danger);
  color: white;
}
.input-base {
  width: 100%;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: 1.5px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  font-size: 0.875rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  transition: border-color 0.15s;
}
.input-base:focus {
  border-color: var(--color-primary);
}
</style>
