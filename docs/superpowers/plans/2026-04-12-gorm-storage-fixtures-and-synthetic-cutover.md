# GORM Storage Fixtures And Synthetic Cutover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make GORM the schema source of truth for fresh control-plane SQLite databases and add an in-process master plus embedded-local-agent cutover suite that verifies HTTP, L4, relay, and certificate behavior against a synthetic fixture.

**Architecture:** Extract a shared storage schema/bootstrap helper inside `panel/backend-go/internal/controlplane/storage` and use it everywhere fresh `panel.db` files are created. Build the cutover suite in a dedicated `internal/controlplane/cutover` package that reuses production storage, router, embedded local-agent, and relay runtime paths instead of adding a test-only execution path.

**Tech Stack:** Go 1.26.2, GORM + SQLite (`github.com/glebarez/sqlite`), Gin-based control-plane HTTP stack, existing `go-agent` runtime and `relay.Dial`, Go integration tests.

---

## File Map

**Create**
- `panel/backend-go/internal/controlplane/storage/schema.go`
- `panel/backend-go/internal/controlplane/cutover/fixture_builder.go`
- `panel/backend-go/internal/controlplane/cutover/runtime_harness.go`
- `panel/backend-go/internal/controlplane/cutover/assertions.go`
- `panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go`

**Modify**
- `docs/superpowers/specs/2026-04-12-master-embedded-synthetic-cutover-verification-design.md`
- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- `panel/backend-go/internal/controlplane/storage/testdata/README.md`

**Delete**
- `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`

### Task 1: Make storage schema/bootstrap GORM-owned

**Files:**
- Create: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write the failing schema bootstrap test**

```go
func TestBootstrapSQLiteSchemaCreatesFreshPanelDatabaseWithoutSQLFixtures(t *testing.T) {
	dataRoot := t.TempDir()
	db, err := openSQLiteForTest(filepath.Join(dataRoot, "panel.db"))
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	state, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if state.ID != 1 || state.LastApplyStatus != "success" {
		t.Fatalf("unexpected local state: %+v", state)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run TestBootstrapSQLiteSchemaCreatesFreshPanelDatabaseWithoutSQLFixtures -count=1 -v`
Expected: FAIL with `undefined: BootstrapSQLiteSchema`

- [ ] **Step 3: Implement the shared GORM schema helper**

Add a helper in `panel/backend-go/internal/controlplane/storage/schema.go` that:
- opens/works with a provided `*gorm.DB`
- runs `AutoMigrate` across every SQLite row model used by the control plane
- creates any missing indexes that are required by existing queries
- inserts the singleton `local_agent_state` row with `id = 1` if missing

Example shape:

```go
func BootstrapSQLiteSchema(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(
		&AgentRow{},
		&HTTPRuleRow{},
		&L4RuleRow{},
		&RelayListenerRow{},
		&ManagedCertificateRow{},
		&LocalAgentStateRow{},
		&VersionPolicyRow{},
		&MetaRow{},
	); err != nil {
		return err
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&LocalAgentStateRow{ID: 1, LastApplyStatus: "success"}).Error
}
```

- [ ] **Step 4: Switch `NewSQLiteStore()` to the shared helper**

Update `panel/backend-go/internal/controlplane/storage/sqlite_store.go` so `NewSQLiteStore()` calls `BootstrapSQLiteSchema()` instead of maintaining its own hand-written DDL path.

- [ ] **Step 5: Run focused storage verification**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go
git commit -m "refactor(storage): make sqlite bootstrap gorm-owned"
```

### Task 2: Remove the handwritten SQL fixture baseline from storage tests

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- Modify: `panel/backend-go/internal/controlplane/storage/testdata/README.md`
- Delete: `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- Delete: `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`

- [ ] **Step 1: Write the failing regression test around the GORM-seeded fixture**

```go
func TestStoreLoadsAgentsAndRulesFromGORMSeededSQLite(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents() = %v, %v", agents, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run TestStoreLoadsAgentsAndRulesFromGORMSeededSQLite -count=1 -v`
Expected: FAIL because `seedSQLiteFixtureFromGORM` does not exist

- [ ] **Step 3: Replace SQL fixture loading with GORM/bootstrap seeding**

In `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`:
- replace `seedSQLiteFixtureFromCanonicalSchema()` with a helper that creates `panel.db`, calls `BootstrapSQLiteSchema()`, and seeds rows through GORM or store methods
- delete `loadFixtureSQLStatements`, `splitSQLStatements`, `execSQLiteStatement`, and `isIgnorableMigrationError`
- rename tests/helpers as needed so they no longer mention canonical SQL fixtures

- [ ] **Step 4: Update the testdata README and remove obsolete fixtures**

Update `panel/backend-go/internal/controlplane/storage/testdata/README.md` to state that GORM/bootstrap code is canonical and this directory is only for optional non-secret local verification assets.

Delete:
- `panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql`
- `panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql`

- [ ] **Step 5: Run focused verification**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -count=1`
Expected: PASS with no dependency on checked-in SQL schema files

- [ ] **Step 6: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/internal/controlplane/storage/testdata/README.md
git rm panel/backend-go/internal/controlplane/storage/testdata/schema_base.sql panel/backend-go/internal/controlplane/storage/testdata/schema_migrations.sql
git commit -m "test(storage): remove handwritten sqlite fixture baseline"
```

### Task 3: Add the synthetic cutover harness and fixture builder

**Files:**
- Create: `panel/backend-go/internal/controlplane/cutover/fixture_builder.go`
- Create: `panel/backend-go/internal/controlplane/cutover/runtime_harness.go`
- Create: `panel/backend-go/internal/controlplane/cutover/assertions.go`
- Test: `panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go`

- [ ] **Step 1: Write the first failing cutover test for HTTP traffic**

```go
func TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic(t *testing.T) {
	harness := newCutoverHarness(t)
	defer harness.Close()

	resp := harness.GetPanel("/panel-api/info")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %d", resp.StatusCode)
	}

	body := harness.GetHTTPFrontend("fixture-http.example.test")
	if !strings.Contains(body, "backend:http") {
		t.Fatalf("unexpected frontend body %q", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/cutover -run TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic -count=1 -v`
Expected: FAIL because the cutover harness package does not exist yet

- [ ] **Step 3: Implement the fixture builder**

Create `panel/backend-go/internal/controlplane/cutover/fixture_builder.go` with helpers that:
- allocate per-test ports
- create a temp `dataDir`
- initialize `panel.db` through `storage.BootstrapSQLiteSchema()`
- seed local agent, HTTP rule, L4 rule, relay listener, managed certificates, and local agent state through the storage layer
- generate and persist certificate/key material under `managed_certificates/<normalized-host>/`

- [ ] **Step 4: Implement the runtime harness**

Create `panel/backend-go/internal/controlplane/cutover/runtime_harness.go` with a test helper that:
- starts HTTP and TCP echo backends
- starts the control-plane router and embedded local agent in-process
- exposes cleanup and wait helpers for apply convergence

- [ ] **Step 5: Add shared assertions**

Create `panel/backend-go/internal/controlplane/cutover/assertions.go` with reusable helpers for:
- waiting on stable local apply metadata
- issuing HTTP requests through the runtime listener
- round-tripping TCP bytes through the L4 listener
- dialing relay traffic with `relay.Dial`

- [ ] **Step 6: Run the first cutover test**

Run: `cd panel/backend-go && go test ./internal/controlplane/cutover -run TestMasterEmbeddedCutoverAppliesHTTPRuleAndServesTraffic -count=1 -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/cutover/fixture_builder.go panel/backend-go/internal/controlplane/cutover/runtime_harness.go panel/backend-go/internal/controlplane/cutover/assertions.go panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go
git commit -m "test(cutover): add synthetic master embedded harness"
```

### Task 4: Expand cutover coverage to L4, relay, and certificate semantics

**Files:**
- Modify: `panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go`
- Modify: `panel/backend-go/internal/controlplane/cutover/fixture_builder.go`
- Modify: `panel/backend-go/internal/controlplane/cutover/assertions.go`

- [ ] **Step 1: Write the failing L4, relay, and certificate tests**

Add:
- `TestMasterEmbeddedCutoverAppliesL4RuleAndForwardsTCP`
- `TestMasterEmbeddedCutoverAppliesRelayListenerAndTrustChain`
- `TestMasterEmbeddedCutoverExposesManagedCertificateStateAndStableApplyMetadata`

Each test should assert real traffic or real persisted metadata rather than mocked calls.

- [ ] **Step 2: Run the package to verify the new tests fail**

Run: `cd panel/backend-go && go test ./internal/controlplane/cutover -count=1`
Expected: FAIL with missing relay/L4/certificate behavior in the harness or fixture

- [ ] **Step 3: Implement the missing cutover coverage**

Extend the fixture and assertions so the suite validates:
- HTTP frontend proxying
- TCP byte forwarding through the L4 listener
- relay round-trip via `relay.Dial`
- uploaded certificate visibility
- internal CA trust pool resolution
- managed-policy and ACME status field semantics
- stable `desired_revision/current_revision/last_apply_*` metadata with no lingering `last_sync_error`

- [ ] **Step 4: Run focused cutover verification**

Run: `cd panel/backend-go && go test ./internal/controlplane/cutover -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/cutover/fixture_builder.go panel/backend-go/internal/controlplane/cutover/assertions.go panel/backend-go/internal/controlplane/cutover/cutover_integration_test.go
git commit -m "test(cutover): cover l4 relay and certificate semantics"
```

### Task 5: Run repo verification for the migrated fixture path and cutover suite

**Files:**
- Modify only if verification reveals a real defect.

- [ ] **Step 1: Run targeted storage and cutover verification**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/storage -count=1
go test ./internal/controlplane/cutover -count=1
```

Expected: PASS

- [ ] **Step 2: Run the full Go verification**

Run:

```bash
cd panel/backend-go && go test ./... -count=1
cd ../../go-agent && go test ./... -count=1
```

Expected: PASS

- [ ] **Step 3: Run container-level verification**

Run:

```bash
docker compose config
docker build -t nginx-reverse-emby:go-verify --target control-plane-runtime .
```

Expected: PASS

- [ ] **Step 4: Commit any verification-driven fixes**

```bash
git add -A
git commit -m "fix(cutover): keep verification green after gorm fixture migration"
```
