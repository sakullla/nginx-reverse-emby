# Go Agent Phase 2 L4 Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `go-agent/internal/l4/server.go` into responsibility-focused files without changing L4 proxy behavior.

**Architecture:** Keep package name `l4` and public APIs stable. Move existing declarations into smaller same-package files so package-private references remain valid. Each task is move-only, with `gofmt`, focused `go test ./internal/l4`, and a narrow commit before proceeding.

**Tech Stack:** Go standard library, existing `go-agent/internal/l4` package, `gofmt`, `go test`, git.

---

## Scope

This plan implements the L4 proxy portion of Phase 2 from `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`.

It does not change protocol behavior, timeout defaults, UDP session behavior, relay path behavior, candidate ordering, traffic accounting, proxy protocol handling, public API payloads, or stored configuration. It does not split HTTP proxy, relay runtime, diagnostics, app runtime, or control-plane code.

Because this is a same-package move-only refactor, the safety signal is existing regression coverage plus compile checks after each batch. Do not edit function bodies except for import cleanup forced by moving code between files.

## File Map

- Keep and shrink: `go-agent/internal/l4/server.go`
  - Owns core `Server`, material provider interface, constructor setup, traffic block state accessors, `Close`, TCP connection tracking, and server-wide cleanup helpers.
- Create: `go-agent/internal/l4/tcp.go`
  - Owns TCP listener startup, TCP accept loop, TCP connection handling, proxy-entry TCP handling, bidirectional TCP copying, L4 traffic copy helper, relay initial payload handling, proxy protocol write/read helpers, and TCP close helpers.
- Create: `go-agent/internal/l4/udp.go`
  - Owns UDP session state and upstream adapters, UDP listener startup, UDP packet processing, UDP upstream dialing, reply piping, UDP backoff observation hooks, UDP timeout/expiry helpers, and UDP session cleanup.
- Create: `go-agent/internal/l4/candidates.go`
  - Owns `l4Candidate`, backend candidate ordering, observation-key construction, backoff address selection, and candidate success/failure observation.
- Create: `go-agent/internal/l4/relay_paths.go`
  - Owns relay path dialer, relay TCP/UDP dialing wrapper, relay path cloning, relay validation/resolution, rule relay detection, and relay listener input-change helpers.
- Test unchanged: `go-agent/internal/l4/*_test.go`
  - Existing L4 tests are the behavior guard. Do not rename tests in this plan.

## Task 1: Baseline And Move Boundary Audit

**Files:**
- Read: `go-agent/internal/l4/server.go`
- Read: `go-agent/internal/l4/*_test.go`

- [ ] **Step 1: Confirm clean worktree**

Run:

```powershell
git status --short
```

Expected: no output.

- [ ] **Step 2: Run focused L4 baseline**

Run:

```powershell
cd go-agent
go test ./internal/l4
```

Expected: package reports `ok`.

- [ ] **Step 3: Record current server declaration map**

Run:

```powershell
rg -n "^(type|const|var|func) " internal/l4/server.go
```

Expected: output includes `handleTCPConnection`, `udpSession`, `l4Candidates`, `validateRelayChain`, and `RelayInputsChanged`. Use this list to verify moved code has not been dropped.

## Task 2: Move TCP Handling Code

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Create: `go-agent/internal/l4/tcp.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/l4/traffic_test.go`

- [ ] **Step 1: Create `tcp.go` with moved TCP code**

Move these declarations from `server.go` into `tcp.go` without changing bodies:

```go
func (s *Server) startTCPListener
func (s *Server) tcpAcceptLoop
func (s *Server) handleTCPConnection
func (s *Server) handleProxyEntryConnection
func (s *Server) dialProxyEntryUpstream
func copyBidirectionalTCP
func l4RecorderOrAggregate
func copyL4TCP
func (s *Server) prefetchRelayInitialPayload
func relayTCPDialTrafficClass
func (s *Server) prepareTCPDownstream
func (s *Server) writeTCPProxyHeader
func proxyInfoFromConn
func cloneTCPAddr
func closeTCPWrite
func closeTCPRead
```

Use this import set as the starting point for `tcp.go`, then let `gofmt` and the compiler identify unused imports:

```go
package l4

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/stream"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run L4 tests**

Run:

```powershell
cd go-agent
gofmt -w internal/l4/server.go internal/l4/tcp.go
go test ./internal/l4
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit TCP split**

Run:

```powershell
cd ..
git add go-agent/internal/l4/server.go go-agent/internal/l4/tcp.go
git commit -m "refactor(l4): split tcp handling"
```

Expected: commit succeeds.

## Task 3: Move UDP Handling Code

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Create: `go-agent/internal/l4/udp.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/l4/traffic_test.go`

- [ ] **Step 1: Create `udp.go` with moved UDP code**

Move these declarations from `server.go` into `udp.go` without changing bodies:

```go
type udpSession struct
type udpUpstream interface
type directUDPUpstream struct
func (u *directUDPUpstream) Close
func (u *directUDPUpstream) SetReadDeadline
func (u *directUDPUpstream) SetWriteDeadline
func (u *directUDPUpstream) ReadPacket
func (u *directUDPUpstream) WritePacket
type relayUDPUpstream struct
func (u *relayUDPUpstream) Close
func (u *relayUDPUpstream) SetReadDeadline
func (u *relayUDPUpstream) SetWriteDeadline
func (u *relayUDPUpstream) ReadPacket
func (u *relayUDPUpstream) WritePacket
func (s *Server) startUDPListener
func (s *Server) udpReadLoop
func (s *Server) proxyUDPPacket
func (s *Server) sessionForPeer
func (s *Server) dialUDPUpstream
func (s *Server) pipeUDPReplies
func (s *Server) markUDPSessionWrite
func (s *Server) markUDPSessionReply
func (s *Server) shouldFailUDPSession
func (s *Server) udpReplyDuration
func (s *Server) udpReplyTimeoutForCandidate
func (s *Server) shouldExpireUDPSession
func (s *Server) closeUDPSession
func (s *Server) closeUDPSessions
func cloneUDPAddr
```

Use this import set as the starting point:

```go
package l4

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run L4 tests**

Run:

```powershell
cd go-agent
gofmt -w internal/l4/server.go internal/l4/udp.go
go test ./internal/l4
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit UDP split**

Run:

```powershell
cd ..
git add go-agent/internal/l4/server.go go-agent/internal/l4/udp.go
git commit -m "refactor(l4): split udp handling"
```

Expected: commit succeeds.

## Task 4: Move Candidate Ordering And Observation Code

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Create: `go-agent/internal/l4/candidates.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/l4/traffic_test.go`

- [ ] **Step 1: Create `candidates.go` with moved candidate code**

Move these declarations from `server.go` into `candidates.go` without changing bodies:

```go
type l4Candidate struct
func l4Candidates
func l4ObservationKey
func l4CandidateBackoffAddr
func (s *Server) observeCandidateFailure
func (s *Server) observeCandidateSuccess
```

Use this import set as the starting point:

```go
package l4

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

- [ ] **Step 2: Remove moved declarations from `server.go`**

Delete only the declarations listed in Step 1 from `server.go`.

- [ ] **Step 3: Format and run L4 tests**

Run:

```powershell
cd go-agent
gofmt -w internal/l4/server.go internal/l4/candidates.go
go test ./internal/l4
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit candidate split**

Run:

```powershell
cd ..
git add go-agent/internal/l4/server.go go-agent/internal/l4/candidates.go
git commit -m "refactor(l4): split candidate selection"
```

Expected: commit succeeds.

## Task 5: Move Relay Path Handling Code

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Create: `go-agent/internal/l4/relay_paths.go`
- Test: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Create `relay_paths.go` with moved relay code**

Move these declarations from `server.go` into `relay_paths.go` without changing bodies:

```go
type relayPathDialer struct
func (d relayPathDialer) DialPath
func (s *Server) dialTCPUpstream
func (s *Server) dialRelayPath
func cloneRelayPlanPaths
func (s *Server) validateRelayChain
func (s *Server) resolveRelayHops
func (s *Server) resolveRelayPaths
func ruleUsesRelay
func RelayInputsChanged
func relayListenerChangedByID
func relayListenerByID
```

Use this import set as the starting point:

```go
package l4

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"time"

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

- [ ] **Step 3: Format and run L4 tests**

Run:

```powershell
cd go-agent
gofmt -w internal/l4/server.go internal/l4/relay_paths.go
go test ./internal/l4
```

Expected: package reports `ok`.

- [ ] **Step 4: Commit relay split**

Run:

```powershell
cd ..
git add go-agent/internal/l4/server.go go-agent/internal/l4/relay_paths.go
git commit -m "refactor(l4): split relay path handling"
```

Expected: commit succeeds.

## Task 6: Final L4 Split Verification

**Files:**
- Verify: `go-agent/internal/l4/*.go`
- Verify: `go-agent/internal/l4/*_test.go`

- [ ] **Step 1: Check remaining `server.go` top-level declarations**

Run:

```powershell
rg -n "^(type|const|var|func) " go-agent/internal/l4/server.go
```

Expected: `server.go` contains core server declarations only: constants, `RelayMaterialProvider`, `Server`, `NewServer`, `NewServerWithResources`, `currentTrafficBlockState`, `SetTrafficBlockState`, `Close`, TCP tracking helpers, and server-wide cleanup helpers.

- [ ] **Step 2: Run all L4 tests**

Run:

```powershell
cd go-agent
go test ./internal/l4
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
git diff --stat HEAD~4..HEAD -- go-agent/internal/l4
```

Expected: primarily moved code across new L4 files. No unrelated packages.

- [ ] **Step 5: Commit any cleanup if needed**

If formatting or import cleanup changed files after the task commits, run:

```powershell
git add go-agent/internal/l4
git commit -m "refactor(l4): finish l4 split"
```

Expected: commit succeeds only when `git status --short` shows remaining L4 changes. Do not create an empty commit.

## Self-Review Notes

- Spec coverage: this plan covers the L4 proxy portion of Phase 2. Relay runtime, diagnostics, and app runtime splits require separate plans.
- Placeholder scan: no TBD/TODO placeholders are intentionally left.
- Type consistency: all moved declarations keep their original names and stay in package `l4`, so existing tests and package-private call sites remain valid.
