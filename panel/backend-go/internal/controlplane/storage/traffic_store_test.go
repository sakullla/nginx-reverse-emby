package storage

import (
	"context"
	"path/filepath"
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
