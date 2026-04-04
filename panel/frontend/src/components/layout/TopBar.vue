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

    <div class="topbar__center">
      <button class="topbar__search" @click="$emit('open-search')" title="全局搜索 (⌘K)">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <span>全局搜索</span>
        <kbd>⌘K</kbd>
      </button>
      <span class="topbar__current-agent">{{ currentAgentName }}</span>
    </div>

    <div class="topbar__actions">
      <ThemeSelector />
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
import { useAgent } from '../../context/AgentContext'
import { useAgents } from '../../hooks/useAgents'
import ThemeSelector from '../base/ThemeSelector.vue'

const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()

const currentAgentName = computed(() => {
  if (!selectedAgentId.value || !agentsData.value) return '—'
  const agent = agentsData.value.find(a => a.id === selectedAgentId.value)
  return agent?.name || '—'
})

function handleLogout() {
  localStorage.removeItem('panel_token')
  localStorage.removeItem('selected_agent_id')
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
.topbar__left {
  display: flex;
  align-items: center;
  gap: 1rem;
}
.topbar__brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
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
  gap: 0.5rem;
}
.topbar__name {
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-text-primary);
}
.topbar__badge {
  font-size: 0.75rem;
  font-weight: 600;
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
}
.topbar__center {
  display: flex;
  align-items: center;
}
.topbar__search {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 1rem;
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  cursor: pointer;
  transition: all 0.25s;
  font-family: inherit;
}
.topbar__search:hover {
  border-color: var(--color-primary);
  color: var(--color-text-primary);
}
.topbar__search kbd {
  font-size: 0.75rem;
  padding: 1px 5px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 4px;
  font-family: var(--font-mono);
}
.topbar__current-agent {
  font-size: 0.8rem;
  color: var(--color-text-tertiary);
  padding: 0.25rem 0.5rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
.topbar__actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
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
  transition: all 0.25s;
  border: none;
  background: transparent;
}
.topbar__action:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}
.topbar__action--logout:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}
@media (max-width: 768px) {
  .topbar__search span,
  .topbar__search kbd {
    display: none;
  }
  .topbar__center {
    display: none;
  }
}
</style>
