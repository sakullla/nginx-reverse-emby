<template>
  <div class="dashboard">
    <div class="dashboard__header animate-fade-in-up">
      <div class="dashboard__header-text">
        <h1 class="dashboard__title">集群概览</h1>
        <p class="dashboard__subtitle">实时监控所有节点状态</p>
      </div>
      <div class="dashboard__actions dashboard__actions--cards">
        <RouterLink to="/agents" class="dashboard__action-card">
          <span class="dashboard__action-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <ellipse cx="12" cy="5" rx="9" ry="3"/>
              <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
              <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
            </svg>
          </span>
          <span class="dashboard__action-label">查看全部节点</span>
        </RouterLink>
        <RouterLink
          v-if="defaultAgentId"
          :to="`/rules?agentId=${defaultAgentId}`"
          class="dashboard__action-card dashboard__action-card--primary"
        >
          <span class="dashboard__action-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
            </svg>
          </span>
          <span class="dashboard__action-label">创建 HTTP 规则</span>
        </RouterLink>
        <span v-else class="dashboard__action-card dashboard__action-card--disabled" title="暂无可用节点">
          <span class="dashboard__action-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
            </svg>
          </span>
          <span class="dashboard__action-label">创建 HTTP 规则</span>
        </span>
        <RouterLink
          v-if="defaultAgentId"
          :to="`/l4?agentId=${defaultAgentId}`"
          class="dashboard__action-card dashboard__action-card--primary"
        >
          <span class="dashboard__action-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
            </svg>
          </span>
          <span class="dashboard__action-label">创建 L4 规则</span>
        </RouterLink>
        <span v-else class="dashboard__action-card dashboard__action-card--disabled" title="暂无可用节点">
          <span class="dashboard__action-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
              <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
            </svg>
          </span>
          <span class="dashboard__action-label">创建 L4 规则</span>
        </span>
      </div>
    </div>

    <div class="stats-grid">
      <StatCard
        :tone="nodeHealthTone"
        size="md"
        :value="`${onlineCount} / ${agents?.length || 0}`"
        label="节点健康"
        :sub-label="offlineCount > 0 ? `${offlineCount} 个离线` : '全部在线'"
        :progress="onlinePercent"
        to="/agents"
        class="card-enter stagger-1"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
        </template>
      </StatCard>

      <StatCard
        tone="primary"
        size="md"
        :value="rulesCount"
        label="HTTP 规则"
        to="/rules"
        class="card-enter stagger-2"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </template>
      </StatCard>

      <StatCard
        tone="warning"
        size="md"
        :value="l4Count"
        label="L4 规则"
        to="/l4"
        class="card-enter stagger-3"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
            <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
          </svg>
        </template>
      </StatCard>

      <StatCard
        :tone="certTone"
        size="md"
        :value="certCount"
        label="证书"
        :sub-label="certSubLabel"
        :to="defaultAgentId ? `/certs?agentId=${defaultAgentId}` : undefined"
        class="card-enter stagger-4"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>
            <path d="M9 12l2 2 4-4"/>
          </svg>
        </template>
      </StatCard>
    </div>

    <DashboardTrafficModule class="card-enter stagger-5" />

    <div v-if="agents?.length" class="dashboard-section card-enter stagger-6">
      <div class="dashboard-section__header">
        <h2 class="dashboard-section__title">节点状态</h2>
        <RouterLink to="/agents" class="dashboard-section__link">查看全部 →</RouterLink>
      </div>
      <AgentTable
        :agents="displayedAgents"
        :show-actions="false"
        :clickable="true"
        @click="navigateToAgent"
      />
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="dashboard__loading card-enter">
      <div class="spinner"></div>
      <span>加载中...</span>
    </div>

    <!-- Empty state -->
    <div v-else-if="!agents?.length" class="dashboard__empty card-enter">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <ellipse cx="12" cy="5" rx="9" ry="3"/>
        <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
        <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
      </svg>
      <p>暂无节点</p>
      <p class="dashboard__empty-hint">点击顶部「加入节点」或顶部导航栏「加入节点」来添加第一个 Agent</p>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import { useCertificates } from '../hooks/useCertificates'
import AgentTable from '../components/AgentTable.vue'
import StatCard from '../components/base/StatCard.vue'
import DashboardTrafficModule from '../components/traffic/DashboardTrafficModule.vue'

const router = useRouter()
const { data: agents, isLoading } = useAgents()

const onlineCount = computed(() => agents.value?.filter(a => a.status === 'online').length || 0)
const offlineCount = computed(() => (agents.value?.length || 0) - onlineCount.value)
const onlinePercent = computed(() => {
  const total = agents.value?.length || 0
  return total > 0 ? Math.round((onlineCount.value / total) * 100) : 0
})

const rulesCount = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0
})
const l4Count = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0
})

const displayedAgents = computed(() => (agents.value || []).slice(0, 8))

const defaultAgentId = computed(() => {
  const list = agents.value || []
  if (!list.length) return ''
  const online = list.find(a => a.status === 'online')
  return online?.id || list[0].id
})

const { data: certs } = useCertificates(defaultAgentId)

const nodeHealthTone = computed(() => {
  if (!agents.value?.length) return 'warning'
  if (offlineCount.value > 0) return 'danger'
  return 'success'
})

const certCount = computed(() => certs.value?.length || 0)
const expiringCount = computed(() => {
  const list = certs.value || []
  const now = Date.now()
  const threshold = now + 30 * 24 * 60 * 60 * 1000
  return list.filter((cert) => {
    const raw = cert?.not_after
    if (!raw) return false
    const time = new Date(raw).getTime()
    return Number.isFinite(time) && time > now && time <= threshold
  }).length
})
const certTone = computed(() => {
  if (expiringCount.value > 0) return 'danger'
  return 'success'
})
const certSubLabel = computed(() => {
  if (expiringCount.value > 0) return `${expiringCount.value} 个即将过期`
  return '证书正常'
})

function navigateToAgent(agent) {
  router.push(`/agents/${agent.id}`)
}
</script>

<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
}

.dashboard__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-8);
}

.dashboard__header-text {
  min-width: 0;
}

.dashboard__title {
  font-size: var(--text-2xl);
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
}

.dashboard__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.dashboard__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: stretch;
  justify-content: flex-end;
  gap: var(--space-3);
  flex-shrink: 0;
}

.dashboard__actions--cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-3);
  width: 100%;
  max-width: 480px;
}

.dashboard__action-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-2);
  font-size: var(--text-sm);
  font-weight: 500;
  color: var(--color-text-secondary);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  text-decoration: none;
  transition: color var(--duration-fast) var(--ease-default),
    background var(--duration-fast) var(--ease-default),
    border-color var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default);
  min-width: 0;
}

.dashboard__action-card:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
  border-color: var(--color-border-strong);
  transform: translateY(-1px);
}

.dashboard__action-card--primary {
  color: var(--color-primary);
  border-color: var(--color-primary-subtle);
  background: var(--color-primary-subtle);
}

.dashboard__action-card--primary:hover {
  color: var(--color-primary-hover);
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
}

.dashboard__action-card--disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.dashboard__action-card--disabled:hover {
  color: var(--color-text-secondary);
  background: var(--color-bg-surface);
  border-color: var(--color-border-default);
  transform: none;
}

.dashboard__action-icon {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  background: var(--color-bg-hover);
  color: currentColor;
  flex-shrink: 0;
}

.dashboard__action-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-4);
  margin-bottom: var(--space-8);
}

@media (max-width: 1024px) {
  .stats-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
}

.dashboard__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-12);
  color: var(--color-text-secondary);
}

.dashboard__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-16) var(--space-6);
  color: var(--color-text-muted);
  text-align: center;
}

.dashboard__empty p {
  margin: 0;
  font-size: var(--text-base);
}

.dashboard__empty .dashboard__empty-hint {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.dashboard-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: var(--space-8);
  box-shadow: var(--shadow-sm);
}

.dashboard-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
}

.dashboard-section__title {
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.dashboard-section__link {
  font-size: 0.8rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
  transition: color var(--duration-fast) var(--ease-default);
}

.dashboard-section__link:hover {
  color: var(--color-primary-hover);
  text-decoration: underline;
}

@media (max-width: 768px) {
  .dashboard__header {
    flex-direction: column;
    align-items: stretch;
  }

  .dashboard__actions {
    justify-content: flex-start;
  }

  .dashboard__actions--cards {
    max-width: none;
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .dashboard__title {
    font-size: var(--text-xl);
  }
}
</style>
