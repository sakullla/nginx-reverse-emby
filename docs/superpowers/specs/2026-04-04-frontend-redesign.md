# 前端重新设计规格文档

> **日期:** 2026-04-04
> **范围:** TopBar、侧边栏导航、节点管理页面、全局搜索、主题系统

---

## 1. 架构变更

### 1.1 TopBar — 仅显示当前节点名称

**现状:** TopBar 包含 Logo + 全局搜索按钮 + 主题切换 + 退出登录
**变更:** 在 Logo 右侧添加当前选中节点的纯文字名称，无下拉菜单

```
[Nginx Proxy🏠] [全局搜索...⌘K] [本机 Agent] [🌸] [🚪]
```

- 显示 `AgentContext.selectedAgentId` 对应节点的名称
- 如果未选中任何节点（如未选择代理时），显示 "全部节点" 或 "—"
- 不提供下拉切换功能 — 切换通过侧边栏节点管理页面进行

### 1.2 侧边栏 — 移除嵌入式节点列表，添加节点管理入口

**现状:** Sidebar 底部有 "节点" 标题 + 展开的 agent 列表（名称/状态/URL/重命名/删除）
**变更:**

```
顶部导航:
  [首页] [HTTP 规则] [L4 规则] [证书] [节点管理] [设置]

底部（移除）:
  节点 刷新 收起 [搜索...]
  [本机 Agent] [重命名] [删除]
  [边缘节点-01] ...
```

- 移除 Sidebar 底部的节点列表（搜索、重命名、删除功能）
- 新增 "节点管理" 导航链接 → 跳转 `/agents`
- 侧边栏仅保留顶部导航区（5个链接）+ "节点管理" 新增入口

### 1.3 路由结构调整

```
/                      → DashboardPage (集群概览)
/rules                 → RulesPage (当前选中节点的 HTTP 规则)
/rules/:id             → RuleDetailPage
/l4                    → L4RulesPage (当前选中节点的 L4 规则)
/l4/:id                → L4RulesPage
/certs                 → CertsPage
/settings              → SettingsPage
/agents                → AgentsPage (节点列表 + 统计)
/agents/:id            → AgentDetailPage (节点详情 + 该节点所有规则)  ← 新增
```

**关键:** RulesPage、L4RulesPage 从 URL 参数 `/agents/:id/rules` 获取 `agentId`，而非仅依赖 AgentContext。这样从节点详情页点击"查看规则"时能直接带上节点上下文。

---

## 2. 节点管理页面 (`/agents`)

### 2.1 AgentsPage.vue — 增强型节点卡片列表

**现有能力（保留）:**
- "加入节点" 弹窗（Linux/macOS/Windows 平台标签 + curl 命令 + 步骤说明）
- 节点删除确认弹窗
- "本机 Agent" 不可删除（`is_local: true` 时隐藏删除按钮）

**新增能力:**

**A. 节点统计列**

每个节点卡片增加 3 个统计字段（来自 mock 或 API）：

| 字段 | 说明 |
|------|------|
| HTTP 规则数 | `agent.http_rules_count` 或 0 |
| L4 规则数 | `agent.l4_rules_count` 或 0 |
| 最后活跃 | `agent.last_seen` 相对时间（如 "3 分钟前"，离线时显示 "—"） |

**B. 点击卡片进入详情页**

```
卡片结构（clickable → RouterLink to="/agents/:id"）:
┌─────────────────────────────────────────┐
│ [●] 边缘节点-01              [主控] [删除]│
│      edge-1.example.com                 │
│      🌍 HTTP 12  L4 3  ·  3 分钟前     │
└─────────────────────────────────────────┘
```

状态 dot: 绿色=在线, 橙色=同步pending, 红色=失败, 灰色=离线

**C. 节点统计汇总（页面头部）**

```
33 个节点 · 28 在线 · 累计 165 HTTP 规则 · 累计 33 L4 规则
```

---

## 3. 节点详情页 (`/agents/:id`) — 新增

### 3.1 路由

新增 `/src/pages/AgentDetailPage.vue`，路由 `agents/:id`

### 3.2 页面布局

```
┌─────────────────────────────────────────────┐
│ [← 返回节点管理]                             │
│                                             │
│  边缘节点-01              [●] 在线           │
│  edge-1.example.com                        │
│                                             │
│  ┌──────────────────────────────────────┐  │
│  │ 🖥 33 HTTP 规则  🔌 8 L4 规则         │  │
│  │ ⏱ 最后同步: 2 分钟前                   │  │
│  │ 📋 同步状态: [已同步] / [同步失败]      │  │
│  └──────────────────────────────────────┘  │
│                                             │
│  [HTTP 规则] [L4 规则] [证书] [系统信息]    │  ← Tab 切换
│                                             │
│  Tab 内容:                                  │
│  - HTTP 规则: 该节点的所有规则列表           │
│  - L4 规则: 该节点的 L4 规则列表            │
│  - 证书: 该节点的证书列表                    │
│  - 系统信息: 版本、IP、系统信息、配置        │
└─────────────────────────────────────────────┘
```

### 3.3 Tab 数据流

| Tab | 数据来源 |
|-----|---------|
| HTTP 规则 | `useRules(agentIdRef)` — agentId 来自 `route.params.id` |
| L4 规则 | `useL4Rules(agentIdRef)` |
| 证书 | `useCerts(agentIdRef)` |
| 系统信息 | `useAgent(route.params.id)` — 节点基本信息 |

### 3.4 导航行为

- 点击 "← 返回节点管理" → RouterLink to `/agents`
- 点击 RulesPage/L4RulesPage 中的 "添加规则" → 保持当前 agentId context
- RulesPage/L4RulesPage 需要支持从 URL param 读取 agentId（用于从节点详情页进入的场景）

---

## 4. RulesPage / L4RulesPage — 支持 URL agentId

**现状:** RulesPage 用 `useAgent().selectedAgentId` 获取当前节点
**问题:** 从节点详情页点击 "HTTP 规则" Tab 时，没有设置 AgentContext

**修复方案:**

```js
// RulesPage.vue
const route = useRoute()
// 优先从 URL query 获取 agentId（从节点详情页进入），否则 fall back 到 AgentContext
const agentId = computed(() => route.query.agentId || selectedAgentId.value)
```

同样适用于 L4RulesPage。

**导航链接:**

```
AgentDetailPage → HTTP 规则 Tab → RouterLink to="/rules" with :agentId as query
AgentDetailPage → L4 规则 Tab → RouterLink to="/l4" with :agentId as query
```

即: `<RouterLink :to="{ path: '/rules', query: { agentId: agent.id } }">`

---

## 5. 全局搜索修复

### 5.1 问题根因

`AppShell.vue` 中没有引入 `GlobalSearch` 组件，`TopBar` 发出的 `open-search` 事件无人接收，搜索面板从不显示。

### 5.2 修复步骤

**Step 1: AppShell 中添加 GlobalSearch**

```vue
<!-- AppShell.vue -->
<script setup>
import GlobalSearch from '../GlobalSearch.vue'
const searchOpen = ref(false)
</script>

<!-- template -->
<GlobalSearch :open="searchOpen" @update:open="searchOpen = $event" />
```

**Step 2: TopBar 监听按钮点击**

```vue
<!-- TopBar 中 -->
<button class="topbar__search" @click="$emit('open-search')">
```

**Step 3: AppShell 监听事件**

```vue
<TopBar @open-search="searchOpen = true" />
```

**Step 4: 搜索逻辑**

GlobalSearch 组件已有 `emit('search', query)` 事件。需要在 AppShell（或新建 `useGlobalSearch` hook）中：
- 接收 search 事件
- 并行调用 `fetchRules` 获取所有节点的规则（需新增跨节点 API）或逐个 agent 查询
- 返回 `{ agentId, agentName, online, rules[] }` 结构

**Step 5: 选中结果后导航**

点击搜索结果 → 导航到 `/agents/:id` 并高亮对应规则

### 5.3 搜索范围

支持搜索 HTTP 规则（frontend_url、backend_url、tags）。暂不搜索 L4 规则和证书。

---

## 6. 移除赛博朋克主题

### 6.1 统一移除（通过 ThemeContext）

在 ThemeContext.js 的 `themes` 数组中移除 cyberpunk 条目。ThemeSelector 和 SettingsPage 都从 ThemeContext 导入同一份 themes 列表，实现一处定义、统一生效。

### 6.2 回退逻辑

如果 `localStorage['theme'] === 'cyberpunk'`（用户之前选过），自动回退到 'sakura'。

---

## 7. 设置页 — 移除访问令牌区块

**现状:** SettingsPage 有"访问令牌"区块（显示/保存按钮），后端不支持修改。

**变更:** 移除该区块。

---

## 8. 设置页主题切换显示修复

### 8.1 问题

SettingsPage 和 ThemeSelector 使用独立的 themes 数组。ThemeSelector 有 4 个主题（sakura/business/midnight），SettingsPage 只有 2 个（sakura/cyberpunk）。SettingsPage 的 themes 来自自身定义，而非 ThemeContext。

### 8.2 修复

在 `ThemeContext.js` 中定义统一的 `themes` 数组并导出：

```js
// ThemeContext.js
export const themes = [
  { id: 'sakura',   emoji: '🌸', label: '二次元' },
  { id: 'business', emoji: '☀️', label: '晴空'    },
  { id: 'midnight', emoji: '🌙', label: '暗夜'   }
]

export function useTheme() {
  // useTheme 返回 themes，以及 currentThemeId、setTheme
}
```

SettingsPage 改为：
```js
import { themes } from '../context/ThemeContext'
const { currentThemeId: currentTheme, setTheme } = useTheme()
// themes 直接使用，不从 useTheme() 解构
```

ThemeSelector 也改为从 ThemeContext 导入 themes（而非本地定义），cyberpunk 在 ThemeContext 的 themes 数组中统一移除。

---

## 9. L4RulesPage — 补充编辑功能

**现状:** L4RulesPage 只有 toggle 开关和删除按钮，没有编辑按钮。

**新增:** 编辑按钮（铅笔图标），点击后用已有表单弹窗编辑 L4 规则：

```vue
<!-- 操作列 -->
<button class="btn-icon" title="编辑" @click="startEdit(rule)">
  <svg>...</svg>
</button>
<button class="btn-icon btn-icon--danger" title="删除" @click="startDelete(rule)">
  <svg>...</svg>
</button>
```

```js
// script
const editingRule = ref(null)
function startEdit(rule) {
  editingRule.value = rule
  form.value = { protocol: rule.protocol, listen_host: rule.listen_host, listen_port: String(rule.listen_port), upstream_host: rule.upstream_host, upstream_port: String(rule.upstream_port), tags: (rule.tags || []).join(', ') }
  showAddForm.value = true
}
// submitForm 中增加判断：如果 editingRule.value 存在则调用 updateL4Rule
```

表单复用现有的 "添加 L4 规则" 弹窗（modal header 改为 "编辑 L4 规则"）。

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `src/components/layout/TopBar.vue` | 修改 | 显示当前节点名称 |
| `src/components/layout/Sidebar.vue` | 修改 | 移除节点列表，添加节点管理链接 |
| `src/components/layout/AppShell.vue` | 修改 | 引入 GlobalSearch，监听 open-search |
| `src/components/GlobalSearch.vue` | 修改 | 实现跨节点搜索逻辑 |
| `src/components/base/ThemeSelector.vue` | 修改 | 移除 cyberpunk 主题 |
| `src/context/ThemeContext.js` | 修改 | 统一 themes 数组（含3个主题），移除 cyberpunk |
| `src/pages/AgentsPage.vue` | 修改 | 增强：统计列、点击进入详情 |
| `src/pages/AgentDetailPage.vue` | 新增 | 节点详情页（4个Tab） |
| `src/pages/RulesPage.vue` | 修改 | 支持 URL agentId |
| `src/pages/L4RulesPage.vue` | 修改 | 支持 URL agentId + 补充编辑功能 |
| `src/pages/SettingsPage.vue` | 修改 | 移除访问令牌区块；useTheme 集成修复 |
| `src/router/index.js` | 修改 | 添加 `/agents/:id` 路由 |
| `src/hooks/useRules.js` | 修改 | 可接受 computed ref 作为 agentId |
| `src/hooks/useL4Rules.js` | 同上 | 同上 |

---

## 10. 不在本设计范围内的内容

- CertsPage 增强（证书申请、续期、下载）
- Dashboard 图表（流量、延迟可视化）
- 移动端 BottomNav 变更
- 暗色主题切换的 CSS 变量实现
