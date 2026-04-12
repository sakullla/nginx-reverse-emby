# Dockerfile Trixie Balance Design

## Context

The repository ships a multi-stage Docker build that produces:

- a Go control-plane runtime image
- a Go agent runtime image
- a control-plane image that embeds four cross-platform agent binaries for download

Current constraints and goals:

- keep embedding the cross-platform agent assets in the control-plane image
- balance build speed and runtime image size rather than optimizing only one side
- migrate all remaining `bookworm` base images to `trixie`
- avoid changing runtime behavior or release semantics

## Current State

The current Dockerfile already uses `node:24-trixie-slim` for the frontend build, but still uses:

- `golang:1.26.2-bookworm` for both Go build stages
- `debian:bookworm-slim` for both runtime stages

The current Go build flow has two inefficiencies:

1. `go-agent` copies the whole source tree before any dependency work, so dependency download layers are invalidated more often than necessary.
2. `backend-go` also copies the full local `go-agent` tree because of the local `replace` directive, but its dependency preparation is only partially separated from later source copies.

## Options Considered

### Option A: Balanced cache-first update

Keep the current multi-stage structure, migrate all remaining bases to `trixie`, and improve cacheability by separating Go module metadata copies from source copies before dependency download.

Pros:

- low-risk, behavior-preserving change
- better incremental rebuild performance
- keeps the Dockerfile readable
- no packaging or runtime behavior changes

Cons:

- final image still includes four embedded agent assets, so runtime size only improves marginally

### Option B: More aggressive builder consolidation

Merge more of the Go build logic into fewer stages to maximize shared source preparation and cache reuse.

Pros:

- may reduce repeated setup work across builders

Cons:

- more complex Dockerfile
- harder to reason about cache invalidation
- limited benefit for runtime image size

### Option C: Runtime-minimization focused

Preserve current builders but push harder on runtime base reduction and payload trimming.

Pros:

- potentially smallest images

Cons:

- higher compatibility and maintenance risk
- conflicts with the requirement to keep bundled cross-platform agent assets
- weaker speed/size balance than requested

## Chosen Approach

Choose Option A.

This approach matches the requested balance between build speed and image size while preserving current behavior. The biggest unavoidable payload remains the bundled cross-platform agent binaries, so the most defensible optimization is to improve cache hit rate and keep the runtime images on slim Debian bases.

## Design

### Base image migration

Update the following stages:

- `golang:1.26.2-bookworm` -> `golang:1.26.2-trixie`
- `debian:bookworm-slim` -> `debian:trixie-slim`

`node:24-trixie-slim` remains unchanged.

This makes all builder and runtime stages consistent on the same Debian family without altering the application behavior.

### Go agent builder changes

Restructure the `go-builder` stage to improve cache reuse:

1. copy `go-agent/go.mod` and `go-agent/go.sum` first
2. run module download using the existing module cache mount
3. copy the rest of `go-agent/`
4. build the four embedded cross-platform binaries
5. build the target-platform runtime agent binary

This reduces unnecessary invalidation when only Go source files change but module metadata does not.

### Control-plane builder changes

Restructure the `backend-go-builder` stage to preserve the existing local replace behavior while improving cacheability:

1. copy `panel/backend-go/go.mod` and `panel/backend-go/go.sum`
2. copy `go-agent/go.mod` and `go-agent/go.sum`
3. run `go mod download` from the backend module with the same cache mount strategy
4. copy `go-agent/` and `panel/backend-go/` source trees
5. build `nre-control-plane`

Because the backend module uses `replace github.com/sakullla/nginx-reverse-emby/go-agent => ../../go-agent`, the builder must still have the local `go-agent` module path present. The design keeps that behavior intact.

### Runtime images

Keep both runtime stages on slim Debian images and continue installing only `ca-certificates`.

No runtime packaging behavior changes are planned:

- `go-agent-runtime` still contains only the target-platform `nre-agent`
- `control-plane-runtime` still embeds the frontend assets, backend binary, scripts, and four downloadable cross-platform agent binaries

### Files in scope

- `Dockerfile`

No other files are expected to change unless verification exposes a compatibility issue.

## Error Handling and Compatibility

This design is intentionally behavior-preserving:

- no environment variable changes
- no file layout changes inside runtime images
- no changes to `docker-compose.yaml`
- no changes to download asset names or paths

Primary compatibility risk is upstream package availability differences between Debian `bookworm` and `trixie`. Verification will explicitly cover full image build success to catch this.

## Verification Plan

Minimum verification for implementation:

1. `docker build -t nginx-reverse-emby .`
2. confirm the build still produces both runtime targets successfully
3. inspect the resulting image build log for cacheable dependency steps behaving as expected on rebuild

If implementation touches anything beyond the Dockerfile, rerun the relevant repository checks for that area.

## Out of Scope

- changing which cross-platform agent assets are bundled
- downloading agent assets on demand
- changing runtime entrypoints or environment defaults
- switching away from Debian slim runtime bases
- refactoring `docker-compose.yaml`
