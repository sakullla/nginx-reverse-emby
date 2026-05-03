package storage

import "testing"

func TestCopyDefaultMigrationRowsCopiesTrafficPolicyAndBaselineButSkipsHistory(t *testing.T) {
	ctx := t.Context()
	source := newTrafficTestStore(t, true)
	target := newTrafficTestStore(t, true)

	quota := int64(1024)
	if err := source.SaveAgent(ctx, AgentRow{ID: "edge-1", Name: "edge-1"}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := source.SaveTrafficPolicy(ctx, AgentTrafficPolicyRow{
		AgentID:           "edge-1",
		Direction:         "rx",
		MonthlyQuotaBytes: &quota,
		BlockWhenExceeded: true,
	}); err != nil {
		t.Fatalf("SaveTrafficPolicy() error = %v", err)
	}
	if err := source.SaveTrafficBaseline(ctx, AgentTrafficBaselineRow{
		AgentID:           "edge-1",
		CycleStart:        "2026-05-01T00:00:00Z",
		RawRXBytes:        100,
		RawTXBytes:        200,
		RawAccountedBytes: 300,
	}); err != nil {
		t.Fatalf("SaveTrafficBaseline() error = %v", err)
	}
	if err := source.SaveTrafficCursor(ctx, AgentTrafficRawCursorRow{
		AgentID:    "edge-1",
		ScopeType:  "agent_total",
		ScopeID:    "",
		RXBytes:    100,
		TXBytes:    200,
		ObservedAt: "2026-05-03T00:00:00Z",
	}); err != nil {
		t.Fatalf("SaveTrafficCursor() error = %v", err)
	}
	if err := source.SaveTrafficEvent(ctx, AgentTrafficEventRow{
		AgentID:   "edge-1",
		EventType: "quota_exceeded",
		Message:   "quota exceeded",
	}); err != nil {
		t.Fatalf("SaveTrafficEvent() error = %v", err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatalf("CopyDefaultMigrationRows() error = %v", err)
	}

	policy, err := target.GetTrafficPolicy(ctx, "edge-1")
	if err != nil {
		t.Fatalf("GetTrafficPolicy() error = %v", err)
	}
	if policy.Direction != "rx" || policy.MonthlyQuotaBytes == nil || *policy.MonthlyQuotaBytes != quota || !policy.BlockWhenExceeded {
		t.Fatalf("target policy = %+v", policy)
	}

	baseline, found, err := target.GetTrafficBaseline(ctx, "edge-1", "2026-05-01T00:00:00Z")
	if err != nil {
		t.Fatalf("GetTrafficBaseline() error = %v", err)
	}
	if !found || baseline.RawAccountedBytes != 300 {
		t.Fatalf("target baseline found=%v row=%+v", found, baseline)
	}

	if _, found, err := target.GetTrafficCursor(ctx, "edge-1", "agent_total", ""); err != nil {
		t.Fatalf("GetTrafficCursor() error = %v", err)
	} else if found {
		t.Fatal("traffic cursor was copied, want skipped")
	}
	var events []AgentTrafficEventRow
	if err := target.db.Find(&events).Error; err != nil {
		t.Fatalf("query target events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("target events = %d, want 0", len(events))
	}
}
