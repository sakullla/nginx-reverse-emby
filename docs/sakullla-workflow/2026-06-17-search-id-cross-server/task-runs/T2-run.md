# Task Run: T2

- task_id: T2
- title: "全局搜索 #id= 识别与精确过滤"
- started_at: 2026-06-17
- completed_at: 2026-06-17
- status: done
- execution_path: local
- execution_reason: "low 风险，单文件改动，本地实现"

## actual_files
- panel/frontend/src/components/GlobalSearch.vue (modified: added import parseIdQuery, added #id= branch in doSearch)

## increment_log
- Import parseIdQuery from useIdSearch
- In doSearch, after fetchAllAgents* data fetched: added #id= exact match branch
  - Checks parseIdQuery(val) before keyword search
  - On match: iterates all agent data, filters by id, builds result groups
  - Early return prevents keyword search from running
- 262 tests pass (including T1's 19 useIdSearch tests), build succeeds

## verify
- commands_run: "cd panel/frontend && npx vitest run && npm run build"
- evidence: 47 test files, 262 tests passed; build succeeded in 4.24s
