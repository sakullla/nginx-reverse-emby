# Relay 页面与诊断 Modal 重新设计

## 概述

将 Relay 监听器页面和规则诊断 Modal 按参考图的视觉风格重新设计，保持现有暗色主题，提升信息密度和可读性。

## 设计决策

| 决策项 | 选择 |
|--------|------|
| 诊断展示形式 | 保持 Modal，内部完全重构 |
| Relay 主视图 | 保持卡片网格 |
| Relay 详情交互 | 手风琴展开（允许多个同时展开） |
| 主题 | 沿用现有暗色主题（CSS 变量系统） |
| 探测样本 | **移除** |
| 后端展开详情 | **移除**（adaptive 指标不再展开，平铺展示） |

---

## 一、诊断 Modal 重新设计

### 1.1 布局结构（从上到下）

```
┌─────────────────────────────────────────┐
│  [eyebrow]  HTTP PATH DIAGNOSIS         │
│  [headline] https://api.example.com     │
│  [subtitle] 后端: 192.168.1.1:8080 +2   │
│                              [状态标签]   │
├─────────────────────────────────────────┤
│  [统计卡片]  总路径数  成功路径  失败路径 │
├─────────────────────────────────────────┤
│  🔄 Relay 路径探测结果                   │
│  ┌───────────────────────────────────┐  │
│  │ 路径 │ 状态 │ 延迟 │ 丢包率 │ 质量 │  │
│  ├───────────────────────────────────┤  │
│  │ ...  │ ...  │ ...  │ ...    │ ...  │  │
│  └───────────────────────────────────┘  │
├─────────────────────────────────────────┤
│  📡 后端探测结果                         │
│  ┌───────────────────────────────────┐  │
│  │ 路径 │ 状态 │ 延迟 │ 持续吞吐 │ 质量│  │
│  ├───────────────────────────────────┤  │
│  │ ...  │ ...  │ ...  │ ...      │ ... │  │
│  └───────────────────────────────────┘  │
└─────────────────────────────────────────┘
```

### 1.2 Hero 区

保留当前结构但简化样式：
- **eyebrow**: `HTTP PATH DIAGNOSIS` / `L4 PATH DIAGNOSIS`
- **headline**: 规则标识（HTTP 为 `frontend_url`，L4 为 `name` 或 `listen_host:port`）
- **subtitle**: 后端地址摘要
- **状态标签**: 成功/失败/诊断中，带语义色 pill

移除复杂的渐变背景、模糊效果和 radial-gradient 装饰。

### 1.3 概览统计区

3 个等宽卡片，基于 **Relay 路径**统计：

| 卡片 | 内容 | 样式 |
|------|------|------|
| 总路径数 | `relay_paths.length` | 语义背景 `var(--color-primary-subtle)` + 主题色文字 |
| 成功路径 | `relay_paths.filter(p => p.success).length` | 语义背景 `var(--color-success-50)` + `var(--color-success)` 文字 |
| 失败路径 | `relay_paths.filter(p => !p.success).length` | 语义背景 `var(--color-danger-50)` + `var(--color-danger)` 文字 |

若 `relay_paths` 为空（后端未返回或旧数据），回退到样本统计：`summary.sent` / `summary.succeeded` / `summary.failed`。

### 1.4 Relay 路径探测结果

按 `relay_paths` 分组展示，每条路径为一个 section。

#### Section 标题格式

```
🔄 路径 N  (listener#1 → listener#4)  [已选中]  [成功]
```

- 路径编号：按 `relay_paths` 数组顺序
- Listener chain：`path.join(' → ')`，格式为 `#id`
- 选中标记：若 `path` 与 `selected_relay_path` 匹配，显示「已选中」pill
- 整体状态：`success ? '成功' : '失败'` pill

#### Hop 表格（每路径一个）

| 列名 | 数据来源 | 说明 |
|------|----------|------|
| 路径 | `hop.from` → `hop.to` | `from_listener_id` 或 `from` 到 `to_listener_id` 或 `to` |
| 状态 | `hop.success` | 成功/失败 pill |
| 延迟 | `hop.latency_ms` | ms，失败时显示「—」 |
| 丢包率 | — | 单跳无丢包率，留「—」 |
| 质量 | 按延迟计算 | 同后端质量分级规则 |

Hop 路径显示规则：
- `from` 为 `"client"` 时显示「入口」
- `to` 为后端地址时显示「出口(backend)`」
- Listener ID 显示为 `#id`

#### 失败路径处理

若某路径 `success == false`：
- section 标题状态为 danger
- 失败的 hop 行高亮
- 若路径有 `error`，在 section 底部显示错误信息

### 1.5 后端探测结果表格

当 `relay_paths` 存在时，后端探测结果作为补充区域展示（折叠状态，默认收起）。

#### HTTP 诊断（6 列）

| 列名 | 数据来源 | 说明 |
|------|----------|------|
| 路径 | `frontend_url` + `backend.backend` | 见 1.6 路径显示规则 |
| 状态 | `backend.summary.succeeded == backend.summary.sent` | 成功/失败 pill |
| 延迟 | `backend.summary.avg_latency_ms` | ms |
| 丢包率 | `backend.summary.loss_rate` | 百分比 |
| 持续吞吐 | `backend.adaptive.sustained_throughput_bps` | 自动转换 B/s / KB/s / MB/s |
| 质量 | `backend.summary.quality` | 优秀/良好/一般/较差/不可用 pill |

#### L4 诊断（5 列）

与 HTTP 相同，但**不显示"持续吞吐"列**。

### 1.6 路径显示规则

后端路径列格式：`<前端标识> → <后端标识> [解析地址]`

- 有域名时优先显示域名，解析 IP 放 `[]` 中
- 纯 IP 直接显示，不加 `[]`
- 纯域名（无 IP）只显示域名

复用现有的 `splitBackendIdentity` 逻辑处理后端地址解析。

### 1.7 移除的内容

- **探测样本列表**（`samples` 数组不再展示）
- **Latency Bar**（min/avg/max 范围条）
- **后端卡片展开详情**（adaptive 指标不再以展开面板形式展示）
- **子后端列表**（`children` 不再展开展示）

### 1.8 失败行高亮

探测失败时，整行背景使用 `var(--color-danger-50)` 轻微高亮，状态列显示 danger pill。

---

## 二、Relay 页面重新设计

### 2.1 卡片改进

现有卡片布局保持不变（header + mapping + meta badges + tags），但简化样式：

- 移除复杂的 hover 效果
- 状态标签改用与诊断一致的 pill 风格
- 配置标签（transport、证书、信任模式等）保持 compact pill
- 新增底部展开提示：「▼ 查看链路拓扑」

### 2.2 链式详情展开

点击「查看链路拓扑」后，在卡片内部下方展开链式详情区域。

> 链式详情展示**配置拓扑**（地址、端口、模式等）。诊断 Modal 中已通过 `relay_paths` / `hops` 展示运行时链路状态。

#### 链路节点（5 个环节）

| 编号 | 节点 | 内容 | 多地址支持 |
|------|------|------|-----------|
| 1 | 绑定地址 | `bind_hosts` + `listen_port` | **是**，列表展示所有绑定地址 |
| 2 | 公网端点 | `public_host` + `public_port` | 否，单条 |
| 3 | 传输配置 | `transport_mode` + `obfs_mode` + `allow_transport_fallback` | 否，pill 标签组 |
| 4 | TLS 信任模式 | `trust_mode_source` / `tls_mode` | 否，单 pill |
| 5 | 证书 | `certificate_id` 或「未绑定证书」 | 否，单 pill |

#### 视觉设计

- 每个节点用带编号的彩色圆点标识（编号 1-5，不同颜色）
- 节点之间用垂直箭头连接
- 多地址节点：圆点旁垂直排列所有地址
- 配置节点：圆点旁水平排列 pill 标签

### 2.3 多展开交互

- 允许多个卡片同时展开链式详情
- 每个卡片的展开状态独立管理
- 展开/折叠动画使用 `max-height` + `opacity` transition（与现有 `slide-expand` 一致）

---

## 三、公共样式与组件

### 3.1 提取的公共模式

| 模式 | 用途 | 实现方式 |
|------|------|----------|
| 语义色 pill | 状态、质量标签 | 统一 CSS class：`.pill--success` `.pill--warning` `.pill--danger` `.pill--info` |
| 统计卡片 | 概览数字展示 | 基础卡片 + 语义背景 modifier |
| 暗色表格 | 数据列表 | 表头下划线 + 行底部边框 + hover 高亮 |

### 3.2 颜色映射（暗色主题）

参考图的颜色语义映射到暗色主题：

| 语义 | 参考图（浅色） | 暗色适配 |
|------|---------------|----------|
| 成功/优秀 | 绿色 | `var(--color-success)` |
| 一般/警告 | 橙色 | `var(--color-warning)` |
| 失败/很差 | 红色 | `var(--color-danger)` |
| 信息 | 蓝色 | `var(--color-primary)` |
| 背景卡片 | 白色 | `var(--color-bg-surface)` |
| 边框 | 浅灰 | `var(--color-border-default)` |

---

## 四、技术实现要点

### 4.1 文件变更清单

| 文件 | 变更类型 | 说明 |
|------|----------|------|
| `RuleDiagnosticModal.vue` | 重写 | 完全重构内部布局和样式 |
| `RelayListenersPage.vue` | 修改 | 卡片样式改进 + 新增链式详情展开 |
| `RelayListenerForm.vue` | 不修改 | 表单保持现有设计 |
| `useDiagnostics.js` | 不修改 | 数据 hook 不变 |
| `useRelayListeners.js` | 不修改 | 数据 hook 不变 |

### 4.2 数据兼容性

- 后端已新增 `relay_paths` / `hops` / `selected_relay_path` 到诊断结果
- 前端读取 `task.result.relay_paths` 展示路径探测结果
- 若 `relay_paths` 为空（旧数据或规则未配置 Relay），回退到原有后端表格展示
- `samples` 和 `children` 数据仍从 API 获取，但前端不再渲染

### 4.3 响应式

- 诊断 Modal 表格：横向滚动（表格容器 `overflow-x: auto`）
- Relay 卡片网格：保持现有 `repeat(auto-fill, minmax(300px, 1fr))`
- 链式详情：节点布局在移动端垂直排列（与桌面端一致）

---

## 五、边界情况

- **诊断中状态**：表格区域显示 loading 占位，不展示表头
- **诊断失败**：Hero 区状态标签为红色，表格区域显示错误信息
- **无后端数据**：表格区域显示「暂无后端数据」空状态
- **Relay 无绑定地址**：节点 1 显示「-」
