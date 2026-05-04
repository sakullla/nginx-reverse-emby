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
  granularity: { type: String, default: 'day' },
  quotaBytes: { type: Number, default: null }
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

function buildConfig() {
  const labels = props.points.map(p => formatLabel(p.bucket_start))
  const rxData = props.points.map(p => Number(p.rx_bytes) || 0)
  const txData = props.points.map(p => Number(p.tx_bytes) || 0)
  const datasets = [
    {
      label: 'RX',
      data: rxData,
      borderColor: 'rgba(99, 102, 241, 0.9)',
      backgroundColor: 'rgba(99, 102, 241, 0.1)',
      fill: true,
      tension: 0.3,
      pointRadius: 2,
      pointHoverRadius: 4
    },
    {
      label: 'TX',
      data: txData,
      borderColor: 'rgba(16, 185, 129, 0.9)',
      backgroundColor: 'rgba(16, 185, 129, 0.1)',
      fill: true,
      tension: 0.3,
      pointRadius: 2,
      pointHoverRadius: 4
    }
  ]
  if (props.quotaBytes != null && props.quotaBytes > 0) {
    datasets.push({
      label: '月额度',
      data: labels.map(() => props.quotaBytes),
      borderColor: 'rgba(239, 68, 68, 0.5)',
      borderDash: [6, 4],
      borderWidth: 1,
      pointRadius: 0,
      fill: false
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
  if (chartInstance) {
    chartInstance.destroy()
    chartInstance = null
  }
  const ctx = canvasRef.value.getContext('2d')
  if (!ctx) return
  chartInstance = new Chart(ctx, buildConfig())
}

onMounted(renderChart)

watch(() => [props.points, props.granularity, props.quotaBytes], renderChart, { deep: true })

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
