# Test Policy

Run tests by module. The fast commands are:

```sh
cd go-agent && go test -p=16 -short -count=1 -timeout=30s ./...
cd panel/backend-go && go test -p=16 -short -count=1 -timeout=30s ./...
cd panel/frontend && npm test
```

Run the affected Go module's complete untagged tier before release:

```sh
cd go-agent && go test -p=16 -count=1 -timeout=30s ./...
cd panel/backend-go && go test -p=16 -count=1 -timeout=30s ./...
```

Run the integration packages when changing persistence, certificate lifecycle, or process handoff:

```sh
cd go-agent && go test -p=16 -tags=integration -count=1 -timeout=30s -run '^TestIntegration' ./embedded ./internal/app ./internal/core ./internal/hotrestart ./internal/modules/certs ./internal/modules/diagnostics ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay ./internal/platform ./pkg/acmeflow
cd panel/backend-go && go test -p=16 -tags=integration -count=1 -timeout=30s -run '^TestIntegration' ./cmd/nre-control-plane ./internal/controlplane/coordinator ./internal/controlplane/cutover ./internal/controlplane/revision ./internal/controlplane/service ./internal/controlplane/storage
```

The frontend has one behavior suite rather than separate fast and full commands. The Go full tier includes tests that opt out under `testing.Short`. The integration tier selects only packages that own `integration`-tagged tests and uses the repository-wide `TestIntegration` prefix, avoiding a second run of unrelated unit packages. The cutover soak is Linux-only and runs in the scheduled CI integration tier.

The canonical Go commands use 16 package workers so package compilation and
execution overlap on developer and CI machines. Tests that own isolated
temporary stores use `t.Parallel`; process environment, fixed-port, and shared
router-state tests remain serial.

## Official Plugin Market

The official-market unit suite is offline. It creates nine canonical packages
under `t.TempDir()`, signs the raw 32-byte package and provenance payload
digests with an ephemeral Ed25519 fixture key, and verifies the market,
complete package digests, file manifests, signed SDK provenance, and tamper
rejection without cloning or contacting GitHub:

```sh
cd panel/backend-go
go test ./internal/controlplane/plugins/... -run 'OfficialMarketV1' -count=1
go test ./internal/controlplane/marketplace/... -count=1
```

The release acceptance command is intentionally separate and requires network
access. It resolves the current `official-market` branch through the repository
root policy file, validates the isolated checkout, and reports the exact Git OID
and package count that were consumed:

```sh
cd panel/backend-go
go run ./cmd/nre-plugin-validator --official-lock ../../official-market.lock
```

Run that command only as an explicit release/integration gate. Ordinary unit and
short test tiers never fetch the official repository.

The complete official-market release gate has one network-enabled entry point:

```powershell
pwsh -File scripts/official-market-release/run.ps1
```

The script validates all nine signed packages and every declared artifact,
performs all published RPC handshakes in networkless containers, and supplies
the verified WAF artifact to the performance process.
It aggregates all package and runtime failures before returning. The Go tests
themselves never fetch a repository. A missing artifact makes the standalone
WAF test skip; the release script rejects that skip, zero matched tests, or any
throughput, p95, p99, and process-memory measurement failure.

## Internal PKI Multi-Process E2E

The internal PKI harness is a third, standalone Go module. Its canonical entry point is identical on Windows and Linux and does not require a shell, WSL, Docker, or the local ACME fixture:

```sh
cd tests/internal-pki
go test -tags=integration -count=1 ./...
```

The harness builds the control-plane and agent integration-tag binaries, starts them below `t.TempDir()` on dynamically allocated loopback ports, and observes only public CLI, HTTP, listener, exit-status, and persisted-file boundaries. It owns the multi-process assertions for enrollment replay, remote/embedded identity separation, relay mTLS attacks and convergence, crash-safe generations, protected backup and migration, epoch fencing, cooperative single-active behavior, and the token-authenticated control-protocol boundary. Product binaries do not expose the harness clock or fault barriers in release builds.

On POSIX systems, `scripts/test-internal-pki-e2e.sh` is only a convenience adapter: it resolves the repository root, changes to the standalone module, and `exec`s the canonical Go command. Windows runs the Go command directly. The scheduled and manually dispatched CI integration tier uses a separate Ubuntu job for this module; it is not added to the two-product-module matrix and does not share the Pebble Docker fixture.

Integration test names use the `TestInternalPKI` prefix. Before using a focused `-run` expression, confirm the intended name is present so a successful `no tests to run` result cannot be mistaken for coverage:

```sh
cd tests/internal-pki
go test -tags=integration -list '^TestInternalPKI' ./...
```

The production regression for a degraded PKI heartbeat with no revision update remains in `go-agent/internal/app/pki_sync_lifecycle_test.go`: it exercises App to SyncController to Runtime, closes the active relay listener and session, and preserves ordinary token-authenticated heartbeat and revision pull. The standalone E2E module treats that product-level regression as a dependency rather than importing or copying either product module's `internal` packages.

## Local ACME Fixture

Real ACME lifecycle tests use the test-only Pebble fixture in `scripts/acme-integration`. Start it before the Go integration tier:

```sh
docker compose -f scripts/acme-integration/docker-compose.yaml up -d --wait --wait-timeout 60
pebble_ca="${TMPDIR:-/tmp}/nre-pebble.minica.pem"
docker compose -f scripts/acme-integration/docker-compose.yaml cp pebble:/test/certs/pebble.minica.pem "$pebble_ca"
export SSL_CERT_FILE="$pebble_ca"
export NRE_ACME_TEST_DIRECTORY_URL=https://127.0.0.1:14000/dir
export NRE_ACME_TEST_MANAGEMENT_URL=https://127.0.0.1:15000
export NRE_ACME_TEST_CHALLTESTSRV_URL=http://127.0.0.1:8055
```

The fixture pins Pebble and `pebble-challtestsrv` to `v2.10.1`, advertises `default` and `shortlived` certificate profiles, and performs real challenge validation without production credentials. It does not mount repository runtime data. The three `NRE_ACME_TEST_*_URL` variables and `SSL_CERT_FILE` are integration-test inputs, not product runtime configuration.

Host ports default to `14000`, `15000`, and `8055` on `127.0.0.1`. Set the matching `NRE_ACME_TEST_DIRECTORY_PORT`, `NRE_ACME_TEST_MANAGEMENT_PORT`, or `NRE_ACME_TEST_CHALLTESTSRV_PORT` compose variable before startup when a port is occupied, and update the corresponding test URL. See `scripts/acme-integration/README.md` for the full fixture contract.

Always clean up the fixture, including after a failed test run:

```sh
docker compose -f scripts/acme-integration/docker-compose.yaml down --volumes --remove-orphans
```

The scheduled and manually dispatched CI integration tier performs the same start, health wait, test, and unconditional cleanup sequence. Fast and untagged full tests never start or require the fixture.

## What Belongs In The Fast Tier

- Pure transformations, validation, and state-machine invariants.
- Regression tests for a concrete failure mode.
- Service behavior using fakes or in-memory collaborators.
- Frontend tests that exercise a user action, request payload, navigation, or state transition.

## What Belongs In The Full Tier

- Cross-package lifecycle scenarios that use local fakes and temporary files.
- Tests that are meaningful without an external service or platform-specific build tag.

## What Belongs In The Integration Tier

- Real process handoff or listener lifecycle tests.
- Real certificate issuance, key generation, and durable certificate recovery.
- SQLite transaction, migration, concurrent writer, and restart scenarios.
- Cross-component browser behavior that cannot be expressed as a pure helper test.

Do not add tests that inspect source text with regular expressions, duplicate type or constant declarations, or assert implementation details without an observable contract. Prefer one scenario with all relevant assertions over many tests that repeat the same setup. Use channels, fake clocks, and explicit synchronization instead of sleeps.
