# Agent Uninstall and Legacy Nginx Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a local-only `uninstall-agent` flow, make migration cleanup stop and remove legacy nginx runtime remnants, and remove the unused managed cert helper script.

**Architecture:** Extend the existing `scripts/join-agent.sh` command dispatcher with a new uninstall subcommand and shared cleanup helpers instead of introducing new scripts. Keep verification centered on the served `join-agent.sh` content test so the public script contract, migration cleanup behavior, and uninstall boundary stay locked together.

**Tech Stack:** POSIX `sh`, Go test suite for the control-plane HTTP layer, Markdown documentation.

---

### Task 1: Lock the Public Script Contract with Failing Tests

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/public_test.go`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Write the failing test**

Add one focused script-contract test next to the existing join-script tests:

```go
func TestJoinScriptIncludesUninstallAndLegacyNginxCleanup(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, "uninstall-agent") {
		t.Fatalf("join-agent.sh missing uninstall-agent command")
	}
	if !strings.Contains(script, "cleanup_legacy_nginx_runtime()") {
		t.Fatalf("join-agent.sh missing shared legacy nginx cleanup helper")
	}
	if !strings.Contains(script, `systemctl disable --now nginx.service`) {
		t.Fatalf("join-agent.sh missing legacy nginx service shutdown")
	}
	if !strings.Contains(script, "cleanup_local_agent_runtime()") {
		t.Fatalf("join-agent.sh missing local uninstall cleanup helper")
	}
	if strings.Contains(script, "/panel-api/agents/$NRE_AGENT_ID") {
		t.Fatalf("join-agent.sh unexpectedly attempts control-plane unregister during uninstall")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/http -run TestJoinScriptIncludesUninstallAndLegacyNginxCleanup
```

Expected: `FAIL` with at least the missing `uninstall-agent` assertion.

- [ ] **Step 3: Keep the existing migration coverage intact**

Retain the current migration-focused test and make sure the new one sits beside it rather than weakening it:

```go
func TestJoinScriptIncludesMigrateFromMainCommand(t *testing.T) {
	deps := Dependencies{Config: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/public/join-agent.sh", nil)
	script, err := deps.buildJoinAgentScript(req)
	if err != nil {
		t.Fatalf("buildJoinAgentScript() error = %v", err)
	}
	if !strings.Contains(script, "migrate-from-main") {
		t.Fatalf("join-agent.sh missing migrate-from-main command")
	}
	if !strings.Contains(script, `DATA_DIR="/var/lib/nre-agent"`) {
		t.Fatalf("join-agent.sh missing normalized agent data dir default")
	}
}
```

- [ ] **Step 4: Re-run the targeted HTTP test after adding the new assertion**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/http -run "TestJoinScriptIncludesMigrateFromMainCommand|TestJoinScriptIncludesUninstallAndLegacyNginxCleanup"
```

Expected: `FAIL` because the uninstall command and shared nginx cleanup helpers do not exist yet.

- [ ] **Step 5: Commit the red test**

```bash
git add panel/backend-go/internal/controlplane/http/public_test.go
git commit -m "test(http): cover uninstall join script contract"
```

### Task 2: Implement Shared Cleanup Helpers and `uninstall-agent`

**Files:**
- Modify: `scripts/join-agent.sh`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Add the new command to usage text**

Extend `usage()` so the new command is visible and explicitly local-only:

```sh
usage() {
    cat <<EOF
Usage:
  join-agent.sh --register-token TOKEN [options]
  join-agent.sh migrate-from-main --register-token TOKEN [options]
  join-agent.sh uninstall-agent [options]

Commands:
  migrate-from-main       Migrate a legacy lightweight Agent node to go-agent
  uninstall-agent         Remove the local Agent runtime from this host

Optional:
  --source-dir DIR         Legacy lightweight Agent directory for migrate-from-main or uninstall-agent

Examples:
  join-agent.sh uninstall-agent --data-dir /var/lib/nre-agent
EOF
}
```

- [ ] **Step 2: Add a shared legacy nginx cleanup helper**

Refactor the current inline nginx deletion block into a reusable helper:

```sh
cleanup_legacy_nginx_runtime() {
    if command -v systemctl >/dev/null 2>&1; then
        run_root_cmd systemctl disable --now nginx.service >/dev/null 2>&1 || true
    fi
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf
    run_root_cmd rm -f /etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf
    run_root_cmd rm -rf /etc/nginx/conf.d/dynamic
    run_root_cmd rm -rf /etc/nginx/stream-conf.d/dynamic
}
```

- [ ] **Step 3: Make migration cleanup call the shared nginx helper**

Keep ACME cleanup and legacy agent directory cleanup in `cleanup_legacy_runtime()`, but delegate nginx cleanup:

```sh
cleanup_legacy_runtime() {
    cleanup_legacy_acme

    disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
    if [ -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service ]; then
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        run_root_cmd systemctl daemon-reload
    fi
    cleanup_legacy_nginx_runtime
    run_root_cmd rm -rf "$OLD_SOURCE_DIR"
    rm -f "$DATA_DIR"/*.bak
}
```

- [ ] **Step 4: Add a helper that removes the installed local agent runtime**

Implement one helper that handles Linux systemd, macOS launchd, and shared directory cleanup:

```sh
cleanup_local_agent_runtime() {
    if [ "$PLATFORM" = "linux" ]; then
        SUDO_BIN="$(require_root_or_sudo)" || {
            echo "Uninstalling systemd services requires root or sudo" >&2
            exit 1
        }
        disable_systemd_unit_if_present nginx-reverse-emby-agent.service
        disable_systemd_unit_if_present nginx-reverse-emby-agent-renew.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent.service
        run_root_cmd rm -f /etc/systemd/system/nginx-reverse-emby-agent-renew.service
        if command -v systemctl >/dev/null 2>&1; then
            run_root_cmd systemctl daemon-reload
        fi
    elif [ "$PLATFORM" = "darwin" ]; then
        SERVICE_FILE="$HOME/Library/LaunchAgents/com.nginx-reverse-emby.agent.plist"
        if [ -f "$SERVICE_FILE" ]; then
            launchctl unload "$SERVICE_FILE" >/dev/null 2>&1 || true
            rm -f "$SERVICE_FILE"
        fi
    fi

    rm -rf "$DATA_DIR"
    if [ -n "${SOURCE_DIR:-}" ]; then
        SOURCE_DIR="$(absolute_path "$SOURCE_DIR")"
        rm -rf "$SOURCE_DIR"
    fi
    cleanup_legacy_nginx_runtime
}
```

- [ ] **Step 5: Add the uninstall command runner and dispatcher**

Add a dedicated runner and extend the command parsing so uninstall stays local-only and does not require register/master arguments:

```sh
run_uninstall_agent() {
    if [ "$USER_DATA_DIR_DEFAULT" = "1" ]; then
        if [ -d "/var/lib/nre-agent" ]; then
            DATA_DIR="/var/lib/nre-agent"
        elif [ -n "${HOME:-}" ] && [ -d "$HOME/.nre-agent" ]; then
            DATA_DIR="$HOME/.nre-agent"
        fi
    fi
    DATA_DIR="$(absolute_path "$DATA_DIR")"
    cleanup_local_agent_runtime
    echo "[UNINSTALL] Local agent runtime removed. Delete the agent record from the control panel if it is no longer needed."
}

COMMAND="join"
if [ $# -gt 0 ] && [ "$1" = "migrate-from-main" ]; then
    COMMAND="migrate-from-main"
    shift 1
elif [ $# -gt 0 ] && [ "$1" = "uninstall-agent" ]; then
    COMMAND="uninstall-agent"
    shift 1
fi

case "$COMMAND" in
    join) run_join ;;
    migrate-from-main) run_migrate_from_main ;;
    uninstall-agent) run_uninstall_agent ;;
    *) echo "Unknown command: $COMMAND" >&2; exit 1 ;;
esac
```

- [ ] **Step 6: Run the targeted HTTP test to verify the new behavior passes**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/http -run "TestJoinScriptIncludesMigrateFromMainCommand|TestJoinScriptIncludesUninstallAndLegacyNginxCleanup"
```

Expected: `PASS`.

- [ ] **Step 7: Run shell syntax validation**

Run:

```powershell
& 'C:\Program Files\Git\bin\bash.exe' -n scripts/join-agent.sh
```

Expected: exit code `0` with no output.

- [ ] **Step 8: Commit the script implementation**

```bash
git add scripts/join-agent.sh panel/backend-go/internal/controlplane/http/public_test.go
git commit -m "feat(agent): add uninstall and legacy nginx cleanup"
```

### Task 3: Remove the Unused Helper and Update Operator Docs

**Files:**
- Delete: `scripts/managed-cert-helper.sh`
- Modify: `README.md`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Delete the unused helper script**

Remove the file entirely:

```diff
*** Delete File: scripts/managed-cert-helper.sh
```

- [ ] **Step 2: Update the migration and uninstall README text**

Adjust the agent section so it matches the implemented cleanup boundary:

````md
默认会从 `/opt/nginx-reverse-emby-agent` 读取旧 lightweight-Agent 目录，复用原 `agent_token`，切换到新的 `/var/lib/nre-agent`，并在新服务验证通过后清理旧 runtime、旧 nginx 服务与动态配置、以及旧 `.acme.sh` 续期状态。

### Uninstall Agent

如需从 VPS 上完全移除本地 Go agent 运行时，可执行：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

该命令只清理本机 service、数据目录、legacy lightweight-Agent 残留和 legacy nginx runtime 残留；控制面里的 agent 记录仍需手动删除。
````

- [ ] **Step 3: Verify there are no remaining references to the deleted helper**

Run:

```bash
rg -n "managed-cert-helper\\.sh" README.md scripts panel
```

Expected: no matches.

- [ ] **Step 4: Run the full backend verification**

Run:

```bash
cd panel/backend-go
go test ./...
```

Expected: all packages `ok`.

- [ ] **Step 5: Commit docs and cleanup**

```bash
git add README.md scripts/managed-cert-helper.sh
git commit -m "chore(agent): document uninstall and remove dead helper"
```
