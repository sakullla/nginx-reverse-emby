# 流量统计策略设置与历史管理重新设计

## 背景与目标

Agent 详情页的流量统计标签页中，策略设置和历史管理目前使用简陋的可折叠面板包裹：

- **策略设置**（`TrafficPolicyForm.vue`）是一个两列网格表单，字段拥挤、缺乏分组说明
- **历史管理**（`TrafficHistoryManager.vue`）只有几个孤零零的按钮，没有任何状态展示或操作反馈
- 两者都包裹在 `TrafficCollapsibleSection` 中，需要点击展开，增加了操作步骤

本次重新设计的目标是优化这两个区域的 **UI/UX**，同时统一并调整数据保留策略的默认值。

## 设计范围

- **前端**：`panel/frontend/src/components/traffic/` 和 `AgentDetailPage.vue`
- **后端**：`sqlite_models.go`、`traffic_store.go`、`traffic_service.go` 中的默认值
- **不涉及**：新增后端 API、修改数据模型、修改存储层

## 整体布局

将流量统计标签页从"三个可折叠面板"改为 **纵向信息流**，信息默认全部展开，通过滚动浏览。

页面从上到下分为 5 个区块：

1. **概览卡片组** —— 4 张横向排列卡片（业务流量、主机流量、阻断节点、计费周期）
2. **趋势图** —— 保留 `TrafficTrendChart`，粒度切换器移至图表左上角
3. **分项流量明细** —— 保留 `TrafficBreakdownTable`，增加提示文案
4. **策略设置** —— 分组卡片布局（详见下文）
5. **历史管理** —— 操作卡片布局（详见下文）

区块之间使用统一标题 + `2rem` 间距分隔，去掉 `TrafficCollapsibleSection` 的折叠交互。

## 策略设置：分组卡片布局

`TrafficPolicyForm.vue` 内部从两列网格改为 **分组卡片布局**，按功能将字段分为三个卡片：

### 计费配置卡片
- **统计方向**：both / rx / tx / max（下拉选择）
- **月周期起始日**：1-28（数字输入）
- **月额度**：数值输入 + 单位选择（B/KiB/MiB/GiB/TiB），留空表示无限制

### 数据保留策略卡片
- 卡片顶部说明文案："不同粒度的历史数据采用不同的保留单位，便于直观理解时间跨度"
- 每个保留项独立分组，包含：
  - **标签 + 单位徽章**（如"小时级明细" + 蓝色"单位：天"徽章）
  - **输入框 + 右侧单位文字**（数值与单位分离显示）
  - **换算说明**（如"约 1 个月的小时级流量明细"）

三个保留项：

| 粒度 | 标签 | 单位 | 默认值 | 换算说明 |
|------|------|------|--------|----------|
| 小时级明细 | 小时级明细 | 天 | 30 | 约 1 个月 |
| 日汇总数据 | 日汇总数据 | 月 | 3 | 约 90 天 |
| 月汇总数据 | 月汇总数据 | 月 | 36 | 约 3 年 |

### 高级设置卡片
- **超额阻断**：开关 + 说明文案"超出月额度后停止代理服务"
- **流量统计上报周期**：文本输入，如 "30s"，留空表示随心跳上报

保存按钮放在三个卡片下方，右对齐。

对外接口保持不变：`v-model` + `saving` prop + `@save` 事件。

## 历史管理：操作卡片布局

`TrafficHistoryManager.vue` 内部从按钮列表改为 **操作卡片布局**，分为两个卡片：

### 流量校准卡片
- 标题："流量校准"
- 说明文案："调整当前计费周期的已用流量基准值。校准后系统将从原始统计数据中扣除基准值，用于额度计算。"
- 操作按钮：
  - "校准为指定值" —— 打开 `TrafficCalibrateModal`
  - "从现在归零" —— 直接调用 `@calibrate-zero`

### 数据清理卡片
- 标题："数据清理"
- 说明文案："按保留策略清理过期历史数据。当前策略：小时 {n} 天、日 {n} 个月、月 {n} 个月。"
- 操作按钮：
  - "清理过期数据"（红色样式，危险操作）

对外接口保持不变：`calibrating` / `cleaning` props + `@calibrate` / `@calibrate-zero` / `@cleanup` 事件。
新增 `policy` prop，用于展示当前保留策略摘要（文案中的 `{n}` 从 policy 读取）。

## 校准弹框

新增 `TrafficCalibrateModal.vue` 组件，替代原生的 `window.prompt`。

弹框内容：
- **标题**："校准当前周期已用流量"
- **说明文案**：解释校准的作用
- **信息面板**（只读，灰色背景）：
  - 当前计费周期（如 2026-05-01 ~ 2026-06-01）
  - 当前原始统计值
  - 上次校准基准（未校准时显示"未校准"）
- **输入区域**：
  - 标签："校准后已用流量"
  - 输入框：placeholder "输入流量值，如 1.5 或 1.5 GiB"
  - 单位下拉：B / KiB / MiB / GiB / TiB（默认 GiB）
  - 提示文案："支持直接输入字节数，或带单位如 '1.5 GiB'"
- **操作按钮**：取消（左）+ 确认校准（右，主色）

对外接口：
- Props: `visible`, `agentId`, `currentUsedBytes`, `cycleStart`, `cycleEnd`
- Emits: `update:visible`, `confirm(usedBytes)`

## AgentDetailPage 结构调整

```vue
<section class="traffic-section">
  <h3>概览</h3>
  <TrafficSummaryCards :summary="trafficSummary" ... />
  <div class="traffic-trend">
    <TrafficTrendChart :points="trafficTrendPoints" ... />
  </div>
  <div class="traffic-breakdown">
    <span class="traffic-breakdown-hint">分项流量明细（点击行查看趋势）</span>
    <TrafficBreakdownTable ... />
  </div>
</section>

<section class="traffic-section">
  <h3>策略设置</h3>
  <TrafficPolicyForm v-model="trafficPolicyForm" :saving="..." @save="saveTrafficPolicy" />
</section>

<section class="traffic-section">
  <h3>历史管理</h3>
  <TrafficHistoryManager
    :policy="trafficPolicyForm"
    :calibrating="..."
    :cleaning="..."
    @calibrate="calibrateModalVisible = true"
    @calibrate-zero="calibrateTrafficToZero"
    @cleanup="cleanupTrafficHistory"
  />
</section>

<TrafficCalibrateModal
  v-model:visible="calibrateModalVisible"
  :agent-id="agentId"
  :current-used-bytes="trafficSummary.used_bytes"
  :cycle-start="trafficSummary.cycle_start"
  :cycle-end="trafficSummary.cycle_end"
  @confirm="onCalibrateConfirm"
/>
```

新增 `calibrateModalVisible` ref 控制弹框显隐。`onCalibrateConfirm` 中解析输入值并调用 `calibrateTrafficMutation.mutateAsync({ used_bytes })`。

## 数据流

数据流基本保持不变，所有 hooks 和 mutation 复用现有实现：

- `useTrafficPolicy(agentId)` / `useUpdateTrafficPolicy(agentId)` —— 策略读写
- `useCalibrateTraffic(agentId)` / `useCleanupTraffic(agentId)` —— 历史操作
- `useTrafficSummary(agentId)` —— 获取 summary 用于弹框信息面板
- `trafficPolicyForm` ref —— 表单状态（结构不变）
- `calibrateModalVisible` ref —— 新增，控制弹框

校准流程：
1. 用户点击"校准为指定值"
2. `calibrateModalVisible = true`
3. 弹框内输入值 + 选择单位
4. 确认后将输入值解析为字节数
5. 调用 `calibrateTrafficMutation.mutateAsync({ used_bytes })`
6. 关闭弹框

## 后端变更

共 4 处需要同步调整默认值，统一改为：小时 30 天 / 日 3 个月 / 月 36 个月。

### 1. `sqlite_models.go` —— GORM 标签默认值

```go
// 修改前
HourlyRetentionDays    int    `gorm:"column:hourly_retention_days;not null;default:180"`
DailyRetentionMonths   int    `gorm:"column:daily_retention_months;not null;default:24"`
MonthlyRetentionMonths *int   `gorm:"column:monthly_retention_months"`

// 修改后
HourlyRetentionDays    int    `gorm:"column:hourly_retention_days;not null;default:30"`
DailyRetentionMonths   int    `gorm:"column:daily_retention_months;not null;default:3"`
MonthlyRetentionMonths *int   `gorm:"column:monthly_retention_months;default:36"`
```

### 2. `traffic_store.go` —— `defaultTrafficPolicy`

查询不到记录时返回的默认策略：

```go
// 修改前
func defaultTrafficPolicy(agentID string) AgentTrafficPolicyRow {
    return AgentTrafficPolicyRow{
        AgentID:              agentID,
        Direction:            "both",
        CycleStartDay:        1,
        HourlyRetentionDays:  180,
        DailyRetentionMonths: 24,
    }
}

// 修改后
func defaultTrafficPolicy(agentID string) AgentTrafficPolicyRow {
    defaultMonthly := 36
    return AgentTrafficPolicyRow{
        AgentID:                agentID,
        Direction:              "both",
        CycleStartDay:          1,
        HourlyRetentionDays:    30,
        DailyRetentionMonths:   3,
        MonthlyRetentionMonths: &defaultMonthly,
    }
}
```

### 3. `traffic_store.go` —— `normalizeTrafficPolicyRow`

行数据规范化时的零值兜底：

```go
// 修改前
if row.HourlyRetentionDays == 0 {
    row.HourlyRetentionDays = 180
}
if row.DailyRetentionMonths == 0 {
    row.DailyRetentionMonths = 24
}

// 修改后
if row.HourlyRetentionDays == 0 {
    row.HourlyRetentionDays = 30
}
if row.DailyRetentionMonths == 0 {
    row.DailyRetentionMonths = 3
}
if row.MonthlyRetentionMonths == nil || *row.MonthlyRetentionMonths == 0 {
    defaultMonthly := 36
    row.MonthlyRetentionMonths = &defaultMonthly
}
```

### 4. `traffic_service.go` —— `UpdatePolicy`

创建/更新策略时的默认值：

```go
// 修改前
HourlyRetentionDays:    defaultInt(input.HourlyRetentionDays, 180),
DailyRetentionMonths:   defaultInt(input.DailyRetentionMonths, 24),
MonthlyRetentionMonths: input.MonthlyRetentionMonths,

// 修改后
HourlyRetentionDays:    defaultInt(input.HourlyRetentionDays, 30),
DailyRetentionMonths:   defaultInt(input.DailyRetentionMonths, 3),
MonthlyRetentionMonths: defaultIntPtr(input.MonthlyRetentionMonths, 36),
```

需要新增 `defaultIntPtr` 辅助函数（或等价实现），为 `*int` 类型提供默认值。

## 测试策略

### 前端测试

1. **`TrafficPolicyForm.test.js`**
   - 三个卡片区块正确渲染
   - 数据保留项显示单位标签和换算说明
   - 表单字段变更触发 `update:modelValue`
   - 点击保存触发 `@save`

2. **`TrafficHistoryManager.test.js`**
   - 流量校准和数据清理两个卡片渲染
   - 按钮点击触发对应事件
   - `calibrating` / `cleaning` 状态正确禁用按钮
   - 传入 `policy` 后正确显示保留策略摘要

3. **`TrafficCalibrateModal.test.js`**
   - `visible` prop 控制显隐
   - 当前状态信息正确展示
   - 输入框支持直接输入字节数或带单位的值
   - 确认时发送正确的 `used_bytes`
   - 取消时关闭弹框不触发操作

4. **`AgentDetailPage.test.js`**（补充）
   - 流量统计标签页不再使用 `TrafficCollapsibleSection`
   - `TrafficPolicyForm` / `TrafficHistoryManager` / `TrafficCalibrateModal` 正确挂载
   - 校准按钮点击后弹框显示，确认后调用 mutation

### 后端测试

5. **`traffic_service_test.go`**
   - `UpdatePolicy` 的默认 `HourlyRetentionDays` 从 180 改为 30
   - `UpdatePolicy` 的默认 `DailyRetentionMonths` 从 24 改为 3
   - `UpdatePolicy` 的默认 `MonthlyRetentionMonths` 从 nil 改为 36

## 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `panel/frontend/src/components/traffic/TrafficPolicyForm.vue` | 修改 | 两列网格 → 分组卡片布局 |
| `panel/frontend/src/components/traffic/TrafficHistoryManager.vue` | 修改 | 按钮列表 → 操作卡片布局 |
| `panel/frontend/src/components/traffic/TrafficCalibrateModal.vue` | 新增 | 校准弹框组件 |
| `panel/frontend/src/pages/AgentDetailPage.vue` | 修改 | 去掉折叠面板，改为纵向信息流，集成弹框 |
| `panel/backend-go/internal/controlplane/storage/sqlite_models.go` | 修改 | GORM 标签默认值调整 |
| `panel/backend-go/internal/controlplane/storage/traffic_store.go` | 修改 | `defaultTrafficPolicy` 和 `normalizeTrafficPolicyRow` 默认值调整 |
| `panel/backend-go/internal/controlplane/service/traffic_service.go` | 修改 | `UpdatePolicy` 默认值调整 |
| `panel/backend-go/internal/controlplane/service/traffic_service_test.go` | 修改 | 更新默认值断言 |
| `panel/backend-go/internal/controlplane/storage/traffic_store_test.go` | 修改 | 更新默认值断言（如涉及） |
| `panel/frontend/src/components/traffic/TrafficPolicyForm.test.js` | 新增/修改 | 适配新布局的测试 |
| `panel/frontend/src/components/traffic/TrafficHistoryManager.test.js` | 新增/修改 | 适配新布局的测试 |
| `panel/frontend/src/components/traffic/TrafficCalibrateModal.test.js` | 新增 | 弹框组件测试 |
