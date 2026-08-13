# Task page-detail

## Attempt History

```yaml
format: task_attempt_history
task_id: page-detail
history_ref: evidence/history/sha256-2eb6f855eb2998f89e39d830da309a332695b693021c09a9e704692a8b6f9566.json
history_count: 2
```

## Execution

```yaml
format: task_run
task_id: page-detail
execution:
  # allowed: blocked|completed|completed_with_concerns|needs_context
  outcome: completed_with_concerns
  summary: 修复 page-detail A2 的 P2[blocking] finding：A1 移除了 enable/disable/rollback 的 window.confirm 门控但仅 uninstall 走 DeleteConfirmDialog，留下三个生命周期操作即点即执行。本次为 disable 与 rollback（版本+权限变更）恢复确认门控，复用已导入的 DeleteConfirmDialog（评审指出的 minimal fix）， enable 保持即时（可逆开机、低风险）；将原先仅 uninstall 的对话框统一为单一 DeleteConfirmDialog 实例，由 confirmDialog.action 驱动，confirmCopy computed 按 action 提供 title/message/confirm-text； 新增回归用例锁定行为（enable 即时执行、disable/rollback 弹框且确认后才派发 API 调用，并断言对话框 标题）。指定两文件 7 用例全绿、全量 524 用例全绿、npm run build 通过。
  verification_refs:
    - cd panel/frontend && npx vitest run src/pages/plugins/PluginDetailPage.test.js src/pages/plugins/PluginDetailPage.integration.test.js
    - cd panel/frontend && npx vitest run
    - cd panel/frontend && npm run build
  concerns:
    - 方案 D1 的「通用确认态」未新建；复用 DeleteConfirmDialog，其硬编码「此操作不可撤销，请谨慎操作」 警告对可逆的 disable/rollback 语义不精确。专用通用 ConfirmDialog（或让 DeleteConfirmDialog 警告 可配置）是更干净的后续项，但超出本任务 3 文件范围，故未新建。
    - PluginConfigForm.vue 仍孤立（单一渲染器切换后仅被自身测试引用），对应评审 P3；属延迟清理， 超出本任务范围。
```
