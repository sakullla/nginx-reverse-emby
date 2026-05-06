# 快速节点选择器设计文档

## 背景

当前节点选择器位于 `TopBar.vue` 右上角，是一个下拉菜单。用户在 HTTP 规则、L4 规则、证书、中继监听等页面间切换时，需要反复点击右上角展开下拉选择节点，操作路径较长，不够便捷。

## 目标

在需要选择节点的页面顶部，增加横向 Chip/Tag 形式的快速节点选择器，替代右上角下拉选择器，实现一键切换节点。

## 设计方案

### 1. 组件设计

新建 `panel/frontend/src/components/QuickAgentSelect.vue`：

**Props：**
| 属性 | 类型 | 说明 |
|------|------|------|
| `agentId` | `string \| null` | 当前选中的 agent ID |
| `agents` | `Agent[]` | 可用 agent 列表 |

**Emits：**
| 事件 | 参数 | 说明 |
|------|------|------|
| `update:agentId` | `string \| null` | 用户选择新 agent 时触发 |

**内部逻辑：**
1. 从 `localStorage` 读取最近使用的 agent ID 列表（key: `nre_recent_agent_ids`）
2. 合并排序：
   - 当前选中的 agent 固定排在第一位（如果存在且在线）
   - 最近使用列表中的 agent 按使用频率排序（越近越靠前）
   - 未在最近使用列表中的 agent 按名称字母序排在后面
3. 截取前 5 个作为 chip 展示，其余放入 "+N 更多" 下拉
4. 切换时更新 `localStorage` 中的最近使用记录（插入头部，保留最多 20 个）

**Chip 样式（使用 CSS 变量适配主题）：**
- 默认态：`background: var(--color-surface-elevated)`，`border: 1px solid var(--color-border)`，圆角 pill
- 选中态：`background: var(--color-primary)`，`color: #fff`
- 每个 chip 左侧带状态圆点：在线绿色 `#22c55e`，离线灰色 `#9ca3af`
- 文本过长时截断，hover 显示完整名称 tooltip

**"+N 更多" 下拉：**
- 点击展开小型下拉面板
- 支持下拉内搜索过滤
- 显示全部节点（含离线），每项显示状态圆点 + 名称
- 面板外点击关闭

### 2. 数据流与状态管理

**最近使用记录存储：**
```js
// localStorage key: 'nre_recent_agent_ids'
// 格式: ['local', 'agent-01', 'agent-03', ...]  (按最近使用倒序，最多 20 个)
```

**读取时排序逻辑：**
1. 从 `localStorage` 读取最近使用列表
2. 过滤掉已不存在的 agent
3. 当前选中的 agent 置顶（如果存在）
4. 最近使用列表中的其他 agent 保持顺序排在前面
5. 其余 agent 按名称字母序排在后面

**切换时更新逻辑：**
1. 用户点击 chip → emit `update:agentId`
2. 父组件（页面）接收新 ID，更新 URL query 参数（`agentId=xxx`）
3. `AgentContext` 同步更新 `selectedAgentId` 和 `localStorage`
4. `QuickAgentSelect` 将新 ID 插入最近使用列表头部，截断保留最多 20 个

**AgentContext 扩展：**
- 在 `AgentContext.js` 中新增 `recordAgentUsage(id)` 辅助函数
- 该函数管理 `localStorage` 中的最近使用列表读写
- `QuickAgentSelect` 通过 `useAgent()` 消费此函数

### 3. 页面集成

每个页面在标题区域下方添加 `QuickAgentSelect`，保持统一的垂直间距。

**集成模式（以 RulesPage 为例）：**
```vue
<template>
  <div class="page-container">
    <div class="page-header">
      <h1>HTTP 规则</h1>
      <div class="header-actions">
        <button @click="showCreateModal">新建规则</button>
      </div>
    </div>

    <QuickAgentSelect
      :agentId="agentId"
      :agents="agents"
      @update:agentId="onAgentChange"
    />

    <div v-if="!agentId" class="empty-state">...</div>
    <div v-else class="rules-grid">...</div>
  </div>
</template>
```

**需要集成的页面：**
| 页面 | 文件路径 |
|------|----------|
| Dashboard | `panel/frontend/src/pages/DashboardPage.vue` |
| HTTP 规则 | `panel/frontend/src/pages/RulesPage.vue` |
| L4 规则 | `panel/frontend/src/pages/L4RulesPage.vue` |
| 证书 | `panel/frontend/src/pages/CertsPage.vue` |
| 中继监听 | `panel/frontend/src/pages/RelayListenersPage.vue` |
| 版本管理 | `panel/frontend/src/pages/VersionsPage.vue` |

**空态处理：**
- 原先无 `agentId` 时显示 `AgentPicker` 大卡片提示用户选择
- 新设计下，`QuickAgentSelect` 始终可见，无 `agentId` 时所有 chip 为未选中态
- 页面内容区保持原有空态逻辑

### 4. TopBar 修改

在 `TopBar.vue` 中，**移除** agent 下拉切换 UI：
- 删除触发按钮（状态圆点 + agent 名称）
- 删除下拉面板（搜索、过滤、排序、列表）
- 删除 `effectiveAgentId` 相关的显示逻辑

TopBar 右侧保留：
- 用户头像/下拉（个人设置、登出）
- 全局搜索按钮
- 主题切换按钮

注意：`useAgent()` 仍然通过 TopBar 响应路由变化，`effectiveAgentId` 计算逻辑保留（用于从 agent-detail 页跳回时同步状态），只是不再渲染为 UI。

### 5. 错误处理与边界情况

| 场景 | 处理方案 |
|------|----------|
| 无可用 agent | 显示"暂无可用节点"提示，chip 区域隐藏 |
| 当前选中 agent 离线 | chip 保持选中态，状态圆点变灰 |
| 当前选中 agent 被删除 | 自动清除选择，回退到第一个可用 agent |
| localStorage 损坏 | try-catch 捕获，降级为空数组 |
| 最近使用列表中的 agent 已不存在 | 渲染时过滤 |
| agent 名称过长 | 文本截断，max-width 限制，hover tooltip |
| 快速切换时请求冲突 | 新请求覆盖旧请求（页面已有 loading 态） |

### 6. 移动端适配

- Chip 区域横向排列，允许折行（`flex-wrap: wrap`）
- 仍最多显示 5 个 chip，"+N 更多"在末尾
- Chip 内边距和字体适当缩小
- 下拉面板全宽显示
- 点击热区不小于 44×44px

## 涉及文件清单

### 新增
- `panel/frontend/src/components/QuickAgentSelect.vue`

### 修改
- `panel/frontend/src/components/layout/TopBar.vue`（移除右上角选择器 UI）
- `panel/frontend/src/context/AgentContext.js`（新增 `recordAgentUsage` 函数）
- `panel/frontend/src/pages/DashboardPage.vue`
- `panel/frontend/src/pages/RulesPage.vue`
- `panel/frontend/src/pages/L4RulesPage.vue`
- `panel/frontend/src/pages/CertsPage.vue`
- `panel/frontend/src/pages/RelayListenersPage.vue`
- `panel/frontend/src/pages/VersionsPage.vue`

## 验收标准

- [ ] 在 HTTP 规则、L4 规则、证书、中继监听、Dashboard、版本管理页面顶部可见 chip 形式节点选择器
- [ ] 点击 chip 可快速切换节点，页面内容随之刷新
- [ ] 最多显示 5 个 chip，超出显示 "+N 更多" 按钮
- [ ] 点击 "+N 更多" 展开下拉，可选择其余节点
- [ ] chip 按最近使用优先排序
- [ ] 选中的 chip 有高亮样式，未选中的为默认样式
- [ ] 每个 chip 左侧显示在线/离线状态圆点
- [ ] 右上角原节点选择器已移除
- [ ] 移动端体验正常，可触摸操作
- [ ] 主题切换（明暗模式）下样式正确
