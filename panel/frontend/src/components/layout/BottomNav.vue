<template>
  <nav class="bottom-nav">
    <RouterLink to="/" class="nav-item" :class="{ active: route.path === '/' }">
      <span class="nav-icon">🏠</span>
      <span>首页</span>
    </RouterLink>
    <RouterLink to="/rules" class="nav-item" :class="{ active: route.path.startsWith('/rules') || route.path.startsWith('/l4') }">
      <span class="nav-icon">🔗</span>
      <span>规则</span>
    </RouterLink>
    <RouterLink to="/certs" class="nav-item" :class="{ active: route.path === '/certs' }">
      <span class="nav-icon">🔒</span>
      <span>证书</span>
    </RouterLink>
    <RouterLink to="/settings" class="nav-item" :class="{ active: route.path === '/settings' }">
      <span class="nav-icon">⚙️</span>
      <span>设置</span>
    </RouterLink>
  </nav>
</template>

<script setup>
import { useRoute } from 'vue-router'
const route = useRoute()
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
}
.nav-item.active,
.nav-item:hover {
  color: var(--color-primary);
}
.nav-icon {
  font-size: 20px;
  line-height: 1;
}
</style>
