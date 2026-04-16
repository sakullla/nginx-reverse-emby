# 规则诊断弹窗 UI/UX 重塑设计

## 背景与目标

- **痛点**：`RuleDiagnosticModal.vue` 中 backend 卡片使用 `auto-fit` grid，adaptive 9 宫格全部平铺，信息密度过高、层级不清。
- **目标**：降低信息密度，建立清晰的信息层级，让用户能一眼定位问题 backend，再按需展开看详细指标。

## 设计原则

1. **纵向阅读顺序**：整体结果 → 各后端健康度 → 已解析候选 → 原始样本。
2. **渐进披露**：默认只展示最关键指标，次要指标折叠。
3. **紧凑但不局促**：统一降级字号、收紧 gap，减少嵌套卡片的视觉重量。

## 页面级细节（RulesPage / L4RulesPage）

- **诊断按钮**：保留脉搏图标，hover 时显示 tooltip `诊断`；hover 颜色改为主色，提升可发现性。
- **规则卡片**：现有布局基本保持，仅做 protocol badge 与 status badge 的垂直居中对齐微调（若存在偏差）。

## 弹窗信息架构（从上到下）

```
Hero 区
  规则名 + endpoint + 节点 + 状态徽章

核心指标（4 格）
  平均延迟 │ 丢包率 │ 成功/总数 │ 链路质量

延迟分布条
  min ───────[avg]────────── max

后端健康度列表（单栏垂直列表）
  ── backend-1 [优选] [良好]
     平均 45ms │ 成功 10/10
     ▶ 自适应摘要：稳定 · 24h稳定性 98% · 置信度 95% · 慢启动 无
     [↓ 展开更多]
       (隐藏区) 延迟 / 评估带宽 / 综合性能 / 异常检测 / 流量阶段
     原因: 综合性能更高
     ── 已解析候选（默认展开）
        candidate-1 [优选] 稳定 · 延迟 42ms · 置信度 94%
        candidate-2         稳定 · 延迟 48ms · 置信度 91%
  ── backend-2 [一般]
     平均 120ms │ 成功 8/10
     ▶ 自适应摘要：恢复中 · 24h稳定性 72% · 已降权 · 慢启动 进行中
     [↓ 展开更多]
       (隐藏区) 延迟 / 评估带宽 / 综合性能 / 异常检测 / 流量阶段

探测样本（默认折叠）
  ▶ 探测样本 (20)
```

## 组件级变更

### 1. Backend 区域：卡片 grid → 垂直列表

- 移除 `.diagnostic-backend-grid` 的 `auto-fit` grid，改为 flex column 列表。
- 每个 backend 项使用 `border-bottom` 分隔，最后一项去边框。
- 项内分为三层：
  1. **Header**：backend 名称 + badge 区（优选、质量）。
  2. **Quick stats**：平均延迟、成功/总数，横向排列。
  3. **Adaptive 摘要**：一行柔和小标签，展示 4 个最关键指标（状态、24h稳定性、样本置信度、慢启动）。
  4. **折叠区**：点击 `[展开更多]` 后显示其余 5 个指标（延迟、评估带宽、综合性能、异常检测、流量阶段）。
  5. **原因文案**：若 `backend.adaptive?.reason` 存在，放在折叠区下方，小号灰色文字。
  6. **已解析候选**：保留在 backend 项内部，默认展开，样式从嵌套卡片改为更紧凑的灰底子列表。

### 2. Adaptive 因子折叠交互

- 每个 backend 项内增加局部状态 `showAdaptiveDetails[backend.backend]`（或对象形式的 ref）。
- 折叠按钮文案：未展开时 `展开更多`，展开后 `收起`。
- 折叠动画：使用 `v-show` + CSS `max-height` 过渡（可选，若实现成本高可只做瞬时切换）。

### 3. 已解析候选样式调整

- 子候选不再使用 `.diagnostic-backend-child` 的独立白底卡片，而是改为：
  - 浅灰背景 `var(--color-bg-subtle)`
  - 圆角 `8px`
  - padding `0.5rem 0.65rem`
  - 内部使用 3~4 列的 mini grid（候选地址、状态、延迟、置信度、优选标记）
- 标题改为 `已解析候选`，字号 `0.72rem`，字重 `600`，颜色 `text-tertiary`。

### 4. 探测样本

- 保持默认折叠，放到弹窗最底部。
- 列表行高和 padding 各减一档：
  - padding：`0.4rem 0.5rem`
  - 行间分隔从 `margin-top: 1px` 改为 `border-bottom: 1px solid var(--color-border-subtle)`
- 整体容器 `max-height` 从 `260px` 降到 `220px`。

### 5. 字号与间距全局收敛

| 元素 | 当前 | 调整后 |
|------|------|--------|
| 弹窗标题 | 1.05rem | 1rem |
| backend 名称 | 0.78rem | 0.75rem |
| 统计值 | 1rem / 0.95rem | 0.95rem / 0.88rem |
| 标签文字 | 0.72rem | 0.68rem |
| 项间距 | 0.65rem | 0.5rem |
| 列表项 padding | 0.85rem 0.95rem | 0.75rem 0.85rem |
| Latency bar 高度 | 6px | 4px |

### 6. 颜色收敛

- Adaptive 摘要区使用灰底 `var(--color-bg-hover)` 圆角块（`border-radius: 8px`），与白底主内容区形成层级区分。
- 加载/错误状态的背景从 `bg-subtle` 改为 `bg-hover`，降低视觉重量。

## 数据与逻辑不变

- 不修改 `useDiagnostics.js` 的数据结构。
- 不修改 API 调用。
- 所有 label 映射函数（`adaptiveStateLabel`、`qualityLabelFor`、`reasonLabel` 等）继续复用。

## 验收标准

- [ ] 弹窗内 backend 区域由 grid 变为垂直列表，信息密度明显降低。
- [ ] 每个 backend 的 adaptive 九宫格默认折叠，只展示精简摘要。
- [ ] 已解析候选保留在各自 backend 内部，默认展开，样式更紧凑。
- [ ] 探测样本位于弹窗最底部，默认折叠，样式更紧凑。
- [ ] 规则列表页诊断按钮 hover 效果增强（tooltip + 主色高亮）。
- [ ] L4 规则页的诊断弹窗同步保持一致（若 L4 使用同一组件则自动生效）。
