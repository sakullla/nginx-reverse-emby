# HTTP Diagnostic HEAD Fallback And Per-Backend Latency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make HTTP diagnostics prefer `HEAD` with automatic fallback to `GET` on `405/501`, and make multi-backend HTTP diagnostics collect and present five samples per backend with per-backend latency summaries.

**Architecture:** Extend the Go diagnostic report model to carry per-backend summaries while keeping the existing overall summary and sample list intact. Update the HTTP prober to probe each configured backend independently for a fixed number of attempts, recording the configured backend URL in samples and handling `HEAD` to `GET` fallback within a single probe attempt. Update the Vue diagnostic modal to render the new per-backend section while preserving the existing aggregate summary and sample details.

**Tech Stack:** Go, Vue 3, Vite, TanStack Query

---

### Task 1: Add failing Go tests for HTTP probe behavior

**Files:**
- Modify: `go-agent/internal/diagnostics/http_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestHTTPProberDiagnoseFallsBackToGetWhenHeadIsNotSupported(t *testing.T) {
	// add an httptest server that returns 405 for HEAD and 204 for GET
	// assert Diagnose() succeeds and both methods were observed in order
}

func TestHTTPProberDiagnoseCollectsFiveSamplesPerBackend(t *testing.T) {
	// add two httptest servers as distinct backends
	// assert report summary sent == 10 and each backend summary sent == 5
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diagnostics -run "TestHTTPProberDiagnoseFallsBackToGetWhenHeadIsNotSupported|TestHTTPProberDiagnoseCollectsFiveSamplesPerBackend"`
Expected: FAIL because the prober still uses `GET` directly and the report has no per-backend summaries.

- [ ] **Step 3: Write minimal implementation**

```go
// Update HTTP probe execution to:
// 1. issue HEAD first
// 2. retry the same probe with GET on 405/501
// 3. probe every configured backend attempts-per-backend times
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/diagnostics -run "TestHTTPProberDiagnoseFallsBackToGetWhenHeadIsNotSupported|TestHTTPProberDiagnoseCollectsFiveSamplesPerBackend"`
Expected: PASS

### Task 2: Add failing Go tests for report aggregation and task serialization

**Files:**
- Modify: `go-agent/internal/diagnostics/result_test.go`
- Modify: `go-agent/internal/task/diagnostics_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildReportIncludesPerBackendSummaries(t *testing.T) {
	// build a report from samples for two backends
	// assert report.Backends contains per-backend sent/succeeded/latency summary
}

func TestDiagnosticHandlerReturnsPerBackendResults(t *testing.T) {
	// execute a diagnostic task against a rule with multiple backends
	// assert result["backends"] exists and includes both backend URLs
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/diagnostics ./internal/task -run "TestBuildReportIncludesPerBackendSummaries|TestDiagnosticHandlerReturnsPerBackendResults"`
Expected: FAIL because `Report` does not yet expose per-backend summaries.

- [ ] **Step 3: Write minimal implementation**

```go
type BackendReport struct {
	Backend string  `json:"backend"`
	Summary Summary `json:"summary"`
}

type Report struct {
	Kind     string          `json:"kind"`
	RuleID   int             `json:"rule_id"`
	Summary  Summary         `json:"summary"`
	Backends []BackendReport `json:"backends,omitempty"`
	Samples  []Sample        `json:"samples"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/diagnostics ./internal/task -run "TestBuildReportIncludesPerBackendSummaries|TestDiagnosticHandlerReturnsPerBackendResults"`
Expected: PASS

### Task 3: Update frontend diagnostic rendering for per-backend latency

**Files:**
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`
- Modify: `panel/frontend/src/api/index.js`

- [ ] **Step 1: Write the failing frontend expectation**

```js
// Update the dev mock diagnostic task payload to include result.backends
// and make the modal read and render it as a dedicated section.
```

- [ ] **Step 2: Run build to verify it fails if bindings are incomplete**

Run: `npm run build`
Working directory: `panel/frontend`
Expected: FAIL if template/script fields are referenced before implementation is complete.

- [ ] **Step 3: Write minimal implementation**

```vue
<div v-if="backendSummaries.length" class="diagnostic-modal__backends">
  <!-- render one card per backend with avg/min/max and success counts -->
</div>
```

- [ ] **Step 4: Run build to verify it passes**

Run: `npm run build`
Working directory: `panel/frontend`
Expected: PASS

### Task 4: Verify the full change set

**Files:**
- Modify: `go-agent/internal/diagnostics/http.go`
- Modify: `go-agent/internal/diagnostics/http_test.go`
- Modify: `go-agent/internal/diagnostics/result.go`
- Modify: `go-agent/internal/diagnostics/result_test.go`
- Modify: `go-agent/internal/task/diagnostics.go`
- Modify: `go-agent/internal/task/diagnostics_test.go`
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`
- Modify: `panel/frontend/src/api/index.js`

- [ ] **Step 1: Run targeted Go tests**

Run: `go test ./internal/diagnostics/... ./internal/task/...`
Working directory: `go-agent`
Expected: PASS

- [ ] **Step 2: Run frontend build**

Run: `npm run build`
Working directory: `panel/frontend`
Expected: PASS

- [ ] **Step 3: Run repository verification relevant to this change**

Run: `go test ./...`
Working directory: `go-agent`
Expected: PASS
