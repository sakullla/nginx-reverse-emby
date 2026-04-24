# Parallel Relay Fanout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add layered parallel Relay fanout with adaptive observation-based path selection and per-hop diagnostic latency reporting.

**Architecture:** Keep the existing Relay wire protocol single-path and implement fanout in the agent/runtime selection layer. Persist and propagate a new `relay_layers` JSON field while keeping `relay_chain` compatibility. Extend diagnostics with optional `relay_paths` and `selected_relay_path` fields so existing consumers continue to work.

**Tech Stack:** Go control plane under `panel/backend-go`, Go execution agent under `go-agent`, Vue 3/Vite frontend under `panel/frontend`, SQLite-backed storage, existing `backends.Cache` adaptive observation model.

---

## File Structure

- `go-agent/internal/model/http.go` and `go-agent/internal/model/l4.go`: add `RelayLayers [][]int` to rule models.
- `go-agent/internal/runtime/runtime.go`: deep-copy `RelayLayers` and include relay layer changes in runtime change detection.
- `go-agent/internal/relayplan/layers.go`: normalize `relay_chain`/`relay_layers`, expand bounded paths, build stable observation keys.
- `go-agent/internal/relayplan/racer.go`: race-first-success path dialer with adaptive ordering and cancellation-safe observations.
- `go-agent/internal/proxy/server.go` and `go-agent/internal/app/local_runtime.go`: use layered path racing for HTTP and L4 relay dials.
- `go-agent/internal/diagnostics/result.go`, `go-agent/internal/diagnostics/http.go`, `go-agent/internal/diagnostics/l4tcp.go`: emit relay path and hop diagnostic reports.
- `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: add `relay_layers` schema/persistence.
- `panel/backend-go/internal/controlplane/http/router.go`: parse and render `relay_layers`.
- `panel/backend-go/internal/controlplane/localagent/runtime.go`: propagate nested relay layers into local agent snapshots.
- `panel/frontend/src/api/runtime.js` and `panel/frontend/src/api/devMocks/data.js`: preserve `relay_layers` and mock diagnostic relay paths.
- `panel/frontend/src/components/common/RelayChainInput.vue`: evolve single-chain editor into layered Relay editor.
- HTTP/L4 rule form components and diagnostic modal component found by `rg "diagnosticTask|samples|backends" panel/frontend/src/components panel/frontend/src/pages`: bind `relay_layers` and show hop latency.

---

### Task 1: Add Agent RelayLayers Models

**Files:**
- Modify: `go-agent/internal/model/http.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Test: `go-agent/internal/runtime/runtime_test.go`

- [ ] **Step 1: Write the failing clone test**

Add near existing snapshot clone tests:

```go
func TestSnapshotCloneDeepCopiesRelayLayers(t *testing.T) {
	snap := Snapshot{
		Rules: []model.HTTPRule{{ID: 1, RelayLayers: [][]int{{1, 2}, {3}}}},
		L4Rules: []model.L4Rule{{ID: 2, RelayLayers: [][]int{{4}, {5, 6}}}},
	}

	current := cloneSnapshot(snap)
	snap.Rules[0].RelayLayers[0][0] = 99
	snap.L4Rules[0].RelayLayers[1][0] = 88

	if got := current.Rules[0].RelayLayers[0][0]; got != 1 {
		t.Fatalf("http relay_layers leaked mutation: got %d", got)
	}
	if got := current.L4Rules[0].RelayLayers[1][0]; got != 5 {
		t.Fatalf("l4 relay_layers leaked mutation: got %d", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/runtime -run TestSnapshotCloneDeepCopiesRelayLayers -count=1`

Expected: FAIL because `RelayLayers` fields do not exist.

- [ ] **Step 3: Write minimal implementation**

Add to both HTTP and L4 rule structs:

```go
RelayLayers [][]int `json:"relay_layers,omitempty"`
```

Add and use this helper in `cloneSnapshot`:

```go
func cloneRelayLayers(layers [][]int) [][]int {
	if layers == nil {
		return nil
	}
	cloned := make([][]int, len(layers))
	for i, layer := range layers {
		cloned[i] = append([]int(nil), layer...)
	}
	return cloned
}
```

Use:

```go
cloned.Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
cloned.L4Rules[i].RelayLayers = cloneRelayLayers(rule.RelayLayers)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd go-agent && go test ./internal/runtime -run TestSnapshotCloneDeepCopiesRelayLayers -count=1`

Expected: PASS.

---

### Task 2: Normalize And Expand Relay Layers

**Files:**
- Create: `go-agent/internal/relayplan/layers.go`
- Test: `go-agent/internal/relayplan/layers_test.go`

- [ ] **Step 1: Write the failing tests**

Create `go-agent/internal/relayplan/layers_test.go`:

```go
package relayplan

import (
	"reflect"
	"testing"
)

func TestNormalizeLayersPrefersRelayLayers(t *testing.T) {
	got := NormalizeLayers([]int{9}, [][]int{{1, 2}, {3}})
	want := [][]int{{1, 2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLayers() = %#v, want %#v", got, want)
	}
}

func TestNormalizeLayersConvertsRelayChain(t *testing.T) {
	got := NormalizeLayers([]int{1, 2, 3}, nil)
	want := [][]int{{1}, {2}, {3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLayers() = %#v, want %#v", got, want)
	}
}

func TestExpandPathsBuildsCartesianProduct(t *testing.T) {
	got, err := ExpandPaths([][]int{{1, 2}, {3, 4}}, 8)
	if err != nil {
		t.Fatalf("ExpandPaths() error = %v", err)
	}
	want := [][]int{{1, 3}, {1, 4}, {2, 3}, {2, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandPaths() = %#v, want %#v", got, want)
	}
}

func TestExpandPathsRejectsDuplicateWithinPath(t *testing.T) {
	_, err := ExpandPaths([][]int{{1, 2}, {1}}, 32)
	if err == nil {
		t.Fatal("ExpandPaths() error = nil, want duplicate listener error")
	}
}

func TestExpandPathsHonorsMaximum(t *testing.T) {
	_, err := ExpandPaths([][]int{{1, 2, 3}, {4, 5, 6}}, 8)
	if err == nil {
		t.Fatal("ExpandPaths() error = nil, want maximum path error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go-agent && go test ./internal/relayplan -run 'TestNormalizeLayers|TestExpandPaths' -count=1`

Expected: FAIL because package/functions do not exist.

- [ ] **Step 3: Write minimal implementation**

Create `go-agent/internal/relayplan/layers.go`:

```go
package relayplan

import (
	"fmt"
	"strconv"
	"strings"
)

func NormalizeLayers(chain []int, layers [][]int) [][]int {
	if len(layers) > 0 {
		return cloneLayers(layers)
	}
	if len(chain) == 0 {
		return nil
	}
	out := make([][]int, 0, len(chain))
	for _, id := range chain {
		out = append(out, []int{id})
	}
	return out
}

func ExpandPaths(layers [][]int, maxPaths int) ([][]int, error) {
	if len(layers) == 0 {
		return nil, nil
	}
	if maxPaths <= 0 {
		return nil, fmt.Errorf("max relay paths must be positive")
	}
	paths := [][]int{{}}
	for layerIndex, layer := range layers {
		if len(layer) == 0 {
			return nil, fmt.Errorf("relay layer %d is empty", layerIndex)
		}
		next := make([][]int, 0, len(paths)*len(layer))
		for _, path := range paths {
			for _, id := range layer {
				candidate := append(append([]int(nil), path...), id)
				if hasDuplicate(candidate) {
					return nil, fmt.Errorf("relay path contains duplicate listener id %d", id)
				}
				next = append(next, candidate)
				if len(next) > maxPaths {
					return nil, fmt.Errorf("relay paths exceed maximum %d", maxPaths)
				}
			}
		}
		paths = next
	}
	return paths, nil
}

func PathKey(prefix string, path []int, target string) string {
	parts := make([]string, 0, len(path))
	for _, id := range path {
		parts = append(parts, strconv.Itoa(id))
	}
	return prefix + "|" + strings.Join(parts, "-") + "|" + strings.TrimSpace(target)
}

func cloneLayers(layers [][]int) [][]int {
	out := make([][]int, len(layers))
	for i, layer := range layers {
		out[i] = append([]int(nil), layer...)
	}
	return out
}

func hasDuplicate(path []int) bool {
	seen := make(map[int]struct{}, len(path))
	for _, id := range path {
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/relayplan -count=1`

Expected: PASS.

---

### Task 3: Persist RelayLayers In Control Plane

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Modify: control-plane service rule type file located by `rg "type HTTPRule|type L4Rule" panel/backend-go/internal/controlplane`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write failing storage tests**

Add HTTP and L4 round-trip tests that create rules with `RelayLayers: [][]int{{1, 2}, {4, 5}}`, list them, and assert `reflect.DeepEqual` on `RelayLayers`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run RelayLayers -count=1`

Expected: FAIL because `RelayLayers` does not exist or is not persisted.

- [ ] **Step 3: Implement storage support**

Add `RelayLayers [][]int` to HTTP/L4 control-plane rule types, add `relay_layers` JSON column with default `'[]'`, and marshal/unmarshal it using the same helper pattern as `relay_chain`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run RelayLayers -count=1`

Expected: PASS.

---

### Task 4: Propagate RelayLayers Through API And Snapshots

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`
- Modify: `panel/backend-go/internal/controlplane/localagent/runtime.go`
- Test: `panel/backend-go/internal/controlplane/localagent/runtime_test.go`

- [ ] **Step 1: Write failing API and local snapshot tests**

Add tests asserting create/update accepts and returns `"relay_layers":[[1,2],[3]]`, keeps `"relay_chain"`, and local agent snapshots contain the nested layers.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel/backend-go && go test ./internal/controlplane/http ./internal/controlplane/localagent -run RelayLayers -count=1`

Expected: FAIL because API/snapshot propagation is missing.

- [ ] **Step 3: Implement propagation**

Thread `RelayLayers` through request decoding, validation, service calls, JSON responses, and local snapshot construction. Preserve behavior when only `relay_chain` is submitted.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/http ./internal/controlplane/localagent -run RelayLayers -count=1`

Expected: PASS.

---

### Task 5: Build Relay Path Racer

**Files:**
- Create: `go-agent/internal/relayplan/racer.go`
- Test: `go-agent/internal/relayplan/racer_test.go`

- [ ] **Step 1: Write failing racer tests**

Create tests for: fastest successful path wins, slower successful losers are canceled/closed, all failures return an aggregate error, and canceled losers are not counted as network failures.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go-agent && go test ./internal/relayplan -run Racer -count=1`

Expected: FAIL because `Racer` does not exist.

- [ ] **Step 3: Implement racer interfaces**

Implement `Path`, `Request`, `Result`, `Attempt`, `Dialer`, and `Racer`. `Race` launches at most `Concurrency` attempts, returns the first successful connection, cancels losers, closes successful loser connections, and returns aggregate errors when every path fails.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/relayplan -count=1`

Expected: PASS.

---

### Task 6: Add Adaptive Path Ordering

**Files:**
- Modify: `go-agent/internal/relayplan/racer.go`
- Test: `go-agent/internal/relayplan/racer_test.go`

- [ ] **Step 1: Write failing adaptive ordering test**

Seed `backends.Cache` so path `[2]` has better observed latency than `[1]`, run `Racer` with `Concurrency: 1`, and assert `[2]` is dialed first.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relayplan -run TestRacerOrdersPathsByAdaptiveObservations -count=1`

Expected: FAIL because racer preserves input order.

- [ ] **Step 3: Implement adaptive ordering**

Project paths to `backends.Candidate{Address: path.Key}` under a stable scope such as `relay_path|<target>`, then order with the existing adaptive model. Keep cold paths in the candidate list for exploration.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd go-agent && go test ./internal/relayplan -run TestRacerOrdersPathsByAdaptiveObservations -count=1`

Expected: PASS.

---

### Task 7: Wire Racer Into HTTP And L4 Runtime

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Test: `go-agent/internal/proxy/server_test.go`
- Test: `go-agent/internal/app/local_runtime_test.go`

- [ ] **Step 1: Write failing integration tests**

Add HTTP and L4 tests with `RelayLayers: [][]int{{1, 2}}`, a fake relay dialer where listener 2 succeeds first, and assert traffic uses the selected listener 2 path once.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go-agent && go test ./internal/proxy ./internal/app -run RelayLayers -count=1`

Expected: FAIL because runtime ignores `RelayLayers`.

- [ ] **Step 3: Implement runtime wiring**

Where `rule.RelayChain` currently builds relay hops and calls `relay.DialWithResult`, normalize layers with `relayplan.NormalizeLayers(rule.RelayChain, rule.RelayLayers)`, expand paths, resolve each path to `[]relay.Hop`, then call `relayplan.Racer.Race`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/proxy ./internal/app -run RelayLayers -count=1`

Expected: PASS.

---

### Task 8: Add Diagnostic Relay Path Reports

**Files:**
- Modify: `go-agent/internal/diagnostics/result.go`
- Modify: `go-agent/internal/diagnostics/http.go`
- Modify: `go-agent/internal/diagnostics/l4tcp.go`
- Test: `go-agent/internal/diagnostics/http_test.go`
- Test: `go-agent/internal/diagnostics/l4tcp_test.go`

- [ ] **Step 1: Write failing diagnostic report tests**

Add tests that marshal reports and assert JSON includes `relay_paths[0].hops[0].latency_ms` and `selected_relay_path`. Add HTTP and L4 diagnose tests that produce layered relay results.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go-agent && go test ./internal/diagnostics -run RelayPath -count=1`

Expected: FAIL because report fields do not exist.

- [ ] **Step 3: Implement report fields**

Add `RelayHopReport`, `RelayPathReport`, `Report.RelayPaths`, and `Report.SelectedRelayPath`. Populate path total latency and available hop latency. If exact intermediate hop timing is not available from the current protocol, keep intermediate hop timing omitted and only report measured client-side hop/path timings.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/diagnostics -run RelayPath -count=1`

Expected: PASS.

---

### Task 9: Update Frontend Payloads And Forms

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Modify: `panel/frontend/src/components/common/RelayChainInput.vue`
- Modify: HTTP/L4 rule form components located by `rg "RelayChainInput|relay_chain" panel/frontend/src/components panel/frontend/src/pages`
- Test: `panel/frontend/src/components/relayObfsForm.test.mjs`

- [ ] **Step 1: Write failing frontend payload test**

Add assertions that `relay_layers: [[1,2],[3]]` survives rule normalization and save payload creation.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/frontend && npm test -- relayObfsForm.test.mjs`

Expected: FAIL because `relay_layers` is dropped.

- [ ] **Step 3: Implement payload and form support**

Preserve `relay_layers` in runtime/dev mocks. Update `RelayChainInput.vue` to render one row per layer and emit `number[][]`. Initialize forms with `relay_layers` when present or derive from `relay_chain`; save both `relay_layers` and compatibility `relay_chain`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/frontend && npm test -- relayObfsForm.test.mjs`

Expected: PASS.

---

### Task 10: Render Diagnostic Hop Latency

**Files:**
- Modify diagnostic modal component located by `rg "diagnosticTask|samples|backends" panel/frontend/src/components panel/frontend/src/pages`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Test: matching frontend component test file

- [ ] **Step 1: Write failing render test**

Add mock task result containing `relay_paths: [{ path: [1, 4], selected: true, success: true, latency_ms: 42.7, hops: [{ from: 'client', to_listener_id: 1, latency_ms: 12.1, success: true }] }]` and `selected_relay_path: [1, 4]`. Assert rendered text includes `1 → 4`, `42.7 ms`, and `12.1 ms`.

- [ ] **Step 2: Run test to verify it fails**

Run the specific component test command used by the existing frontend test file.

Expected: FAIL because relay path UI is missing.

- [ ] **Step 3: Implement rendering**

Add a Relay path section above existing backend samples. Show selected path, path status, total latency, and hop latency rows. Hide the section when `relay_paths` is empty.

- [ ] **Step 4: Run test to verify it passes**

Run the same component test command.

Expected: PASS.

---

### Task 11: End-To-End Verification

**Files:**
- No new files.

- [ ] **Step 1: Run Go agent tests**

Run: `cd go-agent && go test ./...`

Expected: PASS.

- [ ] **Step 2: Run Go control-plane tests**

Run: `cd panel/backend-go && go test ./...`

Expected: PASS.

- [ ] **Step 3: Run frontend build**

Run: `cd panel/frontend && npm run build`

Expected: PASS.

- [ ] **Step 4: Run image build if packaging changed**

Run: `docker build -t nginx-reverse-emby .`

Expected: PASS.

---

## Self-Review

**Spec coverage:**
- `relay_layers` configuration and `relay_chain` compatibility: Tasks 1-4 and 9.
- `race-first-success` runtime: Tasks 5 and 7.
- adaptive observation ordering: Task 6.
- diagnostic path/hop output: Tasks 8 and 10.
- frontend editing and display: Tasks 9 and 10.
- verification: Task 11.

**Known implementation note:** Exact intermediate hop latency may require protocol-level instrumentation beyond the current single-path Relay protocol. Task 8 starts with available measured timings and keeps unmeasurable intermediate hop timings optional unless protocol instrumentation is added during implementation.

**Placeholder scan:** No `TBD`, `TODO`, or untracked feature requirement remains.
