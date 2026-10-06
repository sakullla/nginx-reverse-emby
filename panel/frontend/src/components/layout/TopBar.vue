<template>
  <header class="topbar">
    <div class="topbar__left">
      <div class="topbar__brand">
        <div class="topbar__logo">
          <BrandMark />
        </div>
        <div class="topbar__title">
          <span class="topbar__name">Nginx Proxy</span>
          <span class="topbar__badge">管理端</span>
        </div>
      </div>
    </div>

    <div class="topbar__actions">
      <button
        class="topbar__search"
        aria-label="全局搜索"
        aria-keyshortcuts="Control+K"
        title="全局搜索 (Ctrl+K 或 /)"
        @click="$emit('open-search')"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <span class="topbar__search-label">搜索</span>
        <kbd class="topbar__search-kbd">Ctrl K</kbd>
      </button>

      <ThemeSelector />

      <div ref="accountMenuRef" class="topbar__account">
        <button
          type="button"
          class="topbar__action"
          :class="{ 'topbar__action--open': accountMenuOpen }"
          title="账号"
          aria-label="账号"
          aria-haspopup="menu"
          :aria-expanded="accountMenuOpen ? 'true' : 'false'"
          aria-controls="topbar-account-menu"
          @click="toggleAccountMenu"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
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
          >
            <div v-if="accountLabel" class="topbar__menu-identity" role="none">
              <span class="topbar__menu-identity-label">当前账号</span>
              <strong class="topbar__menu-identity-name">{{ accountLabel }}</strong>
            </div>
            <div v-if="accountLabel" class="topbar__menu-sep" role="separator"></div>
            <button
              type="button"
              class="topbar__menu-item topbar__menu-item--danger"
              role="menuitem"
              @click="handleLogout"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
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
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { logout } from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import ThemeSelector from '../base/ThemeSelector.vue'
import BrandMark from '../base/BrandMark.vue'

const router = useRouter()
const { actor } = useAccessControl()

const accountMenuRef = ref(null)
const accountMenuOpen = ref(false)

const accountLabel = computed(() => {
  const name = String(actor.value?.display_name || actor.value?.username || '').trim()
  return name
})

function toggleAccountMenu() {
  accountMenuOpen.value = !accountMenuOpen.value
}

function closeAccountMenu() {
  accountMenuOpen.value = false
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
  closeAccountMenu()
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
  padding: 0 var(--space-5);
  background: var(--color-bg-chrome, var(--color-bg-surface));
  backdrop-filter: blur(var(--chrome-blur, 0px));
  border-bottom: var(--border-width-thin) solid var(--color-border-subtle);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
  box-shadow: var(--shadow-xs);
}

.topbar__left { display: flex; align-items: center; gap: var(--space-4); }

.topbar__brand { display: flex; align-items: center; gap: var(--space-3); }

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--color-brand-bg, var(--color-primary));
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-brand-ink, white);
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
  transition: transform var(--duration-fast) var(--ease-bounce);
}

.topbar__logo:hover {
  transform: scale(1.05);
}

.topbar__title { display: flex; align-items: center; gap: var(--space-2); }

.topbar__name {
  font-size: var(--text-base);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}

.topbar__badge {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: var(--space-0-5) var(--space-2);
  background: var(--color-badge-bg, var(--color-primary));
  color: var(--color-badge-ink, white);
  border-radius: var(--radius-full);
}

.topbar__actions { display: flex; align-items: center; gap: var(--space-2); }

.topbar__search {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  height: 36px;
  padding: 0 var(--space-2) 0 var(--space-3);
  border: var(--border-width-thin) solid var(--color-border-default);
  border-radius: var(--radius-full);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font: inherit;
  cursor: pointer;
}

.topbar__search:hover {
  color: var(--color-text-primary);
  border-color: var(--color-border-strong);
  background: var(--color-bg-hover);
}

.topbar__search-label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
}

.topbar__search-kbd {
  font-family: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
  background: var(--color-bg-surface);
  border: var(--border-width-thin) solid var(--color-border-default);
  border-radius: var(--radius-sm);
  padding: var(--space-0-5) var(--space-1-5);
}

.topbar__account {
  position: relative;
}

.topbar__action--open {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__menu {
  position: absolute;
  top: calc(100% + var(--space-2));
  right: 0;
  z-index: var(--z-dropdown);
  min-width: 11.5rem;
  padding: var(--space-1-5);
  border: var(--border-width-thin) solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-lg);
}

.topbar__menu-identity {
  display: grid;
  gap: var(--space-0-5);
  min-width: 0;
  padding: var(--space-2) var(--space-3) var(--space-2);
}

.topbar__menu-identity-label {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
}

.topbar__menu-identity-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
}

.topbar__menu-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: 2.15rem;
  padding: 0 var(--space-3);
  border: 0;
  border-radius: var(--radius-lg);
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  font-size: var(--text-sm);
  text-align: left;
  cursor: pointer;
}

.topbar__menu-item:hover,
.topbar__menu-item:focus-visible {
  background: var(--color-bg-hover);
  outline: none;
}

.topbar__menu-item--danger {
  color: var(--color-danger);
}

.topbar__menu-item--danger:hover,
.topbar__menu-item--danger:focus-visible {
  background: var(--color-danger-50);
}

.topbar__menu-sep {
  height: var(--border-width-thin);
  margin: var(--space-1) var(--space-1-5);
  background: var(--color-border-subtle);
}

.dropdown-enter-active,
.dropdown-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: translateY(-4px);
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
  transition: all var(--duration-fast) var(--ease-default);
  border: none;
  background: transparent;
}

.topbar__action:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

@media (max-width: 900px) {
  .topbar__search-label,
  .topbar__search-kbd { display: none; }
  .topbar__search {
    width: 36px;
    padding: 0;
    justify-content: center;
    border-radius: var(--radius-lg);
  }
}

@media (max-width: 640px) {
  .topbar__title { display: none; }
}
</style>
