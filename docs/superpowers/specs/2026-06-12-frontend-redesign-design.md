# 前端 Redesign 设计文档

**日期：** 2026-06-12  
**主题：** nginx-reverse-emby 控制面板前端视觉优化与列表视图支持  
**状态：** 已评审，待实现计划

---

## 1. 背景与目标

当前 `panel/frontend` 已基于 Vue 3 + Vite + Pinia + TanStack Query + UnoCSS 构建，具备 4 套主题、响应式布局、全局搜索和较完整的页面覆盖。本次 redesign 的目标是在**保持现有视觉风格和信息架构不变**的前提下：

1. 提升**组件一致性**（按钮、表单、卡片、弹窗、标签等）。
2. 改善**间距与排版**（留白、字体层级、页面节奏）。
3. 为 HTTP 规则、L4 规则、证书、Relay、WireGuard 五类页面增加**卡片 / 列表视图切换**能力。

---

## 2. 范围

### 2.1 In Scope

- **视觉优化（保持现有风格）**
  - 统一 Base 组件层。
  - 整理 design token 与样式文件边界。
  - 统一页面容器、标题、页眉、空状态、加载状态的间距与排版。
- **列表视图支持**
  - HTTP 规则页（`/rules`）
  - L4 规则页（`/l4`）
  - 证书管理页（`/certs`）
  - Relay 监听器页（`/relay-listeners`）
  - WireGuard 配置页（`/wireguard-profiles`）
- **状态与交互**
  - 视图模式（卡片 / 列表）持久化到 `localStorage`。
  - 搜索、筛选、排序在两种视图下共享同一数据源。

### 2.2 Out of Scope

- 不更换技术栈。
- 不新增主题或改变现有 4 套主题配色。
- 不改动页面路由、导航结构、信息架构。
- 不做视觉回归测试或像素级 redesign。
- 节点管理页、版本策略页、设置页本次不做列表视图。

---

## 3. 总体方案

采用**分阶段推进**方案：

1. **Phase 1：设计 token 整理 + Base 组件搭建**
2. **Phase 2：用新 Base 组件实现列表视图切换**
3. **Phase 3：逐步迁移现有页面到 Base 组件，统一间距和排版**

该方案在“保持现状”的约束下，兼顾一致性与交付节奏，同时避免新增技术债务。

---

## 4. 架构

### 4.1 不变

- 技术栈：Vue 3 + Vite + Pinia + TanStack Query + Vue Router + UnoCSS。
- 4 套主题（`sakura-day`、`sakura-night`、`neko-dark`、`business`）保持可用。
- 页面路由、信息架构、侧边栏/顶部栏布局保持现状。
- 现有 API hooks（`useRules`、`useCertificates`、`useRelayListeners`、`useWireGuardProfiles` 等）继续复用。

### 4.2 新增

- **`components/base/` 设计系统层**：把散落的 `.btn`、`.input`、`.modal` 等全局 class 封装成可复用 Vue 组件。
- **`composables/useViewMode.js`**：统一管理「卡片 / 列表」视图偏好，并持久化到 `localStorage`。
- **列表视图组件**：为 HTTP 规则、L4 规则、证书、Relay、WireGuard 提供列表/表格形态。

### 4.3 调整

- 整理 `styles/themes.css`、`styles/utilities.css`、`styles/index.css` 的边界：
  - `themes.css`：仅存放 design token（颜色、间距、字体、阴影、圆角等）。
  - `utilities.css`：仅存放跨页面工具类（如 `.page-header`、`.card-grid`）。
  - 组件专属样式移到对应 Base 组件的 `<style scoped>`。

---

## 5. Base 组件层

### 5.1 新增/强化组件

| 组件 | 职责 |
|------|------|
| `BaseButton` | 统一按钮 variant（primary / secondary / ghost / danger）、size、loading、disabled |
| `BaseInput` / `BaseSearch` | 统一输入框、搜索框样式与 focus 状态 |
| `BaseTag` / `BaseBadge` | 统一标签与状态徽章 |
| `BaseCard` | 统一卡片容器（圆角、边框、hover、padding），业务卡片只保留内部布局 |
| `BaseModal` | 统一弹窗容器（header / body / footer），替换现有 modal 实现 |
| `BaseTable` | 列表视图核心：表头、排序指示、行操作、空状态、加载骨架、响应式横向滚动 |
| `BaseEmptyState` | 统一空状态（图标、标题、提示、操作按钮） |
| `BaseSkeleton` | 统一加载占位 |
| `BaseErrorState` | 统一错误状态（带重试按钮） |
| `ViewToggle` | 卡片 / 列表切换按钮组 |

### 5.2 组件设计原则

- 每个 Base 组件只负责**容器与状态**，不包含业务逻辑。
- 通过 props 暴露 variant、size、disabled、loading 等常见状态。
- 组件内部使用 CSS 变量，自动适配 4 套主题。
- 复杂组件（如 `BaseTable`）通过 slots 或 render function 支持自定义列渲染。

---

## 6. 数据流

### 6.1 视图模式状态

新增 `composables/useViewMode.js`：

```js
const { viewMode, setViewMode } = useViewMode('rules')
// viewMode: 'card' | 'list'
// 持久化到 localStorage: nre.viewMode.rules
```

- 每个资源一个 key，避免不同页面互相覆盖。
- 默认值 `card`，与现状一致。
- 切换时即时响应，不触发重新请求数据。

### 6.2 列表/卡片共享同一数据源

以规则页为例：

```text
useRules(agentId) ──→ rules (TanStack Query)
       ↓
filteredRules (computed: search + sort)
       ↓
   viewMode === 'card' ? RuleCard[] : BaseTable[]
```

- 搜索、筛选、排序逻辑放在页面层，两种视图共用。
- 行/卡片操作共用同一组 mutation hooks。
- 选中、展开、诊断等交互状态尽量共享。

### 6.3 BaseTable 输入示例

```vue
<BaseTable
  :columns="ruleColumns"
  :rows="filteredRules"
  :loading="isLoading"
  :sort="sortState"
  @sort="handleSort"
  @row-action="handleAction"
/>
```

`ruleColumns` 在页面内定义，决定列宽、渲染方式、排序字段。

### 6.4 响应式策略

- 桌面端：表格正常展示所有列。
- 平板/小屏：`BaseTable` 自动横向滚动，或折叠次要列。
- 移动端：列表视图保留，操作按钮收进更多菜单（`...`）。

---

## 7. 错误处理

### 7.1 三态统一

每个列表/表格区域统一三种状态：

- **Loading**：`BaseSkeleton` 占位，避免布局跳动。
- **Empty**：`BaseEmptyState`，带图标、标题、操作按钮。
- **Error**：`BaseErrorState`，带重试按钮。

### 7.2 BaseErrorState

```vue
<BaseErrorState
  title="加载失败"
  message="无法获取规则列表，请检查网络或节点状态"
  :retry="refetch"
/>
```

- 重试按钮调用对应 query 的 `refetch`。
- Mutation 错误继续复用现有 `StatusMessage` 全局提示。

### 7.3 表格内错误

`BaseTable` 支持 `error` prop，在表格区域显示错误条，而不是整页错误，减少上下文丢失。

---

## 8. 测试策略

### 8.1 组件单元测试

为新增 Base 组件写 Vitest 测试：

- `BaseButton.test.js`：variant / size / disabled / loading 渲染。
- `BaseTable.test.js`：列渲染、排序事件、空状态。
- `ViewToggle.test.js`：切换事件。
- `useViewMode.test.js`：`localStorage` 读写。

### 8.2 页面集成测试

为 5 个目标页面补充：

- 视图切换按钮存在且可点击。
- 卡片视图和列表视图都能渲染数据。
- 切换视图后 `localStorage` 正确保存。
- 搜索/筛选在两种视图下结果一致。

### 8.3 不做

- 不做视觉回归测试（超出本次范围）。
- 不改动现有 API hooks 测试。

---

## 9. 实施阶段

### Phase 1：基础层（预计 1 个迭代）

1. 整理 `themes.css`、`utilities.css`、`index.css` 边界。
2. 创建 `components/base/` 下 `BaseButton`、`BaseInput`、`BaseSearch`、`BaseTag`、`BaseBadge`、`BaseCard`、`BaseModal`、`BaseEmptyState`、`BaseSkeleton`。
3. 创建 `composables/useViewMode.js`。
4. 补充对应单元测试。

### Phase 2：列表视图（预计 1-2 个迭代）

1. 创建 `BaseTable`、`ViewToggle`。
2. 在 HTTP 规则页、L4 规则页实现卡片/列表切换。
3. 在证书页、Relay 监听器页、WireGuard 配置页实现卡片/列表切换。
4. 补充页面集成测试。

### Phase 3：页面迁移与打磨（预计 1-2 个迭代）

1. 逐步将现有页面中的 `.btn`、`.input`、`.modal` 等全局 class 替换为 Base 组件（优先 5 个目标页面，再扩展到其他页面）。
2. 统一页面容器、标题、页眉、空状态、加载状态的间距与排版。
3. 修复迁移过程中发现的视觉不一致问题。

---

## 10. 风险与回滚

- **风险**：Phase 3 迁移范围较广，可能引入回归。
- **缓解**：按页面分批迁移，每次迁移后立即运行 `npm run test` 和 `npm run build`。
- **回滚**：Base 组件与现有全局 class 可共存，单个页面迁移失败可回退到原实现。

---

## 11. 决策记录

| 决策 | 选项 | 选择 | 原因 |
|------|------|------|------|
| 视觉方向 | 大改 / 保持现状 | 保持现状 | 现有主题已覆盖多场景，避免用户重新适应 |
| 视图模式持久化 | localStorage / URL query / 后端偏好 | localStorage | 实现简单、无需后端改动 |
| 列表实现 | 每页独立 / 统一 BaseTable | 统一 BaseTable | 保证一致性和可维护性 |
| 迁移策略 | 大爆炸 / 分阶段 | 分阶段 | 风险低，可持续交付 |

---

## 12. 附录：目录结构变化

```text
panel/frontend/src/
├── components/
│   ├── base/                 # 新增/强化
│   │   ├── BaseButton.vue
│   │   ├── BaseInput.vue
│   │   ├── BaseSearch.vue
│   │   ├── BaseTag.vue
│   │   ├── BaseBadge.vue
│   │   ├── BaseCard.vue
│   │   ├── BaseModal.vue
│   │   ├── BaseTable.vue
│   │   ├── BaseEmptyState.vue
│   │   ├── BaseSkeleton.vue
│   │   ├── BaseErrorState.vue
│   │   └── ViewToggle.vue
│   ├── rules/
│   │   ├── RuleCard.vue      # 容器改用 BaseCard
│   │   └── RuleListRow.vue   # 新增
│   ├── l4/
│   │   ├── L4RuleCard.vue    # 容器改用 BaseCard
│   │   └── L4RuleListRow.vue # 新增
│   ├── certs/
│   ├── relay/
│   └── wireguard/
├── composables/
│   └── useViewMode.js        # 新增
├── styles/
│   ├── themes.css            # 仅 token
│   ├── utilities.css         # 仅工具类
│   └── index.css             # 入口 + 第三方覆盖
└── pages/
    ├── RulesPage.vue         # 增加 ViewToggle + BaseTable
    ├── L4RulesPage.vue       # 增加 ViewToggle + BaseTable
    ├── CertsPage.vue         # 增加 ViewToggle + BaseTable
    ├── RelayListenersPage.vue# 增加 ViewToggle + BaseTable
    └── WireGuardProfilesPage.vue # 增加 ViewToggle + BaseTable
```
