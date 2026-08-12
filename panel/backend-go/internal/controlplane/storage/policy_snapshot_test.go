package storage

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"gorm.io/gorm"
)

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
	if len(stage.DeclaredScopes) != 1 || stage.DeclaredScopes[0] != "http.inspect" || len(stage.GrantedScopes) != 1 || stage.GrantedScopes[0] != "http.inspect" || stage.ResourceGroupID != instance.ResourceGroupID || stage.ResourceBudget.TimeoutMS != 2 {
		t.Fatalf("projected signed declarations/grants/group/budget = %+v / %+v / %q / %+v", stage.DeclaredScopes, stage.GrantedScopes, stage.ResourceGroupID, stage.ResourceBudget)
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

func TestCompleteAgentSnapshotUsesOneSQLiteSnapshot(t *testing.T) {
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
	testCompleteAgentSnapshotUsesOneSnapshot(t, reader, writer, "sqlite", false)
}

func testAgentHeartbeatPendingCertificateOverlayUsesOneSnapshot(t *testing.T, reader, writer *GormStore, operationPrefix string) {
	t.Helper()
	ctx := t.Context()
	const agentID = "edge-cert-heartbeat"
	const domain = "heartbeat-generation.example.test"
	if err := reader.SaveAgent(ctx, AgentRow{ID: agentID, AgentToken: "token", Platform: "linux-amd64", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	certRow := ManagedCertificateRow{
		ID: 401, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-cert-heartbeat"]`, Status: "active", MaterialHash: "old", Revision: 4,
	}
	if err := reader.SaveManagedCertificates(ctx, []ManagedCertificateRow{certRow}); err != nil {
		t.Fatal(err)
	}
	oldBundle := ManagedCertificateBundle{ID: certRow.ID, Domain: domain, Revision: int64(certRow.Revision), CertPEM: "old-cert", KeyPEM: "old-key"}
	active, err := reader.StageManagedCertificateGeneration(ctx, domain, oldBundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.PromoteManagedCertificateGeneration(ctx, domain, active.ID, active.MaterialHash); err != nil {
		t.Fatal(err)
	}
	if err := reader.SaveManagedCertificates(ctx, []ManagedCertificateRow{certRow}); err != nil {
		t.Fatal(err)
	}
	overlay := func(ctx context.Context, tx *GormStore, _ string, snapshot Snapshot) (Snapshot, error) {
		pending, found, err := tx.LoadPendingManagedCertificateGeneration(ctx, domain)
		if err != nil || !found {
			return snapshot, err
		}
		bundle := pending.Material
		bundle.ID = certRow.ID
		bundle.Domain = domain
		bundle.Revision = int64(certRow.Revision)
		replaced := false
		for index := range snapshot.Certificates {
			if snapshot.Certificates[index].ID == bundle.ID {
				snapshot.Certificates[index] = bundle
				replaced = true
				break
			}
		}
		if !replaced {
			snapshot.Certificates = append(snapshot.Certificates, bundle)
		}
		return snapshot, nil
	}
	before, err := reader.LoadAgentHeartbeatSnapshot(ctx, agentID, overlay)
	if err != nil {
		t.Fatal(err)
	}

	firstAgentRead := make(chan struct{})
	continueRead := make(chan struct{})
	var intercept sync.Once
	callbackName := "test:heartbeat-certificate-snapshot-" + operationPrefix
	if err := reader.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != (AgentRow{}).TableName() {
			return
		}
		intercept.Do(func() {
			close(firstAgentRead)
			<-continueRead
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.db.Callback().Query().Remove(callbackName) })
	readDone := make(chan struct {
		result AgentHeartbeatSnapshot
		err    error
	}, 1)
	go func() {
		result, err := reader.LoadAgentHeartbeatSnapshot(ctx, agentID, overlay)
		readDone <- struct {
			result AgentHeartbeatSnapshot
			err    error
		}{result: result, err: err}
	}()
	select {
	case <-firstAgentRead:
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("heartbeat certificate snapshot did not reach the agent-row boundary")
	}
	newBundle := ManagedCertificateBundle{ID: certRow.ID, Domain: domain, Revision: int64(certRow.Revision), CertPEM: "new-cert", KeyPEM: "new-key"}
	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.StageManagedCertificateGeneration(ctx, domain, newBundle)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			close(continueRead)
			t.Fatalf("concurrent pending certificate stage error = %v", err)
		}
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatalf("concurrent %s pending certificate stage could not commit", operationPrefix)
	}
	close(continueRead)
	read := <-readDone
	if read.err != nil {
		t.Fatal(read.err)
	}
	after, err := reader.LoadAgentHeartbeatSnapshot(ctx, agentID, overlay)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(result AgentHeartbeatSnapshot) string {
		encoded, _ := json.Marshal(result)
		return string(encoded)
	}
	got, wantBefore, wantAfter := encode(read.result), encode(before), encode(after)
	if got != wantBefore && got != wantAfter {
		t.Fatalf("pending certificate overlay produced a hybrid generation %q; before=%q after=%q", got, wantBefore, wantAfter)
	}
	if wantBefore == wantAfter {
		t.Fatal("fixture did not create distinct pending certificate snapshot generations")
	}
}

func testAgentHeartbeatSnapshotUsesOneSnapshot(t *testing.T, reader, writer *GormStore, operationPrefix string) {
	t.Helper()
	ctx := t.Context()
	beforeRow := AgentRow{
		ID: "edge-heartbeat", AgentToken: "token", DesiredVersion: "1.0.0", DesiredRevision: 1,
		CurrentRevision: 1, Platform: "linux-amd64", LastApplyStatus: "success",
		OutboundProxyURL: "socks5://before.example.test:1080", TrafficStatsInterval: "30s", CapabilitiesJSON: `[]`,
	}
	if err := reader.SaveAgent(ctx, beforeRow); err != nil {
		t.Fatal(err)
	}
	if err := reader.SaveHTTPRules(ctx, beforeRow.ID, []HTTPRuleRow{{
		ID: 1, AgentID: beforeRow.ID, FrontendURL: "https://heartbeat.example.test", BackendsJSON: `[]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	load := func(store *GormStore) (AgentHeartbeatSnapshot, error) {
		return store.LoadAgentHeartbeatSnapshot(ctx, beforeRow.ID, nil)
	}
	before, err := load(reader)
	if err != nil {
		t.Fatal(err)
	}
	afterRow := beforeRow
	afterRow.DesiredVersion = "2.0.0"
	afterRow.DesiredRevision = 2
	afterRow.Platform = "linux-arm64"
	afterRow.OutboundProxyURL = "http://after.example.test:8080"
	afterRow.TrafficStatsInterval = "2m"

	firstAgentRead := make(chan struct{})
	continueRead := make(chan struct{})
	var intercept sync.Once
	callbackName := "test:heartbeat-snapshot-" + operationPrefix
	if err := reader.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != (AgentRow{}).TableName() {
			return
		}
		intercept.Do(func() {
			close(firstAgentRead)
			<-continueRead
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.db.Callback().Query().Remove(callbackName) })

	readDone := make(chan struct {
		result AgentHeartbeatSnapshot
		err    error
	}, 1)
	go func() {
		result, err := load(reader)
		readDone <- struct {
			result AgentHeartbeatSnapshot
			err    error
		}{result: result, err: err}
	}()
	select {
	case <-firstAgentRead:
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("heartbeat snapshot reader did not reach the agent-row boundary")
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- writer.SaveAgent(ctx, afterRow) }()
	select {
	case err := <-writeDone:
		if err != nil {
			close(continueRead)
			t.Fatalf("concurrent agent settings update error = %v", err)
		}
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatalf("concurrent %s agent settings update could not commit", operationPrefix)
	}
	close(continueRead)
	read := <-readDone
	if read.err != nil {
		t.Fatalf("heartbeat snapshot read error = %v", read.err)
	}
	after, err := load(reader)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(result AgentHeartbeatSnapshot) string {
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Sprintf("invalid:%v", err)
		}
		return string(encoded)
	}
	got, wantBefore, wantAfter := encode(read.result), encode(before), encode(after)
	if got != wantBefore && got != wantAfter {
		t.Fatalf("heartbeat snapshot produced a hybrid generation %q; before=%q after=%q", got, wantBefore, wantAfter)
	}
	if wantBefore == wantAfter {
		t.Fatal("fixture did not create distinct heartbeat snapshot generations")
	}

	stale, err := reader.LoadAgentSnapshot(ctx, afterRow.ID, AgentSnapshotInput{
		DesiredVersion: "stale", DesiredRevision: 99, CurrentRevision: 99, Platform: "stale-platform",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stale.DesiredVersion != afterRow.DesiredVersion || stale.AgentConfig.OutboundProxyURL != afterRow.OutboundProxyURL {
		t.Fatalf("remote snapshot trusted caller-owned settings: %+v", stale)
	}
}

func testCompleteAgentSnapshotUsesOneSnapshot(t *testing.T, reader, writer *GormStore, operationPrefix string, transactionScoped bool) {
	t.Helper()
	ctx := t.Context()
	if err := reader.SaveAgent(ctx, AgentRow{ID: "edge-full", AgentToken: "token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	instance := installActivePolicyFixture(t, reader, signingKey, "policy.full."+operationPrefix, "ip", "full-ip-"+operationPrefix, `["edge-full"]`, `["shared-full"]`)
	if err := reader.EnsureAgentPluginPolicyCatalog(ctx, "edge-full"); err != nil {
		t.Fatal(err)
	}
	beforeRule := HTTPRuleRow{
		ID: 1, AgentID: "edge-full", FrontendURL: "https://before.example.test", BackendsJSON: `[]`,
		Enabled: true, PolicyRefJSON: `{"id":"shared-full"}`, Revision: 1,
	}
	if err := reader.SaveHTTPRules(ctx, "edge-full", []HTTPRuleRow{beforeRule}); err != nil {
		t.Fatal(err)
	}
	before, err := reader.LoadAgentIntentSnapshot(ctx, "edge-full", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}

	installed, found, err := reader.GetInstalledPlugin(ctx, instance.PluginID)
	if err != nil || !found {
		t.Fatalf("GetInstalledPlugin() = %v, %v", found, err)
	}
	installed.DesiredLifecycle = "disabled"
	installed.CurrentLifecycle = "disabled"
	now := time.Now().UTC()
	disable := PluginMutation{
		PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &installed,
		Operation: PluginOperationRow{ID: operationPrefix + "-full-disable", PluginID: installed.PluginID, Kind: "disable", Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
		Audit:     AuditEventRow{ID: operationPrefix + "-full-disable-audit", Action: "plugin.disable", TargetKind: "plugin", TargetID: installed.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	}
	afterRule := beforeRule
	afterRule.FrontendURL = "https://after.example.test"
	afterRule.PolicyRefJSON = ""
	afterRule.Revision = 2

	firstRuleRead := make(chan struct{})
	continueRead := make(chan struct{})
	var intercept sync.Once
	callbackName := "test:complete-snapshot-" + operationPrefix
	if err := reader.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Table != (HTTPRuleRow{}).TableName() {
			return
		}
		intercept.Do(func() {
			close(firstRuleRead)
			<-continueRead
		})
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.db.Callback().Query().Remove(callbackName) })

	readSnapshot := func() (Snapshot, error) {
		if !transactionScoped {
			return reader.LoadAgentIntentSnapshot(ctx, "edge-full", AgentSnapshotInput{})
		}
		var snapshot Snapshot
		err := reader.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
			var err error
			snapshot, err = tx.LoadAgentIntentSnapshot(ctx, "edge-full", AgentSnapshotInput{})
			return RevisionMutationDecision{}, err
		})
		return snapshot, err
	}
	readDone := make(chan struct {
		snapshot Snapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := readSnapshot()
		readDone <- struct {
			snapshot Snapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	select {
	case <-firstRuleRead:
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatal("full snapshot reader did not reach the deterministic rule boundary")
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writer.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
			if err := tx.LockAgentPluginPolicyCatalog(ctx, "edge-full"); err != nil {
				return RevisionMutationDecision{}, err
			}
			if err := tx.SaveHTTPRules(ctx, "edge-full", []HTTPRuleRow{afterRule}); err != nil {
				return RevisionMutationDecision{}, err
			}
			if err := tx.ApplyPluginMutation(ctx, disable); err != nil {
				return RevisionMutationDecision{}, err
			}
			return RevisionMutationDecision{}, nil
		})
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			close(continueRead)
			t.Fatalf("concurrent full snapshot mutation error = %v", err)
		}
	case <-time.After(5 * time.Second):
		close(continueRead)
		t.Fatalf("concurrent %s full snapshot mutation could not commit", operationPrefix)
	}
	close(continueRead)
	read := <-readDone
	if read.err != nil {
		t.Fatalf("complete snapshot read error = %v", read.err)
	}
	after, err := reader.LoadAgentIntentSnapshot(ctx, "edge-full", AgentSnapshotInput{})
	if err != nil {
		t.Fatal(err)
	}
	completeGeneration := func(snapshot Snapshot) string {
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return fmt.Sprintf("invalid-snapshot:%v", err)
		}
		return string(encoded)
	}
	got, wantBefore, wantAfter := completeGeneration(read.snapshot), completeGeneration(before), completeGeneration(after)
	if got != wantBefore && got != wantAfter {
		t.Fatalf("complete snapshot produced a hybrid generation %q; before=%q after=%q", got, wantBefore, wantAfter)
	}
	if wantBefore == wantAfter {
		t.Fatalf("fixture did not create distinct complete generations: %q", wantBefore)
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
