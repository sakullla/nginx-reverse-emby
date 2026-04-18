# Repository Guidelines

## Project Structure & Module Organization
This repository combines a Vue 3 control panel with a Go control plane and the Go `go-agent` execution plane. The SPA lives in `panel/frontend/`: application code is in `src/`, reusable UI in `src/components/`, API clients in `src/api/`, state stores in `src/stores/`, and shared styles in `src/styles/`. The active control-plane runtime is `panel/backend-go`. The default Docker/Compose runtime is the pure-Go control-plane container with embedded local-agent capability; helper scripts such as `join-agent.sh` are in `scripts/`. `deploy.sh`, `conf.d/`, and repo-root `nginx.conf` are retained legacy standalone assets, not the primary runtime path. Treat `panel/data/` as runtime data only; never commit generated files, certificates, or secrets.

## Build, Test, and Development Commands
- `cd panel/frontend && npm ci` - install frontend dependencies.
- `cd panel/frontend && npm run dev` - start the Vite dev server at `http://localhost:5173`.
- `cd panel/frontend && npm run build` - produce the production bundle in `panel/frontend/dist/`.
- `cd panel/frontend && npm run preview` - preview the built frontend locally.
- `cd panel/backend-go && go run ./cmd/nre-control-plane` - run the control-plane API for local debugging.
- `cd panel/backend-go && go test ./...` - run Go control-plane tests.
- `cd go-agent && go test ./...` - run execution-plane tests.
- `docker build -t nginx-reverse-emby .` - build the full container image.
- `docker compose up -d` - start the local stack.

## Coding Style & Naming Conventions
Use 2-space indentation throughout the frontend. Vue components should use `<script setup>`, single quotes, and no semicolons. For control-plane API work, follow Go conventions in `panel/backend-go` and keep files `gofmt`-clean. Keep shell scripts POSIX-friendly unless the file already requires otherwise. Name Vue components with `PascalCase.vue` and JavaScript modules/stores in `camelCase`. Environment variables should remain uppercase with prefixes such as `PANEL_`, `PROXY_`, `MASTER_`, and `AGENT_`.

## Testing Guidelines
There is no dedicated automated app test suite yet. Before opening a PR, run:
- `cd panel/frontend && npm run build`
- `cd panel/backend-go && go test ./...`
- `docker build -t nginx-reverse-emby .`

## Commit & Pull Request Guidelines
Follow Conventional Commits, for example `feat(panel): add login status banner` or `fix(agent): handle timeout`. Keep commits focused by area: frontend, backend, infra/scripts, or docs. PRs should include a short summary, manual verification steps, linked issues when applicable, and screenshots or GIFs for UI changes.

## Security & Configuration Tips
Never commit real `API_TOKEN`, `MASTER_REGISTER_TOKEN`, DNS credentials, or certificate material. Prefer environment-variable overrides to hardcoded secrets in `docker-compose.yaml`.
