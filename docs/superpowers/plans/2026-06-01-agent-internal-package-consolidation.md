# Agent Internal Package Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce `go-agent/internal` package count by merging runtime state, control-plane communication, and platform helper packages without changing agent behavior.

**Architecture:** Move snapshot runtime state into `core`, replace separate `sync` and `task` packages with a single `control` package, and flatten Linux platform helpers into `platform`. Preserve data-plane boundaries for backend selection, proxy protocol, upstream planning, stream accounting, config, and store.

**Tech Stack:** Go modules, standard Go build tags, `go test ./...`, PowerShell commands on Windows.

---

## File Structure

- Modify: `go-agent/internal/core/runtime.go`
  - New home for the current `internal/runtime` implementation.
- Modify: `go-agent/internal/core/activation.go`
  - Return `core.Activator` instead of importing `internal/runtime`.
- Modify: `go-agent/internal/core/sync_controller.go`
  - Replace `internal/runtime` and `internal/sync` imports with local `core.Runtime` and `internal/control`.
- Create: `go-agent/internal/control/sync_client.go`
  - Moved heartbeat/sync client from `internal/sync/client.go`.
- Create: `go-agent/internal/control/task_client.go`
  - Moved task stream/SSE client from `internal/task/client.go`.
- Create: `go-agent/internal/control/task_protocol.go`
  - Moved task protocol message types.
- Create: `go-agent/internal/control/task_types.go`
  - Moved diagnostic task constants.
- Create: `go-agent/internal/control/http.go`
  - Shared master URL normalization and HTTP transport construction.
- Modify: `go-agent/internal/app/*.go`, `go-agent/embedded/*.go`, `go-agent/internal/modules/diagnostics/*.go`
  - Update imports and type references from `sync`/`task`/`runtime` to `control`/`core`.
- Create: `go-agent/internal/platform/service_linux.go`
  - Linux `ExecReplacement` and `ServiceUnitName`.
- Create: `go-agent/internal/platform/service_default.go`
  - Non-Linux fallback.
- Delete: `go-agent/internal/runtime/*`, `go-agent/internal/sync/*`, `go-agent/internal/task/*`, `go-agent/internal/platform/linux/*`, `go-agent/internal/platform/windows/*`.

## Task 1: Move Runtime Into Core

**Files:**
- Create: `go-agent/internal/core/runtime.go`
- Modify: `go-agent/internal/core/activation.go`
- Modify: `go-agent/internal/core/sync_controller.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/snapshot_activation.go`
- Delete: `go-agent/internal/runtime/runtime.go`
- Move tests: `go-agent/internal/runtime/*_test.go` to `go-agent/internal/core/*_test.go`

- [ ] **Step 1: Move runtime implementation**

Move `go-agent/internal/runtime/runtime.go` to `go-agent/internal/core/runtime.go` and change the package declaration:

```go
package core
```

- [ ] **Step 2: Update activation helper**

Change `go-agent/internal/core/activation.go` to use the local type:

```go
func NewSnapshotActivator(modules ModuleApplier) Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if modules == nil {
			return nil
		}
		return modules.Apply(ctx, previous, next)
	}
}
```

- [ ] **Step 3: Update app/core runtime references**

Replace `*agentruntime.Runtime` with `*core.Runtime` in app code, and replace constructors with:

```go
core.NewRuntimeWithActivator(appSnapshotActivator(nil))
```

Within `core`, replace `*agentruntime.Runtime` with:

```go
*Runtime
```

- [ ] **Step 4: Move runtime tests**

Move each `internal/runtime/*_test.go` file into `internal/core/` and change package declarations from `runtime` to `core`.

- [ ] **Step 5: Run focused tests**

Run:

```powershell
cd go-agent
go test ./internal/core ./internal/app
```

Expected: both packages pass.

## Task 2: Merge Sync And Task Into Control

**Files:**
- Create: `go-agent/internal/control/http.go`
- Create: `go-agent/internal/control/sync_client.go`
- Create: `go-agent/internal/control/task_client.go`
- Create: `go-agent/internal/control/task_protocol.go`
- Create: `go-agent/internal/control/task_types.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/core/sync_controller.go`
- Modify: `go-agent/embedded/diagnostics.go`
- Modify: `go-agent/internal/modules/diagnostics/handler.go`
- Modify: `go-agent/internal/modules/diagnostics/module.go`
- Delete: `go-agent/internal/sync/*`
- Delete: `go-agent/internal/task/*`

- [ ] **Step 1: Move files into control**

Move sync and task source files into `go-agent/internal/control/` and change every moved file to:

```go
package control
```

Rename `client.go` files after moving so both can coexist:

```text
internal/sync/client.go -> internal/control/sync_client.go
internal/task/client.go -> internal/control/task_client.go
```

- [ ] **Step 2: Add shared HTTP helpers**

Create `go-agent/internal/control/http.go`:

```go
package control

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

func normalizeMasterBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasSuffix(trimmed, "/panel-api"):
		return strings.TrimSuffix(trimmed, "/panel-api")
	case strings.HasSuffix(trimmed, "/api"):
		return strings.TrimSuffix(trimmed, "/api")
	default:
		return trimmed
	}
}

func resolvedHTTPTransportConfig(cfg config.HTTPTransportConfig) config.HTTPTransportConfig {
	transportCfg := config.Default().HTTPTransport
	if cfg.DialTimeout > 0 {
		transportCfg.DialTimeout = cfg.DialTimeout
	}
	if cfg.TLSHandshakeTimeout > 0 {
		transportCfg.TLSHandshakeTimeout = cfg.TLSHandshakeTimeout
	}
	if cfg.ResponseHeaderTimeout > 0 {
		transportCfg.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	}
	if cfg.IdleConnTimeout > 0 {
		transportCfg.IdleConnTimeout = cfg.IdleConnTimeout
	}
	if cfg.KeepAlive > 0 {
		transportCfg.KeepAlive = cfg.KeepAlive
	}
	return transportCfg
}

func newHTTPTransport(cfg config.HTTPTransportConfig) *http.Transport {
	transportCfg := resolvedHTTPTransportConfig(cfg)
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   transportCfg.DialTimeout,
			KeepAlive: transportCfg.KeepAlive,
		}).DialContext,
		TLSHandshakeTimeout:   transportCfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: transportCfg.ResponseHeaderTimeout,
		IdleConnTimeout:       transportCfg.IdleConnTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
```

- [ ] **Step 3: Deduplicate client constructors**

In `sync_client.go`, replace inline transport construction with:

```go
transport := newHTTPTransport(cfg.HTTPTransport)
client := &http.Client{
	Transport: transport,
	Timeout:   60 * time.Second,
}
```

In `task_client.go`, replace inline transport construction with:

```go
transport := newHTTPTransport(cfg.HTTPTransport)
cfg.HTTPClient = &http.Client{Transport: transport}
```

- [ ] **Step 4: Update imports**

Replace imports of:

```go
github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync
github.com/sakullla/nginx-reverse-emby/go-agent/internal/task
```

with:

```go
github.com/sakullla/nginx-reverse-emby/go-agent/internal/control
```

Update references such as `agentsync.SyncRequest` and `agenttask.TaskMessage` to `control.SyncRequest` and `control.TaskMessage`.

- [ ] **Step 5: Move tests and run focused tests**

Move `internal/sync/*_test.go` and `internal/task/*_test.go` to `internal/control/`, update package declarations to `control`, then run:

```powershell
cd go-agent
go test ./internal/control ./internal/core ./internal/app ./internal/modules/diagnostics ./embedded
```

Expected: all listed packages pass.

## Task 3: Flatten Platform Helpers

**Files:**
- Create: `go-agent/internal/platform/service_linux.go`
- Create: `go-agent/internal/platform/service_default.go`
- Modify: `go-agent/internal/app/app.go`
- Delete: `go-agent/internal/platform/linux/service.go`
- Delete: `go-agent/internal/platform/linux/service_stub.go`
- Delete: `go-agent/internal/platform/windows/update_helper.go`

- [ ] **Step 1: Add Linux implementation**

Create `go-agent/internal/platform/service_linux.go`:

```go
//go:build linux

package platform

import "syscall"

func ServiceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func ExecReplacement(binary string, argv []string, env []string) error {
	return syscall.Exec(binary, argv, env)
}
```

- [ ] **Step 2: Add non-Linux fallback**

Create `go-agent/internal/platform/service_default.go`:

```go
//go:build !linux

package platform

import "fmt"

func ServiceUnitName() string {
	return "nginx-reverse-emby-agent"
}

func ExecReplacement(binary string, argv []string, env []string) error {
	return fmt.Errorf("exec replacement is only supported on linux")
}
```

- [ ] **Step 3: Update app import**

Replace the app import alias:

```go
platformlinux "github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform/linux"
```

with:

```go
"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
```

Then replace:

```go
platformlinux.ExecReplacement
```

with:

```go
platform.ExecReplacement
```

- [ ] **Step 4: Run focused build test**

Run:

```powershell
cd go-agent
go test ./internal/platform ./internal/app
```

Expected: both packages pass.

## Task 4: Remove Old Package Directories And Verify All Imports

**Files:**
- Delete empty/obsolete directories under `go-agent/internal/runtime`, `go-agent/internal/sync`, `go-agent/internal/task`, `go-agent/internal/platform/linux`, `go-agent/internal/platform/windows`.
- Modify any remaining Go files found by import search.

- [ ] **Step 1: Search for obsolete imports**

Run:

```powershell
rg -n "go-agent/internal/(runtime|sync|task|platform/linux|platform/windows)" go-agent -g "*.go"
```

Expected: no output.

- [ ] **Step 2: Format changed Go files**

Run:

```powershell
cd go-agent
gofmt -w internal/core internal/control internal/app internal/modules/diagnostics embedded internal/platform
```

Expected: command exits successfully.

- [ ] **Step 3: Run full agent test suite**

Run:

```powershell
cd go-agent
go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Review package count**

Run:

```powershell
Get-ChildItem -Directory go-agent\internal | Select-Object -ExpandProperty Name | Sort-Object
```

Expected: `runtime`, `sync`, and `task` are gone; `control` and `platform` remain.
