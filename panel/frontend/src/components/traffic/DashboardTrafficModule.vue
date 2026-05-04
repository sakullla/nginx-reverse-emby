<template>
  <div v-if="visible" class="dashboard-traffic">
    <div class="dashboard-traffic__header">
      <h2 class="dashboard-traffic__title">流量统计</h2>
      <div class="dashboard-traffic__selector">
        <select v-model="selectedAgentId" class="dashboard-traffic__select">
          <option value="">全部节点</option>
          <option v-for="agent in overviewAgents" :key="agent.agent_id" :value="agent.agent_id">{{ agent.name }}</option>
        </select>
      </div>
    </div>
    <div v-if="overviewQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>
    <template v-else>
      <div class="dashboard-traffic__chart">
        <TrafficTrendChart :points="trendPoints" granularity="day" :quota-bytes="selectedSummary?.quota_bytes ?? null" />
      </div>
      <div class="dashboard-traffic__cards">
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">已用</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(selectedSummary?.used_bytes ?? 0) }}</span>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">额度</span>
          <span class="dashboard-traffic__card-value">{{ formatQuota(selectedSummary?.quota_bytes ?? null) }}</span>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">剩余</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(selectedSummary?.remaining_bytes ?? 0) }}</span>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useTrafficOverview } from '../../hooks/useTrafficOverview.js'
import { useQuery } from '@tanstack/vue-query'
import { fetchSystemInfo } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { formatBytes, formatQuota } from '../../utils/trafficStats.js'

const { data: systemInfo } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})

const visible = computed(() => systemInfo.value?.traffic_stats_enabled !== false)

const selectedAgentId = ref('')

const overviewQuery = useTrafficOverview(selectedAgentId)

const overviewAgents = computed(() => overviewQuery.data.value?.agents ?? [])

const trendPoints = computed(() => {
  const raw = overviewQuery.data.value?.trend ?? []
  return raw.map(p => ({
    bucket_start: p.bucket_start,
    rx_bytes: Number(p.rx_bytes) || 0,
    tx_bytes: Number(p.tx_bytes) || 0,
    accounted_bytes: Number(p.accounted_bytes) || 0
  }))
})

const selectedSummary = computed(() => {
  const agents = overviewAgents.value
  if (selectedAgentId.value) {
    return agents.find(a => a.agent_id === selectedAgentId.value) ?? null
  }
  if (!agents.length) return null
  return {
    used_bytes: agents.reduce((s, a) => s + (a.used_bytes || 0), 0),
    quota_bytes: agents.every(a => a.quota_bytes == null) ? null : agents.reduce((s, a) => s + (a.quota_bytes || 0), 0),
    remaining_bytes: agents.reduce((s, a) => s + (a.remaining_bytes || 0), 0)
  }
})
</script>

<style scoped>
.dashboard-traffic {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: 2.5rem;
}
.dashboard-traffic__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dashboard-traffic__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}
.dashboard-traffic__select {
  padding: 0.35rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-family: inherit;
  cursor: pointer;
}
.dashboard-traffic__loading {
  display: flex;
  justify-content: center;
  padding: 2rem;
}
.dashboard-traffic__chart {
  padding: 1rem 1.25rem 0;
}
.dashboard-traffic__cards {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.75rem;
  padding: 1rem 1.25rem 1.25rem;
}
.dashboard-traffic__card {
  padding: 0.5rem 0.75rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
.dashboard-traffic__card-label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.dashboard-traffic__card-value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
