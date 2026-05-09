# Go Agent Phase 1 Hot-Path Helpers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract shared hot-path helpers in `go-agent` and replace duplicated proxy, L4, relay, and diagnostics helper code without changing external behavior.

**Architecture:** Add narrow internal packages for network normalization, stream copying/accounting, traffic block state, and relay path construction. Replace call sites in small batches so each package can be tested after its own migration.

**Tech Stack:** Go standard library, existing `go-agent` packages, `go test`, `gofmt`, git.

---

## Scope

This plan implements Phase 1 from `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`.

It does not remove legacy HTTP `BackendURL`, L4 `UpstreamHost`/`UpstreamPort`, or control-plane fields. It does not split large files except where helper movement requires a small local adapter. It does not tune timeout, buffer, relay concurrency, or flush threshold defaults beyond preserving the existing constants in shared helpers.

## File Map

- Create: `go-agent/internal/netutil/netutil.go`
  - Pure host, port, URL authority, client IP, and relay listener endpoint helpers.
- Create: `go-agent/internal/netutil/netutil_test.go`
  - Unit tests for behavior currently duplicated in proxy, L4, and diagnostics.
- Create: `go-agent/internal/stream/stream.go`
  - Shared copy strategy wrappers and traffic-aware IO wrappers.
- Create: `go-agent/internal/stream/stream_test.go`
  - Unit tests for copy fast-path selection, generic copy, direction accounting, and flush thresholds.
- Create: `go-agent/internal/traffic/block_state.go`
  - Shared block state type and atomic storage.
- Create: `go-agent/internal/traffic/block_state_test.go`
  - Unit tests for reason normalization and nil-safe load behavior.
- Create: `go-agent/internal/relayroute/relayroute.go`
  - Shared relay usage, listener map, path resolution, path cloning, and target key helpers.
- Create: `go-agent/internal/relayroute/relayroute_test.go`
  - Unit tests for missing, disabled, invalid, expanded, cloned, and keyed path behavior.
- Modify: `go-agent/internal/proxy/http_engine.go`
  - Replace local host/default-port helpers with `netutil`.
- Modify: `go-agent/internal/proxy/server.go`
  - Replace relay path and copy/traffic helper usage where Phase 1 helpers fit cleanly.
- Modify: `go-agent/internal/proxy/traffic_block.go`
  - Replace duplicated block state implementation with a type alias to `traffic.BlockState`.
- Modify: `go-agent/internal/l4/copy.go`
  - Replace local copy helper with `stream.CopyPreferReaderFrom`.
- Modify: `go-agent/internal/l4/server.go`
  - Replace relay path and relay endpoint helpers with `relayroute`.
- Modify: `go-agent/internal/l4/traffic_block.go`
  - Replace duplicated block state implementation with a type alias to `traffic.BlockState`.
- Modify: `go-agent/internal/relay/copy.go`
  - Replace duplicated copy and relay traffic writer logic with `stream`.
- Modify: `go-agent/internal/relay/traffic_block.go`
  - Replace duplicated state storage with `traffic.BlockState` while keeping relay error presentation local.
- Modify: `go-agent/internal/relay/runtime.go`
  - Replace `state.errorMessage()` call sites with the relay-local presentation helper.
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
  - Replace `state.errorMessage()` call site with the relay-local presentation helper.
- Modify: `go-agent/internal/diagnostics/relay_paths.go`
  - Replace local relay path resolution with `relayroute.ResolvePaths`.
- Modify: `go-agent/internal/diagnostics/http.go`
  - Replace diagnostic path cloning and relay endpoint helper usage.

## Task 1: Baseline Verification

**Files:**
- Read: `go-agent/go.mod`
- Read: `go-agent/internal/proxy/server.go`
- Read: `go-agent/internal/l4/server.go`
- Read: `go-agent/internal/relay/runtime.go`

- [ ] **Step 1: Confirm the worktree is clean**

Run:

```powershell
git status --short
```

Expected: no output.

- [ ] **Step 2: Run the existing go-agent suite**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: every package reports `ok` or `[no test files]`.

- [ ] **Step 3: Commit only if baseline files changed**

No commit is expected for this task. If any generated files appear, inspect them with:

```powershell
git status --short
```

Expected: no source changes.

## Task 2: Add `internal/netutil`

**Files:**
- Create: `go-agent/internal/netutil/netutil_test.go`
- Create: `go-agent/internal/netutil/netutil.go`

- [ ] **Step 1: Write failing netutil tests**

Create `go-agent/internal/netutil/netutil_test.go`:

```go
package netutil

import (
	"net/url"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestNormalizeHostTrimsLowercasesAndStripsPort(t *testing.T) {
	if got := NormalizeHost(" Example.COM:8443 "); got != "example.com" {
		t.Fatalf("NormalizeHost() = %q, want %q", got, "example.com")
	}
}

func TestNormalizeHostReturnsIPv6HostWithoutPort(t *testing.T) {
	if got := NormalizeHost("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Fatalf("NormalizeHost() = %q, want IPv6 host", got)
	}
}

func TestURLAuthorityUsesDefaultPorts(t *testing.T) {
	target, err := url.Parse("https://Example.COM/path")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := URLAuthority(target); got != "example.com:443" {
		t.Fatalf("URLAuthority() = %q, want %q", got, "example.com:443")
	}
}

func TestAddressWithDefaultPortPreservesExplicitPort(t *testing.T) {
	target, err := url.Parse("http://example.com:8080/path")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := AddressWithDefaultPort(target); got != "example.com:8080" {
		t.Fatalf("AddressWithDefaultPort() = %q, want explicit port", got)
	}
}

func TestRelayListenerDialEndpointPrefersPublicHostAndPort(t *testing.T) {
	host, port := RelayListenerDialEndpoint(model.RelayListener{
		ListenHost: "0.0.0.0",
		BindHosts:  []string{"127.0.0.1"},
		ListenPort: 8443,
		PublicHost: "relay.example.com",
		PublicPort: 9443,
	})
	if host != "relay.example.com" || port != 9443 {
		t.Fatalf("RelayListenerDialEndpoint() = %s:%d, want relay.example.com:9443", host, port)
	}
}

func TestRelayListenerDialEndpointFallsBackToFirstBindHost(t *testing.T) {
	host, port := RelayListenerDialEndpoint(model.RelayListener{
		ListenHost: "0.0.0.0",
		BindHosts:  []string{" ", "127.0.0.2"},
		ListenPort: 8443,
	})
	if host != "127.0.0.2" || port != 8443 {
		t.Fatalf("RelayListenerDialEndpoint() = %s:%d, want 127.0.0.2:8443", host, port)
	}
}

func TestClientIPStripsPortWhenPresent(t *testing.T) {
	if got := ClientIP("192.0.2.10:3456"); got != "192.0.2.10" {
		t.Fatalf("ClientIP() = %q, want host", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
cd go-agent
go test ./internal/netutil
```

Expected: FAIL with undefined symbols such as `NormalizeHost`.

- [ ] **Step 3: Add netutil implementation**

Create `go-agent/internal/netutil/netutil.go`:

```go
package netutil

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func NormalizeHost(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

func DefaultPort(scheme string) int {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https":
		return 443
	default:
		return 80
	}
}

func DefaultPortString(scheme string) string {
	port := DefaultPort(scheme)
	if port <= 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func URLAuthority(target *url.URL) string {
	if target == nil {
		return ""
	}
	host := NormalizeHost(target.Hostname())
	if host == "" {
		return ""
	}
	port := target.Port()
	if port == "" {
		port = DefaultPortString(target.Scheme)
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func PortWithDefault(target *url.URL) int {
	if target == nil {
		return 0
	}
	if target.Port() != "" {
		port, _ := strconv.Atoi(target.Port())
		return port
	}
	return DefaultPort(target.Scheme)
}

func AddressWithDefaultPort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if target.Port() != "" {
		return target.Host
	}
	return net.JoinHostPort(target.Hostname(), strconv.Itoa(DefaultPort(target.Scheme)))
}

func ClientIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func RelayListenerDialEndpoint(listener model.RelayListener) (string, int) {
	host := strings.TrimSpace(listener.PublicHost)
	if host == "" {
		for _, bindHost := range listener.BindHosts {
			if trimmed := strings.TrimSpace(bindHost); trimmed != "" {
				host = trimmed
				break
			}
		}
	}
	if host == "" {
		host = strings.TrimSpace(listener.ListenHost)
	}

	port := listener.PublicPort
	if port <= 0 {
		port = listener.ListenPort
	}
	return host, port
}
```

- [ ] **Step 4: Run netutil tests**

Run:

```powershell
cd go-agent
go test ./internal/netutil
```

Expected: PASS.

- [ ] **Step 5: Format and commit**

Run:

```powershell
cd go-agent
gofmt -w internal/netutil/netutil.go internal/netutil/netutil_test.go
go test ./internal/netutil
cd ..
git add go-agent/internal/netutil
git commit -m "refactor(agent): add network normalization helpers"
```

Expected: commit succeeds.

## Task 3: Add `internal/stream`

**Files:**
- Create: `go-agent/internal/stream/stream_test.go`
- Create: `go-agent/internal/stream/stream.go`

- [ ] **Step 1: Write failing stream tests**

Create `go-agent/internal/stream/stream_test.go`:

```go
package stream

import (
	"bytes"
	"io"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type readerFromBuffer struct {
	bytes.Buffer
	usedReaderFrom bool
}

func (b *readerFromBuffer) ReadFrom(r io.Reader) (int64, error) {
	b.usedReaderFrom = true
	return b.Buffer.ReadFrom(r)
}

type writerToReader struct {
	payload []byte
	used   bool
}

func (r *writerToReader) Read(p []byte) (int, error) {
	if len(r.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.payload)
	r.payload = r.payload[n:]
	return n, nil
}

func (r *writerToReader) WriteTo(w io.Writer) (int64, error) {
	r.used = true
	n, err := w.Write(r.payload)
	r.payload = r.payload[n:]
	return int64(n), err
}

func TestCopyPreferReaderFromUsesDestinationFastPath(t *testing.T) {
	dst := &readerFromBuffer{}
	n, err := CopyPreferReaderFrom(dst, bytes.NewBufferString("payload"))
	if err != nil {
		t.Fatalf("CopyPreferReaderFrom() error = %v", err)
	}
	if n != int64(len("payload")) || dst.String() != "payload" || !dst.usedReaderFrom {
		t.Fatalf("copy result n=%d body=%q usedReaderFrom=%v", n, dst.String(), dst.usedReaderFrom)
	}
}

func TestCopyGenericSuppressesWriterTo(t *testing.T) {
	src := &writerToReader{payload: []byte("payload")}
	var dst bytes.Buffer
	n, err := CopyGeneric(&dst, src)
	if err != nil {
		t.Fatalf("CopyGeneric() error = %v", err)
	}
	if n != int64(len("payload")) || dst.String() != "payload" {
		t.Fatalf("copy result n=%d body=%q", n, dst.String())
	}
	if src.used {
		t.Fatal("CopyGeneric used source WriteTo fast path")
	}
}

func TestTrafficWriterCountsTXAndFlushesAtThreshold(t *testing.T) {
	traffic.Reset()
	recorder := traffic.NewHTTPRecorder()
	var dst bytes.Buffer
	writer := NewTrafficWriter(&dst, DirectionTX, recorder, 4)
	if _, err := writer.Write([]byte("abcd")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["http"].(map[string]uint64)
	if total["tx_bytes"] != 4 || total["rx_bytes"] != 0 {
		t.Fatalf("http counters = %+v, want tx=4 rx=0", total)
	}
}

func TestTrafficWriterFlushesSmallWritesWithBelowThresholdPolicy(t *testing.T) {
	traffic.Reset()
	recorder := traffic.NewRelayRecorder()
	var dst bytes.Buffer
	writer := NewTrafficWriterFlushBelow(&dst, DirectionRX, recorder, 32*1024)
	if _, err := writer.Write([]byte("abc")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["relay"].(map[string]uint64)
	if total["rx_bytes"] != 3 || total["tx_bytes"] != 0 {
		t.Fatalf("relay counters = %+v, want rx=3 tx=0", total)
	}
}

func TestTrafficReadCloserFlushesOnEOF(t *testing.T) {
	traffic.Reset()
	recorder := traffic.NewHTTPRecorder()
	reader := NewTrafficReadCloser(io.NopCloser(bytes.NewBufferString("abc")), DirectionRX, recorder)
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stats := traffic.Snapshot()
	total := stats["traffic"].(map[string]any)["http"].(map[string]uint64)
	if total["rx_bytes"] != 3 || total["tx_bytes"] != 0 {
		t.Fatalf("http counters = %+v, want rx=3 tx=0", total)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
cd go-agent
go test ./internal/stream
```

Expected: FAIL with undefined symbols such as `CopyPreferReaderFrom`.

- [ ] **Step 3: Add stream implementation**

Create `go-agent/internal/stream/stream.go`:

```go
package stream

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type Direction int

const (
	DirectionRX Direction = iota
	DirectionTX
)

type FlushPolicy int

const (
	FlushAtOrAboveThreshold FlushPolicy = iota
	FlushAtOrBelowThreshold
)

func CopyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(readerWithoutWriterTo{Reader: src})
	}
	return io.Copy(dst, src)
}

func CopyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(writerWithoutReaderFrom{Writer: dst}, readerWithoutWriterTo{Reader: src})
}

type readerWithoutWriterTo struct {
	io.Reader
}

type writerWithoutReaderFrom struct {
	io.Writer
}

type TrafficWriter struct {
	dst       io.Writer
	direction Direction
	recorder  *traffic.Recorder
	threshold uint64
	policy    FlushPolicy
	pending   uint64
}

func NewTrafficWriter(dst io.Writer, direction Direction, recorder *traffic.Recorder, threshold uint64) *TrafficWriter {
	return &TrafficWriter{
		dst:       dst,
		direction: direction,
		recorder:  recorder,
		threshold: threshold,
		policy:    FlushAtOrAboveThreshold,
	}
}

func NewTrafficWriterFlushBelow(dst io.Writer, direction Direction, recorder *traffic.Recorder, threshold uint64) *TrafficWriter {
	return &TrafficWriter{
		dst:       dst,
		direction: direction,
		recorder:  recorder,
		threshold: threshold,
		policy:    FlushAtOrBelowThreshold,
	}
}

func (w *TrafficWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 && w.recorder != nil {
		w.add(uint64(n))
	}
	return n, err
}

func (w *TrafficWriter) FlushTraffic() {
	if w == nil || w.recorder == nil || w.pending == 0 {
		return
	}
	if w.direction == DirectionRX {
		w.recorder.Add(int64(w.pending), 0)
	} else {
		w.recorder.Add(0, int64(w.pending))
	}
	w.recorder.Flush()
	w.pending = 0
}

func (w *TrafficWriter) add(bytes uint64) {
	w.pending += bytes
	if w.shouldFlush() {
		w.FlushTraffic()
	}
}

func (w *TrafficWriter) shouldFlush() bool {
	if w.threshold == 0 {
		return true
	}
	switch w.policy {
	case FlushAtOrBelowThreshold:
		return w.pending <= w.threshold
	default:
		return w.pending >= w.threshold
	}
}

type TrafficReadCloser struct {
	io.ReadCloser
	direction Direction
	recorder  *traffic.Recorder
}

func NewTrafficReadCloser(delegate io.ReadCloser, direction Direction, recorder *traffic.Recorder) *TrafficReadCloser {
	return &TrafficReadCloser{ReadCloser: delegate, direction: direction, recorder: recorder}
}

func (c *TrafficReadCloser) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 && c.recorder != nil {
		if c.direction == DirectionRX {
			c.recorder.Add(int64(n), 0)
		} else {
			c.recorder.Add(0, int64(n))
		}
	}
	if err != nil && c.recorder != nil {
		c.recorder.Flush()
	}
	return n, err
}

func (c *TrafficReadCloser) Close() error {
	if c.recorder != nil {
		c.recorder.Flush()
	}
	return c.ReadCloser.Close()
}
```

- [ ] **Step 4: Run stream tests**

Run:

```powershell
cd go-agent
go test ./internal/stream
```

Expected: PASS.

- [ ] **Step 5: Format and commit**

Run:

```powershell
cd go-agent
gofmt -w internal/stream/stream.go internal/stream/stream_test.go
go test ./internal/stream
cd ..
git add go-agent/internal/stream
git commit -m "refactor(agent): add stream copy helpers"
```

Expected: commit succeeds.

## Task 4: Add Shared Traffic Block State

**Files:**
- Create: `go-agent/internal/traffic/block_state_test.go`
- Create: `go-agent/internal/traffic/block_state.go`
- Modify: `go-agent/internal/proxy/traffic_block.go`
- Modify: `go-agent/internal/l4/traffic_block.go`
- Modify: `go-agent/internal/relay/traffic_block.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`

- [ ] **Step 1: Write failing traffic block state tests**

Create `go-agent/internal/traffic/block_state_test.go`:

```go
package traffic

import "testing"

func TestBlockStateValueNormalizesReasonOnStoreAndLoad(t *testing.T) {
	var value BlockStateValue
	value.Store(BlockState{Blocked: true, Reason: " monthly quota exceeded "})
	got := value.Load()
	if !got.Blocked || got.Reason != "monthly quota exceeded" {
		t.Fatalf("Load() = %+v, want normalized blocked state", got)
	}
}

func TestNilBlockStateValueLoadReturnsZero(t *testing.T) {
	var value *BlockStateValue
	if got := value.Load(); got.Blocked || got.Reason != "" {
		t.Fatalf("nil Load() = %+v, want zero state", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
cd go-agent
go test ./internal/traffic
```

Expected: FAIL with undefined symbols such as `BlockStateValue`.

- [ ] **Step 3: Add shared block state implementation**

Create `go-agent/internal/traffic/block_state.go`:

```go
package traffic

import (
	"strings"
	"sync/atomic"
)

type BlockState struct {
	Blocked bool
	Reason  string
}

func (s BlockState) Normalized() BlockState {
	s.Reason = strings.TrimSpace(s.Reason)
	return s
}

type BlockStateValue struct {
	value atomic.Value
}

func (v *BlockStateValue) Store(state BlockState) {
	v.value.Store(state.Normalized())
}

func (v *BlockStateValue) Load() BlockState {
	if v == nil {
		return BlockState{}
	}
	if raw := v.value.Load(); raw != nil {
		if state, ok := raw.(BlockState); ok {
			return state.Normalized()
		}
	}
	return BlockState{}
}
```

- [ ] **Step 4: Replace proxy traffic block file**

Replace the full contents of `go-agent/internal/proxy/traffic_block.go` with:

```go
package proxy

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue
```

- [ ] **Step 5: Replace L4 traffic block file**

Replace the full contents of `go-agent/internal/l4/traffic_block.go` with:

```go
package l4

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue
```

- [ ] **Step 6: Replace relay traffic block file and keep error presentation local**

Replace the full contents of `go-agent/internal/relay/traffic_block.go` with:

```go
package relay

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

type TrafficBlockState = traffic.BlockState
type trafficBlockStateValue = traffic.BlockStateValue

func trafficBlockErrorMessage(state TrafficBlockState) string {
	state = state.Normalized()
	if state.Reason != "" {
		return state.Reason
	}
	return "traffic blocked"
}

func trafficBlockErr(state TrafficBlockState) error {
	return fmt.Errorf("%s", trafficBlockErrorMessage(state))
}
```

- [ ] **Step 7: Replace relay runtime error message call**

In `go-agent/internal/relay/runtime.go`, replace:

```go
return writeRelayResponse(clientConn, relayResponse{OK: false, Error: state.errorMessage()})
```

with:

```go
return writeRelayResponse(clientConn, relayResponse{OK: false, Error: trafficBlockErrorMessage(state)})
```

- [ ] **Step 8: Replace relay mux error message call**

In `go-agent/internal/relay/tls_tcp_session_pool.go`, replace:

```go
_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: state.errorMessage()})
```

with:

```go
_ = s.writeOpenResult(frame.StreamID, muxOpenResult{OK: false, Error: trafficBlockErrorMessage(state)})
```

- [ ] **Step 9: Run focused traffic block tests**

Run:

```powershell
cd go-agent
gofmt -w internal/traffic/block_state.go internal/traffic/block_state_test.go internal/proxy/traffic_block.go internal/l4/traffic_block.go internal/relay/traffic_block.go internal/relay/runtime.go internal/relay/tls_tcp_session_pool.go
go test ./internal/traffic ./internal/proxy ./internal/l4 ./internal/relay
```

Expected: PASS.

- [ ] **Step 10: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/traffic/block_state.go go-agent/internal/traffic/block_state_test.go go-agent/internal/proxy/traffic_block.go go-agent/internal/l4/traffic_block.go go-agent/internal/relay/traffic_block.go go-agent/internal/relay/runtime.go go-agent/internal/relay/tls_tcp_session_pool.go
git commit -m "refactor(agent): share traffic block state"
```

Expected: commit succeeds.

## Task 5: Add `internal/relayroute`

**Files:**
- Create: `go-agent/internal/relayroute/relayroute_test.go`
- Create: `go-agent/internal/relayroute/relayroute.go`

- [ ] **Step 1: Write failing relayroute tests**

Create `go-agent/internal/relayroute/relayroute_test.go`:

```go
package relayroute

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
)

func testListener(id int) model.RelayListener {
	return model.RelayListener{
		ID:         id,
		ListenHost: "127.0.0.1",
		ListenPort: 8000 + id,
		PublicHost: "relay.example.com",
		PublicPort: 9000 + id,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: "abc",
		}},
	}
}

func TestUsesRelayDetectsChainOrLayers(t *testing.T) {
	if UsesRelay(nil, nil) {
		t.Fatal("UsesRelay(nil, nil) = true, want false")
	}
	if !UsesRelay([]int{1}, nil) {
		t.Fatal("UsesRelay(chain, nil) = false, want true")
	}
	if !UsesRelay(nil, [][]int{{1, 2}}) {
		t.Fatal("UsesRelay(nil, layers) = false, want true")
	}
}

func TestResolvePathsBuildsHopsAndKeys(t *testing.T) {
	paths, err := ResolvePaths("http rule \"https://app.example\"", []int{1}, nil, []model.RelayListener{testListener(1)}, "backend.example:443")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if len(paths) != 1 || len(paths[0].Hops) != 1 {
		t.Fatalf("paths = %+v, want one path with one hop", paths)
	}
	hop := paths[0].Hops[0]
	if hop.Address != "relay.example.com:9001" || hop.ServerName != "relay.example.com" {
		t.Fatalf("hop = %+v, want public endpoint", hop)
	}
	wantKey := relayplan.PathKey("relay_path", []int{1}, "backend.example:443")
	if paths[0].Key != wantKey {
		t.Fatalf("path key = %q, want %q", paths[0].Key, wantKey)
	}
}

func TestResolvePathsWrapsMissingListenerWithLabel(t *testing.T) {
	_, err := ResolvePaths("l4 rule 127.0.0.1:8443", []int{2}, nil, []model.RelayListener{testListener(1)}, "")
	if err == nil || !strings.Contains(err.Error(), "l4 rule 127.0.0.1:8443: relay listener 2 not found") {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
}

func TestClonePathsWithTargetDoesNotAliasSlices(t *testing.T) {
	paths, err := ResolvePaths("rule", []int{1}, nil, []model.RelayListener{testListener(1)}, "")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	cloned := ClonePathsWithTarget(paths, "backend.example:443")
	cloned[0].IDs[0] = 99
	cloned[0].Hops[0].Address = "changed"
	if paths[0].IDs[0] != 1 || paths[0].Hops[0].Address == "changed" {
		t.Fatalf("ClonePathsWithTarget aliases original path: original=%+v cloned=%+v", paths, cloned)
	}
	if cloned[0].Key != relayplan.PathKey("relay_path", []int{1}, "backend.example:443") {
		t.Fatalf("cloned key = %q", cloned[0].Key)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
cd go-agent
go test ./internal/relayroute
```

Expected: FAIL with undefined symbols such as `ResolvePaths`.

- [ ] **Step 3: Add relayroute implementation**

Create `go-agent/internal/relayroute/relayroute.go`:

```go
package relayroute

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
)

const DefaultMaxPaths = 32

func UsesRelay(chain []int, layers [][]int) bool {
	return len(chain) > 0 || len(layers) > 0
}

func ListenerMap(listeners []model.RelayListener) map[int]model.RelayListener {
	out := make(map[int]model.RelayListener, len(listeners))
	for _, listener := range listeners {
		out[listener.ID] = listener
	}
	return out
}

func ResolvePaths(label string, chain []int, layers [][]int, listeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	paths, err := ResolvePathsFromMap(chain, layers, ListenerMap(listeners), target)
	if err != nil {
		if strings.TrimSpace(label) != "" {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return nil, err
	}
	return paths, nil
}

func ResolvePathsFromMap(chain []int, layers [][]int, listenersByID map[int]model.RelayListener, target string) ([]relayplan.Path, error) {
	normalizedLayers := relayplan.NormalizeLayers(chain, layers)
	pathIDs, err := relayplan.ExpandPaths(normalizedLayers, DefaultMaxPaths)
	if err != nil {
		return nil, err
	}
	if len(pathIDs) == 0 {
		return nil, nil
	}
	paths := make([]relayplan.Path, 0, len(pathIDs))
	for _, ids := range pathIDs {
		hops := make([]relay.Hop, 0, len(ids))
		for _, listenerID := range ids {
			listener, ok := listenersByID[listenerID]
			if !ok {
				return nil, fmt.Errorf("relay listener %d not found", listenerID)
			}
			if !listener.Enabled {
				return nil, fmt.Errorf("relay listener %d is disabled", listenerID)
			}
			if err := relay.ValidateListener(listener); err != nil {
				return nil, fmt.Errorf("relay listener %d: %w", listenerID, err)
			}
			host, port := netutil.RelayListenerDialEndpoint(listener)
			hops = append(hops, relay.Hop{
				Address:    net.JoinHostPort(host, strconv.Itoa(port)),
				ServerName: host,
				Listener:   listener,
			})
		}
		paths = append(paths, relayplan.Path{
			IDs:  append([]int(nil), ids...),
			Hops: hops,
			Key:  relayplan.PathKey("relay_path", ids, target),
		})
	}
	return paths, nil
}

func ClonePaths(paths []relayplan.Path) []relayplan.Path {
	cloned := make([]relayplan.Path, len(paths))
	for i, path := range paths {
		cloned[i] = relayplan.Path{
			IDs:  append([]int(nil), path.IDs...),
			Hops: append([]relay.Hop(nil), path.Hops...),
			Key:  path.Key,
		}
	}
	return cloned
}

func ClonePathsWithTarget(paths []relayplan.Path, target string) []relayplan.Path {
	cloned := ClonePaths(paths)
	for i := range cloned {
		cloned[i].Key = relayplan.PathKey("relay_path", cloned[i].IDs, target)
	}
	return cloned
}
```

- [ ] **Step 4: Run relayroute tests**

Run:

```powershell
cd go-agent
go test ./internal/relayroute
```

Expected: PASS.

- [ ] **Step 5: Format and commit**

Run:

```powershell
cd go-agent
gofmt -w internal/relayroute/relayroute.go internal/relayroute/relayroute_test.go
go test ./internal/relayroute
cd ..
git add go-agent/internal/relayroute
git commit -m "refactor(agent): add shared relay route helpers"
```

Expected: commit succeeds.

## Task 6: Migrate Proxy Helper Call Sites

**Files:**
- Modify: `go-agent/internal/proxy/http_engine.go`
- Modify: `go-agent/internal/proxy/server.go`
- Test: `go-agent/internal/proxy/http_engine_test.go`
- Test: `go-agent/internal/proxy/server_test.go`
- Test: `go-agent/internal/proxy/resume_test.go`
- Test: `go-agent/internal/proxy/traffic_test.go`

- [ ] **Step 1: Update imports in proxy files**

In `go-agent/internal/proxy/http_engine.go`, add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
```

Remove `net` from `http_engine.go`; after this replacement the file no longer calls `net.SplitHostPort` directly.

In `go-agent/internal/proxy/server.go`, add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
```

- [ ] **Step 2: Replace netutil wrapper functions in `http_engine.go`**

At the bottom helper section of `go-agent/internal/proxy/http_engine.go`, replace the existing `normalizeHost`, `normalizeURLAuthority`, and `clientIP` implementations with:

```go
func normalizeHost(value string) string {
	return netutil.NormalizeHost(value)
}

func normalizeURLAuthority(target *url.URL) string {
	return netutil.URLAuthority(target)
}

func clientIP(remoteAddr string) string {
	return netutil.ClientIP(remoteAddr)
}
```

This keeps package-local function names so most proxy code does not move in this task.

- [ ] **Step 3: Replace default port helpers in `server.go`**

Replace the existing `portWithDefault`, `addressWithDefaultPort`, `httpBackendDialAddress`, `defaultPort`, and `defaultPortString` helper implementations in `go-agent/internal/proxy/server.go` with:

```go
func portWithDefault(target *url.URL) int {
	return netutil.PortWithDefault(target)
}

func addressWithDefaultPort(target *url.URL) string {
	return netutil.AddressWithDefaultPort(target)
}

func httpBackendDialAddress(target *url.URL) string {
	return netutil.AddressWithDefaultPort(target)
}

func defaultPort(scheme string) int {
	return netutil.DefaultPort(scheme)
}

func defaultPortString(scheme string) string {
	return netutil.DefaultPortString(scheme)
}
```

- [ ] **Step 4: Replace relay route helpers in `server.go`**

Replace:

```go
func ruleUsesRelay(rule model.HTTPRule) bool {
	return len(rule.RelayChain) > 0 || len(rule.RelayLayers) > 0
}
```

with:

```go
func ruleUsesRelay(rule model.HTTPRule) bool {
	return relayroute.UsesRelay(rule.RelayChain, rule.RelayLayers)
}
```

Replace the body of `resolveRelayPaths` with:

```go
func resolveRelayPaths(rule model.HTTPRule, relayListeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	return relayroute.ResolvePaths(fmt.Sprintf("http rule %q", rule.FrontendURL), rule.RelayChain, rule.RelayLayers, relayListeners, target)
}
```

Replace the body of `cloneRelayPlanPaths` with:

```go
func cloneRelayPlanPaths(paths []relayplan.Path) []relayplan.Path {
	return relayroute.ClonePaths(paths)
}
```

Delete the local `relayHopDialEndpoint` function from `server.go`.

- [ ] **Step 5: Use keyed path cloning in relay transport dial**

In `newRelayTransports`, replace:

```go
requestPaths := cloneRelayPlanPaths(paths)
for i := range requestPaths {
	requestPaths[i].Key = relayplan.PathKey("relay_path", requestPaths[i].IDs, target)
}
```

with:

```go
requestPaths := relayroute.ClonePathsWithTarget(paths, target)
```

- [ ] **Step 6: Replace switch-protocol traffic writer with stream helper**

Replace `copySwitchProtocolTraffic` in `server.go` with:

```go
func copySwitchProtocolTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := stream.DirectionTX
	if rxDirection {
		direction = stream.DirectionRX
	}
	writer := stream.NewTrafficWriter(dst, direction, httpRecorderOrAggregate(recorder), 0)
	return stream.CopyGeneric(writer, src)
}
```

Delete the local `switchProtocolTrafficWriter` type and its `Write` method from `server.go`.

- [ ] **Step 7: Run focused proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/http_engine.go internal/proxy/server.go
go test ./internal/proxy
```

Expected: PASS.

- [ ] **Step 8: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/http_engine.go go-agent/internal/proxy/server.go
git commit -m "refactor(agent): reuse shared helpers in http proxy"
```

Expected: commit succeeds.

## Task 7: Migrate L4 Helper Call Sites

**Files:**
- Modify: `go-agent/internal/l4/copy.go`
- Modify: `go-agent/internal/l4/server.go`
- Test: `go-agent/internal/l4/copy_test.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/l4/traffic_test.go`

- [ ] **Step 1: Replace L4 copy helper implementation**

Replace the full contents of `go-agent/internal/l4/copy.go` with:

```go
package l4

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
)

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyPreferReaderFrom(dst, src)
}
```

- [ ] **Step 2: Update imports in `l4/server.go`**

Add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
```

Keep `relayplan` in imports because the `resolveRelayPaths` signature returns `[]relayplan.Path`. Keep `relay` if the file still references `relay.Hop`, `relay.DialOptions`, or other relay package types outside the replaced helper.

- [ ] **Step 3: Replace L4 rule relay detection**

Replace:

```go
func ruleUsesRelay(rule model.L4Rule) bool {
	return len(rule.RelayChain) > 0 || len(rule.RelayLayers) > 0
}
```

with:

```go
func ruleUsesRelay(rule model.L4Rule) bool {
	return relayroute.UsesRelay(rule.RelayChain, rule.RelayLayers)
}
```

- [ ] **Step 4: Replace L4 relay path resolution body**

Replace `resolveRelayPaths` in `go-agent/internal/l4/server.go` with:

```go
func (s *Server) resolveRelayPaths(rule model.L4Rule) ([]relayplan.Path, error) {
	label := fmt.Sprintf("l4 rule %s:%d", rule.ListenHost, rule.ListenPort)
	return relayroute.ResolvePathsFromMapWithLabel(label, rule.RelayChain, rule.RelayLayers, s.relayListenersByID, "")
}
```

Before making this replacement, extend `go-agent/internal/relayroute/relayroute.go` with this helper:

```go
func ResolvePathsFromMapWithLabel(label string, chain []int, layers [][]int, listenersByID map[int]model.RelayListener, target string) ([]relayplan.Path, error) {
	paths, err := ResolvePathsFromMap(chain, layers, listenersByID, target)
	if err != nil {
		if strings.TrimSpace(label) != "" {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		return nil, err
	}
	return paths, nil
}
```

Run `gofmt` after adding the helper.

- [ ] **Step 5: Delete duplicated relay endpoint helper**

Delete the local `relayHopDialEndpoint` function from `go-agent/internal/l4/server.go`.

- [ ] **Step 6: Run focused L4 and relayroute tests**

Run:

```powershell
cd go-agent
gofmt -w internal/relayroute/relayroute.go internal/l4/copy.go internal/l4/server.go
go test ./internal/relayroute ./internal/l4
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/relayroute/relayroute.go go-agent/internal/l4/copy.go go-agent/internal/l4/server.go
git commit -m "refactor(agent): reuse shared helpers in l4 proxy"
```

Expected: commit succeeds.

## Task 8: Migrate Relay Copy Helpers

**Files:**
- Modify: `go-agent/internal/relay/copy.go`
- Test: `go-agent/internal/relay/runtime_test.go`
- Test: `go-agent/internal/relay/traffic_test.go`
- Test: `go-agent/internal/relay/perf_bench_test.go`

- [ ] **Step 1: Replace relay copy helper implementation**

Replace the full contents of `go-agent/internal/relay/copy.go` with:

```go
package relay

import (
	"io"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

const relayTrafficFlushThreshold uint64 = 32 * 1024

func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyPreferReaderFrom(dst, src)
}

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return stream.CopyGeneric(dst, src)
}

func copyRelayTraffic(dst io.Writer, src io.Reader, rxDirection bool, recorder *traffic.Recorder) (int64, error) {
	direction := stream.DirectionTX
	if rxDirection {
		direction = stream.DirectionRX
	}
	writer := stream.NewTrafficWriterFlushBelow(dst, direction, recorder, relayTrafficFlushThreshold)
	n, err := stream.CopyGeneric(writer, src)
	writer.FlushTraffic()
	return n, err
}
```

- [ ] **Step 2: Run focused relay tests**

Run:

```powershell
cd go-agent
gofmt -w internal/relay/copy.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 3: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/relay/copy.go
git commit -m "refactor(agent): reuse shared stream helpers in relay"
```

Expected: commit succeeds.

## Task 9: Migrate Diagnostics Relay Path Helpers

**Files:**
- Modify: `go-agent/internal/diagnostics/relay_paths.go`
- Modify: `go-agent/internal/diagnostics/http.go`
- Test: `go-agent/internal/diagnostics/relay_paths_test.go`
- Test: `go-agent/internal/diagnostics/http_test.go`

- [ ] **Step 1: Update diagnostics imports**

In `go-agent/internal/diagnostics/relay_paths.go`, add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
```

Remove `strconv` from `relay_paths.go` if it becomes unused.

In `go-agent/internal/diagnostics/http.go`, add:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
```

- [ ] **Step 2: Replace diagnostic relay path resolution body**

Replace the body of `resolveDiagnosticRelayPaths` in `go-agent/internal/diagnostics/relay_paths.go` with:

```go
func resolveDiagnosticRelayPaths(ruleLabel string, chain []int, layers [][]int, relayListeners []model.RelayListener, target string) ([]relayplan.Path, error) {
	return relayroute.ResolvePaths(ruleLabel, chain, layers, relayListeners, target)
}
```

- [ ] **Step 3: Replace diagnostic path clone helper body**

Replace `cloneDiagnosticRelayPaths` in `go-agent/internal/diagnostics/http.go` with:

```go
func cloneDiagnosticRelayPaths(paths []relayplan.Path) []relayplan.Path {
	return relayroute.ClonePaths(paths)
}
```

- [ ] **Step 4: Delete duplicated diagnostics relay endpoint helper**

Delete the local `relayHopDialEndpoint` function from `go-agent/internal/diagnostics/http.go`.

- [ ] **Step 5: Run focused diagnostics tests**

Run:

```powershell
cd go-agent
gofmt -w internal/diagnostics/relay_paths.go internal/diagnostics/http.go
go test ./internal/diagnostics
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
cd ..
git add go-agent/internal/diagnostics/relay_paths.go go-agent/internal/diagnostics/http.go
git commit -m "refactor(agent): reuse shared relay routes in diagnostics"
```

Expected: commit succeeds.

## Task 10: Full Verification and Cleanup

**Files:**
- Verify: all files changed in Tasks 2 through 9.

- [ ] **Step 1: Search for duplicated helper remnants**

Run:

```powershell
rg -n "func relayHopDialEndpoint|func copyPreferReaderFrom|type TrafficBlockState struct|func normalizeHost|func defaultPortString|func defaultPort\\(" go-agent\\internal
```

Expected:

- No `type TrafficBlockState struct` in `proxy`, `l4`, or `relay`.
- No duplicated `relayHopDialEndpoint`.
- `copyPreferReaderFrom` remains only as package-local wrappers in `l4` and `relay`, each delegating to `stream`.
- Proxy wrapper functions may remain for local compatibility, each delegating to `netutil`.

- [ ] **Step 2: Run all go-agent tests**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run targeted benchmarks without enforcing a percentage**

Run:

```powershell
cd go-agent
go test ./internal/relay -run '^$' -bench . -benchmem
```

Expected: benchmark command completes successfully. Record notable regressions in the final implementation summary if a benchmark becomes materially worse.

- [ ] **Step 4: Inspect final diff**

Run:

```powershell
git diff --stat
git diff -- go-agent/internal/netutil go-agent/internal/stream go-agent/internal/traffic go-agent/internal/relayroute go-agent/internal/proxy go-agent/internal/l4 go-agent/internal/relay go-agent/internal/diagnostics
```

Expected: diff only contains helper extraction and call-site replacement from this Phase 1 plan.

- [ ] **Step 5: Final commit if cleanup changes remain**

If Step 1 or formatting caused changes after Task 9, run:

```powershell
git add go-agent
git commit -m "refactor(agent): finish phase 1 helper cleanup"
```

Expected: commit succeeds only when there are remaining changes. If `git status --short` is clean, do not create an empty commit.

## Notes for Implementers

- Keep wrappers in caller packages when they reduce churn. Phase 2 can delete wrappers during file splits.
- Do not change public API payloads or stored config in this plan.
- Do not tune constants unless the implementation fails existing tests and the smallest correct fix requires preserving the old value in a new location.
- If a planned exact replacement conflicts with nearby user changes, preserve the user changes and adapt the replacement locally.
