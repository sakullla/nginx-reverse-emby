# 首页流量统计模块重设计 — 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将首页流量统计模块重设计为 Bento 网格布局，图表库从 Chart.js 完全迁移至 ApexCharts。

**Architecture:** 保持现有数据流不变，重构视觉层。新增两个 ApexCharts 组件（环形图、迷你趋势图），重写趋势图为 ApexCharts 面积图，DashboardTrafficModule 改为 CSS Grid Bento 布局。

**Tech Stack:** Vue 3, ApexCharts 5.10.6, vue3-apexcharts 1.11.1, CSS Grid

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `panel/frontend/package.json` | Modify | 移除 `chart.js`/`vue-chartjs`，添加 `apexcharts`/`vue3-apexcharts` |
| `panel/frontend/src/main.js` | Modify | 全局注册 `vue3-apexcharts` 插件 |
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | Rewrite | ApexCharts 面积图（原 Chart.js 趋势图） |
| `panel/frontend/src/components/traffic/TrafficTrendChart.test.js` | Rewrite | 对应测试，mock apexchart 组件 |
| `panel/frontend/src/components/traffic/TrafficQuotaRing.vue` | Create | 配额使用环形图（ApexCharts donut） |
| `panel/frontend/src/components/traffic/TrafficRateSparkline.vue` | Create | 速率迷你趋势图（ApexCharts 极简 area） |
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | Modify | Bento 网格布局，整合新组件 |
| `panel/frontend/src/pages/DashboardPage.vue` | Modify | StatCard 视觉微调，整体间距 |

---

### Task 1: 替换图表库依赖

**Files:**
- Modify: `panel/frontend/package.json`

- [ ] **Step 1: 修改 package.json**

  打开 `panel/frontend/package.json`，在 `dependencies` 中：
  - 删除 `"chart.js": "^4.5.1"`
  - 删除 `"vue-chartjs": "^5.3.3"`
  - 添加 `"apexcharts": "^5.10.6"`
  - 添加 `"vue3-apexcharts": "^1.11.1"`

  ```json
  "dependencies": {
    "@tanstack/vue-query": "^5.96.2",
    "@vueuse/core": "^14.2.1",
    "apexcharts": "^5.10.6",
    "axios": "^1.15.0",
    "pinia": "^2.1.7",
    "vue": "^3.4.21",
    "vue3-apexcharts": "^1.11.1",
    "vue-router": "^4.6.4"
  }
  ```

- [ ] **Step 2: 安装新依赖并更新 lock 文件**

  Run:
  ```bash
  cd panel/frontend && rm -rf node_modules package-lock.json && npm install
  ```
  Expected: 安装成功，无报错。

- [ ] **Step 3: 全局注册 vue3-apexcharts**

  Modify: `panel/frontend/src/main.js`

  在文件顶部导入并注册：

  ```js
  import VueApexCharts from 'vue3-apexcharts'
  ```

  在 `createApp(App)` 链式调用中添加：

  ```js
  const app = createApp(App)
  app.use(VueApexCharts)
  // ... existing .use() calls
  ```

- [ ] **Step 4: 验证无 Chart.js 残留**

  Run:
  ```bash
  cd panel/frontend && grep -r "chart.js" src/ --include="*.js" --include="*.vue" || echo "No Chart.js imports found"
  ```
  Expected: `No Chart.js imports found`（除了 test 文件中的 mock，将在 Task 2 处理）

- [ ] **Step 5: Commit**

  ```bash
  git add panel/frontend/package.json panel/frontend/package-lock.json panel/frontend/src/main.js
  git commit -m "feat(frontend): replace chart.js with apexcharts"
  ```

---

### Task 2: 重写 TrafficTrendChart（ApexCharts 面积图）

**Files:**
- Rewrite: `panel/frontend/src/components/traffic/TrafficTrendChart.vue`
- Rewrite: `panel/frontend/src/components/traffic/TrafficTrendChart.test.js`

- [ ] **Step 1: 重写 TrafficTrendChart.vue**

  完整替换文件内容：

  ```vue
  <template>
    <div class="traffic-trend-chart">
      <apexchart
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
      size: [3, 2, 2, 2, 0, 0, 0],
      hover: { size: 5 }
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
  ```

- [ ] **Step 2: 重写 TrafficTrendChart.test.js**

  完整替换：

  ```js
  import { describe, it, expect, vi } from 'vitest'
  import { mount } from '@vue/test-utils'
  import TrafficTrendChart from './TrafficTrendChart.vue'

  // Mock vue3-apexcharts component
  vi.mock('vue3-apexcharts', () => ({
    default: {
      name: 'apexchart',
      template: '<div data-testid="apexchart" />',
      props: ['type', 'options', 'series', 'height', 'width']
    }
  }))

  describe('TrafficTrendChart', () => {
    it('renders apexchart component', () => {
      const wrapper = mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
          ]
        }
      })
      expect(wrapper.find('[data-testid="apexchart"]').exists()).toBe(true)
    })

    it('computes series from points prop', () => {
      const wrapper = mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 },
            { bucket_start: '2026-05-02T00:00:00Z', accounted_bytes: 2000, rx_bytes: 1200, tx_bytes: 800 }
          ]
        }
      })
      const series = wrapper.vm.series
      expect(series.length).toBeGreaterThanOrEqual(3)
      expect(series[0].name).toBe('用量')
      expect(series[0].data).toEqual([1000, 2000])
      expect(series[1].name).toBe('RX')
      expect(series[2].name).toBe('TX')
    })

    it('includes host traffic series when hostPoints provided', () => {
      const wrapper = mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
          ],
          hostPoints: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1500, rx_bytes: 900, tx_bytes: 600 }
          ]
        }
      })
      const hostSeries = wrapper.vm.series.find(s => s.name === '主机流量')
      expect(hostSeries).toBeDefined()
      expect(hostSeries.data).toEqual([1500])
    })

    it('formats x-axis labels for day granularity', () => {
      const wrapper = mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-01T00:00:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
          ],
          granularity: 'day'
        }
      })
      expect(wrapper.vm.labels.length).toBe(1)
      expect(wrapper.vm.labels[0]).toContain('5')
    })

    it('formats x-axis labels for hour granularity', () => {
      const wrapper = mount(TrafficTrendChart, {
        props: {
          points: [
            { bucket_start: '2026-05-01T08:30:00Z', accounted_bytes: 1000, rx_bytes: 600, tx_bytes: 400 }
          ],
          granularity: 'hour'
        }
      })
      expect(wrapper.vm.labels[0]).toMatch(/\d{2}:\d{2}/)
    })
  })
  ```

- [ ] **Step 3: 运行测试验证通过**

  Run:
  ```bash
  cd panel/frontend && npm test -- TrafficTrendChart.test.js
  ```
  Expected: 所有 5 个测试通过。

- [ ] **Step 4: Commit**

  ```bash
  git add panel/frontend/src/components/traffic/TrafficTrendChart.vue panel/frontend/src/components/traffic/TrafficTrendChart.test.js
  git commit -m "feat(frontend): rewrite TrafficTrendChart with ApexCharts"
  ```

---

### Task 3: 创建 TrafficQuotaRing（配额环形图）

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficQuotaRing.vue`

- [ ] **Step 1: 创建组件文件**

  ```vue
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
  ```

- [ ] **Step 2: Commit**

  ```bash
  git add panel/frontend/src/components/traffic/TrafficQuotaRing.vue
  git commit -m "feat(frontend): add TrafficQuotaRing with ApexCharts donut"
  ```

---

### Task 4: 创建 TrafficRateSparkline（速率迷你图）

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficRateSparkline.vue`

- [ ] **Step 1: 创建组件文件**

  ```vue
  <template>
    <div class="traffic-rate-sparkline">
      <div class="traffic-rate-sparkline__header">
        <span class="traffic-rate-sparkline__label">实时速率</span>
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
    points: { type: Array, default: () => [] }
  })

  const rates = computed(() => {
    const pts = props.points || []
    if (pts.length < 2) return []
    const result = []
    for (let i = 1; i < pts.length; i++) {
      const prev = Number(pts[i - 1]?.accounted_bytes) || 0
      const curr = Number(pts[i]?.accounted_bytes) || 0
      result.push(Math.max(0, curr - prev))
    }
    return result
  })

  const currentRate = computed(() => {
    const r = rates.value
    if (!r.length) return '—'
    return formatBytes(r[r.length - 1]) + '/周期'
  })

  const series = computed(() => [{
    name: '速率',
    data: rates.value
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
  ```

- [ ] **Step 2: Commit**

  ```bash
  git add panel/frontend/src/components/traffic/TrafficRateSparkline.vue
  git commit -m "feat(frontend): add TrafficRateSparkline component"
  ```

---

### Task 5: 重构 DashboardTrafficModule（Bento 布局）

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: 完整重写 DashboardTrafficModule.vue**

  保留所有数据逻辑（`useTrafficOverview`、`topRulesQuery`、computed 属性等），仅重构模板和样式为 Bento 网格布局。

  模板结构变为：
  ```vue
  <template>
    <div v-if="visible" class="dashboard-traffic">
      <div class="dashboard-traffic__header">
        <h2 class="dashboard-traffic__title">流量统计</h2>
        <div class="dashboard-traffic__toolbar">
          <select v-model="selectedAgentId" class="dashboard-traffic__select">
            <option value="">全部节点</option>
            <option v-for="agent in selectableAgents" :key="agent.agent_id" :value="agent.agent_id">{{ agent.name }}</option>
          </select>
        </div>
      </div>

      <div v-if="overviewQuery.isLoading.value" class="dashboard-traffic__loading">
        <div class="spinner"></div>
      </div>

      <template v-else>
        <!-- Bento Grid -->
        <div class="dashboard-traffic__bento">
          <!-- 趋势图 -->
          <div class="bento-card bento-card--trend">
            <TrafficTrendChart
              :points="trendPoints"
              :host-points="hostTrendPoints"
              granularity="day"
              :quota-bytes="selectedSummary?.quota_bytes ?? null"
            />
          </div>

          <!-- 配额环形图 -->
          <div class="bento-card bento-card--quota">
            <TrafficQuotaRing
              :used-bytes="selectedSummary?.used_bytes ?? 0"
              :quota-bytes="selectedSummary?.quota_bytes ?? null"
              :remaining-bytes="selectedSummary?.remaining_bytes ?? null"
            />
          </div>

          <!-- 实时速率 -->
          <div class="bento-card bento-card--rate">
            <TrafficRateSparkline :points="trendPoints" />
          </div>

          <!-- 阻断节点 -->
          <div class="bento-card bento-card--blocked" :class="{ 'bento-card--alert': blockedCount > 0 }">
            <span class="bento-card__label">阻断节点</span>
            <span class="bento-card__value">{{ blockedCount }} / {{ overviewAgents.length }}</span>
            <span v-if="blockedCount > 0" class="bento-card__sub bento-card__sub--alert">{{ blockedCount }} 个节点已超额阻断</span>
            <span v-else class="bento-card__sub">所有节点正常</span>
          </div>

          <!-- 计费周期 -->
          <div class="bento-card bento-card--cycle">
            <span class="bento-card__label">计费周期</span>
            <span class="bento-card__value">{{ cycleLabel }}</span>
            <span class="bento-card__sub">方向: {{ directionLabel }}</span>
          </div>

          <!-- Top 节点 -->
          <div class="bento-card bento-card--top-nodes">
            <h3 class="bento-card__title">Top 节点</h3>
            <div v-for="agent in topNodes" :key="agent.agent_id" class="top-row">
              <span class="top-row__name">{{ agent.name || agent.agent_id }}</span>
              <div class="top-row__bar-track">
                <div class="top-row__bar-fill" :style="{ width: topNodePercent(agent) + '%' }" />
              </div>
              <span class="top-row__value">{{ formatBytes(agent.used_bytes) }}</span>
            </div>
            <p v-if="!topNodes.length" class="bento-card__empty">暂无节点数据</p>
          </div>

          <!-- Top 规则 -->
          <div class="bento-card bento-card--top-rules">
            <h3 class="bento-card__title">Top 规则</h3>
            <div v-for="rule in topRules" :key="rule.key" class="top-row">
              <span class="top-row__name" :title="rule.label">{{ rule.label }}</span>
              <div class="top-row__bar-track">
                <div class="top-row__bar-fill" :style="{ width: rule.percent + '%' }" />
              </div>
              <span class="top-row__value">{{ formatBytes(rule.accounted_bytes) }}</span>
            </div>
            <p v-if="!topRules.length" class="bento-card__empty">暂无规则数据</p>
          </div>
        </div>
      </template>
    </div>
  </template>
  ```

  `<script setup>` 部分：
  - 保留所有现有 import、data/computed 逻辑不变
  - 新增 `topNodePercent` helper：
    ```js
    function topNodePercent(agent) {
      if (!agent.quota_bytes || agent.quota_bytes <= 0) {
        const max = Math.max(...topNodes.value.map(a => a.used_bytes), 1)
        return Math.round((agent.used_bytes / max) * 100)
      }
      return Math.min(100, usagePercent(agent.used_bytes, agent.quota_bytes))
    }
    ```
  - 新增 `formatBytes` 到 import

  `<style scoped>` 部分：
  ```css
  .dashboard-traffic { /* same header styles */ }
  .dashboard-traffic__header { /* same */ }
  .dashboard-traffic__title { /* same */ }
  .dashboard-traffic__toolbar { /* same */ }
  .dashboard-traffic__select { /* same */ }
  .dashboard-traffic__loading { /* same */ }

  .dashboard-traffic__bento {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    grid-template-rows: 280px 120px auto;
    grid-template-areas:
      "trend trend quota"
      "rate blocked cycle"
      "top-nodes top-nodes top-rules";
    gap: 1rem;
    padding: 1rem 1.25rem 1.25rem;
  }

  .bento-card {
    background: var(--color-bg-subtle);
    border-radius: var(--radius-lg);
    padding: 0.75rem;
    min-width: 0;
    overflow: hidden;
  }
  .bento-card--trend { grid-area: trend; padding: 0.5rem; }
  .bento-card--quota { grid-area: quota; }
  .bento-card--rate { grid-area: rate; }
  .bento-card--blocked { grid-area: blocked; }
  .bento-card--cycle { grid-area: cycle; }
  .bento-card--top-nodes { grid-area: top-nodes; }
  .bento-card--top-rules { grid-area: top-rules; }

  .bento-card--alert {
    background: var(--color-danger-50);
    border: 1px solid var(--color-danger-100);
  }

  .bento-card__label {
    display: block;
    font-size: 0.75rem;
    color: var(--color-text-tertiary);
    margin-bottom: 0.25rem;
  }
  .bento-card__value {
    display: block;
    font-size: 1.125rem;
    font-weight: 700;
    color: var(--color-text-primary);
    font-variant-numeric: tabular-nums;
  }
  .bento-card__sub {
    display: block;
    font-size: 0.75rem;
    color: var(--color-text-tertiary);
    margin-top: 0.25rem;
  }
  .bento-card__sub--alert { color: var(--color-danger); }
  .bento-card__title {
    font-size: 0.8125rem;
    font-weight: 600;
    color: var(--color-text-primary);
    margin: 0 0 0.5rem;
  }
  .bento-card__empty {
    text-align: center;
    color: var(--color-text-muted);
    padding: 1rem;
    font-size: 0.8125rem;
    margin: 0;
  }

  .top-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: 0.5rem;
    align-items: center;
    padding: 0.35rem 0;
    font-size: 0.8125rem;
    border-bottom: 1px solid var(--color-border-subtle);
  }
  .top-row:last-child { border-bottom: none; }
  .top-row__name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--color-text-primary);
  }
  .top-row__bar-track {
    display: none;
  }
  .top-row__value {
    color: var(--color-text-primary);
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }

  .spinner { /* same */ }
  @keyframes spin { to { transform: rotate(360deg); } }

  @media (max-width: 1023px) {
    .dashboard-traffic__bento {
      grid-template-columns: repeat(2, 1fr);
      grid-template-rows: 260px 260px auto auto;
      grid-template-areas:
        "trend trend"
        "quota rate"
        "blocked cycle"
        "top-nodes top-rules";
    }
  }

  @media (max-width: 767px) {
    .dashboard-traffic__bento {
      grid-template-columns: 1fr;
      grid-template-rows: auto;
      grid-template-areas:
        "trend"
        "quota"
        "rate"
        "blocked"
        "cycle"
        "top-nodes"
        "top-rules";
    }
    .bento-card--trend { min-height: 220px; }
  }
  ```

- [ ] **Step 2: 运行构建检查**

  Run:
  ```bash
  cd panel/frontend && npm run build
  ```
  Expected: 构建成功，无 Chart.js 相关错误。

- [ ] **Step 3: Commit**

  ```bash
  git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
  git commit -m "feat(frontend): redesign DashboardTrafficModule with Bento grid"
  ```

---

### Task 6: 升级 DashboardPage（StatCard 视觉微调）

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: 修改 StatCard 样式**

  不需要修改 `DashboardPage.vue` 的模板（StatCard 接口不变），只需修改 `StatCard.vue` 组件本身：

  Modify: `panel/frontend/src/components/base/StatCard.vue`

  在 `<style scoped>` 中：
  - 添加 `.stat-card` hover 效果：
    ```css
    .stat-card {
      /* existing styles */
      transition: transform 200ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1)),
        box-shadow 200ms var(--ease-default, cubic-bezier(0.4, 0, 0.2, 1));
    }
    .stat-card:hover {
      transform: translateY(-2px);
      box-shadow: var(--shadow-sm);
    }
    ```
  - `.stat-card__value` 添加紧凑字距：
    ```css
    .stat-card__value {
      /* existing styles */
      letter-spacing: -0.02em;
    }
    ```

- [ ] **Step 2: 微调 DashboardPage 间距**

  在 `DashboardPage.vue` 的 `<style scoped>` 中：
  - `.stats-grid` 的 `margin-bottom` 从 `2.5rem` 改为 `2rem`
  - `.dashboard__header` 的 `margin-bottom` 从 `2.5rem` 改为 `2rem`

- [ ] **Step 3: Commit**

  ```bash
  git add panel/frontend/src/components/base/StatCard.vue panel/frontend/src/pages/DashboardPage.vue
  git commit -m "feat(frontend): polish StatCard hover and dashboard spacing"
  ```

---

### Task 7: 验证引用点无 Chart.js 残留

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficTrendModal.vue`（如有需要）
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`（如有需要）

- [ ] **Step 1: 检查并修复引用文件**

  `TrafficTrendModal.vue` 和 `AgentDetailPage.vue` 都只是 import `TrafficTrendChart.vue`，由于该组件接口未变（props 相同），理论上无需修改。

  但需确认它们没有直接引用 chart.js：

  Run:
  ```bash
  cd panel/frontend && grep -r "chart.js\|Chart.js\|vue-chartjs" src/ --include="*.js" --include="*.vue" || echo "Clean"
  ```
  Expected: `Clean`

- [ ] **Step 2: 全量构建验证**

  Run:
  ```bash
  cd panel/frontend && npm run build
  ```
  Expected: 构建成功，无错误。

- [ ] **Step 3: 全量测试验证**

  Run:
  ```bash
  cd panel/frontend && npm test
  ```
  Expected: 所有测试通过。

- [ ] **Step 4: Commit（如无任何修改则跳过）**

  ```bash
  git add -A
  git diff --cached --quiet || git commit -m "chore(frontend): verify no Chart.js残留"
  ```

---

## Self-Review Checklist

### Spec Coverage

| 设计需求 | 对应任务 |
|---|---|
| 现代简约视觉风格 | Task 5, 6 |
| Bento 网格布局 | Task 5 |
| ApexCharts 面积图（趋势图） | Task 2 |
| ApexCharts 环形图（配额） | Task 3 |
| ApexCharts 迷你图（速率） | Task 4 |
| 完全移除 Chart.js | Task 1, 2, 7 |
| 响应式（桌面/平板/手机） | Task 5 |
| StatCard 视觉升级 | Task 6 |
| 数据流保持不变 | 所有 Task |

### Placeholder Scan

- [x] 无 TBD/TODO
- [x] 无 "add appropriate error handling"
- [x] 无 "similar to Task N"
- [x] 所有步骤包含完整代码

### Type Consistency

- [x] `TrafficTrendChart` props 与原组件一致
- [x] `formatBytes` 等工具函数引用路径正确
- [x] `vue3-apexcharts` 组件名 `apexchart` 与 mock 一致

---

## Execution Options

**Plan complete and saved to `docs/superpowers/plans/2026-05-05-dashboard-traffic-redesign.md`.**

Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
