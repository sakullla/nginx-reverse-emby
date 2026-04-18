# Agent Uninstall Entry Design

## Goal

Provide an installed, host-local uninstall entry for the Go agent that mirrors the ergonomics of k3s-style uninstall scripts. After `join-agent.sh` installs the agent, the host should expose a stable uninstall command without requiring the user to re-fetch the join script.

## Scope

In scope:

- Linux systemd installs via `join-agent.sh --install-systemd`
- macOS launchd installs via `join-agent.sh --install-launchd`
- Reuse of the existing `uninstall-agent` behavior
- Documentation and tests for the new uninstall entry

Out of scope:

- Changing uninstall deletion scope
- Automatic unregister/delete in the control plane
- Reworking the existing `uninstall-agent` cleanup policy
- Windows support

## Approaches

### Recommended: install a fixed uninstall script that delegates to existing uninstall logic

During install, write a stable script to `/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh`. That script calls the installed `join-agent.sh` uninstall path with the resolved install parameters, especially `--data-dir` and optional `--source-dir`.

Pros:

- Matches the k3s mental model closely
- Keeps one uninstall implementation
- Smallest behavioral change

Cons:

- Requires the installer to preserve enough context to regenerate the uninstall invocation

### Alternative: document `curl ... | sh -s -- uninstall-agent`

Pros:

- Minimal code change

Cons:

- Does not provide a host-local uninstall entry
- Depends on the control plane remaining reachable
- Does not match the requested ergonomics

### Alternative: create a dedicated system service or package-style uninstaller

Pros:

- More formal lifecycle model

Cons:

- Over-engineered for the current shell-based installer
- Duplicates existing uninstall behavior

## Design

### Installed entrypoint

The installer writes a small shell script to:

- Linux: `/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh`
- macOS: `/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh`

The script:

- uses `#!/bin/sh`
- executes the installed agent join script in uninstall mode
- passes the concrete `--data-dir`
- passes `--source-dir` only when that value was explicitly configured or migrated

### Source of truth

The actual cleanup implementation remains inside `scripts/join-agent.sh` under `run_uninstall_agent` and related helpers. The generated uninstall entry is only a delegating wrapper.

This avoids drift between:

- direct uninstall via `join-agent.sh uninstall-agent`
- installed uninstall via `/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh`

### Installer behavior

For install modes that produce a persistent local runtime, the installer also persists the uninstall wrapper.

Expected behavior:

- systemd install writes the wrapper as root
- launchd install writes the wrapper and makes it executable
- manual install may also write the wrapper if feasible, but systemd/launchd are the required paths

### Uninstall behavior

No deletion-policy change is introduced.

The uninstall wrapper continues to:

- stop and disable the local service if present
- remove installed unit/plist files
- remove the local runtime under the configured data dir
- clean legacy runtime markers the same way current uninstall does

The uninstall wrapper does not:

- call the control plane to unregister the node
- delete the remote agent record automatically

### Safety

The wrapper should embed resolved absolute paths rather than environment-dependent relative paths. This keeps uninstall deterministic even when the original current working directory is gone.

## Testing

Add tests that assert the public `join-agent.sh` content now includes:

- uninstall script install path
- uninstall wrapper creation
- delegation to `uninstall-agent`

If practical, also add shell-oriented unit coverage around generated uninstall wrapper content.

## Documentation

Update README install docs to show:

- install command
- installed uninstall command

Example target UX:

```sh
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh
```

## Acceptance Criteria

- A host installed via `join-agent.sh --install-systemd` gets a stable uninstall command
- A host installed via `join-agent.sh --install-launchd` gets a stable uninstall command
- The uninstall command reuses existing cleanup behavior
- README documents the installed uninstall command
- Existing uninstall behavior outside this new entrypoint remains unchanged
