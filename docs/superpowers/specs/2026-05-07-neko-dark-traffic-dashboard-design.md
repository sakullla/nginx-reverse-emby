# Neko 深蓝暗色主题 + 流量统计看板重构设计文档

**日期**: 2026-05-07  
**作者**: Claude Code  
**参考**: [Neko Master Rules Dark](https://github.com/foru17/neko-master/blob/main/assets/neko-master-rules-dark.png)

---

## 1. 背景与目标

### 1.1 现状问题

当前 Dashboard 流量统计模块 (`DashboardTrafficModule.vue`) 使用 Bento Grid 布局，存在以下问题：

1. **加载性能差**: Top 规则统计通过逐个节点 `fetchTrafficSummary` 串行查询，N 个节点 = N+1 次请求
2. **视觉风格不统一**: 流量统计组件与参考的现代化 SaaS 看板风格差距较大
3. **主题缺失**: 没有深蓝暗色主题，暗色模式只有粉色系的 `sakura-night`

### 1.2 目标

1. 新增 Neko 风格深蓝暗色主题，WCAG AA 合规
2. 重构 Dashboard 流量统计看板布局（参考 Neko Master 设计）
3. 后端新增聚合 API，解决性能问题
4. 统一所有流量统计页面的暗色主题适配

---

## 2. 新增主题: neko-dark

### 2.1 设计原则

基于 SaaS 暗色看板最佳实践：

- **避免纯黑**: 使用 `#0f172a` (slate-900) 作为画布，减少眼疲劳
- **层级分明**: Canvas → Surface → Elevated 三级亮度差异
- **对比度合规**: 所有文本满足 WCAG AA 4.5:1
- **去饱和主色**: 暗色背景下使用 `#60a5fa` (blue-400) 而非饱和蓝色

### 2.2 完整 CSS 变量

```css
[data-theme="neko-dark"] {
  /* Primary: Blue-400 (desaturated for dark bg) */
  --color-primary: #60a5fa;
  --color-primary-hover: #93c5fd;
  --color-primary-active: #3b82f6;
  --color-primary-subtle: rgba(96, 165, 250, 0.12);
  --color-primary-50: rgba(96, 165, 250, 0.06);
  --color-primary-100: rgba(96, 165, 250, 0.14);
  --color-primary-200: rgba(96, 165, 250, 0.22);
  --color-primary-300: rgba(96, 165, 250, 0.32);

  /* Accent: Purple */
  --color-accent: #a78bfa;
  --color-accent-hover: #c4b5fd;
  --color-accent-subtle: rgba(167, 139, 250, 0.10);

  /* Text: Slate scale, WCAG AA compliant */
  --color-text-primary: #f1f5f9;   /* ~18:1 on #0f172a */
  --color-text-secondary: #94a3b8; /* ~7:1 */
  --color-text-tertiary: #8899aa;  /* ~5.2:1 (fixed from #64748b which fails on cards) */
  --color-text-muted: #475569;
  --color-text-inverse: #0f172a;

  /* Background: 3 elevation levels */
  --color-bg-canvas: #0f172a;       /* slate-900 */
  --color-bg-surface: #1e293b;      /* slate-800 */
  --color-bg-surface-raised: #334155; /* slate-700 */
  --color-bg-sunken: #0b1221;
  --color-bg-subtle: rgba(96, 165, 250, 0.05);
  --color-bg-hover: rgba(96, 165, 250, 0.08);
  --color-bg-active: rgba(96, 165, 250, 0.12);

  /* Borders: subtle for dark */
  --color-border-subtle: rgba(255, 255, 255, 0.04);
  --color-border-default: rgba(255, 255, 255, 0.08);
  --color-border-strong: rgba(255, 255, 255, 0.14);
  --color-border-focus: rgba(96, 165, 250, 0.40);

  /* Semantic: brighter for dark backgrounds */
  --color-success: #34d399;
  --color-success-50: rgba(52, 211, 153, 0.10);
  --color-danger: #f87171;
  --color-danger-50: rgba(248, 113, 113, 0.10);
  --color-warning: #fbbf24;
  --color-warning-50: rgba(251, 191, 36, 0.10);

  /* Shadows: stronger for dark */
  --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.25);
  --shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.30), 0 1px 2px rgba(0, 0, 0, 0.20);
  --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.35), 0 2px 4px rgba(0, 0, 0, 0.25);
  --shadow-lg: 0 10px 15px rgba(0, 0, 0, 0.40), 0 4px 6px rgba(0, 0, 0, 0.30);
  --shadow-xl: 0 20px 25px rgba(0, 0, 0, 0.45), 0 8px 10px rgba(0, 0, 0, 0.30);
  --shadow-2xl: 0 24px 48px rgba(0, 0, 0, 0.55);
  --shadow-focus: 0 0 0 3px var(--color-primary-subtle);
  --shadow-inner: inset 0 1px 2px rgba(0, 0, 0, 0.20);
}
```

### 2.3 图表配色（7 色轮盘）

| 顺序 | 颜色 | Hex | 用途 |
|------|------|-----|------|
| 1 | 蓝 | `#60a5fa` | 主色 |
| 2 | 紫 | `#a78bfa` | 第二 |
| 3 | 绿 | `#34d399` | 第三 |
| 4 | 橙 | `#fbbf24` | 第四 |
| 5 | 红 | `#f87171` | 第五 |
| 6 | 青 | `#22d3ee` | 第六 |
| 7 | 粉 | `#f472b6` | 第七 |

---

## 3. Dashboard 流量统计布局重构

### 3.1 新布局（桌面端 ≥1024px）

```
┌─────────────────────────────────────────────────────────────┐
│  流量统计                                [小时·日·月] [节点▼]  │
├──────────────┬──────────────────────────┬───────────────────┤
│              │                          │                   │
│  流量分布    │    流量趋势              │  Top 规则         │
│  (甜甜圈图)  │    (面积图)              │  (水平条形)       │
│              │                          │                   │
│  ┌────────┐  │                          │  ████████ 659 MB  │
│  │  ●●●   │  │    ∿∿∿∿∿∿∿∿∿∿∿          │  ██████░░ 590 MB  │
│  │ ●   ●  │  │   ∿∿∿∿∿∿∿∿∿∿∿∿∿         │  █████░░░ 546 MB  │
│  │  ●●●   │  │  ∿∿∿∿∿∿∿∿∿∿∿∿∿∿∿        │  ████░░░░ 477 MB  │
│  └────────┘  │                          │  ███░░░░░ 368 MB  │
│              │                          │                   │
├──────────────┤                          ├───────────────────┤
│  Top 节点    │                          │  实时速率         │
│  🔵 节点-A   │                          │  12.4 MB/s ↑ +5%  │
│  🟣 节点-B   │                          │  ∿∿∿∿∿∿∿∿∿∿      │
│  🟢 节点-C   │                          │                   │
├──────────────┴──────────────────────────┴───────────────────┤
│  [阻断 1/3]  [周期 2026-05-01]  [已用 4.5/10GB ▓▓▓▓▓░░░░░]  │
│  [剩余 5.5GB]                                               │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 响应式适配

**平板 (768-1023px)**:
- 3列 → 2列：左列(甜甜圈+Top节点) | 中列(趋势图)
- 右列(Top规则+速率) 移到下方
- 底部小统计卡片 2×2 网格

**手机 (<768px)**:
- 单列堆叠：趋势图 → 甜甜圈图 → Top规则 → Top节点 → 速率 → 小统计卡片

### 3.3 涉及改动的现有组件

| 组件 | 改动 |
|------|------|
| `DashboardTrafficModule.vue` | 重写布局为 3 列网格 + 底部统计行 |
| `TrafficQuotaRing.vue` | 改为甜甜圈图 + 彩色图例列表样式（已是 donut，改样式） |
| `TrafficTrendChart.vue` | 暗色主题适配（网格线 `#334155`，标签色 `#94a3b8`） |
| `TrafficRateSparkline.vue` | 暗色主题适配 |

---

## 4. 后端聚合 API

### 4.1 问题

当前 Dashboard 需要发起多次请求：
1. `GET /api/traffic/overview` — 获取概览（trend + agents 基础信息）
2. `GET /api/traffic/summary/:agent_id` — 每个节点逐个查询（串行或并行 N 次）

### 4.2 方案

新增聚合端点，**不新增字段**，复用现有数据结构：

```
GET /api/traffic/aggregate?agent_id=&granularity=
```

Query 参数：
- `agent_id`: `string` (可选，不传则全部聚合)
- `granularity`: `"hour" | "day" | "month"`

返回值：

```json
{
  "agents": [
    {
      "agent_id": "...",
      "name": "...",
      "used_bytes": 1234,
      "quota_bytes": 5678,
      "remaining_bytes": 4444,
      "blocked": false,
      "direction": "both",
      "cycle_start": "2026-05-01",
      "cycle_end": "2026-06-01"
    }
  ],
  "trend": [
    {
      "bucket_start": "2026-05-01T00:00:00Z",
      "bucket_local_start": "2026-05-01T08:00:00+08:00",
      "rx_bytes": 100,
      "tx_bytes": 200,
      "accounted_bytes": 300
    }
  ],
  "top_rules": [
    {
      "scope_type": "http_rule",
      "scope_id": 1,
      "label": "HTTP #1",
      "accounted_bytes": 1234,
      "rx_bytes": 500,
      "tx_bytes": 734
    }
  ],
  "top_nodes": [
    {
      "agent_id": "...",
      "name": "...",
      "used_bytes": 1234,
      "quota_bytes": 5678
    }
  ]
}
```

### 4.3 实现

**Go 后端**:
- 文件: `panel/backend-go/internal/api/traffic.go`
- 新增 `handleTrafficAggregate()` handler
- 复用现有 storage 查询方法，在内存中聚合
- 文件: `panel/backend-go/internal/api/routes.go`
- 注册路由 `GET /api/traffic/aggregate`

**前端**:
- 文件: `panel/frontend/src/api/index.js`
- 新增 `fetchTrafficAggregate(agentId, granularity)`
- 替换 `DashboardTrafficModule.vue` 中 3 个 `useQuery` 为 1 个

---

## 5. 其他页面适配

### 5.1 Agent 详情页

- 文件: `AgentDetailPage.vue`
- `TrafficSummaryCards.vue` — 卡片背景自动适配 CSS 变量
- `TrafficTrendChart.vue` — 暗色适配（已统一）
- 不使用新组件

### 5.2 规则详情页

- 文件: `RuleDetailPage.vue`
- `TrafficBar.vue` — 进度条颜色适配暗色
- `TrafficBreakdownTable.vue` — 表头、边框适配暗色
- 不使用新组件

### 5.3 favicon 调整

- 文件: `ThemeContext.js`
- 监听主题变化，动态切换 favicon
- 亮色主题: 粉色樱花图标 + 浅色背景
- 暗色主题: 粉色樱花图标 + 深色背景

---

## 6. 改动清单

| # | 文件 | 改动 |
|---|------|------|
| 1 | `panel/frontend/src/styles/themes.css` | 新增 `[data-theme="neko-dark"]` |
| 2 | `panel/frontend/src/context/ThemeContext.js` | 增加 neko-dark 选项，favicon 切换 |
| 3 | `panel/backend-go/internal/api/traffic.go` | 新增 `handleTrafficAggregate` |
| 4 | `panel/backend-go/internal/api/routes.go` | 注册 `GET /api/traffic/aggregate` |
| 5 | `panel/frontend/src/api/index.js` | 新增 `fetchTrafficAggregate` |
| 6 | `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | 重写布局，使用聚合 API |
| 7 | `panel/frontend/src/components/traffic/TrafficQuotaRing.vue` | 改为甜甜圈图 + 图例列表 |
| 8 | `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | 暗色适配 |
| 9 | `panel/frontend/src/components/traffic/TrafficRateSparkline.vue` | 暗色适配 |
| 10 | `panel/frontend/src/components/traffic/TrafficSummaryCards.vue` | 暗色适配 |
| 11 | `panel/frontend/src/components/traffic/TrafficBar.vue` | 暗色适配 |
| 12 | `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue` | 暗色适配 |
| 13 | `panel/frontend/src/components/base/StatCard.vue` | 暗色适配 |

---

## 7. 测试计划

1. **主题切换**: 验证 4 个主题切换正常，neko-dark 下所有组件渲染正确
2. **对比度**: 使用浏览器 devtools 检查关键文本对比度 ≥ 4.5:1
3. **聚合 API**: 验证单节点和多节点聚合数据正确
4. **响应式**: 验证 320px / 768px / 1024px / 1440px 断点布局正常
5. **现有功能**: 验证现有 3 个主题不受影响
