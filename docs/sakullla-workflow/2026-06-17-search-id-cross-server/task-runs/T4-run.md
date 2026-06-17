# Task Run: T4

- task_id: T4
- title: "候选列表 UI（防御性）"
- started_at: 2026-06-17
- status: done
- execution_path: local
- execution_reason: "low 风险，自包含 UI 组件 + 3 个页面小幅改动"

## actual_files
- panel/frontend/src/components/IdCandidateModal.vue
- panel/frontend/src/pages/RulesPage.vue
- panel/frontend/src/pages/L4RulesPage.vue
- panel/frontend/src/pages/CertsPage.vue

## increment_log
- IdCandidateModal.vue: 新建，基于 BaseModal，接收 candidates 数组，展示 agentId/type/name，emit select
- 三页面: import findAllMatchesInAgents + IdCandidateModal；cross-agent watch 改用 findAllMatchesInAgents；多匹配时显示 Modal
- 新增 state: candidateModalVisible, candidateModalCandidates, candidateModalId
- 新增 handler: handleCandidateSelect → router.replace

## verify
- commands_run: cd panel/frontend && npm run build && npx vitest run
- evidence: build ✓ (4.40s), 262 tests ✓ (47 files), 0 failures

## review
- mode: self_check
- checks:
  - IdCandidateModal 基于 BaseModal，API 一致 ✓
  - findAllMatchesInAgents 单命中时直接跳转，多命中时弹 Modal ✓
  - Modal select 事件触发正确跳转 ✓
  - 当前 ID 模型全局唯一，多候选不会实际触发 ✓
- verdict: pass
