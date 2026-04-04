<template>
  <aside class="sidebar" :class="{ 'sidebar--collapsed': collapsed }">
    <div class="sidebar__section">
      <div class="sidebar__section-header">
        <span class="sidebar__section-title" v-show="!collapsed">Agent 节点</span>
        <div class="sidebar__section-header-actions">
          <button @click="loadAgents()" class="sidebar__section-action" title="刷新">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="23 4 23 10 17 10"/>
              <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
            </svg>
          </button>
          <button @click="collapsed = !collapsed" class="sidebar__section-action sidebar__collapse-btn" :title="collapsed ? '展开' : '收起'">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :style="{ transform: collapsed ? 'rotate(180deg)' : '' }">
              <polyline points="15 18 9 12 15 6"/>
            </svg>
          </button>
        </div>
      </div>

      <div v-if="!collapsed && agents.length" class="sidebar__search">
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
          <div class="sidebar__agent-info" v-show="!collapsed">
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
              <span>{{ agent.agent_url ? new URL(agent.agent_url).hostname : (agent.last_seen_ip || '') }}</span>
            </div>
          </div>
          <div class="sidebar__agent-actions" v-show="!collapsed" @click.stop>
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
          <span v-show="!collapsed">暂无节点</span>
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
import { useAgents } from '../../hooks/useAgents'
import { useAgent } from '../../context/AgentContext'

const { data: agents = [], refetch: loadAgents } = useAgents()
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

async function confirmRename() {
  if (!newName.value.trim() || !renamingAgent.value) return
  // useRenameAgent would be called here — for now just update locally
  renamingAgent.value.name = newName.value.trim()
  renamingAgent.value = null
}

function startDelete(agent) {
  deletingAgent.value = agent
}

async function confirmDelete() {
  if (!deletingAgent.value) return
  // useDeleteAgent would be called here
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
