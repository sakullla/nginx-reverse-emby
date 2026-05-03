package storage

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
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

func TestListTrafficPolicies(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:   "edge-b",
		Direction: "tx",
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy(edge-b) error = %v", err)
	}
	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:   "edge-a",
		Direction: "rx",
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy(edge-a) error = %v", err)
	}

	rows, err := store.ListTrafficPolicies(ctx)
	if err != nil {
		t.Fatalf("ListTrafficPolicies() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].AgentID != "edge-a" || rows[0].Direction != "rx" {
		t.Fatalf("rows[0] = %+v", rows[0])
	}
	if rows[1].AgentID != "edge-b" || rows[1].Direction != "tx" {
		t.Fatalf("rows[1] = %+v", rows[1])
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

func TestListTrafficBaselines(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		AgentID:           "edge-b",
		CycleStart:        "2026-06-01T00:00:00Z",
		RawAccountedBytes: 20,
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline(edge-b) error = %v", err)
	}
	if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		AgentID:           "edge-a",
		CycleStart:        "2026-05-01T00:00:00Z",
		RawAccountedBytes: 10,
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline(edge-a) error = %v", err)
	}

	rows, err := store.ListTrafficBaselines(ctx)
	if err != nil {
		t.Fatalf("ListTrafficBaselines() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	if rows[0].AgentID != "edge-a" || rows[0].RawAccountedBytes != 10 {
		t.Fatalf("rows[0] = %+v", rows[0])
	}
	if rows[1].AgentID != "edge-b" || rows[1].RawAccountedBytes != 20 {
		t.Fatalf("rows[1] = %+v", rows[1])
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

func TestDeleteTrafficBeforeEmptyAgentIDUsesLocalAgent(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	oldBucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		ScopeType:   "agent_total",
		BucketStart: oldBucket,
		RXBytes:     100,
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteTrafficBefore(ctx, "", TrafficCleanupCutoff{HourlyBefore: oldBucket.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want local row deleted", deleted)
	}
}

func TestIngestTrafficCursorDeltaIsIdempotent(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	cursor := AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    50,
		ObservedAt: observedAt.Format(time.RFC3339),
	}

	first, err := store.IngestTrafficCursorDelta(ctx, cursor, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.IngestTrafficCursorDelta(ctx, cursor, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeltaRXBytes != 100 || first.DeltaTXBytes != 50 || second.DeltaRXBytes != 0 || second.DeltaTXBytes != 0 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 100 || rows[0].TXBytes != 50 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestIngestTrafficCursorDeltaConcurrentFirstIngestCountsOnce(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	cursor := AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    50,
		ObservedAt: observedAt.Format(time.RFC3339),
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.IngestTrafficCursorDelta(ctx, cursor, observedAt)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 100 || rows[0].TXBytes != 50 {
		t.Fatalf("rows = %+v, want single first-ingest delta", rows)
	}
}

func TestIngestTrafficCursorDeltaRollsBackWhenResetEventFails(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    50,
		ObservedAt: observedAt.Format(time.RFC3339),
	}, observedAt); err != nil {
		t.Fatal(err)
	}

	_, err := store.IngestTrafficCursorDeltaWithEvent(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    10,
		TXBytes:    5,
		ObservedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	}, observedAt.Add(time.Hour), &AgentTrafficEventRow{
		AgentID:   "edge-1",
		Message:   "traffic counter reset",
		CreatedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("IngestTrafficCursorDeltaWithEvent() error = nil, want event failure")
	}

	cursor, ok, err := store.GetTrafficCursor(ctx, "edge-1", "agent_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || cursor.RXBytes != 100 || cursor.TXBytes != 50 {
		t.Fatalf("cursor = %+v ok=%v, want original cursor after rollback", cursor, ok)
	}
	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 100 || rows[0].TXBytes != 50 {
		t.Fatalf("rows = %+v, want reset delta rolled back", rows)
	}
}

func TestListTrafficBreakdownGroupsByScopeID(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: bucket, RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: bucket.Add(30 * time.Minute), RXBytes: 50, TXBytes: 25},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "12", BucketStart: bucket, RXBytes: 7, TXBytes: 9},
		{AgentID: "edge-1", ScopeType: "l4_rule", ScopeID: "99", BucketStart: bucket, RXBytes: 1000, TXBytes: 2000},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficBreakdown(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		Granularity: "hour",
		From:        bucket,
		To:          bucket.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two grouped scope IDs", rows)
	}
	assertTrafficBucket(t, rows, "http_rule", "11", 150, 225)
	assertTrafficBucket(t, rows, "http_rule", "12", 7, 9)
}

func TestSaveTrafficEventPersists(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficEvent(ctx, AgentTrafficEventRow{
		AgentID:   "edge-1",
		EventType: "counter_reset",
		Message:   "traffic counter reset",
		Payload:   `{"scope_type":"agent_total"}`,
		CreatedAt: "2026-05-03T08:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	var rows []AgentTrafficEventRow
	if err := store.db.WithContext(ctx).Where("agent_id = ?", "edge-1").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].EventType != "counter_reset" || rows[0].Payload == "" {
		t.Fatalf("rows = %+v", rows)
	}
}

func assertTrafficBucket(t *testing.T, rows []TrafficBucketRow, scopeType, scopeID string, rx, tx uint64) {
	t.Helper()
	for _, row := range rows {
		if row.ScopeType == scopeType && row.ScopeID == scopeID {
			if row.RXBytes != rx || row.TXBytes != tx {
				t.Fatalf("%s/%s = %+v, want rx=%d tx=%d", scopeType, scopeID, row, rx, tx)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s in %+v", scopeType, scopeID, rows)
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
