<template>
  <div class="traffic-quota-ring">
    <apexchart
      type="donut"
      :options="chartOptions"
      :series="series"
      height="200"
    />
    <div class="traffic-quota-ring__info">
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

const DISTRIBUTION_COLORS = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#6366f1', '#ec4899']

const isDistribution = computed(() => Array.isArray(props.agents) && props.agents.length > 1)

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
  if (p >= 90) return '#ef4444'
  if (p >= 70) return '#f59e0b'
  return '#10b981'
})

const series = computed(() => {
  if (isDistribution.value) {
    return props.agents.map(a => a.used_bytes || 0)
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
    return props.agents.map(a => a.name || a.agent_id)
  }
  if (effectiveQuota.value == null || effectiveQuota.value <= 0) {
    return ['已用']
  }
  return ['已用', '剩余']
})

const chartColors = computed(() => {
  if (isDistribution.value) {
    return DISTRIBUTION_COLORS
  }
  return [color.value, '#e5e7eb']
})

const chartOptions = computed(() => ({
  chart: {
    type: 'donut',
    toolbar: { show: false },
    animations: { enabled: true }
  },
  labels: chartLabels.value,
  colors: chartColors.value,
  plotOptions: {
    pie: {
      donut: {
        size: '75%',
        labels: {
          show: true,
          name: { show: false },
          value: {
            show: true,
            fontSize: '22px',
            fontWeight: 700,
            color: '#374151',
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
  stroke: { show: false },
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
