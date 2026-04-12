# Master Embedded Synthetic Cutover Verification Design

**Date:** 2026-04-12  
**Status:** Proposed

## Goal

Add a fully in-process Go integration test suite that verifies the pure-Go master runtime with embedded local agent can apply synthetic control-plane state and serve real HTTP, L4, relay, and certificate-backed traffic without Docker or external services.

## Scope

This design covers only the first verification phase:

- master control plane running in-process
- embedded local agent only
- synthetic fixture data generated in-test
- HTTP, L4, relay, and certificate/runtime-state validation
- uploaded/internal CA materials plus managed-certificate policy and ACME status-field semantics

This phase does **not** cover:

- remote agent registration or heartbeat from a separate process
- Docker multi-node topology
- real ACME issuance
- copied production data

Those will be handled in a later Docker-based cutover verification phase.

## Recommended Approach

Implement a new cutover-focused integration test package under `panel/backend-go/internal/controlplane/cutover/`. This package will generate a complete synthetic `dataDir`, start the control plane and embedded local agent in-process, launch local HTTP/TCP backends, and validate real traffic through the resulting runtime listeners.

This is preferred over extending existing `go-agent/internal/app/local_runtime_test.go` because the first-phase goal is not just runtime correctness. It is the end-to-end cutover loop from control-plane storage and APIs through embedded apply and back into persisted runtime state.

## Architecture

The test harness will assemble these components inside a single test process:

1. A synthetic `dataDir` containing:
   - SQLite `panel.db` created through the Go control-plane GORM schema path
   - managed certificate material directories
   - initial local runtime state
2. The Go control plane using `panel/backend-go`
3. The embedded local agent path enabled by default
4. A local HTTP echo backend
5. A local TCP echo backend
6. Real relay dial verification using `relay.Dial`

The tests will not mock traffic. They will exercise real listeners opened by the Go runtime and assert that data is forwarded correctly.

## Synthetic Fixture Model

The synthetic fixture is built explicitly during each test run rather than copied from any external snapshot. Each test gets an isolated temporary `dataDir`.

Schema ownership for the fixture is Go-only:

- the fixture builder must initialize `panel.db` through a shared GORM-owned schema/bootstrap helper from `panel/backend-go/internal/controlplane/storage`
- the Go/GORM model layer is the single schema source of truth for fresh test databases
- hand-written SQL baselines under `panel/backend-go/internal/controlplane/storage/testdata/` are transitional only and should be removed as part of this work

The fixture must populate at least these persisted entities after the GORM schema/bootstrap step:

- `agents`
  - one local agent row for `local`
- `rules`
  - one HTTP rule pointing to the HTTP echo backend
- `l4_rules`
  - one TCP rule pointing to the TCP echo backend
- `relay_listeners`
  - one relay listener with real certificate and trust references
- `managed_certificates`
  - one `uploaded` server certificate
  - one `internal_ca` trust certificate
  - one managed-certificate policy row carrying ACME status fields
- `local_agent_state`
  - initial revision and apply metadata

Material files under `managed_certificates/<normalized-host>/` must also be created so the runtime consumes real certificate/key pairs from disk.

## Certificate Model

The fixture builder generates all certificate material locally during the test run:

- server certificates for HTTPS and relay listener usage
- an internal CA certificate for trust validation
- optional CA-signed leaf material when the relay path needs trust-chain validation

The first phase will model three classes of certificate behavior:

1. `uploaded`
   - validates direct runtime use of provided certificate material
2. `internal_ca`
   - validates relay trust and CA loading semantics
3. managed-certificate policy rows with ACME status fields
   - validates that control-plane and embedded local-agent paths consume policy/status metadata correctly without invoking real ACME issuance

The design intentionally verifies ACME **state semantics**, not real ACME networking.

## Test Cases

The suite will include four primary integration tests.

### 1. HTTP Rule Traffic

`TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic`

Expected behavior:

- control plane reports `local_apply_runtime=go-agent`
- embedded local agent reaches a stable applied state
- an HTTP request sent to the synthetic frontend listener is forwarded to the HTTP echo backend

Assertions:

- `/panel-api/info` reports Go local apply
- local agent revision/apply state converges
- response body contains backend echo marker

### 2. L4 Rule Traffic

`TestMasterEmbeddedCutoverAppliesL4RuleAndForwardsTCP`

Expected behavior:

- L4 listener binds successfully
- a TCP payload sent to the configured L4 port is forwarded to the TCP echo backend and returned intact
- no `last_sync_error` remains after apply stabilizes

### 3. Relay Traffic And Trust Chain

`TestMasterEmbeddedCutoverAppliesRelayListenerAndTrustChain`

Expected behavior:

- relay listener binds successfully
- relay tunnel traffic reaches the backend using real `relay.Dial`
- the configured server certificate and trusted CA materials are resolvable from fixture data

Assertions:

- listener port is open
- `relay.Dial` round-trip succeeds
- configured `certificate_id` is used
- configured `trusted_ca_certificate_ids` resolve into a usable trust pool

### 4. Managed Certificate State And Runtime Metadata

`TestMasterEmbeddedCutoverExposesManagedCertificateStateAndStableApplyMetadata`

Expected behavior:

- uploaded/internal CA/managed-policy rows are all visible to the control plane
- ACME-related state fields remain intact
- local runtime reaches stable `desired_revision/current_revision/last_apply_*`
- no residual `last_sync_error` remains

Assertions:

- `/panel-api/certificates` contains the expected rows
- `status`, `issuer_mode`, `certificate_type`, `usage`, and `acme_info` match fixture expectations
- runtime metadata converges to a stable, successful state

## Implementation Shape

The implementation should live in a focused test-only package tree:

- `panel/backend-go/internal/controlplane/cutover/fixture_builder.go`
  - builds the synthetic `dataDir`
  - initializes `panel.db` through the shared GORM schema/bootstrap helper
  - writes fixture rows through GORM-backed storage helpers plus certificate materials
  - returns useful IDs, ports, and expected revisions
- `panel/backend-go/internal/controlplane/cutover/runtime_harness.go`
  - starts control-plane dependencies and embedded local agent in-process
  - starts HTTP/TCP echo backends
  - exposes wait helpers and cleanup
- `panel/backend-go/internal/controlplane/cutover/assertions.go`
  - common helpers for revision convergence, relay round-trip, HTTP/L4 assertions
- `panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go`
  - contains the primary test cases

This layout keeps fixture generation, harness lifecycle, and test assertions separate so each file has one clear responsibility.

## Reused Runtime Pieces

The tests should reuse existing production code rather than reimplementing behavior:

- `storage.NewSQLiteStore`
- the shared GORM schema/bootstrap helper used by `storage.NewSQLiteStore`
- control-plane app/router wiring
- embedded local-agent startup path
- `relay.Dial`
- existing Go runtime behavior for HTTP/L4/relay listeners

The tests must not introduce a second implementation path just for verification.

## Storage Fixture Migration Requirement

As part of this phase, the storage compatibility fixture path must stop depending on hand-written SQL files as the long-term baseline.

Required outcome:

- `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go` seeds fixture databases through the same shared GORM schema/bootstrap helper used by production
- `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`

These SQL files may remain only transiently while the migration is in flight. The end state for this phase is Go/GORM-owned schema creation for storage tests and cutover tests alike.

## Port And Lifecycle Rules

To stay deterministic and CI-safe:

- all listener ports must be dynamically allocated
- each test must get its own temporary `dataDir`
- every started backend, runtime listener, and harness component must be closed in test cleanup
- tests must wait on explicit conditions instead of fixed sleeps whenever state convergence is required

## Verification Plan

The first-phase test suite is successful when:

- `cd panel/backend-go && go test ./internal/controlplane/cutover -count=1` passes
- `cd panel/backend-go && go test ./... -count=1` still passes
- the four cutover tests are repeatable on a developer machine without Docker or external network dependencies

## Rollout Strategy

Phase 1 adds the in-process synthetic cutover suite described here.

Phase 2 will build on this by adding Docker-based multi-node validation for:

- master + remote agent + relay node topology
- real join/register/heartbeat behavior
- container-network relay traffic
- end-to-end cutover using deployed nodes rather than a single in-process harness

The in-process suite is the long-term fast regression gate. The Docker multi-node suite is the slower deployment-confidence layer.

## Risks And Mitigations

### Risk: Test-only harness diverges from production wiring

Mitigation:

- reuse production constructors and runtime paths
- keep test helpers limited to fixture generation and orchestration

### Risk: Port and lifecycle flakiness

Mitigation:

- dynamic port allocation
- explicit readiness/state waits
- full cleanup of listeners and temp directories

### Risk: ACME semantics become too shallow

Mitigation:

- validate persisted policy/state fields and runtime consumption semantics now
- leave real issuance networking for the later Docker phase

## Decision Summary

- First phase is fully in-process
- Coverage includes HTTP, L4, relay, and certificate-backed traffic
- Coverage includes uploaded/internal CA plus managed-policy and ACME state semantics
- Scope is limited to master + embedded local agent
- Docker multi-node validation is deferred to the next phase
