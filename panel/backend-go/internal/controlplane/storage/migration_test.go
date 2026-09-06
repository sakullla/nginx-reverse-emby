//go:build exhaustive && integration

package storage

import (
	"reflect"
	"testing"
	"time"
)

func TestIntegrationCopyDefaultMigrationRowsPreservesScopedSecretRecovery(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			source, target := newTrafficTestStore(t, true), newTrafficTestStore(t, true)
			var operations []PluginScopedSecretOperationRow
			var deliveries []PluginScopedSecretDeliveryRow
			if populated {
				operations = []PluginScopedSecretOperationRow{{ID: "intent", SecretName: "safe-reference", Action: "rotate", OldVersion: "old", Fingerprint: "keyed-digest", State: "pending", CreatedAt: time.Now().UTC().Truncate(time.Second)}, {ID: "completed", SecretName: "other-reference", Action: "create", State: "completed", NewVersion: "created", Fingerprint: "other-keyed-digest"}}
				deliveries = []PluginScopedSecretDeliveryRow{{ID: "delivery", SecretName: "safe-reference", Version: "old", AgentID: "edge", InstanceID: "instance", PluginID: "plugin", GenerationID: "runtime", ProviderGenerationID: "provider", Revision: 7, FenceID: "intent"}, {ID: "acknowledged", SecretName: "safe-reference", Version: "old", AgentID: "other-edge", InstanceID: "instance", PluginID: "plugin", GenerationID: "other-runtime", ProviderGenerationID: "provider", Revision: 8, FenceID: "intent", Acknowledged: true}}
				if err := source.db.Create(&operations).Error; err != nil {
					t.Fatal(err)
				}
				if err := source.db.Create(&deliveries).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := CopyDefaultMigrationRows(t.Context(), source, target); err != nil {
				t.Fatal(err)
			}
			var gotOps, wantOps []PluginScopedSecretOperationRow
			var gotDeliveries, wantDeliveries []PluginScopedSecretDeliveryRow
			for _, pair := range []struct {
				store *GormStore
				rows  any
			}{{source, &wantOps}, {target, &gotOps}, {source, &wantDeliveries}, {target, &gotDeliveries}} {
				if err := pair.store.db.Order("id").Find(pair.rows).Error; err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(gotOps, wantOps) || !reflect.DeepEqual(gotDeliveries, wantDeliveries) {
				t.Fatal("migration changed scoped intent/recipient recovery authority")
			}
		})
	}
}

func TestIntegrationCopyDefaultMigrationRowsCopiesTrafficPolicyAndBaselineButSkipsHistory(t *testing.T) {
	t.Parallel()
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

// TestSchemaAddsDDNSAgentColumnsIdempotently locks in the dual-track migration
// (GORM AutoMigrate for fresh DBs + HasColumn-guarded ALTER for legacy SQLite):
// the four DDNS/liveness columns must appear after bootstrap, survive a second
// bootstrap run without error, and default to empty strings on fresh rows.
