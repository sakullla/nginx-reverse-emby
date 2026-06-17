# Task Run: T3

- task_id: T3
- title: "功能页 #id= 跨 agent 解析 + 跳转"
- started_at: 2026-06-17
- status: done
- execution_path: local
- execution_reason: "medium 风险但改动模式一致，三个页面用相同 pattern，本地实现"

## actual_files
- panel/frontend/src/pages/RulesPage.vue
- panel/frontend/src/pages/L4RulesPage.vue
- panel/frontend/src/pages/CertsPage.vue

## increment_log
- RulesPage.vue: add watch(filteredRules) → fetchAllAgentsRules → findRecordInAgents → router.replace
- L4RulesPage.vue: add watch(filteredRules) → fetchAllAgentsL4Rules → findRecordInAgents → router.replace
- CertsPage.vue: add watch(filteredCerts) → fetchAllAgentsCertificates → findRecordInAgents → router.replace
- All three: import { watch } from 'vue', { parseIdQuery, findRecordInAgents } from useIdSearch, fetchAllAgents* from api
- Pattern: _crossSearching guard, empty agentIds early return, .finally() cleanup

## verify
- commands_run: cd panel/frontend && npm run build && npx vitest run
- evidence: build ✓ (4.60s), 262 tests ✓ (47 files), 0 failures

## review
- mode: delegate (self-check performed)
- checks:
  - router.replace preserves existing query params ✓
  - _crossSearching guard prevents concurrent fetches ✓
  - Empty agent list early return ✓
  - parseIdQuery + findRecordInAgents with correct type per page ✓
  - finally() resets guard on error ✓
  - watch(filteredRules) implicitly tracks searchQuery via computed ✓
- verdict: pass
