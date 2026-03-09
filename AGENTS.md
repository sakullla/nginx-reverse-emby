# Repository Guidelines

## Project Structure & Module Organization
- `panel/frontend/` contains the Vue 3 SPA (Vite + Pinia). Main app code lives in `src/`:
  - `src/components/` for UI components
  - `src/stores/rules.js` for app state
  - `src/api/index.js` for backend API calls
  - `src/styles/` for shared styles
- `panel/backend/server.js` is the Node.js backend (single-file HTTP API).
- `docker/` contains entrypoint scripts and Nginx templates used at container startup.
- Root files (`Dockerfile`, `docker-compose.yaml`, `deploy.sh`, `nginx.conf`) define build and runtime behavior.
- Runtime data is stored under `data/` (mounted to `/opt/nginx-reverse-emby/panel/data`); do not commit secrets or generated runtime artifacts.

## Build, Test, and Development Commands
- `cd panel/frontend && npm ci` - install frontend dependencies.
- `cd panel/frontend && npm run dev` - start Vite dev server (`http://localhost:5173`).
- `cd panel/frontend && npm run build` - create production frontend bundle in `dist/`.
- `docker build -t nginx-reverse-emby .` - build the multi-stage image.
- `docker compose up -d` - run the full stack locally.
- `docker compose logs -f nginx-reverse-emby` - stream container logs.
- `node panel/backend/server.js` - run backend standalone for API debugging.

## Coding Style & Naming Conventions
- Match existing style per area:
  - Frontend: 2-space indentation, Composition API with `<script setup>`, single quotes, no semicolons.
  - Backend: CommonJS (`require`), semicolons, defensive error handling.
- Use `PascalCase.vue` for components and `camelCase` for JS modules/stores.
- Keep environment variable names uppercase with prefixes such as `PANEL_`, `PROXY_`, and `ACME_`.
- Keep user-facing text consistent with Chinese localization.

## Testing Guidelines
- There is no dedicated automated test suite yet.
- Minimum manual checks before opening a PR:
  - `cd panel/frontend && npm run build`
  - `docker build -t nginx-reverse-emby .`
  - Basic API/UI smoke test after `docker compose up -d`
- For proxy-rule or template changes, verify generated config validity with `nginx -t` in the running container.

## Commit & Pull Request Guidelines
- Follow the existing Conventional Commit pattern from history: `feat(panel): ...`, `fix: ...`, `style: ...`, `chore: ...`.
- Keep commits focused; avoid mixing UI, backend, and infra refactors without clear reason.
- PRs should include:
  - concise problem/solution summary
  - manual verification steps and results
  - linked issue(s), if applicable
  - screenshots/GIFs for frontend behavior changes

## Security & Configuration Tips
- Never commit real `API_TOKEN`, DNS provider credentials, or certificate material.
- Prefer environment-variable overrides for sensitive settings instead of hardcoding values in `docker-compose.yaml`.
