<script setup>
import { computed, onMounted, ref } from 'vue'
import {
  fetchAuditEvents,
  fetchQuotaOverview,
  fetchResourceGroups,
  fetchRoles,
  fetchSecrets,
  fetchUsers
} from '../../api/access'
import { useAccessControl } from '../../context/useAccessControl'
import QuotaUsage from '../../components/access/QuotaUsage.vue'

const { can, refreshActor, visibleNavigation } = useAccessControl()
const loading = ref(true)
const error = ref(null)
const counts = ref({})
const quotaUsage = ref([])

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
    if (can('resource.read')) {
      requests.push(fetchQuotaOverview().then((payload) => {
        quotaUsage.value = payload.quota_usage || []
        counts.value.quotas = quotaUsage.value.length
      }))
    }
    if (can('secret.metadata.read')) assign('secrets', fetchSecrets)
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
    <section v-else aria-label="访问与安全概览">
      <article v-for="card in cards" :key="card.id" class="access-card">
        <div class="access-card-heading">
          <strong>{{ card.label }}</strong>
          <span v-if="card.count !== undefined">{{ card.count }}</span>
        </div>
        <QuotaUsage
          v-for="usage in card.id === 'quotas' ? quotaUsage : []"
          :key="usage.policy_id"
          :current="usage.current"
          :limit="usage.limit"
          :recovery-condition="usage.recovery_condition"
        />
      </article>
    </section>
  </main>
</template>

<style scoped>
.access-overview {
  display: grid;
  gap: 1.25rem;
}

section {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: 0.75rem;
}

.access-card {
  display: grid;
  gap: 0.75rem;
  border: 1px solid var(--border-color, #e5e7eb);
  border-radius: 0.75rem;
  padding: 1rem;
}

.access-card-heading {
  display: flex;
  justify-content: space-between;
}
</style>
