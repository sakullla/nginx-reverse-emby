package storage

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
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
	rules := snapshotHTTPRules([]HTTPRuleRow{{
		ID: 1, FrontendURL: "https://media.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`,
		Enabled: true, PolicyRefJSON: `{"id":`,
	}}, false)
	if len(rules) != 1 || rules[0].PolicyRef == nil || !strings.Contains(rules[0].PolicyRef.ID, "invalid") {
		t.Fatalf("malformed policy ref was silently dropped: %+v", rules)
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
		ConfigJSON: `{"mode":"block"}`, ConfigVersion: 3, DesiredEnabled: false, CurrentState: "disabled",
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
	if len(snapshot.PluginPolicies) != 1 || snapshot.PluginPolicies[0].ID != instance.ID || len(snapshot.PluginPolicies[0].Stages) != 1 {
		t.Fatalf("plugin policies = %+v", snapshot.PluginPolicies)
	}
	stage := snapshot.PluginPolicies[0].Stages[0]
	if stage.Kind != "waf" || stage.PluginID != packageRow.PluginID || stage.PackageDigest != digest ||
		stage.ArtifactDigest != artifacts[0].SHA256 || stage.SignerKeyID != trust.KeyID || stage.SignerFingerprint != fingerprint ||
		!stage.SignatureVerified || string(stage.Config) != instance.ConfigJSON {
		t.Fatalf("projected policy stage = %+v", stage)
	}
	if !strings.HasPrefix(filepath.Clean(stage.ArtifactPath), filepath.Clean(cachePath)+string(filepath.Separator)) {
		t.Fatalf("artifact path %q is outside verified package %q", stage.ArtifactPath, cachePath)
	}
	if len(stage.GrantedScopes) != 1 || stage.GrantedScopes[0] != "http.inspect" || stage.ResourceBudget.TimeoutMS != 2 {
		t.Fatalf("projected grants/budget = %+v / %+v", stage.GrantedScopes, stage.ResourceBudget)
	}
}
