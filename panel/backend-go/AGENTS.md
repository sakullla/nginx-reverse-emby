# Repository Guidelines

## Project Structure & Module Organization
This directory is the Go control-plane module for the panel. The main entry point lives in `cmd/nre-control-plane/`. Core packages are under `internal/controlplane/`, including `app`, `config`, `cutover`, `http`, `localagent`, `service`, and `storage`. Package tests should live beside the package they cover as `*_test.go`; storage fixtures belong under package-local `testdata/`.

The broader repository also includes `panel/frontend/` for the Vue 3 + Vite SPA and `go-agent/` for the Go execution plane. Runtime state under `panel/data/` is local data, not source.

## Build, Test, and Development Commands
- `go run ./cmd/nre-control-plane` - run the control plane locally from this module.
- `go test ./...` - run all Go control-plane tests.
- `go test ./internal/controlplane/storage` - run a focused package test while iterating.
- `cd ../frontend && npm run build` - verify frontend bundle compatibility when API or served asset behavior changes.
- `cd ../../go-agent && go test ./...` - verify execution-plane behavior when agent contracts change.
- `cd ../.. && docker build -t nginx-reverse-emby .` - validate image-impacting edits.

## Coding Style & Naming Conventions
Use standard Go style and run `gofmt` on edited Go files. Keep packages focused and prefer clear Go names over abbreviations. Test files use the `*_test.go` suffix and test functions use `TestName` or `TestName_Case` patterns. Do not introduce repo-wide formatting churn.

Shell scripts elsewhere in the repository target POSIX `sh`; keep variables lowercase snake_case unless they are exported environment variables, which should use `UPPER_SNAKE_CASE`.

## Testing Guidelines
Use the standard Go testing package. Add tests near affected code, especially for storage, revisioning, migrations, compatibility handling, and API/service behavior. Prefer invariant-style tests for persistence and state transitions. Minimum verification for backend changes is `go test ./...`; broaden to frontend build, `go-agent` tests, or Docker build when needed.

## Commit & Pull Request Guidelines
Recent history uses Conventional Commits with scopes, such as `fix(backend): migrate legacy rule fields during bootstrap`, `fix(panel): preserve rule relay layers on detail save`, and `perf(agent): index backend observations`. Keep commits narrowly scoped.

Pull requests should include a short summary, linked issues when applicable, exact verification commands, and screenshots or GIFs for UI-facing panel changes.

## Security & Configuration Tips
Never commit API tokens, registration tokens, certificates, database files, or anything from `panel/data/`. Document new environment variables in the repository README and examples. Preserve local user data and unrelated working-tree changes while editing.
