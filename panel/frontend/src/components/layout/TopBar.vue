<template>
  <header class="topbar">
    <div class="topbar__left">
      <div class="topbar__brand">
        <div class="topbar__logo">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
          </svg>
        </div>
        <div class="topbar__title">
          <span class="topbar__name">Nginx Proxy</span>
          <span class="topbar__badge">管理端</span>
        </div>
      </div>
    </div>

    <div class="topbar__actions">
      <button class="topbar__action topbar__action--search" @click="$emit('open-search')" title="全局搜索 (Ctrl+K)">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
      </button>

      <ThemeSelector />

      <!-- Agent Switcher Dropdown -->
      <div class="agent-switcher" ref="agentSwitcherRef">
        <button class="agent-switcher__trigger" @click="agentDropdownOpen = !agentDropdownOpen">
          <span class="agent-switcher__dot" :class="`agent-switcher__dot--${getAgentStatus(currentAgent)}`"></span>
          <span class="agent-switcher__name">{{ currentAgentName }}</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </button>
        <div v-if="agentDropdownOpen" class="agent-switcher__dropdown">
          <div class="agent-switcher__search">
            <input v-model="agentSearchQuery" class="agent-switcher__search-input" placeholder="搜索节点..." />
          </div>
          <div class="agent-switcher__list">
            <button
              v-for="agent in filteredAgents"
              :key="agent.id"
              class="agent-switcher__item"
              :class="{ active: agent.id === effectiveAgentId }"
              @click="selectAgent(agent)"
            >
              <span class="agent-switcher__dot" :class="`agent-switcher__dot--${getAgentStatus(agent)}`"></span>
              <span class="agent-switcher__item-name">{{ agent.name }}</span>
              <span class="agent-switcher__item-url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '') }}</span>
            </button>
          </div>
        </div>
      </div>

      <button class="topbar__action topbar__action--logout" @click="handleLogout" title="退出登录">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
          <polyline points="16 17 21 12 16 7"/>
          <line x1="21" y1="12" x2="9" y2="12"/>
        </svg>
      </button>
    </div>
  </header>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAgent } from '../../context/AgentContext'
import { useAgents } from '../../hooks/useAgents'
import { useAuthState } from '../../context/useAuthState'
import ThemeSelector from '../base/ThemeSelector.vue'

const router = useRouter()
const route = useRoute()
const { selectedAgentId, selectAgent: setSelectedAgentId } = useAgent()
const { data: agentsData } = useAgents()
const { clearToken } = useAuthState()

const agentDropdownOpen = ref(false)
const agentSearchQuery = ref('')
const agentSwitcherRef = ref(null)

// Effective agent mirrors what the page uses: route.params.id (agent-detail) wins, then
// route.query.agentId (list pages), then context selection
const effectiveAgentId = computed(() =>
  route.params.id || route.query.agentId || selectedAgentId.value
)

const currentAgentName = computed(() => {
  if (!effectiveAgentId.value || !agentsData.value) return '—'
  const agent = agentsData.value.find(a => a.id === effectiveAgentId.value)
  return agent?.name || '—'
})

const currentAgent = computed(() => {
  if (!effectiveAgentId.value || !agentsData.value) return null
  return agentsData.value.find(a => a.id === effectiveAgentId.value)
})

const filteredAgents = computed(() => {
  const agents = agentsData.value || []
  if (!agentSearchQuery.value.trim()) return agents
  const q = agentSearchQuery.value.toLowerCase()
  return agents.filter(a =>
    a.name.toLowerCase().includes(q) ||
    (a.agent_url || '').toLowerCase().includes(q) ||
    (a.last_seen_ip || '').toLowerCase().includes(q)
  )
})

function getAgentStatus(agent) {
  if (!agent) return 'offline'
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if ((agent.desired_revision || 0) > (agent.current_revision || 0)) return 'pending'
  return 'online'
}

function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

function selectAgent(agent) {
  setSelectedAgentId(agent.id)
  // If we're on an agent-detail page, navigate to the new agent's detail
  if (route.name?.includes('agent-detail')) {
    router.push({ name: 'agent-detail', params: { id: agent.id } })
  } else if (route.query.agentId) {
    // Clear ?agentId= so the page uses the context selection
    router.replace({ query: { ...route.query, agentId: undefined } })
  }
  agentDropdownOpen.value = false
  agentSearchQuery.value = ''
}

function handleClickOutside(e) {
  if (agentSwitcherRef.value && !agentSwitcherRef.value.contains(e.target)) {
    agentDropdownOpen.value = false
    agentSearchQuery.value = ''
  }
}

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))

function handleLogout() {
  localStorage.removeItem('panel_token')
  localStorage.removeItem('selected_agent_id')
  clearToken()
  window.location.reload()
}
</script>

<style scoped>
.topbar {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 1.25rem;
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-default);
  backdrop-filter: blur(16px);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
}
.topbar__left { display: flex; align-items: center; gap: 1rem; }
.topbar__brand { display: flex; align-items: center; gap: 0.75rem; }
.topbar__logo {
  width: 36px; height: 36px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  display: flex; align-items: center; justify-content: center;
  color: white; box-shadow: var(--shadow-md); flex-shrink: 0;
}
.topbar__title { display: flex; align-items: center; gap: 0.5rem; }
.topbar__name { font-size: 1rem; font-weight: 700; color: var(--color-text-primary); }
.topbar__badge {
  font-size: 0.75rem; font-weight: 600;
  padding: 2px 8px;
  background: var(--gradient-primary); color: white;
  border-radius: var(--radius-full);
}
.topbar__actions { display: flex; align-items: center; gap: 0.5rem; }
.topbar__action {
  display: flex; align-items: center; justify-content: center;
  width: 36px; height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary); cursor: pointer;
  transition: all 0.25s; border: none; background: transparent;
}
.topbar__action:hover { color: var(--color-text-primary); background: var(--color-bg-hover); }
.topbar__action--logout:hover { color: var(--color-danger); background: var(--color-danger-50); }
.topbar__action--search:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

/* Agent Switcher */
.agent-switcher { position: relative; }
.agent-switcher__trigger {
  display: flex; align-items: center; gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  color: var(--color-text-primary); font-size: 0.8rem;
  cursor: pointer; transition: all 0.15s; font-family: inherit;
  max-width: 160px;
}
.agent-switcher__trigger:hover { border-color: var(--color-primary); }
.agent-switcher__dot {
  width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0;
}
.agent-switcher__dot--online { background: var(--color-success); }
.agent-switcher__dot--offline { background: var(--color-text-muted); }
.agent-switcher__dot--failed { background: var(--color-danger); }
.agent-switcher__dot--pending { background: var(--color-warning); }
.agent-switcher__name {
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.agent-switcher__dropdown {
  position: absolute; top: calc(100% + 6px); right: 0;
  width: 240px;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  overflow: visible;
}
.agent-switcher__search { padding: 0.5rem; border-bottom: 1px solid var(--color-border-subtle); }
.agent-switcher__search-input {
  width: 100%; padding: 0.375rem 0.625rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8rem; color: var(--color-text-primary);
  outline: none; font-family: inherit;
  box-sizing: border-box;
}
.agent-switcher__search-input:focus { border-color: var(--color-primary); }
.agent-switcher__list { max-height: 280px; overflow-y: auto; padding: 0.25rem; }
.agent-switcher__item {
  display: flex; align-items: center; gap: 0.5rem;
  width: 100%; padding: 0.5rem 0.625rem;
  border: none; background: transparent;
  border-radius: var(--radius-md); cursor: pointer;
  transition: background 0.1s; font-family: inherit; text-align: left;
}
.agent-switcher__item:hover { background: var(--color-bg-hover); }
.agent-switcher__item.active { background: var(--color-primary-subtle); }
.agent-switcher__item-name {
  font-size: 0.8125rem; color: var(--color-text-primary);
  flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.agent-switcher__item-url {
  font-size: 0.7rem; color: var(--color-text-muted);
  font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis;
}

@media (max-width: 768px) {
  .agent-switcher__trigger { max-width: 120px; }
}
</style>
