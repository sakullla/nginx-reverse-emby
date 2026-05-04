<template>
  <div class="traffic-trend-chart">
    <canvas ref="canvasRef"></canvas>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { Chart, registerables } from 'chart.js'
import { formatBytes } from '../../utils/trafficStats.js'

Chart.register(...registerables)

const props = defineProps({
  points: { type: Array, default: () => [] },
  prevPoints: { type: Array, default: null },
  hostPoints: { type: Array, default: null },
  granularity: { type: String, default: 'day' },
  quotaBytes: { type: Number, default: null },
  budgetBytes: { type: Number, default: null }
})

const canvasRef = ref(null)
let chartInstance = null

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

function alignPrevData(labels, currentPoints, prevPoints) {
  if (!Array.isArray(prevPoints) || prevPoints.length === 0) return labels.map(() => null)
  const map = new Map()
  for (const p of prevPoints) {
    const key = String(p.bucket_start || '')
    if (key) map.set(key, Number(p.accounted_bytes) || 0)
  }
  return currentPoints.map(p => map.get(String(p.bucket_start || '')) ?? null)
}

function buildConfig() {
  const labels = props.points.map(p => formatLabel(p.bucket_start))
  const accountedData = props.points.map(p => Number(p.accounted_bytes) || 0)
  const rxData = props.points.map(p => Number(p.rx_bytes) || 0)
  const txData = props.points.map(p => Number(p.tx_bytes) || 0)
  const datasets = [
    {
      label: '用量',
      data: accountedData,
      borderColor: 'rgba(59, 130, 246, 0.9)',
      backgroundColor: 'rgba(59, 130, 246, 0.12)',
      fill: true,
      tension: 0.3,
      pointRadius: 2,
      pointHoverRadius: 5,
      order: 1
    },
    {
      label: 'RX',
      data: rxData,
      borderColor: 'rgba(99, 102, 241, 0.6)',
      backgroundColor: 'rgba(99, 102, 241, 0.05)',
      fill: false,
      tension: 0.3,
      pointRadius: 1,
      pointHoverRadius: 3,
      borderWidth: 1.5,
      order: 2
    },
    {
      label: 'TX',
      data: txData,
      borderColor: 'rgba(16, 185, 129, 0.6)',
      backgroundColor: 'rgba(16, 185, 129, 0.05)',
      fill: false,
      tension: 0.3,
      pointRadius: 1,
      pointHoverRadius: 3,
      borderWidth: 1.5,
      order: 3
    }
  ]
  if (Array.isArray(props.hostPoints) && props.hostPoints.length > 0) {
    const hostData = alignPrevData(labels, props.points, props.hostPoints)
    datasets.push({
      label: '主机流量',
      data: hostData,
      borderColor: 'rgba(139, 92, 246, 0.8)',
      backgroundColor: 'rgba(139, 92, 246, 0.08)',
      fill: true,
      tension: 0.3,
      pointRadius: 1,
      pointHoverRadius: 4,
      borderWidth: 2,
      order: 4
    })
  }
  if (Array.isArray(props.prevPoints) && props.prevPoints.length > 0) {
    const prevData = alignPrevData(labels, props.points, props.prevPoints)
    datasets.push({
      label: '上期',
      data: prevData,
      borderColor: 'rgba(156, 163, 175, 0.7)',
      backgroundColor: 'transparent',
      borderDash: [4, 4],
      fill: false,
      tension: 0.3,
      pointRadius: 0,
      pointHoverRadius: 3,
      borderWidth: 1.5,
      order: 4,
      spanGaps: true
    })
  }
  if (props.budgetBytes != null && props.budgetBytes > 0 && props.granularity !== 'month') {
    datasets.push({
      label: '日均预算',
      data: labels.map(() => props.budgetBytes),
      borderColor: 'rgba(245, 158, 11, 0.6)',
      borderDash: [6, 3],
      borderWidth: 1,
      pointRadius: 0,
      fill: false,
      order: 5
    })
  }
  if (props.quotaBytes != null && props.quotaBytes > 0 && props.granularity === 'month') {
    datasets.push({
      label: '月额度',
      data: labels.map(() => props.quotaBytes),
      borderColor: 'rgba(239, 68, 68, 0.5)',
      borderDash: [6, 4],
      borderWidth: 1,
      pointRadius: 0,
      fill: false,
      order: 6
    })
  }
  return {
    type: 'line',
    data: { labels, datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { display: true, position: 'top', labels: { boxWidth: 12, padding: 12, font: { size: 12 } } },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const value = ctx.parsed.y
              return ` ${ctx.dataset.label}: ${formatBytes(value)}`
            }
          }
        }
      },
      scales: {
        x: {
          grid: { display: false },
          ticks: { maxRotation: 45, font: { size: 11 } }
        },
        y: {
          beginAtZero: true,
          grid: { color: 'rgba(0, 0, 0, 0.05)' },
          ticks: {
            font: { size: 11 },
            callback: (value) => formatBytes(value)
          }
        }
      }
    }
  }
}

function renderChart() {
  if (!canvasRef.value) return
  if (typeof navigator !== 'undefined' && /jsdom/i.test(navigator.userAgent || '')) return
  if (chartInstance) {
    chartInstance.destroy()
    chartInstance = null
  }
  let ctx = null
  try {
    ctx = typeof canvasRef.value.getContext === 'function' ? canvasRef.value.getContext('2d') : null
  } catch {
    ctx = null
  }
  if (!ctx) return
  chartInstance = new Chart(ctx, buildConfig())
}

onMounted(renderChart)

watch(() => [props.points, props.prevPoints, props.hostPoints, props.granularity, props.quotaBytes, props.budgetBytes], renderChart, { deep: true })

onUnmounted(() => {
  if (chartInstance) {
    chartInstance.destroy()
    chartInstance = null
  }
})
</script>

<style scoped>
.traffic-trend-chart {
  position: relative;
  width: 100%;
  height: 280px;
}
</style>
