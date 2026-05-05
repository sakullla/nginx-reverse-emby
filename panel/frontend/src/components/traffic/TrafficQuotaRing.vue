<template>
  <div class="traffic-quota-ring">
    <apexchart
      type="donut"
      :options="chartOptions"
      :series="series"
      height="200"
    />
    <div class="traffic-quota-ring__info">
      <span class="traffic-quota-ring__label">已用 / 额度</span>
      <span class="traffic-quota-ring__value">{{ usedText }} / {{ quotaText }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota, usagePercent } from '../../utils/trafficStats.js'

const props = defineProps({
  usedBytes: { type: Number, default: 0 },
  quotaBytes: { type: Number, default: null },
  remainingBytes: { type: Number, default: null }
})

const percent = computed(() => usagePercent(props.usedBytes, props.quotaBytes))

const color = computed(() => {
  const p = percent.value ?? 0
  if (p >= 90) return '#ef4444'
  if (p >= 70) return '#f59e0b'
  return '#10b981'
})

const series = computed(() => {
  if (props.quotaBytes == null || props.quotaBytes <= 0) {
    return [props.usedBytes || 0]
  }
  const used = props.usedBytes || 0
  const remaining = Math.max(0, (props.remainingBytes != null ? props.remainingBytes : props.quotaBytes - used))
  return [used, remaining]
})

const chartOptions = computed(() => ({
  chart: {
    type: 'donut',
    toolbar: { show: false },
    animations: { enabled: true }
  },
  colors: [color.value, '#e5e7eb'],
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
            formatter: () => `${percent.value ?? 0}%`
          },
          total: {
            show: false
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

const usedText = computed(() => formatBytes(props.usedBytes))
const quotaText = computed(() => formatQuota(props.quotaBytes))
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
