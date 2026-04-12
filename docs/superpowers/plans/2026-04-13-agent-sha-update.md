# Agent SHA Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make agent self-update decisions rely on runtime package SHA256 only, and compute the running binary SHA locally at startup.

**Architecture:** Keep the control plane schema and snapshot shape unchanged. Shift the update decision entirely into the Go agent's SHA comparison path and make config populate `RuntimePackageSHA256` from the actual executable file instead of external metadata.

**Tech Stack:** Go, standard library SHA256/file I/O, existing `go-agent` app/config tests

---

### Task 1: Lock update behavior to SHA-only in app tests

**Files:**
- Modify: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that prove `desired_version` no longer controls self-update:

```go
func TestHandlePendingUpdateSkipsWhenDesiredPackageSHAMatchesCurrentRuntime(t *testing.T) {
	cfg := Config{
		CurrentVersion:       "1.0.0",
		RuntimePackageSHA256: "same-sha",
	}
	mem := store.NewInMemory()
	app := newAppWithDeps(cfg, mem, newTestSyncClient(nil, syncResponse{}), nil, nil, nil)
	updater := &testUpdater{}
	app.updater = updater

	err := app.handlePendingUpdate(context.Background(), Snapshot{
		DesiredVersion: "2.0.0",
		VersionPackage: &model.VersionPackage{
			URL:    "https://example.com/nre-agent",
			SHA256: "same-sha",
		},
	})
	if err != nil {
		t.Fatalf("handlePendingUpdate returned error: %v", err)
	}
	if len(updater.calls) != 0 {
		t.Fatalf("expected no update when sha matches, got %+v", updater.calls)
	}
}

func TestHandlePendingUpdateUpdatesWhenDesiredPackageSHADiffers(t *testing.T) {
	cfg := Config{
		CurrentVersion:       "9.9.9",
		RuntimePackageSHA256: "old-sha",
	}
	mem := store.NewInMemory()
	app := newAppWithDeps(cfg, mem, newTestSyncClient(nil, syncResponse{}), nil, nil, nil)
	updater := &testUpdater{}
	app.updater = updater

	err := app.handlePendingUpdate(context.Background(), Snapshot{
		DesiredVersion: "9.9.9",
		VersionPackage: &model.VersionPackage{
			URL:    "https://example.com/nre-agent",
			SHA256: "new-sha",
		},
	})
	if !errors.Is(err, agentupdate.ErrRestartRequested) {
		t.Fatalf("expected restart requested, got %v", err)
	}
	if len(updater.calls) != 1 {
		t.Fatalf("expected one update call, got %d", len(updater.calls))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run "TestHandlePendingUpdate(SkipsWhenDesiredPackageSHAMatchesCurrentRuntime|UpdatesWhenDesiredPackageSHADiffers)" -count=1`
Expected: FAIL because current implementation still prefers `desired_version`.

- [ ] **Step 3: Write minimal implementation**

Update `go-agent/internal/app/app.go` so `handlePendingUpdate` only compares `snapshot.VersionPackage.SHA256` against `a.cfg.RuntimePackageSHA256`:

```go
desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
if desiredSHA == "" {
	return nil
}
currentSHA := strings.TrimSpace(a.cfg.RuntimePackageSHA256)
if currentSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
	return nil
}
```

Remove the version-based `NeedsUpdate` branch from this path.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run "TestHandlePendingUpdate(SkipsWhenDesiredPackageSHAMatchesCurrentRuntime|UpdatesWhenDesiredPackageSHADiffers)" -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/app/app.go go-agent/internal/app/app_test.go
git commit -m "fix(agent): use package sha for self-update decisions"
```

### Task 2: Compute runtime package SHA from the current executable

**Files:**
- Modify: `go-agent/internal/config/config.go`
- Modify: `go-agent/internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that prove config computes runtime SHA and degrades safely on read failure:

```go
func TestLoadFromEnvComputesRuntimePackageSHA256FromExecutable(t *testing.T) {
	execPath := filepath.Join(t.TempDir(), "nre-agent")
	payload := []byte("agent-binary")
	if err := os.WriteFile(execPath, payload, 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	t.Setenv("NRE_MASTER_URL", "https://example.com")
	t.Setenv("NRE_AGENT_TOKEN", "token")
	t.Setenv("NRE_AGENT_VERSION", "1.2.3")

	cfg, err := loadFromEnvForExecutable(execPath)
	if err != nil {
		t.Fatalf("loadFromEnvForExecutable returned error: %v", err)
	}
	if cfg.RuntimePackageSHA256 != sumSHA256(payload) {
		t.Fatalf("RuntimePackageSHA256 = %q", cfg.RuntimePackageSHA256)
	}
}

func TestLoadFromEnvLeavesRuntimePackageSHA256EmptyWhenExecutableMissing(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://example.com")
	t.Setenv("NRE_AGENT_TOKEN", "token")

	cfg, err := loadFromEnvForExecutable(filepath.Join(t.TempDir(), "missing-agent"))
	if err != nil {
		t.Fatalf("loadFromEnvForExecutable returned error: %v", err)
	}
	if cfg.RuntimePackageSHA256 != "" {
		t.Fatalf("expected empty runtime sha, got %q", cfg.RuntimePackageSHA256)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run "TestLoadFromEnv(ComputesRuntimePackageSHA256FromExecutable|LeavesRuntimePackageSHA256EmptyWhenExecutableMissing)" -count=1`
Expected: FAIL because config does not yet compute the executable SHA.

- [ ] **Step 3: Write minimal implementation**

In `go-agent/internal/config/config.go`:

```go
func LoadFromEnv() (Config, error) {
	return loadFromEnvForExecutable("")
}

func loadFromEnvForExecutable(executablePath string) (Config, error) {
	cfg := Default()
	// existing env loading
	cfg.RuntimePackageSHA256 = executableSHA256(executablePath)
	return cfg, nil
}
```

Add helpers that resolve the current executable when no override path is provided and compute lowercase SHA256 hex from file bytes. Return `""` on any resolution/read error.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run "TestLoadFromEnv(ComputesRuntimePackageSHA256FromExecutable|LeavesRuntimePackageSHA256EmptyWhenExecutableMissing)" -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/config/config.go go-agent/internal/config/config_test.go
git commit -m "fix(agent): derive runtime package sha from executable"
```

### Task 3: Full verification

**Files:**
- Modify: `go-agent/internal/app/app_test.go`
- Modify: `go-agent/internal/config/config_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/config/config.go`

- [ ] **Step 1: Run focused tests**

Run: `go test ./internal/app ./internal/config -count=1`
Expected: PASS

- [ ] **Step 2: Run full go-agent suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Inspect git status**

Run: `git status --short`
Expected: only intended files changed before the final commit, then clean after the final commit.

- [ ] **Step 4: Commit final verification state if needed**

```bash
git add go-agent/internal/app/app.go go-agent/internal/app/app_test.go go-agent/internal/config/config.go go-agent/internal/config/config_test.go docs/superpowers/specs/2026-04-13-agent-sha-update-design.md docs/superpowers/plans/2026-04-13-agent-sha-update.md
git commit -m "docs: record sha-based agent update plan"
```
