# Go Agent Phase 4 Benchmarks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add repeatable benchmarks for the go-agent hot paths identified in Phase 4 so future tuning has measurement evidence before constants or behavior change.

**Architecture:** Add package-local benchmark files under the existing hot-path packages. Keep the benchmarks behavior-preserving: no timeout, buffer-size, relay concurrency, protocol, or runtime tuning in this plan.

**Tech Stack:** Go benchmark tests, existing `go-agent` internal packages, `go test -bench`, `benchstat`-compatible output.

---

## Scope And Constraints

This plan implements Phase 4 measurement coverage from `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`.

Required constraints:

- Add benchmarks only. Do not tune constants or change production behavior in this plan.
- Existing benchmark file `go-agent/internal/relay/perf_bench_test.go` remains the relay protocol benchmark file.
- New benchmark files should use package-local tests so they can call unexported hot-path helpers without introducing exported test-only APIs.
- Benchmarks should call `b.ReportAllocs()` and `b.SetBytes(...)` when the benchmark transfers bytes.
- Benchmarks must be deterministic enough for local comparison. Avoid sleeping, real DNS, external network, or wall-clock assertions.
- Keep helper code inside the benchmark files unless the helper is already useful to production tests.
- Run package tests before and after adding each benchmark file.
- Do not require `benchstat` to be installed; benchmark output should still be usable by redirecting `go test -bench` output to files.

## Target Benchmark Coverage

- HTTP response copy and traffic accounting.
- HTTP upgrade copy paths.
- L4 TCP bidirectional copy.
- Relay TCP copy paths and existing relay UDP/UOT packet paths.
- Relay path expansion and clone/key assignment.
- Backend candidate ordering and observation-key construction.

## File Map

- Create: `go-agent/internal/proxy/perf_bench_test.go`
  - Benchmarks `copyResponse`, `copySwitchProtocolTraffic`, and reusable request-body preparation.
- Create: `go-agent/internal/l4/perf_bench_test.go`
  - Benchmarks `copyBidirectionalTCP` over `net.Pipe` with L4 traffic accounting.
- Modify: `go-agent/internal/relay/perf_bench_test.go`
  - Add benchmark coverage for `copyRelayTraffic`.
- Create: `go-agent/internal/relayroute/perf_bench_test.go`
  - Benchmarks `ResolvePaths` and `ClonePathsWithTarget`.
- Create: `go-agent/internal/backends/perf_bench_test.go`
  - Benchmarks candidate ordering and observation/backoff key construction.
- No production files should be modified.

## Task 1: Baseline Benchmark Inventory

**Files:**
- Read: `go-agent/internal/relay/perf_bench_test.go`
- Read: `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`

- [ ] **Step 1: Confirm clean worktree**

Run:

```powershell
git status --short
```

Expected: no output.

- [ ] **Step 2: Run current tests**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run existing benchmark suite once**

Run:

```powershell
cd go-agent
go test ./internal/relay -run '^$' -bench . -benchmem
```

Expected: benchmark command completes successfully and prints the existing relay benchmark names.

- [ ] **Step 4: Commit**

No commit is expected for this task.

## Task 2: Add HTTP Proxy Benchmarks

**Files:**
- Create: `go-agent/internal/proxy/perf_bench_test.go`
- Test: `go-agent/internal/proxy`

- [ ] **Step 1: Write HTTP benchmark file**

Create `go-agent/internal/proxy/perf_bench_test.go`:

```go
package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func BenchmarkCopyResponse1MiBWithTrafficAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("r"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(payload)),
		}
		recorder := httptest.NewRecorder()
		if _, err := copyResponse(recorder, resp, traffic.NewHTTPRecorder()); err != nil {
			b.Fatalf("copyResponse() error = %v", err)
		}
	}
}

func BenchmarkCopySwitchProtocolTraffic1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("u"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		var dst bytes.Buffer
		if _, err := copySwitchProtocolTraffic(&dst, bytes.NewReader(payload), false, traffic.NewHTTPRecorder()); err != nil {
			b.Fatalf("copySwitchProtocolTraffic() error = %v", err)
		}
	}
}

func BenchmarkPrepareReusableBody1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("b"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "https://frontend.example/upload", io.NopCloser(bytes.NewReader(payload)))
		body, err := prepareReusableBody(req, 2, traffic.NewHTTPRecorder())
		if err != nil {
			b.Fatalf("prepareReusableBody() error = %v", err)
		}
		if body == nil {
			b.Fatal("prepareReusableBody() returned nil body")
		}
	}
}
```

- [ ] **Step 2: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/perf_bench_test.go
go test ./internal/proxy
```

Expected: PASS.

- [ ] **Step 3: Run proxy benchmarks**

Run:

```powershell
cd go-agent
go test ./internal/proxy -run '^$' -bench 'Benchmark(CopyResponse|CopySwitchProtocolTraffic|PrepareReusableBody)' -benchmem
```

Expected: command completes and prints all three benchmark names.

- [ ] **Step 4: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/perf_bench_test.go
git commit -m "test(agent): add http proxy benchmarks"
```

Expected: commit succeeds.

## Task 3: Add L4 TCP Copy Benchmark

**Files:**
- Create: `go-agent/internal/l4/perf_bench_test.go`
- Test: `go-agent/internal/l4`

- [ ] **Step 1: Write L4 benchmark file**

Create `go-agent/internal/l4/perf_bench_test.go`:

```go
package l4

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func BenchmarkCopyBidirectionalTCP1MiBWithTrafficAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("t"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload) * 2))
	for i := 0; i < b.N; i++ {
		downstreamClient, downstreamServer := net.Pipe()
		upstreamServer, upstreamBackend := net.Pipe()
		done := make(chan struct{})
		go func() {
			copyBidirectionalTCP(downstreamServer, upstreamServer, traffic.NewL4Recorder())
			close(done)
		}()

		var wg sync.WaitGroup
		wg.Add(4)
		go benchmarkWriteAll(b, &wg, downstreamClient, payload)
		go benchmarkDiscardN(b, &wg, upstreamBackend, len(payload))
		go benchmarkWriteAll(b, &wg, upstreamBackend, payload)
		go benchmarkDiscardN(b, &wg, downstreamClient, len(payload))
		wg.Wait()

		_ = downstreamClient.Close()
		_ = downstreamServer.Close()
		_ = upstreamServer.Close()
		_ = upstreamBackend.Close()
		<-done
	}
}

func benchmarkWriteAll(b *testing.B, wg *sync.WaitGroup, conn net.Conn, payload []byte) {
	b.Helper()
	defer wg.Done()
	if _, err := conn.Write(payload); err != nil {
		b.Errorf("Write() error = %v", err)
	}
}

func benchmarkDiscardN(b *testing.B, wg *sync.WaitGroup, r io.Reader, size int) {
	b.Helper()
	defer wg.Done()
	if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
		b.Errorf("CopyN() error = %v", err)
	}
}
```

- [ ] **Step 2: Format and run L4 tests**

Run:

```powershell
cd go-agent
gofmt -w internal/l4/perf_bench_test.go
go test ./internal/l4
```

Expected: PASS.

- [ ] **Step 3: Run L4 benchmark**

Run:

```powershell
cd go-agent
go test ./internal/l4 -run '^$' -bench BenchmarkCopyBidirectionalTCP1MiBWithTrafficAccounting -benchmem
```

Expected: command completes and prints `BenchmarkCopyBidirectionalTCP1MiBWithTrafficAccounting`.

- [ ] **Step 4: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/l4/perf_bench_test.go
git commit -m "test(agent): add l4 copy benchmark"
```

Expected: commit succeeds.

## Task 4: Add Relay Copy Benchmark

**Files:**
- Modify: `go-agent/internal/relay/perf_bench_test.go`
- Test: `go-agent/internal/relay`

- [ ] **Step 1: Extend relay benchmark imports**

In `go-agent/internal/relay/perf_bench_test.go`, add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
```

- [ ] **Step 2: Add relay copy benchmark**

Append this benchmark to `go-agent/internal/relay/perf_bench_test.go`:

```go
func BenchmarkCopyRelayTraffic1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("c"), 1<<20)
	traffic.Reset()
	traffic.SetEnabled(true)
	b.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		var dst bytes.Buffer
		if _, err := copyRelayTraffic(&dst, bytes.NewReader(payload), false, traffic.NewRelayRecorder()); err != nil {
			b.Fatalf("copyRelayTraffic() error = %v", err)
		}
	}
}
```

- [ ] **Step 3: Format and run relay tests**

Run:

```powershell
cd go-agent
gofmt -w internal/relay/perf_bench_test.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 4: Run relay benchmarks**

Run:

```powershell
cd go-agent
go test ./internal/relay -run '^$' -bench 'Benchmark(TLSTCPLogicalStreamReadFrom1MiB|UOTPacketRoundTrip1400B|ReadMuxFrame64KiB|WriteMuxFrame64KiB|CopyRelayTraffic1MiB)' -benchmem
```

Expected: command completes and prints the existing four benchmark names plus `BenchmarkCopyRelayTraffic1MiB`.

- [ ] **Step 5: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/relay/perf_bench_test.go
git commit -m "test(agent): add relay copy benchmark"
```

Expected: commit succeeds.

## Task 5: Add Relay Route Benchmarks

**Files:**
- Create: `go-agent/internal/relayroute/perf_bench_test.go`
- Test: `go-agent/internal/relayroute`

- [ ] **Step 1: Write relayroute benchmark file**

Create `go-agent/internal/relayroute/perf_bench_test.go`:

```go
package relayroute

import (
	"strconv"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func BenchmarkResolvePathsLayeredFanout(b *testing.B) {
	listeners := benchmarkRelayListeners(12)
	layers := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		paths, err := ResolvePaths("benchmark rule", nil, layers, listeners, "backend.example:443")
		if err != nil {
			b.Fatalf("ResolvePaths() error = %v", err)
		}
		if len(paths) != 27 {
			b.Fatalf("ResolvePaths() paths = %d, want 27", len(paths))
		}
	}
}

func BenchmarkClonePathsWithTargetLayeredFanout(b *testing.B) {
	listeners := benchmarkRelayListeners(12)
	paths, err := ResolvePaths("benchmark rule", nil, [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}, listeners, "backend.example:443")
	if err != nil {
		b.Fatalf("ResolvePaths() error = %v", err)
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cloned := ClonePathsWithTarget(paths, "backend-"+strconv.Itoa(i)+".example:443")
		if len(cloned) != len(paths) {
			b.Fatalf("ClonePathsWithTarget() paths = %d, want %d", len(cloned), len(paths))
		}
	}
}

func benchmarkRelayListeners(total int) []model.RelayListener {
	listeners := make([]model.RelayListener, 0, total)
	for id := 1; id <= total; id++ {
		listeners = append(listeners, model.RelayListener{
			ID:         id,
			ListenHost: "127.0.0.1",
			ListenPort: 8000 + id,
			PublicHost: "relay-" + strconv.Itoa(id) + ".example.com",
			PublicPort: 9000 + id,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "abc",
			}},
		})
	}
	return listeners
}
```

- [ ] **Step 2: Format and run relayroute tests**

Run:

```powershell
cd go-agent
gofmt -w internal/relayroute/perf_bench_test.go
go test ./internal/relayroute
```

Expected: PASS.

- [ ] **Step 3: Run relayroute benchmarks**

Run:

```powershell
cd go-agent
go test ./internal/relayroute -run '^$' -bench . -benchmem
```

Expected: command completes and prints both relayroute benchmark names.

- [ ] **Step 4: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/relayroute/perf_bench_test.go
git commit -m "test(agent): add relay route benchmarks"
```

Expected: commit succeeds.

## Task 6: Add Backend Candidate Benchmarks

**Files:**
- Create: `go-agent/internal/backends/perf_bench_test.go`
- Test: `go-agent/internal/backends`

- [ ] **Step 1: Write backend benchmark file**

Create `go-agent/internal/backends/perf_bench_test.go`:

```go
package backends

import (
	"strconv"
	"testing"
	"time"
)

func BenchmarkCacheOrderAdaptive64Candidates(b *testing.B) {
	now := time.Date(2026, time.May, 11, 0, 0, 0, 0, time.UTC)
	cache := NewCache(Config{
		Now:        func() time.Time { return now },
		RandomIntn: func(n int) int { return 0 },
	})
	candidates := benchmarkCandidates(64)
	for i, candidate := range candidates {
		key := BackendObservationKey("http:bench", candidate.Address)
		latency := time.Duration(10+i%20) * time.Millisecond
		cache.ObserveBackendSuccess(key, latency, 120*time.Millisecond, int64(256*1024+i*1024))
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ordered := cache.Order("http:bench", StrategyAdaptive, candidates)
		if len(ordered) != len(candidates) {
			b.Fatalf("Order() candidates = %d, want %d", len(ordered), len(candidates))
		}
	}
}

func BenchmarkCacheOrderRoundRobin64Candidates(b *testing.B) {
	cache := NewCache(Config{
		RandomIntn: func(n int) int { return 0 },
	})
	candidates := benchmarkCandidates(64)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ordered := cache.Order("http:bench", StrategyRoundRobin, candidates)
		if len(ordered) != len(candidates) {
			b.Fatalf("Order() candidates = %d, want %d", len(ordered), len(candidates))
		}
	}
}

func BenchmarkBackendObservationKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if key := BackendObservationKey("http:rule-"+strconv.Itoa(i%32), "backend-"+strconv.Itoa(i%64)+".example:8096"); key == "" {
			b.Fatal("BackendObservationKey() returned empty key")
		}
	}
}

func BenchmarkRelayBackoffKeyForLayers(b *testing.B) {
	layers := [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8, 9}}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if key := RelayBackoffKeyForLayers(nil, layers, "backend-"+strconv.Itoa(i%64)+".example:443"); key == "" {
			b.Fatal("RelayBackoffKeyForLayers() returned empty key")
		}
	}
}

func benchmarkCandidates(total int) []Candidate {
	candidates := make([]Candidate, 0, total)
	for i := 0; i < total; i++ {
		host := "backend-" + strconv.Itoa(i) + ".example"
		candidates = append(candidates, Candidate{
			Endpoint: Endpoint{Host: host, Port: 8096},
			Address:  host + ":8096",
		})
	}
	return candidates
}
```

- [ ] **Step 2: Format and run backend tests**

Run:

```powershell
cd go-agent
gofmt -w internal/backends/perf_bench_test.go
go test ./internal/backends
```

Expected: PASS.

- [ ] **Step 3: Run backend benchmarks**

Run:

```powershell
cd go-agent
go test ./internal/backends -run '^$' -bench . -benchmem
```

Expected: command completes and prints all four benchmark names.

- [ ] **Step 4: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/backends/perf_bench_test.go
git commit -m "test(agent): add backend selection benchmarks"
```

Expected: commit succeeds.

## Task 7: Full Benchmark Verification

**Files:**
- Verify all files created or modified in Tasks 2 through 6.

- [ ] **Step 1: Run all go-agent tests**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run focused benchmark suite**

Run:

```powershell
cd go-agent
go test ./internal/proxy ./internal/l4 ./internal/relay ./internal/relayroute ./internal/backends -run '^$' -bench . -benchmem
```

Expected: command completes and prints benchmark output for all five packages.

- [ ] **Step 3: Save optional local benchmark output**

If a local artifact is useful for comparison, run:

```powershell
cd go-agent
go test ./internal/proxy ./internal/l4 ./internal/relay ./internal/relayroute ./internal/backends -run '^$' -bench . -benchmem | Tee-Object ..\docs\superpowers\plans\2026-05-11-go-agent-phase-4-benchmarks.local.txt
```

Expected: command completes. Do not commit `*.local.txt`; it is a local measurement artifact.

- [ ] **Step 4: Confirm no production files changed**

Run:

```powershell
git diff --name-only
```

Expected: only benchmark test files and this plan are changed if commits were not made task-by-task. No non-test Go production file should appear.

- [ ] **Step 5: Run whitespace check**

Run:

```powershell
git diff --check
```

Expected: no whitespace errors.

- [ ] **Step 6: Commit final cleanup if needed**

If changes remain after prior task commits:

```powershell
git add go-agent/internal/proxy/perf_bench_test.go go-agent/internal/l4/perf_bench_test.go go-agent/internal/relay/perf_bench_test.go go-agent/internal/relayroute/perf_bench_test.go go-agent/internal/backends/perf_bench_test.go docs/superpowers/plans/2026-05-11-go-agent-phase-4-benchmarks.md
git commit -m "test(agent): add phase 4 benchmark coverage"
```

Expected: commit succeeds only when there are uncommitted changes.

## Self-Review Notes

- Spec coverage: This plan covers every Phase 4 measurement target listed in the design: HTTP copy/accounting, HTTP upgrade copy, L4 TCP copy, relay copy/UOT, relay path expansion/key cloning, and backend ordering/key construction.
- Placeholder scan: All benchmark files, commands, and commit messages are explicit. No task asks implementers to tune later or fill in missing details.
- Type consistency: All referenced functions and types exist in the current `go-agent` packages: `copyResponse`, `copySwitchProtocolTraffic`, `prepareReusableBody`, `copyBidirectionalTCP`, `copyRelayTraffic`, `ResolvePaths`, `ClonePathsWithTarget`, `Cache.Order`, `BackendObservationKey`, and `RelayBackoffKeyForLayers`.
- Scope check: The plan intentionally excludes production tuning. Any future tuning should be a separate plan that compares benchmark output before and after the specific change.
