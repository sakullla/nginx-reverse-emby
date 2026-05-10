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
	if policy.Direction != "both" || policy.CycleStartDay != 1 || policy.HourlyRetentionDays != 30 || policy.DailyRetentionMonths != 3 || policy.MonthlyRetentionMonths == nil || *policy.MonthlyRetentionMonths != 36 {
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

func TestTrafficPolicySaveAndReloadPreservesNilMonthlyRetention(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   3,
		MonthlyRetentionMonths: nil,
	}); err != nil {
		t.Fatal(err)
	}

	policy, err := store.GetTrafficPolicy(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if policy.MonthlyRetentionMonths != nil {
		t.Fatalf("MonthlyRetentionMonths = %v, want nil", *policy.MonthlyRetentionMonths)
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

func TestReplaceTrafficPoliciesRemovesRowsMissingFromReplacement(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()

	for _, row := range []AgentTrafficPolicyRow{
		{AgentID: "edge-a", Direction: "rx"},
		{AgentID: "edge-b", Direction: "tx"},
	} {
		if err := store.SaveTrafficPolicy(ctx, row); err != nil {
			t.Fatalf("SaveTrafficPolicy(%s) error = %v", row.AgentID, err)
		}
	}

	if err := store.ReplaceTrafficPolicies(ctx, []AgentTrafficPolicyRow{{
		AgentID:       "edge-a",
		Direction:     "both",
		CycleStartDay: 5,
	}}); err != nil {
		t.Fatalf("ReplaceTrafficPolicies() error = %v", err)
	}

	rows, err := store.ListTrafficPolicies(ctx)
	if err != nil {
		t.Fatalf("ListTrafficPolicies() error = %v", err)
	}
	if len(rows) != 1 || rows[0].AgentID != "edge-a" || rows[0].Direction != "both" || rows[0].CycleStartDay != 5 {
		t.Fatalf("rows after replace = %+v", rows)
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

func TestReplaceTrafficBaselinesRemovesRowsMissingFromReplacementWithoutDeletingHistory(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	cycleStart := "2026-05-01T00:00:00Z"

	for _, row := range []AgentTrafficBaselineRow{
		{AgentID: "edge-a", CycleStart: cycleStart, RawAccountedBytes: 10},
		{AgentID: "edge-b", CycleStart: cycleStart, RawAccountedBytes: 20},
	} {
		if err := store.SaveTrafficBaseline(ctx, row); err != nil {
			t.Fatalf("SaveTrafficBaseline(%s) error = %v", row.AgentID, err)
		}
	}
	if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-b",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: "2026-05-01T01:00:00Z",
	}); err != nil {
		t.Fatalf("SaveTrafficCursor() error = %v", err)
	}

	if err := store.ReplaceTrafficBaselines(ctx, []AgentTrafficBaselineRow{{
		AgentID:           "edge-a",
		CycleStart:        cycleStart,
		RawAccountedBytes: 30,
	}}); err != nil {
		t.Fatalf("ReplaceTrafficBaselines() error = %v", err)
	}

	rows, err := store.ListTrafficBaselines(ctx)
	if err != nil {
		t.Fatalf("ListTrafficBaselines() error = %v", err)
	}
	if len(rows) != 1 || rows[0].AgentID != "edge-a" || rows[0].RawAccountedBytes != 30 {
		t.Fatalf("rows after replace = %+v", rows)
	}
	if _, found, err := store.GetTrafficCursor(ctx, "edge-b", "agent_total", ""); err != nil {
		t.Fatalf("GetTrafficCursor() error = %v", err)
	} else if !found {
		t.Fatal("traffic cursor was deleted by baseline replacement")
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

func TestIncrementTrafficBucketsPreservesLocalDailyAndMonthlyPeriods(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}

	first := time.Date(2026, 5, 5, 1, 30, 0, 0, shanghai)
	second := time.Date(2026, 5, 5, 23, 30, 0, 0, shanghai)
	for _, bucket := range []time.Time{first, second} {
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     "edge-1",
			ScopeType:   "agent_total",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     50,
		}); err != nil {
			t.Fatal(err)
		}
	}

	dailyRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        time.Date(2026, 5, 5, 0, 0, 0, 0, shanghai),
		To:          time.Date(2026, 5, 6, 0, 0, 0, 0, shanghai),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDayStart := time.Date(2026, 5, 5, 0, 0, 0, 0, shanghai)
	if len(dailyRows) != 1 || !dailyRows[0].BucketStart.Equal(wantDayStart) || dailyRows[0].RXBytes != 200 || dailyRows[0].TXBytes != 100 {
		t.Fatalf("dailyRows = %+v, want one comparable UTC instant for Asia/Shanghai local-day bucket at %s", dailyRows, wantDayStart)
	}

	monthlyRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 5, 1, 0, 0, 0, 0, shanghai),
		To:          time.Date(2026, 6, 1, 0, 0, 0, 0, shanghai),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMonthStart := time.Date(2026, 5, 1, 0, 0, 0, 0, shanghai)
	if len(monthlyRows) != 1 || !monthlyRows[0].BucketStart.Equal(wantMonthStart) || monthlyRows[0].RXBytes != 200 || monthlyRows[0].TXBytes != 100 {
		t.Fatalf("monthlyRows = %+v, want one comparable UTC instant for Asia/Shanghai local-month bucket at %s", monthlyRows, wantMonthStart)
	}
}

func TestIncrementTrafficBucketsUsesLocalHourForFractionalOffsetTimezone(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	bucket := time.Date(2026, 5, 5, 1, 30, 0, 0, kathmandu)

	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     50,
	}); err != nil {
		t.Fatal(err)
	}

	wantLocalHour := time.Date(2026, 5, 5, 1, 0, 0, 0, kathmandu)
	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        wantLocalHour,
		To:          wantLocalHour.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].BucketStart.Equal(wantLocalHour) || rows[0].RXBytes != 100 || rows[0].TXBytes != 50 {
		t.Fatalf("rows = %+v, want local hourly bucket at %s", rows, wantLocalHour)
	}
}

func TestIncrementTrafficBucketsPreservesDistinctDSTFallbackHours(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	first0130 := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC).In(newYork)
	second0130 := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC).In(newYork)
	if first0130.Hour() != 1 || second0130.Hour() != 1 || first0130.Equal(second0130) {
		t.Fatalf("unexpected DST fixture: first=%s second=%s", first0130, second0130)
	}

	for i, bucket := range []time.Time{first0130, second0130} {
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     "edge-1",
			ScopeType:   "agent_total",
			BucketStart: bucket,
			RXBytes:     uint64(100 + i),
			TXBytes:     50,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want distinct buckets for repeated local DST hour", rows)
	}
	if rows[0].BucketStart.Equal(rows[1].BucketStart) || rows[0].RXBytes != 100 || rows[1].RXBytes != 101 {
		t.Fatalf("rows = %+v, want separate EDT and EST hourly buckets", rows)
	}
}

func TestDailyAndMonthlyPeriodRangesCompareByInstant(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	dayStart := time.Date(2026, 5, 5, 0, 0, 0, 0, newYork)
	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: dayStart.Add(2 * time.Hour),
		RXBytes:     100,
		TXBytes:     50,
	}); err != nil {
		t.Fatal(err)
	}

	dailyRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        dayStart,
		To:          dayStart.AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dailyRows) != 1 || !dailyRows[0].BucketStart.Equal(dayStart) {
		t.Fatalf("dailyRows = %+v, want cutoff-equal America/New_York local-day bucket", dailyRows)
	}

	monthlyRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 5, 1, 0, 0, 0, 0, newYork),
		To:          time.Date(2026, 6, 1, 0, 0, 0, 0, newYork),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(monthlyRows) != 1 || monthlyRows[0].BucketStart.Month() != time.May {
		t.Fatalf("monthlyRows = %+v, want cutoff-included America/New_York local-month bucket", monthlyRows)
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

func TestDeleteTrafficBeforeComparesDailyAndMonthlyPeriodsByInstant(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cutoffDay := time.Date(2026, 5, 5, 0, 0, 0, 0, newYork)

	for _, bucket := range []time.Time{
		cutoffDay.AddDate(0, 0, -1).Add(2 * time.Hour),
		cutoffDay.Add(2 * time.Hour),
	} {
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     "edge-1",
			ScopeType:   "agent_total",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     50,
		}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteTrafficBefore(ctx, "edge-1", TrafficCleanupCutoff{
		DailyBefore: cutoffDay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want only rows before cutoff instant deleted", deleted)
	}
	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        cutoffDay,
		To:          cutoffDay.AddDate(0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].BucketStart.Equal(cutoffDay) {
		t.Fatalf("rows = %+v, want cutoff-equal local day preserved", rows)
	}

	monthStore := newTrafficTestStore(t, true)
	cutoffMonth := time.Date(2026, 5, 1, 0, 0, 0, 0, newYork)
	for _, bucket := range []time.Time{
		cutoffMonth.AddDate(0, -1, 0).Add(2 * time.Hour),
		cutoffMonth.Add(2 * time.Hour),
	} {
		if err := monthStore.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     "edge-1",
			ScopeType:   "agent_total",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     50,
		}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err = monthStore.DeleteTrafficBefore(ctx, "edge-1", TrafficCleanupCutoff{
		MonthlyBefore: cutoffMonth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("monthly deleted = %d, want only rows before cutoff instant deleted", deleted)
	}
	rows, err = monthStore.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        cutoffMonth,
		To:          cutoffMonth.AddDate(0, 1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BucketStart.Month() != time.May {
		t.Fatalf("monthly rows = %+v, want cutoff-equal local month preserved", rows)
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

func TestDeleteTrafficByScopeRemovesOnlyMatchingScope(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	samples := []struct {
		agentID   string
		scopeType string
		scopeID   string
	}{
		{agentID: "edge-1", scopeType: "http_rule", scopeID: "11"},
		{agentID: "edge-1", scopeType: "http_rule", scopeID: "12"},
		{agentID: "edge-2", scopeType: "http_rule", scopeID: "11"},
	}
	for _, sample := range samples {
		if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
			AgentID:    sample.agentID,
			ScopeType:  sample.scopeType,
			ScopeID:    sample.scopeID,
			RXBytes:    100,
			TXBytes:    200,
			ObservedAt: bucket.Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     sample.agentID,
			ScopeType:   sample.scopeType,
			ScopeID:     sample.scopeID,
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     200,
		}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteTrafficByScope(ctx, "edge-1", "http_rule", "11")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d, want cursor plus hourly/daily/monthly rows", deleted)
	}

	assertTrafficScopeRows(t, store, "edge-1", "http_rule", "11", 0)
	assertTrafficScopeRows(t, store, "edge-1", "http_rule", "12", 4)
	assertTrafficScopeRows(t, store, "edge-2", "http_rule", "11", 4)
}

func TestDeleteAgentRemovesAssociatedTrafficData(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	for _, agentID := range []string{"edge-1", "edge-2"} {
		if err := store.SaveAgent(ctx, AgentRow{ID: agentID}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
			AgentID:              agentID,
			Direction:            "both",
			CycleStartDay:        1,
			HourlyRetentionDays:  180,
			DailyRetentionMonths: 24,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
			AgentID:    agentID,
			CycleStart: bucket.Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
			AgentID:    agentID,
			ScopeType:  "http_rule",
			ScopeID:    "11",
			RXBytes:    100,
			TXBytes:    200,
			ObservedAt: bucket.Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     agentID,
			ScopeType:   "http_rule",
			ScopeID:     "11",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     200,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficEvent(ctx, AgentTrafficEventRow{
			AgentID:   agentID,
			EventType: "cleanup",
			Message:   "traffic cleanup",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.DeleteAgent(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetTrafficCursor(ctx, "edge-1", "http_rule", "11"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("expected deleted traffic cursor to be removed")
	}

	assertTrafficAgentRows(t, store, "edge-1", 0)
	assertTrafficAgentRows(t, store, "edge-2", 1)
}

func TestDeleteTrafficDataIgnoresMissingTrafficTables(t *testing.T) {
	store := newTrafficTestStore(t, false)
	ctx := context.Background()

	deleted, err := store.DeleteTrafficByScope(ctx, "edge-1", "http_rule", "11")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("scope deleted = %d, want 0", deleted)
	}

	deleted, err = store.DeleteTrafficByAgent(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("agent deleted = %d, want 0", deleted)
	}
}

func assertTrafficScopeRows(t *testing.T, store *GormStore, agentID, scopeType, scopeID string, want int64) {
	t.Helper()

	models := []any{
		&AgentTrafficRawCursorRow{},
		&AgentTrafficHourlyBucketRow{},
		&AgentTrafficDailySummaryRow{},
		&AgentTrafficMonthlySummaryRow{},
	}
	for _, model := range models {
		var got int64
		if err := store.db.Model(model).
			Where("agent_id = ? AND scope_type = ? AND scope_id = ?", agentID, scopeType, scopeID).
			Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != want/4 {
			t.Fatalf("%T rows for %s/%s/%s = %d, want %d", model, agentID, scopeType, scopeID, got, want/4)
		}
	}
}

func assertTrafficAgentRows(t *testing.T, store *GormStore, agentID string, wantEach int64) {
	t.Helper()

	models := []any{
		&AgentTrafficPolicyRow{},
		&AgentTrafficBaselineRow{},
		&AgentTrafficRawCursorRow{},
		&AgentTrafficHourlyBucketRow{},
		&AgentTrafficDailySummaryRow{},
		&AgentTrafficMonthlySummaryRow{},
		&AgentTrafficEventRow{},
	}
	for _, model := range models {
		var got int64
		if err := store.db.Model(model).Where("agent_id = ?", agentID).Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != wantEach {
			t.Fatalf("%T rows for %s = %d, want %d", model, agentID, got, wantEach)
		}
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

func TestIngestTrafficCursorDeltaHostFirstSampleSeedsBaselineOnly(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	first, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    1000,
		TXBytes:    2000,
		ObservedAt: observedAt.Format(time.RFC3339),
	}, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    1200,
		TXBytes:    2300,
		ObservedAt: observedAt.Add(time.Minute).Format(time.RFC3339),
	}, observedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if first.DeltaRXBytes != 0 || first.DeltaTXBytes != 0 || second.DeltaRXBytes != 200 || second.DeltaTXBytes != 300 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 200 || rows[0].TXBytes != 300 {
		t.Fatalf("rows = %+v, want only second host delta", rows)
	}
}

func TestIngestTrafficCursorDeltaHostBootIDChangeResetsCounter(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    1000,
		TXBytes:    2000,
		BootID:     "boot-a",
		ObservedAt: observedAt.Format(time.RFC3339),
	}, observedAt); err != nil {
		t.Fatal(err)
	}
	result, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    1200,
		TXBytes:    2300,
		BootID:     "boot-b",
		ObservedAt: observedAt.Add(time.Minute).Format(time.RFC3339),
	}, observedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !result.CounterReset || result.DeltaRXBytes != 1200 || result.DeltaTXBytes != 2300 {
		t.Fatalf("result = %+v, want boot-id reset with current counters", result)
	}
	cursor, found, err := store.GetTrafficCursor(ctx, "edge-1", "host_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || cursor.BootID != "boot-b" {
		t.Fatalf("cursor found=%v row=%+v, want boot-b", found, cursor)
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
