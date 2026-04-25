# 节点管理重设计

## 背景

当前节点管理页面（AgentsPage）使用卡片网格布局，支持搜索但缺少系统化的筛选和排序能力。节点在多个页面被使用（TopBar Agent Switcher、Dashboard、规则页面），但各处的节点展示和交互缺乏一致性。本设计旨在统一节点相关的所有交互，提升管理效率。

## 目标

1. 重新设计 AgentsPage，支持卡片/列表混合布局、下拉筛选、排序
2. 增强 TopBar Agent Switcher 的可用性
3. 统一 Dashboard 节点表格的展示和交互
4. 改善规则页面的节点选择体验

## 架构与组件拆分

### 新增可复用组件

| 组件 | 用途 | 使用位置 |
|------|------|---------|
| `AgentFilterBar.vue` | 视图切换 + 筛选下拉 + 排序下拉 | AgentsPage |
| `AgentCard.vue` | 单个节点卡片 | AgentsPage 卡片视图 |
| `AgentTable.vue` | 节点表格 | AgentsPage 列表视图、Dashboard |
| `AgentStatusBadge.vue` | 状态徽章（带文字） | 全站所有显示节点状态的地方 |
| `AgentPicker.vue` | 嵌入式节点选择器 | RulesPage、L4RulesPage 等空状态 |
| `AgentSwitcher.vue`（重构） | TopBar 下拉，增加筛选/排序 | TopBar |

### 调整现有组件

- `TopBar.vue` — Agent Switcher 替换为增强版
- `DashboardPage.vue` — 节点表格替换为 `AgentTable`，增加行跳转
- `AgentsPage.vue` — 整体重构，使用新组件

## AgentsPage 节点管理页

### 顶部工具栏

从左到右：
- **视图切换**：卡片图标 / 列表图标按钮，点击切换，激活项高亮。偏好持久化到 `localStorage`。
- **筛选下拉**：
  - 状态：全部、在线、离线、失败、同步中（单选）
  - 模式：全部、本机、主控、拉取（单选）
  - 标签：动态从所有节点的 `tags` 提取并去重。单选。
- **排序**：下拉选择维度（最后活跃/名称/HTTP规则数/L4规则数），右侧箭头按钮切换升/降序。默认最后活跃降序。
- **搜索框**：名称/IP/标签/#id=...（保持现有行为）
- **加入节点**按钮

筛选和排序状态保存在 URL query 参数中（`view`, `status`, `mode`, `tag`, `sort`, `order`），支持刷新保留和链接分享。

### 卡片视图

保持现有卡片风格，每张卡片展示：
- 左上角：状态徽章 + 模式徽章
- 右上角：重命名、删除按钮（hover 显示）
- 节点名称（大号）
- URL 或 IP（等宽字体）
- 底部行：HTTP 规则数、L4 规则数、最后活跃时间
- 如果节点有标签，在底部增加标签行（胶囊样式）

网格：`grid-template-columns: repeat(auto-fill, minmax(300px, 1fr))`

### 列表视图

表格列：

| 列 | 内容 | 宽度 |
|--|------|------|
| 名称 | 节点名称 + URL/IP（两行） | flex: 2 |
| 状态 | 状态徽章 | 80px |
| 模式 | 模式徽章 | 70px |
| HTTP | 规则数量 | 70px |
| L4 | 规则数量 | 70px |
| 最后活跃 | 时间（如"2h"） | 90px |
| 操作 | 重命名、删除图标按钮 | 80px |

行 hover 有背景色变化。点击整行进入节点详情。

### 空状态

保持现有空状态。如果当前有筛选条件但无结果，显示"没有符合筛选条件的节点" + "清除筛选"按钮。

## TopBar Agent Switcher

### 触发按钮

保持现有样式：状态圆点 + 节点名称 + 下拉箭头。

### 下拉面板（宽度 280px）

从上到下：
1. **搜索输入框**：搜索节点名称/IP
2. **状态快速筛选标签**：胶囊式单行（全部/在线/离线），可横向滚动。单选，默认"全部"。
3. **节点列表**：每行显示状态圆点 + 节点名称 + 最后活跃时间（右对齐）。默认按最后活跃时间倒序。
4. **底部排序条**：切换"最近活跃" / "按名称"。

点击节点后关闭下拉、设置选中节点、清空搜索和筛选。如果在 agent-detail 页面，导航到新节点的详情页。

## Dashboard 节点状态表格

表格列与 AgentsPage 列表视图保持一致：节点、状态、模式、HTTP、L4、最后活跃。

### 行交互

- hover：行背景色变化
- 点击整行：跳转至 `/agents/{id}`（`cursor: pointer`）

### 头部

右侧"查看全部 →"保留，跳转至 `/agents`。

### 筛选联动增强

点击状态徽章可跳转至 AgentsPage 并自动带上对应筛选条件（如 `status=offline`）。

## 规则页面节点选择器

### 空状态页面

中央展示：
- 图标
- 提示文字："请选择一个节点来管理规则"
- **嵌入式 `AgentPicker.vue`**
- "或前往节点管理页面添加新节点" + [加入节点] 按钮

### AgentPicker 组件

嵌入式下拉选择器：
- **触发区域**：输入框样式，显示"选择节点..."或当前选中节点名称
- **下拉面板**：搜索框 + 状态快速筛选 + 节点列表 + 排序切换
- **选中行为**：通过 `router.replace` 设置 `?agentId={id}`，组件自动拉取数据

### 应用范围

RulesPage、L4RulesPage、RelayListenersPage、CertsPage 统一使用。

已选择节点后，页面顶部显示当前节点名称，旁边增加"切换节点"按钮。

## 数据流与状态管理

### 状态持久化

| 状态 | 位置 | 说明 |
|------|------|------|
| 筛选/排序 | URL query | AgentsPage，支持分享和刷新保留 |
| 视图偏好 | localStorage | `agent-list-view: 'card' \| 'list'` |
| Switcher 筛选 | 局部 ref | 关闭后自动清空 |
| Picker 筛选 | 局部 ref | 关闭后自动清空 |

### 筛选逻辑（前端实现）

所有筛选排序在前端完成（对 `agents` 数组进行 `computed` 处理）：

```js
filteredAgents = agents
  .filter(a => !statusFilter || getStatus(a) === statusFilter)
  .filter(a => !modeFilter || a.mode === modeFilter)
  .filter(a => !tagFilter || (a.tags || []).includes(tagFilter))
  .filter(a => !searchQuery || matchesSearch(a, searchQuery))
  .sort((a, b) => applySort(a, b, sortField, sortOrder))
```

### 标签动态提取

标签筛选选项从所有节点的 `tags` 中动态提取、去重、按字母排序。无标签时显示"无可用标签"并禁用。

## 响应式与移动端

### AgentsPage

- **桌面端（>768px）**：工具栏所有控件在一行
- **平板端（640px-768px）**：筛选折叠为 2 个，标签筛选收为"更多筛选"按钮
- **移动端（<640px）**：工具栏换行；卡片视图单列；列表视图表格横向滚动

### TopBar Agent Switcher

- 下拉面板宽度自适应（最小 260px）
- 状态快速筛选标签支持横向滚动

### AgentPicker

- 移动端下拉面板全宽（与父容器同宽）

### Dashboard

- 移动端表格允许横向滚动
- 统计卡片保持 2 列

## 实现范围汇总

| 页面/组件 | 核心改动 |
|-----------|---------|
| AgentsPage | 混合布局 + 筛选/排序 + URL 状态持久化 |
| TopBar Agent Switcher | 状态快速筛选 + 排序 + 列表项增加活跃时间 |
| Dashboard | 表格列对齐 + 行点击跳转 + 状态徽章筛选联动 |
| 规则页面空状态 | 新增嵌入式 AgentPicker |
| 全站 | 新增 6 个可复用组件 |
