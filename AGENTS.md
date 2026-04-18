# Repository Guidelines

## Project Structure & Module Organization
The repo ships a Go control-plane container plus the Go `go-agent` execution plane. The active panel runtime is split into `panel/backend-go/` (Go control plane) and `panel/frontend/` (Vue 3 + Vite SPA, source in `src/`). Top-level helper assets live in `scripts/` (join/agent helpers) and `examples/` (optional legacy examples, which may be empty after cleanup). `panel/data/` is runtime state; treat it as local data, not source. `deploy.sh`, `conf.d/`, and repo-root `nginx.conf` are retained for legacy standalone Nginx workflows and are not the default control-plane runtime path.

## Build, Test, and Development Commands
- `cd panel/frontend && npm run dev` - start the Vite UI locally.
- `cd panel/frontend && npm run build` - produce the frontend bundle used by the image.
- `cd panel/backend-go && go run ./cmd/nre-control-plane` - run the Go control plane locally.
- `cd panel/backend-go && go test ./...` - run the Go control-plane test suite.
- `cd go-agent && go test ./...` - run the Go execution-plane test suite.
- `docker build -t nginx-reverse-emby .` - validate the full multi-stage container build.
- `docker compose up -d` - start the packaged pure-Go control-plane stack from `docker-compose.yaml` (local agent embedded by default).

## Coding Style & Naming Conventions
Match the style of the area you edit; do not introduce repo-wide reformatting. Frontend files use 2-space indentation, ES modules, single quotes, and PascalCase Vue component names such as `RuleList.vue`. Go files should follow `gofmt` formatting and standard Go naming conventions with focused packages. Shell scripts target POSIX `sh`; keep them portable and favor lowercase snake_case variable names. Use `UPPER_SNAKE_CASE` for environment variables.

## Testing Guidelines
Add Go control-plane tests near the affected package under `panel/backend-go/` using standard Go test files (`*_test.go`). Prefer invariant-style coverage for storage, revisioning, and compatibility changes. Minimum verification for behavior changes: `cd panel/backend-go && go test ./...`, relevant frontend build checks, `cd go-agent && go test ./...` when execution-plane behavior is touched, and `docker build -t nginx-reverse-emby .` for image-impacting edits.

## Commit & Pull Request Guidelines
Recent history follows Conventional Commits with scopes, e.g. `feat(backend): ...`, `fix(panel): ...`, `fix(nginx): ...`. Keep commits narrowly scoped by subsystem. PRs should include a short summary, linked issues when applicable, exact verification commands, and screenshots or GIFs for panel UI changes.

## Security & Contributor Notes
Never commit API tokens, register tokens, certificates, or files from `panel/data/`. Document new environment variables in `README.md` and examples. If a nested `AGENTS.md` exists in the area you edit, follow the more specific guide there.
