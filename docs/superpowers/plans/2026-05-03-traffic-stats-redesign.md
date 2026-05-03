# Traffic Stats Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist historical traffic stats, compute node quota usage from node policies, expose traffic trends in node detail, support PostgreSQL/MySQL storage, and allow disabling the traffic module with an environment variable.

**Architecture:** Keep agents reporting cumulative raw counters. Generalize the control-plane store to a dialect-aware GORM store, add a focused traffic storage/service module for cursor delta ingestion and quota accounting, then expose node traffic APIs and UI. Agent config receives global traffic reporting/blocking state from the control plane and enforces node-wide blocking for new traffic only.

**Tech Stack:** Go 1.26, GORM with SQLite/PostgreSQL/MySQL dialectors, Vue 3/Vite, TanStack Vue Query, existing go-agent traffic counters.

---

## File Structure Map

Storage and database:

- Modify `panel/backend-go/go.mod`: add `gorm.io/driver/postgres` and `gorm.io/driver/mysql`.
- Modify `panel/backend-go/internal/controlplane/config/config.go`: add `DatabaseDriver`, `DatabaseDSN`, and `TrafficStatsEnabled`.
- Create `panel/backend-go/internal/controlplane/storage/gorm_store.go`: shared GORM store constructor, driver selection, `NewStore`.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: either alias `SQLiteStore` to the shared `GormStore` or keep compatibility wrappers while moving common methods to `GormStore`.
- Modify `panel/backend-go/internal/controlplane/storage/schema.go`: rename schema bootstrap to a generic function and gate traffic migrations by module flag.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add traffic row structs.
- Create `panel/backend-go/internal/controlplane/storage/traffic_store.go`: traffic cursor/bucket/policy/baseline CRUD and dialect-aware increment upserts.
- Create `panel/backend-go/internal/controlplane/storage/traffic_store_test.go`: storage coverage for policy defaults, cursor upserts, bucket increments, and disabled migrations.

Control plane services and APIs:

- Create `panel/backend-go/internal/controlplane/service/traffic_types.go`: policy, summary, trend, calibration, cleanup DTOs and constants.
- Create `panel/backend-go/internal/controlplane/service/traffic_accounting.go`: cycle windows, accounting direction, quota math, calibration math.
- Create `panel/backend-go/internal/controlplane/service/traffic_service.go`: ingestion, summary, trend, policy update, calibration, cleanup.
- Create `panel/backend-go/internal/controlplane/service/traffic_service_test.go`: core service tests.
- Modify `panel/backend-go/internal/controlplane/service/agents.go`: call traffic ingestion from heartbeat, include `traffic_blocked`, disable stats when module is off.
- Modify `panel/backend-go/internal/controlplane/service/system.go`: include `TrafficStatsEnabled` in system info.
- Modify `panel/backend-go/internal/controlplane/http/router.go`: add `TrafficService` dependency and traffic routes.
- Create `panel/backend-go/internal/controlplane/http/handlers_traffic.go`: traffic policy/summary/trend/calibration/cleanup handlers.
- Modify `panel/backend-go/internal/controlplane/http/handlers_public.go`: include traffic block/reporting fields in heartbeat payload.
- Modify `panel/backend-go/internal/controlplane/http/handlers_info.go`: expose `traffic_stats_enabled`.
- Add or update tests in `panel/backend-go/internal/controlplane/http/router_test.go`, `public_test.go`, and new `traffic_handlers_test.go`.

Agent runtime:

- Modify `go-agent/internal/model/types.go`: add `TrafficStatsEnabled`, `TrafficBlocked`, and `TrafficBlockReason` to `AgentConfig`.
- Modify `go-agent/internal/model/snapshot_decode_test.go`: decode new config fields.
- Modify `go-agent/internal/app/app.go`: apply traffic enabled/blocked config and omit stats when disabled.
- Modify `go-agent/internal/proxy/server.go`: return HTTP 429 before proxying when blocked.
- Modify `go-agent/internal/l4/server.go`: reject/close new connections when blocked.
- Modify `go-agent/internal/relay/runtime.go`, `go-agent/internal/relay/tls_tcp_session_pool.go`, and `go-agent/internal/relay/quic_runtime.go`: reject/close new sessions when blocked.
- Add focused tests in `go-agent/internal/app/app_test.go`, `go-agent/internal/proxy/traffic_test.go`, `go-agent/internal/l4/traffic_test.go`, and `go-agent/internal/relay/traffic_test.go`.

Frontend:

- Modify `panel/frontend/src/api/index.js`, `runtime.js`, and `devRuntime.js`: add traffic API exports.
- Modify `panel/frontend/src/api/devMocks/data.js`: add `trafficStatsEnabled`, summary, trends, and policies.
- Create `panel/frontend/src/hooks/useTraffic.js`: traffic policy/summary/trend/calibration/cleanup queries and mutations.
- Expand `panel/frontend/src/utils/trafficStats.js`: accounting helpers for display.
- Modify `panel/frontend/src/pages/AgentDetailPage.vue`: move existing traffic summary into a Traffic tab and add trend/policy/calibration/cleanup UI.
- Modify `panel/frontend/src/pages/RulesPage.vue`, `L4RulesPage.vue`, and `RelayListenersPage.vue`: hide traffic stats when global module is disabled and use accounted values when enabled.
- Add/update tests in `panel/frontend/src/pages/AgentDetailPage.test.js`, `panel/frontend/src/utils/trafficStats.test.mjs`, and API tests.

Docker, docs, and migration:

- Modify `docker-compose.yaml`: add PostgreSQL service and default database env.
- Modify `README.md`: document PG default, MySQL support, SQLite legacy, manual migration, and `NRE_TRAFFIC_STATS_ENABLED`.
- Create or extend CLI support under `panel/backend-go/cmd/nre-control-plane/main.go` for `migrate-storage`.
- Add command tests in `panel/backend-go/cmd/nre-control-plane/main_test.go`.

---

### Task 1: Database Config And Dialect-Aware Store

**Files:**
- Modify: `panel/backend-go/internal/controlplane/config/config.go`
- Create: `panel/backend-go/internal/controlplane/storage/gorm_store.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/go.mod`
- Test: `panel/backend-go/internal/controlplane/config/config_test.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Add failing config tests**

Add tests that assert:

```go
func TestLoadFromEnvParsesDatabaseConfig(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "panel")
	t.Setenv("NRE_REGISTER_TOKEN", "register")
	t.Setenv("NRE_DATABASE_DRIVER", "postgres")
	t.Setenv("NRE_DATABASE_DSN", "postgres://nre:nre@postgres:5432/nre?sslmode=disable")
	t.Setenv("NRE_TRAFFIC_STATS_ENABLED", "false")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.DatabaseDriver != "postgres" {
		t.Fatalf("DatabaseDriver = %q", cfg.DatabaseDriver)
	}
	if cfg.DatabaseDSN != "postgres://nre:nre@postgres:5432/nre?sslmode=disable" {
		t.Fatalf("DatabaseDSN = %q", cfg.DatabaseDSN)
	}
	if cfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}

func TestLoadFromEnvRejectsInvalidDatabaseDriver(t *testing.T) {
	t.Setenv("NRE_PANEL_TOKEN", "panel")
	t.Setenv("NRE_REGISTER_TOKEN", "register")
	t.Setenv("NRE_DATABASE_DRIVER", "oracle")

	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "NRE_DATABASE_DRIVER") {
		t.Fatalf("LoadFromEnv() error = %v, want NRE_DATABASE_DRIVER error", err)
	}
}
```

- [ ] **Step 2: Run the config tests and verify failure**

Run:

```sh
cd panel/backend-go && go test ./internal/controlplane/config -run 'TestLoadFromEnv(Parse|RejectsInvalidDatabaseDriver)' -count=1
```

Expected: compile failure because `DatabaseDriver`, `DatabaseDSN`, and `TrafficStatsEnabled` do not exist.

- [ ] **Step 3: Implement config fields**

Add to `Config`:

```go
DatabaseDriver     string
DatabaseDSN        string
TrafficStatsEnabled bool
```

Set defaults:

```go
const defaultDatabaseDriver = "sqlite"
```

In `Default()`:

```go
DatabaseDriver: defaultDatabaseDriver,
TrafficStatsEnabled: true,
```

In `LoadFromEnv()` parse:

```go
if val := strings.TrimSpace(os.Getenv("NRE_DATABASE_DRIVER")); val != "" {
	driver := strings.ToLower(val)
	switch driver {
	case "sqlite", "postgres", "mysql":
		cfg.DatabaseDriver = driver
	default:
		return Config{}, fmt.Errorf("invalid NRE_DATABASE_DRIVER: %q", val)
	}
}
if val := strings.TrimSpace(os.Getenv("NRE_DATABASE_DSN")); val != "" {
	cfg.DatabaseDSN = val
}
if val := strings.TrimSpace(os.Getenv("NRE_TRAFFIC_STATS_ENABLED")); val != "" {
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return Config{}, fmt.Errorf("invalid NRE_TRAFFIC_STATS_ENABLED: %w", err)
	}
	cfg.TrafficStatsEnabled = enabled
	cfg.LocalAgentTrafficStatsEnabled = enabled
	cfg.LocalAgentTrafficStatsExplicit = true
}
```

- [ ] **Step 4: Add GORM dialectors**

Run:

```sh
cd panel/backend-go && go get gorm.io/driver/postgres gorm.io/driver/mysql
```

Expected: `go.mod` and `go.sum` include both drivers.

- [ ] **Step 5: Create shared store constructor**

Create `panel/backend-go/internal/controlplane/storage/gorm_store.go` with:

```go
package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type GormStore struct {
	db           *gorm.DB
	dataRoot     string
	localAgentID string
	driver       string
}

type StoreConfig struct {
	Driver              string
	DSN                 string
	DataRoot            string
	LocalAgentID        string
	TrafficStatsEnabled bool
}

func NewStore(cfg StoreConfig) (*GormStore, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver == "sqlite" {
		if err := os.MkdirAll(cfg.DataRoot, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := gorm.Open(resolveDialector(driver, cfg), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	store := &GormStore{
		db:           db,
		dataRoot:     cfg.DataRoot,
		localAgentID: cfg.LocalAgentID,
		driver:       driver,
	}
	if err := BootstrapSchema(context.Background(), db, SchemaOptions{TrafficStatsEnabled: cfg.TrafficStatsEnabled}); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func resolveDialector(driver string, cfg StoreConfig) gorm.Dialector {
	switch driver {
	case "postgres":
		return postgres.Open(cfg.DSN)
	case "mysql":
		return mysql.Open(cfg.DSN)
	case "sqlite":
		dsn := strings.TrimSpace(cfg.DSN)
		if dsn == "" {
			dsn = filepath.Join(cfg.DataRoot, "panel.db") + "?_journal_mode=WAL&_busy_timeout=5000"
		}
		return sqlite.Open(dsn)
	default:
		panic(fmt.Sprintf("unsupported database driver %q", driver))
	}
}

func (s *GormStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
```

- [ ] **Step 6: Preserve SQLite compatibility wrapper**

Change `SQLiteStore` to alias `GormStore`:

```go
type SQLiteStore = GormStore

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	return NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            dataRoot,
		LocalAgentID:        localAgentID,
		TrafficStatsEnabled: true,
	})
}
```

Move existing methods from `*SQLiteStore` to `*GormStore` by updating receivers.

- [ ] **Step 7: Generalize schema bootstrap**

Rename `BootstrapSQLiteSchema` to:

```go
type SchemaOptions struct {
	TrafficStatsEnabled bool
}

func BootstrapSchema(ctx context.Context, db *gorm.DB, options SchemaOptions) error
```

Keep `BootstrapSQLiteSchema` as a wrapper for existing tests:

```go
func BootstrapSQLiteSchema(ctx context.Context, db *gorm.DB) error {
	return BootstrapSchema(ctx, db, SchemaOptions{TrafficStatsEnabled: true})
}
```

- [ ] **Step 8: Run focused tests**

Run:

```sh
cd panel/backend-go && go test ./internal/controlplane/config ./internal/controlplane/storage -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```sh
git add panel/backend-go/go.mod panel/backend-go/go.sum panel/backend-go/internal/controlplane/config panel/backend-go/internal/controlplane/storage
git commit -m "feat(backend): add database driver configuration"
```

### Task 2: Traffic Schema And Store Methods

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Create: `panel/backend-go/internal/controlplane/storage/traffic_store.go`
- Test: `panel/backend-go/internal/controlplane/storage/traffic_store_test.go`

- [ ] **Step 1: Write failing storage tests**

Create tests for:

```go
func TestTrafficSchemaDisabledSkipsTrafficTables(t *testing.T) {
	db := openTestGormDB(t)
	if err := BootstrapSchema(context.Background(), db, SchemaOptions{TrafficStatsEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&AgentTrafficPolicyRow{}) {
		t.Fatal("traffic policy table exists while module disabled")
	}
}

func TestTrafficPolicyDefaults(t *testing.T) {
	store := newTrafficTestStore(t, true)
	policy, err := store.GetTrafficPolicy(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Direction != "both" || policy.CycleStartDay != 1 || policy.HourlyRetentionDays != 180 || policy.DailyRetentionMonths != 24 {
		t.Fatalf("policy defaults = %+v", policy)
	}
}

func TestIncrementTrafficBucketsAccumulates(t *testing.T) {
	store := newTrafficTestStore(t, true)
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	delta := TrafficDelta{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: bucket, RXBytes: 100, TXBytes: 200}
	if err := store.IncrementTrafficBuckets(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTrafficBuckets(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTrafficTrend(context.Background(), TrafficTrendQuery{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", Granularity: "hour", From: bucket, To: bucket.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 200 || rows[0].TXBytes != 400 {
		t.Fatalf("rows = %+v", rows)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/backend-go && go test ./internal/controlplane/storage -run 'TestTraffic' -count=1
```

Expected: compile failure for missing traffic row types and methods.

- [ ] **Step 3: Add traffic row models**

Add row structs with explicit table names:

```go
type AgentTrafficPolicyRow struct {
	AgentID                string `gorm:"column:agent_id;primaryKey"`
	Direction              string `gorm:"column:direction;not null;default:'both'"`
	CycleStartDay          int    `gorm:"column:cycle_start_day;not null;default:1"`
	MonthlyQuotaBytes      *int64 `gorm:"column:monthly_quota_bytes"`
	BlockWhenExceeded      bool   `gorm:"column:block_when_exceeded;not null;default:false"`
	HourlyRetentionDays    int    `gorm:"column:hourly_retention_days;not null;default:180"`
	DailyRetentionMonths   int    `gorm:"column:daily_retention_months;not null;default:24"`
	MonthlyRetentionMonths *int   `gorm:"column:monthly_retention_months"`
	UpdatedAt              string `gorm:"column:updated_at"`
	CreatedAt              string `gorm:"column:created_at"`
}

type AgentTrafficRawCursorRow struct {
	AgentID    string `gorm:"column:agent_id;primaryKey"`
	ScopeType  string `gorm:"column:scope_type;primaryKey"`
	ScopeID    string `gorm:"column:scope_id;primaryKey"`
	RXBytes    uint64 `gorm:"column:rx_bytes"`
	TXBytes    uint64 `gorm:"column:tx_bytes"`
	ObservedAt string `gorm:"column:observed_at"`
}
```

Add baseline, hourly, daily, monthly, and event rows using the spec columns.

- [ ] **Step 4: Gate traffic migrations**

In `BootstrapSchema`, include traffic rows in `AutoMigrate` only when `options.TrafficStatsEnabled` is true.

- [ ] **Step 5: Implement traffic store methods**

In `traffic_store.go`, implement:

```go
func (s *GormStore) GetTrafficPolicy(ctx context.Context, agentID string) (AgentTrafficPolicyRow, error)
func (s *GormStore) SaveTrafficPolicy(ctx context.Context, row AgentTrafficPolicyRow) error
func (s *GormStore) GetTrafficCursor(ctx context.Context, agentID, scopeType, scopeID string) (AgentTrafficRawCursorRow, bool, error)
func (s *GormStore) SaveTrafficCursor(ctx context.Context, row AgentTrafficRawCursorRow) error
func (s *GormStore) IncrementTrafficBuckets(ctx context.Context, delta TrafficDelta) error
func (s *GormStore) ListTrafficTrend(ctx context.Context, query TrafficTrendQuery) ([]TrafficBucketRow, error)
func (s *GormStore) DeleteTrafficBefore(ctx context.Context, agentID string, cutoff TrafficCleanupCutoff) (int64, error)
```

Use `gorm.io/gorm/clause.OnConflict` with `gorm.Expr("rx_bytes + ?", delta.RXBytes)` and `gorm.Expr("tx_bytes + ?", delta.TXBytes)` for increment upserts.

- [ ] **Step 6: Run storage tests**

```sh
cd panel/backend-go && go test ./internal/controlplane/storage -run 'TestTraffic' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add panel/backend-go/internal/controlplane/storage
git commit -m "feat(backend): add traffic storage tables"
```

### Task 3: Traffic Accounting And Service

**Files:**
- Create: `panel/backend-go/internal/controlplane/service/traffic_types.go`
- Create: `panel/backend-go/internal/controlplane/service/traffic_accounting.go`
- Create: `panel/backend-go/internal/controlplane/service/traffic_service.go`
- Test: `panel/backend-go/internal/controlplane/service/traffic_service_test.go`

- [ ] **Step 1: Write accounting tests**

Cover:

```go
func TestAccountedBytesByDirection(t *testing.T) {
	tests := []struct {
		direction string
		rx, tx    uint64
		want      uint64
	}{
		{"rx", 10, 20, 10},
		{"tx", 10, 20, 20},
		{"both", 10, 20, 30},
		{"max", 10, 20, 20},
		{"", 10, 20, 30},
	}
	for _, tc := range tests {
		if got := accountedBytes(tc.direction, tc.rx, tc.tx); got != tc.want {
			t.Fatalf("%s got %d want %d", tc.direction, got, tc.want)
		}
	}
}

func TestMonthlyCycleWindow(t *testing.T) {
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local)
	start, end := monthlyCycleWindow(now, 15)
	if !start.Equal(time.Date(2026, 4, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("start = %s", start)
	}
	if !end.Equal(time.Date(2026, 5, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("end = %s", end)
	}
}
```

- [ ] **Step 2: Write ingestion tests**

Add tests where a fake store records cursor/bucket writes:

```go
func TestTrafficServiceIngestHeartbeatComputesDeltas(t *testing.T) {
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: fixedNow}, fakeStore)
	stats := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100), "tx_bytes": float64(50)}}}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	if fakeStore.hourlyRX("edge-1", "agent_total", "") != 100 {
		t.Fatalf("double counted heartbeat")
	}
}

func TestTrafficServiceDisabledIgnoresHeartbeat(t *testing.T) {
	svc := NewTrafficService(TrafficServiceConfig{Enabled: false, Now: fixedNow}, fakeStore)
	err := svc.IngestHeartbeat(context.Background(), "edge-1", AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100)}}})
	if err != nil {
		t.Fatal(err)
	}
	if fakeStore.writeCount != 0 {
		t.Fatalf("writeCount = %d", fakeStore.writeCount)
	}
}
```

- [ ] **Step 3: Run service tests and verify failure**

```sh
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestTraffic|TestAccounted|TestMonthly' -count=1
```

Expected: compile failure for missing traffic service.

- [ ] **Step 4: Implement traffic DTOs and constants**

Define:

```go
const ErrCodeTrafficStatsDisabled = "TRAFFIC_STATS_DISABLED"

type TrafficPolicy struct {
	AgentID                string `json:"agent_id"`
	Direction              string `json:"direction"`
	CycleStartDay          int    `json:"cycle_start_day"`
	MonthlyQuotaBytes      *int64 `json:"monthly_quota_bytes"`
	BlockWhenExceeded      bool   `json:"block_when_exceeded"`
	HourlyRetentionDays    int    `json:"hourly_retention_days"`
	DailyRetentionMonths   int    `json:"daily_retention_months"`
	MonthlyRetentionMonths *int   `json:"monthly_retention_months"`
}
```

Add `TrafficSummary`, `TrafficTrendPoint`, `TrafficCalibrationRequest`, and `TrafficCleanupResult`.

- [ ] **Step 5: Implement accounting helpers**

Implement `accountedBytes`, `normalizeTrafficDirection`, `normalizeCycleStartDay`, `monthlyCycleWindow`, and quota status helpers. Default direction must be `both`; cycle start day must clamp/reject outside `1..28` in service validation.

- [ ] **Step 6: Implement traffic service**

Implement `NewTrafficService`, disabled guard returning a typed service error, heartbeat stats parsing for current stats shape, delta calculation, cursor reset handling, bucket writes, summary calculation, trend loading, policy update, calibration, and cleanup.

- [ ] **Step 7: Run service tests**

```sh
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestTraffic|TestAccounted|TestMonthly' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```sh
git add panel/backend-go/internal/controlplane/service/traffic_*.go panel/backend-go/internal/controlplane/service/traffic_service_test.go
git commit -m "feat(backend): add traffic accounting service"
```

### Task 4: Heartbeat Integration And Agent Config

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_public.go`
- Modify: `go-agent/internal/model/types.go`
- Modify: `go-agent/internal/app/app.go`
- Test: `panel/backend-go/internal/controlplane/service/agents_test.go`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`
- Test: `go-agent/internal/model/snapshot_decode_test.go`
- Test: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write backend heartbeat tests**

Assert:

```go
func TestHeartbeatIngestsTrafficWhenModuleEnabled(t *testing.T)
func TestHeartbeatIgnoresTrafficAndDisablesAgentReportingWhenModuleDisabled(t *testing.T)
func TestHeartbeatReplyIncludesTrafficBlockedState(t *testing.T)
```

The disabled test should assert latest stats are not updated and heartbeat sync payload includes `agent_config.traffic_stats_enabled=false` and `traffic_blocked=false`.

- [ ] **Step 2: Write agent config decode tests**

Add JSON decode test:

```go
func TestSnapshotDecodePreservesTrafficBlockingConfig(t *testing.T) {
	var snapshot Snapshot
	err := json.Unmarshal([]byte(`{"agent_config":{"traffic_stats_enabled":false,"traffic_blocked":true,"traffic_block_reason":"monthly quota exceeded"}}`), &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.AgentConfig.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
	if !snapshot.AgentConfig.TrafficBlocked {
		t.Fatal("TrafficBlocked = false, want true")
	}
	if snapshot.AgentConfig.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("TrafficBlockReason = %q", snapshot.AgentConfig.TrafficBlockReason)
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

```sh
cd panel/backend-go && go test ./internal/controlplane/service ./internal/controlplane/http -run 'TestHeartbeat.*Traffic|Test.*TrafficBlocked' -count=1
cd go-agent && go test ./internal/model ./internal/app -run 'Test.*Traffic' -count=1
```

Expected: missing fields/integration failures.

- [ ] **Step 4: Add runtime config fields**

Backend `AgentRuntimeConfig` and go-agent `model.AgentConfig` get:

```go
TrafficStatsEnabled *bool  `json:"traffic_stats_enabled,omitempty"`
TrafficBlocked      bool   `json:"traffic_blocked,omitempty"`
TrafficBlockReason  string `json:"traffic_block_reason,omitempty"`
```

Use pointer for `TrafficStatsEnabled` so older snapshots without the field preserve defaults.

- [ ] **Step 5: Wire heartbeat ingestion**

In `agentService.Heartbeat`:

- If module enabled and `request.Stats != nil`, call `trafficSvc.IngestHeartbeat(ctx, row.ID, request.Stats)`.
- If module disabled, do not update `row.LastReportedStatsJSON`.
- If enabled, preserve existing latest stats behavior for `request.Stats != nil`.
- Compute block state through traffic service summary/policy.
- Populate `HeartbeatReply.TrafficStatsEnabled` and `HeartbeatReply.TrafficBlocked`.

- [ ] **Step 6: Apply agent config**

In go-agent app activation, call `traffic.SetEnabled(false)` when `TrafficStatsEnabled` is explicitly false; otherwise preserve default enabled behavior. Store `TrafficBlocked` state in app/runtime config where HTTP/L4/Relay can read it.

- [ ] **Step 7: Run focused tests**

```sh
cd panel/backend-go && go test ./internal/controlplane/service ./internal/controlplane/http -run 'TestHeartbeat.*Traffic|Test.*TrafficBlocked' -count=1
cd go-agent && go test ./internal/model ./internal/app -run 'Test.*Traffic' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```sh
git add panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/http/handlers_public.go go-agent/internal/model/types.go go-agent/internal/app/app.go panel/backend-go/internal/controlplane/service/*test.go panel/backend-go/internal/controlplane/http/*test.go go-agent/internal/model/*test.go go-agent/internal/app/*test.go
git commit -m "feat(traffic): sync traffic module state to agents"
```

### Task 5: Traffic HTTP API

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_traffic.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_info.go`
- Modify: `panel/backend-go/internal/controlplane/service/system.go`
- Test: `panel/backend-go/internal/controlplane/http/traffic_handlers_test.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write handler tests**

Cover:

```go
func TestTrafficPolicyRoutesRequirePanelToken(t *testing.T)
func TestTrafficPolicyDisabledReturnsStable404(t *testing.T)
func TestTrafficTrendReturnsPoints(t *testing.T)
func TestSystemInfoExposesTrafficStatsEnabled(t *testing.T)
```

Disabled response body should include:

```json
{"error":"traffic stats disabled","code":"TRAFFIC_STATS_DISABLED"}
```

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/backend-go && go test ./internal/controlplane/http -run 'TestTraffic|TestSystemInfoExposesTrafficStatsEnabled' -count=1
```

Expected: missing routes/fields.

- [ ] **Step 3: Add traffic service dependency**

Add `TrafficService` interface to `router.go` with `Policy`, `UpdatePolicy`, `Summary`, `Trend`, `Calibrate`, and `Cleanup`.

- [ ] **Step 4: Add routes**

Register:

```go
mux.Handle(prefix+"/agents/{agentID}/traffic-policy", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficPolicy)))
mux.Handle(prefix+"/agents/{agentID}/traffic-summary", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficSummary)))
mux.Handle(prefix+"/agents/{agentID}/traffic-trend", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficTrend)))
mux.Handle(prefix+"/agents/{agentID}/traffic-calibration", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficCalibration)))
mux.Handle(prefix+"/agents/{agentID}/traffic-cleanup", resolved.requirePanelToken(http.HandlerFunc(resolved.handleAgentTrafficCleanup)))
```

- [ ] **Step 5: Implement handlers**

Use method checks:

- `GET/PATCH traffic-policy`
- `GET traffic-summary`
- `GET traffic-trend`
- `POST traffic-calibration`
- `POST traffic-cleanup`

Map disabled traffic service error to `404` and code `TRAFFIC_STATS_DISABLED`.

- [ ] **Step 6: Expose module flag in info**

Add `TrafficStatsEnabled bool json:"traffic_stats_enabled"` to system info output.

- [ ] **Step 7: Run handler tests**

```sh
cd panel/backend-go && go test ./internal/controlplane/http -run 'TestTraffic|TestSystemInfoExposesTrafficStatsEnabled' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```sh
git add panel/backend-go/internal/controlplane/http panel/backend-go/internal/controlplane/service/system.go
git commit -m "feat(panel): expose traffic APIs"
```

### Task 6: Agent Node-Wide Blocking

**Files:**
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: `go-agent/internal/proxy/traffic_test.go`
- Test: `go-agent/internal/l4/traffic_test.go`
- Test: `go-agent/internal/relay/traffic_test.go`

- [ ] **Step 1: Write blocking tests**

Add tests:

```go
func TestHTTPReturns429WhenTrafficBlocked(t *testing.T)
func TestL4RejectsNewConnectionWhenTrafficBlocked(t *testing.T)
func TestRelayRejectsNewSessionWhenTrafficBlocked(t *testing.T)
```

Each test should configure active snapshot with `AgentConfig{TrafficBlocked: true, TrafficBlockReason: "monthly quota exceeded"}`.

- [ ] **Step 2: Run tests and verify failure**

```sh
cd go-agent && go test ./internal/proxy ./internal/l4 ./internal/relay -run 'Test.*TrafficBlocked|Test.*Blocked' -count=1
```

Expected: connections are not blocked yet.

- [ ] **Step 3: Add runtime block accessor**

Expose a small helper in app/runtime layer, or pass a callback into proxy/L4/Relay servers:

```go
type TrafficBlockState struct {
	Blocked bool
	Reason  string
}
```

Keep the check read-only and cheap for hot paths.

- [ ] **Step 4: Enforce HTTP block**

At the start of the HTTP request handler, before upstream selection or body reads:

```go
if state := s.trafficBlockState(); state.Blocked {
	http.Error(w, defaultString(state.Reason, "traffic quota exceeded"), http.StatusTooManyRequests)
	return
}
```

- [ ] **Step 5: Enforce L4/Relay block**

After accepting a new connection/session and before starting proxy copy, close it immediately if blocked. Existing sessions should not be interrupted by later state changes.

- [ ] **Step 6: Run focused tests**

```sh
cd go-agent && go test ./internal/proxy ./internal/l4 ./internal/relay -run 'Test.*TrafficBlocked|Test.*Blocked' -count=1
```

Expected: PASS except any known local bind limitation; if relay bind fails only on `127.0.0.2`, run the specific new relay test.

- [ ] **Step 7: Commit**

```sh
git add go-agent/internal/app go-agent/internal/proxy go-agent/internal/l4 go-agent/internal/relay
git commit -m "feat(agent): block new traffic when over quota"
```

### Task 7: Frontend Traffic APIs And Utilities

**Files:**
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devRuntime.js`
- Create: `panel/frontend/src/hooks/useTraffic.js`
- Modify: `panel/frontend/src/utils/trafficStats.js`
- Test: `panel/frontend/src/utils/trafficStats.test.mjs`
- Test: `panel/frontend/src/api/index.test.mjs`

- [ ] **Step 1: Write utility tests**

Cover:

```js
import { describe, expect, it } from 'vitest'
import { accountedBytes, normalizeTrafficPolicy } from './trafficStats.js'

describe('traffic accounting helpers', () => {
  it('accounts by direction', () => {
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'rx')).toBe(10)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'tx')).toBe(20)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'both')).toBe(30)
    expect(accountedBytes({ rx_bytes: 10, tx_bytes: 20 }, 'max')).toBe(20)
  })

  it('normalizes policy defaults', () => {
    expect(normalizeTrafficPolicy({}).direction).toBe('both')
    expect(normalizeTrafficPolicy({}).cycle_start_day).toBe(1)
  })
})
```

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/frontend && npm run test -- trafficStats
```

Expected: missing helper exports.

- [ ] **Step 3: Implement API functions**

Add exports:

```js
export const fetchTrafficPolicy = (...args) => call('fetchTrafficPolicy', ...args)
export const updateTrafficPolicy = (...args) => call('updateTrafficPolicy', ...args)
export const fetchTrafficSummary = (...args) => call('fetchTrafficSummary', ...args)
export const fetchTrafficTrend = (...args) => call('fetchTrafficTrend', ...args)
export const calibrateTraffic = (...args) => call('calibrateTraffic', ...args)
export const cleanupTraffic = (...args) => call('cleanupTraffic', ...args)
```

Implement matching runtime calls to the backend routes.

- [ ] **Step 4: Implement hooks**

Create `useTraffic.js` with queries/mutations and invalidation for:

- `['traffic-policy', agentId]`
- `['traffic-summary', agentId]`
- `['traffic-trend', agentId, granularity, range, scope]`

- [ ] **Step 5: Implement utility helpers**

Add `accountedBytes`, `normalizeTrafficPolicy`, `formatQuota`, and trend point normalization.

- [ ] **Step 6: Run frontend utility/API tests**

```sh
cd panel/frontend && npm run test -- trafficStats api
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add panel/frontend/src/api panel/frontend/src/hooks/useTraffic.js panel/frontend/src/utils/trafficStats.js panel/frontend/src/utils/trafficStats.test.mjs
git commit -m "feat(panel): add traffic api hooks"
```

### Task 8: Node Detail Traffic Tab

**Files:**
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
- Modify: `panel/frontend/src/pages/AgentDetailPage.test.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`

- [ ] **Step 1: Write page tests**

Cover:

```js
it('renders traffic tab when traffic stats are enabled', async () => {
  // mount AgentDetailPage with system info traffic_stats_enabled=true
  // expect screen text: 流量统计, 趋势, 月额度, 校准, 清理
})

it('hides traffic tab when traffic stats are disabled', async () => {
  // mount AgentDetailPage with system info traffic_stats_enabled=false
  // expect no 流量统计 tab and no quota controls
})
```

- [ ] **Step 2: Run page tests and verify failure**

```sh
cd panel/frontend && npm run test -- AgentDetailPage
```

Expected: traffic tab/policy controls missing.

- [ ] **Step 3: Add dev mock data**

Add mock policy, summary, daily trend, and disabled flag variants to `devMocks/data.js`.

- [ ] **Step 4: Build Traffic tab**

In `AgentDetailPage.vue`:

- Replace top-level old traffic summary with tab content.
- Add `traffic` tab only when `systemInfo.traffic_stats_enabled !== false`.
- Show summary cards for used/quota/remaining/cycle.
- Render simple chart using CSS bars or existing chart dependency if present; do not add a new heavy chart library unless one already exists.
- Add policy form fields for direction, cycle start day, monthly quota, block switch, and retention.
- Add calibration actions.
- Add cleanup action.

- [ ] **Step 5: Run page tests**

```sh
cd panel/frontend && npm run test -- AgentDetailPage
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add panel/frontend/src/pages/AgentDetailPage.vue panel/frontend/src/pages/AgentDetailPage.test.js panel/frontend/src/api/devMocks/data.js
git commit -m "feat(panel): add node traffic tab"
```

### Task 9: Rule/List/Relay Breakdown Uses Policy

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
- Modify: `panel/frontend/src/components/rules/RuleCard.vue`
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
- Modify: `panel/frontend/src/components/relay/RelayCard.vue`
- Test: related existing page/component tests

- [ ] **Step 1: Write display tests**

Assert rule cards render accounted traffic from policy direction and hide stats when disabled.

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/frontend && npm run test -- RulesPage L4RulesPage RelayListenersPage
```

Expected: current display uses raw/latest helpers only or lacks disabled gating.

- [ ] **Step 3: Use traffic summary breakdowns**

Fetch `traffic-summary` for the selected agent and pass per-scope accounted usage into HTTP/L4/Relay lists. Keep raw rx/tx available as secondary detail only.

- [ ] **Step 4: Hide traffic fields when disabled**

Use `systemInfo.traffic_stats_enabled === false` to omit traffic columns/cards.

- [ ] **Step 5: Run focused frontend tests**

```sh
cd panel/frontend && npm run test -- RulesPage L4RulesPage RelayListenersPage
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add panel/frontend/src/pages panel/frontend/src/components
git commit -m "feat(panel): apply traffic policy to rule usage"
```

### Task 10: Docker PostgreSQL Default And Migration Command

**Files:**
- Modify: `docker-compose.yaml`
- Modify: `panel/backend-go/cmd/nre-control-plane/main.go`
- Modify: `panel/backend-go/cmd/nre-control-plane/main_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write command tests**

Add command parsing tests for:

```go
func TestMigrateStorageCommandRequiresSourceAndTarget(t *testing.T)
func TestMigrateStorageCommandDoesNotRunOnNormalStartup(t *testing.T)
```

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/backend-go && go test ./cmd/nre-control-plane -run 'TestMigrateStorage' -count=1
```

Expected: missing migrate command.

- [ ] **Step 3: Add PostgreSQL compose service**

Update `docker-compose.yaml`:

```yaml
services:
  postgres:
    image: postgres:17-alpine
    container_name: nginx-reverse-emby-postgres
    restart: unless-stopped
    environment:
      POSTGRES_DB: nre
      POSTGRES_USER: nre
      POSTGRES_PASSWORD: nre
    volumes:
      - ./data/postgres:/var/lib/postgresql/data

  nginx-reverse-emby:
    depends_on:
      - postgres
    environment:
      NRE_DATABASE_DRIVER: postgres
      NRE_DATABASE_DSN: postgres://nre:nre@postgres:5432/nre?sslmode=disable
```

Remove `network_mode: host` from the app service when adding the PostgreSQL service, publish the panel port with `ports: ["8080:8080"]`, and use the Compose service name `postgres` in the DSN. Keep any legacy host-networking notes in README as an advanced deployment variant that requires changing the DSN to a reachable host/port.

- [ ] **Step 4: Add migration command**

Add command mode:

```sh
nre-control-plane migrate-storage --from-driver sqlite --from-dsn ./data/panel.db --to-driver postgres --to-dsn postgres://...
```

Implementation opens the source store normally using its source driver/DSN, opens the target store with migrations enabled, copies core rows plus traffic policy/baseline rows, and skips high-volume traffic history by default. The command refuses to run when source and target driver/DSN are identical.

- [ ] **Step 5: Update README**

Document:

- Docker now defaults to PostgreSQL.
- MySQL DSN example.
- SQLite legacy/dev mode.
- `NRE_TRAFFIC_STATS_ENABLED=false`.
- Manual migration command.
- Traffic history is not included in normal backup by default.

- [ ] **Step 6: Run command tests**

```sh
cd panel/backend-go && go test ./cmd/nre-control-plane -run 'TestMigrateStorage' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add docker-compose.yaml README.md panel/backend-go/cmd/nre-control-plane
git commit -m "feat(deploy): default docker storage to postgres"
```

### Task 11: Backup Compatibility

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/backup.go`
- Modify: `panel/backend-go/internal/controlplane/service/backup_types.go`
- Test: `panel/backend-go/internal/controlplane/service/backup_test.go`

- [ ] **Step 1: Write backup tests**

Assert backup includes traffic policies and baselines but excludes hourly/daily/monthly history by default.

- [ ] **Step 2: Run tests and verify failure**

```sh
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestBackup.*Traffic' -count=1
```

Expected: traffic policy/baseline missing.

- [ ] **Step 3: Extend backup DTOs**

Add traffic policy and baseline arrays to backup payload. Do not add bucket history to default export.

- [ ] **Step 4: Import policy/baseline**

On import, restore policies and baselines after agents are restored.

- [ ] **Step 5: Run backup tests**

```sh
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestBackup.*Traffic' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add panel/backend-go/internal/controlplane/service/backup*
git commit -m "feat(backend): preserve traffic quota settings in backups"
```

### Task 12: Full Verification

**Files:** no planned source edits unless verification exposes defects.

- [ ] **Step 1: Run backend tests**

```sh
cd panel/backend-go && go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run agent tests**

```sh
cd go-agent && go test ./...
```

Expected: PASS, except the known local macOS relay bind limitation in `go-agent/internal/relay TestStartBindsAllConfiguredHosts`. If that environment failure appears, run the new targeted traffic/blocking relay tests and document the limitation.

- [ ] **Step 3: Run frontend build**

```sh
cd panel/frontend && npm run build
```

Expected: PASS.

- [ ] **Step 4: Run container build**

```sh
docker build -t nginx-reverse-emby .
```

Expected: PASS.

- [ ] **Step 5: Final status**

Summarize commits, verification commands, any environment-specific failures, and manual checks for Docker PostgreSQL startup.
