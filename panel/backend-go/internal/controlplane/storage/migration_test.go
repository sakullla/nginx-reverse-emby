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

func TestCopyDefaultMigrationRowsCopiesEgressProfiles(t *testing.T) {
	ctx := t.Context()
	source := newTrafficTestStore(t, true)
	target := newTrafficTestStore(t, true)

	if err := source.SaveEgressProfiles(ctx, []EgressProfileRow{
		{
			ID:                  41,
			Name:                "wg exit",
			Type:                "wireguard",
			WireGuardConfigJSON: `{"private_key":"secret","addresses":["10.10.0.2/32"],"peers":[]}`,
			Enabled:             false,
			Description:         "lab",
			Revision:            7,
		},
		{
			ID:          42,
			Name:        "source socks",
			Type:        "socks",
			ProxyURL:    "socks5://source:1080",
			Enabled:     true,
			Description: "source",
			Revision:    8,
		},
	}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := target.SaveEgressProfiles(ctx, []EgressProfileRow{
		{
			ID:          42,
			Name:        "stale socks",
			Type:        "socks",
			ProxyURL:    "socks5://stale:1080",
			Enabled:     false,
			Description: "stale",
			Revision:    1,
		},
		{
			ID:          99,
			Name:        "target only",
			Type:        "http",
			ProxyURL:    "http://target-only:8080",
			Enabled:     false,
			Description: "target",
			Revision:    2,
		},
	}); err != nil {
		t.Fatalf("SaveEgressProfiles(target) error = %v", err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatalf("CopyDefaultMigrationRows() error = %v", err)
	}

	got, err := target.ListEgressProfiles(ctx)
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("target egress profiles = %+v, want copied and target-only profiles", got)
	}
	if got[0].ID != 41 || got[0].WireGuardConfigJSON == "" || got[0].Enabled {
		t.Fatalf("target egress profiles = %+v, want copied disabled profile with config", got)
	}
	if got[1].ID != 42 || got[1].Name != "source socks" || got[1].ProxyURL != "socks5://source:1080" || !got[1].Enabled {
		t.Fatalf("target egress profiles = %+v, want source profile to upsert over stale target profile", got)
	}
	if got[2].ID != 99 || got[2].Name != "target only" || got[2].Enabled {
		t.Fatalf("target egress profiles = %+v, want target-only disabled profile preserved", got)
	}
}
