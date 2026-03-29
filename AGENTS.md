# Repository Guidelines

## Project Structure & Module Organization
The repo packages an Nginx-based reverse-proxy image plus a web control panel. Top-level runtime assets live in `docker/` (entrypoint scripts and Nginx templates), `conf.d/` (sample generated configs), `scripts/` (agent/join/apply helpers), and `examples/` (service and env examples). The panel is split into `panel/frontend/` (Vue 3 + Vite SPA, source in `src/`) and `panel/backend/` (Node API, Prisma schema in `prisma/`, property tests in `tests/`). `panel/data/` is runtime state; treat it as local data, not source.

## Build, Test, and Development Commands
- `cd panel/frontend && npm run dev` - start the Vite UI locally.
- `cd panel/frontend && npm run build` - produce the frontend bundle used by the image.
- `cd panel/backend && node server.js` - run the backend API for local debugging.
- `cd panel/backend && npm test` - run the backend property-test suite.
- `cd panel/backend && npm run prisma:generate` - regenerate Prisma client code after schema changes.
- `docker build -t nginx-reverse-emby .` - validate the full multi-stage container build.
- `docker compose up -d` - start the packaged stack from `docker-compose.yaml`.

## Coding Style & Naming Conventions
Match the style of the area you edit; do not introduce repo-wide reformatting. Frontend files use 2-space indentation, ES modules, single quotes, and PascalCase Vue component names such as `RuleList.vue`. Backend files use CommonJS, semicolons, double quotes, and small focused modules like `storage-prisma-core.js`. Shell scripts target POSIX `sh`; keep them portable and favor lowercase snake_case variable names. Use `UPPER_SNAKE_CASE` for environment variables.

## Testing Guidelines
Add backend tests under `panel/backend/tests/` with the `*.test.js` suffix. Prefer property or invariant-based coverage for storage, revisioning, and compatibility changes. Minimum verification for behavior changes: `cd panel/backend && npm test`, relevant frontend build checks, and `docker build -t nginx-reverse-emby .` for image-impacting edits.

## Commit & Pull Request Guidelines
Recent history follows Conventional Commits with scopes, e.g. `feat(backend): ...`, `fix(panel): ...`, `fix(nginx): ...`. Keep commits narrowly scoped by subsystem. PRs should include a short summary, linked issues when applicable, exact verification commands, and screenshots or GIFs for panel UI changes.

## Security & Contributor Notes
Never commit API tokens, register tokens, certificates, or files from `panel/data/`. Document new environment variables in `README.md` and examples. If a nested `AGENTS.md` exists (for example in `panel/backend/`), follow the more specific guide there.