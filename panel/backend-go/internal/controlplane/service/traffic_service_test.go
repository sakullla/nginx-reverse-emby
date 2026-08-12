package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestTrafficServiceIngestHeartbeatComputesDeltas(t *testing.T) {
	t.Parallel()
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

func TestTrafficServiceIngestHeartbeatAccountsUnifiedTrafficQuotas(t *testing.T) {
	store := newTrafficServiceRealStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-1"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 3, 12, 34, 0, 0, time.UTC)
	for _, policy := range []storage.QuotaPolicyRow{
		{ID: "traffic-limit", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "traffic_bytes", Limit: 1000, CreatedAt: now, UpdatedAt: now},
		{ID: "bandwidth-limit", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "bandwidth_bytes_per_second", Limit: 100, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.UpsertQuotaPolicy(t.Context(), policy); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewTrafficService(TrafficServiceConfig{Enabled: true, Now: func() time.Time { return now }}, store)
	stats := func(rx, tx uint64) AgentStats {
		return AgentStats{"traffic": map[string]any{"host": map[string]any{"total": map[string]any{"rx_bytes": rx, "tx_bytes": tx}}}}
	}
	if err := svc.IngestHeartbeat(t.Context(), "edge-1", stats(100, 200)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	if err := svc.IngestHeartbeat(t.Context(), "edge-1", stats(300, 500)); err != nil {
		t.Fatal(err)
	}
	traffic, err := store.ResourceGroupQuotaStatus(t.Context(), "default", "traffic_bytes")
	if err != nil {
		t.Fatal(err)
	}
	bandwidth, err := store.ResourceGroupQuotaStatus(t.Context(), "default", "bandwidth_bytes_per_second")
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Current != 500 || bandwidth.Current != 50 {
		t.Fatalf("quota usage traffic=%d bandwidth=%d, want 500/50", traffic.Current, bandwidth.Current)
	}
}

func TestTrafficServiceSummaryIncludesObjectBreakdownsWithRealStore(t *testing.T) {
	t.Parallel()
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

func TestTrafficServiceUpdatePolicyRollsBackPolicyWhenMonthlyRebuildFails(t *testing.T) {
	t.Parallel()
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

type nonAtomicGovernedTrafficStore struct {
	*fakeTrafficStore
}

func (*nonAtomicGovernedTrafficStore) GetResourceBinding(context.Context, string, string) (storage.ResourceBindingRow, error) {
	return storage.ResourceBindingRow{}, gorm.ErrRecordNotFound
}

func (*nonAtomicGovernedTrafficStore) ConsumeQuota(context.Context, string, string, string, int64, time.Time) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

func (*nonAtomicGovernedTrafficStore) ReconcileResourceGroupQuota(context.Context, string, string, int64, time.Time) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

func (*nonAtomicGovernedTrafficStore) ReconcileAgentBandwidth(context.Context, string, string, int64, time.Time) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

func (*nonAtomicGovernedTrafficStore) RefreshResourceGroupBandwidth(context.Context, string, time.Time) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

func (*nonAtomicGovernedTrafficStore) ResourceGroupQuotaStatus(context.Context, string, string) (storage.QuotaDecision, error) {
	return storage.QuotaDecision{}, nil
}

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

func (*fakeTrafficStore) allowUngovernedMutationsForTests() {}

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
