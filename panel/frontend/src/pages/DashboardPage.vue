<template>
  <div class="dashboard">
    <div class="dashboard__header animate-fade-in-up">
      <div class="dashboard__header-text">
        <h1 class="dashboard__title">集群概览</h1>
        <p class="dashboard__subtitle">实时监控所有节点状态</p>
      </div>
    </div>

    <AttentionBar :attention="attention" class="dashboard__attention card-enter stagger-1" />

    <DashboardTrafficModule v-if="trafficEnabled" class="card-enter stagger-2">
      <template #side>
        <ClusterMetricsCard
          :agents="agents || []"
          :certs-total="certCount"
          :certs-expiring="expiringCount"
          :default-agent-id="defaultAgentId"
        />
      </template>
      <template #nodes>
        <div class="dashboard__nodes-header">
          <h2 class="dashboard__nodes-title">节点状态</h2>
          <RouterLink to="/agents" class="dashboard__nodes-link">查看全部 →</RouterLink>
        </div>
        <AgentStatusTiles :agents="displayedAgents" />
      </template>
    </DashboardTrafficModule>

    <!-- 流量统计关闭时的降级布局:集群指标 + 节点状态独立成行 -->
    <div v-else class="dashboard__fallback card-enter stagger-2">
      <section class="dashboard__fallback-cell">
        <ClusterMetricsCard
          :agents="agents || []"
          :certs-total="certCount"
          :certs-expiring="expiringCount"
          :default-agent-id="defaultAgentId"
        />
      </section>
      <section v-if="agents?.length" class="dashboard__fallback-cell">
        <div class="dashboard__nodes-header">
          <h2 class="dashboard__nodes-title">节点状态</h2>
          <RouterLink to="/agents" class="dashboard__nodes-link">查看全部 →</RouterLink>
        </div>
        <AgentStatusTiles :agents="displayedAgents" />
      </section>
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
import { useQuery } from '@tanstack/vue-query'
import { useAgents } from '../hooks/useAgents'
import { useCertificates } from '../hooks/useCertificates'
import { useAttention } from '../hooks/useAttention'
import { fetchSystemInfo } from '../api'
import AttentionBar from '../components/dashboard/AttentionBar.vue'
import AgentStatusTiles from '../components/dashboard/AgentStatusTiles.vue'
import ClusterMetricsCard from '../components/dashboard/ClusterMetricsCard.vue'
import DashboardTrafficModule from '../components/traffic/DashboardTrafficModule.vue'

const { data: agents, isLoading } = useAgents()

const { data: attentionData } = useAttention()
const attention = computed(() => attentionData.value?.ok ? attentionData.value : null)

const { data: systemInfo } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})
const trafficEnabled = computed(() => !!systemInfo.value && systemInfo.value.traffic_stats_enabled !== false)

const displayedAgents = computed(() => (agents.value || []).slice(0, 12))

const defaultAgentId = computed(() => {
  const list = agents.value || []
  if (!list.length) return ''
  const online = list.find(a => a.status === 'online')
  return online?.id || list[0].id
})

const { data: certs } = useCertificates(defaultAgentId)

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
  margin-bottom: var(--space-5);
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

.dashboard__attention {
  margin-bottom: var(--space-4);
}

.dashboard__nodes-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.dashboard__nodes-title {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.dashboard__nodes-link {
  display: inline-flex;
  align-items: center;
  padding: 0.25rem 0.75rem;
  border-radius: var(--radius-full);
  font-size: 0.8125rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
  transition: background var(--duration-fast) var(--ease-default),
    color var(--duration-fast) var(--ease-default);
}

.dashboard__nodes-link:hover {
  color: var(--color-primary-hover);
  background: var(--color-primary-subtle);
}

/* 流量统计关闭时的降级布局 */
.dashboard__fallback {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 2.2fr);
  gap: var(--space-4);
  margin-bottom: var(--space-4);
}

.dashboard__fallback-cell {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-xs);
  padding: var(--space-4) var(--space-5);
  min-width: 0;
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

@media (max-width: 1024px) {
  .dashboard__fallback {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 768px) {
  .dashboard__header {
    flex-direction: column;
    align-items: stretch;
  }
}

@media (max-width: 640px) {
  .dashboard__title {
    font-size: var(--text-xl);
  }
}
/* Wide-screen (2K/4K) width steps */
@media (min-width: 1920px) {
  .dashboard { max-width: 1600px; }
}
@media (min-width: 2560px) {
  .dashboard { max-width: 2000px; }
}
</style>
