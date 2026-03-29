# Repository Guidelines

## Project Structure & Module Organization
This backend lives in `panel/backend/`. `server.js` is the HTTP API entrypoint. Storage adapters are split across `storage.js`, `storage-json.js`, `storage-sqlite.js`, and Prisma helpers in `storage-prisma-*.js`. Database schema and generation config live in `prisma/`. Property tests live in `tests/`, with shared setup in `tests/helpers.js`. The Vue SPA is in `../frontend/`; coordinate API or payload changes with its `src/api/` and `src/stores/` modules.

## Build, Test, and Development Commands
- `npm test` - run the backend property-test suite.
- `node --check server.js` - syntax-check the backend entrypoint.
- `node server.js` - start the API locally for manual debugging.
- `npm run prisma:generate` - regenerate the Prisma client after schema changes.
- `cd ../frontend && npm run build` - verify the frontend still builds after API changes.
- From the repo root: `docker build -t nginx-reverse-emby .` - validate the full container image.

## Coding Style & Naming Conventions
Use CommonJS (`require`) and semicolons in backend files. Prefer small helpers, explicit validation, and defensive error handling around storage, I/O, and request parsing. Follow existing naming: `storage-*.js` for persistence adapters, `camelCase` for variables/functions, and `UPPER_SNAKE_CASE` for environment variables such as `PANEL_*`, `MASTER_*`, and `AGENT_*`. Keep frontend edits aligned with the existing Vue style: 2-space indentation, single quotes, and no semicolons.

## Testing Guidelines
Add new tests under `tests/` using the `*.test.js` suffix. The current suite uses `fast-check` property tests, so prefer invariant-focused cases over brittle fixtures. Reuse `tests/helpers.js` instead of duplicating setup. For storage or revision changes, cover round-trip, isolation, revision, and compatibility behavior. Minimum verification: `npm test`, `node --check server.js`, and a repo-root `docker build`.

## Commit & Pull Request Guidelines
Follow the repo's Conventional Commits, for example `feat(backend): migrate sqlite storage to prisma` or `fix(panel): expose master_register_token`. Keep commits scoped by area. PRs should include a short summary, manual verification steps, linked issues when applicable, and screenshots or GIFs for UI changes.

## Security & Configuration Tips
Never commit runtime data, SQLite files, API tokens, register tokens, or certificate material. Prefer environment-variable overrides to hardcoded secrets, and document any new config flags in both backend and frontend touchpoints.
