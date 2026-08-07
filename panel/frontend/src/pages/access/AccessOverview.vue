<script setup>
import { computed, onMounted, ref } from 'vue'
import {
  fetchAuditEvents,
  fetchQuotaPolicies,
  fetchResourceGroups,
  fetchRoles,
  fetchSecrets,
  fetchUsers
} from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'

const { can, refreshActor, visibleNavigation } = useAccessControl()
const loading = ref(true)
const error = ref(null)
const counts = ref({})

const cards = computed(() => visibleNavigation.value.map((item) => ({
  ...item,
  count: counts.value[item.id]
})))

onMounted(async () => {
  try {
    await refreshActor()
    const requests = []
    const assign = (id, request) => requests.push(request().then((items) => { counts.value[id] = items.length }))
    if (can('access.manage')) {
      assign('users', fetchUsers)
      assign('roles', fetchRoles)
    }
    if (can('resource.read')) assign('resource-groups', fetchResourceGroups)
    if (can('quota.manage')) assign('quotas', fetchQuotaPolicies)
    if (can('secret.use')) assign('secrets', fetchSecrets)
    if (can('audit.read')) assign('audit', () => fetchAuditEvents(20))
    await Promise.all(requests)
  } catch (cause) {
    error.value = cause
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <main class="access-overview">
    <header>
      <h1>访问与安全</h1>
      <p>用户、角色、资源范围、配额、凭据和审计使用同一控制面权限模型。</p>
    </header>
    <p v-if="loading">正在加载…</p>
    <p v-else-if="error" role="alert">{{ error.message }}</p>
    <nav v-else aria-label="访问与安全">
      <RouterLink v-for="card in cards" :key="card.id" :to="card.path" class="access-card">
        <strong>{{ card.label }}</strong>
        <span v-if="card.count !== undefined">{{ card.count }}</span>
      </RouterLink>
    </nav>
  </main>
</template>

<style scoped>
.access-overview {
  display: grid;
  gap: 1.25rem;
}

nav {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: 0.75rem;
}

.access-card {
  display: flex;
  justify-content: space-between;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 0.75rem;
  padding: 1rem;
  text-decoration: none;
}
</style>
