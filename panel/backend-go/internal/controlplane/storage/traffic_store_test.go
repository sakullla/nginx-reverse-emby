package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestTrafficSchemaDisabledSkipsTrafficTables(t *testing.T) {
	db := openTrafficTestGormDB(t)

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

func TestTrafficPolicySaveAndReload(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	quota := int64(1024)

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:           "edge-1",
		Direction:         "rx",
		CycleStartDay:     15,
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: true,
	}); err != nil {
		t.Fatal(err)
	}

	policy, err := store.GetTrafficPolicy(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Direction != "rx" || policy.CycleStartDay != 15 || policy.MonthlyQuotaBytes == nil || *policy.MonthlyQuotaBytes != quota || !policy.BlockWhenExceeded {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestTrafficPolicyUpsertPreservesCreatedAt(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:   "edge-1",
		Direction: "rx",
		CreatedAt: "2026-05-03T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	first, err := store.GetTrafficPolicy(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:   "edge-1",
		Direction: "tx",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetTrafficPolicy(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAt != first.CreatedAt {
		t.Fatalf("CreatedAt = %q, want %q", updated.CreatedAt, first.CreatedAt)
	}
	if updated.Direction != "tx" {
		t.Fatalf("Direction = %q, want tx", updated.Direction)
	}
}

func TestTrafficPolicyEmptyAgentIDUsesLocalAgent(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{Direction: "max"}); err != nil {
		t.Fatal(err)
	}

	policy, err := store.GetTrafficPolicy(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if policy.AgentID != "local" || policy.Direction != "max" {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestTrafficBaselineUpsert(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	cycleStart := "2026-05-01T00:00:00Z"

	_, found, err := store.GetTrafficBaseline(ctx, "edge-1", cycleStart)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("baseline found before save")
	}

	first := AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        cycleStart,
		RawRXBytes:        100,
		RawTXBytes:        200,
		RawAccountedBytes: 300,
	}
	if err := store.SaveTrafficBaseline(ctx, first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.AdjustUsedBytes = -50
	if err := store.SaveTrafficBaseline(ctx, updated); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.GetTrafficBaseline(ctx, "edge-1", cycleStart)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got.RawRXBytes != 100 || got.RawTXBytes != 200 || got.RawAccountedBytes != 300 || got.AdjustUsedBytes != -50 {
		t.Fatalf("baseline = %+v, found=%v", got, found)
	}
}

func TestTrafficBaselineUpsertPreservesCreatedAt(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	cycleStart := "2026-05-01T00:00:00Z"

	if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		AgentID:    "edge-1",
		CycleStart: cycleStart,
		RawRXBytes: 100,
		CreatedAt:  "2026-05-03T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	first, found, err := store.GetTrafficBaseline(ctx, "edge-1", cycleStart)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("baseline not found after save")
	}

	if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		AgentID:    "edge-1",
		CycleStart: cycleStart,
		RawRXBytes: 200,
	}); err != nil {
		t.Fatal(err)
	}
	updated, found, err := store.GetTrafficBaseline(ctx, "edge-1", cycleStart)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("baseline not found after update")
	}
	if updated.CreatedAt != first.CreatedAt {
		t.Fatalf("CreatedAt = %q, want %q", updated.CreatedAt, first.CreatedAt)
	}
	if updated.RawRXBytes != 200 {
		t.Fatalf("RawRXBytes = %d, want 200", updated.RawRXBytes)
	}
}

func TestTrafficBaselineEmptyAgentIDUsesLocalAgent(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	cycleStart := "2026-05-01T00:00:00Z"

	if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		CycleStart:        cycleStart,
		RawAccountedBytes: 100,
	}); err != nil {
		t.Fatal(err)
	}

	baseline, found, err := store.GetTrafficBaseline(ctx, "", cycleStart)
	if err != nil {
		t.Fatal(err)
	}
	if !found || baseline.AgentID != "local" || baseline.RawAccountedBytes != 100 {
		t.Fatalf("baseline = %+v, found=%v", baseline, found)
	}
}

func TestTrafficCursorUpsert(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	_, found, err := store.GetTrafficCursor(ctx, "edge-1", "agent_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("cursor found before save")
	}

	first := AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: "2026-05-03T08:00:00Z",
	}
	if err := store.SaveTrafficCursor(ctx, first); err != nil {
		t.Fatal(err)
	}
	updated := first
	updated.RXBytes = 150
	updated.TXBytes = 275
	updated.ObservedAt = "2026-05-03T08:01:00Z"
	if err := store.SaveTrafficCursor(ctx, updated); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.GetTrafficCursor(ctx, "edge-1", "agent_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || got.RXBytes != 150 || got.TXBytes != 275 || got.ObservedAt != "2026-05-03T08:01:00Z" {
		t.Fatalf("cursor = %+v, found=%v", got, found)
	}
}

func TestTrafficCursorValidation(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{AgentID: "edge-1"}); err == nil || !strings.Contains(err.Error(), "scope_type") {
		t.Fatalf("SaveTrafficCursor() error = %v, want scope_type error", err)
	}
	if _, _, err := store.GetTrafficCursor(ctx, "edge-1", "", ""); err == nil || !strings.Contains(err.Error(), "scope_type") {
		t.Fatalf("GetTrafficCursor() error = %v, want scope_type error", err)
	}
}

func TestTrafficCursorEmptyAgentIDUsesLocalAgentAndAllowsAggregateScopeID(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
		ScopeType: "agent_total",
		RXBytes:   100,
	}); err != nil {
		t.Fatal(err)
	}

	cursor, found, err := store.GetTrafficCursor(ctx, "", "agent_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || cursor.AgentID != "local" || cursor.ScopeID != "" || cursor.RXBytes != 100 {
		t.Fatalf("cursor = %+v, found=%v", cursor, found)
	}
}

func TestIncrementTrafficBucketsAccumulates(t *testing.T) {
	store := newTrafficTestStore(t, true)
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	delta := TrafficDelta{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     200,
	}

	if err := store.IncrementTrafficBuckets(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTrafficBuckets(context.Background(), delta); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListTrafficTrend(context.Background(), TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		Granularity: "hour",
		From:        bucket,
		To:          bucket.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 200 || rows[0].TXBytes != 400 {
		t.Fatalf("rows = %+v", rows)
	}

	for _, granularity := range []string{"day", "month"} {
		t.Run(granularity, func(t *testing.T) {
			rows, err := store.ListTrafficTrend(context.Background(), TrafficTrendQuery{
				AgentID:     "edge-1",
				ScopeType:   "http_rule",
				ScopeID:     "11",
				Granularity: granularity,
				From:        bucket.AddDate(0, -1, 0),
				To:          bucket.AddDate(0, 1, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].RXBytes != 200 || rows[0].TXBytes != 400 {
				t.Fatalf("rows = %+v", rows)
			}
		})
	}
}

func TestIncrementTrafficBucketsValidation(t *testing.T) {
	store := newTrafficTestStore(t, true)

	err := store.IncrementTrafficBuckets(context.Background(), TrafficDelta{
		AgentID:     "edge-1",
		BucketStart: time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC),
		RXBytes:     100,
	})
	if err == nil || !strings.Contains(err.Error(), "scope_type") {
		t.Fatalf("IncrementTrafficBuckets() error = %v, want scope_type error", err)
	}
}

func TestIncrementTrafficBucketsEmptyAgentIDUsesLocalAgentAndAllowsAggregateScopeID(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		ScopeType:   "agent_total",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     200,
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        bucket,
		To:          bucket.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AgentID != "local" || rows[0].ScopeID != "" || rows[0].RXBytes != 100 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestTrafficTrendValidation(t *testing.T) {
	store := newTrafficTestStore(t, true)

	_, err := store.ListTrafficTrend(context.Background(), TrafficTrendQuery{
		AgentID:     "edge-1",
		Granularity: "hour",
	})
	if err == nil || !strings.Contains(err.Error(), "scope_type") {
		t.Fatalf("ListTrafficTrend() error = %v, want scope_type error", err)
	}
}

func TestDeleteTrafficBeforeRemovesExpiredBuckets(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	oldBucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	newBucket := oldBucket.Add(2 * time.Hour)

	for _, bucket := range []time.Time{oldBucket, newBucket} {
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     "edge-1",
			ScopeType:   "http_rule",
			ScopeID:     "11",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     200,
		}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteTrafficBefore(ctx, "edge-1", TrafficCleanupCutoff{
		HourlyBefore: oldBucket.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		Granularity: "hour",
		From:        oldBucket,
		To:          newBucket.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].BucketStart.Equal(newBucket) {
		t.Fatalf("rows = %+v", rows)
	}
}

func openTrafficTestGormDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := openSQLiteForTest(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	t.Cleanup(func() {
		closeSQLiteForTest(t, db)
	})
	return db
}

func newTrafficTestStore(t *testing.T, trafficStatsEnabled bool) *GormStore {
	t.Helper()

	store, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            t.TempDir(),
		LocalAgentID:        "local",
		TrafficStatsEnabled: trafficStatsEnabled,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close() error = %v", err)
		}
	})
	return store
}
