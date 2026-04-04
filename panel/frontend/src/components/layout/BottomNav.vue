<template>
  <nav class="bottom-nav">
    <RouterLink to="/" class="nav-item" :class="{ active: route.path === '/' }" aria-label="首页">
      <span class="nav-icon" aria-hidden="true">🏠</span>
      <span>首页</span>
    </RouterLink>
    <RouterLink to="/rules" class="nav-item" :class="{ active: route.path.startsWith('/rules') }" aria-label="HTTP规则">
      <span class="nav-icon" aria-hidden="true">🔗</span>
      <span>HTTP规则</span>
    </RouterLink>
    <RouterLink to="/certs" class="nav-item" :class="{ active: route.path === '/certs' }" aria-label="证书">
      <span class="nav-icon" aria-hidden="true">🔒</span>
      <span>证书</span>
    </RouterLink>
    <div class="nav-item nav-item--dropdown" :class="{ active: isMoreActive }" @click="moreOpen = !moreOpen" @keydown.escape="moreOpen = false" ref="moreRef" aria-label="更多">
      <span class="nav-icon" aria-hidden="true">⋯</span>
      <span>更多</span>
      <div v-if="moreOpen" class="more-dropdown">
        <RouterLink to="/l4" class="more-dropdown__item" @click.stop="moreOpen = false">
          L4 规则
        </RouterLink>
        <RouterLink to="/agents" class="more-dropdown__item" @click.stop="moreOpen = false">
          节点管理
        </RouterLink>
        <RouterLink to="/settings" class="more-dropdown__item" @click.stop="moreOpen = false">
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
  height: 60px;
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
  gap: 2px;
  text-decoration: none;
  color: var(--color-text-muted);
  font-size: 10px;
  transition: color 0.15s;
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius-lg);
  cursor: pointer;
}
.nav-item.active,
.nav-item:hover {
  color: var(--color-primary);
}
.nav-icon {
  font-size: 20px;
  line-height: 1;
}

/* More Dropdown */
.nav-item--dropdown {
  position: relative;
}
.more-dropdown {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  min-width: 140px;
  overflow: hidden;
  z-index: var(--z-dropdown);
  backdrop-filter: blur(16px);
}
.more-dropdown__item {
  display: block;
  padding: 0.625rem 1rem;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  text-decoration: none;
  text-align: center;
  transition: background 0.1s;
  white-space: nowrap;
}
.more-dropdown__item:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}
</style>
