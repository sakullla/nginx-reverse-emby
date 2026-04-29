<template>
  <div class="dashboard">
    <div class="dashboard__header">
      <h1 class="dashboard__title">集群概览</h1>
      <p class="dashboard__subtitle">实时监控所有节点状态</p>
    </div>

    <div class="stats-grid">
      <StatCard tone="primary" :value="agents?.length || 0" label="总节点数">
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
        </template>
      </StatCard>

      <StatCard tone="success" :value="onlineCount" label="在线节点">
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
            <polyline points="22 4 12 14.01 9 11.01"/>
          </svg>
        </template>
      </StatCard>

      <StatCard tone="primary" :value="rulesCount" label="HTTP 规则">
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </template>
      </StatCard>

      <StatCard tone="warning" :value="l4Count" label="L4 规则">
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
            <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
          </svg>
        </template>
      </StatCard>
    </div>

    <div v-if="agents?.length" class="dashboard-section">
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
    <div v-if="isLoading" class="dashboard__loading">
      <div class="spinner"></div>
      <span>加载中...</span>
    </div>

    <!-- Empty state -->
    <div v-else-if="!agents?.length" class="dashboard__empty">
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
import AgentTable from '../components/agents/AgentTable.vue'
import StatCard from '../components/base/StatCard.vue'

const router = useRouter()
const { data: agents, isLoading } = useAgents()

const onlineCount = computed(() => agents.value?.filter(a => a.status === 'online').length || 0)

const rulesCount = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0
})

const l4Count = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0
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
  margin-bottom: 2.5rem;
}
.dashboard__title {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 0.25rem;
}
.dashboard__subtitle {
  font-size: 0.875rem;
  color: var(--color-text-tertiary);
  margin: 0;
}
.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 1rem;
  margin-bottom: 2.5rem;
}
.dashboard__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3rem;
  color: var(--color-text-secondary);
}
.dashboard__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}
.dashboard__empty p {
  margin: 0;
  font-size: 1rem;
}
.dashboard__empty-hint {
  font-size: 0.875rem !important;
  color: var(--color-text-tertiary) !important;
}
.dashboard-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); overflow: hidden; margin-bottom: 2.5rem; }
.dashboard-section__header { display: flex; align-items: center; justify-content: space-between; padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-section__title { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); margin: 0; }
.dashboard-section__link { font-size: 0.8rem; color: var(--color-primary); text-decoration: none; }
</style>
