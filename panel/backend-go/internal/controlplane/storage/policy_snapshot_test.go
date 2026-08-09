package storage

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"gorm.io/gorm"
)

func TestPolicySnapshotProjectionPreservesAttachmentsAndExplicitEmptyDefinitions(t *testing.T) {
	httpRules := snapshotHTTPRules([]HTTPRuleRow{{
		ID: 1, AgentID: "edge-1", FrontendURL: "https://media.example.test",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true,
		TrustedProxyRangesJSON: `["10.0.0.0/8"]`, PolicyRefJSON: `{"id":"shared-policy","overlay":{"site":"media"}}`,
	}}, false)
	l4Rules := snapshotL4Rules([]L4RuleRow{{
		ID: 2, AgentID: "edge-1", Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 9000,
		BackendsJSON: `[{"host":"127.0.0.1","port":9001}]`, Enabled: true,
		TuningJSON:    `{"proxy_protocol":{"decode":true,"trusted_peers":["192.0.2.0/24"]}}`,
		PolicyRefJSON: `{"id":"shared-policy"}`,
	}}, false)
	if len(httpRules) != 1 || httpRules[0].PolicyRef == nil || httpRules[0].PolicyRef.ID != "shared-policy" || string(httpRules[0].PolicyRef.Overlay) != `{"site":"media"}` {
		t.Fatalf("HTTP policy projection = %+v", httpRules)
	}
	if len(httpRules[0].TrustedProxyRanges) != 1 || httpRules[0].TrustedProxyRanges[0] != "10.0.0.0/8" {
		t.Fatalf("HTTP trusted proxy projection = %+v", httpRules[0].TrustedProxyRanges)
	}
	if len(l4Rules) != 1 || l4Rules[0].PolicyRef == nil || l4Rules[0].PolicyRef.ID != "shared-policy" {
		t.Fatalf("L4 policy projection = %+v", l4Rules)
	}
	if peers := l4Rules[0].Tuning.ProxyProtocol.TrustedPeers; len(peers) != 1 || peers[0] != "192.0.2.0/24" {
		t.Fatalf("L4 trusted peer projection = %+v", peers)
	}

	payload, err := json.Marshal(Snapshot{Rules: httpRules, L4Rules: l4Rules, PluginPolicies: []PluginPolicy{}})
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot) error = %v", err)
	}
	if !strings.Contains(string(payload), `"plugin_policies":[]`) {
		t.Fatalf("snapshot does not carry explicit empty plugin_policies: %s", payload)
	}
}

func TestMalformedPersistedPolicyRefFailsClosedInSnapshot(t *testing.T) {
	for _, refJSON := range []string{`{"id":`, `{"id":" shared-policy"}`, `{"id":"shared-policy\n"}`} {
		rules := snapshotHTTPRules([]HTTPRuleRow{{
			ID: 1, FrontendURL: "https://media.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`,
			Enabled: true, PolicyRefJSON: refJSON,
		}}, false)
		if len(rules) != 1 || rules[0].PolicyRef == nil || !strings.Contains(rules[0].PolicyRef.ID, "invalid") {
			t.Fatalf("malformed policy ref %q was silently dropped: %+v", refJSON, rules)
		}
	}
}

func TestActivePluginPolicyProjectsFromVerifiedDurableState(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(cacheRoot)
	})

	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	staging := t.TempDir()
	digest := writeMigrationVerifiedPackage(t, staging, "official.waf", signingKey)
	publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trust := marketplace.SignatureTrust{
		SourceID: "snapshot-source", SourceKind: marketplace.SourceKindCustom,
		KeyID: "community-release", PublicKey: publicKey, Fingerprint: fingerprint,
	}
	validator, err := marketplace.ValidatorForSignatureTrust(trust)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validator.ValidatePackage(staging, plugins.PackageExpectation{ID: "official.waf", Version: "1.0.0", SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.ImportVerifiedPackage(cacheRoot, validated, validator, trust)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := json.Marshal(validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{
		Digest: digest, PluginID: validated.Manifest.ID, Version: validated.Manifest.Version,
		SourceID: trust.SourceID, SourceKind: trust.SourceKind, SourceRiskLabel: marketplace.UntrustedRiskLabel,
		SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: cachePath,
		ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: time.Now().UTC(),
	}, validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	for index := range artifacts {
		if err := store.db.Create(&artifacts[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	installed := InstalledPluginRow{
		PluginID: packageRow.PluginID, ActivePackageDigest: packageRow.Digest, ActivePackageIdentity: packageRow.Identity,
		RuntimeKind: packageRow.RuntimeKind, RuntimeABI: packageRow.RuntimeABI, HostScope: packageRow.HostScope,
		ActiveSourceID: packageRow.SourceID, ActiveSourceKind: packageRow.SourceKind, ActiveSourceRiskLabel: packageRow.SourceRiskLabel,
		ActiveSignatureKeyID: packageRow.SignatureKeyID, ActiveSignaturePublicKey: packageRow.SignaturePublicKey,
		ActiveSignatureFingerprint: packageRow.SignatureFingerprint, DesiredLifecycle: "enabled", CurrentLifecycle: "active",
		CleanupPolicyJSON: `{}`, LastOperationID: "enable-policy", StateVersion: 7, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{
		ID: "waf-main", PluginID: packageRow.PluginID, ResourceGroupID: "default", TargetJSON: `["edge-policy"]`,
		PolicyChainsJSON: `["shared-policy"]`,
		ConfigJSON:       `{"mode":"block"}`, ConfigVersion: 3, DesiredEnabled: false, CurrentState: "disabled",
		StatusSummaryJSON: `{}`, StateVersion: 5, UpdatedAt: now,
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&PluginGrantRow{
		ID: "waf-grant", GrantKey: "waf-grant-key", PluginID: packageRow.PluginID,
		PackageDigest: packageRow.Digest, PackageIdentity: packageRow.Identity,
		Permission: "http.inspect", GrantedBy: "admin", GrantedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-policy", AgentToken: "edge-policy-token", CapabilitiesJSON: `["http_rules","package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.LoadAgentSnapshot(ctx, "edge-policy", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PluginPolicies) != 1 || snapshot.PluginPolicies[0].ID != "shared-policy" || len(snapshot.PluginPolicies[0].Stages) != 1 {
		t.Fatalf("plugin policies = %+v", snapshot.PluginPolicies)
	}
	stage := snapshot.PluginPolicies[0].Stages[0]
	if stage.Kind != "waf" || stage.PluginID != packageRow.PluginID || stage.PackageDigest != digest ||
		stage.ArtifactDigest != artifacts[0].SHA256 || stage.SignerKeyID != trust.KeyID || stage.SignerFingerprint != fingerprint ||
		!stage.SignatureVerified || string(stage.Config) != instance.ConfigJSON {
		t.Fatalf("projected policy stage = %+v", stage)
	}
	if stage.ArtifactPath != "" {
		t.Fatalf("remote artifact path = %q, want empty", stage.ArtifactPath)
	}
	if stage.ArtifactSource.ArtifactID != artifacts[0].ID || stage.ArtifactSource.PackageIdentity != packageRow.Identity ||
		stage.ArtifactSource.RelativePath != artifacts[0].Path || stage.ArtifactSource.SHA256 != artifacts[0].SHA256 ||
		stage.ArtifactSource.SizeBytes != artifacts[0].SizeBytes {
		t.Fatalf("artifact source = %+v", stage.ArtifactSource)
	}
	if len(stage.GrantedScopes) != 1 || stage.GrantedScopes[0] != "http.inspect" || stage.ResourceBudget.TimeoutMS != 2 {
		t.Fatalf("projected grants/budget = %+v / %+v", stage.GrantedScopes, stage.ResourceBudget)
	}
}

func TestAuthoritativePolicyCatalogGroupsSharedChainsAndUsesGlobalAgentRevision(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(ctx, AgentRow{ID: agentID, AgentToken: agentID + "-token", CapabilitiesJSON: `[]`}); err != nil {
			t.Fatal(err)
		}
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instances := make(map[string]PluginInstanceRow)
	for _, fixture := range []struct {
		pluginID, kind, instanceID, chains string
	}{
		{"policy.ip", "ip", "ip-main", `["chain-full","chain-lite"]`},
		{"policy.rate", "rate", "rate-main", `["chain-lite","chain-full"]`},
		{"policy.waf", "waf", "waf-main", `["chain-full"]`},
	} {
		instance := installActivePolicyFixture(t, store, signingKey, fixture.pluginID, fixture.kind, fixture.instanceID, `["edge-a"]`, fixture.chains)
		instances[fixture.kind] = instance
	}
	if err := store.db.Create(&AgentRevisionPointerRow{AgentID: "edge-a", DesiredRevision: 40, AppliedRevision: 39, LastKnownGoodRevision: 39, UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}

	policies, err := store.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 2 || policies[0].ID != "chain-full" || policies[1].ID != "chain-lite" {
		t.Fatalf("policies = %+v", policies)
	}
	if got := policyKinds(policies[0]); strings.Join(got, ",") != "ip,rate,waf" {
		t.Fatalf("full chain kinds = %v", got)
	}
	if got := policyKinds(policies[1]); strings.Join(got, ",") != "ip,rate" {
		t.Fatalf("lite chain kinds = %v", got)
	}
	if policies[0].Stages[0].ArtifactPath != "" || policies[0].Stages[0].ArtifactSource.ArtifactID == "" {
		t.Fatalf("remote artifact projection = %+v", policies[0].Stages[0])
	}
	if policies[0].Stages[0].PolicyID != "ip-main" || policies[0].Stages[0].InstanceID != "ip-main" {
		t.Fatalf("shared stage authority = %+v", policies[0].Stages[0])
	}
	if !reflect.DeepEqual(policies[0].Stages[0], policies[1].Stages[0]) {
		t.Fatalf("shared instance differs across containing chains: full=%+v lite=%+v", policies[0].Stages[0], policies[1].Stages[0])
	}

	instance := instances["waf"]
	instance.ConfigJSON = `{"mode":"observe"}`
	instance.ConfigVersion++
	installed, found, err := store.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	now := time.Now().UTC()
	if err := store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: instance.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed, ReplaceInstance: &instance,
		Operation: PluginOperationRow{ID: "configure-waf", PluginID: instance.PluginID, Kind: "configure", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: "configure-waf-audit", Action: "plugin.configure", TargetKind: "plugin", TargetID: instance.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	policies, err = store.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if policies[0].Revision != 41 || policies[1].Revision != 41 {
		t.Fatalf("catalog revisions = %d/%d, want global floor 41", policies[0].Revision, policies[1].Revision)
	}
	snapshot, err := store.LoadAgentSnapshot(ctx, "edge-a", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 41 {
		t.Fatalf("snapshot revision = %d, want 41", snapshot.Revision)
	}

	mismatched := instances["rate"]
	mismatched.TargetJSON = `["edge-b"]`
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", mismatched.ID).Update("target_json", mismatched.TargetJSON).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadAgentPluginPolicies(ctx, "edge-a"); err == nil || !strings.Contains(err.Error(), "incompatible target sets") {
		t.Fatalf("incompatible chain targets error = %v", err)
	}
}

func TestPluginPolicyIdentityAndRestoreBoundariesRejectNonCanonicalValues(t *testing.T) {
	for _, value := range []string{" policy", "policy ", "policy\r", "policy\n", "policy\x00", strings.Repeat("x", 513)} {
		if err := ValidatePluginPolicyIdentity(value); err == nil {
			t.Fatalf("ValidatePluginPolicyIdentity(%q) succeeded", value)
		}
	}
	if _, err := CanonicalPluginPolicyChains(`["chain-a"," chain-b"]`); err == nil {
		t.Fatal("non-canonical chain identity was accepted")
	}

	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close(); _ = target.Close() })
	row := PluginInstanceRow{ID: "bad\ninstance", PluginID: "policy.ip", ResourceGroupID: "default", TargetJSON: `["local"]`, PolicyChainsJSON: `["chain-a"]`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", UpdatedAt: time.Now().UTC()}
	if err := source.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := copyRows(t.Context(), source, target, &PluginInstanceRow{}); err == nil || !strings.Contains(err.Error(), "policy identity") {
		t.Fatalf("copyRows malformed identity error = %v", err)
	}
}

func TestPluginMutationRejectsDanglingPolicyReferencesInSameTransaction(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-a", AgentToken: "token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instance := installActivePolicyFixture(t, store, signingKey, "policy.single", "ip", "ip-only", `["edge-a"]`, `["shared"]`)
	if err := store.db.Create(&HTTPRuleRow{ID: 1, AgentID: "edge-a", FrontendURL: "https://example.test", BackendsJSON: `[]`, Enabled: true, PolicyRefJSON: `{"id":"shared"}`, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	installed, found, err := store.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	now := time.Now().UTC()
	pending := instance
	pending.PendingConfigJSON = pending.ConfigJSON
	pending.PendingVersion = pending.ConfigVersion + 1
	pending.PendingTargetJSON = pending.TargetJSON
	pending.PendingPolicyChainsJSON = `[]`
	pending.PendingOperationID = "configure-remove-chain"
	installed.PendingOperationID = pending.PendingOperationID
	installed.PendingKind = "configure"
	err = store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed, ReplaceInstance: &pending,
		Operation: PluginOperationRow{ID: pending.PendingOperationID, PluginID: installed.PluginID, Kind: "configure", Status: "applying", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: "configure-remove-chain-audit", Action: "plugin.configure", TargetKind: "plugin", TargetID: installed.PluginID, Result: "accepted", MetadataJSON: `{}`, CreatedAt: now},
	})
	if !errors.Is(err, ErrPluginConflict) || !strings.Contains(err.Error(), "would become unavailable") {
		t.Fatalf("prospective configure error = %v", err)
	}
	persistedInstance, found, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !found || persistedInstance.PendingOperationID != "" {
		t.Fatalf("rolled-back pending instance = %+v, %v, %v", persistedInstance, found, err)
	}

	installed, found, err = store.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin(after prospective rollback) = %v, %v", found, err)
	}
	installed.DesiredLifecycle = "disabled"
	installed.CurrentLifecycle = "disabled"
	err = store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed,
		Operation: PluginOperationRow{ID: "disable", PluginID: installed.PluginID, Kind: "disable", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: "disable-audit", Action: "plugin.disable", TargetKind: "plugin", TargetID: installed.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	})
	if !errors.Is(err, ErrPluginConflict) || !strings.Contains(err.Error(), "would become unavailable") {
		t.Fatalf("disable error = %v", err)
	}
	persisted, found, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !found || persisted.CurrentLifecycle != "active" {
		t.Fatalf("rolled-back installed state = %+v, %v, %v", persisted, found, err)
	}
}

func TestTransactionalPluginPolicyCatalogReadDoesNotCreateFence(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.WithRevisionMutation(t.Context(), func(tx *GormStore) (RevisionMutationDecision, error) {
		policies, err := tx.LoadAgentPluginPolicies(t.Context(), "new-agent")
		if err != nil {
			return RevisionMutationDecision{}, err
		}
		if len(policies) != 0 {
			t.Fatalf("new Agent policies = %+v, want empty", policies)
		}
		return RevisionMutationDecision{}, nil
	}); err != nil {
		t.Fatalf("transactional catalog read error = %v", err)
	}

	var count int64
	if err := store.db.Model(&PluginPolicyAgentRevisionRow{}).Where("agent_id = ?", "new-agent").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("transactional catalog read created %d fence rows, want 0", count)
	}
}

func TestStandalonePluginPolicyCatalogReadUsesOneSQLiteSnapshot(t *testing.T) {
	tests := []struct {
		name string
		read func(context.Context, *GormStore, string) ([]PluginPolicy, error)
	}{
		{name: "catalog API", read: func(ctx context.Context, store *GormStore, agentID string) ([]PluginPolicy, error) {
			return store.LoadAgentPluginPolicies(ctx, agentID)
		}},
		{name: "heartbeat intent snapshot", read: func(ctx context.Context, store *GormStore, agentID string) ([]PluginPolicy, error) {
			snapshot, err := store.LoadAgentIntentSnapshot(ctx, agentID, AgentSnapshotInput{})
			return snapshot.PluginPolicies, err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testStandalonePluginPolicyCatalogReadUsesOneSQLiteSnapshot(t, test.read)
		})
	}
}

func testStandalonePluginPolicyCatalogReadUsesOneSQLiteSnapshot(t *testing.T, readCatalog func(context.Context, *GormStore, string) ([]PluginPolicy, error)) {
	t.Helper()
	dataRoot := t.TempDir()
	reader, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	testStandalonePluginPolicyCatalogReadUsesOneSnapshot(t, reader, writer, "sqlite", readCatalog)
}

func testStandalonePluginPolicyCatalogReadUsesOneSnapshot(t *testing.T, reader, writer *GormStore, operationPrefix string, readCatalog func(context.Context, *GormStore, string) ([]PluginPolicy, error)) {
	t.Helper()
	ctx := t.Context()
	if err := reader.SaveAgent(ctx, AgentRow{ID: "edge-a", AgentToken: "token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instance := installActivePolicyFixture(t, reader, signingKey, "policy.snapshot", "ip", "snapshot-ip", `["edge-a"]`, `["shared"]`)
	before, err := reader.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(before[0].Stages) != 1 {
		t.Fatalf("before catalog = %+v", before)
	}

	candidate, artifacts := preparePolicyPackageFixture(t, reader, signingKey, instance.PluginID, "ip", 32768)
	installed, found, err := reader.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	installed.ActivePackageDigest, installed.ActivePackageIdentity = candidate.Digest, candidate.Identity
	installed.RuntimeKind, installed.RuntimeABI, installed.HostScope = candidate.RuntimeKind, candidate.RuntimeABI, candidate.HostScope
	installed.ActiveSourceID, installed.ActiveSourceKind, installed.ActiveSourceRiskLabel = candidate.SourceID, candidate.SourceKind, candidate.SourceRiskLabel
	installed.ActiveSignatureKeyID, installed.ActiveSignaturePublicKey, installed.ActiveSignatureFingerprint = candidate.SignatureKeyID, candidate.SignaturePublicKey, candidate.SignatureFingerprint
	instance.ConfigJSON = `{"generation":2}`
	instance.ConfigVersion++
	now := time.Now().UTC()
	mutation := PluginMutation{
		PluginID: instance.PluginID, ExpectedActive: before[0].Stages[0].PackageDigest, ExpectedStateVersion: installed.StateVersion,
		Package: &candidate, Artifacts: artifacts, Installed: &installed, ReplaceInstance: &instance,
		Operation: PluginOperationRow{ID: operationPrefix + "-snapshot-upgrade", PluginID: instance.PluginID, Kind: "upgrade", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: operationPrefix + "-snapshot-upgrade-audit", Action: "plugin.upgrade", TargetKind: "plugin", TargetID: instance.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	}

	firstInstanceRead := make(chan struct{})
	continueRead := make(chan struct{})
	var intercept sync.Once
	const callbackName = "test:standalone-policy-snapshot"
	if err := reader.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != "plugin_instances" {
			return
		}
		intercept.Do(func() {
			close(firstInstanceRead)
			<-continueRead
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.db.Callback().Query().Remove(callbackName) })

	readDone := make(chan struct {
		policies []PluginPolicy
		err      error
	}, 1)
	go func() {
		policies, err := readCatalog(ctx, reader, "edge-a")
		readDone <- struct {
			policies []PluginPolicy
			err      error
		}{policies: policies, err: err}
	}()
	select {
	case <-firstInstanceRead:
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("catalog reader did not reach the deterministic query boundary")
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- writer.ApplyPluginMutation(ctx, mutation) }()
	select {
	case err := <-writeDone:
		if err != nil {
			close(continueRead)
			t.Fatalf("concurrent upgrade error = %v", err)
		}
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatalf("concurrent %s upgrade could not commit while the read snapshot was open", operationPrefix)
	}
	close(continueRead)
	read := <-readDone
	if read.err != nil {
		t.Fatalf("standalone catalog read error = %v", read.err)
	}
	after, err := reader.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}

	catalogGeneration := func(policies []PluginPolicy) string {
		if len(policies) != 1 || len(policies[0].Stages) != 1 {
			return fmt.Sprintf("invalid:%+v", policies)
		}
		stage := policies[0].Stages[0]
		return stage.PackageDigest + "\x00" + string(stage.Config)
	}
	got := catalogGeneration(read.policies)
	wantBefore, wantAfter := catalogGeneration(before), catalogGeneration(after)
	if got != wantBefore && got != wantAfter {
		t.Fatalf("standalone catalog read produced a hybrid generation %q; before=%q after=%q", got, wantBefore, wantAfter)
	}
	if wantBefore == wantAfter {
		t.Fatalf("fixture did not create distinct catalog generations: %q", wantBefore)
	}
}

func TestRuleCatalogFenceSerializesReferenceCreateAndDeleteWithPluginLifecycle(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-a", AgentToken: "token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instance := installActivePolicyFixture(t, store, signingKey, "policy.fenced", "ip", "ip-fenced", `["edge-a"]`, `["shared"]`)
	if err := store.EnsureAgentPluginPolicyCatalog(ctx, "edge-a"); err != nil {
		t.Fatalf("EnsureAgentPluginPolicyCatalog() error = %v", err)
	}

	installed, found, err := store.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	disable := func(operationID string) error {
		candidate := installed
		candidate.DesiredLifecycle = "disabled"
		candidate.CurrentLifecycle = "disabled"
		now := time.Now().UTC()
		return store.ApplyPluginMutation(ctx, PluginMutation{
			PluginID: candidate.PluginID, ExpectedActive: candidate.ActivePackageDigest, ExpectedStateVersion: candidate.StateVersion,
			Installed: &candidate,
			Operation: PluginOperationRow{ID: operationID, PluginID: candidate.PluginID, Kind: "disable", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
			Audit:     AuditEventRow{ID: operationID + "-audit", Action: "plugin.disable", TargetKind: "plugin", TargetID: candidate.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
		})
	}

	locked := make(chan struct{})
	allowCommit := make(chan struct{})
	ruleDone := make(chan error, 1)
	go func() {
		ruleDone <- store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
			if err := tx.LockAgentPluginPolicyCatalog(ctx, "edge-a"); err != nil {
				return RevisionMutationDecision{}, err
			}
			policies, err := tx.LoadAgentPluginPolicies(ctx, "edge-a")
			if err != nil {
				return RevisionMutationDecision{}, err
			}
			if len(policies) != 1 || policies[0].ID != "shared" {
				return RevisionMutationDecision{}, fmt.Errorf("locked catalog = %+v", policies)
			}
			close(locked)
			<-allowCommit
			return RevisionMutationDecision{}, tx.SaveHTTPRules(ctx, "edge-a", []HTTPRuleRow{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://fenced.example.test", BackendsJSON: `[]`,
				Enabled: true, PolicyRefJSON: `{"id":"shared"}`, Revision: 1,
			}})
		})
	}()
	<-locked
	disableDone := make(chan error, 1)
	go func() { disableDone <- disable("disable-behind-create") }()
	close(allowCommit)
	if err := <-ruleDone; err != nil {
		t.Fatalf("fenced rule create error = %v", err)
	}
	if err := <-disableDone; !errors.Is(err, ErrPluginConflict) || !strings.Contains(err.Error(), "would become unavailable") {
		t.Fatalf("plugin disable behind rule create error = %v", err)
	}

	locked = make(chan struct{})
	allowCommit = make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
			if err := tx.LockAgentPluginPolicyCatalog(ctx, "edge-a"); err != nil {
				return RevisionMutationDecision{}, err
			}
			close(locked)
			<-allowCommit
			return RevisionMutationDecision{}, tx.SaveHTTPRules(ctx, "edge-a", nil)
		})
	}()
	<-locked
	disableDone = make(chan error, 1)
	go func() { disableDone <- disable("disable-behind-delete") }()
	close(allowCommit)
	if err := <-deleteDone; err != nil {
		t.Fatalf("fenced rule delete error = %v", err)
	}
	if err := <-disableDone; err != nil {
		t.Fatalf("plugin disable behind rule delete error = %v", err)
	}
}

func TestPluginMutationRetargetAndDeletePreserveUnrelatedAgentCatalog(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(ctx, AgentRow{ID: agentID, AgentToken: agentID + "-token", CapabilitiesJSON: `[]`}); err != nil {
			t.Fatal(err)
		}
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	moving := installActivePolicyFixture(t, store, signingKey, "policy.moving", "ip", "moving-ip", `["edge-a"]`, `["moving"]`)
	installActivePolicyFixture(t, store, signingKey, "policy.keep", "rate", "keep-rate", `["edge-a","edge-b"]`, `["keep"]`)
	for index, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.db.Create(&HTTPRuleRow{
			ID: index + 1, AgentID: agentID, FrontendURL: "https://example.test", BackendsJSON: `[]`, Enabled: true,
			PolicyRefJSON: `{"id":"keep"}`, Revision: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	installed, found, err := store.GetInstalledPlugin(ctx, moving.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	moving.TargetJSON = `["edge-b"]`
	now := time.Now().UTC()
	if err := store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: moving.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed, ReplaceInstance: &moving,
		Operation: PluginOperationRow{ID: "retarget-moving", PluginID: moving.PluginID, Kind: "configure", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: "retarget-moving-audit", Action: "plugin.configure", TargetKind: "plugin", TargetID: moving.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	edgeA, err := store.LoadAgentPluginPolicies(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(edgeA) != 1 || edgeA[0].ID != "keep" {
		t.Fatalf("edge-a catalog = %+v, want unrelated keep chain", edgeA)
	}
	edgeB, err := store.LoadAgentPluginPolicies(ctx, "edge-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(edgeB) != 2 || edgeB[0].ID != "keep" || edgeB[1].ID != "moving" {
		t.Fatalf("edge-b catalog = %+v, want keep and retargeted moving chains", edgeB)
	}

	installed, found, err = store.GetInstalledPlugin(ctx, moving.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin(after retarget) = %v, %v", found, err)
	}
	if err := store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: moving.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		DeletePlugin: true, DeleteInstances: true, DeleteGrants: true,
		Operation: PluginOperationRow{ID: "delete-moving", PluginID: moving.PluginID, Kind: "uninstall", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now.Add(time.Second)},
		Audit:     AuditEventRow{ID: "delete-moving-audit", Action: "plugin.uninstall", TargetKind: "plugin", TargetID: moving.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now.Add(time.Second)},
	}); err != nil {
		t.Fatal(err)
	}
	edgeB, err = store.LoadAgentPluginPolicies(ctx, "edge-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(edgeB) != 1 || edgeB[0].ID != "keep" {
		t.Fatalf("edge-b catalog after delete = %+v, want unrelated keep chain", edgeB)
	}
}

func TestProspectiveUpgradeRejectsPolicyOverlayBudgetShrinkAndRollsBack(t *testing.T) {
	ctx := t.Context()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-a", AgentToken: "token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instance := installActivePolicyFixture(t, store, signingKey, "policy.budget", "ip", "ip-budget", `["edge-a"]`, `["shared"]`)
	overlay := `{"payload":"` + strings.Repeat("x", 512) + `"}`
	if err := store.db.Create(&HTTPRuleRow{ID: 1, AgentID: "edge-a", FrontendURL: "https://example.test", BackendsJSON: `[]`, Enabled: true, PolicyRefJSON: `{"id":"shared","overlay":` + overlay + `}`, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	candidate, artifacts := preparePolicyPackageFixture(t, store, signingKey, instance.PluginID, "ip", 300)
	installed, found, err := store.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	installed.StagedPackageDigest, installed.StagedPackageIdentity = candidate.Digest, candidate.Identity
	installed.StagedSourceID, installed.StagedSourceKind, installed.StagedSourceRiskLabel = candidate.SourceID, candidate.SourceKind, candidate.SourceRiskLabel
	installed.StagedSignatureKeyID, installed.StagedSignaturePublicKey, installed.StagedSignatureFingerprint = candidate.SignatureKeyID, candidate.SignaturePublicKey, candidate.SignatureFingerprint
	installed.CurrentLifecycle, installed.PendingOperationID, installed.PendingKind = "upgrading", "upgrade-budget", "upgrade"
	instance.PendingConfigJSON, instance.PendingVersion = instance.ConfigJSON, instance.ConfigVersion+1
	instance.PendingPolicyChainsJSON, instance.PendingOperationID = instance.PolicyChainsJSON, installed.PendingOperationID
	now := time.Now().UTC()
	err = store.ApplyPluginMutation(ctx, PluginMutation{
		PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed, Package: &candidate, Artifacts: artifacts, ReplaceInstances: []PluginInstanceRow{instance},
		Operation: PluginOperationRow{ID: installed.PendingOperationID, PluginID: installed.PluginID, Kind: "upgrade", Status: "staged", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: "upgrade-budget-audit", Action: "plugin.upgrade", TargetKind: "plugin", TargetID: installed.PluginID, Result: "accepted", MetadataJSON: `{}`, CreatedAt: now},
	})
	if !errors.Is(err, ErrPluginConflict) || !strings.Contains(err.Error(), "overlay budget") {
		t.Fatalf("budget shrink error = %v", err)
	}
	persisted, found, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !found || persisted.StagedPackageDigest != "" || persisted.ActivePackageDigest == candidate.Digest {
		t.Fatalf("rolled-back upgrade state = %+v, %v, %v", persisted, found, err)
	}
	if _, found, err := store.GetPluginPackageByIdentity(ctx, candidate.Identity); err != nil || found {
		t.Fatalf("rolled-back candidate package = %v, %v", found, err)
	}
}

func policyKinds(policy PluginPolicy) []string {
	result := make([]string, 0, len(policy.Stages))
	for _, stage := range policy.Stages {
		result = append(result, stage.Kind)
	}
	return result
}

func installActivePolicyFixture(t *testing.T, store *GormStore, signingKey ed25519.PrivateKey, pluginID, kind, instanceID, targetsJSON, chainsJSON string) PluginInstanceRow {
	t.Helper()
	packageRow, artifacts := preparePolicyPackageFixture(t, store, signingKey, pluginID, kind, 65536)
	if err := store.db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	installed := InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: packageRow.Digest, ActivePackageIdentity: packageRow.Identity,
		RuntimeKind: packageRow.RuntimeKind, RuntimeABI: packageRow.RuntimeABI, HostScope: packageRow.HostScope,
		ActiveSourceID: packageRow.SourceID, ActiveSourceKind: packageRow.SourceKind, ActiveSourceRiskLabel: packageRow.SourceRiskLabel,
		ActiveSignatureKeyID: packageRow.SignatureKeyID, ActiveSignaturePublicKey: packageRow.SignaturePublicKey,
		ActiveSignatureFingerprint: packageRow.SignatureFingerprint, DesiredLifecycle: "enabled", CurrentLifecycle: "active",
		CleanupPolicyJSON: `{}`, LastOperationID: "enable-" + instanceID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{
		ID: instanceID, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: targetsJSON, PolicyChainsJSON: chainsJSON,
		ConfigJSON: `{}`, ConfigVersion: 1, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now,
	}
	if err := normalizePluginInstancePolicyChains(&instance); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&PluginGrantRow{ID: "grant-" + instanceID, GrantKey: "grant-key-" + instanceID, PluginID: pluginID, PackageDigest: packageRow.Digest, PackageIdentity: packageRow.Identity, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return instance
}

func preparePolicyPackageFixture(t *testing.T, store *GormStore, signingKey ed25519.PrivateKey, pluginID, kind string, inputBytes int64) (PluginPackageRow, []PluginArtifactRow) {
	t.Helper()
	staging := t.TempDir()
	digest := writeMigrationVerifiedPolicyPackageWithBudget(t, staging, pluginID, kind, "http.request", inputBytes, signingKey)
	publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trust := marketplace.SignatureTrust{SourceID: "snapshot-source", SourceKind: marketplace.SourceKindCustom, KeyID: "community-release", PublicKey: publicKey, Fingerprint: fingerprint}
	validator, err := marketplace.ValidatorForSignatureTrust(trust)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := validator.ValidatePackage(staging, plugins.PackageExpectation{ID: pluginID, Version: "1.0.0", SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.ImportVerifiedPackage(filepath.Join(store.dataRoot, "plugins", "packages"), validated, validator, trust)
	if err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := json.Marshal(validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{
		Digest: digest, PluginID: pluginID, Version: "1.0.0", SourceID: trust.SourceID, SourceKind: trust.SourceKind,
		SourceRiskLabel: marketplace.UntrustedRiskLabel, SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint,
		CachePath: cachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: time.Now().UTC(),
	}, validated.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	return packageRow, artifacts
}
