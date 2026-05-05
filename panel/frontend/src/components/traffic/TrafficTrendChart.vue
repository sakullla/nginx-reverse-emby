<template>
  <div class="traffic-trend-chart">
    <apexchart
      :key="props.granularity"
      type="area"
      :options="chartOptions"
      :series="series"
      height="100%"
      width="100%"
    />
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  points: { type: Array, default: () => [] },
  prevPoints: { type: Array, default: null },
  hostPoints: { type: Array, default: null },
  granularity: { type: String, default: 'day' },
  quotaBytes: { type: Number, default: null },
  budgetBytes: { type: Number, default: null }
})

function formatLabel(bucketStart) {
  if (!bucketStart) return ''
  const date = new Date(bucketStart)
  if (Number.isNaN(date.getTime())) return ''
  if (props.granularity === 'hour') {
    return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  if (props.granularity === 'month') {
    return date.toLocaleDateString('zh-CN', { year: '2-digit', month: 'short' })
  }
  return date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function bucketKey(point) {
  return String(point?.bucket_start || '')
}

function uniqueBucketStarts(currentPoints, hostPoints) {
  const buckets = []
  for (const points of [currentPoints, hostPoints]) {
    if (!Array.isArray(points)) continue
    for (const point of points) {
      const key = bucketKey(point)
      if (key) buckets.push(key)
    }
  }
  return [...new Set(buckets)].sort()
}

function buildValueMap(points) {
  const map = new Map()
  if (!Array.isArray(points)) return map
  for (const p of points) {
    const key = bucketKey(p)
    if (key) map.set(key, Number(p.accounted_bytes) || 0)
  }
  return map
}

function alignToBuckets(bucketStarts, points) {
  const map = buildValueMap(points)
  return bucketStarts.map((bucket) => (map.has(bucket) ? map.get(bucket) : null))
}

function alignPrevSeries(bucketStarts, currentPoints, prevPoints) {
  if (!Array.isArray(currentPoints) || currentPoints.length === 0) {
    return bucketStarts.map(() => null)
  }
  const currentIndexByBucket = new Map(bucketStarts.map((bucket, index) => [bucket, index]))
  const series = bucketStarts.map(() => null)
  const values = Array.isArray(prevPoints) ? prevPoints.map((point) => Number(point?.accounted_bytes) || 0) : []
  currentPoints.forEach((point, index) => {
    const bucket = bucketKey(point)
    const targetIndex = currentIndexByBucket.get(bucket)
    if (targetIndex == null || index >= values.length) return
    series[targetIndex] = values[index]
  })
  return series
}

const labels = computed(() => {
  const bucketStarts = uniqueBucketStarts(props.points, props.hostPoints)
  return bucketStarts.map(formatLabel)
})

const series = computed(() => {
  const bucketStarts = uniqueBucketStarts(props.points, props.hostPoints)
  const datasets = []

  datasets.push({
    name: '用量',
    data: alignToBuckets(bucketStarts, props.points)
  })

  const rxData = bucketStarts.map((bucket) => {
    const point = Array.isArray(props.points) ? props.points.find((item) => bucketKey(item) === bucket) : null
    return point ? (Number(point.rx_bytes) || 0) : null
  })
  datasets.push({ name: 'RX', data: rxData })

  const txData = bucketStarts.map((bucket) => {
    const point = Array.isArray(props.points) ? props.points.find((item) => bucketKey(item) === bucket) : null
    return point ? (Number(point.tx_bytes) || 0) : null
  })
  datasets.push({ name: 'TX', data: txData })

  if (Array.isArray(props.hostPoints) && props.hostPoints.length > 0) {
    datasets.push({
      name: '主机流量',
      data: alignToBuckets(bucketStarts, props.hostPoints)
    })
  }

  if (Array.isArray(props.prevPoints) && props.prevPoints.length > 0) {
    datasets.push({
      name: '上期',
      data: alignPrevSeries(bucketStarts, props.points, props.prevPoints)
    })
  }

  if (props.budgetBytes != null && props.budgetBytes > 0 && props.granularity !== 'month') {
    datasets.push({
      name: '日均预算',
      data: bucketStarts.map(() => props.budgetBytes)
    })
  }

  if (props.quotaBytes != null && props.quotaBytes > 0 && props.granularity === 'month') {
    datasets.push({
      name: '月额度',
      data: bucketStarts.map(() => props.quotaBytes)
    })
  }

  return datasets
})

const chartOptions = computed(() => ({
  chart: {
    type: 'area',
    toolbar: { show: false },
    animations: { enabled: false },
    fontFamily: 'inherit'
  },
  colors: ['#3b82f6', '#6366f1', '#10b981', '#8b5cf6', '#9ca3af', '#f59e0b', '#ef4444'],
  stroke: {
    curve: 'smooth',
    width: [2, 1.5, 1.5, 2, 1.5, 1, 1],
    dashArray: [0, 0, 0, 0, 4, 6, 6]
  },
  fill: {
    type: ['solid', 'none', 'none', 'solid', 'none', 'none', 'none'],
    opacity: [0.12, 0, 0, 0.08, 0, 0, 0]
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
      formatter: (value) => formatBytes(value)
    }
  },
  xaxis: {
    categories: labels.value,
    tooltip: { enabled: false },
    labels: { style: { fontSize: '11px' } },
    axisBorder: { show: false },
    axisTicks: { show: false }
  },
  yaxis: {
    labels: {
      style: { fontSize: '11px' },
      formatter: (value) => formatBytes(value)
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
}))
</script>

<style scoped>
.traffic-trend-chart {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 260px;
}
</style>
