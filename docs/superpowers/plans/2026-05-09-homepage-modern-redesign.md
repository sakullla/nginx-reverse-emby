# 首页现代化重设计实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重构首页视觉和布局，使其更现代、有设计感、更简洁清爽

**Architecture:** 合并 4 张统计卡为 2 张核心卡片并升级视觉质感，调整全局间距为 32px 留白系统，添加错开入场动画，优化表格 hover 交互。所有改动为纯前端样式/布局层，不涉及数据逻辑或 API。

**Tech Stack:** Vue 3, scoped CSS, CSS 变量系统（项目已有 themes.css / animations.css）

---

## 文件结构

| 文件 | 职责 | 变更类型 |
|------|------|----------|
| `panel/frontend/src/components/base/StatCard.vue` | 统计卡片组件：新增 size 属性、升级 hover 效果、调整字号和阴影 | 修改 |
| `panel/frontend/src/pages/DashboardPage.vue` | 首页：合并统计卡为 2 张、调整间距、添加入场动画 | 修改 |
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | 流量模块：统一间距 | 微调 |
| `panel/frontend/src/components/AgentTable.vue` | 节点表格：优化 hover 过渡 | 微调 |

---

## Task 1: 升级 StatCard 组件

**Files:**
- Modify: `panel/frontend/src/components/base/StatCard.vue`

- [ ] **Step 1: 添加 `size` prop 并调整模板结构**

在 `<script setup>` 的 defineProps 中添加 `size` 属性，并在模板中绑定对应的 class。

```javascript
defineProps({
  tone: {
    type: String,
    default: 'primary',
    validator: (v) => ['primary', 'success', 'warning', 'danger'].includes(v),
  },
  value: { type: [String, Number], required: true },
  label: { type: String, required: true },
  subLabel: { type: String, default: '' },
  progress: { type: Number, default: null },
  to: { type: [String, Object], default: null },
  size: {
    type: String,
    default: 'md',
    validator: (v) => ['md', 'lg'].includes(v),
  },
})
```

在 `<component>` 的 class 绑定中添加 `stat-card--${size}`：

```vue
:class="[`stat-card--${tone}`, `stat-card--${size}`, { 'stat-card--linked': !!to }]"
```

- [ ] **Step 2: 重写 `<style>` 段，升级视觉系统**

将 `StatCard.vue` 的 `<style scoped>` 整段替换为：

```css
.stat-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-5);
  box-shadow: var(--shadow-sm);
  transition:
    box-shadow var(--duration-normal) var(--ease-default),
    border-color var(--duration-normal) var(--ease-default),
    transform var(--duration-normal) var(--ease-default);
  position: relative;
  overflow: hidden;
}

/* ---- Size variants ---- */
.stat-card--lg {
  padding: var(--space-6);
}
.stat-card--lg .stat-card__icon {
  width: 48px;
  height: 48px;
  margin-bottom: var(--space-4);
}
.stat-card--lg .stat-card__value {
  font-size: var(--text-3xl);
  font-weight: 600;
}

/* ---- Hover: subtle lift + bottom accent bar ---- */
.stat-card:hover {
  box-shadow: var(--shadow-md);
  border-color: var(--color-border-strong);
}
.stat-card--linked:hover {
  transform: translateY(-1px);
}

/* Value scale on hover */
.stat-card:hover .stat-card__value {
  transform: scale(1.03);
}
.stat-card__value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
  line-height: 1.2;
  transition: transform var(--duration-normal) var(--ease-default);
  display: inline-block;
  font-variant-numeric: tabular-nums;
}

/* ---- Icon ---- */
.stat-card__icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  margin-bottom: var(--space-3);
  transition: transform var(--duration-normal) var(--ease-default);
}
.stat-card:hover .stat-card__icon {
  transform: scale(1.05);
}

.stat-card--primary .stat-card__icon {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}
.stat-card--success .stat-card__icon {
  background: var(--color-success-50);
  color: var(--color-success);
}
.stat-card--warning .stat-card__icon {
  background: var(--color-warning-50);
  color: var(--color-warning);
}
.stat-card--danger .stat-card__icon {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

/* ---- Labels ---- */
.stat-card__sub-label {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  margin: 0 0 0.125rem;
  font-weight: 500;
  opacity: 0.85;
}
.stat-card__label {
  font-size: 0.8125rem;
  color: var(--color-text-tertiary);
  margin: 0;
  font-weight: 500;
}

/* ---- Progress bar ---- */
.stat-card__progress {
  margin-top: var(--space-3);
}
.stat-card__progress-track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.stat-card__progress-fill {
  height: 100%;
  border-radius: var(--radius-full);
  background: var(--color-primary);
  transition: width 0.5s var(--ease-default);
}
.stat-card--success .stat-card__progress-fill { background: var(--color-success); }
.stat-card--warning .stat-card__progress-fill { background: var(--color-warning); }
.stat-card--danger .stat-card__progress-fill { background: var(--color-danger); }

/* ---- Bottom accent bar (replaces top bar) ---- */
.stat-card::after {
  content: '';
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  height: 0;
  background: var(--color-primary);
  transition: height var(--duration-normal) var(--ease-default);
}
.stat-card--success::after { background: var(--color-success); }
.stat-card--warning::after { background: var(--color-warning); }
.stat-card--danger::after { background: var(--color-danger); }

.stat-card:hover::after {
  height: 3px;
}

/* ---- Linked arrow indicator ---- */
.stat-card--linked {
  text-decoration: none;
}
.stat-card--linked::before {
  content: '';
  position: absolute;
  right: var(--space-4);
  top: 50%;
  transform: translateY(-50%);
  width: 6px;
  height: 6px;
  border-top: 1.5px solid var(--color-text-tertiary);
  border-right: 1.5px solid var(--color-text-tertiary);
  rotate: 45deg;
  opacity: 0;
  transition: opacity var(--duration-normal) var(--ease-default), transform var(--duration-normal) var(--ease-default);
}
.stat-card--linked:hover::before {
  opacity: 1;
  transform: translateY(-50%) translateX(3px);
}

@media (max-width: 640px) {
  .stat-card--lg {
    padding: var(--space-5);
  }
  .stat-card--lg .stat-card__value {
    font-size: var(--text-2xl);
  }
  .stat-card--lg .stat-card__icon {
    width: 40px;
    height: 40px;
  }
}
```

注意：移除了旧的 `::before` 顶部色条和 `.stat-card--linked` 的 padding-right 及 `::after` 箭头，统一改为 `::after` 底部色条和 `::before` 箭头。

- [ ] **Step 3: 验证修改无语法错误**

检查：
- `size` prop 默认值是 `'md'`，现有使用方未传 `size` 时保持向后兼容
- 没有删除 `tone`、`value`、`label`、`subLabel`、`progress`、`to` 等原有 prop
- 模板中的 slot `#icon` 仍然保留

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/base/StatCard.vue
git commit -m "feat(panel): upgrade StatCard with size variants and refined hover effects"
```

---

## Task 2: 重构 DashboardPage 布局

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: 重写 `<template>`，合并统计卡并调整结构**

将 `DashboardPage.vue` 的 `<template>` 整段替换为：

```vue
<template>
  <div class="dashboard">
    <div class="dashboard__header animate-fade-in-up">
      <h1 class="dashboard__title">集群概览</h1>
      <p class="dashboard__subtitle">实时监控所有节点状态</p>
    </div>

    <div class="stats-grid">
      <!-- 节点健康度 -->
      <StatCard
        size="lg"
        :tone="healthTone"
        :value="`${onlineCount} / ${agents?.length || 0}`"
        :sub-label="healthSubLabel"
        :progress="onlinePercent"
        label="节点健康度"
        to="/agents"
        class="card-enter stagger-1"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
        </template>
      </StatCard>

      <!-- 规则概览 -->
      <StatCard
        size="lg"
        tone="primary"
        :value="totalRules"
        :sub-label="rulesSubLabel"
        label="规则概览"
        to="/rules"
        class="card-enter stagger-2"
      >
        <template #icon>
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
            <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
          </svg>
        </template>
      </StatCard>
    </div>

    <DashboardTrafficModule class="card-enter stagger-3" />

    <div v-if="agents?.length" class="dashboard-section card-enter stagger-4">
      <div class="dashboard-section__header">
        <h2 class="dashboard-section__title">节点状态</h2>
        <RouterLink to="/agents" class="dashboard-section__link">查看全部 →</RouterLink>
      </div>
      <AgentTable
        :agents="displayedAgents"
        :show-actions="false"
        :clickable="true"
        @click="navigateToAgent"
      />
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="dashboard__loading card-enter">
      <div class="spinner"></div>
      <span>加载中...</span>
    </div>

    <!-- Empty state -->
    <div v-else-if="!agents?.length" class="dashboard__empty card-enter">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <ellipse cx="12" cy="5" rx="9" ry="3"/>
        <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
        <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
      </svg>
      <p>暂无节点</p>
      <p class="dashboard__empty-hint">点击顶部导航栏「加入节点」来添加第一个 Agent</p>
    </div>
  </div>
</template>
```

- [ ] **Step 2: 更新 `<script setup>`，添加合并后的计算属性**

将 `script setup` 整段替换为：

```javascript
<script setup>
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import AgentTable from '../components/AgentTable.vue'
import StatCard from '../components/base/StatCard.vue'
import DashboardTrafficModule from '../components/traffic/DashboardTrafficModule.vue'

const router = useRouter()
const { data: agents, isLoading } = useAgents()

const onlineCount = computed(() => agents.value?.filter(a => a.status === 'online').length || 0)
const offlineCount = computed(() => (agents.value?.length || 0) - onlineCount.value)
const onlinePercent = computed(() => {
  const total = agents.value?.length || 0
  return total > 0 ? Math.round((onlineCount.value / total) * 100) : 0
})

// 节点健康度卡片数据
const healthTone = computed(() => {
  if (offlineCount.value > 0) return 'warning'
  return 'success'
})
const healthSubLabel = computed(() => {
  const total = agents.value?.length || 0
  if (total === 0) return ''
  if (offlineCount.value > 0) return `${offlineCount.value} 个节点离线`
  return '全部在线'
})

// 规则概览卡片数据
const rulesCount = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.http_rules_count || 0), 0) || 0
})
const l4Count = computed(() => {
  return agents.value?.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0) || 0
})
const totalRules = computed(() => rulesCount.value + l4Count.value)
const rulesSubLabel = computed(() => {
  if (totalRules.value === 0) return ''
  return `HTTP ${rulesCount.value} / L4 ${l4Count.value}`
})

const displayedAgents = computed(() => (agents.value || []).slice(0, 8))

function navigateToAgent(agent) {
  router.push(`/agents/${agent.id}`)
}
</script>
```

- [ ] **Step 3: 重写 `<style>`，调整间距和动画**

将 `<style scoped>` 整段替换为：

```css
<style scoped>
.dashboard {
  max-width: 1200px;
  margin: 0 auto;
}

.dashboard__header {
  margin-bottom: var(--space-8);
}

.dashboard__title {
  font-size: var(--text-2xl);
  font-weight: 700;
  color: var(--color-text-primary);
  margin: 0 0 var(--space-1);
  letter-spacing: -0.02em;
}

.dashboard__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-4);
  margin-bottom: var(--space-8);
}

.dashboard__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-12);
  color: var(--color-text-secondary);
}

.dashboard__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-16) var(--space-6);
  color: var(--color-text-muted);
  text-align: center;
}

.dashboard__empty p {
  margin: 0;
  font-size: var(--text-base);
}

.dashboard__empty-hint {
  font-size: var(--text-sm) !important;
  color: var(--color-text-tertiary) !important;
}

.dashboard-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: var(--space-8);
  box-shadow: var(--shadow-sm);
}

.dashboard-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
}

.dashboard-section__title {
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}

.dashboard-section__link {
  font-size: 0.8rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
  transition: color var(--duration-fast) var(--ease-default);
}

.dashboard-section__link:hover {
  color: var(--color-primary-hover);
  text-decoration: underline;
}

@media (max-width: 640px) {
  .stats-grid {
    grid-template-columns: 1fr;
  }
  .dashboard__title {
    font-size: var(--text-xl);
  }
}
</style>
```

注意：移除了 `.dashboard` 上的 `animation: fadeIn`，因为各个子元素已使用 `card-enter` + `stagger` 类进行错开动画。`animations.css` 中已有 `.card-enter` 和 `.stagger-1` 到 `.stagger-6` 的定义。

- [ ] **Step 4: 验证修改**

检查点：
- 4 张独立统计卡已合并为 2 张 `size="lg"` 的卡片
- `healthTone` 在离线节点 > 0 时为 `'warning'`，否则 `'success'`
- `healthSubLabel` 在有离线时显示具体数量，否则显示"全部在线"
- `totalRules` 是 HTTP + L4 的总和
- `rulesSubLabel` 格式为 `HTTP x / L4 x`
- 间距全部使用 CSS 变量（`--space-*`）
- 每个区块都有 `card-enter stagger-N` 入场动画

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/pages/DashboardPage.vue
git commit -m "feat(panel): redesign dashboard layout with merged stat cards and staggered animations"
```

---

## Task 3: 统一 DashboardTrafficModule 间距

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: 调整 margin-bottom**

找到 `.dashboard-traffic` 的 `margin-bottom: 2.5rem;` 并改为使用 CSS 变量：

```css
.dashboard-traffic {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: var(--space-8);
}
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "fix(panel): unify traffic module spacing with design system tokens"
```

---

## Task 4: 优化 AgentTable hover 过渡

**Files:**
- Modify: `panel/frontend/src/components/AgentTable.vue`

- [ ] **Step 1: 为表格行 hover 添加平滑过渡**

找到 `.agent-table__row--clickable:hover` 和 `.agent-table td` 的样式，添加过渡：

```css
.agent-table td {
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--color-border-subtle);
  font-size: 0.875rem;
  vertical-align: middle;
  transition: background-color var(--duration-fast) var(--ease-default);
}

.agent-table__row--clickable:hover {
  background: var(--color-bg-hover);
}
```

注意：`.agent-table td` 上添加 `transition`，hover 效果在 `.agent-table__row--clickable:hover` 上。由于 `td` 的 `background` 会覆盖 `tr` 的 `background`，需要确保 `td` 的 `background` 是透明的或使用 `transition`。

实际上当前代码中 `td` 没有设置背景色，所以 `tr:hover` 的背景会显示。但为了让过渡更平滑，应该给 `tr` 添加 transition：

```css
.agent-table tr {
  transition: background-color var(--duration-fast) var(--ease-default);
}
```

将此段添加到 `.agent-table-wrap` 之后、`.agent-table` 之前的位置。

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/AgentTable.vue
git commit -m "feat(panel): add smooth transition to agent table row hover"
```

---

## Task 5: 构建验证

**Files:**
- 不涉及文件修改，纯验证步骤

- [ ] **Step 1: 运行前端构建**

```bash
cd panel/frontend
npm run build
```

Expected: 构建成功，无 TypeScript/Vite 报错。

- [ ] **Step 2: 启动预览服务检查视觉效果**

```bash
cd panel/frontend
npm run preview
```

在浏览器中打开预览 URL，检查：
- [ ] 首页显示 2 张统计卡（节点健康度 + 规则概览），而非 4 张
- [ ] 统计卡数值更大（36px），padding 更大
- [ ] hover 统计卡时，数值轻微放大，底部有色条滑入
- [ ] 各区块之间有 32px 间距
- [ ] 页面加载时卡片有依次入场动画
- [ ] 节点表格行 hover 有过渡效果
- [ ] 暗色主题（neko-dark / sakura-night）下视觉效果正常
- [ ] 移动端（<640px）下统计卡堆叠为单列

- [ ] **Step 3: 停止预览服务**

按 `Ctrl+C` 停止预览。

- [ ] **Step 4: Commit（如果无问题则无需额外 commit，否则修复后 commit）**

---

## Self-Review Checklist

### Spec Coverage

| 设计规范要求 | 对应任务 |
|-------------|---------|
| 合并 4 张统计卡为 2 张 | Task 2, Step 1-2 |
| 节点健康度：在线/总数 + 进度条 + 状态 tone | Task 2, Step 2 (`healthTone`, `healthSubLabel`) |
| 规则概览：总规则 + HTTP/L4 分项 | Task 2, Step 2 (`totalRules`, `rulesSubLabel`) |
| 核心数字 36px | Task 1, Step 2 (`.stat-card--lg .stat-card__value`) |
| 间距 32px | Task 2, Step 3 (`--space-8`); Task 3 |
| 错开入场动画 | Task 2, Step 1 (`card-enter stagger-N`) |
| 底部色条 hover 效果 | Task 1, Step 2 (`::after`) |
| 表格 hover 过渡 | Task 4 |

### Placeholder Scan
- 无 TBD、TODO、"implement later"
- 每个代码块都是完整的可替换内容
- 所有命令都包含预期输出

### Type Consistency
- `size` prop 在 Task 1 中定义为 `'md' | 'lg'`，在 Task 2 中传 `'lg'` — 一致
- `tone` prop 保持原有的 4 个合法值 — 一致
- 计算属性名在 Task 2 中定义和使用一致
