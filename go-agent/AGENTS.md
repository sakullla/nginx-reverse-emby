# Repository Guidelines

## Project Structure & Module Organization
This directory contains the Go execution-plane agent module for `nginx-reverse-emby`.

- `cmd/nre-agent/` contains the agent entrypoint.
- `internal/` contains private agent packages, including proxy, relay, diagnostics, runtime, config, certs, task sync, and platform-specific helpers.
- `embedded/` contains the embedded runtime bridge used by the Go control plane.
- Tests live beside the packages they cover as `*_test.go` files.
- `go.mod` and `go.sum` define this module separately from the parent control-plane and frontend code.

Parent-level assets such as `panel/`, `scripts/`, `Dockerfile`, and `docker-compose.yaml` are outside this module but may be relevant for full-stack validation.

## Build, Test, and Development Commands
- `make test` or `go test ./...` - run the full Go agent test suite.
- `make run` or `go run ./cmd/nre-agent` - run the agent locally.
- `go test ./internal/relay ./internal/proxy` - run focused package tests while iterating.
- `go test -run TestName ./internal/package` - run a single test by name.
- From the repo root, `docker build -t nginx-reverse-emby .` validates image-impacting changes.

## Coding Style & Naming Conventions
Use standard Go style and run `gofmt` on edited Go files. Keep packages focused and prefer existing package boundaries over adding broad utility packages. Use descriptive Go names such as `Runtime`, `Config`, `RelayPath`, and `TestRuntimeStarts`. Environment variables should use `UPPER_SNAKE_CASE`. Avoid unrelated repo-wide formatting or dependency churn.

## Testing Guidelines
Use the standard Go testing package. Place tests next to implementation files and name them `*_test.go`. Add targeted tests for behavior changes, especially around relay selection, proxy behavior, diagnostics, runtime activation, update flow, storage, and platform-specific code. Include regression tests for bug fixes when practical. Minimum verification for this module is `go test ./...`.

## Commit & Pull Request Guidelines
Follow the repository's Conventional Commit pattern, for example `fix(agent): handle relay timeout` or `feat(proxy): add resume support`. Keep commits narrowly scoped. Pull requests should include a short summary, linked issues when applicable, exact verification commands, and screenshots or logs when behavior is visible outside unit tests.

## Security & Configuration Tips
Do not commit tokens, certificates, private keys, generated runtime state, or files from parent `panel/data/`. Document new configuration and environment variables in the root `README.md` and relevant examples. Keep platform-specific behavior isolated under `internal/platform/` or build-tagged files.
