# Repository Guidelines

## Project Structure & Module Organization
- `panel/frontend/` contains the Vue 3 SPA (Vite + Pinia). Main code lives in `src/`, with UI in `src/components/`, API helpers in `src/api/`, state in `src/stores/`, and shared styles in `src/styles/`.
- `panel/backend/server.js` is the single-file Node.js HTTP API.
- `docker/` stores entrypoint scripts and Nginx templates used at container startup.
- `scripts/` contains Master/Agent helper scripts such as `join-agent.sh` and `light-agent.js`.
- Runtime data is persisted under `panel/data/` in-container; do not commit generated data, certs, or secrets.

## Build, Test, and Development Commands
- `cd panel/frontend && npm ci` — install frontend dependencies.
- `cd panel/frontend && npm run dev` — start the Vite dev server on `http://localhost:5173`.
- `cd panel/frontend && npm run build` — build the production frontend into `panel/frontend/dist/`.
- `node panel/backend/server.js` — run the backend locally for API debugging.
- `docker build -t nginx-reverse-emby .` — build the full image.
- `docker compose up -d` — start the local stack.
- `docker exec nginx-reverse-emby nginx -t` — validate generated Nginx config after proxy/template changes.

## Coding Style & Naming Conventions
- Frontend: 2-space indentation, `<script setup>`, single quotes, and no semicolons.
- Backend: CommonJS (`require`), semicolons, and defensive error handling.
- Shell scripts in `docker/` should remain POSIX-friendly; `scripts/join-agent.sh` is Bash-specific.
- Use `PascalCase.vue` for components and `camelCase` for JS modules/stores.
- Keep environment variables uppercase with prefixes like `PANEL_`, `PROXY_`, `ACME_`, `MASTER_`, and `AGENT_`.

## Testing Guidelines
- There is no dedicated automated app test suite yet. The root `package.json` only provides Playwright scaffolding, and `tests/e2e/` is currently absent.
- Minimum verification before a PR:
  - `cd panel/frontend && npm run build`
  - `node --check panel/backend/server.js`
  - `docker build -t nginx-reverse-emby .`
- For Nginx, rule, certificate, or L4 changes, also run `nginx -t` in a running container.

## Commit & Pull Request Guidelines
- Follow the existing Conventional Commit style, e.g. `feat(panel): ...`, `fix(agent): ...`, `docs: ...`.
- Keep commits scoped by area: frontend, backend, infra/scripts, or docs.
- PRs should include a short summary, manual verification steps, linked issues if any, and screenshots/GIFs for UI changes.

## Security & Configuration Tips
- Never commit real `API_TOKEN`, `MASTER_REGISTER_TOKEN`, DNS credentials, or certificate material.
- Prefer environment-variable overrides instead of hardcoding secrets in `docker-compose.yaml`.
