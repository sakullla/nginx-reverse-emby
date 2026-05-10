# Go Agent Phase 2 HTTP Proxy Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `go-agent/internal/proxy/server.go` into responsibility-focused files without changing HTTP proxy behavior.

**Architecture:** Keep package name `proxy` and public APIs stable. Move existing types and functions into smaller same-package files so Go package visibility, tests, and call sites remain unchanged. Each task is a move-only batch with focused `go test ./internal/proxy` verification before committing.

**Tech Stack:** Go standard library, existing `go-agent/internal/proxy` package, `gofmt`, `go test`, git.

---

## Scope

This plan implements the HTTP proxy portion of Phase 2 from `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`.

It does not change proxy behavior, timeout defaults, relay selection behavior, traffic accounting thresholds, request rewriting, retry semantics, or public API payloads. It does not split L4, relay, diagnostics, app runtime, or control-plane code.

Because this is a same-package move-only refactor, the safety signal is existing regression coverage plus compile checks after each batch. Do not edit function bodies except for import cleanup forced by moving code between files.

## File Map

- Keep and shrink: `go-agent/internal/proxy/server.go`
  - Owns `Server`, `Providers`, `routeEntry`, `httpBackend`, server construction, `ServeHTTP`, route lookup, and request serving orchestration.
- Create: `go-agent/internal/proxy/runtime.go`
  - Owns `Runtime`, listener spec construction, runtime start/close, binding keys, inbound TLS listener setup, and runtime rule binding helpers.
- Create: `go-agent/internal/proxy/relay_paths.go`
  - Owns HTTP proxy relay transport construction, relay path racer, relay path resolution wrappers, selected relay connection tracing, and relay dial context helpers.
- Create: `go-agent/internal/proxy/backends.go`
  - Owns HTTP backend parsing, candidate ordering, retry classification, backend success/failure observation, and backend dial address helpers.
- Create: `go-agent/internal/proxy/request.go`
  - Owns reusable request body handling, proxy request cloning, request-body traffic read closer, and request URL clone helper.
- Create: `go-agent/internal/proxy/response.go`
  - Owns normal response copy, upgrade response handling, switch-protocol copy, response traffic wrappers, header copy/filter helpers, and upgrade header detection.
- Test unchanged: `go-agent/internal/proxy/*_test.go`
  - Existing proxy tests are the behavior guard. Do not rename tests in this plan.

## Task 1: Baseline And Move Boundary Audit

**Files:**
- Read: `go-agent/internal/proxy/server.go`
- Read: `go-agent/internal/proxy/*_test.go`

- [ ] **Step 1: Confirm clean worktree**

Run:

```powershell
git status --short
```

Expected: no output.

- [ ] **Step 2: Run focused proxy baseline**

Run:

```powershell
cd go-agent
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 3: Record current server function map**

Run:

```powershell
rg -n "^(type|const|var|func) " internal/proxy/server.go
```

Expected: output includes `Runtime`, `StartWithResourcesAndOptions`, `newRelayTransports`, `prepareReusableBody`, `copyResponse`, and selected relay helpers. Use this list to verify moved code has not been dropped.

## Task 2: Move Runtime And Listener Startup Code

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/runtime.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Create `runtime.go` with moved runtime code**

Move these declarations from `server.go` into `runtime.go` without changing bodies:

```go
type Runtime struct
type runtimeListenerSpec struct
func ValidateRules
func BindingKeys
func Start
func StartWithResources
func StartWithResourcesAndOptions
func (r *Runtime) Close
func (r *Runtime) BindingKeys
func (r *Runtime) SetTrafficBlockState
func buildRuntimeListenerSpecs
func validateRelayChain
type runtimeRuleBinding struct
func runtimeRuleSpec
func newInboundTLSConfig
func newTLSListener
```

Use this import set as the starting point for `runtime.go`, then let `gofmt` and the compiler identify unused imports:

```go
package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`. Keep `Server`, `Providers`, `routeEntry`, `httpBackend`, `NewServer`, `newServer`, `newServerWithResilience`, `ServeHTTP`, `routeFor`, and `serveHTTP` in `server.go`.

- [ ] **Step 3: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/server.go internal/proxy/runtime.go
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit runtime split**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/runtime.go
git commit -m "refactor(proxy): split runtime startup code"
```

Expected: commit succeeds.

## Task 3: Move Relay Transport And Relay Selection Code

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/relay_paths.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Create `relay_paths.go` with moved relay code**

Move these declarations from `server.go` into `relay_paths.go` without changing bodies:

```go
type relayPathDialer struct
func (d relayPathDialer) DialPath
func newRelayTransports
func newRelayPathRacer
func resolveRelayHops
func ruleUsesRelay
func resolveRelayPaths
func cloneRelayPlanPaths
type dialAddressContextKey struct
type selectedRelayAddressContextKey struct
type selectedRelayAddressHolder struct
type selectedRelayConn struct
func newSelectedRelayConn
func (c *selectedRelayConn) selectedRelaySelection
func (c *selectedRelayConn) ConnectionState
func withDialAddress
func dialAddressFromContext
func withSelectedRelayAddressHolder
func withSelectedRelayConnTrace
func setSelectedRelaySelection
func selectedRelaySelectionFromContext
func selectedRelaySelectionFromConn
func (h *selectedRelayAddressHolder) set
func (h *selectedRelayAddressHolder) get
func requestContext
func mapValues
```

Use this import set as the starting point:

```go
package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayplan"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relayroute"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/server.go internal/proxy/relay_paths.go
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit relay split**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/relay_paths.go
git commit -m "refactor(proxy): split relay path handling"
```

Expected: commit succeeds.

## Task 4: Move Backend Candidate And Retry Code

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/backends.go`
- Test: `go-agent/internal/proxy/server_test.go`
- Test: `go-agent/internal/proxy/traffic_test.go`

- [ ] **Step 1: Create `backends.go` with moved backend code**

Move these declarations from `server.go` into `backends.go` without changing bodies:

```go
func (e *routeEntry) transportForRequest
func (e *routeEntry) sameBackendRetryMaxAttempts
func (e *routeEntry) observeSuccessfulBackend
func (e *routeEntry) markCandidateFailure
func (e *routeEntry) closeRelayIdleConnections
func isRetrySafeMethod
type httpCandidate struct
func (e *routeEntry) candidates
func cloneDefaultTransport
func cloneTransport
func NewSharedTransport
func parseHTTPBackends
func isBackendRetryable
func backendRetryError
type startedResponseError struct
func (e *startedResponseError) Error
func (e *startedResponseError) Unwrap
func newStartedResponseError
func portWithDefault
func addressWithDefaultPort
func httpBackendDialAddress
func defaultPort
func defaultPortString
```

Use this import set as the starting point:

```go
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/netutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/server.go internal/proxy/backends.go
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit backend split**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/backends.go
git commit -m "refactor(proxy): split backend candidate code"
```

Expected: commit succeeds.

## Task 5: Move Request Body And Request Clone Code

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/request.go`
- Test: `go-agent/internal/proxy/traffic_test.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Create `request.go` with moved request code**

Move these declarations from `server.go` into `request.go` without changing bodies:

```go
type reusableRequestBody struct
func prepareReusableBody
func (b *reusableRequestBody) Open
func (b *reusableRequestBody) Close
func cloneProxyRequest
type trafficReadCloser struct
func newTrafficReadCloser
func httpRecorderOrAggregate
func (c trafficReadCloser) Read
func (c trafficReadCloser) Close
func cloneURL
```

Use this import set as the starting point:

```go
package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/url"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/server.go internal/proxy/request.go
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit request split**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/request.go
git commit -m "refactor(proxy): split request cloning code"
```

Expected: commit succeeds.

## Task 6: Move Response Copy And Upgrade Code

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/response.go`
- Test: `go-agent/internal/proxy/traffic_test.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Create `response.go` with moved response code**

Move these declarations from `server.go` into `response.go` without changing bodies:

```go
func copyResponse
func handleUpgradeResponse
var errCopyDone
type switchProtocolCopier struct
func (c switchProtocolCopier) copyFromBackend
func (c switchProtocolCopier) copyToBackend
func copySwitchProtocolTraffic
const httpResponseTrafficFlushThreshold
func newHTTPResponseTrafficWriter
type httpResponseTrafficWriter struct
func (w *httpResponseTrafficWriter) Write
func (w *httpResponseTrafficWriter) FlushTraffic
func newHTTPResponseTrafficResponseWriter
type httpResponseTrafficResponseWriter struct
func (w *httpResponseTrafficResponseWriter) Write
func (w *httpResponseTrafficResponseWriter) Flush
func (w *httpResponseTrafficResponseWriter) FlushTraffic
func (w *httpResponseTrafficResponseWriter) Unwrap
type httpResponseTrafficFlusher struct
func newHTTPResponseTrafficFlusher
func (f *httpResponseTrafficFlusher) Add
func (f *httpResponseTrafficFlusher) Flush
func copyHeaders
func copyProxyResponseHeaders
func shouldStripProxyResponseHeader
func hopByHopHeaders
func upgradeType
```

Use this import set as the starting point:

```go
package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run proxy tests**

Run:

```powershell
cd go-agent
gofmt -w internal/proxy/server.go internal/proxy/response.go
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit response split**

Run:

```powershell
cd ..
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/response.go
git commit -m "refactor(proxy): split response copy code"
```

Expected: commit succeeds.

## Task 7: Final Proxy Split Verification

**Files:**
- Verify: `go-agent/internal/proxy/*.go`
- Verify: `go-agent/internal/proxy/*_test.go`

- [ ] **Step 1: Check remaining `server.go` top-level declarations**

Run:

```powershell
rg -n "^(type|const|var|func) " go-agent/internal/proxy/server.go
```

Expected: `server.go` contains server core declarations only: `Server`, provider interfaces, `routeEntry`, `httpBackend`, `NewServer`, `newServer`, `newServerWithResilience`, `ServeHTTP`, `currentTrafficBlockState`, `SetTrafficBlockState`, `routeFor`, and `serveHTTP`.

- [ ] **Step 2: Run all proxy tests**

Run:

```powershell
cd go-agent
go test ./internal/proxy
```

Expected: package reports `ok`.

- [ ] **Step 3: Run all go-agent tests**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: every package reports `ok` or `[no test files]`.

- [ ] **Step 4: Inspect final diff stat**

Run:

```powershell
git diff --stat HEAD~5..HEAD -- go-agent/internal/proxy
```

Expected: primarily moved code across new proxy files. No unrelated packages.

- [ ] **Step 5: Commit any cleanup if needed**

If formatting or import cleanup changed files after the task commits, run:

```powershell
git add go-agent/internal/proxy
git commit -m "refactor(proxy): finish http proxy split"
```

Expected: commit succeeds only when `git status --short` shows remaining proxy changes. Do not create an empty commit.

## Self-Review Notes

- Spec coverage: this plan covers the HTTP proxy portion of Phase 2. L4, relay, diagnostics, and app runtime splits require separate plans.
- Placeholder scan: no TBD/TODO placeholders are intentionally left.
- Type consistency: all moved declarations keep their original names and stay in package `proxy`, so existing tests and package-private call sites remain valid.
