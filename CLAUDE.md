# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`nginx-reverse-emby` now uses a split architecture:

- **Control plane:** Node.js backend + Vue frontend
- **Execution plane:** Go `nre-agent`

The control plane stores rules, certificates, agents, relay listeners, and version policy. Agents keep using the **heartbeat pull** model: registered agents poll the master, fetch desired state, and apply it locally.

The repository is no longer centered around a bundled Nginx runtime for the control plane. The panel backend now serves:

- the JSON API,
- `/panel-api/*` aliases,
- the built frontend bundle,
- public agent assets such as `join-agent.sh` and Go binaries.

## Commands

### Backend (Node.js API)
```bash
cd panel/backend
node server.js
node --check server.js
npm test
npm run prisma:generate
```

### Frontend (Vue 3 / Vite)
```bash
cd panel/frontend
npm ci
npm run dev
npm run build
npm run preview
```

The Vite dev server proxies `/panel-api` requests to the backend. For local UI development, run the backend alongside the frontend dev server.

### Go Agent
```bash
cd go-agent
go test ./...
go run ./cmd/nre-agent
```

### Container / Runtime Packaging
```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

The default image/runtime produced by this repository is the **control-plane container**. The Go agent is packaged as a separate execution-plane binary and exposed for download by remote or local agents.

## Architecture

### Control-Plane Request Flow
```text
Browser
  -> Node backend (server.js)
    -> authenticated /api/* routes
    -> /panel-api/* compatibility aliases
    -> public agent asset routes
    -> built frontend static files / SPA fallback
```

### Agent Sync Flow (pull model)
1. Master stores desired state and desired revisions
2. A registered Go `nre-agent` sends heartbeat / sync requests to the master
3. Master returns rules, L4 rules, relay listeners, certificates, and version/update information
4. Agent applies the config locally and reports current status/revision back on later heartbeats

### Runtime Responsibilities

**Node/Vue control plane**
- API, auth, storage, revisioning, agent registry
- relay listener and version policy management
- agent asset publishing and join/bootstrap flow

**Go execution plane**
- heartbeat sync client
- HTTP proxy engine
- L4 direct proxying
- TCP relay validation/runtime
- certificate/runtime primitives
- local-agent mode and update plumbing

If you explicitly enable legacy local apply / Node-side execution paths, you must provide `PANEL_GENERATOR_SCRIPT` or `PANEL_APPLY_COMMAND`; the default control-plane image no longer bundles the old generator script.

### Storage Layer
- **SQLite (default):** Prisma-backed storage with worker-thread helpers
- **JSON fallback:** flat files in `panel/data/`

Relevant backend files:
- `panel/backend/server.js`
- `panel/backend/storage.js`
- `panel/backend/storage-json.js`
- `panel/backend/storage-sqlite.js`
- `panel/backend/storage-prisma-*.js`

### Frontend State
The Vue SPA under `panel/frontend/src/` manages rules, agents, certificates, relay listeners, and version/update UI.

## Key Environment Variables

| Prefix | Purpose |
|--------|---------|
| `PANEL_*` | Panel host/port, storage backend, runtime behavior |
| `MASTER_*` | Master register token, local-agent settings, version/update behavior |
| `AGENT_*` | Go agent identity, polling, and sync settings |
| `PROXY_*` | Proxy/runtime configuration shared with rules or local runtime behavior |

## API Conventions

- Authenticated API routes live under `/api/*`
- Public bootstrap / asset routes live under `/api/public/*`
- `/panel-api/*` aliases are also served directly by the Node backend for compatibility
- Public health endpoint: `/panel-api/health`

## Testing

Backend tests live in `panel/backend/tests/` and are primarily property/invariant based.

Common verification commands:

```bash
cd panel/backend && npm test
cd panel/backend && node --check server.js
cd panel/frontend && npm run build
cd go-agent && go test ./...
docker build -t nginx-reverse-emby .
```

## Commit Style

Commits follow Conventional Commits, for example:

- `feat(backend): ...`
- `feat(go-agent): ...`
- `fix(panel): ...`
- `feat(runtime): ...`

## Security Notes

- Never log or commit API tokens, register tokens, certificates, or files under `panel/data/`
- Treat agent registration/update endpoints as sensitive
- Keep public asset routes limited to bootstrap scripts and published agent binaries
