# Agent Uninstall Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install a stable host-local uninstall command for Go agent installs without changing uninstall cleanup scope.

**Architecture:** Persist the installed `join-agent.sh` into the agent data directory, then generate a fixed `/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh` wrapper that delegates to the persisted script's `uninstall-agent` command with resolved install paths. Keep all uninstall cleanup logic in `scripts/join-agent.sh` and only expose the new entrypoint plus docs/tests.

**Tech Stack:** POSIX `sh`, Go `testing`, README docs

---

### Task 1: Lock the new join-script behavior with tests

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/public_test.go`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJoinScriptInstallsStableUninstallWrapper(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, `UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"`) {
		t.Fatalf("join-agent.sh missing uninstall wrapper path")
	}
	if !strings.Contains(script, "install_uninstall_wrapper()") {
		t.Fatalf("join-agent.sh missing uninstall wrapper installer")
	}
	if !strings.Contains(script, `exec "$JOIN_SCRIPT_PATH" uninstall-agent --data-dir "$DATA_DIR"`) {
		t.Fatalf("join-agent.sh missing uninstall wrapper delegation")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run 'TestJoinScript(InstallsStableUninstallWrapper|IncludesUninstallAndLegacyNginxCleanup)' -count=1`
Expected: FAIL because the current public script does not yet expose the stable uninstall wrapper path/installer/delegation.

- [ ] **Step 3: Write minimal implementation**

```sh
JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"
UNINSTALL_WRAPPER_PATH="/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh"

persist_join_script() {
    cat > "$JOIN_SCRIPT_PATH" <<'EOF'
...current script content...
EOF
    chmod 755 "$JOIN_SCRIPT_PATH"
}

install_uninstall_wrapper() {
    cat <<EOF | run_root_cmd tee "$UNINSTALL_WRAPPER_PATH" >/dev/null
#!/bin/sh
set -eu
exec $(shell_quote "$JOIN_SCRIPT_PATH") uninstall-agent --data-dir $(shell_quote "$DATA_DIR")
EOF
    run_root_cmd chmod 755 "$UNINSTALL_WRAPPER_PATH"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run 'TestJoinScript(InstallsStableUninstallWrapper|IncludesUninstallAndLegacyNginxCleanup)' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/public_test.go scripts/join-agent.sh
git commit -m "feat(agent): install local uninstall wrapper"
```

### Task 2: Persist the join script and install the wrapper during agent install

**Files:**
- Modify: `scripts/join-agent.sh`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Write the failing test**

```go
if !strings.Contains(script, `JOIN_SCRIPT_PATH="$BIN_DIR/join-agent.sh"`) {
	t.Fatalf("join-agent.sh missing persisted join script path")
}
if !strings.Contains(script, "persist_installed_join_script()") {
	t.Fatalf("join-agent.sh missing join script persistence helper")
}
if !strings.Contains(script, "install_uninstall_wrapper") {
	t.Fatalf("join-agent.sh missing uninstall wrapper install call")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestJoinScriptInstallsStableUninstallWrapper -count=1`
Expected: FAIL because the installer does not yet persist the join script or call the wrapper installer.

- [ ] **Step 3: Write minimal implementation**

```sh
persist_installed_join_script() {
    mkdir -p "$BIN_DIR"
    cat > "$JOIN_SCRIPT_PATH" <<'EOF'
...current join-agent.sh content...
EOF
    chmod 755 "$JOIN_SCRIPT_PATH"
}

run_join() {
    ...
    persist_installed_join_script
    if [ "$INSTALL_SYSTEMD" = "1" ]; then
        install_systemd_service
    elif [ "$INSTALL_LAUNCHD" = "1" ]; then
        install_launchd_service
    else
        install_manual_runtime
    fi
}

install_systemd_service() {
    ...
    install_uninstall_wrapper
}

install_launchd_service() {
    ...
    install_uninstall_wrapper
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestJoinScriptInstallsStableUninstallWrapper -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add scripts/join-agent.sh panel/backend-go/internal/controlplane/http/public_test.go
git commit -m "feat(agent): persist installer for local uninstall"
```

### Task 3: Document the installed uninstall entry

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write the documentation change**

```md
安装完成后，Linux / macOS 主机都可直接执行：

```bash
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```
```

- [ ] **Step 2: Run a documentation sanity check**

Run: `rg -n "/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh|uninstall-agent" README.md`
Expected: README shows both the installed local uninstall entry and the direct `join-agent.sh uninstall-agent` fallback.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document agent uninstall entry"
```

### Task 4: Full verification

**Files:**
- Verify only: `scripts/join-agent.sh`, `panel/backend-go/internal/controlplane/http/public_test.go`, `README.md`

- [ ] **Step 1: Run backend tests**

Run: `cd panel/backend-go && go test ./...`
Expected: PASS

- [ ] **Step 2: Run agent tests for regression confidence**

Run: `cd go-agent && go test ./...`
Expected: PASS

- [ ] **Step 3: Check git diff**

Run: `git diff -- scripts/join-agent.sh panel/backend-go/internal/controlplane/http/public_test.go README.md docs/superpowers/plans/2026-04-18-agent-uninstall-entry.md`
Expected: only the planned uninstall-entry changes and plan doc are present.

- [ ] **Step 4: Commit final implementation**

```bash
git add docs/superpowers/plans/2026-04-18-agent-uninstall-entry.md \
  panel/backend-go/internal/controlplane/http/public_test.go \
  scripts/join-agent.sh \
  README.md
git commit -m "feat(agent): add installed uninstall entry"
```
