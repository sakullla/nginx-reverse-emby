# Test Policy

Run tests by module. The fast commands are:

```sh
cd go-agent && go test -short -count=1 -timeout=90s ./...
cd panel/backend-go && go test -short -count=1 -timeout=90s ./...
cd panel/frontend && npm test
```

Run the affected Go module's full tier before release or when changing persistence, certificate lifecycle, or process handoff:

```sh
cd go-agent && go test -tags=integration -count=1 -timeout=8m ./...
cd panel/backend-go && go test -tags=integration -count=1 -timeout=8m ./...
```

The frontend has one behavior suite rather than separate fast and full commands. The Go full tier enables tests tagged `integration` and includes tests that opt out under `testing.Short`. The cutover soak is Linux-only and runs in the scheduled CI full tier.

## Local ACME Fixture

Real ACME lifecycle tests use the test-only Pebble fixture in `scripts/acme-integration`. Start it before the Go full tier:

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

The scheduled and manually dispatched CI full tier performs the same start, health wait, test, and unconditional cleanup sequence. Fast tests never start or require the fixture.

## What Belongs In The Fast Tier

- Pure transformations, validation, and state-machine invariants.
- Regression tests for a concrete failure mode.
- Service behavior using fakes or in-memory collaborators.
- Frontend tests that exercise a user action, request payload, navigation, or state transition.

## What Belongs In The Full Tier

- Real process handoff or listener lifecycle tests.
- Real certificate issuance, key generation, and durable certificate recovery.
- SQLite transaction, migration, concurrent writer, and restart scenarios.
- Cross-component browser behavior that cannot be expressed as a pure helper test.

Do not add tests that inspect source text with regular expressions, duplicate type or constant declarations, or assert implementation details without an observable contract. Prefer one scenario with all relevant assertions over many tests that repeat the same setup. Use channels, fake clocks, and explicit synchronization instead of sleeps.
