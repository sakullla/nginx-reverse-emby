<template>
  <div class="traffic-rate-sparkline">
    <div class="traffic-rate-sparkline__header">
      <span class="traffic-rate-sparkline__label">{{ labelText }}</span>
      <span class="traffic-rate-sparkline__value">{{ currentRate }}</span>
    </div>
    <apexchart
      type="area"
      :options="chartOptions"
      :series="series"
      height="60"
    />
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  points: { type: Array, default: () => [] },
  granularity: { type: String, default: 'day' }
})

const labelText = computed(() => {
  switch (props.granularity) {
    case 'hour': return '当前小时'
    case 'day': return '今日用量'
    case 'month': return '当月用量'
    default: return '当前用量'
  }
})

const sparkData = computed(() => {
  const pts = props.points || []
  return pts.map(p => Number(p?.accounted_bytes) || 0)
})

const currentRate = computed(() => {
  const data = sparkData.value
  if (!data.length) return '—'
  return formatBytes(data[data.length - 1])
})

const series = computed(() => [{
  name: '用量',
  data: sparkData.value
}])

const chartOptions = computed(() => ({
  chart: {
    type: 'area',
    sparkline: { enabled: true },
    toolbar: { show: false },
    animations: { enabled: false }
  },
  colors: ['#3b82f6'],
  stroke: { curve: 'smooth', width: 2 },
  fill: { opacity: 0.2 },
  tooltip: {
    enabled: true,
    x: { show: false },
    y: {
      formatter: (value) => formatBytes(value)
    },
    marker: { show: false }
  }
}))
</script>

<style scoped>
.traffic-rate-sparkline {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.traffic-rate-sparkline__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 0.25rem;
}
.traffic-rate-sparkline__label {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.traffic-rate-sparkline__value {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
</style>
