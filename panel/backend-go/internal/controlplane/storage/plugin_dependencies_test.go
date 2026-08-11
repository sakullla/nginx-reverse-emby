package storage

import (
	"strings"
	"testing"
	"time"
)

func TestPluginConsumerOwnershipFenceIsStableAndReverseIntegrated(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, row := range []any{
		&ResourceGroupRow{ID: "group-a", Name: "A", CreatedAt: now, UpdatedAt: now},
		&ResourceGroupRow{ID: "group-b", Name: "B", CreatedAt: now, UpdatedAt: now},
		&AgentRow{ID: "edge", Name: "Edge", CapabilitiesJSON: `[]`},
		&HTTPRuleRow{ID: 1, AgentID: "edge", FrontendURL: "https://before.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true, Revision: 1},
	} {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "agent-owner", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "consumer-owner-v1", ResourceKind: "http_rule", ResourceID: "edge:1", ResourceGroupID: "group-a", ParentResourceKind: "agent", ParentResourceID: "edge", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	request := []PluginInstanceBindingRequest{{Consumer: PluginInstanceBindingConsumer{Kind: "http_rule", ID: "1"}, TargetAgentID: "edge"}}
	resolved, err := store.ResolvePluginInstanceBindingRequests(t.Context(), request, "group-a")
	if err != nil || len(resolved) != 1 || !ValidPluginDependencyConsumerVersion(resolved[0].Consumer.Version) {
		t.Fatalf("initial ownership fence = %+v err=%v", resolved, err)
	}
	versionV1 := resolved[0].Consumer.Version

	// SaveHTTPRules replaces the content row, but ownership remains the same.
	if err := store.SaveHTTPRules(t.Context(), "edge", []HTTPRuleRow{{ID: 1, FrontendURL: "https://after.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8097"}]`, Enabled: true, Revision: 2}}); err != nil {
		t.Fatal(err)
	}
	resolved, err = store.ResolvePluginInstanceBindingRequests(t.Context(), request, "group-a")
	if err != nil || resolved[0].Consumer.Version != versionV1 {
		t.Fatalf("ordinary rule update rotated ownership fence: %+v err=%v", resolved, err)
	}

	bindingsJSON, err := EncodePluginInstanceBindings(resolved)
	if err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{
		ID: "provider", PluginID: "runtime.rpc", ResourceGroupID: "group-a", TargetJSON: `["edge"]`, PolicyChainsJSON: `[]`,
		BindingsJSON: bindingsJSON, PendingBindingsJSON: bindingsJSON, RollbackBindingsJSON: bindingsJSON,
		ConfigJSON: `{}`, PendingConfigJSON: `{}`, RollbackConfigJSON: `{}`, PendingTargetJSON: `["edge"]`, PendingResourceGroupID: "group-a",
		ConfigVersion: 1, PendingVersion: 2, PendingOperationID: "operation", DesiredEnabled: true, CurrentState: "applying", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now,
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "ignored-new-id", ResourceKind: "http_rule", ResourceID: "edge:1", ResourceGroupID: "group-b", ParentResourceKind: "agent", ParentResourceID: "edge", UpdatedAt: now.Add(time.Second)}); err == nil || !strings.Contains(err.Error(), ErrPluginDependencyConsumerInUse.Error()) {
		t.Fatalf("referenced consumer rebind error = %v", err)
	}
	owner, err := store.GetResourceBinding(t.Context(), "http_rule", "edge:1")
	if err != nil || owner.ResourceGroupID != "group-a" {
		t.Fatalf("rejected rebind changed owner = %+v err=%v", owner, err)
	}

	quotaCtx := WithQuotaActor(t.Context(), QuotaActor{UserID: "system", Bootstrap: true})
	if _, err := store.ConsumeQuotaForResource(quotaCtx, "http_rule", "edge:1", "agent", "edge", "rule_count", -1); err != nil {
		t.Fatal(err)
	}
	stored, found, err := store.GetPluginInstance(t.Context(), instance.ID)
	if err != nil || !found {
		t.Fatalf("load detached instance found=%v err=%v", found, err)
	}
	for label, raw := range map[string]string{"active": stored.BindingsJSON, "pending": stored.PendingBindingsJSON, "rollback": stored.RollbackBindingsJSON} {
		bindings, err := CanonicalPluginInstanceBindings(raw)
		if err != nil || len(bindings) != 0 {
			t.Fatalf("%s bindings after consumer delete = %+v err=%v", label, bindings, err)
		}
	}
	if err := store.db.Where("agent_id = ? AND id = ?", "edge", 1).Delete(&HTTPRuleRow{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&HTTPRuleRow{ID: 1, AgentID: "edge", FrontendURL: "https://recreated.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8098"}]`, Enabled: true, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "consumer-owner-v2", ResourceKind: "http_rule", ResourceID: "edge:1", ResourceGroupID: "group-a", ParentResourceKind: "agent", ParentResourceID: "edge", UpdatedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	resolved, err = store.ResolvePluginInstanceBindingRequests(t.Context(), request, "group-a")
	if err != nil || resolved[0].Consumer.Version == versionV1 {
		t.Fatalf("recreated consumer ownership fence = %+v err=%v", resolved, err)
	}
}

func TestResolvePluginConsumerOwnershipRejectsCrossGroup(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.db.Create(&HTTPRuleRow{ID: 7, AgentID: "edge", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ResourceBindingRow{ID: "foreign-owner", ResourceKind: "http_rule", ResourceID: "edge:7", ResourceGroupID: "group-b", ParentResourceKind: "agent", ParentResourceID: "edge", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = store.ResolvePluginInstanceBindingRequests(t.Context(), []PluginInstanceBindingRequest{{Consumer: PluginInstanceBindingConsumer{Kind: "http_rule", ID: "7"}, TargetAgentID: "edge"}}, "group-a")
	if err == nil || !strings.Contains(err.Error(), "not provider group group-a") {
		t.Fatalf("cross-group consumer ownership error = %v", err)
	}
}
