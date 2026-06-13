package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

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

func TestAccountedDeltaBytesByDirection(t *testing.T) {
	tests := []struct {
		direction              string
		currentRX, currentTX   uint64
		baselineRX, baselineTX uint64
		want                   uint64
	}{
		{"rx", 120, 500, 100, 200, 20},
		{"tx", 120, 500, 100, 200, 300},
		{"both", 120, 500, 100, 200, 320},
		{"max", 120, 500, 100, 200, 300},
		{"max", 6000, 5000, 1000, 5000, 1000},
		{"both", 50, 500, 100, 200, 300},
	}
	for _, tc := range tests {
		if got := accountedDeltaBytes(tc.direction, tc.currentRX, tc.currentTX, tc.baselineRX, tc.baselineTX); got != tc.want {
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

func TestTrafficQuotaNilMeansUnlimited(t *testing.T) {
	used := uint64(123)
	blocked, reason := quotaBlocked(used, TrafficPolicy{MonthlyQuotaBytes: nil, BlockWhenExceeded: true})
	if blocked || reason != "" {
		t.Fatalf("quotaBlocked() = %v, %q; want unlimited nil quota", blocked, reason)
	}
	if got := quotaPercent(used, nil); got != 0 {
		t.Fatalf("quotaPercent() = %v, want 0 for unlimited nil quota", got)
	}
	if got := quotaRemaining(used, nil); got != nil {
		t.Fatalf("quotaRemaining() = %v, want nil for unlimited nil quota", *got)
	}
}

func TestTrafficQuotaZeroIsRealQuota(t *testing.T) {
	quota := int64(0)

	blocked, reason := quotaBlocked(0, TrafficPolicy{MonthlyQuotaBytes: &quota, BlockWhenExceeded: true})
	if blocked || reason != "" {
		t.Fatalf("quotaBlocked(0) = %v, %q; want not blocked at zero usage", blocked, reason)
	}
	if got := quotaPercent(0, &quota); got != 0 {
		t.Fatalf("quotaPercent(0) = %v, want 0", got)
	}
	remaining := quotaRemaining(0, &quota)
	if remaining == nil || *remaining != 0 {
		t.Fatalf("quotaRemaining(0) = %v, want 0", remaining)
	}

	blocked, reason = quotaBlocked(1, TrafficPolicy{MonthlyQuotaBytes: &quota, BlockWhenExceeded: true})
	if !blocked || reason != "monthly quota exceeded" {
		t.Fatalf("quotaBlocked(1) = %v, %q; want monthly quota exceeded", blocked, reason)
	}
	if got := quotaPercent(1, &quota); got != 100 {
		t.Fatalf("quotaPercent(1) = %v, want 100 for positive usage over zero quota", got)
	}
	remaining = quotaRemaining(1, &quota)
	if remaining == nil || *remaining != -1 {
		t.Fatalf("quotaRemaining(1) = %v, want -1", remaining)
	}
}

func TestTrafficServiceIngestHeartbeatComputesDeltas(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fixedNow := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return fixedNow }}, fakeStore)
	stats := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100), "tx_bytes": float64(50)}}}

	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}

	if got := fakeStore.bucketRX("edge-1", "agent_total", ""); got != 100 {
		t.Fatalf("rx bytes = %d, want idempotent 100", got)
	}
	if got := fakeStore.bucketTX("edge-1", "agent_total", ""); got != 50 {
		t.Fatalf("tx bytes = %d, want idempotent 50", got)
	}
}

func TestTrafficServiceIngestHeartbeatParsesCurrentStatsShape(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fakeStore.httpRulesByAgent["edge-1"] = []storage.HTTPRuleRow{{ID: 11, AgentID: "edge-1"}}
	fakeStore.l4RulesByAgent["edge-1"] = []storage.L4RuleRow{{ID: 22, AgentID: "edge-1"}}
	fakeStore.relayListenersByAgent["edge-1"] = []storage.RelayListenerRow{{ID: 33, AgentID: "edge-1"}}
	fixedNow := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return fixedNow }}, fakeStore)
	stats := AgentStats{"traffic": map[string]any{
		"total":           map[string]any{"rx_bytes": uint64(10), "tx_bytes": uint64(20)},
		"http":            map[string]uint64{"rx_bytes": 30, "tx_bytes": 40},
		"l4":              map[string]any{"rx_bytes": int64(50), "tx_bytes": int64(60)},
		"relay":           map[string]any{"rx_bytes": jsonNumber("70"), "tx_bytes": jsonNumber("80")},
		"http_rules":      map[string]map[string]uint64{"11": {"rx_bytes": 90, "tx_bytes": 100}},
		"l4_rules":        map[string]any{"22": map[string]any{"rx_bytes": float64(110), "tx_bytes": float64(120)}},
		"relay_listeners": map[string]any{"33": map[string]any{"rx_bytes": "130", "tx_bytes": "140"}},
	}}

	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}

	assertBucket := func(scopeType, scopeID string, rx, tx uint64) {
		t.Helper()
		if got := fakeStore.bucketRX("edge-1", scopeType, scopeID); got != rx {
			t.Fatalf("%s/%s rx = %d, want %d", scopeType, scopeID, got, rx)
		}
		if got := fakeStore.bucketTX("edge-1", scopeType, scopeID); got != tx {
			t.Fatalf("%s/%s tx = %d, want %d", scopeType, scopeID, got, tx)
		}
	}
	assertBucket("agent_total", "", 10, 20)
	assertBucket("http", "", 30, 40)
	assertBucket("l4", "", 50, 60)
	assertBucket("relay", "", 70, 80)
	assertBucket("http_rule", "11", 90, 100)
	assertBucket("l4_rule", "22", 110, 120)
	assertBucket("relay_listener", "33", 130, 140)
}

func TestTrafficServiceIngestHeartbeatIgnoresDeletedScopedTraffic(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fixedNow := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return fixedNow }}, fakeStore)
	stats := AgentStats{"traffic": map[string]any{
		"http_rules":      map[string]any{"11": map[string]any{"rx_bytes": uint64(90), "tx_bytes": uint64(100)}},
		"l4_rules":        map[string]any{"22": map[string]any{"rx_bytes": uint64(110), "tx_bytes": uint64(120)}},
		"relay_listeners": map[string]any{"33": map[string]any{"rx_bytes": uint64(130), "tx_bytes": uint64(140)}},
	}}

	if err := svc.IngestHeartbeat(context.Background(), "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	if fakeStore.writeCount != 0 {
		t.Fatalf("writeCount = %d, want deleted scopes ignored", fakeStore.writeCount)
	}
	if len(fakeStore.cursors) != 0 || len(fakeStore.buckets) != 0 {
		t.Fatalf("traffic rows recreated for deleted scopes: cursors=%+v buckets=%+v", fakeStore.cursors, fakeStore.buckets)
	}
}

func TestTrafficServiceIngestHeartbeatUsesPreservedCursorForReusedRuleID(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	if err := store.SaveHTTPRules(ctx, "edge-1", []storage.HTTPRuleRow{{ID: 1, AgentID: "edge-1"}}); err != nil {
		t.Fatal(err)
	}
	stats := AgentStats{"traffic": map[string]any{
		"http_rules": map[string]any{"1": map[string]any{"rx_bytes": uint64(100), "tx_bytes": uint64(200)}},
	}}
	if err := svc.IngestHeartbeat(ctx, "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteTrafficByScope(ctx, "edge-1", "http_rule", "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveHTTPRules(ctx, "edge-1", []storage.HTTPRuleRow{{ID: 1, AgentID: "edge-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestHeartbeat(ctx, "edge-1", stats); err != nil {
		t.Fatal(err)
	}

	rows, err := store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		ScopeID:     "1",
		Granularity: "hour",
		From:        now.Add(-time.Hour),
		To:          now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want no old cumulative traffic attributed to reused rule ID", rows)
	}
}

func TestTrafficServiceIngestHeartbeatParsesHostTrafficStats(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fixedNow := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return fixedNow }}, fakeStore)
	first := AgentStats{"traffic": map[string]any{
		"host": map[string]any{
			"total":      map[string]any{"rx_bytes": uint64(1000), "tx_bytes": uint64(2000)},
			"interfaces": map[string]any{"eth0": map[string]any{"rx_bytes": uint64(900), "tx_bytes": uint64(1800)}},
		},
	}}
	second := AgentStats{"traffic": map[string]any{
		"host": map[string]any{
			"total":      map[string]any{"rx_bytes": uint64(1200), "tx_bytes": uint64(2300)},
			"interfaces": map[string]any{"eth0": map[string]any{"rx_bytes": uint64(950), "tx_bytes": uint64(1850)}},
		},
	}}

	if err := svc.IngestHeartbeat(context.Background(), "edge-1", first); err != nil {
		t.Fatal(err)
	}
	if got := fakeStore.bucketRX("edge-1", "host_total", ""); got != 0 {
		t.Fatalf("first host_total rx = %d, want initial baseline only", got)
	}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", second); err != nil {
		t.Fatal(err)
	}

	if got := fakeStore.bucketRX("edge-1", "host_total", ""); got != 200 {
		t.Fatalf("host_total rx = %d, want 200", got)
	}
	if got := fakeStore.bucketTX("edge-1", "host_total", ""); got != 300 {
		t.Fatalf("host_total tx = %d, want 300", got)
	}
	if got := fakeStore.bucketRX("edge-1", "host_interface", "eth0"); got != 50 {
		t.Fatalf("host_interface eth0 rx = %d, want 50", got)
	}
}

func TestTrafficServiceIngestHeartbeatDailySummaryDefaultsToUTC(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2026, 5, 5, 1, 30, 0, 0, loc)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	stats := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": uint64(100), "tx_bytes": uint64(50)}}}
	if err := svc.IngestHeartbeat(ctx, "edge-1", stats); err != nil {
		t.Fatal(err)
	}

	points, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBucketStart := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if len(points) != 1 || points[0].BucketStart != wantBucketStart || points[0].RXBytes != 100 || points[0].TXBytes != 50 {
		t.Fatalf("points = %+v, want one UTC-day bucket at %s", points, wantBucketStart)
	}
}

func TestTrafficServiceIngestHeartbeatDailySummaryUsesConfiguredTimezone(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 4, 17, 30, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return now },
		Timezone: shanghai,
	}, store)

	stats := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": uint64(100), "tx_bytes": uint64(50)}}}
	if err := svc.IngestHeartbeat(ctx, "edge-1", stats); err != nil {
		t.Fatal(err)
	}

	points, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBucketStart := time.Date(2026, 5, 5, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339)
	wantBucketLocalStart := time.Date(2026, 5, 5, 0, 0, 0, 0, shanghai).Format(time.RFC3339)
	if len(points) != 1 || points[0].BucketStart != wantBucketStart || points[0].BucketLocalStart != wantBucketLocalStart || points[0].RXBytes != 100 || points[0].TXBytes != 50 {
		t.Fatalf("points = %+v, want one configured-timezone bucket at %s", points, wantBucketStart)
	}
}

func TestTrafficServiceTrendDateFiltersUseConfiguredTimezone(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Timezone: shanghai,
	}, store)

	stats := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": uint64(100), "tx_bytes": uint64(50)}}}
	svc.now = func() time.Time { return time.Date(2026, 5, 4, 17, 30, 0, 0, time.UTC) }
	if err := svc.IngestHeartbeat(ctx, "edge-1", stats); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Date(2026, 5, 5, 17, 30, 0, 0, time.UTC) }
	if err := svc.IngestHeartbeat(ctx, "edge-1", AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": uint64(120), "tx_bytes": uint64(60)}}}); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Date(2026, 6, 1, 17, 30, 0, 0, time.UTC) }
	if err := svc.IngestHeartbeat(ctx, "edge-1", AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": uint64(130), "tx_bytes": uint64(70)}}}); err != nil {
		t.Fatal(err)
	}

	dailyPoints, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        "2026-05-05T00:00:00Z",
		To:          "2026-05-06T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDailyBucketStart := time.Date(2026, 5, 5, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339)
	if len(dailyPoints) != 1 || dailyPoints[0].BucketStart != wantDailyBucketStart {
		t.Fatalf("daily points = %+v, want local May 5 bucket at %s", dailyPoints, wantDailyBucketStart)
	}

	dailyEndOfDayPoints, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        "2026-05-05T00:00:00Z",
		To:          "2026-05-05T23:59:59.999Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dailyEndOfDayPoints) != 1 || dailyEndOfDayPoints[0].BucketStart != wantDailyBucketStart {
		t.Fatalf("daily end-of-day points = %+v, want local May 5 bucket at %s", dailyEndOfDayPoints, wantDailyBucketStart)
	}

	monthlyPoints, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-05-01T00:00:00Z",
		To:          "2026-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMonthlyBucketStart := time.Date(2026, 5, 1, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339)
	if len(monthlyPoints) != 1 || monthlyPoints[0].BucketStart != wantMonthlyBucketStart {
		t.Fatalf("monthly points = %+v, want local May bucket at %s", monthlyPoints, wantMonthlyBucketStart)
	}
}

func TestTrafficServiceTrendMonthUsesConfiguredTimezoneCycleStart(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "rx",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, shanghai), RXBytes: 100, TXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai), RXBytes: 200, TXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC) },
		Timezone: shanghai,
	}, store)

	points, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-04-15T00:00:00+08:00",
		To:          "2026-06-15T00:00:00+08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []string{
		time.Date(2026, 4, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
		time.Date(2026, 5, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
	}
	if len(points) != 2 {
		t.Fatalf("points = %+v, want two cycle-month buckets", points)
	}
	if points[0].BucketStart != wantStarts[0] || points[0].BucketLocalStart != "2026-04-15T00:00:00+08:00" || points[0].RXBytes != 100 || points[0].AccountedBytes != 100 {
		t.Fatalf("points[0] = %+v, want Apr 15 local cycle bucket", points[0])
	}
	if points[1].BucketStart != wantStarts[1] || points[1].BucketLocalStart != "2026-05-15T00:00:00+08:00" || points[1].RXBytes != 200 || points[1].AccountedBytes != 200 {
		t.Fatalf("points[1] = %+v, want May 15 local cycle bucket", points[1])
	}
}

func TestTrafficServiceTrendMonthDefaultWindowUsesSixPolicyCycles(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "rx",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2025, 11, 20, 10, 0, 0, 0, shanghai), RXBytes: 1},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2025, 12, 20, 10, 0, 0, 0, shanghai), RXBytes: 2},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai), RXBytes: 3},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 6, 20, 10, 0, 0, 0, shanghai), RXBytes: 4},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) },
		Timezone: shanghai,
	}, store)

	points, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []string{
		time.Date(2025, 12, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
		time.Date(2026, 5, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
	}
	if len(points) != 2 {
		t.Fatalf("points = %+v, want exactly the six-cycle window buckets with data", points)
	}
	if points[0].BucketStart != wantStarts[0] || points[0].RXBytes != 2 {
		t.Fatalf("points[0] = %+v, want Dec 15 cycle bucket", points[0])
	}
	if points[1].BucketStart != wantStarts[1] || points[1].RXBytes != 3 {
		t.Fatalf("points[1] = %+v, want May 15 cycle bucket", points[1])
	}
}

func TestTrafficServiceTrendAppliesDefaultLookbackWindow(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 34, 0, 0, time.UTC)
	tests := []struct {
		name        string
		granularity string
		rows        []storage.TrafficDelta
		wantStarts  []string
	}{
		{
			name:        "hour uses recent 24 hours",
			granularity: "hour",
			rows: []storage.TrafficDelta{
				{BucketStart: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC), RXBytes: 1},
				{BucketStart: time.Date(2026, 5, 19, 13, 0, 0, 0, time.UTC), RXBytes: 2},
				{BucketStart: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), RXBytes: 3},
				{BucketStart: time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC), RXBytes: 4},
			},
			wantStarts: []string{"2026-05-19T13:00:00Z", "2026-05-20T12:00:00Z"},
		},
		{
			name:        "day uses recent 7 days",
			granularity: "day",
			rows: []storage.TrafficDelta{
				{BucketStart: time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC), RXBytes: 1},
				{BucketStart: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), RXBytes: 2},
				{BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 3},
				{BucketStart: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC), RXBytes: 4},
			},
			wantStarts: []string{"2026-05-14T00:00:00Z", "2026-05-20T00:00:00Z"},
		},
		{
			name:        "month uses recent 6 months",
			granularity: "month",
			rows: []storage.TrafficDelta{
				{BucketStart: time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), RXBytes: 1},
				{BucketStart: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), RXBytes: 2},
				{BucketStart: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), RXBytes: 3},
				{BucketStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), RXBytes: 4},
			},
			wantStarts: []string{"2025-12-01T00:00:00Z", "2026-05-01T00:00:00Z"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newTrafficServiceRealStore(t)
			ctx := context.Background()
			if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
				AgentID:       "edge-1",
				Direction:     "rx",
				CycleStartDay: 1,
			}); err != nil {
				t.Fatal(err)
			}
			for _, row := range tc.rows {
				row.AgentID = "edge-1"
				row.ScopeType = "agent_total"
				if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
					t.Fatal(err)
				}
			}
			svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

			points, err := svc.Trend(ctx, TrafficTrendQuery{
				AgentID:     "edge-1",
				ScopeType:   "agent_total",
				Granularity: tc.granularity,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(points) != len(tc.wantStarts) {
				t.Fatalf("points = %+v, want starts %v", points, tc.wantStarts)
			}
			for i, want := range tc.wantStarts {
				if points[i].BucketStart != want {
					t.Fatalf("points[%d].BucketStart = %q, want %q; points = %+v", i, points[i].BucketStart, want, points)
				}
			}
		})
	}
}

func TestTrafficServiceOverviewTrendUsesDefaultLookbackWindow(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 34, 0, 0, time.UTC)
	for _, agentID := range []string{"edge-1", "edge-2"} {
		if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
			AgentID:       agentID,
			Direction:     "rx",
			CycleStartDay: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC), RXBytes: 100},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-2", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), RXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	overview, err := svc.Overview(ctx, "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Trend) != 1 {
		t.Fatalf("overview trend = %+v, want one in-window aggregate", overview.Trend)
	}
	if overview.Trend[0].BucketStart != "2026-05-14T00:00:00Z" || overview.Trend[0].RXBytes != 30 {
		t.Fatalf("overview trend[0] = %+v, want May 14 rx aggregate 30", overview.Trend[0])
	}
}

func TestTrafficServiceOverviewTrendFallsBackWhenHostTotalOnlyOutsideDefaultWindow(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 34, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "rx",
		CycleStartDay: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC), RXBytes: 100},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 25},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	overview, err := svc.Overview(ctx, "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Trend) != 1 {
		t.Fatalf("overview trend = %+v, want recent agent_total fallback bucket", overview.Trend)
	}
	if overview.Trend[0].BucketStart != "2026-05-20T00:00:00Z" ||
		overview.Trend[0].RXBytes != 25 {
		t.Fatalf("overview trend[0] = %+v, want recent agent_total fallback bucket", overview.Trend[0])
	}
}

func TestTrafficServiceIngestHeartbeatParsesHostBootID(t *testing.T) {
	samples := parseHeartbeatTrafficStats(AgentStats{"traffic": map[string]any{
		"host": map[string]any{
			"boot_id": "boot-123",
			"total":   map[string]any{"rx_bytes": uint64(1000), "tx_bytes": uint64(2000)},
			"interfaces": map[string]any{
				"eth0": map[string]any{"rx_bytes": uint64(900), "tx_bytes": uint64(1800)},
			},
		},
	}})

	for _, sample := range samples {
		if sample.scopeType == "host_total" || sample.scopeType == "host_interface" {
			if sample.bootID != "boot-123" {
				t.Fatalf("%s bootID = %q, want boot-123", sample.scopeType, sample.bootID)
			}
		}
	}
}

func TestTrafficServiceSummaryUsesHostTotalForQuotaWhenAvailable(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     1,
		TXBytes:     2,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 100 || summary.TXBytes != 200 || summary.UsedBytes != 300 {
		t.Fatalf("summary uses %+v, want host_total rx=100 tx=200 used=300", summary)
	}
}

func TestTrafficServiceSummaryPreservesExistingCycleAgentTotalDuringHostRollout(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     10,
		TXBytes:     20,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 110 || summary.TXBytes != 220 || summary.UsedBytes != 330 {
		t.Fatalf("summary uses %+v, want agent_total before host rollout plus host_total after rollout", summary)
	}
}

func TestTrafficServiceSummaryPreservesSameMonthAgentTotalDuringHostRollout(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10, TXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 110 || summary.TXBytes != 220 || summary.UsedBytes != 330 {
		t.Fatalf("summary uses %+v, want same-month agent_total before host rollout plus host_total after rollout", summary)
	}
}

func TestTrafficServiceSummaryFallsBackToAgentTotalWhenHostTotalOnlyOutsideCycle(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     1,
		TXBytes:     2,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 1 || summary.TXBytes != 2 || summary.UsedBytes != 3 {
		t.Fatalf("summary uses %+v, want current-cycle agent_total fallback", summary)
	}
}

func TestTrafficServiceTrendDefaultsToHostTotalAtRequestedGranularity(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	fakeStore.emptyTrends = []storage.TrafficTrendQuery{{
		AgentID:     "edge-1",
		ScopeType:   "host_total",
		Granularity: "hour",
	}}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	points, err := svc.Trend(context.Background(), TrafficTrendQuery{
		AgentID:     "edge-1",
		Granularity: "day",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].ScopeType != "host_total" || points[0].RXBytes != 100 || points[0].TXBytes != 200 {
		t.Fatalf("Trend() = %+v, want host_total daily row", points)
	}
}

func TestTrafficServiceIngestHeartbeatCounterResetRecordsNonNegativeDelta(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fixedNow := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return fixedNow }}, fakeStore)

	first := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100), "tx_bytes": float64(80)}}}
	reset := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(7), "tx_bytes": float64(9)}}}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", first); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestHeartbeat(context.Background(), "edge-1", reset); err != nil {
		t.Fatal(err)
	}

	if got := fakeStore.bucketRX("edge-1", "agent_total", ""); got != 107 {
		t.Fatalf("rx bytes = %d, want reset delta counted from zero", got)
	}
	if got := fakeStore.bucketTX("edge-1", "agent_total", ""); got != 89 {
		t.Fatalf("tx bytes = %d, want reset delta counted from zero", got)
	}
	if len(fakeStore.events) != 1 || fakeStore.events[0].EventType != "counter_reset" {
		t.Fatalf("events = %+v, want counter_reset", fakeStore.events)
	}
}

func TestTrafficServiceDisabledIgnoresHeartbeat(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	svc := NewTrafficService(TrafficServiceConfig{Enabled: false}, fakeStore)
	err := svc.IngestHeartbeat(context.Background(), "edge-1", AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100)}}})
	if err != nil {
		t.Fatal(err)
	}
	if fakeStore.writeCount != 0 {
		t.Fatalf("writeCount = %d", fakeStore.writeCount)
	}
}

func TestTrafficServiceDisabledMethodsReturnStableError(t *testing.T) {
	svc := NewTrafficService(TrafficServiceConfig{Enabled: false}, newFakeTrafficStore())

	_, err := svc.Summary(context.Background(), "edge-1")
	if !errors.Is(err, ErrTrafficStatsDisabled) {
		t.Fatalf("Summary() error = %v, want ErrTrafficStatsDisabled", err)
	}
	var serviceErr TrafficServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != ErrCodeTrafficStatsDisabled {
		t.Fatalf("Summary() error = %v, want code %s", err, ErrCodeTrafficStatsDisabled)
	}
}

func TestTrafficServiceUpdatePolicyValidatesAndNormalizes(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, fakeStore)
	quota := int64(1024)
	monthlyRetention := 36

	policy, err := svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{
		Direction:              "RX",
		CycleStartDay:          15,
		MonthlyQuotaBytes:      &quota,
		BlockWhenExceeded:      true,
		HourlyRetentionDays:    7,
		DailyRetentionMonths:   3,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.AgentID != "edge-1" || policy.Direction != "rx" || policy.CycleStartDay != 15 || policy.MonthlyQuotaBytes == nil || *policy.MonthlyQuotaBytes != quota {
		t.Fatalf("policy = %+v", policy)
	}

	_, err = svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{Direction: "sideways", CycleStartDay: 1})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "direction") {
		t.Fatalf("UpdatePolicy() error = %v, want direction validation", err)
	}
	_, err = svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{Direction: "rx", CycleStartDay: 29})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "cycle_start_day") {
		t.Fatalf("UpdatePolicy() error = %v, want cycle_start_day validation", err)
	}

	negativeMonthlyRetention := -1
	for _, tc := range []struct {
		name   string
		policy TrafficPolicy
	}{
		{name: "hourly_retention_days", policy: TrafficPolicy{Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: -1}},
		{name: "daily_retention_months", policy: TrafficPolicy{Direction: "rx", CycleStartDay: 1, DailyRetentionMonths: -1}},
		{name: "monthly_retention_months", policy: TrafficPolicy{Direction: "rx", CycleStartDay: 1, MonthlyRetentionMonths: &negativeMonthlyRetention}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdatePolicy(context.Background(), "edge-1", tc.policy)
			if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("UpdatePolicy() error = %v, want %s validation", err, tc.name)
			}
		})
	}
}

func TestTrafficServiceUpdatePolicyPreservesNilMonthlyRetention(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, fakeStore)

	policy, err := svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   3,
		BlockWhenExceeded:      false,
		MonthlyQuotaBytes:      nil,
		MonthlyRetentionMonths: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.MonthlyRetentionMonths != nil {
		t.Fatalf("policy.MonthlyRetentionMonths = %v, want nil", *policy.MonthlyRetentionMonths)
	}
	if fakeStore.policy.MonthlyRetentionMonths != nil {
		t.Fatalf("stored MonthlyRetentionMonths = %v, want nil", *fakeStore.policy.MonthlyRetentionMonths)
	}
}

func TestTrafficServiceUpdatePolicyRecomputesPersistedBlockState(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     75,
		TXBytes:     50,
	})
	quota := int64(100)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	_, err := svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{
		Direction:         "both",
		CycleStartDay:     1,
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !fakeStore.agentTrafficBlocked["edge-1"] || fakeStore.agentTrafficBlockReason["edge-1"] != "monthly quota exceeded" {
		t.Fatalf("persisted block state = %v %q", fakeStore.agentTrafficBlocked["edge-1"], fakeStore.agentTrafficBlockReason["edge-1"])
	}
	if len(fakeStore.events) != 1 || fakeStore.events[0].EventType != "traffic_block_state_changed" {
		t.Fatalf("events = %+v, want traffic_block_state_changed", fakeStore.events)
	}
}

func TestTrafficServiceSummaryUsesCycleBaselineAndQuota(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	quota := int64(1000)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:           "edge-1",
		Direction:         "both",
		CycleStartDay:     15,
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: true,
	}
	fakeStore.baselines["edge-1|2026-05-15T00:00:00Z"] = storage.AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        "2026-05-15T00:00:00Z",
		RawRXBytes:        10,
		RawTXBytes:        15,
		RawAccountedBytes: 25,
		AdjustUsedBytes:   10,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     200,
		TXBytes:     300,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 485 || summary.MonthlyQuotaBytes == nil || *summary.MonthlyQuotaBytes != quota || summary.QuotaPercent != 48.5 || summary.Blocked {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.CycleStart != "2026-05-15T00:00:00Z" || summary.CycleEnd != "2026-06-15T00:00:00Z" {
		t.Fatalf("cycle = %s..%s", summary.CycleStart, summary.CycleEnd)
	}
}

func TestTrafficServiceSummaryRecomputesBaselineForDirectionChange(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "rx",
		CycleStartDay: 1,
	}
	fakeStore.baselines["edge-1|2026-05-01T00:00:00Z"] = storage.AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        "2026-05-01T00:00:00Z",
		RawRXBytes:        100,
		RawTXBytes:        300,
		RawAccountedBytes: 400,
		AdjustUsedBytes:   10,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     250,
		TXBytes:     700,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 160 {
		t.Fatalf("UsedBytes = %d, want current rx 250 - baseline rx 100 + adjust 10", summary.UsedBytes)
	}
}

func TestTrafficServiceBlockStateSkipsSummaryWhenPolicyCannotBlock(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, fakeStore)

	blocked, reason, err := svc.BlockState(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if blocked || reason != "" {
		t.Fatalf("BlockState() = blocked %v reason %q, want unblocked", blocked, reason)
	}
	if fakeStore.baselineReadCount != 0 {
		t.Fatalf("baseline reads = %d, want 0", fakeStore.baselineReadCount)
	}
	if fakeStore.trendReadCount != 0 {
		t.Fatalf("trend reads = %d, want 0", fakeStore.trendReadCount)
	}
}

func TestTrafficServiceBlockStateSkipsSummaryWhenBlockingDisabled(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	quota := int64(100)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		MonthlyQuotaBytes:    &quota,
		BlockWhenExceeded:    false,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, fakeStore)

	blocked, reason, err := svc.BlockState(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if blocked || reason != "" {
		t.Fatalf("BlockState() = blocked %v reason %q, want unblocked", blocked, reason)
	}
	if fakeStore.baselineReadCount != 0 {
		t.Fatalf("baseline reads = %d, want 0", fakeStore.baselineReadCount)
	}
	if fakeStore.trendReadCount != 0 {
		t.Fatalf("trend reads = %d, want 0", fakeStore.trendReadCount)
	}
}

func TestTrafficServiceBlockStateUsesSummaryWhenBlockingCanApply(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	quota := int64(100)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		MonthlyQuotaBytes:    &quota,
		BlockWhenExceeded:    true,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     80,
		TXBytes:     30,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	blocked, reason, err := svc.BlockState(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !blocked || reason != "monthly quota exceeded" {
		t.Fatalf("BlockState() = blocked %v reason %q, want monthly quota exceeded", blocked, reason)
	}
	if fakeStore.baselineReadCount == 0 {
		t.Fatal("baseline reads = 0, want summary path to check current cycle baseline")
	}
	if fakeStore.trendReadCount == 0 {
		t.Fatal("trend reads = 0, want summary path to check current usage")
	}
}

func TestTrafficServiceSummaryReportsOverQuotaWithoutBlocking(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	quota := int64(100)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:           "edge-1",
		Direction:         "both",
		CycleStartDay:     1,
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: false,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     75,
		TXBytes:     50,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !summary.OverQuota || summary.Blocked {
		t.Fatalf("OverQuota=%v Blocked=%v, want over quota without blocking", summary.OverQuota, summary.Blocked)
	}
}

func TestTrafficServiceSummaryReportsOverQuotaWithBlocking(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	quota := int64(100)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:           "edge-1",
		Direction:         "both",
		CycleStartDay:     1,
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: true,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     75,
		TXBytes:     50,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !summary.OverQuota || !summary.Blocked {
		t.Fatalf("OverQuota=%v Blocked=%v, want over quota with blocking", summary.OverQuota, summary.Blocked)
	}
}

func TestTrafficServiceSummaryIncludesBreakdowns(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "max",
		CycleStartDay: 1,
	}
	for _, row := range []storage.TrafficBucketRow{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 1, TXBytes: 2},
		{AgentID: "edge-1", ScopeType: "http", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 40},
		{AgentID: "edge-1", ScopeType: "l4", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 50, TXBytes: 70},
		{AgentID: "edge-1", ScopeType: "relay", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 5, TXBytes: 9},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 1, TXBytes: 7},
		{AgentID: "edge-1", ScopeType: "l4_rule", ScopeID: "22", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 8, TXBytes: 3},
		{AgentID: "edge-1", ScopeType: "relay_listener", ScopeID: "33", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 4, TXBytes: 6},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 1000, TXBytes: 700},
		{AgentID: "edge-1", ScopeType: "host_interface", ScopeID: "eth0", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 900, TXBytes: 650},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSummaryBreakdown(t, summary.Aggregates, "http", "", 100, 40, 100)
	assertSummaryBreakdown(t, summary.Aggregates, "l4", "", 50, 70, 70)
	assertSummaryBreakdown(t, summary.Aggregates, "relay", "", 5, 9, 9)
	assertSummaryBreakdown(t, summary.HTTPRules, "http_rule", "11", 1, 7, 7)
	assertSummaryBreakdown(t, summary.L4Rules, "l4_rule", "22", 8, 3, 8)
	assertSummaryBreakdown(t, summary.RelayListeners, "relay_listener", "33", 4, 6, 6)
	if summary.HostTotal.ScopeType != "host_total" || summary.HostTotal.RXBytes != 1000 || summary.HostTotal.TXBytes != 700 || summary.HostTotal.AccountedBytes != 1000 {
		t.Fatalf("HostTotal = %+v, want host_total rx=1000 tx=700 accounted=1000", summary.HostTotal)
	}
	assertSummaryBreakdown(t, summary.HostInterfaces, "host_interface", "eth0", 900, 650, 900)
}

func TestTrafficServiceSummaryIncludesObjectBreakdownsWithRealStore(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	store := newTrafficServiceRealStore(t, dataRoot)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "max",
		CycleStartDay: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: now, RXBytes: 1, TXBytes: 2},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: now, RXBytes: 10, TXBytes: 40},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "12", BucketStart: now, RXBytes: 50, TXBytes: 20},
		{AgentID: "edge-1", ScopeType: "l4_rule", ScopeID: "22", BucketStart: now, RXBytes: 7, TXBytes: 9},
		{AgentID: "edge-1", ScopeType: "relay_listener", ScopeID: "33", BucketStart: now, RXBytes: 8, TXBytes: 3},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	assertSummaryBreakdown(t, summary.HTTPRules, "http_rule", "11", 10, 40, 40)
	assertSummaryBreakdown(t, summary.HTTPRules, "http_rule", "12", 50, 20, 50)
	assertSummaryBreakdown(t, summary.L4Rules, "l4_rule", "22", 7, 9, 9)
	assertSummaryBreakdown(t, summary.RelayListeners, "relay_listener", "33", 8, 3, 8)
}

func TestTrafficServiceOverviewAggregatesHostTrend(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-1", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-2", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	firstDay := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	for _, row := range []storage.TrafficBucketRow{
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: firstDay, RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-2", ScopeType: "host_total", BucketStart: firstDay, RXBytes: 50, TXBytes: 75},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	overview, err := svc.Overview(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.HostTrend) != 1 {
		t.Fatalf("HostTrend = %+v, want one aggregated host bucket", overview.HostTrend)
	}
	if overview.HostTrend[0].BucketStart != "2026-05-19T00:00:00Z" ||
		overview.HostTrend[0].RXBytes != 150 ||
		overview.HostTrend[0].TXBytes != 275 ||
		overview.HostTrend[0].AccountedBytes != 425 {
		t.Fatalf("HostTrend[0] = %+v, want first day aggregate", overview.HostTrend[0])
	}
}

func TestTrafficServiceOverviewIncludesCycleWindow(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "rx",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "l4_rule",
		ScopeID:     "old",
		BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     500,
		TXBytes:     600,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	overview, err := svc.Overview(context.Background(), "edge-1", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Agents) != 1 {
		t.Fatalf("Agents = %+v, want one agent", overview.Agents)
	}
	if overview.Agents[0].CycleStart != "2026-05-15T00:00:00Z" || overview.Agents[0].CycleEnd != "2026-06-15T00:00:00Z" {
		t.Fatalf("overview agent cycle = %q..%q", overview.Agents[0].CycleStart, overview.Agents[0].CycleEnd)
	}
}

func TestTrafficServiceAggregateTopRulesExposeAgentIdentity(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-1", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-2", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	for _, row := range []storage.TrafficBucketRow{
		{
			AgentID:     "edge-1",
			ScopeType:   "http_rule",
			ScopeID:     "1",
			BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
			RXBytes:     100,
			TXBytes:     200,
		},
		{
			AgentID:     "edge-2",
			ScopeType:   "http_rule",
			ScopeID:     "1",
			BucketStart: time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
			RXBytes:     300,
			TXBytes:     400,
		},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	aggregate, err := svc.Aggregate(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopRules) != 2 {
		t.Fatalf("TopRules = %+v, want two overlapping rule rows", aggregate.TopRules)
	}
	seenKeys := map[string]bool{}
	seenAgents := map[string]bool{}
	for _, rule := range aggregate.TopRules {
		if rule.AgentID == "" {
			t.Fatalf("TopRules missing agent_id: %+v", aggregate.TopRules)
		}
		if rule.Key == "" {
			t.Fatalf("TopRules missing key: %+v", aggregate.TopRules)
		}
		if seenKeys[rule.Key] {
			t.Fatalf("duplicate aggregate rule key %q in %+v", rule.Key, aggregate.TopRules)
		}
		seenKeys[rule.Key] = true
		seenAgents[rule.AgentID] = true
	}
	if !seenAgents["edge-1"] || !seenAgents["edge-2"] {
		t.Fatalf("TopRules agents = %+v, want edge-1 and edge-2", aggregate.TopRules)
	}
	if fakeStore.breakdownReadCount > 1 {
		t.Fatalf("ListTrafficBreakdown calls = %d, want one aggregate top-rule query", fakeStore.breakdownReadCount)
	}
	if fakeStore.trendReadCount > 12 {
		t.Fatalf("ListTrafficTrend calls = %d, want aggregate endpoint to skip unused host trend", fakeStore.trendReadCount)
	}
}

func TestTrafficServiceAggregateUsesBatchedGlobalTrend(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-host", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-agent", Direction: "tx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	for _, row := range []storage.TrafficBucketRow{
		{AgentID: "edge-host", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 200},
		{AgentID: "edge-host", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 999, TXBytes: 999},
		{AgentID: "edge-agent", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 30, TXBytes: 40},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	aggregate, err := svc.Aggregate(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Trend) != 1 {
		t.Fatalf("Trend = %+v, want one merged bucket", aggregate.Trend)
	}
	if aggregate.Trend[0].RXBytes != 130 || aggregate.Trend[0].TXBytes != 240 || aggregate.Trend[0].AccountedBytes != 140 {
		t.Fatalf("Trend[0] = %+v, want host-total rx plus agent-total tx accounted by policy", aggregate.Trend[0])
	}
	if fakeStore.aggregateTrendReadCount != 1 {
		t.Fatalf("ListTrafficTrendByScopeTypes calls = %d, want one batched aggregate trend query", fakeStore.aggregateTrendReadCount)
	}
}

func TestTrafficServiceAggregateTopRulesReturnsTopTen(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "rx",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	for i := 1; i <= 12; i++ {
		fakeStore.addBucket(storage.TrafficBucketRow{
			AgentID:     "edge-1",
			ScopeType:   "http_rule",
			ScopeID:     strconv.Itoa(i),
			BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
			RXBytes:     uint64(130 - i),
		})
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	aggregate, err := svc.Aggregate(context.Background(), "edge-1", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopRules) != 10 {
		t.Fatalf("TopRules length = %d, want 10; rows = %+v", len(aggregate.TopRules), aggregate.TopRules)
	}
	if aggregate.TopRules[0].ScopeID != "1" || aggregate.TopRules[9].ScopeID != "10" {
		t.Fatalf("TopRules = %+v, want top ten rules sorted by accounted bytes", aggregate.TopRules)
	}
}

func TestTrafficServiceAggregateTopListsFollowGranularityWindow(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-old", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-recent", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	for _, row := range []storage.TrafficBucketRow{
		{AgentID: "edge-old", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC), RXBytes: 5000, TXBytes: 0},
		{AgentID: "edge-old", ScopeType: "http_rule", ScopeID: "old", BucketStart: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC), RXBytes: 5000, TXBytes: 0},
		{AgentID: "edge-recent", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 0},
		{AgentID: "edge-recent", ScopeType: "http_rule", ScopeID: "recent", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 0},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	daily, err := svc.Aggregate(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily.TopNodes) != 1 || daily.TopNodes[0].AgentID != "edge-recent" || daily.TopNodes[0].UsedBytes != 100 {
		t.Fatalf("daily TopNodes = %+v, want only recent window node", daily.TopNodes)
	}
	if len(daily.TopRules) != 1 || daily.TopRules[0].ScopeID != "recent" || daily.TopRules[0].AccountedBytes != 100 {
		t.Fatalf("daily TopRules = %+v, want only recent window rule", daily.TopRules)
	}

	monthly, err := svc.Aggregate(context.Background(), "", "month", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(monthly.TopNodes) < 2 || monthly.TopNodes[0].AgentID != "edge-old" || monthly.TopNodes[0].UsedBytes != 5000 {
		t.Fatalf("monthly TopNodes = %+v, want old high-usage node first", monthly.TopNodes)
	}
	if len(monthly.TopRules) < 2 || monthly.TopRules[0].ScopeID != "old" || monthly.TopRules[0].AccountedBytes != 5000 {
		t.Fatalf("monthly TopRules = %+v, want old high-usage rule first", monthly.TopRules)
	}
}

func TestTrafficServiceAggregateMonthUsesConfiguredTimezoneCycleStart(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "rx",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, shanghai), RXBytes: 5000},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "old-cycle", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, shanghai), RXBytes: 5000},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai), RXBytes: 100},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "current-cycle", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai), RXBytes: 100},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC) },
		Timezone: shanghai,
	}, store)

	aggregate, err := svc.Aggregate(ctx, "edge-1", "month", nil)
	if err != nil {
		t.Fatal(err)
	}
	wantBucketStarts := []string{
		time.Date(2026, 4, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
		time.Date(2026, 5, 15, 0, 0, 0, 0, shanghai).UTC().Format(time.RFC3339),
	}
	if len(aggregate.Trend) != 2 || aggregate.Trend[0].BucketStart != wantBucketStarts[0] || aggregate.Trend[0].RXBytes != 5000 || aggregate.Trend[1].BucketStart != wantBucketStarts[1] || aggregate.Trend[1].RXBytes != 100 {
		t.Fatalf("Trend = %+v, want cycle-month buckets at %v", aggregate.Trend, wantBucketStarts)
	}
	if len(aggregate.TopNodes) != 1 || aggregate.TopNodes[0].AgentID != "edge-1" || aggregate.TopNodes[0].UsedBytes != 5100 {
		t.Fatalf("TopNodes = %+v, want default monthly window usage across cycle buckets", aggregate.TopNodes)
	}
	if len(aggregate.TopRules) != 2 || aggregate.TopRules[0].ScopeID != "old-cycle" || aggregate.TopRules[0].AccountedBytes != 5000 || aggregate.TopRules[1].ScopeID != "current-cycle" || aggregate.TopRules[1].AccountedBytes != 100 {
		t.Fatalf("TopRules = %+v, want default monthly window rules across cycle buckets", aggregate.TopRules)
	}
}

func TestTrafficServiceAggregateTopNodesPreserveLegacyAgentTotalDuringHostRollout(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-rollout", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-current", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	}
	for _, row := range []storage.TrafficBucketRow{
		{AgentID: "edge-rollout", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 0},
		{AgentID: "edge-rollout", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 20, TXBytes: 0},
		{AgentID: "edge-current", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC), RXBytes: 80, TXBytes: 0},
	} {
		fakeStore.addBucket(row)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	aggregate, err := svc.Aggregate(context.Background(), "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopNodes) < 2 {
		t.Fatalf("TopNodes = %+v, want rollout and current agents", aggregate.TopNodes)
	}
	if aggregate.TopNodes[0].AgentID != "edge-rollout" || aggregate.TopNodes[0].UsedBytes != 120 {
		t.Fatalf("TopNodes = %+v, want rollout agent first with legacy+host bytes", aggregate.TopNodes)
	}
	if aggregate.TopNodes[1].AgentID != "edge-current" || aggregate.TopNodes[1].UsedBytes != 80 {
		t.Fatalf("TopNodes = %+v, want current host-only agent second", aggregate.TopNodes)
	}
}

func TestTrafficServiceAggregateTopNodesPreserveSameDayLegacyAgentTotalDuringHostRollout(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	for _, policy := range []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-rollout", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-current", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	} {
		if err := store.SaveTrafficPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-rollout", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 0},
		{AgentID: "edge-rollout", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), RXBytes: 20, TXBytes: 0},
		{AgentID: "edge-current", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), RXBytes: 80, TXBytes: 0},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	aggregate, err := svc.Aggregate(ctx, "", "day", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopNodes) < 2 {
		t.Fatalf("TopNodes = %+v, want rollout and current agents", aggregate.TopNodes)
	}
	if aggregate.TopNodes[0].AgentID != "edge-rollout" || aggregate.TopNodes[0].UsedBytes != 120 {
		t.Fatalf("TopNodes = %+v, want rollout agent first with same-day legacy+host bytes", aggregate.TopNodes)
	}
	if aggregate.TopNodes[1].AgentID != "edge-current" || aggregate.TopNodes[1].UsedBytes != 80 {
		t.Fatalf("TopNodes = %+v, want current host-only agent second", aggregate.TopNodes)
	}
}

func TestTrafficServiceAggregateTopNodesPreserveSameMonthLegacyAgentTotalDuringHostRollout(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	for _, policy := range []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-rollout", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-current", Direction: "rx", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
	} {
		if err := store.SaveTrafficPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []storage.TrafficDelta{
		{AgentID: "edge-rollout", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 100, TXBytes: 0},
		{AgentID: "edge-rollout", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), RXBytes: 20, TXBytes: 0},
		{AgentID: "edge-current", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC), RXBytes: 80, TXBytes: 0},
	} {
		if err := store.IncrementTrafficBuckets(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	aggregate, err := svc.Aggregate(ctx, "", "month", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopNodes) < 2 {
		t.Fatalf("TopNodes = %+v, want rollout and current agents", aggregate.TopNodes)
	}
	if aggregate.TopNodes[0].AgentID != "edge-rollout" || aggregate.TopNodes[0].UsedBytes != 120 {
		t.Fatalf("TopNodes = %+v, want rollout agent first with same-month legacy+host bytes", aggregate.TopNodes)
	}
	if aggregate.TopNodes[1].AgentID != "edge-current" || aggregate.TopNodes[1].UsedBytes != 80 {
		t.Fatalf("TopNodes = %+v, want current host-only agent second", aggregate.TopNodes)
	}
}

func TestTrafficServiceCounterResetPersistsEventWithRealStore(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "data")
	store := newTrafficServiceRealStore(t, dataRoot)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)
	first := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(100), "tx_bytes": float64(80)}}}
	reset := AgentStats{"traffic": map[string]any{"total": map[string]any{"rx_bytes": float64(7), "tx_bytes": float64(9)}}}

	if err := svc.IngestHeartbeat(ctx, "edge-1", first); err != nil {
		t.Fatal(err)
	}
	if err := svc.IngestHeartbeat(ctx, "edge-1", reset); err != nil {
		t.Fatal(err)
	}

	rows := loadTrafficEventsFromDataRoot(t, dataRoot, "edge-1", "counter_reset")
	if len(rows) != 1 || !strings.Contains(rows[0].Payload, "previous_rx") {
		t.Fatalf("events = %+v", rows)
	}
}

func assertSummaryBreakdown(t *testing.T, rows []TrafficSummaryBreakdown, scopeType, scopeID string, rx, tx, accounted uint64) {
	t.Helper()
	for _, row := range rows {
		if row.ScopeType == scopeType && row.ScopeID == scopeID {
			if row.RXBytes != rx || row.TXBytes != tx || row.AccountedBytes != accounted {
				t.Fatalf("%s/%s = %+v, want rx=%d tx=%d accounted=%d", scopeType, scopeID, row, rx, tx, accounted)
			}
			return
		}
	}
	t.Fatalf("missing breakdown %s/%s in %+v", scopeType, scopeID, rows)
}

func TestTrafficServiceTrendReturnsAccountedPoints(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fakeStore.policy.Direction = "max"
	bucketStart := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: bucketStart, RXBytes: 10, TXBytes: 20})
	now := time.Date(2026, 5, 3, 13, 0, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	points, err := svc.Trend(context.Background(), TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].AccountedBytes != 20 || points[0].BucketStart != "2026-05-03T12:00:00Z" {
		t.Fatalf("points = %+v", points)
	}
}

func TestTrafficServiceCalibrateAndCleanup(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Calibrate(context.Background(), "edge-1", TrafficCalibrationRequest{UsedBytes: 1234})
	if err != nil {
		t.Fatal(err)
	}
	baseline := fakeStore.baselines["edge-1|2026-05-01T00:00:00Z"]
	if baseline.RawAccountedBytes != 300 || baseline.AdjustUsedBytes != 1234 || summary.UsedBytes != 1234 {
		t.Fatalf("baseline = %+v summary = %+v", baseline, summary)
	}
	if len(fakeStore.events) != 1 || fakeStore.events[0].EventType != "calibration" {
		t.Fatalf("events after calibration = %+v, want calibration event", fakeStore.events)
	}

	result, err := svc.Cleanup(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 3 {
		t.Fatalf("cleanup = %+v", result)
	}
	if len(fakeStore.events) != 2 || fakeStore.events[1].EventType != "cleanup" {
		t.Fatalf("events after cleanup = %+v, want cleanup event", fakeStore.events)
	}
}

func TestTrafficServiceCalibrateToZeroClearsTrafficBucketsAndKeepsCursor(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.cursors[cursorKey("edge-1", "agent_total", "")] = storage.AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: now.Format(time.RFC3339),
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     200,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "http_rule",
		ScopeID:     "11",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     20,
		TXBytes:     30,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "l4_rule",
		ScopeID:     "old",
		BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     500,
		TXBytes:     600,
	})
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-2",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     7,
		TXBytes:     9,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Calibrate(context.Background(), "edge-1", TrafficCalibrationRequest{UsedBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 0 || summary.RXBytes != 0 || summary.TXBytes != 0 || summary.AccountedBytes != 0 {
		t.Fatalf("summary = %+v, want zeroed usage and raw stats", summary)
	}
	if _, ok := fakeStore.buckets[cursorKey("edge-1", "agent_total", "")]; ok {
		t.Fatal("agent_total bucket remains after zero calibration")
	}
	if _, ok := fakeStore.buckets[cursorKey("edge-1", "l4_rule", "old")]; !ok {
		t.Fatal("previous-cycle scoped bucket was deleted")
	}
	if _, ok := fakeStore.buckets[cursorKey("edge-1", "http_rule", "11")]; ok {
		t.Fatal("scoped bucket remains after zero calibration")
	}
	if _, ok := fakeStore.buckets[cursorKey("edge-2", "agent_total", "")]; !ok {
		t.Fatal("other agent bucket was deleted")
	}
	if _, ok := fakeStore.cursors[cursorKey("edge-1", "agent_total", "")]; !ok {
		t.Fatal("cursor was deleted; next heartbeat would replay cumulative counters")
	}
	if len(fakeStore.events) != 1 || fakeStore.events[0].EventType != "calibration" {
		t.Fatalf("events = %+v, want calibration event", fakeStore.events)
	}

	if err := svc.IngestHeartbeat(context.Background(), "edge-1", AgentStats{
		"traffic": map[string]any{
			"total": map[string]any{"rx_bytes": uint64(125), "tx_bytes": uint64(225)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	summary, err = svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 50 || summary.RXBytes != 25 || summary.TXBytes != 25 {
		t.Fatalf("summary after heartbeat = %+v, want post-zero delta only", summary)
	}
}

func TestTrafficServiceCalibrateUsesConfiguredTimezoneCycle(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 4, 17, 30, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        5,
		HourlyRetentionDays:  30,
		DailyRetentionMonths: 3,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: now,
		RXBytes:     100,
		TXBytes:     50,
	})
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return now },
		Timezone: shanghai,
	}, fakeStore)

	summary, err := svc.Calibrate(context.Background(), "edge-1", TrafficCalibrationRequest{UsedBytes: 1234})
	if err != nil {
		t.Fatal(err)
	}
	if summary.CycleStart != "2026-05-04T16:00:00Z" || summary.UsedBytes != 1234 {
		t.Fatalf("summary = %+v, want calibrated current Asia/Shanghai cycle", summary)
	}
	if _, ok := fakeStore.baselines["edge-1|2026-05-04T16:00:00Z"]; !ok {
		t.Fatalf("baselines = %+v, want baseline saved under Asia/Shanghai cycle start", fakeStore.baselines)
	}
}

func TestTrafficServiceCalibrateToZeroClearsLocalTimezoneMonthlyBuckets(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        15,
		HourlyRetentionDays:  30,
		DailyRetentionMonths: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTrafficBuckets(ctx, storage.TrafficDelta{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, shanghai),
		RXBytes:     100,
		TXBytes:     200,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return now },
		Timezone: shanghai,
	}, store)

	if _, err := svc.Calibrate(ctx, "edge-1", TrafficCalibrationRequest{UsedBytes: 0}); err != nil {
		t.Fatal(err)
	}
	monthlyRows, err := store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(monthlyRows) != 0 {
		t.Fatalf("monthlyRows = %+v, want local-month bucket cleared", monthlyRows)
	}
}

func TestTrafficServiceSummaryCountsPostCalibrationDirectionDeltas(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "max",
		CycleStartDay: 1,
	}
	fakeStore.baselines["edge-1|2026-05-01T00:00:00Z"] = storage.AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        "2026-05-01T00:00:00Z",
		RawRXBytes:        1000,
		RawTXBytes:        5000,
		RawAccountedBytes: 5000,
		AdjustUsedBytes:   123,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     5200,
		TXBytes:     5000,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 323 {
		t.Fatalf("UsedBytes = %d, want calibrated used 123 + max accounted delta 200", summary.UsedBytes)
	}
}

func TestTrafficServiceSummaryPreservesMaxBaselineWhenDominantSideSwitches(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:       "edge-1",
		Direction:     "max",
		CycleStartDay: 1,
	}
	fakeStore.baselines["edge-1|2026-05-01T00:00:00Z"] = storage.AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        "2026-05-01T00:00:00Z",
		RawRXBytes:        1000,
		RawTXBytes:        5000,
		RawAccountedBytes: 5000,
		AdjustUsedBytes:   123,
	}
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     5000,
		TXBytes:     5000,
	})
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Summary(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.UsedBytes != 123 {
		t.Fatalf("UsedBytes = %d, want calibrated used without extra max-direction delta", summary.UsedBytes)
	}
}

func TestTrafficServiceCleanupUsesConfiguredTimezoneCutoffs(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	monthlyRetention := 36
	now := time.Date(2026, 5, 4, 17, 30, 0, 0, time.UTC)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   3,
		MonthlyRetentionMonths: &monthlyRetention,
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return now },
		Timezone: shanghai,
	}, fakeStore)

	result, err := svc.Cleanup(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.DailyBefore != "2026-02-04T16:00:00Z" {
		t.Fatalf("DailyBefore = %q, want Asia/Shanghai midnight cutoff", result.DailyBefore)
	}
	if result.MonthlyBefore != "2023-04-30T16:00:00Z" {
		t.Fatalf("MonthlyBefore = %q, want Asia/Shanghai month cutoff", result.MonthlyBefore)
	}
}

func TestTrafficServiceCleanupTruncatesHourlyCutoffInConfiguredTimezone(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 5, 1, 30, 0, 0, kathmandu)
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        5,
		HourlyRetentionDays:  1,
		DailyRetentionMonths: 3,
	}
	svc := NewTrafficService(TrafficServiceConfig{
		Enabled:  true,
		Now:      func() time.Time { return now },
		Timezone: kathmandu,
	}, fakeStore)

	result, err := svc.Cleanup(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 5, 4, 1, 0, 0, 0, kathmandu).UTC().Format(time.RFC3339)
	if result.HourlyBefore != want {
		t.Fatalf("HourlyBefore = %q, want local-hour cutoff %q", result.HourlyBefore, want)
	}
}

func TestTrafficServiceCalibrateRecomputesTrafficBlockState(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	quota := int64(500)
	monthlyRetention := 12
	fakeStore.policy = storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		MonthlyQuotaBytes:      &quota,
		BlockWhenExceeded:      true,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   24,
		MonthlyRetentionMonths: &monthlyRetention,
	}
	fakeStore.agentTrafficBlocked["edge-1"] = true
	fakeStore.agentTrafficBlockReason["edge-1"] = "monthly quota exceeded"
	fakeStore.addBucket(storage.TrafficBucketRow{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC),
		RXBytes:     700,
		TXBytes:     100,
	})
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	summary, err := svc.Calibrate(context.Background(), "edge-1", TrafficCalibrationRequest{UsedBytes: 123})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Blocked {
		t.Fatalf("summary.Blocked = true, want false after calibration below quota")
	}
	if fakeStore.agentTrafficBlocked["edge-1"] {
		t.Fatalf("stored traffic block = true, want false")
	}
	if fakeStore.agentTrafficBlockReason["edge-1"] != "" {
		t.Fatalf("stored traffic block reason = %q, want empty", fakeStore.agentTrafficBlockReason["edge-1"])
	}
	if len(fakeStore.events) != 2 || fakeStore.events[0].EventType != "calibration" || fakeStore.events[1].EventType != "traffic_block_state_changed" {
		t.Fatalf("events = %+v, want calibration then block-state event", fakeStore.events)
	}
}

func TestTrafficServiceCleanupAllUsesConfiguredPolicies(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-1", Direction: "both", CycleStartDay: 1, HourlyRetentionDays: 180, DailyRetentionMonths: 24},
		{AgentID: "edge-2", Direction: "tx", CycleStartDay: 15, HourlyRetentionDays: 30, DailyRetentionMonths: 6},
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	result, err := svc.CleanupAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 6 || len(result.Results) != 2 {
		t.Fatalf("CleanupAll() = %+v, want two node results", result)
	}
	if result.Results[0].AgentID != "edge-1" || result.Results[1].AgentID != "edge-2" {
		t.Fatalf("CleanupAll() results = %+v", result.Results)
	}
	if len(fakeStore.events) != 2 || fakeStore.events[0].EventType != "cleanup" || fakeStore.events[1].EventType != "cleanup" {
		t.Fatalf("events = %+v, want cleanup events per node", fakeStore.events)
	}
}

func TestTrafficServiceCleanupAllIncludesAgentsUsingDefaultPolicy(t *testing.T) {
	fakeStore := newFakeTrafficStore()
	fakeStore.agents = []storage.AgentRow{
		{ID: "edge-default", Name: "edge-default"},
		{ID: "edge-custom", Name: "edge-custom"},
	}
	fakeStore.policies = []storage.AgentTrafficPolicyRow{
		{AgentID: "edge-custom", Direction: "tx", CycleStartDay: 15, HourlyRetentionDays: 30, DailyRetentionMonths: 6},
	}
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, fakeStore)

	result, err := svc.CleanupAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 6 || len(result.Results) != 2 {
		t.Fatalf("CleanupAll() = %+v, want cleanup for default and custom agents", result)
	}
	ids := []string{result.Results[0].AgentID, result.Results[1].AgentID}
	if !slices.Contains(ids, "edge-default") || !slices.Contains(ids, "edge-custom") {
		t.Fatalf("CleanupAll() result IDs = %+v, want edge-default and edge-custom", ids)
	}
}

func TestTrafficServiceCleanupAllIncludesAgentsWithOnlyTrafficData(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.IncrementTrafficBuckets(ctx, storage.TrafficDelta{
		AgentID:     "local",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2025, 10, 1, 10, 0, 0, 0, time.UTC),
		RXBytes:     10,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	result, err := svc.CleanupAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows == 0 || len(result.Results) != 1 {
		t.Fatalf("CleanupAll() = %+v, want cleanup for traffic data agent", result)
	}
	if result.Results[0].AgentID != "local" {
		t.Fatalf("CleanupAll() result = %+v, want local", result.Results)
	}
}

func TestTrafficServiceCleanupPreservesCurrentCycleUsageSummary(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:             "edge-1",
		Direction:           "both",
		CycleStartDay:       1,
		HourlyRetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 30},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	result, err := svc.Cleanup(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.HourlyBefore != "2026-05-13T12:00:00Z" {
		t.Fatalf("HourlyBefore = %q, want retention cutoff", result.HourlyBefore)
	}
	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 50 {
		t.Fatalf("summary.RXBytes = %d, want current cycle rows preserved", summary.RXBytes)
	}
}

func TestTrafficServiceCleanupDeletesExpiredHourlyRowsWithinCurrentCycle(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:             "edge-1",
		Direction:           "both",
		CycleStartDay:       1,
		HourlyRetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	result, err := svc.Cleanup(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.HourlyBefore != "2026-05-13T12:00:00Z" {
		t.Fatalf("HourlyBefore = %q, want retention cutoff", result.HourlyBefore)
	}
	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "hour",
		From:        "2026-05-01T00:00:00Z",
		To:          "2026-05-21T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BucketStart != "2026-05-19T10:00:00Z" {
		t.Fatalf("hourly rows = %+v, want only recent current-cycle bucket", rows)
	}
	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 30 {
		t.Fatalf("summary.RXBytes = %d, want daily/monthly summaries to preserve cycle usage", summary.RXBytes)
	}
}

func TestTrafficServiceCleanupPreservesRolloutDayHourlyBucketsNeededForCurrentCycleSummary(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  7,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 8, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 30},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 40},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	before, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.RXBytes != 100 {
		t.Fatalf("summary before cleanup = %+v, want rollout-day bridge included", before)
	}

	result, err := svc.Cleanup(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.HourlyBefore != "2026-05-10T08:00:00Z" {
		t.Fatalf("HourlyBefore = %q, want preserved rollout-day bridge cutoff", result.HourlyBefore)
	}

	after, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.RXBytes != 100 {
		t.Fatalf("summary after cleanup = %+v, want rollout-day hourly bridge preserved for current-cycle summary", after)
	}
}

func TestTrafficServiceCleanupDeletesExpiredDailyRowsByRetention(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  30,
		DailyRetentionMonths: 1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC), RXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	result, err := svc.Cleanup(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.DailyBefore != "2026-04-20T00:00:00Z" {
		t.Fatalf("DailyBefore = %q, want retention cutoff", result.DailyBefore)
	}
	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "day",
		From:        "2026-04-01T00:00:00Z",
		To:          "2026-05-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].BucketStart != "2026-04-25T00:00:00Z" {
		t.Fatalf("daily rows = %+v, want only row inside retention window", rows)
	}
}

func TestTrafficServiceCleanupDeletesExpiredMonthlyRowsByCycleRetention(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 1
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), RXBytes: 5},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	result, err := svc.Cleanup(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.MonthlyBefore != "2026-03-15T00:00:00Z" {
		t.Fatalf("MonthlyBefore = %q, want cycle retention cutoff", result.MonthlyBefore)
	}
	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-03-15T00:00:00Z",
		To:          "2026-06-15T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].BucketStart != "2026-03-15T00:00:00Z" || rows[1].BucketStart != "2026-04-15T00:00:00Z" {
		t.Fatalf("monthly rows = %+v, want retained cycle-month rows from cutoff onward", rows)
	}
}

func TestTrafficServiceAggregateMonthReadPreservesRetainedMonthlyHistoryBeyondDailyRetention(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 30},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	if _, err := svc.Cleanup(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}
	before, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2025-12-01T00:00:00Z",
		To:          "2026-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 {
		t.Fatalf("monthly rows before aggregate read = %+v, want retained Jan/Apr/May cycle months", before)
	}

	aggregate, err := svc.Aggregate(ctx, "edge-1", "month", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.TopNodes) != 1 || aggregate.TopNodes[0].UsedBytes != 60 {
		t.Fatalf("Aggregate() = %+v, want retained monthly usage total", aggregate.TopNodes)
	}

	after, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2025-12-01T00:00:00Z",
		To:          "2026-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 3 {
		t.Fatalf("monthly rows after aggregate read = %+v, want month read to preserve retained history", after)
	}
}

func TestTrafficServiceCleanupPreservesCurrentCycleBreakdownsWhenHourlyRetentionIsShorterThanCycle(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  7,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 30},
		{AgentID: "edge-1", ScopeType: "host_total", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "http", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "http", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "http_rule", ScopeID: "11", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "host_interface", ScopeID: "eth0", BucketStart: time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC), RXBytes: 30},
		{AgentID: "edge-1", ScopeType: "host_interface", ScopeID: "eth0", BucketStart: time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC), RXBytes: 40},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	if _, err := svc.Cleanup(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}
	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 70 {
		t.Fatalf("summary.RXBytes = %d, want full-cycle total preserved", summary.RXBytes)
	}
	if summary.HostTotal.RXBytes != 70 || summary.HostTotal.AccountedBytes != 70 {
		t.Fatalf("HostTotal = %+v, want full-cycle host breakdown", summary.HostTotal)
	}
	assertSummaryBreakdown(t, summary.Aggregates, "http", "", 30, 0, 30)
	assertSummaryBreakdown(t, summary.HTTPRules, "http_rule", "11", 30, 0, 30)
	assertSummaryBreakdown(t, summary.HostInterfaces, "host_interface", "eth0", 70, 0, 70)
}

func TestTrafficServiceUpdatePolicyRebuildsMonthlySummariesForCycleStartDayChanges(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC), RXBytes: 50},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 70},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	before, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if before.RXBytes != 120 {
		t.Fatalf("summary before policy change = %+v, want original cycle usage", before)
	}

	updated, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:            "both",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CycleStartDay != 15 {
		t.Fatalf("updated policy = %+v, want cycle_start_day 15", updated)
	}

	after, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.CycleStart != "2026-05-15T00:00:00Z" || after.RXBytes != 70 {
		t.Fatalf("summary after policy change = %+v, want current-cycle usage rebuilt under new cycle boundary", after)
	}
}

func TestTrafficServiceUpdatePolicyRebuildsRetainedMonthlySummariesBeyondDailyRetention(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-01-01T00:00:00Z",
		To:          "2026-06-15T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []string{
		"2026-01-15T00:00:00Z",
		"2026-02-15T00:00:00Z",
		"2026-04-15T00:00:00Z",
		"2026-05-15T00:00:00Z",
	}
	if len(rows) != len(wantStarts) {
		t.Fatalf("monthly rows after policy change = %+v, want only retained rows rebuilt to cycle-day 15", rows)
	}
	for i, want := range wantStarts {
		if rows[i].BucketStart != want {
			t.Fatalf("monthly row %d start = %q, want %q; rows = %+v", i, rows[i].BucketStart, want, rows)
		}
	}
}

func TestTrafficServiceUpdatePolicyPreservesRetainedMonthlySummariesWithoutDailySource(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)
	if _, err := svc.Cleanup(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-01-01T00:00:00Z",
		To:          "2026-06-15T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"2026-01-01T00:00:00Z": 10,
		"2026-02-01T00:00:00Z": 20,
		"2026-04-15T00:00:00Z": 40,
		"2026-05-15T00:00:00Z": 50,
	}
	if len(rows) != len(want) {
		t.Fatalf("monthly rows after policy change = %+v, want unrebuildable retained months preserved", rows)
	}
	for _, row := range rows {
		if wantRX, ok := want[row.BucketStart]; !ok || row.RXBytes != wantRX {
			t.Fatalf("monthly row = %+v, want retained/source-backed rows %+v", row, want)
		}
	}
}

func TestTrafficServiceUpdatePolicyPreservesPartialMonthlyBytesWithoutDailySource(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC), RXBytes: 25},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)
	if _, err := svc.Cleanup(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-04-01T00:00:00Z",
		To:          "2026-06-15T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, row := range rows {
		total += row.RXBytes
	}
	if total != 115 {
		t.Fatalf("monthly rows after policy change = %+v, total RX = %d, want retained partial-month bytes preserved", rows, total)
	}
}

func TestTrafficServiceUpdatePolicyPreservesPartialMonthlyBytesAcrossRepeatedCycleChanges(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC), RXBytes: 25},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)
	if _, err := svc.Cleanup(ctx, "edge-1"); err != nil {
		t.Fatal(err)
	}

	for _, cycleStartDay := range []int{15, 1} {
		_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
			Direction:              "both",
			CycleStartDay:          cycleStartDay,
			HourlyRetentionDays:    30,
			DailyRetentionMonths:   1,
			MonthlyRetentionMonths: &monthlyRetention,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-04-01T00:00:00Z",
		To:          "2026-06-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, row := range rows {
		total += row.RXBytes
	}
	if total != 115 {
		t.Fatalf("monthly rows after repeated policy changes = %+v, total RX = %d, want retained bytes preserved", rows, total)
	}
}

func TestTrafficServiceUpdatePolicyReplacesTargetRowsAcrossRepeatedCycleChanges(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthlyRetention := 6
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    180,
		DailyRetentionMonths:   6,
		MonthlyRetentionMonths: &monthlyRetention,
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-01-01T00:00:00Z",
		To:          "2026-03-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"2026-01-01T00:00:00Z": 10,
		"2026-02-01T00:00:00Z": 20,
	}
	if len(rows) != len(want) {
		t.Fatalf("monthly rows after repeated rebuild = %+v, want target rows replaced without duplicates", rows)
	}
	for _, row := range rows {
		if wantRX, ok := want[row.BucketStart]; !ok || row.RXBytes != wantRX {
			t.Fatalf("monthly row = %+v, want target rows %+v", row, want)
		}
	}
}

func TestTrafficServiceUpdatePolicyRebuildsPermanentMonthlyHistoryFromAvailableDailyRows(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:                "edge-1",
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: nil,
	}); err != nil {
		t.Fatal(err)
	}
	for _, delta := range []storage.TrafficDelta{
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 1, 20, 10, 0, 0, 0, time.UTC), RXBytes: 10},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), RXBytes: 20},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC), RXBytes: 40},
		{AgentID: "edge-1", ScopeType: "agent_total", BucketStart: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC), RXBytes: 50},
	} {
		if err := store.IncrementTrafficBuckets(ctx, delta); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)

	_, err := svc.UpdatePolicy(ctx, "edge-1", TrafficPolicy{
		Direction:              "both",
		CycleStartDay:          15,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   1,
		MonthlyRetentionMonths: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := svc.Trend(ctx, TrafficTrendQuery{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		Granularity: "month",
		From:        "2026-01-01T00:00:00Z",
		To:          "2026-06-15T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantStarts := []string{
		"2026-01-15T00:00:00Z",
		"2026-02-15T00:00:00Z",
		"2026-04-15T00:00:00Z",
		"2026-05-15T00:00:00Z",
	}
	if len(rows) != len(wantStarts) {
		t.Fatalf("monthly rows after permanent policy change = %+v, want all daily-backed history rebuilt", rows)
	}
	for i, want := range wantStarts {
		if rows[i].BucketStart != want {
			t.Fatalf("monthly row %d start = %q, want %q; rows = %+v", i, rows[i].BucketStart, want, rows)
		}
	}
}

func TestTrafficServiceUpdatePolicyRollsBackPolicyWhenMonthlyRebuildFails(t *testing.T) {
	rebuildErr := errors.New("rebuild failed")
	store := &failingMonthlyRebuildTrafficStore{
		fakeTrafficStore: newFakeTrafficStore(),
		rebuildErr:       rebuildErr,
	}
	store.policy = storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        1,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, store)

	_, err := svc.UpdatePolicy(context.Background(), "edge-1", TrafficPolicy{
		Direction:            "both",
		CycleStartDay:        15,
		HourlyRetentionDays:  180,
		DailyRetentionMonths: 24,
	})
	if !errors.Is(err, rebuildErr) {
		t.Fatalf("UpdatePolicy() error = %v, want rebuild failure", err)
	}
	if !store.transactionUsed {
		t.Fatalf("UpdatePolicy() did not use transactional policy update path")
	}
	if store.policy.CycleStartDay != 1 {
		t.Fatalf("stored policy cycle_start_day = %d, want rollback to 1", store.policy.CycleStartDay)
	}
}

type jsonNumber string

func (n jsonNumber) String() string { return string(n) }

type fakeTrafficStore struct {
	policy                  storage.AgentTrafficPolicyRow
	policies                []storage.AgentTrafficPolicyRow
	agents                  []storage.AgentRow
	httpRulesByAgent        map[string][]storage.HTTPRuleRow
	l4RulesByAgent          map[string][]storage.L4RuleRow
	relayListenersByAgent   map[string][]storage.RelayListenerRow
	cursors                 map[string]storage.AgentTrafficRawCursorRow
	buckets                 map[string]storage.TrafficBucketRow
	baselines               map[string]storage.AgentTrafficBaselineRow
	events                  []storage.AgentTrafficEventRow
	agentTrafficBlocked     map[string]bool
	agentTrafficBlockReason map[string]string
	emptyTrends             []storage.TrafficTrendQuery
	writeCount              int
	baselineReadCount       int
	trendReadCount          int
	aggregateTrendReadCount int
	breakdownReadCount      int
}

func newFakeTrafficStore() *fakeTrafficStore {
	return &fakeTrafficStore{
		policy: storage.AgentTrafficPolicyRow{
			AgentID:              "edge-1",
			Direction:            "both",
			CycleStartDay:        1,
			HourlyRetentionDays:  180,
			DailyRetentionMonths: 24,
		},
		cursors:                 map[string]storage.AgentTrafficRawCursorRow{},
		buckets:                 map[string]storage.TrafficBucketRow{},
		baselines:               map[string]storage.AgentTrafficBaselineRow{},
		httpRulesByAgent:        map[string][]storage.HTTPRuleRow{},
		l4RulesByAgent:          map[string][]storage.L4RuleRow{},
		relayListenersByAgent:   map[string][]storage.RelayListenerRow{},
		agentTrafficBlocked:     map[string]bool{},
		agentTrafficBlockReason: map[string]string{},
	}
}

func (s *fakeTrafficStore) GetTrafficPolicy(_ context.Context, agentID string) (storage.AgentTrafficPolicyRow, error) {
	for _, row := range s.policies {
		if row.AgentID == agentID {
			policy := row
			if policy.Direction == "" {
				policy.Direction = "both"
			}
			if policy.CycleStartDay == 0 {
				policy.CycleStartDay = 1
			}
			if policy.HourlyRetentionDays == 0 {
				policy.HourlyRetentionDays = 180
			}
			if policy.DailyRetentionMonths == 0 {
				policy.DailyRetentionMonths = 24
			}
			return policy, nil
		}
	}
	policy := s.policy
	policy.AgentID = agentID
	if policy.Direction == "" {
		policy.Direction = "both"
	}
	if policy.CycleStartDay == 0 {
		policy.CycleStartDay = 1
	}
	if policy.HourlyRetentionDays == 0 {
		policy.HourlyRetentionDays = 180
	}
	if policy.DailyRetentionMonths == 0 {
		policy.DailyRetentionMonths = 24
	}
	return policy, nil
}

func (s *fakeTrafficStore) SaveTrafficPolicy(_ context.Context, row storage.AgentTrafficPolicyRow) error {
	s.writeCount++
	s.policy = row
	return nil
}

type failingMonthlyRebuildTrafficStore struct {
	*fakeTrafficStore
	rebuildErr      error
	transactionUsed bool
}

func (s *failingMonthlyRebuildTrafficStore) SaveTrafficPolicyAndRebuildMonthlySummaries(ctx context.Context, row storage.AgentTrafficPolicyRow, rebuild bool, from, to time.Time, previousCycleStartDay int) error {
	s.transactionUsed = true
	before := s.policy
	if err := s.SaveTrafficPolicy(ctx, row); err != nil {
		s.policy = before
		return err
	}
	if rebuild {
		if err := s.RebuildTrafficMonthlySummaries(ctx, row.AgentID, from, to); err != nil {
			s.policy = before
			return err
		}
	}
	return nil
}

func (s *failingMonthlyRebuildTrafficStore) RebuildTrafficMonthlySummaries(context.Context, string, time.Time, time.Time) error {
	return s.rebuildErr
}

func (s *fakeTrafficStore) ListTrafficPolicies(context.Context) ([]storage.AgentTrafficPolicyRow, error) {
	if len(s.policies) > 0 {
		return append([]storage.AgentTrafficPolicyRow(nil), s.policies...), nil
	}
	return []storage.AgentTrafficPolicyRow{s.policy}, nil
}

func (s *fakeTrafficStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.agents...), nil
}

func (s *fakeTrafficStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.httpRulesByAgent[agentID]...), nil
}

func (s *fakeTrafficStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), s.l4RulesByAgent[agentID]...), nil
}

func (s *fakeTrafficStore) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	return append([]storage.RelayListenerRow(nil), s.relayListenersByAgent[agentID]...), nil
}

func (s *fakeTrafficStore) ListTrafficAgentIDs(context.Context) ([]string, error) {
	seen := map[string]bool{}
	ids := []string{}
	add := func(agentID string) {
		if agentID == "" || seen[agentID] {
			return
		}
		seen[agentID] = true
		ids = append(ids, agentID)
	}
	for _, policy := range s.policies {
		add(policy.AgentID)
	}
	if len(s.policies) == 0 {
		add(s.policy.AgentID)
	}
	for _, agent := range s.agents {
		add(agent.ID)
	}
	for _, row := range s.buckets {
		add(row.AgentID)
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *fakeTrafficStore) GetTrafficBaseline(_ context.Context, agentID, cycleStart string) (storage.AgentTrafficBaselineRow, bool, error) {
	s.baselineReadCount++
	row, ok := s.baselines[agentID+"|"+cycleStart]
	return row, ok, nil
}

func (s *fakeTrafficStore) SaveTrafficBaseline(_ context.Context, row storage.AgentTrafficBaselineRow) error {
	s.writeCount++
	s.baselines[row.AgentID+"|"+row.CycleStart] = row
	return nil
}

func (s *fakeTrafficStore) GetTrafficCursor(_ context.Context, agentID, scopeType, scopeID string) (storage.AgentTrafficRawCursorRow, bool, error) {
	row, ok := s.cursors[cursorKey(agentID, scopeType, scopeID)]
	return row, ok, nil
}

func (s *fakeTrafficStore) SaveTrafficCursor(_ context.Context, row storage.AgentTrafficRawCursorRow) error {
	s.writeCount++
	s.cursors[cursorKey(row.AgentID, row.ScopeType, row.ScopeID)] = row
	return nil
}

func (s *fakeTrafficStore) IncrementTrafficBuckets(_ context.Context, delta storage.TrafficDelta) error {
	s.writeCount++
	key := cursorKey(delta.AgentID, delta.ScopeType, delta.ScopeID)
	row := s.buckets[key]
	row.AgentID = delta.AgentID
	row.ScopeType = delta.ScopeType
	row.ScopeID = delta.ScopeID
	row.BucketStart = delta.BucketStart.Truncate(time.Hour).UTC()
	row.RXBytes += delta.RXBytes
	row.TXBytes += delta.TXBytes
	s.buckets[key] = row
	return nil
}

func (s *fakeTrafficStore) ListTrafficTrend(_ context.Context, query storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error) {
	s.trendReadCount++
	for _, emptyQuery := range s.emptyTrends {
		if trafficTrendQueryMatches(emptyQuery, query) {
			return []storage.TrafficBucketRow{}, nil
		}
	}
	rows := []storage.TrafficBucketRow{}
	for _, row := range s.buckets {
		if row.AgentID != query.AgentID {
			continue
		}
		if query.ScopeType != "" && (row.ScopeType != query.ScopeType || row.ScopeID != query.ScopeID) {
			continue
		}
		if !query.From.IsZero() && row.BucketStart.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !row.BucketStart.Before(query.To) {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func trafficTrendQueryMatches(want, got storage.TrafficTrendQuery) bool {
	if want.AgentID != "" && want.AgentID != got.AgentID {
		return false
	}
	if want.ScopeType != "" && want.ScopeType != got.ScopeType {
		return false
	}
	if want.ScopeID != "" && want.ScopeID != got.ScopeID {
		return false
	}
	if want.Granularity != "" && want.Granularity != got.Granularity {
		return false
	}
	return true
}

func (s *fakeTrafficStore) ListTrafficBreakdown(_ context.Context, query storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error) {
	s.breakdownReadCount++
	rows := []storage.TrafficBucketRow{}
	for _, row := range s.buckets {
		if row.AgentID != query.AgentID {
			continue
		}
		if query.ScopeType != "" && row.ScopeType != query.ScopeType {
			continue
		}
		if !query.From.IsZero() && row.BucketStart.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !row.BucketStart.Before(query.To) {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *fakeTrafficStore) ListTrafficBreakdownByScopeTypes(_ context.Context, query storage.TrafficBreakdownQuery) ([]storage.TrafficBucketRow, error) {
	s.breakdownReadCount++
	agentIDs := map[string]struct{}{}
	for _, agentID := range query.AgentIDs {
		agentIDs[agentID] = struct{}{}
	}
	scopeTypes := map[string]struct{}{}
	for _, scopeType := range query.ScopeTypes {
		scopeTypes[scopeType] = struct{}{}
	}
	byScope := map[string]storage.TrafficBucketRow{}
	order := []string{}
	for _, row := range s.buckets {
		if len(agentIDs) > 0 {
			if _, ok := agentIDs[row.AgentID]; !ok {
				continue
			}
		}
		if len(scopeTypes) > 0 {
			if _, ok := scopeTypes[row.ScopeType]; !ok {
				continue
			}
		}
		if !query.From.IsZero() && row.BucketStart.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !row.BucketStart.Before(query.To) {
			continue
		}
		key := cursorKey(row.AgentID, row.ScopeType, row.ScopeID)
		current, ok := byScope[key]
		if !ok {
			current.AgentID = row.AgentID
			current.ScopeType = row.ScopeType
			current.ScopeID = row.ScopeID
			order = append(order, key)
		}
		current.RXBytes += row.RXBytes
		current.TXBytes += row.TXBytes
		byScope[key] = current
	}
	rows := make([]storage.TrafficBucketRow, 0, len(order))
	for _, key := range order {
		rows = append(rows, byScope[key])
	}
	return rows, nil
}

func (s *fakeTrafficStore) ListTrafficTrendByScopeTypes(_ context.Context, query storage.TrafficBreakdownQuery) ([]storage.TrafficBucketRow, error) {
	s.trendReadCount++
	s.aggregateTrendReadCount++
	agentIDs := map[string]struct{}{}
	for _, agentID := range query.AgentIDs {
		agentIDs[agentID] = struct{}{}
	}
	scopeTypes := map[string]struct{}{}
	for _, scopeType := range query.ScopeTypes {
		scopeTypes[scopeType] = struct{}{}
	}
	rows := []storage.TrafficBucketRow{}
	for _, row := range s.buckets {
		if len(agentIDs) > 0 {
			if _, ok := agentIDs[row.AgentID]; !ok {
				continue
			}
		}
		if len(scopeTypes) > 0 {
			if _, ok := scopeTypes[row.ScopeType]; !ok {
				continue
			}
		}
		if !query.From.IsZero() && row.BucketStart.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !row.BucketStart.Before(query.To) {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *fakeTrafficStore) DeleteTrafficBefore(_ context.Context, _ string, _ storage.TrafficCleanupCutoff) (int64, error) {
	s.writeCount++
	return 3, nil
}

func (s *fakeTrafficStore) DeleteTrafficBucketsByAgentInWindow(_ context.Context, agentID string, from, to time.Time) (int64, error) {
	s.writeCount++
	var deleted int64
	for key, row := range s.buckets {
		if row.AgentID != agentID {
			continue
		}
		if row.BucketStart.Before(from) || !row.BucketStart.Before(to) {
			continue
		}
		delete(s.buckets, key)
		deleted++
	}
	return deleted, nil
}

func (s *fakeTrafficStore) SaveTrafficEvent(_ context.Context, row storage.AgentTrafficEventRow) error {
	s.writeCount++
	s.events = append(s.events, row)
	return nil
}

func (s *fakeTrafficStore) GetAgentTrafficState(_ context.Context, agentID string) (bool, string, bool, error) {
	blocked, found := s.agentTrafficBlocked[agentID]
	return blocked, s.agentTrafficBlockReason[agentID], found, nil
}

func (s *fakeTrafficStore) SaveAgentTrafficState(_ context.Context, agentID string, blocked bool, reason string) error {
	s.writeCount++
	s.agentTrafficBlocked[agentID] = blocked
	s.agentTrafficBlockReason[agentID] = reason
	return nil
}

func (s *fakeTrafficStore) addBucket(row storage.TrafficBucketRow) {
	s.buckets[cursorKey(row.AgentID, row.ScopeType, row.ScopeID)] = row
}

func (s *fakeTrafficStore) bucketRX(agentID, scopeType, scopeID string) uint64 {
	return s.buckets[cursorKey(agentID, scopeType, scopeID)].RXBytes
}

func (s *fakeTrafficStore) bucketTX(agentID, scopeType, scopeID string) uint64 {
	return s.buckets[cursorKey(agentID, scopeType, scopeID)].TXBytes
}

func cursorKey(agentID, scopeType, scopeID string) string {
	return agentID + "|" + scopeType + "|" + scopeID
}

func newTrafficServiceRealStore(t *testing.T, dataRoot ...string) *storage.GormStore {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	if len(dataRoot) > 0 {
		root = dataRoot[0]
	}
	store, err := storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DataRoot:            root,
		LocalAgentID:        "local",
		TrafficStatsEnabled: true,
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

func loadTrafficEventsFromDataRoot(t *testing.T, dataRoot, agentID, eventType string) []storage.AgentTrafficEventRow {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dataRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open event verification db error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	var rows []storage.AgentTrafficEventRow
	if err := db.Where("agent_id = ? AND event_type = ?", agentID, eventType).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}
