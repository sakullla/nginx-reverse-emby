<template>
  <nav class="bottom-nav">
    <RouterLink to="/" class="nav-item" :class="{ active: route.path === '/' }" aria-label="首页">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <rect x="3" y="3" width="7" height="9"/><rect x="14" y="3" width="7" height="5"/><rect x="14" y="12" width="7" height="9"/><rect x="3" y="16" width="7" height="5"/>
      </svg>
      <span>首页</span>
    </RouterLink>
    <RouterLink to="/rules" class="nav-item" :class="{ active: route.path.startsWith('/rules') }" aria-label="HTTP规则">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <span>HTTP规则</span>
    </RouterLink>
    <RouterLink to="/certs" class="nav-item" :class="{ active: route.path === '/certs' }" aria-label="证书管理">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <span>证书管理</span>
    </RouterLink>
    <div class="nav-item nav-item--dropdown" :class="{ active: isMoreActive }" @click="moreOpen = !moreOpen" @keydown.escape="moreOpen = false" ref="moreRef" aria-label="更多">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="6" r="1.5" fill="currentColor" stroke="none"/>
        <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none"/>
        <circle cx="12" cy="18" r="1.5" fill="currentColor" stroke="none"/>
      </svg>
      <span>更多</span>
      <div v-if="moreOpen" class="more-dropdown">
        <RouterLink to="/l4" class="more-dropdown__item" @click.stop="moreOpen = false">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
          </svg>
          L4 规则
        </RouterLink>
        <RouterLink to="/relay-listeners" class="more-dropdown__item" @click.stop="moreOpen = false">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
          </svg>
          Relay 监听器
        </RouterLink>
        <RouterLink to="/agents" class="more-dropdown__item" @click.stop="moreOpen = false">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
          </svg>
          节点管理
        </RouterLink>
        <RouterLink to="/settings" class="more-dropdown__item" @click.stop="moreOpen = false">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1z"/>
          </svg>
          设置
        </RouterLink>
      </div>
    </div>
  </nav>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

const route = useRoute()
const moreOpen = ref(false)
const moreRef = ref(null)

const isMoreActive = computed(() =>
  route.path.startsWith('/l4') ||
  route.path.startsWith('/relay-listeners') ||
  route.path.startsWith('/agents') ||
  route.path.startsWith('/settings')
)

function handleClickOutside(e) {
  if (moreRef.value && !moreRef.value.contains(e.target)) {
    moreOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('mousedown', handleClickOutside)
  document.addEventListener('touchstart', handleClickOutside)
})
onUnmounted(() => {
  document.removeEventListener('mousedown', handleClickOutside)
  document.removeEventListener('touchstart', handleClickOutside)
})
</script>

<style scoped>
.bottom-nav {
  display: none;
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  height: 64px;
  background: var(--color-bg-surface);
  border-top: 1px solid var(--color-border-default);
  backdrop-filter: blur(16px);
  z-index: var(--z-sticky);
  padding-bottom: env(safe-area-inset-bottom, 0);
}
@media (max-width: 1023px) {
  .bottom-nav { display: flex; }
}
.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 4px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 11px;
  font-weight: 500;
  transition: all 0.2s;
  padding: 0.5rem 0.25rem;
  border-radius: var(--radius-lg);
  cursor: pointer;
  position: relative;
}
.nav-item.active {
  color: var(--color-primary);
}
.nav-item.active::before {
  content: '';
  position: absolute;
  top: 4px;
  left: 50%;
  transform: translateX(-50%);
  width: 20px;
  height: 3px;
  background: var(--color-primary);
  border-radius: 2px;
}
.nav-icon {
  width: 24px;
  height: 24px;
  transition: transform 0.2s;
}
.nav-item.active .nav-icon {
  transform: translateY(-2px);
}

/* More Dropdown */
.nav-item--dropdown {
  position: relative;
}
.more-dropdown {
  position: absolute;
  bottom: calc(100% + 12px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  min-width: 160px;
  overflow: hidden;
  z-index: var(--z-dropdown);
  backdrop-filter: blur(16px);
  padding: 0.5rem;
}
.more-dropdown__item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.625rem 0.875rem;
  font-size: 0.875rem;
  color: var(--color-text-primary);
  text-decoration: none;
  text-align: left;
  transition: all 0.15s;
  white-space: nowrap;
  border-radius: var(--radius-md);
  font-weight: 500;
}
.more-dropdown__item:hover {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.more-dropdown__item svg {
  flex-shrink: 0;
  color: var(--color-text-secondary);
}
.more-dropdown__item:hover svg {
  color: var(--color-primary);
}
</style>