<template>
  <aside class="sidebar" :class="{ 'sidebar--collapsed': collapsed }">
    <div class="sidebar__header">
      <span class="sidebar__brand" v-show="!collapsed">Nginx Proxy</span>
      <button class="sidebar__collapse-btn" @click="toggleCollapse" :title="collapsed ? '展开' : '折叠'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ 'rotate-180': collapsed }">
          <polyline points="15 18 9 12 15 6"/>
        </svg>
      </button>
    </div>
    <!-- Navigation links -->
    <nav class="sidebar__nav" v-show="!collapsed">
      <RouterLink to="/" class="sidebar__nav-item" :class="{ active: route.name === 'dashboard' }">
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
      <RouterLink to="/relay-listeners" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M8 12h8"/>
          <path d="M6 8h12"/>
          <path d="M10 16h4"/>
          <circle cx="4" cy="12" r="2"/>
          <circle cx="20" cy="12" r="2"/>
        </svg>
        <span>Relay 监听器</span>
      </RouterLink>
      <RouterLink to="/versions" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 5h18"/>
          <path d="M3 12h10"/>
          <path d="M3 19h6"/>
        </svg>
        <span>版本策略</span>
      </RouterLink>
      <RouterLink to="/agents" class="sidebar__nav-item" :class="{ active: route.name === 'agents' || route.name === 'agent-detail' }">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
        </svg>
        <span>节点管理</span>
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
      <RouterLink to="/" class="sidebar__nav-icon" title="首页" :class="{ 'sidebar__nav-icon--active': route.name === 'dashboard' }">
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
      <RouterLink to="/relay-listeners" class="sidebar__nav-icon" title="Relay 监听器" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M8 12h8"/>
          <path d="M6 8h12"/>
          <path d="M10 16h4"/>
          <circle cx="4" cy="12" r="2"/>
          <circle cx="20" cy="12" r="2"/>
        </svg>
      </RouterLink>
      <RouterLink to="/versions" class="sidebar__nav-icon" title="版本策略" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 5h18"/>
          <path d="M3 12h10"/>
          <path d="M3 19h6"/>
        </svg>
      </RouterLink>
      <RouterLink to="/agents" class="sidebar__nav-icon" title="节点管理" :class="{ 'sidebar__nav-icon--active': route.name === 'agents' || route.name === 'agent-detail' }">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
        </svg>
      </RouterLink>
      <RouterLink to="/settings" class="sidebar__nav-icon" title="设置" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </RouterLink>
    </nav>
  </aside>
</template>

<script setup>
import { ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()
const collapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')

function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem('sidebar_collapsed', String(collapsed.value))
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

/* Header */
.sidebar__header {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.875rem 0.875rem 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
  min-height: 56px;
  box-sizing: border-box;
}
.sidebar__logo {
  width: 32px; height: 32px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  display: flex; align-items: center; justify-content: center;
  color: white; flex-shrink: 0;
}
.sidebar__brand {
  font-size: 0.875rem; font-weight: 700;
  color: var(--color-text-primary); flex: 1;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.sidebar__collapse-btn {
  display: flex; align-items: center; justify-content: center;
  width: 28px; height: 28px;
  border-radius: var(--radius-md);
  border: none; background: transparent;
  color: var(--color-text-secondary); cursor: pointer;
  transition: all 0.15s; flex-shrink: 0;
}
.sidebar--collapsed .sidebar__collapse-btn {
  margin: 0 auto;
}
.sidebar__collapse-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
.sidebar__collapse-btn svg {
  transition: transform 0.2s;
}
.sidebar__collapse-btn svg.rotate-180 {
  transform: rotate(180deg);
}

/* Navigation */
.sidebar__nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 0.5rem 0.75rem;
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
.sidebar__nav-item--active,
.sidebar__nav-item.active {
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
</style>
