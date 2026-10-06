<template>
  <nav class="bottom-nav" aria-label="主导航">
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
    <RouterLink to="/certs" class="nav-item" :class="{ active: route.path === '/certs' || route.path === '/pki' }" aria-label="证书中心">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <span>证书中心</span>
    </RouterLink>
    <RouterLink to="/agents" class="nav-item" :class="{ active: route.path.startsWith('/agents') }" aria-label="节点管理">
      <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8m-4-4v4"/>
      </svg>
      <span>节点</span>
    </RouterLink>
    <div
      ref="moreRef"
      class="nav-more"
      @keydown="handleMenuKeydown"
      @focusout="handleFocusOut"
    >
      <button
        ref="moreTriggerRef"
        type="button"
        class="nav-item nav-item--dropdown"
        :class="{ active: isMoreActive }"
        aria-label="更多"
        aria-haspopup="menu"
        :aria-expanded="moreOpen"
        aria-controls="mobile-more-menu"
        @click="toggleMore"
      >
        <svg class="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="12" cy="6" r="1.5" fill="currentColor" stroke="none"/>
          <circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none"/>
          <circle cx="12" cy="18" r="1.5" fill="currentColor" stroke="none"/>
        </svg>
        <span>更多</span>
      </button>
      <Transition name="more-pop">
        <div v-if="moreOpen" id="mobile-more-menu" class="more-dropdown" role="menu" aria-label="更多导航">
          <RouterLink to="/l4" class="more-dropdown__item" role="menuitem" :class="{ 'more-dropdown__item--active': isMoreItemActive('/l4') }" :aria-current="isMoreItemActive('/l4') ? 'page' : undefined" @click.stop="moreOpen = false">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
            </svg>
            L4 规则
          </RouterLink>
          <RouterLink to="/relay-listeners" class="more-dropdown__item" role="menuitem" :class="{ 'more-dropdown__item--active': isMoreItemActive('/relay-listeners') }" :aria-current="isMoreItemActive('/relay-listeners') ? 'page' : undefined" @click.stop="moreOpen = false">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M8 12h8"/><path d="M6 8h12"/><path d="M10 16h4"/><circle cx="4" cy="12" r="2"/><circle cx="20" cy="12" r="2"/>
            </svg>
            Relay 监听器
          </RouterLink>
          <a
            v-for="pluginRoute in pluginUIRoutes"
            :key="pluginRoute.id"
            :href="pluginRoute.href"
            class="more-dropdown__item" role="menuitem"
            @click.stop="moreOpen = false"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/>
            </svg>
            {{ pluginRoute.label }}
          </a>
          <RouterLink to="/plugins" class="more-dropdown__item" role="menuitem" :class="{ 'more-dropdown__item--active': route.name === 'plugins' || route.name === 'plugin-detail' }" :aria-current="route.name === 'plugins' || route.name === 'plugin-detail' ? 'page' : undefined" @click.stop="moreOpen = false">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M8.5 3a2.5 2.5 0 1 0 5 0H18a2 2 0 0 1 2 2v4.5a2.5 2.5 0 1 1 0 5V19a2 2 0 0 1-2 2h-4.5a2.5 2.5 0 1 0-5 0H4a2 2 0 0 1-2-2v-4.5a2.5 2.5 0 1 0 0-5V5a2 2 0 0 1 2-2z"/>
            </svg>
            已安装插件
          </RouterLink>
          <RouterLink to="/plugins/marketplace" class="more-dropdown__item" role="menuitem" :class="{ 'more-dropdown__item--active': isMoreItemActive('/plugins/marketplace') }" :aria-current="isMoreItemActive('/plugins/marketplace') ? 'page' : undefined" @click.stop="moreOpen = false">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M8.5 3a2.5 2.5 0 1 0 5 0H18a2 2 0 0 1 2 2v4.5a2.5 2.5 0 1 1 0 5V19a2 2 0 0 1-2 2h-4.5a2.5 2.5 0 1 0-5 0H4a2 2 0 0 1-2-2v-4.5a2.5 2.5 0 1 0 0-5V5a2 2 0 0 1 2-2z"/>
            </svg>
            插件市场
          </RouterLink>
          <RouterLink to="/settings" class="more-dropdown__item" role="menuitem" :class="{ 'more-dropdown__item--active': isMoreItemActive('/settings') }" :aria-current="isMoreItemActive('/settings') ? 'page' : undefined" @click.stop="moreOpen = false">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="3"/>
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1z"/>
            </svg>
            设置
          </RouterLink>
        </div>
      </Transition>
    </div>
  </nav>
</template>

<script setup>
import { ref, computed, nextTick, watch, onMounted, onUnmounted } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useAccessControl } from '../../context/useAccessControl'
import { usePluginUIRoutes } from '../../hooks/usePluginUIRoutes'

const route = useRoute()
const moreOpen = ref(false)
const moreRef = ref(null)
const moreTriggerRef = ref(null)
const { refreshActor } = useAccessControl()
const { routes: pluginUIRoutes } = usePluginUIRoutes()

function isMoreItemActive(to) {
  return Boolean(to) && (route.path === to || route.path.startsWith(`${to}/`))
}

const isMoreActive = computed(() =>
  route.path.startsWith('/l4') ||
  route.path.startsWith('/relay-listeners') ||
  route.path.startsWith('/plugins') ||
  route.path.startsWith('/settings')
)

function menuItems() {
  return [...(moreRef.value?.querySelectorAll('.more-dropdown__item') || [])]
}

async function openMore(last = false) {
  moreOpen.value = true
  await nextTick()
  const items = menuItems()
  items[last ? items.length - 1 : 0]?.focus()
}

function closeMore(restoreFocus = false) {
  moreOpen.value = false
  if (restoreFocus) moreTriggerRef.value?.focus()
}

function toggleMore() {
  if (moreOpen.value) closeMore()
  else openMore()
}

function handleMenuKeydown(event) {
  if (event.key === 'Escape' && moreOpen.value) {
    event.preventDefault()
    event.stopPropagation()
    closeMore(true)
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  if (!moreOpen.value) {
    openMore(event.key === 'ArrowUp' || event.key === 'End')
    return
  }
  const items = menuItems()
  if (!items.length) return
  const current = items.indexOf(document.activeElement)
  const index = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1
    : (current + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
  items[index]?.focus()
}

function handleFocusOut(event) {
  if (!moreRef.value?.contains(event.relatedTarget)) closeMore()
}

watch(() => route.fullPath, () => closeMore())

function handleClickOutside(e) {
  if (moreRef.value && !moreRef.value.contains(e.target)) {
    moreOpen.value = false
  }
}

onMounted(() => {
  refreshActor().catch(() => undefined)
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
  background: var(--color-bg-chrome, var(--color-bg-surface));
  border-top: var(--border-width-thin) solid var(--color-border-default);
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
  gap: var(--space-1);
  text-decoration: none;
  color: var(--color-text-muted);
  font-family: inherit;
  font-size: var(--text-xs);
  border: none;
  background: transparent;
  min-width: 0;
  font-weight: var(--font-medium);
  transition: all var(--duration-normal) var(--ease-default);
  padding: var(--space-2) var(--space-1);
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
  top: var(--space-1);
  left: 50%;
  transform: translateX(-50%);
  width: 20px;
  height: 3px;
  background: var(--color-primary);
  border-radius: var(--radius-full);
}
.nav-icon {
  width: 24px;
  height: 24px;
  transition: transform var(--duration-normal) var(--ease-default);
}
.nav-item.active > .nav-icon {
  transform: translateY(-2px);
}

/* More Dropdown */
.nav-more {
  flex: 1;
  min-width: 0;
  position: relative;
  display: flex;
}
.more-dropdown {
  position: absolute;
  bottom: calc(100% + var(--space-3));
  right: var(--space-1-5);
  background: var(--color-bg-surface);
  border: var(--border-width-thick) solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  min-width: 200px;
  max-width: calc(100vw - var(--space-6));
  max-height: calc(100dvh - 150px - env(safe-area-inset-bottom, 0px));
  overflow-y: auto;
  overscroll-behavior: contain;
  z-index: var(--z-dropdown);
  backdrop-filter: blur(16px);
  padding: var(--space-2);
}
.more-pop-enter-active,
.more-pop-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}
.more-pop-enter-from,
.more-pop-leave-to {
  opacity: 0;
  transform: translateY(6px);
}
.more-dropdown__item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2-5) var(--space-4);
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  text-decoration: none;
  text-align: left;
  transition: all var(--duration-fast) var(--ease-default);
  white-space: nowrap;
  border-radius: var(--radius-md);
  font-weight: var(--font-medium);
  min-height: 44px;
  box-sizing: border-box;
}
.more-dropdown__item:hover,
.more-dropdown__item:focus-visible {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.more-dropdown__item--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}
.more-dropdown__item svg {
  flex-shrink: 0;
  color: var(--color-text-secondary);
}
.more-dropdown__item:hover svg,
.more-dropdown__item--active svg {
  color: var(--color-primary);
}
</style>
