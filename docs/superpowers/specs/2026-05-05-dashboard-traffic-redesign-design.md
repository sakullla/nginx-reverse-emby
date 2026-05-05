# 首页流量统计模块重设计 — 设计文档

## 概述

重新设计 `DashboardPage` 首页及 `DashboardTrafficModule` 流量统计模块，采用现代简约视觉风格 + Bento 网格布局，并将图表库从 Chart.js 完全迁移至 ApexCharts。

## 目标

1. 提升首页视觉现代感与信息层次清晰度
2. 用 Bento 网格重组流量统计模块的信息架构
3. 用 ApexCharts 替换 Chart.js，引入环形图、仪表盘、迷你趋势图等 richer 可视化
4. 完全移除 Chart.js 及 `vue-chartjs` 依赖
5. 优化响应式体验

## 视觉风格

- **方向**：现代简约（Vercel / Cloudflare Dashboard 风格）
- **核心元素**：
  - 大面积留白、柔和圆角（`radius-xl` / `radius-2xl`）
  - 卡片式容器，细边框 + 微弱背景色区分层次
  - 字体层级：大标题 `1.5rem` / 模块标题 `0.875rem` / 数据 `1.125rem-1.75rem` / 辅助文字 `0.75rem`
  - 色彩：保持现有 CSS 变量体系（`--color-primary`, `--color-success`, `--color-warning`, `--color-danger`），不引入新色板

## 布局结构（方案 1：分层 Bento）

### DashboardPage 整体布局

```
┌─────────────────────────────────────────┐
│  集群概览                                │
│  实时监控所有节点状态                     │
├─────────────────────────────────────────┤
│ [总节点] [在线] [HTTP] [L4]              │  ← StatCard 视觉升级
├─────────────────────────────────────────┤
│  流量统计          [全部节点 ▼]          │
├────────────────────────┬────────────────┤
│                        │                │
│  [流量趋势面积图]        │ [配额环形图]    │
│  ApexCharts            │  donut chart   │
│  主视觉区域              │  78% / 1.2TB   │
│                        │                │
├───────────┬────────────┴────────────────┤
│ [实时速率]  │ [阻断节点]    │ [计费周期]    │
│ sparkline │  2/8 ⚠️     │  2026-05    │
├───────────┴─────────────────────────────┤
│ [Top 节点排行]    │ [Top 规则排行]        │
├─────────────────────────────────────────┤
│  节点状态              查看全部 →         │
│  [AgentTable 前 8 条]                   │
└─────────────────────────────────────────┘
```

### 流量统计模块（DashboardTrafficModule）内部 Bento 网格

| 区域 | 内容 | 宽度占比 | 高度 |
|---|---|---|---|
| 趋势图 | ApexCharts 面积图（accounted + rx + tx + host） | 2/3 | 280px |
| 配额环形图 | ApexCharts donut 展示 `used / quota` | 1/3 | 280px |
| 实时速率 | 迷你 sparkline（最近 N 个 bucket 的速率） | 1/3 | 120px |
| 阻断节点 | 数字 + 状态指示 + 可点击跳转 | 1/3 | 120px |
| 计费周期 | 周期起止 + 方向标签 | 1/3 | 120px |
| Top 节点 | 排行列表（最多 8 条） | 1/2 | auto |
| Top 规则 | 排行列表（最多 10 条） | 1/2 | auto |

## 组件设计

### 1. StatCard（升级）

现有 `StatCard` 保持接口不变，优化视觉：
- 图标区域从 48px 圆形背景改为更柔和的圆角矩形
- 数值字体增加 `letter-spacing: -0.02em` 更紧凑现代
- 可选：添加 hover 时的微弱上浮效果（`transform: translateY(-2px)`）

### 2. TrafficTrendChart（重写）

**从 Chart.js 重写为 ApexCharts。**

Props 保持不变：
```js
{
  points: Array,      // 趋势数据
  prevPoints: Array,  // 上期对比（可选）
  hostPoints: Array,  // 主机流量（可选）
  granularity: String, // 'hour' | 'day' | 'month'
  quotaBytes: Number,  // 月额度（可选）
  budgetBytes: Number  // 日均预算（可选）
}
```

ApexCharts 配置要点：
- 类型：`area`（面积图）
- 数据系列：`用量`（主色填充）、`RX`、`TX`、`主机流量`
- 上期对比用 `stroke.dashArray: [4, 4]` 虚线
- Y 轴：自定义 formatter `formatBytes`
- Tooltip：自定义显示 `formatBytes`
- 颜色与现有 CSS 变量对齐（蓝/紫/绿/灰）

### 3. TrafficQuotaRing（新增）

ApexCharts `radialBar` 或 `donut` 类型。

Props：
```js
{
  usedBytes: Number,
  quotaBytes: Number,
  remainingBytes: Number
}
```

- 中心显示百分比数字
- 颜色根据阈值变化：`success`(<70%) / `warning`(70-90%) / `danger`(>90%)
- 底部显示 `已用 / 额度` 的文字

### 4. TrafficRateSparkline（新增）

ApexCharts `area` 类型，极简配置：
- 无坐标轴、无图例、无 tooltip（或极简 tooltip）
- 仅显示线条和微弱填充
- 高度约 60px，作为卡片内的点缀元素

数据来源于趋势点的 `accounted_bytes` 差分计算速率。

### 5. Top 排行列表（复用现有）

现有 `topNodes` / `topRules` 的列表渲染逻辑基本保留，视觉微调：
- 行高增加内边距
- 进度条用 CSS 渐变条替代纯文字百分比
- 空状态保持现有 EmptyState 风格

## Chart.js → ApexCharts 迁移范围

### 移除文件

| 文件 | 操作 |
|---|---|
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | 重写 |
| `panel/frontend/src/components/traffic/TrafficTrendChart.test.js` | 重写（mock ApexCharts） |

### 新增文件

| 文件 | 说明 |
|---|---|
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | ApexCharts 面积图 |
| `panel/frontend/src/components/traffic/TrafficQuotaRing.vue` | 配额环形图 |
| `panel/frontend/src/components/traffic/TrafficRateSparkline.vue` | 速率迷你图 |

### 修改文件

| 文件 | 修改内容 |
|---|---|
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | Bento 布局重构，引入新组件 |
| `panel/frontend/src/pages/DashboardPage.vue` | StatCard 升级、整体间距调整 |
| `panel/frontend/src/components/traffic/TrafficTrendModal.vue` | 确认引用 TrafficTrendChart 正常 |
| `panel/frontend/src/pages/AgentDetailPage.vue` | 确认引用 TrafficTrendChart 正常 |
| `panel/frontend/package.json` | 移除 `chart.js`、`vue-chartjs`；添加 `apexcharts`、`vue3-apexcharts` |
| `panel/frontend/package-lock.json` | 重新生成 |

### 依赖变更

```diff
- "chart.js": "^4.5.1",
- "vue-chartjs": "^5.3.3",
+ "apexcharts": "^5.10.6",
+ "vue3-apexcharts": "^1.11.1",
```

## 响应式策略

### 桌面端（>= 1024px）
- Bento 网格按设计完整展示
- 趋势图 2/3 + 环形图 1/3
- 三列指标卡片

### 平板端（768px - 1023px）
- 趋势图和环形图上下堆叠（各 100%）
- 指标卡片 2 列
- Top 排行左右并列

### 移动端（< 768px）
- 所有卡片单列堆叠
- 趋势图高度降至 220px
- 环形图居中显示
- Top 排行上下堆叠

使用 CSS Grid `grid-template-columns` + `@media` 实现，无需 JS 断点检测。

## 数据流

保持现有数据流不变：
1. `DashboardTrafficModule` 通过 `useTrafficOverview` 获取数据
2. `selectedAgentId` 控制全局/单节点过滤
3. `topRulesQuery` 通过 `fetchTrafficSummary` 聚合规则数据
4. 所有 computed 数据转换逻辑保留

仅新增：速率 sparkline 的数据由 `trendPoints` 差分计算得到。

## 测试策略

1. `TrafficTrendChart.test.js`：重写，mock `vue3-apexcharts` 的 `apexchart` 组件
2. `DashboardTrafficModule`：现有逻辑测试保留，新增布局结构断言
3. 构建验证：`npm run build` 通过，无 Chart.js 残留

## 回滚计划

若 ApexCharts 出现不可预期问题：
1. `git revert` 本次 commit
2. 重新安装 `chart.js` + `vue-chartjs`
3. 恢复 `TrafficTrendChart.vue` 原始版本
