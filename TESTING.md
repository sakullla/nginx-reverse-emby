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
