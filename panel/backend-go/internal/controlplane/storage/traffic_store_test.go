package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func TestWireGuardSchemaDisabledSkipsWireGuardTables(t *testing.T) {
	db := openTrafficTestGormDB(t)

	if err := BootstrapSchema(context.Background(), db, SchemaOptions{TrafficStatsEnabled: true, WireGuardEnabled: testBoolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasTable(&WireGuardProfileRow{}) {
		t.Fatal("wireguard profile table exists while module disabled")
	}
	if db.Migrator().HasTable(&WireGuardClientRow{}) {
		t.Fatal("wireguard client table exists while module disabled")
	}
	if !db.Migrator().HasTable(&AgentTrafficPolicyRow{}) {
		t.Fatal("traffic policy table missing while traffic stats enabled")
	}
}

func testBoolPtr(value bool) *bool {
	return &value
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

func TestTrafficBucketTablesHaveAggregateQueryIndexes(t *testing.T) {
	store := newTrafficTestStore(t, true)
	for _, index := range []struct {
		model any
		name  string
	}{
		{model: &AgentTrafficHourlyBucketRow{}, name: "idx_agent_traffic_hourly_aggregate"},
		{model: &AgentTrafficDailySummaryRow{}, name: "idx_agent_traffic_daily_aggregate"},
		{model: &AgentTrafficMonthlySummaryRow{}, name: "idx_agent_traffic_monthly_aggregate"},
	} {
		if !store.db.Migrator().HasIndex(index.model, index.name) {
			t.Fatalf("missing traffic aggregate index %s", index.name)
		}
	}
}

func TestTrafficDailyAggregateQueryUsesAggregateIndex(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 100},
		{AgentID: "edge-2", ScopeType: "l4_rule", ScopeID: "22", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 200},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	plan := []struct {
		Detail string `gorm:"column:detail"`
	}{}
	if err := store.db.Raw(`
		EXPLAIN QUERY PLAN
		SELECT agent_id, scope_type, scope_id, SUM(rx_bytes) AS rx_bytes, SUM(tx_bytes) AS tx_bytes
		FROM agent_traffic_daily_summaries
		WHERE agent_id IN (?, ?) AND scope_type IN (?, ?) AND period_start >= ? AND period_start < ?
		GROUP BY agent_id, scope_type, scope_id
	`, "edge-1", "edge-2", "http_rule", "l4_rule", "2026-05-19T00:00:00Z", "2026-05-21T00:00:00Z").Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range plan {
		if strings.Contains(row.Detail, "idx_agent_traffic_daily_aggregate") {
			return
		}
	}
	t.Fatalf("query plan = %+v, want idx_agent_traffic_daily_aggregate", plan)
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

func TestIncrementTrafficBucketsMonthlySummaryUsesPolicyCycleStartDay(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "rx",
		CycleStartDay: 15,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, shanghai), RXBytes: 100, TXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai), RXBytes: 200, TXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 4, 15, 0, 0, 0, 0, shanghai),
		To:          time.Date(2026, 6, 15, 0, 0, 0, 0, shanghai),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two cycle-month summary rows", rows)
	}
	assertTrafficBucketAt(t, rows, "edge-1", "agent_total", "", time.Date(2026, 4, 15, 0, 0, 0, 0, shanghai), 100, 10)
	assertTrafficBucketAt(t, rows, "edge-1", "agent_total", "", time.Date(2026, 5, 15, 0, 0, 0, 0, shanghai), 200, 20)
}

func TestRetainedMonthlyResidualBytesReplacesRebuiltTargetRows(t *testing.T) {
	tests := []struct {
		name     string
		existing uint64
		rebuilt  uint64
		want     uint64
	}{
		{name: "same daily backed row", existing: 40, rebuilt: 40, want: 0},
		{name: "residual only row smaller than rebuilt", existing: 25, rebuilt: 40, want: 25},
		{name: "mixed row larger than rebuilt", existing: 65, rebuilt: 40, want: 25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := retainedMonthlyResidualBytes(tc.existing, tc.rebuilt); got != tc.want {
				t.Fatalf("retainedMonthlyResidualBytes(%d, %d) = %d, want %d", tc.existing, tc.rebuilt, got, tc.want)
			}
		})
	}
}

func TestTrafficPolicyMonthlyRebuildReplacesExistingTargetRow(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	now := nowTrafficTimestamp()
	if err := store.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 6,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&AgentTrafficDailySummaryRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		PeriodStart: "2026-01-20T00:00:00Z",
		RXBytes:     10,
		UpdatedAt:   now,
		CreatedAt:   now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&AgentTrafficMonthlySummaryRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		PeriodStart: "2026-01-01T00:00:00Z",
		RXBytes:     10,
		UpdatedAt:   now,
		CreatedAt:   now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := store.SaveTrafficPolicyAndRebuildMonthlySummaries(ctx, AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 6,
	}, true, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), 15); err != nil {
		t.Fatal(err)
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RXBytes != 10 {
		t.Fatalf("monthly rows = %+v, want rebuilt target row replaced without double-counting", rows)
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

func TestListTrafficBreakdownByScopeTypesAggregatesAgentsAndScopes(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	inWindow := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	outOfWindow := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: inWindow, RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-1", ScopeType: "l4_rule", ScopeID: "22", BucketStart: inWindow, RXBytes: 30, TXBytes: 40},
		{AgentID: "edge-2", ScopeType: "http_rule", ScopeID: "11", BucketStart: inWindow, RXBytes: 300, TXBytes: 400},
		{AgentID: "edge-2", ScopeType: "relay_listener", ScopeID: "33", BucketStart: inWindow, RXBytes: 50, TXBytes: 60},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: inWindow, RXBytes: 999, TXBytes: 999},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "old", BucketStart: outOfWindow, RXBytes: 777, TXBytes: 777},
		{AgentID: "edge-3", ScopeType: "http_rule", ScopeID: "11", BucketStart: inWindow, RXBytes: 888, TXBytes: 888},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficBreakdownByScopeTypes(ctx, TrafficBreakdownQuery{
		AgentIDs:    []string{"edge-1", "edge-2"},
		ScopeTypes:  []string{"http_rule", "l4_rule", "relay_listener"},
		Granularity: "day",
		From:        time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %+v, want four scoped aggregates", rows)
	}
	assertTrafficAgentBucket(t, rows, "edge-1", "http_rule", "11", 100, 200)
	assertTrafficAgentBucket(t, rows, "edge-1", "l4_rule", "22", 30, 40)
	assertTrafficAgentBucket(t, rows, "edge-2", "http_rule", "11", 300, 400)
	assertTrafficAgentBucket(t, rows, "edge-2", "relay_listener", "33", 50, 60)
}

func TestListTrafficTrendByScopeTypesFiltersAgentsScopesAndWindow(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	inWindow := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	outOfWindow := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: inWindow, RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: inWindow, RXBytes: 30, TXBytes: 40},
		{AgentID: "edge-2", ScopeType: "agent_total", BucketStart: inWindow, RXBytes: 300, TXBytes: 400},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: inWindow, RXBytes: 999, TXBytes: 999},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: outOfWindow, RXBytes: 777, TXBytes: 777},
		{AgentID: "edge-3", ScopeType: "agent_total", BucketStart: inWindow, RXBytes: 888, TXBytes: 888},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListTrafficTrendByScopeTypes(ctx, TrafficBreakdownQuery{
		AgentIDs:    []string{"edge-1", "edge-2"},
		ScopeTypes:  []string{"agent_total", "host_total"},
		Granularity: "day",
		From:        time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want three scoped trend rows", rows)
	}
	assertTrafficAgentBucket(t, rows, "edge-1", "agent_total", "", 100, 200)
	assertTrafficAgentBucket(t, rows, "edge-1", "host_total", "", 30, 40)
	assertTrafficAgentBucket(t, rows, "edge-2", "agent_total", "", 300, 400)
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
	if deleted != 3 {
		t.Fatalf("deleted = %d, want hourly/daily/monthly rows", deleted)
	}

	assertTrafficScopeRows(t, store, "edge-1", "http_rule", "11", 1)
	assertTrafficScopeRows(t, store, "edge-1", "http_rule", "12", 4)
	assertTrafficScopeRows(t, store, "edge-2", "http_rule", "11", 4)
}

func TestDeleteTrafficByScopeKeepsCursorBaselineForReusedRuleID(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "http_rule",
		ScopeID:    "11",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: bucket.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
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

	deleted, err := store.DeleteTrafficByScope(ctx, "edge-1", "http_rule", "11")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want hourly/daily/monthly rows only", deleted)
	}

	cursor, ok, err := store.GetTrafficCursor(ctx, "edge-1", "http_rule", "11")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected cursor baseline to remain")
	}
	if cursor.RXBytes != 100 || cursor.TXBytes != 200 {
		t.Fatalf("cursor = %+v, want old cumulative baseline preserved", cursor)
	}
	assertTrafficScopeRows(t, store, "edge-1", "http_rule", "11", 1)
}

func TestDeleteTrafficBucketsByAgentKeepsCursorBaselinePolicyAndEvents(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	for _, agentID := range []string{"edge-1", "edge-2"} {
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
			AgentID:           agentID,
			CycleStart:        "2026-05-01T00:00:00Z",
			RawAccountedBytes: 300,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
			AgentID:    agentID,
			ScopeType:  "agent_total",
			RXBytes:    100,
			TXBytes:    200,
			ObservedAt: bucket.Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
			AgentID:     agentID,
			ScopeType:   "agent_total",
			BucketStart: bucket,
			RXBytes:     100,
			TXBytes:     200,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveTrafficEvent(ctx, AgentTrafficEventRow{
			AgentID:   agentID,
			EventType: "calibration",
			Message:   "traffic usage calibrated",
		}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteTrafficBucketsByAgent(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want hourly/daily/monthly bucket rows only", deleted)
	}

	assertTrafficScopeRows(t, store, "edge-1", "agent_total", "", 1)
	assertTrafficScopeRows(t, store, "edge-2", "agent_total", "", 4)
	if _, ok, err := store.GetTrafficCursor(ctx, "edge-1", "agent_total", ""); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("cursor was deleted")
	}
	if _, ok, err := store.GetTrafficBaseline(ctx, "edge-1", "2026-05-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("baseline was deleted")
	}
	if _, err := store.GetTrafficPolicy(ctx, "edge-1"); err != nil {
		t.Fatalf("policy was deleted: %v", err)
	}
}

func TestDeleteTrafficBucketsByAgentInWindowPreservesHistoryOutsideWindow(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	currentBucket := time.Date(2026, 5, 20, 8, 0, 0, 0, time.UTC)
	previousBucket := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	nextBucket := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	for _, delta := range []TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: currentBucket, RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: currentBucket, RXBytes: 10, TXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: previousBucket, RXBytes: 300, TXBytes: 400},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: nextBucket, RXBytes: 500, TXBytes: 600},
		{AgentID: "edge-2", ScopeType: "agent_total", BucketStart: currentBucket, RXBytes: 700, TXBytes: 800},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.DeleteTrafficBucketsByAgentInWindow(ctx, "edge-1", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 7 {
		t.Fatalf("deleted = %d, want hourly/daily rows and overlapping monthly rows for two current-cycle scopes", deleted)
	}

	rows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("edge-1 rows = %+v, want previous and next cycle rows preserved", rows)
	}
	assertTrafficBucketAt(t, rows, "edge-1", "agent_total", "", time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC), 300, 400)
	assertTrafficBucketAt(t, rows, "edge-1", "agent_total", "", nextBucket, 500, 600)
	otherRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-2",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(otherRows) != 1 {
		t.Fatalf("edge-2 rows = %+v, want other agent current-cycle row preserved", otherRows)
	}

	monthlyRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(monthlyRows) != 1 {
		t.Fatalf("monthly rows = %+v, want pre-cycle month preserved", monthlyRows)
	}
	assertTrafficBucketAt(t, monthlyRows, "edge-1", "agent_total", "", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), 300, 400)

	juneRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(juneRows) != 1 || juneRows[0].RXBytes != 500 || juneRows[0].TXBytes != 600 {
		t.Fatalf("june monthly rows = %+v, want post-cycle June usage rebuilt", juneRows)
	}
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

func TestListTrafficAgentIDsUsesAgentsAndRawCursors(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.SaveAgent(ctx, AgentRow{ID: "agent-only"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
		AgentID:    "cursor-only",
		ScopeType:  "http_rule",
		ScopeID:    "11",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: bucket.Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		AgentID:     "bucket-only",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     200,
	}); err != nil {
		t.Fatal(err)
	}

	agentIDs, err := store.ListTrafficAgentIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agent-only", "bucket-only", "cursor-only"}
	if strings.Join(agentIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("ListTrafficAgentIDs() = %+v, want %+v", agentIDs, want)
	}
}

func TestListTrafficAgentIDsFallsBackToBucketsWhenFastSourcesAreEmpty(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		AgentID:     "bucket-only",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     200,
	}); err != nil {
		t.Fatal(err)
	}

	agentIDs, err := store.ListTrafficAgentIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bucket-only"}
	if strings.Join(agentIDs, ",") != strings.Join(want, ",") {
		t.Fatalf("ListTrafficAgentIDs() = %+v, want %+v", agentIDs, want)
	}
}

func TestListTrafficAgentIDsDoesNotScanBucketTables(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	bucket := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	if err := store.IncrementTrafficBuckets(ctx, TrafficDelta{
		AgentID:     "bucket-only",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: bucket,
		RXBytes:     100,
		TXBytes:     200,
	}); err != nil {
		t.Fatal(err)
	}

	var queries []string
	callbackName := "test:list_traffic_agent_ids_queries"
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		queries = append(queries, strings.ToLower(tx.Statement.SQL.String()))
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = store.db.Callback().Query().Remove(callbackName)
	}()

	agentIDs, err := store.ListTrafficAgentIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(agentIDs, ",") != "bucket-only" {
		t.Fatalf("ListTrafficAgentIDs() = %+v, want bucket-only", agentIDs)
	}
	for _, query := range queries {
		for _, table := range []string{"agent_traffic_hourly_buckets", "agent_traffic_daily_summaries", "agent_traffic_monthly_summaries"} {
			if strings.Contains(query, table) {
				t.Fatalf("ListTrafficAgentIDs scanned bucket table %s via query %q", table, query)
			}
		}
	}
}

func TestBootstrapSchemaBackfillsTrafficAgentIndexFromBuckets(t *testing.T) {
	db := openTrafficTestGormDB(t)
	ctx := context.Background()

	if err := BootstrapSchema(ctx, db, SchemaOptions{TrafficStatsEnabled: true, WireGuardEnabled: testBoolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&AgentTrafficAgentRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("key = ?", trafficAgentIndexBackfillMarkerKey).Delete(&MetaRow{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&AgentTrafficHourlyBucketRow{
		AgentID:     "legacy-bucket",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: "2026-05-03T08:00:00Z",
		RXBytes:     100,
		TXBytes:     200,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := BootstrapSchema(ctx, db, SchemaOptions{TrafficStatsEnabled: true, WireGuardEnabled: testBoolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	store := &GormStore{db: db, localAgentID: "local"}
	agentIDs, err := store.ListTrafficAgentIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(agentIDs, ",") != "legacy-bucket" {
		t.Fatalf("ListTrafficAgentIDs() = %+v, want legacy-bucket", agentIDs)
	}
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

	var total int64
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
		total += got
	}
	if total != want {
		t.Fatalf("traffic scope rows for %s/%s/%s = %d, want %d", agentID, scopeType, scopeID, total, want)
	}
}

func assertTrafficAgentRows(t *testing.T, store *GormStore, agentID string, wantEach int64) {
	t.Helper()

	models := []any{
		&AgentTrafficAgentRow{},
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

func TestIngestTrafficCursorDeltaSkipsCursorWriteWhenCountersUnchanged(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	firstObservedAt := observedAt.Format(time.RFC3339)

	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    100,
		TXBytes:    50,
		BootID:     "boot-a",
		ObservedAt: firstObservedAt,
	}, observedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    100,
		TXBytes:    50,
		BootID:     "boot-a",
		ObservedAt: observedAt.Add(time.Minute).Format(time.RFC3339),
	}, observedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	cursor, found, err := store.GetTrafficCursor(ctx, "edge-1", "host_total", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found || cursor.ObservedAt != firstObservedAt {
		t.Fatalf("cursor found=%v row=%+v, want unchanged observed_at %q", found, cursor, firstObservedAt)
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

func TestIngestTrafficCursorDeltasWithEventsProcessesBatch(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	results, err := store.IngestTrafficCursorDeltasWithEvents(ctx, []TrafficCursorIngestRow{
		{Cursor: AgentTrafficRawCursorRow{
			AgentID:    "edge-1",
			ScopeType:  "agent_total",
			RXBytes:    100,
			TXBytes:    50,
			ObservedAt: observedAt.Format(time.RFC3339),
		}},
		{Cursor: AgentTrafficRawCursorRow{
			AgentID:    "edge-1",
			ScopeType:  "http",
			RXBytes:    40,
			TXBytes:    20,
			ObservedAt: observedAt.Format(time.RFC3339),
		}},
	}, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].DeltaRXBytes != 100 || results[1].DeltaRXBytes != 40 {
		t.Fatalf("results = %+v", results)
	}

	agentRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	httpRows, err := store.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "http",
		Granularity: "hour",
		From:        observedAt,
		To:          observedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agentRows) != 1 || agentRows[0].RXBytes != 100 || len(httpRows) != 1 || httpRows[0].RXBytes != 40 {
		t.Fatalf("agentRows = %+v httpRows = %+v, want batch deltas", agentRows, httpRows)
	}
}

func TestIngestTrafficCursorDeltasWithEventsSerializesConcurrentSQLiteWriters(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)

	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			agentID := fmt.Sprintf("edge-%d", i)
			_, err := store.IngestTrafficCursorDeltasWithEvents(ctx, []TrafficCursorIngestRow{{
				Cursor: AgentTrafficRawCursorRow{
					AgentID:    agentID,
					ScopeType:  "host_total",
					RXBytes:    uint64(100 + i),
					TXBytes:    uint64(50 + i),
					BootID:     "boot-a",
					ObservedAt: observedAt.Format(time.RFC3339),
				},
			}}, observedAt)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
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

func TestIngestTrafficCursorDeltaConcurrentFirstIngestIsIdempotentAcrossStores(t *testing.T) {
	dataRoot := t.TempDir()
	storeA, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            dataRoot,
		LocalAgentID:        "local",
		TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewStore(storeA) error = %v", err)
	}
	t.Cleanup(func() {
		if err := storeA.Close(); err != nil {
			t.Fatalf("storeA.Close() error = %v", err)
		}
	})
	storeB, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            dataRoot,
		LocalAgentID:        "local",
		TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewStore(storeB) error = %v", err)
	}
	t.Cleanup(func() {
		if err := storeB.Close(); err != nil {
			t.Fatalf("storeB.Close() error = %v", err)
		}
	})

	ctx := context.Background()
	observedAt := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	cursor := AgentTrafficRawCursorRow{
		AgentID:    "edge-2",
		ScopeType:  "http_rule",
		ScopeID:    "11",
		RXBytes:    100,
		TXBytes:    50,
		ObservedAt: observedAt.Format(time.RFC3339),
	}

	if err := storeA.writeTransaction(ctx, func(tx *gorm.DB) error {
		seed := cursor
		seed.RXBytes = 0
		seed.TXBytes = 0
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error
	}); err != nil {
		t.Fatalf("storeA seed error = %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, store := range []*GormStore{storeA, storeB} {
		wg.Add(1)
		go func(store *GormStore) {
			defer wg.Done()
			<-start
			_, err := store.IngestTrafficCursorDelta(ctx, cursor, observedAt)
			errs <- err
		}(store)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	rows, err := storeA.ListTrafficTrend(ctx, TrafficTrendQuery{
		AgentID:     "edge-2",
		ScopeType:   "http_rule",
		ScopeID:     "11",
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

func TestIngestTrafficCursorDeltaFirstIngestReloadsSeedBeforeCounting(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	cursor := AgentTrafficRawCursorRow{
		AgentID:    "edge-3",
		ScopeType:  "http_rule",
		ScopeID:    "12",
		RXBytes:    100,
		TXBytes:    50,
		ObservedAt: observedAt.Format(time.RFC3339),
	}

	if err := store.writeTransaction(ctx, func(tx *gorm.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}
	const callbackName = "test:rewrite_traffic_cursor_seed"
	if err := store.writeDB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		row, ok := tx.Statement.Dest.(*AgentTrafficRawCursorRow)
		if !ok || row.AgentID != cursor.AgentID || row.ScopeType != cursor.ScopeType || row.ScopeID != cursor.ScopeID {
			return
		}
		if row.RXBytes == 0 && row.TXBytes == 0 {
			row.RXBytes = 80
			row.TXBytes = 40
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.writeDB.Callback().Create().Remove(callbackName)
	})

	result, err := store.IngestTrafficCursorDelta(ctx, cursor, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeltaRXBytes != 20 || result.DeltaTXBytes != 10 {
		t.Fatalf("result = %+v, want delta from reloaded seed", result)
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

func TestIngestTrafficCursorDeltaSkipsEventForHostRollbackWithSameBootID(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    100,
		TXBytes:    50,
		BootID:     "boot-a",
		ObservedAt: observedAt.Format(time.RFC3339),
	}, observedAt); err != nil {
		t.Fatal(err)
	}

	result, err := store.IngestTrafficCursorDeltaWithEvent(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    10,
		TXBytes:    5,
		BootID:     "boot-a",
		ObservedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	}, observedAt.Add(time.Hour), &AgentTrafficEventRow{
		AgentID:   "edge-1",
		EventType: "counter_reset",
		Message:   "traffic counter reset",
		CreatedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.CounterReset || result.DeltaRXBytes != 10 || result.DeltaTXBytes != 5 {
		t.Fatalf("result = %+v, want reset delta without event", result)
	}
	var events int64
	if err := store.db.Model(&AgentTrafficEventRow{}).Where("agent_id = ? AND event_type = ?", "edge-1", "counter_reset").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("counter reset events = %d, want 0", events)
	}
}

func TestIngestTrafficCursorDeltaRecordsEventForHostBootChange(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	observedAt := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	if _, err := store.IngestTrafficCursorDelta(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    100,
		TXBytes:    50,
		BootID:     "boot-a",
		ObservedAt: observedAt.Format(time.RFC3339),
	}, observedAt); err != nil {
		t.Fatal(err)
	}

	if _, err := store.IngestTrafficCursorDeltaWithEvent(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "host_total",
		RXBytes:    10,
		TXBytes:    5,
		BootID:     "boot-b",
		ObservedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	}, observedAt.Add(time.Hour), &AgentTrafficEventRow{
		AgentID:   "edge-1",
		EventType: "counter_reset",
		Message:   "traffic counter reset",
		CreatedAt: observedAt.Add(time.Hour).Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}
	var events int64
	if err := store.db.Model(&AgentTrafficEventRow{}).Where("agent_id = ? AND event_type = ?", "edge-1", "counter_reset").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("counter reset events = %d, want 1", events)
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

func assertTrafficAgentBucket(t *testing.T, rows []TrafficBucketRow, agentID, scopeType, scopeID string, rx, tx uint64) {
	t.Helper()
	for _, row := range rows {
		if row.AgentID == agentID && row.ScopeType == scopeType && row.ScopeID == scopeID {
			if row.RXBytes != rx || row.TXBytes != tx {
				t.Fatalf("%s/%s/%s = %+v, want rx=%d tx=%d", agentID, scopeType, scopeID, row, rx, tx)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s/%s in %+v", agentID, scopeType, scopeID, rows)
}

func assertTrafficBucketAt(t *testing.T, rows []TrafficBucketRow, agentID, scopeType, scopeID string, bucketStart time.Time, rx, tx uint64) {
	t.Helper()
	for _, row := range rows {
		if row.AgentID == agentID && row.ScopeType == scopeType && row.ScopeID == scopeID && row.BucketStart.Equal(bucketStart) {
			if row.RXBytes != rx || row.TXBytes != tx {
				t.Fatalf("%s/%s/%s at %s = %+v, want rx=%d tx=%d", agentID, scopeType, scopeID, bucketStart, row, rx, tx)
			}
			return
		}
	}
	t.Fatalf("missing %s/%s/%s at %s in %+v", agentID, scopeType, scopeID, bucketStart, rows)
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
