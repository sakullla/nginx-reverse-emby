<template>
  <div class="dashboard">
    <div class="dashboard__header animate-fade-in-up">
      <h1 class="dashboard__title">集群概览</h1>
      <p class="dashboard__subtitle">实时监控所有节点状态</p>
    </div>

    <div class="stats-grid">
      <!-- Node Health Card -->
      <StatCard
        size="lg"
        :tone="healthTone"
        :value="`${onlineCount} / ${agents?.length || 0}`"
        :sub-label="healthSubLabel"
        :progress="onlinePercent"
        label="节点健康度"
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

      <!-- Rules Overview Card -->
      <StatCard
        size="lg"
        tone="primary"
        :value="totalRules"
        :sub-label="rulesSubLabel"
        label="规则概览"
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
    </div>

    <DashboardTrafficModule class="card-enter stagger-3" />

    <div v-if="agents?.length" class="dashboard-section card-enter stagger-4">
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
      <p class="dashboard__empty-hint">点击顶部导航栏「加入节点」来添加第一个 Agent</p>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
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

// Node health card data
const healthTone = computed(() => {
  if (offlineCount.value > 0) return 'warning'
  return 'success'
})
const healthSubLabel = computed(() => {
  const total = agents.value?.length || 0
  if (total === 0) return ''
  if (offlineCount.value > 0) return `${offlineCount.value} 个节点离线`
  return '全部在线'
})

// Rules overview card data
const rulesCount = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0
})
const l4Count = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0
})
const totalRules = computed(() => rulesCount.value + l4Count.value)
const rulesSubLabel = computed(() => {
  if (totalRules.value === 0) return ''
  return `HTTP ${rulesCount.value} / L4 ${l4Count.value}`
})

const displayedAgents = computed(() => (agents.value || []).slice(0, 8))

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
  margin-bottom: var(--space-8);
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

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-4);
  margin-bottom: var(--space-8);
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

.dashboard__empty-hint {
  font-size: var(--text-sm) !important;
  color: var(--color-text-tertiary) !important;
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

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
  .dashboard__title {
    font-size: var(--text-xl);
  }
}
</style>
