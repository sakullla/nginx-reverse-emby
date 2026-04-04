# 前端重新设计规格书

> 日期：2026-04-04
> 状态：已确认

## 1. 概述

对 `nginx-reverse-emby` 管理面板前端进行完整重建，从技术栈到视觉风格全面升级。保持现有功能不变，改进架构清晰度、可维护性和用户体验。

### 目标

- 拆分单体 App.vue，建立清晰的分层架构
- 引入现代工具链，提升开发体验和性能
- 保留并升级现有的多主题系统
- 从头重新设计移动端导航体验
- 保持所有现有功能不变

---

## 2. 技术栈

| 层级 | 选择 | 说明 |
|------|------|------|
| 框架 | Vue 3 + Vite 5 | 保持 Vue 3，复用现有技能栈 |
| 原子 CSS | UnoCSS | Tailwind 兼容预设，按需生成原子类 |
| 路由 | Vue Router 4 | 扁平 URL，直接反映功能页面 |
| 服务端状态 | TanStack Query (Vue Query) | 接管所有服务端状态，无 Pinia |
| 组件类型 | Headless + 自建样式 | 少量 Radix Vue 搭配 UnoCSS 样式 |

### 为什么不用 Pinia

TanStack Query 提供：
- 自动缓存 + 轮询
- 乐观更新
- 后台预取
- 请求去重和取消

将服务端状态（agents, rules, certs）与 UI 状态分离，职责单一。

---

## 3. 路由结构

所有路由扁平，直接反映功能：

```
/                     → 首页 Dashboard（集群概览）
/agents               → 节点列表（管理所有 Agent，包含添加/删除/重命名）
/rules               → HTTP 规则列表（当前选中 Agent）
/rules/:id           → 单条 HTTP 规则详情/编辑
/l4                  → L4 规则列表（当前选中 Agent）
/l4/:id              → 单条 L4 规则详情/编辑
/certs               → 统一证书列表（跨 Agent）
/settings            → 系统设置（Token、主题、部署模式）
```

### Agent 上下文

Agent 选择作为**全局上下文**，不编码在 URL 中：
- 桌面端：侧边栏顶部 Agent 选择器，切换后刷新当前页面数据
- 移动端：独立的 `/agents` 页面作为 Tab 之一
- 规则列表 URL `/rules` 始终显示当前选中 Agent 的规则

---

## 4. 布局结构

### 桌面端（≥1024px）

```
┌─────────────────────────────────────────────┐
│  TopBar: Logo + 全局搜索(⌘K) + 主题 + 退出   │
├──────────┬──────────────────────────────────┤
│          │                                  │
│ Sidebar  │   <RouterView>                  │
│ (Agent   │   规则列表 / 证书 / 设置 等        │
│  列表)   │                                  │
│          │                                  │
└──────────┴──────────────────────────────────┘
```

- Sidebar 宽度 260px（可折叠到 64px 图标模式）
- 内容区 `<RouterView>` 独立滚动
- TopBar 高度 64px，固定定位

### 移动端（<1024px）

```
┌─────────────────────────┐
│  TopBar (简化版)         │
├─────────────────────────┤
│                         │
│   <RouterView>          │
│                         │
├─────────────────────────┤
│  底部 Tab 栏 (4 个)      │
└─────────────────────────┘
```

底部 Tab：
1. 首页（Dashboard）
2. 规则（HTTP + L4 子 Tab 切换）
3. 证书
4. 设置

移动端无侧边栏，Agent 切换通过 `/agents` 页面。

---

## 5. 主题系统

### 3 套主题，各有独立主导色

| 主题 ID | 名称 | Emoji | 主导色 | 风格 |
|---------|------|-------|--------|------|
| `sakura` | 二次元 | 🌸 | `#c084fc` 紫罗兰 | 梦幻动漫风 |
| `business` | 晴空 | ☀️ | `#2563eb` 蓝 | Linear/Notion 专业风 |
| `midnight` | 暗夜 | 🌙 | `#818cf8` 靛蓝 | GitHub Dark 风 |

### 主题包含的 CSS 变量（每套主题独立定义）

- `--color-primary` 系列（主色、主色悬停、主色激活、主色透明背景）
- `--color-text-*` 系列（主文字、次要文字、三级文字、占位文字、反色文字）
- `--color-bg-*` 系列（画布色、表面色、表面凸起、微妙色、悬停色、激活色）
- `--color-border-*` 系列（微妙、默认、强调）
- `--color-success/warning/danger` 系列
- `--shadow-*` 系列（xs, sm, md, lg, xl, 2xl, focus, glow）
- `--gradient-primary`、`--gradient-soft`
- `--theme-bg` 背景渐变

### 主题切换机制

- `data-theme` 属性设置在 `<html>` 元素上
- `ThemeSelector` 组件管理切换，下拉选择
- 设置持久化到 `localStorage`
- 默认跟随系统（`prefers-color-scheme`）

---

## 6. 状态管理

### TanStack Query 职责

每个数据域对应一组 `useQuery` / `useMutation` hooks：

```typescript
// 节点
useAgents()                    // 获取所有 Agent
useAgent(agentId)              // 获取单个 Agent
useCreateAgent()
useUpdateAgent()
useDeleteAgent()

// HTTP 规则（按 Agent 隔离）
useRules(agentId)              // 获取规则列表
useCreateRule(agentId)
useUpdateRule(agentId)
useDeleteRule(agentId)

// L4 规则
useL4Rules(agentId)
useCreateL4Rule(agentId)
useUpdateL4Rule(agentId)
useDeleteL4Rule(agentId)

// 证书（跨 Agent）
useCertificates()
useCreateCertificate()
useUpdateCertificate()
useDeleteCertificate()
useIssueCertificate()

// 全局搜索
useGlobalSearch(query)
```

### Context 职责

```typescript
// UI 状态，不走 Query
AgentContext     // 当前选中的 Agent ID
ThemeContext     // 当前主题
ModalContext     // Modal 开关状态
```

### 组件内状态

表单输入、展开状态、本地 loading 等，留在组件内部。

---

## 7. 关键组件

### 布局组件

| 组件 | 路径 | 职责 |
|------|------|------|
| `AppShell` | `components/layout/AppShell.vue` | 根布局：TopBar + Sidebar + RouterView |
| `TopBar` | `components/layout/TopBar.vue` | Logo、全局搜索、主题切换、退出 |
| `Sidebar` | `components/layout/Sidebar.vue` | 桌面端 Agent 列表，支持折叠 |
| `BottomNav` | `components/layout/BottomNav.vue` | 移动端底部 Tab 导航 |
| `MobileAgentPage` | `pages/AgentsPage.vue` | 移动端 Agent 管理全屏页面 |

### 页面组件

| 组件 | 路径 | 路由 |
|------|------|------|
| `DashboardPage` | `pages/DashboardPage.vue` | `/` |
| `AgentsPage` | `pages/AgentsPage.vue` | `/agents` |
| `RulesPage` | `pages/RulesPage.vue` | `/rules` |
| `RuleDetailPage` | `pages/RuleDetailPage.vue` | `/rules/:id` |
| `L4RulesPage` | `pages/L4RulesPage.vue` | `/l4` |
| `CertsPage` | `pages/CertsPage.vue` | `/certs` |
| `SettingsPage` | `pages/SettingsPage.vue` | `/settings` |

### 业务组件

| 组件 | 路径 | 职责 |
|------|------|------|
| `RuleTable` | `components/rules/RuleTable.vue` | 规则列表，支持展开行内编辑 |
| `RuleForm` | `components/rules/RuleForm.vue` | 添加/编辑规则表单 |
| `L4RuleTable` | `components/l4/L4RuleTable.vue` | L4 规则列表 |
| `L4RuleForm` | `components/l4/L4RuleForm.vue` | 添加/编辑 L4 规则表单 |
| `CertCard` | `components/certs/CertCard.vue` | 证书卡片展示 |
| `StatsGrid` | `components/dashboard/StatsGrid.vue` | Dashboard 统计网格 |
| `GlobalSearch` | `components/GlobalSearch.vue` | ⌘K 快捷面板 |
| `ThemeSelector` | `components/base/ThemeSelector.vue` | 主题切换（保留现有实现） |

### Headless 组件

使用 Radix Vue 提供基础交互：
- `Dialog` — Modal 弹窗
- `DropdownMenu` — 下拉菜单
- `Select` — 选择器
- `Tabs` — Tab 切换
- `Collapsible` — 可折叠区域
- `Tooltip` — 工具提示

样式全部用 UnoCSS 原子类自定义。

---

## 8. 移动端设计

### 导航结构

- **4 个固定 Tab**：首页、规则、证书、设置
- **Agent 切换**：作为 `/agents` 页面，点击底部 Tab "节点"进入
- **规则子 Tab**：HTTP / L4 在规则 Tab 内切换（Segmented Control）

### 交互模式

- 列表页：全屏滚动，支持下拉刷新
- 详情页：全屏覆盖，点击返回
- 表单页：全屏覆盖，底部固定提交按钮
- 搜索：`⌘K` 快捷面板（移动端键盘上方显示）

### 底部 Tab 栏

```
┌────┬────┬────┬────┐
│🏠  │🔗  │🔒  │⚙️  │
│首页│规则│证书│设置│
└────┴────┴────┴────┘
```

图标 + 文字，活跃态高亮主色。

---

## 9. 全局搜索

- 触发：`⌘K` / `Ctrl+K` 快捷键，或点击 TopBar 搜索图标
- 功能：跨节点搜索 HTTP 规则、L4 规则、证书
- 展示：分组显示，每组按 Agent 聚合
- 交互：点击结果跳转到对应 Agent 的规则详情

---

## 10. 文件结构

```
panel/frontend/src/
├── main.js
├── App.vue
├── router/
│   └── index.js
├── pages/
│   ├── DashboardPage.vue
│   ├── AgentsPage.vue
│   ├── RulesPage.vue
│   ├── RuleDetailPage.vue
│   ├── L4RulesPage.vue
│   └── CertsPage.vue
│   └── SettingsPage.vue
├── components/
│   ├── layout/
│   │   ├── AppShell.vue
│   │   ├── TopBar.vue
│   │   ├── Sidebar.vue
│   │   └── BottomNav.vue
│   ├── rules/
│   │   ├── RuleTable.vue
│   │   └── RuleForm.vue
│   ├── l4/
│   │   ├── L4RuleTable.vue
│   │   └── L4RuleForm.vue
│   ├── certs/
│   │   └── CertCard.vue
│   ├── dashboard/
│   │   └── StatsGrid.vue
│   ├── GlobalSearch.vue
│   └── base/
│       └── ThemeSelector.vue   # 复用现有实现
├── hooks/
│   ├── useAgents.js
│   ├── useRules.js
│   ├── useL4Rules.js
│   ├── useCertificates.js
│   └── useGlobalSearch.js
├── context/
│   ├── AgentContext.js
│   └── ThemeContext.js
├── api/
│   └── index.js               # API 调用封装（保留现有 mock 机制）
├── styles/
│   └── themes.css             # 3 套主题 CSS 变量
└── uno.config.js              # UnoCSS 配置
```

---

## 11. 待确认事项

- [x] 技术栈选择
- [x] 路由结构
- [x] 状态管理方案
- [x] 移动端导航模式
- [x] 主题系统
- [ ] 分阶段还是一次性完成（一次性）
- [x] 移除赛博朋克主题

---

## 12. 实现顺序（建议）

1. 项目脚手架：Vite + Vue Router + TanStack Query + UnoCSS + Radix Vue
2. 主题系统：迁移现有 3 套主题到新 CSS 变量体系
3. 布局组件：AppShell、TopBar、Sidebar、BottomNav
4. 路由配置：所有页面路由
5. Context 层：AgentContext、ThemeContext
6. Hooks 层：useAgents、useRules、useL4Rules、useCertificates
7. 页面组件（按顺序）：Dashboard → Agents → Rules → L4 → Certs → Settings
8. 全局搜索 ⌘K
9. 响应式调试（桌面 / 平板 / 移动）
10. 动画和过渡效果
