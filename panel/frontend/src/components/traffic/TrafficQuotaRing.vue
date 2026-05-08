<template>
  <div class="traffic-quota-ring">
    <apexchart
      type="donut"
      :options="chartOptions"
      :series="series"
      height="180"
    />
    <div v-if="isDistribution" class="traffic-quota-ring__legend">
      <div v-for="(agent, i) in topLegendAgents" :key="agent.agent_id" class="tqr-legend-item">
        <span class="tqr-legend-item__dot" :style="{ background: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length] }" />
        <span class="tqr-legend-item__name">{{ agent.name || agent.agent_id }}</span>
        <span class="tqr-legend-item__value">{{ formatBytes(agent.used_bytes || 0) }}</span>
      </div>
      <p v-if="extraAgentCount > 0" class="tqr-legend-more">+{{ extraAgentCount }} 个其他节点</p>
    </div>
    <div v-else class="traffic-quota-ring__info">
      <span class="traffic-quota-ring__label">{{ infoLabel }}</span>
      <span class="traffic-quota-ring__value">{{ infoValue }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota, usagePercent } from '../../utils/trafficStats.js'

const props = defineProps({
  usedBytes: { type: Number, default: 0 },
  quotaBytes: { type: Number, default: null },
  remainingBytes: { type: Number, default: null },
  agents: { type: Array, default: null }
})

const DISTRIBUTION_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f87171', '#22d3ee', '#f472b6']

const sortedAgents = computed(() => {
  if (!Array.isArray(props.agents)) return []
  return [...props.agents].sort((a, b) => (b.used_bytes || 0) - (a.used_bytes || 0))
})

const isDistribution = computed(() => sortedAgents.value.length > 1)

const topLegendAgents = computed(() => sortedAgents.value.slice(0, 5))
const extraAgentCount = computed(() => Math.max(0, sortedAgents.value.length - 5))

const singleAgent = computed(() => {
  if (Array.isArray(props.agents) && props.agents.length === 1) return props.agents[0]
  return null
})

const effectiveUsed = computed(() => singleAgent.value?.used_bytes ?? props.usedBytes ?? 0)
const effectiveQuota = computed(() => singleAgent.value?.quota_bytes ?? props.quotaBytes ?? null)
const effectiveRemaining = computed(() => singleAgent.value?.remaining_bytes ?? props.remainingBytes ?? null)

const percent = computed(() => usagePercent(effectiveUsed.value, effectiveQuota.value))

const color = computed(() => {
  const p = percent.value ?? 0
  if (p >= 90) return 'var(--color-danger)'
  if (p >= 70) return 'var(--color-warning)'
  return 'var(--color-success)'
})

const series = computed(() => {
  if (isDistribution.value) {
    return sortedAgents.value.map(a => a.used_bytes || 0)
  }
  if (effectiveQuota.value == null || effectiveQuota.value <= 0) {
    return [effectiveUsed.value || 0]
  }
  const used = effectiveUsed.value || 0
  const remaining = Math.max(0, (effectiveRemaining.value != null ? effectiveRemaining.value : effectiveQuota.value - used))
  return [used, remaining]
})

const chartLabels = computed(() => {
  if (isDistribution.value) {
    return sortedAgents.value.map(a => a.name || a.agent_id)
  }
  if (effectiveQuota.value == null || effectiveQuota.value <= 0) {
    return ['已用']
  }
  return ['已用', '剩余']
})

const chartColors = computed(() => {
  if (isDistribution.value) {
    return sortedAgents.value.map((_, i) => DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length])
  }
  return [color.value, 'var(--color-border-default)']
})

const chartOptions = computed(() => ({
  chart: {
    type: 'donut',
    toolbar: { show: false },
    animations: { enabled: true },
    foreColor: 'var(--color-text-secondary)'
  },
  labels: chartLabels.value,
  colors: chartColors.value,
  plotOptions: {
    pie: {
      donut: {
        size: '70%',
        labels: {
          show: true,
          name: { show: false },
          value: {
            show: true,
            fontSize: '20px',
            fontWeight: 700,
            color: 'var(--color-text-primary)',
            formatter: () => {
              if (isDistribution.value) {
                const total = props.agents.reduce((s, a) => s + (a.used_bytes || 0), 0)
                return formatBytes(total)
              }
              if (effectiveQuota.value == null) return '—'
              return `${percent.value ?? 0}%`
            }
          },
          total: {
            show: true,
            showAlways: true,
            label: isDistribution.value ? '总用量' : '额度',
            color: 'var(--color-text-tertiary)',
            fontSize: '11px',
            formatter: () => {
              if (isDistribution.value) {
                const total = props.agents.reduce((s, a) => s + (a.used_bytes || 0), 0)
                return formatBytes(total)
              }
              return effectiveQuota.value == null ? '—' : `${percent.value ?? 0}%`
            }
          }
        }
      }
    }
  },
  dataLabels: { enabled: false },
  legend: { show: false },
  stroke: { show: true, colors: ['var(--color-bg-surface-raised, var(--color-bg-surface))'], width: 2 },
  tooltip: {
    y: {
      formatter: (value) => formatBytes(value)
    }
  }
}))

const infoLabel = computed(() => {
  if (isDistribution.value) return '节点分布'
  return '已用 / 额度'
})

const infoValue = computed(() => {
  if (isDistribution.value) {
    const total = props.agents.reduce((s, a) => s + (a.used_bytes || 0), 0)
    return formatBytes(total)
  }
  return `${formatBytes(effectiveUsed.value)} / ${formatQuota(effectiveQuota.value)}`
})
</script>

<style scoped>
.traffic-quota-ring {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 0.5rem;
}
.traffic-quota-ring__legend {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  width: 100%;
  margin-top: 0.5rem;
}
.tqr-legend-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.75rem;
}
.tqr-legend-item__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}
.tqr-legend-item__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.tqr-legend-item__value {
  color: var(--color-text-secondary);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.tqr-legend-more {
  margin: 0.25rem 0 0;
  padding-left: 1rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}

.traffic-quota-ring__info {
  text-align: center;
}
.traffic-quota-ring__label {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.traffic-quota-ring__value {
  display: block;
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
</style>
