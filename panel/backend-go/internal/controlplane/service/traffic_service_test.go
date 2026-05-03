package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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

	result, err := svc.Cleanup(context.Background(), "edge-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 3 {
		t.Fatalf("cleanup = %+v", result)
	}
}

type jsonNumber string

func (n jsonNumber) String() string { return string(n) }

type fakeTrafficStore struct {
	policy     storage.AgentTrafficPolicyRow
	cursors    map[string]storage.AgentTrafficRawCursorRow
	buckets    map[string]storage.TrafficBucketRow
	baselines  map[string]storage.AgentTrafficBaselineRow
	events     []storage.AgentTrafficEventRow
	writeCount int
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
		cursors:   map[string]storage.AgentTrafficRawCursorRow{},
		buckets:   map[string]storage.TrafficBucketRow{},
		baselines: map[string]storage.AgentTrafficBaselineRow{},
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

func (s *fakeTrafficStore) GetTrafficBaseline(_ context.Context, agentID, cycleStart string) (storage.AgentTrafficBaselineRow, bool, error) {
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

func (s *fakeTrafficStore) DeleteTrafficBefore(_ context.Context, _ string, _ storage.TrafficCleanupCutoff) (int64, error) {
	s.writeCount++
	return 3, nil
}

func (s *fakeTrafficStore) SaveTrafficEvent(_ context.Context, row storage.AgentTrafficEventRow) error {
	s.writeCount++
	s.events = append(s.events, row)
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
