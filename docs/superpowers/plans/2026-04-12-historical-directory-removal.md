# Historical Directory Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the legacy `panel/backend/` and `docker/` source trees after migrating their remaining schema, documentation, and runtime references into Go-owned locations.

**Architecture:** Keep the active control-plane in `panel/backend-go/` and the runtime in `go-agent/`. Move any remaining compatibility fixtures that still read Node/Prisma assets into `panel/backend-go/internal/controlplane/storage/testdata/`, update contributor/runtime documentation to describe the Go-only architecture, then delete the legacy directories and verify the Go-only image and API smoke checks still pass.

**Tech Stack:** Go 1.26.2, GORM + sqlite, Gin HTTP control-plane, Docker multi-stage image, shell verification scripts, repo docs under `README.md` and `AGENTS.md`.

---

## File Map

**Create**
- `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`

**Modify**
- `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- `README.md`
- `AGENTS.md`
- `CLAUDE.md`
- `panel/frontend/AGENTS.md`
- `Dockerfile`
- `docker-compose.yaml`
- `scripts/verify-pure-go-master.sh`

**Delete**
- `panel/backend/`
- `docker/`

**Do Not Modify**
- `deploy.sh`
- `conf.d/`
- `nginx.conf`

### Task 1: Move storage compatibility fixtures into Go-owned testdata

**Files:**
- Create: `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- Create: `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Capture the failing dependency check**

Run: `rg -n "panel\\\\backend|storage-prisma-core|prisma\\\\migrations" panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
Expected: matches showing the Go storage tests still read `panel/backend` files directly.

- [ ] **Step 2: Snapshot the legacy schema into Go-owned fixtures**

Create `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql` with the canonical base table creation statements that currently come from `panel/backend/storage-prisma-core.js`.

Create `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql` with the ordered SQLite migration statements that currently come from `panel/backend/prisma/migrations/*.sql`.

- [ ] **Step 3: Rewrite the fixture loader to read local testdata only**

Replace the repository-root lookup helpers in `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go` so `seedSQLiteFixtureFromCanonicalSchema()` loads SQL from:

```go
filepath.Join("testdata", "schema_base.sql")
filepath.Join("testdata", "schema_migrations.sql")
```

and remove the code that parses JavaScript string literals out of `storage-prisma-core.js`.

- [ ] **Step 4: Run focused storage verification**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -count=1`
Expected: PASS with no remaining dependency on `panel/backend`.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql
git commit -m "test(storage): own sqlite compatibility fixtures in go"
```

### Task 2: Rewrite docs and contributor instructions for the Go-only control plane

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Modify: `panel/frontend/AGENTS.md`

- [ ] **Step 1: Capture the failing docs search**

Run: `rg -n "panel/backend|node server\\.js|Node backend|docker/" README.md AGENTS.md CLAUDE.md panel/frontend/AGENTS.md`
Expected: matches describing the removed Node backend and historical `docker/` runtime path.

- [ ] **Step 2: Rewrite architecture and verification commands**

Update the docs so they consistently describe:
- `panel/backend-go` as the control-plane API/runtime
- `go-agent` as the execution plane
- verification commands based on `go test`, frontend build, Docker build, and the pure-Go master smoke script
- `deploy.sh`, `conf.d/`, and `nginx.conf` as retained legacy standalone assets outside the default runtime

- [ ] **Step 3: Re-run the docs search**

Run: `rg -n "panel/backend|node server\\.js|Node backend" README.md AGENTS.md CLAUDE.md panel/frontend/AGENTS.md`
Expected: no matches that describe active runtime or contributor workflows via `panel/backend`.

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md CLAUDE.md panel/frontend/AGENTS.md
git commit -m "docs: remove legacy node backend guidance"
```

### Task 3: Remove runtime/build references and delete the legacy directories

**Files:**
- Modify: `Dockerfile`
- Modify: `docker-compose.yaml`
- Modify: `scripts/verify-pure-go-master.sh`
- Delete: `panel/backend/`
- Delete: `docker/`

- [ ] **Step 1: Capture the failing repository search**

Run: `rg -n "panel/backend|docker/|20-panel-backend|15-panel-config" Dockerfile docker-compose.yaml scripts README.md AGENTS.md CLAUDE.md panel/frontend/AGENTS.md panel/backend-go go-agent`
Expected: any remaining matches that still rely on the legacy directories.

- [ ] **Step 2: Move or replace anything still needed**

If a runtime or build asset still points at `docker/`, relocate the needed content into a surviving path such as `scripts/` or inline it into the Docker/runtime config. Do not move or edit `deploy.sh`, `conf.d/`, or `nginx.conf`.

- [ ] **Step 3: Delete the obsolete source trees**

Remove:

```text
panel/backend/
docker/
```

after the repository search shows no surviving production or test dependency on either directory.

- [ ] **Step 4: Re-run the repository search**

Run: `rg -n "panel/backend|docker/|20-panel-backend|15-panel-config" . -g '!conf.d/**' -g '!deploy.sh'`
Expected: no live code/build/doc references that block deletion.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove legacy node backend and docker runtime tree"
```

### Task 4: Run end-to-end Go-only verification after deletion

**Files:**
- Modify only if verification reveals a real breakage.

- [ ] **Step 1: Run Go test suites**

Run:

```bash
cd panel/backend-go && go test ./... -count=1
cd ../../go-agent && go test ./... -count=1
```

Expected: PASS

- [ ] **Step 2: Run image build verification**

Run: `docker build -t nginx-reverse-emby:go-verify --target control-plane-runtime .`
Expected: PASS

- [ ] **Step 3: Run pure-Go master smoke verification**

Run: `bash scripts/verify-pure-go-master.sh <temp-data-dir>`
Expected: PASS with embedded local agent present and panel endpoints healthy.

- [ ] **Step 4: Capture the clean deletion state**

Run:

```bash
Test-Path panel/backend
Test-Path docker
git status --short
```

Expected:
- `False` for both directory existence checks
- only intended tracked changes or a clean tree after commit

- [ ] **Step 5: Commit follow-up fixes if needed**

```bash
git add -A
git commit -m "fix(go-cutover): restore verification after legacy deletion"
```
