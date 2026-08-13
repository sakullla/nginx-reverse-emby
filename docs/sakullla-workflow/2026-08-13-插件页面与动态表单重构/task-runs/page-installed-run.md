# Task page-installed

## Attempt History

```yaml
format: task_attempt_history
task_id: page-installed
history_ref: evidence/history/sha256-6368f4fc412186fd7601bc6cd968a12dae0fce0c8ae654238d942ab68ec9dd50.json
history_count: 1
```

## Execution

```yaml
format: task_run
task_id: page-installed
execution:
  # allowed: blocked|completed|completed_with_concerns|needs_context
  outcome: completed_with_concerns
  summary: 将已安装插件列表页统一到共享骨架：页头改用 page-header/page-title/page-subtitle 结构与 page-header__left/right；新增 ResourceListFilterBar 提供搜索（插件名/ID）与生命周期 chip 筛选； 加载态改用 spinner，错误态与空态改用 EmptyState；卡片改用 BaseListCard（外包 RouterLink 保留导航， 生命周期映射为 BaseBadge 状态色）；桌面/移动视口保持响应式网格（移动端单列）。测试扩展至 8 条， 覆盖资源组可见性过滤、页头、加载、错误、空态、搜索、生命周期筛选与筛选字段装配。
  verification_refs: [cd panel/frontend && npx vitest run src/pages/plugins/PluginsPage.test.js]
  concerns:
    - ResourceListFilterBar 的节点（agent）选择器无法隐藏；本页按 actor 可见资源组过滤而非按节点过滤， 故传入空 agents 列表（下拉显示「全部节点」且无候选项）。
    - 搜索与生命周期筛选状态保存在组件本地，未像 Rules/Relay/Certs 等按节点列表页那样经 useListFilterUrl 同步到 URL。
```
