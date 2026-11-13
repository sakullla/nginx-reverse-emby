<template>
  <header class="topbar" :class="{ 'topbar--compact': compact }">
    <div class="topbar__left">
      <RouterLink v-if="compact" to="/" class="topbar__brand" aria-label="Nginx Proxy 首页">
        <span class="topbar__logo"><BrandMark /></span>
        <span class="topbar__name">Nginx Proxy</span>
      </RouterLink>
      <nav v-else class="topbar__crumbs" aria-label="当前位置">
        <template v-if="crumbs.section">
          <component
            :is="crumbs.sectionTo ? 'RouterLink' : 'span'"
            :to="crumbs.sectionTo"
            class="topbar__crumb topbar__crumb--muted"
          >{{ crumbs.section }}</component>
          <svg class="topbar__crumb-sep" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m9 18 6-6-6-6"/></svg>
        </template>
        <span class="topbar__crumb" aria-current="page">{{ crumbs.title }}</span>
      </nav>
    </div>

    <div class="topbar__actions">
      <button
        class="topbar__search"
        aria-label="全局搜索"
        aria-keyshortcuts="Control+K"
        title="全局搜索 (Ctrl+K 或 /)"
        @click="$emit('open-search')"
      >
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <circle cx="11" cy="11" r="7"/>
          <path d="m20 20-3.5-3.5"/>
        </svg>
        <span class="topbar__search-label">搜索规则、节点、证书…</span>
        <kbd class="topbar__search-kbd">Ctrl K</kbd>
      </button>

      <ThemeSelector />

      <div ref="accountMenuRef" class="topbar__account">
        <button
          ref="accountTriggerRef"
          type="button"
          class="topbar__avatar"
          :class="{ 'topbar__avatar--open': accountMenuOpen }"
          title="账号"
          aria-label="账号"
          aria-haspopup="menu"
          :aria-expanded="accountMenuOpen ? 'true' : 'false'"
          aria-controls="topbar-account-menu"
          @click="toggleAccountMenu"
          @keydown.arrow-down.prevent="openAccountMenu(false)"
          @keydown.arrow-up.prevent="openAccountMenu(true)"
        >
          <span v-if="accountInitial" aria-hidden="true">{{ accountInitial }}</span>
          <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
            <circle cx="12" cy="7" r="4"/>
          </svg>
        </button>

        <Transition name="dropdown">
          <div
            v-if="accountMenuOpen"
            id="topbar-account-menu"
            class="topbar__menu"
            role="menu"
            aria-label="账号"
            @keydown="onMenuKeydown"
          >
            <div v-if="accountLabel" class="topbar__menu-identity" role="none">
              <span class="topbar__menu-avatar" aria-hidden="true">{{ accountInitial }}</span>
              <span class="topbar__menu-identity-text">
                <strong class="topbar__menu-identity-name">{{ accountLabel }}</strong>
                <span class="topbar__menu-identity-label">当前账号</span>
              </span>
            </div>
            <div v-if="accountLabel" class="topbar__menu-sep" role="separator"></div>
            <RouterLink
              to="/settings"
              class="topbar__menu-item"
              role="menuitem"
              @click="closeAccountMenu()"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="12" cy="12" r="3"/>
                <path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>
              </svg>
              设置
            </RouterLink>
            <button
              type="button"
              class="topbar__menu-item topbar__menu-item--danger"
              role="menuitem"
              @click="handleLogout"
            >
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                <polyline points="16 17 21 12 16 7"/>
                <line x1="21" y1="12" x2="9" y2="12"/>
              </svg>
              退出
            </button>
          </div>
        </Transition>
      </div>
    </div>
  </header>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { logout } from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import ThemeSelector from '../base/ThemeSelector.vue'
import BrandMark from '../base/BrandMark.vue'

defineProps({
  compact: { type: Boolean, default: false }
})
defineEmits(['open-search'])

const router = useRouter()
const route = useRoute()
const { actor } = useAccessControl()

const SECTIONS = {
  rules: '流量管理',
  l4: '流量管理',
  certs: '基础设施',
  pki: '基础设施',
  'relay-listeners': '基础设施',
  agents: '基础设施',
  'agent-detail': { label: '节点管理', to: '/agents' },
  plugins: '插件',
  'plugin-marketplace': '插件',
  'plugin-marketplace-detail': { label: '插件市场', to: '/plugins/marketplace' },
  'plugin-repositories': '插件',
  'plugin-detail': { label: '已安装插件', to: '/plugins' }
}

const crumbs = computed(() => {
  const section = SECTIONS[route.name]
  const title = String(route.meta?.title || 'Nginx Proxy').split(' · ')[0]
  if (!section) return { section: '', sectionTo: null, title }
  if (typeof section === 'string') return { section, sectionTo: null, title }
  return { section: section.label, sectionTo: section.to, title }
})

const accountMenuRef = ref(null)
const accountTriggerRef = ref(null)
const accountMenuOpen = ref(false)

const accountLabel = computed(() => {
  const name = String(actor.value?.display_name || actor.value?.username || '').trim()
  return name
})

const accountInitial = computed(() => Array.from(accountLabel.value)[0]?.toUpperCase() || '')

function toggleAccountMenu() {
  accountMenuOpen.value = !accountMenuOpen.value
}

function closeAccountMenu(restoreFocus = false) {
  accountMenuOpen.value = false
  if (restoreFocus) accountTriggerRef.value?.focus()
}

function menuItems() {
  return [...(accountMenuRef.value?.querySelectorAll('[role="menuitem"]') || [])]
}

async function openAccountMenu(focusLast = false) {
  accountMenuOpen.value = true
  await nextTick()
  const items = menuItems()
  items[focusLast ? items.length - 1 : 0]?.focus()
}

function onMenuKeydown(event) {
  if (!accountMenuOpen.value) return
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    closeAccountMenu(true)
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const items = menuItems()
  if (!items.length) return
  const current = items.indexOf(document.activeElement)
  const index = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1
    : (current + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
  items[index]?.focus()
}

function onDocumentClick(event) {
  if (accountMenuRef.value && !accountMenuRef.value.contains(event.target)) {
    closeAccountMenu()
  }
}

function onDocumentKeydown(event) {
  if (event.key === 'Escape') closeAccountMenu()
}

onMounted(() => {
  document.addEventListener('click', onDocumentClick)
  document.addEventListener('keydown', onDocumentKeydown)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocumentClick)
  document.removeEventListener('keydown', onDocumentKeydown)
})

async function handleLogout() {
  closeAccountMenu(true)
  await logout().catch(() => undefined)
  localStorage.removeItem('selected_agent_id')
  await router.replace({ name: 'login' })
}
</script>

<style scoped>
.topbar {
  height: var(--header-height);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  padding: 0 var(--space-8);
  background: var(--color-bg-chrome);
  backdrop-filter: blur(var(--chrome-blur)) saturate(160%);
  -webkit-backdrop-filter: blur(var(--chrome-blur)) saturate(160%);
  border-bottom: 1px solid var(--color-border-subtle);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
}

.topbar--compact {
  padding: 0 var(--space-3) 0 var(--space-4);
}

.topbar__left {
  display: flex;
  align-items: center;
  min-width: 0;
}

.topbar__brand {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  color: var(--color-text-primary);
  text-decoration: none;
}

.topbar__logo {
  display: flex;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-md);
  background: var(--color-brand-bg);
  color: var(--color-brand-ink);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.25), var(--shadow-sm);
  flex-shrink: 0;
}

.topbar__name {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  letter-spacing: -0.02em;
}

.topbar__crumbs {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  min-width: 0;
  font-size: var(--text-sm);
}

.topbar__crumb {
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-decoration: none;
}

.topbar__crumb--muted {
  color: var(--color-text-tertiary);
  font-weight: var(--font-normal);
}

a.topbar__crumb--muted:hover {
  color: var(--color-text-primary);
}

.topbar__crumb-sep {
  color: var(--color-text-muted);
  opacity: 0.6;
  flex-shrink: 0;
}

.topbar__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
}

.topbar__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: clamp(14rem, 24vw, 20rem);
  height: var(--control-height-icon);
  padding: 0 var(--space-1-5) 0 var(--space-3);
  margin-right: var(--space-2);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  color: var(--color-text-muted);
  font: inherit;
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.topbar__search:hover {
  color: var(--color-text-secondary);
  border-color: var(--color-border-strong);
}

.topbar__search-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: left;
  font-size: var(--text-sm);
}

.topbar__search-kbd {
  font-family: var(--font-sans);
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xs);
  padding: 1px var(--space-1-5);
}

.topbar__account {
  position: relative;
}

.topbar__avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  margin-left: var(--space-1);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
  background: linear-gradient(135deg, var(--color-primary-100), var(--color-accent-subtle));
  color: var(--color-primary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: box-shadow var(--duration-fast) var(--ease-default);
}

.topbar__avatar:hover,
.topbar__avatar--open {
  box-shadow: 0 0 0 3px var(--color-primary-100);
}

.topbar__menu {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: var(--z-dropdown);
  min-width: 13rem;
  padding: var(--space-1);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface-raised);
  box-shadow: var(--shadow-lg);
}

.topbar__menu-identity {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  min-width: 0;
  padding: var(--space-2) var(--space-2-5);
}

.topbar__menu-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-full);
  background: linear-gradient(135deg, var(--color-primary-100), var(--color-accent-subtle));
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  flex-shrink: 0;
}

.topbar__menu-identity-text {
  display: grid;
  min-width: 0;
}

.topbar__menu-identity-label {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
}

.topbar__menu-identity-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
}

.topbar__menu-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 2rem;
  padding: 0 var(--space-2-5);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-secondary);
  font: inherit;
  font-size: var(--text-sm);
  text-align: left;
  text-decoration: none;
  cursor: pointer;
  box-sizing: border-box;
}

.topbar__menu-item:hover,
.topbar__menu-item:focus-visible {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
  outline: none;
}

.topbar__menu-item--danger:hover,
.topbar__menu-item--danger:focus-visible {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

.topbar__menu-sep {
  height: 1px;
  margin: var(--space-1) 0;
  background: var(--color-border-subtle);
}

.dropdown-enter-active,
.dropdown-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px) scale(0.98);
}

@media (max-width: 1279px) {
  .topbar:not(.topbar--compact) {
    padding: 0 var(--space-6);
  }
}

@media (max-width: 1023px) {
  .topbar__search-label,
  .topbar__search-kbd {
    display: none;
  }

  .topbar__search {
    width: var(--control-height-icon);
    padding: 0;
    margin-right: 0;
    justify-content: center;
    border-color: transparent;
    background: transparent;
    box-shadow: none;
    color: var(--color-text-secondary);
  }

  .topbar__search:hover {
    background: var(--color-bg-hover);
    border-color: transparent;
  }
}
</style>
