<template>
  <div class="app-shell">
    <a class="skip-link" href="#main-content">跳到主内容</a>
    <OperationTracker />
    <TopBar @open-search="searchOpen = true" />
    <GlobalSearch
      :open="searchOpen"
      @update:open="searchOpen = $event"
    />
    <div class="app-layout">
      <SakuraBackdrop />
      <!-- Desktop sidebar -->
      <Sidebar v-if="!isMobile" />
      <main id="main-content" class="content" tabindex="-1">
        <RouterView />
      </main>
    </div>
    <BottomNav v-if="isMobile" />
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import BottomNav from './BottomNav.vue'
import GlobalSearch from '../GlobalSearch.vue'
import OperationTracker from '../operations/OperationTracker.vue'
import SakuraBackdrop from '../base/SakuraBackdrop.vue'

const searchOpen = ref(false)
const isMobile = ref(window.innerWidth < 1024)

function checkMobile() {
  isMobile.value = window.innerWidth < 1024
}

onMounted(() => {
  window.addEventListener('resize', checkMobile)
})

onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})
</script>

<style scoped>
.app-shell {
  background: var(--color-bg-atmosphere, var(--color-bg-canvas));
  height: 100dvh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.app-layout {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
}
.content {
  position: relative;
  z-index: 1;
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  scrollbar-gutter: stable;
  padding: var(--space-6);
}
/* Keyboard-only shortcut into the page — invisible until focused */
.skip-link {
  position: fixed;
  top: var(--space-2);
  left: var(--space-2);
  z-index: var(--z-toast);
  padding: var(--space-2) var(--space-4);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface-raised);
  color: var(--color-text-primary);
  border: var(--border-width-thin) solid var(--color-border-default);
  box-shadow: var(--shadow-lg);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  text-decoration: none;
  transform: translateY(calc(-100% - var(--space-4)));
  transition: transform var(--duration-fast) var(--ease-default);
}
.skip-link:focus-visible {
  transform: translateY(0);
}
#main-content:focus {
  outline: none;
}
@media (max-width: 1023px) {
  .content {
    /* Tighter side padding on phones/tablets so list cards keep usable width */
    padding: var(--space-4) var(--space-3) calc(var(--space-20) + env(safe-area-inset-bottom, 0px));
  }
}

@media (max-width: 640px) {
  .content {
    padding: var(--space-3) var(--space-3) calc(var(--space-20) + env(safe-area-inset-bottom, 0px));
  }
}
</style>
