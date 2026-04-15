# HTTP Slice Cache Design

## Summary

Add an optional slice-style cache to the Go HTTP proxy so large media objects can be fetched from upstream in fixed-size byte ranges and reused across later requests. The feature is configured per HTTP rule, defaults to off, and preserves the existing proxy path for requests that do not meet the slice-cache requirements.

## Goals

- Improve streaming stability for large media playback by reusing already-fetched byte ranges.
- Keep the feature scoped to the existing Go execution plane instead of reintroducing Nginx-only behavior.
- Enable the feature per HTTP rule so operators can opt in only for media routes that benefit from it.
- Preserve existing routing, relay, retry, redirect-rewrite, and header override behavior for requests that are not handled by the slice-cache path.

## Non-Goals

- No speculative prefetch of future slices.
- No multipart byte-range response assembly.
- No upload or bidirectional stream acceleration.
- No media-type-specific logic for Emby or Jellyfin paths.
- No cross-agent cache sharing.

## User-Facing Configuration

`model.HTTPRule` gains an optional `slice_cache` object. First version fields:

- `enabled`: enable slice-cache handling for this rule.
- `slice_size`: fixed upstream range size in bytes.
- `max_file_size`: upper bound for caching a single resource. Larger resources fall back to normal proxying.
- `max_cache_size`: per-rule cache budget used by background eviction for entries owned by that rule.

Defaults:

- Slice cache is disabled unless explicitly enabled.
- The cache directory is derived from the agent local data directory, for example `<agent-data>/http-slice-cache`, and is not configured per rule in v1.
- Only `GET` and `HEAD` are eligible. All other methods always use the current direct proxy path.

Control-plane and execution-plane schema changes must treat the new object as optional so existing rules and stored snapshots remain valid.

## Architecture

Implementation stays in the Go agent HTTP proxy layer.

- `go-agent/internal/model/http.go`
  - Extend `HTTPRule` with the new `slice_cache` configuration.
- `panel/backend-go/...`
  - Accept, persist, and emit the optional slice-cache fields without changing behavior for rules that omit them.
- `go-agent/internal/proxy/server.go`
  - Detect whether a request should enter the slice-cache path or continue through the current transport round trip path.
- `go-agent/internal/proxy/slicecache`
  - New package containing metadata probing, cache storage, slice fetch coordination, response assembly, and eviction helpers.

The current `routeEntry` remains the request owner. It delegates to the slice-cache module only when the rule is enabled and the request satisfies eligibility checks.

## Eligibility Rules

The slice-cache path is used only when all conditions below are true:

- Rule has `slice_cache.enabled = true`.
- Request method is `GET` or `HEAD`.
- Request is not a protocol upgrade.
- Request body is empty.
- Request is either a full-object request or a single byte range request.
- Upstream resource can be resolved to a stable object length.
- Upstream indicates byte-range support, or range probing proves it behaves correctly.
- Resource size does not exceed `max_file_size` when that limit is configured.

The following always fall back to the existing proxy flow:

- Multipart `Range` requests.
- Requests with unsupported range units.
- Requests whose upstream metadata is missing, inconsistent, or changes during access.
- Responses that are streamed without a stable length.
- Any slice-cache internal error detected before response headers are committed.

## Resource Identity And Cache Keys

Each cache entry identifies one upstream object version for one HTTP rule revision.

Primary cache key inputs:

- Rule identity: prefer `rule.ID`; if unavailable, fall back to normalized `frontend_url`.
- Rule revision.
- Chosen backend target origin.
- Rewritten upstream request path.
- Raw query string.

Tracked validators in metadata:

- `Content-Length`
- `ETag` when present
- `Last-Modified` when present

Rules:

- Query string stays part of the key because media servers often encode authorization or transcode variants there.
- Rule revision is part of the key so route updates naturally stop reusing old cache entries.
- If validators change for an existing entry, the entry is discarded and rebuilt from upstream.

## Metadata Probe

Before serving slices for a resource, the proxy probes upstream metadata.

Probe order:

1. Send `HEAD` to the rewritten upstream URL.
2. If `HEAD` is unsupported or does not provide reliable metadata, send `GET` with `Range: bytes=0-0`.

Probe output:

- Total object length.
- Whether byte ranges are supported.
- Validators used for cache invalidation.
- Headers that should be reproduced in final client responses where applicable.

If probing cannot establish a stable object length or valid range behavior, the request falls back to the existing proxy path and no cache entry is created.

## Request Handling Flow

### Full `GET`

1. Probe or load metadata.
2. Translate the requested object into slice indexes for the full object.
3. For each slice:
   - Read from local cache if present.
   - Otherwise request the exact slice range from upstream, then atomically persist it.
4. Stream the assembled object to the client with `200 OK`.

### Single-Range `GET`

1. Parse and validate the requested byte range against object length.
2. Map the requested range to one or more slice indexes.
3. Ensure all required slices exist, fetching only missing slices.
4. Stream only the requested subrange to the client with `206 Partial Content`.
5. Emit correct `Content-Range`, `Content-Length`, and `Accept-Ranges` headers.

### `HEAD`

1. Probe or load metadata.
2. Return synthesized headers that match how a cache-backed `GET` would respond.
3. Do not download object bodies or slices solely for `HEAD`.

## Response Semantics

Client-visible behavior must remain standards-compliant:

- Full-object `GET` returns `200`.
- Single-range `GET` returns `206`.
- Unsatisfiable ranges return `416`.
- `Accept-Ranges: bytes` is set only when the proxy is confident the resource is range-capable.
- `Content-Length` reflects the exact bytes returned to the client.

The slice-cache path does not change request path rewriting, custom headers, pass-through proxy headers, or relay-chain transport selection. It changes only how upstream bytes are acquired and reused.

## Storage Layout

Each cached resource lives in its own directory under the agent-managed cache root.

Proposed layout:

- `meta.json`
- `00000000.part`
- `00000001.part`
- `...`

`meta.json` stores:

- Cache key and rule revision.
- Upstream object length.
- Slice size.
- Validator fields.
- Last access time.
- Slice completion map or bitmap.

Write rules:

- Slice downloads are written to a temporary file first, then atomically renamed.
- Readers never observe partially written slice files.
- Metadata updates are written after successful slice persistence.

## Concurrency Model

The slice-cache module coordinates concurrent requests for the same object.

Required protections:

- One metadata probe per resource at a time.
- One upstream fetch per missing slice at a time.
- Other requests wait for the in-flight result and then read the completed slice locally.

This avoids duplicate range fetches when multiple players start or seek to the same region concurrently.

## Failure Handling

The system is intentionally conservative.

- If an error occurs before response headers are written, abandon slice-cache handling and retry through the current normal proxy path.
- If validators or object length change while serving, invalidate the cache entry and stop using cached slices for that resource.
- If a later slice fetch fails after the response has started streaming, terminate the current response and mark the cache entry invalid so the next request reprobes.
- If upstream does not honor the requested byte range correctly, invalidate the entry and fall back to normal proxying.

The proxy must never combine slices from different upstream object versions into one response.

## Eviction

V1 eviction stays simple:

- Track last access time per resource.
- Enforce each rule's `max_cache_size` in a background cleanup pass for entries belonging to that rule.
- Prefer removing least-recently-used resources first.

Eviction is best-effort. If cleanup lags, new requests may bypass slice-cache temporarily instead of blocking proxy service.

## Testing Strategy

Minimum automated coverage:

1. First full-object request fetches slices from upstream and persists them.
2. Repeated request for the same object is served from cache without another upstream body fetch.
3. Single-range request spanning one slice returns correct `206` and headers.
4. Single-range request spanning multiple slices assembles the expected bytes and headers.
5. `HEAD` synthesizes metadata without downloading body data.
6. Multipart `Range` requests bypass slice-cache and use current proxy behavior.
7. Upstream without reliable range support bypasses slice-cache.
8. Validator change invalidates a stale entry and prevents mixed-version responses.
9. Concurrent requests for the same missing slice trigger only one upstream fetch.

Execution verification after implementation:

- `cd panel/backend-go && go test ./...`
- `cd go-agent && go test ./...`
- `docker build -t nginx-reverse-emby .`

## Rollout Notes

- Feature is opt-in per rule and safe to ship dark by default.
- Existing installations continue using the current proxy path until rules explicitly enable slice-cache.
- Documentation must describe intended usage for large media routes and note that speculative prefetch is not part of v1.
