package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true}, fakeStore)

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

func TestTrafficServiceCleanupPreservesCurrentCycleHourlyRows(t *testing.T) {
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
	if result.HourlyBefore != "2026-05-01T00:00:00Z" {
		t.Fatalf("HourlyBefore = %q, want current cycle start", result.HourlyBefore)
	}
	summary, err := svc.Summary(ctx, "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.RXBytes != 50 {
		t.Fatalf("summary.RXBytes = %d, want current cycle rows preserved", summary.RXBytes)
	}
}

type jsonNumber string

func (n jsonNumber) String() string { return string(n) }

type fakeTrafficStore struct {
	policy                  storage.AgentTrafficPolicyRow
	policies                []storage.AgentTrafficPolicyRow
	agents                  []storage.AgentRow
	cursors                 map[string]storage.AgentTrafficRawCursorRow
	buckets                 map[string]storage.TrafficBucketRow
	baselines               map[string]storage.AgentTrafficBaselineRow
	events                  []storage.AgentTrafficEventRow
	agentTrafficBlocked     map[string]bool
	agentTrafficBlockReason map[string]string
	writeCount              int
	baselineReadCount       int
	trendReadCount          int
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
		agentTrafficBlocked:     map[string]bool{},
		agentTrafficBlockReason: map[string]string{},
	}
}

func (s *fakeTrafficStore) GetTrafficPolicy(_ context.Context, agentID string) (storage.AgentTrafficPolicyRow, error) {
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

func (s *fakeTrafficStore) ListTrafficPolicies(context.Context) ([]storage.AgentTrafficPolicyRow, error) {
	if len(s.policies) > 0 {
		return append([]storage.AgentTrafficPolicyRow(nil), s.policies...), nil
	}
	return []storage.AgentTrafficPolicyRow{s.policy}, nil
}

func (s *fakeTrafficStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.agents...), nil
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

func (s *fakeTrafficStore) ListTrafficBreakdown(_ context.Context, query storage.TrafficTrendQuery) ([]storage.TrafficBucketRow, error) {
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

func (s *fakeTrafficStore) DeleteTrafficBefore(_ context.Context, _ string, _ storage.TrafficCleanupCutoff) (int64, error) {
	s.writeCount++
	return 3, nil
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
