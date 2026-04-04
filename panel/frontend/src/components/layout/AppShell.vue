<template>
  <div class="app-shell">
    <TopBar @open-search="searchOpen = true" />
    <GlobalSearch
      :open="searchOpen"
      @update:open="searchOpen = $event"
    />
    <div class="app-layout">
      <!-- Desktop sidebar -->
      <Sidebar v-if="!isMobile" />
      <!-- Mobile sidebar overlay -->
      <div v-if="mobileSidebarOpen" class="sidebar-overlay" @click="mobileSidebarOpen = false" />
      <main class="content">
        <RouterView />
      </main>
    </div>
    <BottomNav v-if="isMobile" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import BottomNav from './BottomNav.vue'
import GlobalSearch from '../GlobalSearch.vue'

const mobileSidebarOpen = ref(false)
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
  height: 100dvh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.app-layout {
  display: flex;
  flex: 1;
  min-height: 0;
}
.content {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
}
.sidebar-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.3);
  z-index: calc(var(--z-fixed) - 1);
}
@media (max-width: 1023px) {
  .content {
    padding-bottom: 5rem;
  }
}
</style>
