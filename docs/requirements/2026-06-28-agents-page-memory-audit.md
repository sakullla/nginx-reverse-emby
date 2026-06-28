# 需求文档：Agents 监控页内存泄漏审计与修复

日期：2026-06-28
确认状态：已确认

## 0. 原始材料与需求锚点

- source_materials：
  - source_type：chat
  - source_ref：当前 Claude Code 会话
  - local_ref：无
  - captured_at：2026-06-28
  - content_scope：用户在完成 monitor stream 与列表渲染优化后，提出“解决可能存在的内存泄露以及优化内存占用”，经澄清确认聚焦 Agents 监控页前端代码审计。
- requirement_anchors：
  - R1：对 Agents 监控页进行预防性内存泄漏审计；来源：用户确认；状态：confirmed
  - R2：修复组件卸载、watch/computed、流连接、定时器等高风险泄漏模式；来源：用户确认；状态：confirmed
  - R3：以代码审计和高风险模式修复为验收标准，不承诺具体内存数值；来源：用户确认；状态：confirmed
- 原始材料偏差说明：用户原话“解决可能存在的内存泄露以及优化内存占用”范围较宽，经三轮澄清收窄为“Agents 监控页前端预防性审计”，不包含后端 Go 全链路审计和长期内存监控指标建设。

## 1. 问题陈述

- 问题句：我们要怎样确保 Agents 监控页在长时间运行和频繁流数据更新下不会出现内存泄漏？
- 当前问题：近期对 Agents 页做了 monitor stream 启用策略、数据量化、综合排序和列表渲染优化，但尚未系统检查这些改动是否引入了未清理的监听器、流连接、定时器或 computed 引用。
- 受影响用户 / 角色：长期使用面板监控页的管理员/运维人员。
- 为什么现在要做：流式数据场景是内存泄漏高发区，越早审计越能避免线上长时运行后浏览器 tab 内存持续增长。
- 现有替代方案 / 当前做法：无系统性内存审计，仅依赖功能测试。

## 2. 推荐方向

- 推荐方向：对 Agents 监控页相关前端代码进行静态审计 + 针对性修复。
- 选择理由：范围可控、风险低、与近期优化直接相关、不需要额外测量基础设施。
- 备选方向与取舍：
  - 方向 A：先复现再定位（需要实际 heap profile 证据，周期不可控）。
  - 方向 B：前后端全链路审计（范围过大，与当前优化上下文脱节）。
- 不选其它方向的原因：用户已明确选择“先针对 Agents 监控页预防性审计”和“以代码审计修复为准”。

## 3. 目标与成功标准

- 目标：消除 Agents 监控页已知的内存泄漏风险点，降低长时运行内存占用不确定性。
- 成功标准：审计发现并修复所有可识别的高风险泄漏模式。
- 验收标准：
  - 所有 watch/effect 在组件卸载或作用域销毁时正确清理。
  - stream 连接在视图切换、页面离开或 enabled=false 时正确 abort 并清理 reconnect 定时器。
  - 无未清理的 setInterval/setTimeout。
  - 无全局事件监听、ResizeObserver、IntersectionObserver 等未卸载。
  - 相关单元测试通过，`npm run build` 成功。
- 验收与需求锚点映射：
  - R1：完成 Agents 监控页代码审计。
  - R2：修复高风险泄漏模式并通过新增/补充测试覆盖。
  - R3：不以具体内存数值作为验收条件。
- 度量 / 可观察结果：代码审计清单 + 修复 PR + 测试绿 + 构建成功。

## 4. 范围

- 最小可交付范围 / 本次包含：
  - `panel/frontend/src/pages/AgentsPage.vue`
  - `panel/frontend/src/hooks/useAgentMonitorStream.js`
  - `panel/frontend/src/hooks/useAgentFilters.js`
  - `panel/frontend/src/components/AgentMonitorCard.vue`
  - 相关工具函数与上下文（如 `agentMonitor.js`）
  - 新增/补充单元测试
- 不包含：
  - 后端 Go 控制面内存审计
  - go-agent 内存审计
  - 全局内存监控指标体系建设
  - 性能基准或 heap snapshot 自动化
- 关键约束：改动必须是修复泄漏或降低内存风险，不做无关重构。
- 依赖与前置条件：现有 Agents 页优化代码已合入当前分支。

## 5. 业务规则与交互要求

- 核心业务能力：稳定展示 Agent 监控卡片与列表，不因为长时运行导致内存异常增长。
- 关键业务规则：流连接生命周期必须与视图状态严格绑定；组件卸载时必须释放所有外部资源。
- 关键数据 / 信息：monitor stream 连接、watchers、定时器、全局监听器。
- 用户操作与反馈：用户切换 list/monitor 视图、离开页面、登出时，流应正确断开。
- 权限与数据范围：不涉及权限变更。
- 状态 / 流程变化：视图切换时 stream enabled 状态变化已存在，需确保无泄漏。
- 审计、导入导出、历史记录等特殊要求：无。

## 6. 边界与异常

- 异常场景：
  - 网络抖动导致 stream 频繁重连：需确保旧 controller 和定时器已清理。
  - 用户快速切换视图：需避免多个 stream 并发。
- 边界场景：
  - 无 agent 数据时流不应异常持有资源。
  - 登录态失效时流应自动停止。
- 空数据 / 重复数据 / 无权限：不涉及。
- 兼容与历史数据：保持现有 API 和行为不变。

## 7. 假设与验证

- 当前假设：
  - 泄漏风险主要集中在前端 Agents 监控相关代码。
  - 通过静态审计可以发现并修复高风险模式。
- 关键假设待验证：
  - [ ] 假设：当前代码存在可识别的内存泄漏风险点；验证方式：代码审计。
- 风险：可能审计后未发现明显泄漏点，导致交付物为“已审计，无修复”。
- 待补齐：无。

## 8. 不做事项

- 后端 Go 控制面内存审计：超出本次前端聚焦范围。
- go-agent 内存审计：同上。
- 建立内存回归测试 / 监控指标：用户未选择该验收标准。
- 承诺具体内存数值下降：缺乏基线数据，无法承诺。

## 9. 交接给研发

- 需求摘要：对 Agents 监控页前端代码做内存泄漏预防性审计，重点修复 watch/computed、流连接、定时器、全局监听器等高风险模式，补充测试并确保构建成功。
- 原始材料 refs：当前 Claude Code 会话。
- 需求锚点 refs：R1 / R2 / R3。
- 代码探索关注点：
  - `useAgentMonitorStream` 的 `AbortController`、`reconnectTimer`、`onScopeDispose`。
  - `AgentsPage` 中 `useAgentFilters`、`watch(view, ...)` 的清理。
  - `AgentMonitorCard` 中是否使用了全局 observer 或事件监听。
  - `agentMonitor.js` 中 parser 和 buffer 的持有情况。
- 技术方案待确认：
  - 是否需要为 stream hook 增加显式测试覆盖快速启停场景？
  - 是否需要在 `useAgentFilters` 中显式清理 closure 引用？
- 任务清单：
  - [ ] 静态审计 Agents 页相关代码
  - [ ] 识别并修复泄漏风险点
  - [ ] 补充单元测试
  - [ ] 运行 `npm test -- --run`
  - [ ] 运行 `npm run build`
- 交付核验提示：回看 R2 中列出的高风险模式是否全部覆盖；确认 stream 在视图切换时完全断开。
- 本轮追问：无。
