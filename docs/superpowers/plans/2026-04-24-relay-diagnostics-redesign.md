# Relay 与诊断页面重新设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重构规则诊断 Modal 和 Relay 监听器页面，支持新的 `relay_paths` / `hops` 数据结构，按参考图风格设计

**Architecture:** 诊断 Modal 重构为概览统计 + 路径分组 hops 表格 + 折叠的后端探测结果；Relay 页面保持卡片网格主视图，新增手风琴链式配置拓扑展开

**Tech Stack:** Vue 3 + 现有 CSS 变量系统 + 现有数据 hooks

---

## File Structure

| File | Responsibility |
|------|---------------|
| `RuleDiagnosticModal.vue` | 诊断 Modal 的完整重构：Hero + 统计 + 路径探测 + 后端表格 |
| `RelayListenersPage.vue` | Relay 卡片样式 + 手风琴链式详情展开 |

---

## Task 1: 诊断 Modal 基础结构重构

**Files:**
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`

- [ ] **Step 1.1: 移除不需要的 computed / data**

移除以下内容：
- `latencyBarPct` computed
- `showSamples` / `showBackends` / `expandedAdaptive` refs
- `resetExpandedState` / `toggleAdaptive` / `isAdaptiveExpanded` functions
- `backendActualLatency` function（将使用 `backend.summary.avg_latency_ms` 直接）
- `samples` computed（数据仍从 props 获取但不渲染）
- 所有与 `children` 展开相关的逻辑

添加新的 computed：
- `relayPaths` = `props.task?.result?.relay_paths || []`
- `selectedRelayPath` = `props.task?.result?.selected_relay_path || []`
- `hasRelayPaths` = `relayPaths.value.length > 0`
- `pathStats`： `{ total, success, failed }`

```javascript
const relayPaths = computed(() => props.task?.result?.relay_paths || [])
const selectedRelayPath = computed(() => props.task?.result?.selected_relay_path || [])
const hasRelayPaths = computed(() => relayPaths.value.length > 0)
const pathStats = computed(() => {
  const paths = relayPaths.value
  if (!paths.length) return null
  const total = paths.length
  const success = paths.filter(p => p.success).length
  return { total, success, failed: total - success }
})
```

- [ ] **Step 1.2: 简化 Hero 样式**

移除 `.diagnostic-modal__hero::after` 伪元素和 radial-gradient 装饰。保留基础布局但移除 `backdrop-filter` 和 `box-shadow: inset`。

```css
.diagnostic-modal__hero {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem 1.1rem;
  border-radius: 16px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
}
```

移除 `.diagnostic-modal__hero::after` 规则。

- [ ] **Step 1.3: 替换统计区为路径统计卡片**

替换模板中的 `.diagnostic-modal__stats` 部分：

```html
<div class="diagnostic-modal__stats">
  <div class="diagnostic-stat">
    <span class="diagnostic-stat__label">{{ hasRelayPaths ? '总路径数' : '总测试数' }}</span>
    <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.total : (summary.sent ?? 0) }}</strong>
  </div>
  <div class="diagnostic-stat diagnostic-stat--success">
    <span class="diagnostic-stat__label">{{ hasRelayPaths ? '成功路径' : '成功' }}</span>
    <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.success : (summary.succeeded ?? 0) }}</strong>
  </div>
  <div class="diagnostic-stat diagnostic-stat--danger">
    <span class="diagnostic-stat__label">{{ hasRelayPaths ? '失败路径' : '失败' }}</span>
    <strong class="diagnostic-stat__value">{{ hasRelayPaths ? pathStats.failed : (summary.failed ?? 0) }}</strong>
  </div>
</div>
```

更新 CSS：3 列网格（不是 4 列），添加语义背景 modifier。

```css
.diagnostic-modal__stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
}
.diagnostic-stat {
  padding: 0.875rem 1rem;
  border-radius: 14px;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  text-align: center;
}
.diagnostic-stat--success {
  background: var(--color-success-50);
  border-color: var(--color-success);
}
.diagnostic-stat--success .diagnostic-stat__value {
  color: var(--color-success);
}
.diagnostic-stat--danger {
  background: var(--color-danger-50);
  border-color: var(--color-danger);
}
.diagnostic-stat--danger .diagnostic-stat__value {
  color: var(--color-danger);
}
```

- [ ] **Step 1.4: 提交**

```bash
git add panel/frontend/src/components/RuleDiagnosticModal.vue
git commit -m "feat(diagnostic): restructure modal foundation with path stats"
```

---

## Task 2: 实现 Relay 路径探测结果区域

**Files:**
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`

- [ ] **Step 2.1: 添加 hop 路径显示 helper**

在 script 中添加：

```javascript
function formatHopPath(hop) {
  if (hop.from === 'client') return '入口'
  if (hop.from_listener_id) return `#${hop.from_listener_id}`
  return hop.from || '-'
}

function formatHopTarget(hop) {
  if (!hop.to_listener_id) return `出口(${hop.to || '-'})`
  return `#${hop.to_listener_id}`
}

function isSelectedPath(path) {
  if (!selectedRelayPath.value.length) return false
  if (path.length !== selectedRelayPath.value.length) return false
  return path.every((id, i) => id === selectedRelayPath.value[i])
}

function classifyHopQuality(latencyMs) {
  if (latencyMs == null) return '不可用'
  if (latencyMs <= 50) return '优秀'
  if (latencyMs <= 150) return '良好'
  if (latencyMs <= 300) return '一般'
  return '较差'
}
```

- [ ] **Step 2.2: 添加 Relay 路径 section 模板**

在统计区后面添加：

```html
<div v-if="hasRelayPaths" class="diagnostic-modal__relay-paths">
  <div v-for="(pathReport, pathIndex) in relayPaths" :key="pathIndex" class="relay-path-section">
    <div class="relay-path-section__header">
      <span class="relay-path-section__icon">🔄</span>
      <span class="relay-path-section__title">路径 {{ pathIndex + 1 }} ({{ pathReport.path.map(id => '#' + id).join(' → ') }})</span>
      <span v-if="isSelectedPath(pathReport.path)" class="pill pill--success">已选中</span>
      <span :class="`pill pill--${pathReport.success ? 'success' : 'danger'}`">
        {{ pathReport.success ? '成功' : '失败' }}
      </span>
    </div>

    <div class="relay-path-section__table-wrap">
      <div class="diagnostic-table">
        <div class="diagnostic-table__header">
          <span>路径</span>
          <span style="text-align:center">状态</span>
          <span style="text-align:center">延迟</span>
          <span style="text-align:center">丢包率</span>
          <span style="text-align:center">质量</span>
        </div>
        <div
          v-for="(hop, hopIndex) in pathReport.hops"
          :key="hopIndex"
          class="diagnostic-table__row"
          :class="{ 'diagnostic-table__row--failed': !hop.success }"
        >
          <div class="diagnostic-table__cell">
            <span :class="hop.success ? 'status-icon--success' : 'status-icon--danger'">
              {{ hop.success ? '✓' : '✕' }}
            </span>
            <span>{{ formatHopPath(hop) }} → {{ formatHopTarget(hop) }}</span>
          </div>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span :class="`pill pill--${hop.success ? 'success' : 'danger'}`">
              {{ hop.success ? '成功' : '失败' }}
            </span>
          </span>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span :class="hop.success ? 'value-primary' : 'value-danger'">
              {{ hop.success ? (hop.latency_ms + ' ms') : '—' }}
            </span>
          </span>
          <span class="diagnostic-table__cell" style="text-align:center">—</span>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span :class="`pill pill--${qualityToneFor(classifyHopQuality(hop.latency_ms))}`">
              {{ hop.success ? classifyHopQuality(hop.latency_ms) : '不可用' }}
            </span>
          </span>
        </div>
      </div>
    </div>

    <div v-if="pathReport.error" class="relay-path-section__error">
      错误: {{ pathReport.error }}
    </div>
  </div>
</div>
```

- [ ] **Step 2.3: 添加通用表格和 pill CSS**

在 style 区域添加：

```css
/* 通用 pill */
.pill {
  display: inline-flex;
  align-items: center;
  padding: 2px 10px;
  border-radius: var(--radius-full);
  font-size: 0.65rem;
  font-weight: 700;
}
.pill--success { background: var(--color-success-50); color: var(--color-success); border: 1px solid var(--color-success); }
.pill--danger { background: var(--color-danger-50); color: var(--color-danger); border: 1px solid var(--color-danger); }
.pill--warning { background: var(--color-warning-50); color: var(--color-warning); border: 1px solid var(--color-warning); }
.pill--info { background: var(--color-primary-subtle); color: var(--color-primary); border: 1px solid var(--color-primary); }

/* 通用表格 */
.diagnostic-table {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 12px;
  overflow: hidden;
}
.diagnostic-table__header {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px 80px;
  gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
  font-weight: 600;
  border-bottom: 1px solid var(--color-border-default);
}
.diagnostic-table__row {
  display: grid;
  grid-template-columns: 1fr 80px 80px 80px 80px;
  gap: 0.5rem;
  padding: 0.55rem 0.75rem;
  align-items: center;
  border-bottom: 1px solid var(--color-border-subtle);
}
.diagnostic-table__row:last-child { border-bottom: none; }
.diagnostic-table__row--failed { background: var(--color-danger-50); }
.diagnostic-table__cell {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.82rem;
  min-width: 0;
}
.status-icon--success { color: var(--color-success); }
.status-icon--danger { color: var(--color-danger); }
.value-primary { color: var(--color-primary); font-weight: 600; }
.value-danger { color: var(--color-danger); font-weight: 600; }

/* 路径 section */
.relay-path-section { margin-bottom: 1rem; }
.relay-path-section__header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
  font-weight: 600;
  font-size: 0.85rem;
}
.relay-path-section__icon { font-size: 1rem; }
.relay-path-section__title { color: var(--color-text-primary); }
.relay-path-section__error {
  margin-top: 0.4rem;
  font-size: 0.78rem;
  color: var(--color-danger);
  padding-left: 0.5rem;
}
```

- [ ] **Step 2.4: 移除不需要的旧组件**

移除模板中的：
- `.diagnostic-modal__range` (latency bar)
- `.diagnostic-modal__backends` 及其子元素
- `.diagnostic-modal__samples` 及其子元素
- 所有相关的旧 CSS（后端卡片、子后端、探测样本等）

- [ ] **Step 2.5: 提交**

```bash
git add panel/frontend/src/components/RuleDiagnosticModal.vue
git commit -m "feat(diagnostic): add relay path hop tables"
```

---

## Task 3: 后端探测结果表格（折叠式）

**Files:**
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`

- [ ] **Step 3.1: 添加后端表格折叠区域**

在路径区域后面添加：

```html
<div class="diagnostic-modal__backend-section">
  <button
    type="button"
    class="diagnostic-modal__section-toggle"
    @click="showBackends = !showBackends"
  >
    <span>{{ showBackends ? '▾' : '▸' }}</span>
    <span>后端探测结果</span>
    <span v-if="backendSummaries.length" class="count-badge">{{ backendSummaries.length }} 个</span>
  </button>

  <Transition name="slide-expand">
    <div v-if="showBackends" class="diagnostic-table-wrap">
      <div class="diagnostic-table">
        <div class="diagnostic-table__header" :style="backendTableGridStyle">
          <span>路径</span>
          <span style="text-align:center">状态</span>
          <span style="text-align:center">延迟</span>
          <span style="text-align:center">丢包率</span>
          <span v-if="isHTTP" style="text-align:center">持续吞吐</span>
          <span style="text-align:center">质量</span>
        </div>
        <div
          v-for="backend in backendSummaries"
          :key="backend.backend"
          class="diagnostic-table__row"
          :class="{ 'diagnostic-table__row--failed': backend.summary?.succeeded !== backend.summary?.sent }"
          :style="backendTableGridStyle"
        >
          <div class="diagnostic-table__cell">
            <span :class="(backend.summary?.succeeded === backend.summary?.sent) ? 'status-icon--success' : 'status-icon--danger'">
              {{ (backend.summary?.succeeded === backend.summary?.sent) ? '✓' : '✕' }}
            </span>
            <span class="truncate">{{ ruleLabel }} → {{ backendDisplayLabel(backend) }}{{ backendDisplayAddress(backend) ? ' [' + backendDisplayAddress(backend) + ']' : '' }}</span>
          </div>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span :class="`pill pill--${(backend.summary?.succeeded === backend.summary?.sent) ? 'success' : 'danger'}`">
              {{ (backend.summary?.succeeded === backend.summary?.sent) ? '成功' : '失败' }}
            </span>
          </span>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span class="value-primary">{{ backend.summary?.avg_latency_ms ?? 0 }} ms</span>
          </span>
          <span class="diagnostic-table__cell" style="text-align:center">{{ formatPercent(backend.summary?.loss_rate) }}</span>
          <span v-if="isHTTP" class="diagnostic-table__cell" style="text-align:center">
            <span class="value-primary">{{ formatThroughput(backend.adaptive?.sustained_throughput_bps) }}</span>
          </span>
          <span class="diagnostic-table__cell" style="text-align:center">
            <span :class="`pill pill--${qualityToneFor(backend.summary?.quality)}`">
              {{ qualityLabelFor(backend.summary?.quality) }}
            </span>
          </span>
        </div>
      </div>
    </div>
  </Transition>
</div>
```

添加 computed：

```javascript
const showBackends = ref(false)
const backendTableGridStyle = computed(() => ({
  gridTemplateColumns: isHTTP.value
    ? '1fr 70px 70px 70px 90px 70px'
    : '1fr 70px 70px 70px 70px'
}))
```

添加 CSS：

```css
.diagnostic-modal__section-toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: transparent;
  border: none;
  padding: 0.5rem 0;
  color: var(--color-primary);
  font: inherit;
  font-weight: 600;
  font-size: 0.85rem;
  cursor: pointer;
  width: 100%;
  text-align: left;
}
.diagnostic-modal__section-toggle:hover {
  background: var(--color-bg-hover);
  border-radius: 8px;
}
.count-badge {
  font-size: 0.68rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  background: var(--color-bg-hover);
  padding: 2px 7px;
  border-radius: var(--radius-full);
}
.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

- [ ] **Step 3.2: 移除旧的后端卡片相关逻辑**

确保模板中不再引用任何与 `adaptive` 展开、`children` 、`backend-item` 相关的元素。

- [ ] **Step 3.3: 提交**

```bash
git add panel/frontend/src/components/RuleDiagnosticModal.vue
git commit -m "feat(diagnostic): add collapsible backend table"
```

---

## Task 4: Relay 页面卡片改进

**Files:**
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

- [ ] **Step 4.1: 改进状态标签为 pill 风格**

修改 `.relay-card__status` 相关 CSS：

```css
.relay-card__status {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: var(--radius-full);
}
.relay-card__status--active {
  background: var(--color-success-50);
  color: var(--color-success);
  border: 1px solid var(--color-success);
}
.relay-card__status--disabled {
  background: var(--color-bg-hover);
  color: var(--color-text-muted);
  border: 1px solid var(--color-border-subtle);
}
```

- [ ] **Step 4.2: 添加展开按钮和状态管理**

在 script setup 中添加：

```javascript
const expandedCards = ref(new Set())

function toggleCardExpand(listenerId) {
  const s = new Set(expandedCards.value)
  if (s.has(listenerId)) s.delete(listenerId)
  else s.add(listenerId)
  expandedCards.value = s
}

function isCardExpanded(listenerId) {
  return expandedCards.value.has(listenerId)
}
```

在模板的 `.relay-card` 内最后添加：

```html
<div
  class="relay-card__expand"
  :class="{ 'relay-card__expand--open': isCardExpanded(listener.id) }"
  @click="toggleCardExpand(listener.id)"
>
  <span>{{ isCardExpanded(listener.id) ? '▲' : '▼' }}</span>
  <span>{{ isCardExpanded(listener.id) ? '收起链路拓扑' : '查看链路拓扑' }}</span>
</div>
```

添加 CSS：

```css
.relay-card__expand {
  margin-top: 0.5rem;
  padding-top: 0.5rem;
  border-top: 1px solid var(--color-border-default);
  display: flex;
  align-items: center;
  gap: 0.4rem;
  color: var(--color-primary);
  font-size: 0.75rem;
  cursor: pointer;
  transition: color 0.15s;
}
.relay-card__expand:hover {
  color: var(--color-primary-hover);
}
```

- [ ] **Step 4.3: 提交**

```bash
git add panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(relay): improve card status badges and add expand toggle"
```

---

## Task 5: Relay 链式详情展开

**Files:**
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

- [ ] **Step 5.1: 添加链式详情模板**

在 `.relay-card__expand` 后面添加：

```html
<Transition name="slide-expand">
  <div v-if="isCardExpanded(listener.id)" class="relay-chain">
    <div class="relay-chain__node">
      <div class="relay-chain__dot" style="background:#38bdf8">
        <span>1</span>
      </div>
      <div class="relay-chain__content">
        <div class="relay-chain__label">绑定地址</div>
        <div class="relay-chain__values">
          <code v-for="host in resolveBindHosts(listener)" :key="host" class="relay-chain__value">
            {{ host }}:{{ normalizePort(listener.listen_port) }}
          </code>
        </div>
      </div>
    </div>

    <div class="relay-chain__arrow">↓</div>

    <div class="relay-chain__node">
      <div class="relay-chain__dot" style="background:#a78bfa">
        <span>2</span>
      </div>
      <div class="relay-chain__content">
        <div class="relay-chain__label">公网端点</div>
        <code class="relay-chain__value">{{ formatPublicEndpoint(listener) }}</code>
      </div>
    </div>

    <div class="relay-chain__arrow">↓</div>

    <div class="relay-chain__node">
      <div class="relay-chain__dot" style="background:#fbbf24">
        <span>3</span>
      </div>
      <div class="relay-chain__content">
        <div class="relay-chain__label">传输配置</div>
        <div class="relay-chain__tags">
          <span class="relay-chain__tag">{{ transportSummary(listener) }}</span>
          <span class="relay-chain__tag">{{ obfsSummary(listener) }}</span>
          <span v-if="listener.transport_mode === 'quic'" class="relay-chain__tag">{{ fallbackSummary(listener) }}</span>
        </div>
      </div>
    </div>

    <div class="relay-chain__arrow">↓</div>

    <div class="relay-chain__node">
      <div class="relay-chain__dot" style="background:#34d399">
        <span>4</span>
      </div>
      <div class="relay-chain__content">
        <div class="relay-chain__label">TLS 信任模式</div>
        <span class="relay-chain__tag">{{ trustSummary(listener) }}</span>
      </div>
    </div>

    <div class="relay-chain__arrow">↓</div>

    <div class="relay-chain__node">
      <div class="relay-chain__dot" style="background:#f87171">
        <span>5</span>
      </div>
      <div class="relay-chain__content">
        <div class="relay-chain__label">证书</div>
        <span class="relay-chain__tag">{{ listener.certificate_id ? '证书 #' + listener.certificate_id : '未绑定证书' }}</span>
      </div>
    </div>
  </div>
</Transition>
```

- [ ] **Step 5.2: 添加链式 CSS**

圦 style 中添加：

```css
.relay-chain {
  margin-top: 0.75rem;
  padding: 0.75rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: 12px;
}
.relay-chain__node {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
}
.relay-chain__dot {
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #0f172a;
  font-size: 0.65rem;
  font-weight: 700;
  flex-shrink: 0;
  margin-top: 0.1rem;
}
.relay-chain__arrow {
  padding-left: 7px;
  color: var(--color-text-muted);
  font-size: 0.8rem;
  line-height: 1.2;
}
.relay-chain__content {
  flex: 1;
}
.relay-chain__label {
  font-size: 0.72rem;
  color: var(--color-text-tertiary);
  margin-bottom: 0.2rem;
}
.relay-chain__values {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}
.relay-chain__value {
  font-family: var(--font-mono);
  font-size: 0.8rem;
  color: var(--color-text-primary);
  background: var(--color-bg-canvas);
  padding: 0.25rem 0.5rem;
  border-radius: 6px;
  display: inline-block;
}
.relay-chain__tags {
  display: flex;
  gap: 0.25rem;
  flex-wrap: wrap;
}
.relay-chain__tag {
  font-size: 0.7rem;
  padding: 2px 8px;
  background: var(--color-bg-canvas);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
  border-radius: 6px;
}
```

- [ ] **Step 5.3: 提交**

```bash
git add panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(relay): add chain topology accordion detail"
```

---

## Task 6: 验证与清理

**Files:**
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

- [ ] **Step 6.1: 运行前端 dev server 验证**

```bash
cd panel/frontend && npm run build
```

确保：
- 构建无错误
- 没有未使用的变量/函数警告

- [ ] **Step 6.2: 浏览器验证**

启动前端开发服务器：

```bash
cd panel/frontend && npm run dev
```

验证金路径：
1. 进入 HTTP 规则页面，点击诊断按钮
2. 确认 Modal 展示正确：Hero + 统计 + 路径 hops 表格 + 折叠后端
3. 确认路径编号、选中状态、hop 延迟显示正确
4. 进入 L4 规则页面，点击诊断，确认无持续吞吐列
5. 进入 Relay 监听器页面，点击卡片展开
6. 确认 5 个链路节点正确展示
7. 确认多个卡片可同时展开

- [ ] **Step 6.3: 清理旧 CSS**

检查 RuleDiagnosticModal.vue 的 style 区域，删除所有不再使用的 CSS 规则（latency bar、后端卡片、探测样本等）。

- [ ] **Step 6.4: 最终提交**

```bash
git add panel/frontend/src/components/RuleDiagnosticModal.vue panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "style(diagnostic,relay): clean up unused styles after redesign"
```

---

## Self-Review

**Spec coverage:**
- [x] 诊断 Modal Hero 区简化 → Task 1.2
- [x] 概览统计区（3 个卡片） → Task 1.3
- [x] Relay 路径探测结果（按路径分组 hops） → Task 2
- [x] Hop 路径显示规则（入口/出口/#id） → Task 2.1
- [x] 后端探测结果表格（折叠，HTTP 6列/L4 5列） → Task 3
- [x] 持续吞吐显示 → Task 3
- [x] 路径显示规则（域名+IP） → Task 3
- [x] 移除探测样本 → Task 2.4
- [x] 移除 latency bar → Task 2.4
- [x] 移除后端卡片展开 → Task 2.4
- [x] Relay 卡片改进 → Task 4
- [x] 手风琴链式详情（5节点） → Task 5
- [x] 多地址支持（bind_hosts 列表） → Task 5
- [x] 多卡片同时展开 → Task 4.1
- [x] 暗色主题适配（CSS 变量） → 全部任务使用现有变量

**Placeholder scan:** 无 TBD/TODO

**Type consistency:** `relayPaths` / `selectedRelayPath` / `hasRelayPaths` / `pathStats` 在 Task 1.1 定义，Task 2 使用。`showBackends` 在 Task 3 定义使用。`expandedCards` 在 Task 4.1 定义。一致。
