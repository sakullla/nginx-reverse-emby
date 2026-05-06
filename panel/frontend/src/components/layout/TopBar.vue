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
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../../context/AgentContext'
import { useAuthState } from '../../context/useAuthState'
import ThemeSelector from '../base/ThemeSelector.vue'

const route = useRoute()
const { selectedAgentId } = useAgent()
const { clearToken } = useAuthState()

// Effective agent mirrors what the page uses: route.params.id (agent-detail) wins, then
// route.query.agentId (list pages), then context selection
const effectiveAgentId = computed(() =>
  route.params.id || route.query.agentId || selectedAgentId.value
)

function handleLogout() {
  localStorage.removeItem('panel_token')
  localStorage.removeItem('selected_agent_id')
  clearToken()
  window.location.reload()
}
</script>

<style scoped>
.topbar {
  height: var(--header-height);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-5);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-subtle);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
  box-shadow: var(--shadow-xs);
}

.topbar__left { display: flex; align-items: center; gap: 1rem; }

.topbar__brand { display: flex; align-items: center; gap: 0.75rem; }

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--color-primary);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-bounce);
}

.topbar__logo:hover {
  transform: scale(1.05);
}

.topbar__title { display: flex; align-items: center; gap: 0.5rem; }

.topbar__name {
  font-size: 1rem;
  font-weight: 700;
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

.topbar__badge {
  font-size: 0.6875rem;
  font-weight: 600;
  padding: 2px 8px;
  background: var(--color-primary);
  color: white;
  border-radius: var(--radius-full);
}

.topbar__actions { display: flex; align-items: center; gap: 0.5rem; }

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
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

@media (max-width: 640px) {
  .topbar__title { display: none; }
}
</style>
