# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`nginx-reverse-emby` is an automated nginx reverse-proxy solution with a web management panel, designed primarily for fronting Emby/Jellyfin and arbitrary HTTP/TCP services. It supports a **Master/Agent architecture** where a Master node manages rules centrally and pushes them to remote Agents via a polling heartbeat protocol.

**Two deploy modes:**
- `direct` — container binds ports 80/443, handles SSL via `acme.sh`
- `front_proxy` — container runs behind an upstream proxy (Caddy/Nginx), listens on port 3000

## Commands

### Backend (Node.js API)
```bash
cd panel/backend
node server.js                    # run locally
node --check server.js            # syntax check
npm test                          # run property-based tests (fast-check)
npm run prisma:generate           # regenerate Prisma client after schema changes
```

### Frontend (Vue 3 / Vite)
```bash
cd panel/frontend
npm ci                            # install deps
npm run dev                       # dev server at http://localhost:5173
npm run build                     # build to dist/
npm run preview                   # preview built bundle
```

Dev server proxies `/panel-api` → `http://localhost:18081` (rewritten to `/api`), so the backend must be running for API calls to work. The frontend also has a **mock mode** (see `api/index.js`) for UI-only development.

### Container
```bash
docker build -t nginx-reverse-emby .       # full multi-stage image build
docker compose up -d                       # start the stack
docker exec nginx-reverse-emby nginx -t   # validate nginx config inside container
```

## Architecture

### Request Flow
```
Browser (Vue SPA :8080)
  → nginx (/panel-api/ → 127.0.0.1:18081/api/)
    → server.js (raw Node.js HTTP, no framework)
      → storage.js dispatcher
          ├── storage-sqlite.js (default; Worker thread + Prisma)
          └── storage-json.js (fallback; flat JSON files in panel/data/)
```

### Rule Application Flow
When a rule is saved and `PANEL_AUTO_APPLY=1` (default):
1. `server.js` calls `spawnSync("25-dynamic-reverse-proxy.sh")`
2. Script reads `proxy_rules.json` + `l4_rules.json`
3. Issues/installs certs via `acme.sh`
4. Generates `/etc/nginx/conf.d/dynamic/*.conf` (HTTP) and `/etc/nginx/stream-conf.d/dynamic/*.conf` (L4/TCP/UDP)
5. Runs `nginx -t && nginx -s reload`

### Remote Agent Sync (pull mode)
1. Master sets `desired_revision` on agent record when rules change
2. `light-agent.js` on remote host polls every 10s via heartbeat POST
3. Master returns sync payload (rules, L4 rules, cert bundle, policy)
4. Agent writes rule JSON files, runs `APPLY_COMMAND` (typically `light-agent-apply.sh`)
5. Agent reports back `current_revision` and `last_apply_status`

### Storage Layer
- **SQLite (default):** `storage-sqlite.js` maintains in-memory state for performance, uses a Worker thread (`storage-prisma-worker.js` → `storage-prisma-core.js`) for Prisma ORM writes to avoid blocking the event loop.
- **JSON fallback:** `storage-json.js` writes flat JSON files to `panel/data/`. Controlled via `PANEL_STORAGE_BACKEND` env var (`sqlite` | `json`).
- Prisma schema: `panel/backend/prisma/schema.prisma`. Models: `Agent`, `Rule`, `L4Rule`, `ManagedCertificate`, `LocalAgentState`, `Meta`.

### Frontend State
Single Pinia store (`useRuleStore` in `stores/rules.js`) manages all app state: auth, system info, agent list, selected agent, HTTP rules, L4 rules, certificates, and search/filter state. All API calls go through `api/index.js`.

## Key Environment Variables

| Prefix | Purpose |
|--------|---------|
| `PANEL_*` | Panel behavior: port, role (`master`/`agent`), data root, storage backend |
| `PROXY_*` | Deploy mode, redirect/header behavior |
| `MASTER_*` | Master-specific: register token, local agent config |
| `AGENT_*` | Agent identity, polling behavior (used by `light-agent.js`) |
| `ACME_*`, `CF_*` | Certificate issuance and DNS challenge |
| `NGINX_*` | Nginx tuning: max body size, IPv6, resolvers |

## API Conventions

- All authenticated routes: `/api/` (token via `X-Panel-Token` header)
- Public routes (agent join script, asset downloads): `/api/public/`
- Frontend accesses everything through `/panel-api/` (nginx rewrites to `/api/`)

## Testing

Tests are in `panel/backend/tests/` and use `fast-check` for property-based testing:
- `property-roundtrip.test.js` — storage read/write roundtrips
- `property-isolation.test.js` — agent data isolation
- `property-revision.test.js` — revision increment correctness
- `property-compatibility.test.js` — JSON/SQLite storage compatibility

Run a single test file:
```bash
cd panel/backend && node tests/property-roundtrip.test.js
```

## Commit Style

Commits follow Conventional Commits: `type(scope): description`
- Types: `feat`, `fix`, `refactor`, `test`, `docs`, `chore`
- Scopes: `nginx`, `backend`, `frontend`, `agent`, `panel`, `docker`

## Security Notes

- All write operations to `/api/` require `X-Panel-Token` authentication
- Never log or expose API tokens, `MASTER_REGISTER_TOKEN`, or acme.sh credentials
- `server.js` validates that agent IDs are alphanumeric only before using them in file paths
