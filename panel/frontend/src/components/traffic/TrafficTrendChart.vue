<template>
  <div class="traffic-trend-chart">
    <apexchart
      v-if="hasData"
      :key="chartKey"
      type="area"
      :options="chartOptions"
      :series="series"
      height="100%"
      width="100%"
    />
    <div v-else class="traffic-trend-chart__empty">
      <span class="traffic-trend-chart__empty-text">暂无数据</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  points: { type: Array, default: () => [] },
  prevPoints: { type: Array, default: null },
  granularity: { type: String, default: 'day' },
  quotaBytes: { type: Number, default: null },
  budgetBytes: { type: Number, default: null },
  refreshKey: { type: [Number, String], default: '' }
})

const hasData = computed(() => {
  return Array.isArray(props.points) && props.points.length > 0
})

const hourDataVersion = ref(0)

watch(
  () => props.points,
  (points, previousPoints) => {
    if (props.granularity === 'hour' && previousPoints && points !== previousPoints) {
      hourDataVersion.value += 1
    }
  }
)

const chartKey = computed(() => {
  const pointSignature = props.points.map((point) => [
    point?.bucket_start || '',
    point?.bucket_local_start || '',
    Number(point?.accounted_bytes) || 0,
    Number(point?.rx_bytes) || 0,
    Number(point?.tx_bytes) || 0
  ].join(':')).join('|')
  const prevSignature = Array.isArray(props.prevPoints)
    ? props.prevPoints.map((point) => [
      point?.bucket_start || '',
      point?.bucket_local_start || '',
      Number(point?.accounted_bytes) || 0
    ].join(':')).join('|')
    : ''
  const dataVersion = props.granularity === 'hour' ? hourDataVersion.value : ''
  return `${props.granularity}-${props.quotaBytes ?? ''}-${props.budgetBytes ?? ''}-${props.refreshKey}-${dataVersion}-${pointSignature}-${prevSignature}`
})

function localDateParts(value) {
  const match = String(value || '').match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/)
  if (!match) return null
  return {
    year: Number(match[1]),
    month: Number(match[2]),
    day: Number(match[3]),
    hour: match[4],
    minute: match[5]
  }
}

function formatLabel(point) {
  const parts = localDateParts(point?.bucket_local_start || point?.bucket_start)
  if (!parts) return ''
  if (props.granularity === 'hour') {
    return `${parts.hour}:${parts.minute}`
  }
  if (props.granularity === 'month') {
    return `${String(parts.year).slice(-2)}年${parts.month}月`
  }
  return `${parts.month}月${parts.day}日`
}

function formatChartBytes(value) {
  if (value == null || value === '') return ''
  try {
    const number = Number(value)
    if (!Number.isFinite(number)) return ''
    return formatBytes(number)
  } catch {
    return ''
  }
}

function bucketKey(point) {
  return String(point?.bucket_start || '')
}

function uniqueBucketStarts(currentPoints) {
  const buckets = []
  if (!Array.isArray(currentPoints)) return buckets
  for (const point of currentPoints) {
    const key = bucketKey(point)
    if (key) buckets.push(key)
  }
  return [...new Set(buckets)].sort()
}

function buildBucketMap(points) {
  const map = new Map()
  if (!Array.isArray(points)) return map
  for (const point of points) {
    const key = bucketKey(point)
    if (!key) continue
    let entry = map.get(key)
    if (!entry) {
      entry = {
        bucket_start: key,
        bucket_local_start: String(point?.bucket_local_start || ''),
        rx_bytes: 0,
        tx_bytes: 0,
        accounted_bytes: 0
      }
      map.set(key, entry)
    }
    entry.rx_bytes += Number(point?.rx_bytes) || 0
    entry.tx_bytes += Number(point?.tx_bytes) || 0
    entry.accounted_bytes += Number(point?.accounted_bytes) || 0
  }
  return map
}

function alignToBuckets(bucketStarts, points) {
  const map = buildBucketMap(points)
  return bucketStarts.map((bucket) => map.get(bucket) || null)
}

function alignPrevSeries(bucketStarts, currentPoints, prevPoints) {
  if (!Array.isArray(currentPoints) || currentPoints.length === 0) {
    return []
  }
  const values = Array.isArray(prevPoints) ? prevPoints.map((point) => Number(point?.accounted_bytes) || 0) : []
  return bucketStarts.map((_, index) => (index < values.length ? values[index] : null))
}

const bucketStarts = computed(() => uniqueBucketStarts(props.points))
const alignedPoints = computed(() => alignToBuckets(bucketStarts.value, props.points))

const labels = computed(() => {
  return alignedPoints.value.map(formatLabel)
})

const series = computed(() => {
  const points = alignedPoints.value
  const datasets = []

  datasets.push({
    name: '用量',
    data: points.map((point) => point?.accounted_bytes ?? null)
  })

  datasets.push({ name: 'RX', data: points.map((point) => point?.rx_bytes ?? null) })
  datasets.push({ name: 'TX', data: points.map((point) => point?.tx_bytes ?? null) })

  if (Array.isArray(props.prevPoints) && props.prevPoints.length > 0) {
    datasets.push({
      name: '上期',
      data: alignPrevSeries(bucketStarts.value, props.points, props.prevPoints)
    })
  }

  if (props.budgetBytes != null && props.budgetBytes > 0 && props.granularity !== 'month') {
    datasets.push({
      name: '日均预算',
      data: bucketStarts.value.map(() => props.budgetBytes)
    })
  }

  if (props.quotaBytes != null && props.quotaBytes > 0 && props.granularity === 'month') {
    datasets.push({
      name: '月额度',
      data: bucketStarts.value.map(() => props.quotaBytes)
    })
  }

  return datasets
})

const seriesStyles = {
  '用量': { color: '#3b82f6', width: 2, dashArray: 0, fillType: 'solid', fillOpacity: 0.12 },
  RX: { color: '#6366f1', width: 1.5, dashArray: 0, fillType: 'none', fillOpacity: 0 },
  TX: { color: '#10b981', width: 1.5, dashArray: 0, fillType: 'none', fillOpacity: 0 },
  '上期': { color: '#8b5cf6', width: 2, dashArray: 0, fillType: 'solid', fillOpacity: 0.08 },
  '日均预算': { color: '#f59e0b', width: 1, dashArray: 6, fillType: 'none', fillOpacity: 0 },
  '月额度': { color: '#ef4444', width: 1, dashArray: 6, fillType: 'none', fillOpacity: 0 }
}

const fallbackSeriesStyle = { color: '#9ca3af', width: 1.5, dashArray: 4, fillType: 'none', fillOpacity: 0 }

const chartSeriesStyles = computed(() => {
  return series.value.map((item) => seriesStyles[item.name] || fallbackSeriesStyle)
})

const chartOptions = computed(() => {
  void chartKey.value
  return {
    chart: {
      type: 'area',
      toolbar: { show: false },
      animations: { enabled: false },
      fontFamily: 'inherit'
    },
    colors: chartSeriesStyles.value.map((style) => style.color),
    stroke: {
      curve: 'smooth',
      width: chartSeriesStyles.value.map((style) => style.width),
      dashArray: chartSeriesStyles.value.map((style) => style.dashArray)
    },
    fill: {
      type: chartSeriesStyles.value.map((style) => style.fillType),
      opacity: chartSeriesStyles.value.map((style) => style.fillOpacity)
    },
    dataLabels: { enabled: false },
    legend: {
      position: 'top',
      fontSize: '12px',
      markers: { width: 12, height: 12, radius: 2 }
    },
    tooltip: {
      shared: true,
      intersect: false,
      y: {
        formatter: formatChartBytes
      }
    },
    xaxis: {
      categories: labels.value,
      tooltip: { enabled: false },
      labels: {
        style: { fontSize: '11px' },
        rotate: labels.value.length > 12 ? -45 : 0,
        rotateAlways: labels.value.length > 12,
        hideOverlappingLabels: true
      },
      axisBorder: { show: false },
      axisTicks: { show: false }
    },
    yaxis: {
      labels: {
        style: { fontSize: '11px' },
        formatter: formatChartBytes
      }
    },
    grid: {
      borderColor: 'rgba(0,0,0,0.05)',
      strokeDashArray: 0,
      xaxis: { lines: { show: false } }
    },
    markers: {
      size: 0,
      hover: { size: 0 }
    }
  }
})
</script>

<style scoped>
.traffic-trend-chart {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 260px;
}
.traffic-trend-chart__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  min-height: 260px;
}
.traffic-trend-chart__empty-text {
  font-size: 0.875rem;
  color: var(--color-text-muted);
}
</style>
