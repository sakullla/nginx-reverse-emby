package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestPluginConsumerOwnershipFenceIsStableAndReverseIntegrated(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

func TestAgentGroupMoveRejectsParentlessRequiredPluginConsumer(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	packageDigest := strings.Repeat("a", 64)
	artifactDigest := strings.Repeat("b", 64)
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: "runtime.rpc-group-move", Version: "1.0.0", Name: "RPC Group Move",
		Runtime:         plugins.Runtime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "artifacts/linux-amd64/plugin"},
		Artifacts:       []plugins.Artifact{{Path: "artifacts/linux-amd64/plugin", SHA256: artifactDigest, Size: 42, Mode: "executable", GOOS: "linux", GOARCH: "amd64"}},
		ExtensionPoints: []string{"http.request"},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 100, MemoryBytes: 4096, Concurrency: 1, InputBytes: 1024, OutputBytes: 512},
		FailurePolicy:   plugins.FailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
		Signature:       plugins.Signature{Algorithm: "ed25519", KeyID: "release", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{
		Digest: packageDigest, PluginID: manifest.ID, Version: manifest.Version,
		SignatureFingerprint: strings.Repeat("c", 64), CachePath: t.TempDir(), ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now,
	}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	consumerOwner := ResourceBindingRow{ID: "parentless-rule-owner", ResourceKind: "http_rule", ResourceID: "edge:1", ResourceGroupID: "group-a", UpdatedAt: now}
	bindingsJSON, err := EncodePluginInstanceBindings([]PluginInstanceBinding{{
		Consumer:      PluginDependencyConsumer{Kind: "http_rule", ID: "1", ResourceGroupID: "group-a", Version: pluginDependencyConsumerOwnershipVersion(consumerOwner)},
		TargetAgentID: "edge",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{
		&ResourceGroupRow{ID: "group-a", Name: "A", CreatedAt: now, UpdatedAt: now},
		&ResourceGroupRow{ID: "group-b", Name: "B", CreatedAt: now, UpdatedAt: now},
		&AgentRow{ID: "edge", Name: "Edge", Platform: "linux-amd64", CapabilitiesJSON: `[]`},
		&AgentRow{ID: "free", Name: "Free", Platform: "linux-amd64", CapabilitiesJSON: `[]`},
		&ResourceBindingRow{ID: "edge-owner", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "group-a", UpdatedAt: now},
		&ResourceBindingRow{ID: "free-owner", ResourceKind: "agent", ResourceID: "free", ResourceGroupID: "group-a", UpdatedAt: now},
		&packageRow,
		&artifacts,
		&InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: packageDigest, ActivePackageIdentity: packageRow.Identity, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "operation-active", StateVersion: 1, InstalledAt: now, UpdatedAt: now},
		&PluginInstanceRow{ID: "required-provider", PluginID: manifest.ID, ResourceGroupID: "group-a", TargetJSON: `["edge"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: bindingsJSON, PendingBindingsJSON: `[]`, RollbackBindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 1, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now},
		&ResourceBindingRow{ID: "provider-owner", ResourceKind: "plugin_instance", ResourceID: "required-provider", ResourceGroupID: "group-a", ParentResourceKind: "agent", ParentResourceID: "edge", UpdatedAt: now},
		&HTTPRuleRow{ID: 1, AgentID: "edge", FrontendURL: "https://required.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true, Revision: 1},
		&consumerOwner,
	}
	for _, row := range rows {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.LoadAgentSnapshot(t.Context(), "edge", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil || len(before.PluginDependencies) != 1 {
		t.Fatalf("snapshot before Agent move dependencies=%+v err=%v", before.PluginDependencies, err)
	}
	move := ResourceBindingRow{ID: "ignored-edge-owner", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)}
	if err := store.BindResource(t.Context(), move); err == nil || !strings.Contains(err.Error(), ErrPluginDependencyConsumerInUse.Error()) {
		t.Fatalf("Agent move with parentless required consumer error = %v", err)
	}
	for kind, id := range map[string]string{"agent": "edge", "http_rule": "edge:1", "plugin_instance": "required-provider"} {
		owner, err := store.GetResourceBinding(t.Context(), kind, id)
		if err != nil || owner.ResourceGroupID != "group-a" {
			t.Fatalf("%s %s owner after rejected move = %+v err=%v", kind, id, owner, err)
		}
	}
	provider, found, err := store.GetPluginInstance(t.Context(), "required-provider")
	if err != nil || !found || provider.ResourceGroupID != "group-a" || provider.StateVersion != 1 {
		t.Fatalf("provider after rejected move found=%v row=%+v err=%v", found, provider, err)
	}
	after, err := store.LoadAgentSnapshot(t.Context(), "edge", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil || len(after.PluginDependencies) != 1 || after.PluginDependencies[0] != before.PluginDependencies[0] {
		t.Fatalf("snapshot after rejected Agent move dependencies=%+v err=%v", after.PluginDependencies, err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "ignored-free-owner", ResourceKind: "agent", ResourceID: "free", ResourceGroupID: "group-b", UpdatedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatalf("Agent move without plugin bindings: %v", err)
	}
	freeOwner, err := store.GetResourceBinding(t.Context(), "agent", "free")
	if err != nil || freeOwner.ResourceGroupID != "group-b" {
		t.Fatalf("unbound Agent owner after move = %+v err=%v", freeOwner, err)
	}
}

func TestAgentGroupMoveChecksEveryPluginBindingLifecycleFence(t *testing.T) {
	for _, field := range []string{"active", "pending", "rollback"} {
		t.Run(field, func(t *testing.T) {
			store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			now := time.Now().UTC()
			owner := ResourceBindingRow{ID: "parentless-owner", ResourceKind: "http_rule", ResourceID: "edge:1", ResourceGroupID: "group-a", UpdatedAt: now}
			bindingsJSON, err := EncodePluginInstanceBindings([]PluginInstanceBinding{{
				Consumer:      PluginDependencyConsumer{Kind: "http_rule", ID: "1", ResourceGroupID: "group-a", Version: pluginDependencyConsumerOwnershipVersion(owner)},
				TargetAgentID: "edge",
			}})
			if err != nil {
				t.Fatal(err)
			}
			instance := PluginInstanceRow{
				ID: "provider", PluginID: "runtime.rpc", ResourceGroupID: "group-a", TargetJSON: `["edge"]`,
				PolicyChainsJSON: `[]`, BindingsJSON: `[]`, PendingBindingsJSON: `[]`, RollbackBindingsJSON: `[]`,
				ConfigJSON: `{}`, PendingConfigJSON: `{}`, RollbackConfigJSON: `{}`, StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now,
			}
			switch field {
			case "active":
				instance.BindingsJSON = bindingsJSON
			case "pending":
				instance.PendingBindingsJSON = bindingsJSON
				instance.PendingOperationID = "operation"
				instance.PendingResourceGroupID = "group-a"
				instance.PendingTargetJSON = `["edge"]`
			case "rollback":
				instance.RollbackBindingsJSON = bindingsJSON
			}
			for _, row := range []any{
				&ResourceGroupRow{ID: "group-a", Name: "A", CreatedAt: now, UpdatedAt: now},
				&ResourceGroupRow{ID: "group-b", Name: "B", CreatedAt: now, UpdatedAt: now},
				&AgentRow{ID: "edge", Name: "Edge", CapabilitiesJSON: `[]`},
				&ResourceBindingRow{ID: "agent-owner", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "group-a", UpdatedAt: now},
				&HTTPRuleRow{ID: 1, AgentID: "edge", Enabled: true},
				&owner,
				&instance,
			} {
				if err := store.db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			err = store.BindResource(t.Context(), ResourceBindingRow{ID: "ignored", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)})
			if err == nil || !strings.Contains(err.Error(), ErrPluginDependencyConsumerInUse.Error()) {
				t.Fatalf("Agent move with %s binding error = %v", field, err)
			}
			agentOwner, err := store.GetResourceBinding(t.Context(), "agent", "edge")
			if err != nil || agentOwner.ResourceGroupID != "group-a" {
				t.Fatalf("Agent owner after rejected %s move = %+v err=%v", field, agentOwner, err)
			}
		})
	}
}
