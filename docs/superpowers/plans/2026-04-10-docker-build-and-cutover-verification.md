# Docker Build And Cutover Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a pure-Go master image, keep remote agent assets published, and prove the cutover against copied production-like data before release.

**Architecture:** Replace the Node runtime image with the Go control-plane binary while keeping the frontend build stage and published agent binaries. Add repeatable verification scripts that load copied panel data into the new control-plane image, exercise API compatibility, confirm embedded local-agent startup, and run the existing Go test suite plus image build checks.

**Tech Stack:** Docker multi-stage builds, docker compose, Go tests, copied SQLite data fixtures, shell verification scripts.

---

## File Map

**Create**
- `scripts/verify-pure-go-master.sh`
- `go-agent/internal/controlplane/http/compat_fixture_test.go`
- `go-agent/internal/controlplane/storage/testdata/README.md`

**Modify**
- `Dockerfile`
- `docker-compose.yaml`
- `README.md`

**Do Not Modify**
- `conf.d/`
- `nginx.conf`
- `deploy.sh`

### Task 1: Build the Go master image and keep agent assets published

**Files:**
- Modify: `Dockerfile`
- Modify: `docker-compose.yaml`

- [ ] **Step 1: Write the failing image verification commands**

```bash
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
docker run --rm nginx-reverse-emby:pure-go /usr/local/bin/nre-control-plane --help
```

- [ ] **Step 2: Run build to verify it fails before the Dockerfile change**

Run: `docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .`
Expected: FAIL because the runtime stage still depends on `node` and `server.js`

- [ ] **Step 3: Update the control-plane runtime stage**

```dockerfile
FROM debian:bookworm-slim AS control-plane-runtime
RUN set -eux; apt-get update; apt-get install -y --no-install-recommends ca-certificates; rm -rf /var/lib/apt/lists/*
WORKDIR /opt/nginx-reverse-emby
COPY --from=frontend-builder /build/dist ./panel/frontend/dist/
COPY --from=go-builder /out/nre-control-plane /usr/local/bin/nre-control-plane
COPY --from=go-builder /out/nre-agent-linux-amd64 ./panel/public/agent-assets/nre-agent-linux-amd64
CMD ["/usr/local/bin/nre-control-plane"]
```

- [ ] **Step 4: Run build and compose rendering**

Run: `docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .`
Expected: PASS

Run: `docker compose config`
Expected: PASS and no Node entrypoint remains

- [ ] **Step 5: Commit**

```bash
git add Dockerfile docker-compose.yaml
git commit -m "build(docker): switch master image to go control-plane"
```

### Task 2: Add a shadow-data verification script for copied panel data

**Files:**
- Create: `scripts/verify-pure-go-master.sh`
- Create: `go-agent/internal/controlplane/storage/testdata/README.md`

- [ ] **Step 1: Write the failing verification script skeleton**

```sh
#!/bin/sh
set -eu

DATA_DIR="${1:?usage: verify-pure-go-master.sh <copied-panel-data-dir>}"

docker run --rm \
  -e NRE_CONTROL_PLANE_ADDR=0.0.0.0:8080 \
  -e NRE_CONTROL_PLANE_DATA_DIR=/data \
  -e NRE_PANEL_TOKEN=test-token \
  -e NRE_REGISTER_TOKEN=test-register-token \
  -v "${DATA_DIR}:/data" \
  -p 18080:8080 \
  nginx-reverse-emby:pure-go
```

- [ ] **Step 2: Run script to verify it fails before API checks are added**

Run: `sh scripts/verify-pure-go-master.sh ./data-copy`
Expected: FAIL or hang without assertions

- [ ] **Step 3: Add API compatibility checks**

```sh
curl -fsS -H 'X-Panel-Token: test-token' http://127.0.0.1:18080/panel-api/health >/dev/null
curl -fsS -H 'X-Panel-Token: test-token' http://127.0.0.1:18080/panel-api/info >/dev/null
curl -fsS -H 'X-Panel-Token: test-token' http://127.0.0.1:18080/panel-api/agents >/dev/null
curl -fsS http://127.0.0.1:18080/panel-api/public/join-agent.sh >/dev/null
```

- [ ] **Step 4: Run shadow-data verification**

Run: `sh scripts/verify-pure-go-master.sh ./data-copy`
Expected: PASS with zero non-2xx API responses

- [ ] **Step 5: Commit**

```bash
git add scripts/verify-pure-go-master.sh go-agent/internal/controlplane/storage/testdata/README.md
git commit -m "test(cutover): add pure-go master shadow-data verification"
```

### Task 3: Document the release gate and cutover procedure

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write the failing documentation check**

```bash
rg -n "Pure Go Cutover Verification" README.md
```

- [ ] **Step 2: Run check to verify it fails before the section is added**

Run: `rg -n "Pure Go Cutover Verification" README.md`
Expected: no matches

- [ ] **Step 3: Add the release gate and commands**

````md
## Pure Go Cutover Verification

Before replacing the production master image, run:

```bash
cd go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```
````

- [ ] **Step 4: Run check to verify it passes**

Run: `rg -n "Pure Go Cutover Verification" README.md`
Expected: one match

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: add pure-go cutover verification gate"
```
