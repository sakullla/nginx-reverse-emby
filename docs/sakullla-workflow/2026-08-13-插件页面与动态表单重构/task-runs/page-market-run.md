# Task page-market

## Attempt History

```yaml
format: task_attempt_history
task_id: page-market
history_ref: evidence/history/sha256-e69edca9b8b6e55d9d9b34ddd370ed98cc00ee10f8bd334bceba725cad6d9de5.json
history_count: 1
```

## Execution

```yaml
format: task_run
task_id: page-market
execution:
  # allowed: blocked|completed|completed_with_concerns|needs_context
  outcome: completed
  summary: 市场页统一到共享骨架，移除行内 checkbox 同意门控改为确认弹窗：安装/升级打开 BaseModal 确认，列出所请求权限与第三方源风险确认，确认后提交相同载荷形态（confirmed_permissions=排序后的请求权限，risk_accepted 仅第三方源为 true）。加载/错误/空态由裸 p 替换为 spinner（加载）与 EmptyState（加载错误/空市场/空详情），新增 page-header（page-title/page-subtitle/page-header__left/right）与返回 /plugins 的 back-link。布局保留双栏 workspace 网格，新增移动端（max-width:800px）单列回退。测试从 1 条扩展至 7 条，覆盖页头/返回导航、加载、错误、空态、安装确认、升级确认、已安装禁用。npm run build 通过。
  verification_refs:
    - cd panel/frontend && npx vitest run src/pages/plugins/PluginMarketplacePage.test.js
    - cd panel/frontend && npm run build
  concerns: []
```
