# Repository Guidelines

## Project Structure & Module Organization
This repository combines a Vue 3 control panel with a lightweight Node backend and the Go `nre-agent` execution plane. The SPA lives in `panel/frontend/`: application code is in `src/`, reusable UI in `src/components/`, API clients in `src/api/`, state stores in `src/stores/`, and shared styles in `src/styles/`. The backend entrypoint is `panel/backend/server.js`. The default Docker/Compose runtime is the control-plane container; helper scripts such as `join-agent.sh` are in `scripts/`. Legacy Nginx-oriented files may still exist under `docker/`, but they are not the primary runtime path. Treat `panel/data/` as runtime data only; never commit generated files, certificates, or secrets.

## Build, Test, and Development Commands
- `cd panel/frontend && npm ci` - install frontend dependencies.
- `cd panel/frontend && npm run dev` - start the Vite dev server at `http://localhost:5173`.
- `cd panel/frontend && npm run build` - produce the production bundle in `panel/frontend/dist/`.
- `cd panel/frontend && npm run preview` - preview the built frontend locally.
- `node panel/backend/server.js` - run the backend API for local debugging.
- `docker build -t nginx-reverse-emby .` - build the full container image.
- `docker compose up -d` - start the local stack.

## Coding Style & Naming Conventions
Use 2-space indentation throughout the frontend. Vue components should use `<script setup>`, single quotes, and no semicolons. Backend code follows CommonJS (`require`) with semicolons and defensive error handling. Keep shell scripts POSIX-friendly unless the file already requires otherwise. Name Vue components with `PascalCase.vue` and JavaScript modules/stores in `camelCase`. Environment variables should remain uppercase with prefixes such as `PANEL_`, `PROXY_`, `MASTER_`, and `AGENT_`.

## Testing Guidelines
There is no dedicated automated app test suite yet. Before opening a PR, run:
- `cd panel/frontend && npm run build`
- `node --check panel/backend/server.js`
- `docker build -t nginx-reverse-emby .`

## Commit & Pull Request Guidelines
Follow Conventional Commits, for example `feat(panel): add login status banner` or `fix(agent): handle timeout`. Keep commits focused by area: frontend, backend, infra/scripts, or docs. PRs should include a short summary, manual verification steps, linked issues when applicable, and screenshots or GIFs for UI changes.

## Security & Configuration Tips
Never commit real `API_TOKEN`, `MASTER_REGISTER_TOKEN`, DNS credentials, or certificate material. Prefer environment-variable overrides to hardcoded secrets in `docker-compose.yaml`.
