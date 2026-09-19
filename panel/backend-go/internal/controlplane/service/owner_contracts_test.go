//go:build exhaustive && !integration

package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginUpgradeCanReplacePackageWithRetiredCapabilities(t *testing.T) {
	t.Parallel()

	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store := newServiceOwnerStore(t)
	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })

	key := publishFixtureSigningKey()
	trust := retiredPackageTrust(t, key)
	legacyValidator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion:            "0.0.0-dev",
		AllowedPermissions:     []string{"http.inspect", "container.compose"},
		AllowedExtensionPoints: []string{"http.request", "container.provider"},
		TrustedSigners:         map[string]ed25519.PublicKey{trust.KeyID: key.Public().(ed25519.PublicKey)},
		TrustedSignerPolicy:    plugins.TrustedSignerPolicyExact,
	})
	currentValidator := plugins.NewValidator(plugins.ValidatorOptions{
		HostVersion:         "0.0.0-dev",
		TrustedSigners:      map[string]ed25519.PublicKey{trust.KeyID: key.Public().(ed25519.PublicKey)},
		TrustedSignerPolicy: plugins.TrustedSignerPolicyExact,
	})

	const pluginID = "official.retired-upgrade"
	oldRoot := writeRetiredPackageFixture(t, pluginID, "1.0.0", key)
	oldCandidate := importPackageCandidate(t, cacheRoot, oldRoot, legacyValidator, trust)
	installed, err := NewPluginServiceWithValidator(store, legacyValidator, cacheRoot).Install(ctx, PluginInstallRequest{
		Package: oldCandidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect", "container.compose"}, RiskAccepted: true,
	})
	if err != nil {
		t.Fatalf("install historically valid package: %v", err)
	}
	if _, err := currentValidator.ValidatePackage(oldCandidate.CachePath, plugins.PackageExpectation{}); err == nil || !strings.Contains(err.Error(), "container.compose") {
		t.Fatalf("current validator unexpectedly accepts retired package: %v", err)
	}

	currentRoot := writeCurrentPackageFixture(t, pluginID, "2.0.0", key)
	currentCandidate := importPackageCandidate(t, cacheRoot, currentRoot, currentValidator, trust)
	upgraded, err := NewPluginServiceWithValidator(store, currentValidator, cacheRoot).Upgrade(ctx, PluginUpgradeRequest{
		PluginID: pluginID, Package: currentCandidate, ActorID: "admin", RiskAccepted: true,
	})
	if err != nil {
		t.Fatalf("upgrade away from retired capabilities: %v", err)
	}
	if upgraded.ActivePackageDigest != currentCandidate.Package.Digest || upgraded.ActivePackageDigest == installed.ActivePackageDigest {
		t.Fatalf("upgrade did not promote current package: %+v", upgraded)
	}
}

func retiredPackageTrust(t *testing.T, key ed25519.PrivateKey) marketplace.SignatureTrust {
	t.Helper()
	publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return marketplace.SignatureTrust{
		SourceID: "retired-package-fixture", SourceKind: marketplace.SourceKindCustom,
		KeyID: "test-fixture", PublicKey: publicKey, Fingerprint: fingerprint,
	}
}

func writeRetiredPackageFixture(t *testing.T, pluginID, version string, key ed25519.PrivateKey) string {
	t.Helper()
	root := writeCurrentPackageFixture(t, pluginID, version, key)
	manifestPath := filepath.Join(root, plugins.PackageManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(data), "permissions: [http.inspect]", "permissions: [http.inspect, container.compose]", 1)
	manifest = strings.Replace(manifest, "extension_points: [http.request]", "extension_points: [http.request, container.provider]", 1)
	writePublishFile(t, root, plugins.PackageManifestFile, manifest)
	resignPackageFixture(t, root, key)
	return root
}

func writeCurrentPackageFixture(t *testing.T, pluginID, version string, key ed25519.PrivateKey) string {
	t.Helper()
	root := writeHostInjectedPackage(t, pluginID, `{"type":"object","additionalProperties":false}`, false, key)
	manifestPath := filepath.Join(root, plugins.PackageManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageManifestFile, strings.Replace(string(data), "version: 1.0.0", "version: "+version, 1))
	resignPackageFixture(t, root, key)
	return root
}

func resignPackageFixture(t *testing.T, root string, key ed25519.PrivateKey) {
	t.Helper()
	digest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageDigestFile, digest+"\n")
	writePublishFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(digest)))+"\n")
}

func importPackageCandidate(t *testing.T, cacheRoot, root string, validator *plugins.Validator, trust marketplace.SignatureTrust) PluginPackageCandidate {
	t.Helper()
	validated, err := validator.ValidatePackage(root, plugins.PackageExpectation{SignatureKeyID: trust.KeyID})
	if err != nil {
		t.Fatalf("validate package fixture: %v", err)
	}
	cachePath, err := marketplace.ImportVerifiedPackage(cacheRoot, validated, validator, trust)
	if err != nil {
		t.Fatalf("import package fixture: %v", err)
	}
	return PluginPackageCandidate{
		Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...),
		SignatureTrust: trust, CachePath: cachePath, validator: validator,
		sourceID: trust.SourceID, sourceKind: trust.SourceKind, sourceRiskLabel: marketplace.UntrustedRiskLabel,
	}
}

func TestGovernedMutationWithoutPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	store := &recordingResourceQuotaStore{}
	ctx := context.Background()
	if err := authorizeReferencedResource(ctx, store, "agent", "edge-a"); !errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("authorizeReferencedResource() error = %v, want %v", err, ErrMutationPrincipalRequired)
	}
	if err := consumeResourceQuota(ctx, store, "http_rule", "edge-a:1", "agent", "edge-a", "rule_count", 1); !errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("consumeResourceQuota() error = %v, want %v", err, ErrMutationPrincipalRequired)
	}
	if store.called {
		t.Fatal("quota store was called without a principal")
	}

	ctx = WithSystemMutationPrincipal(context.Background(), "system:reconciler")
	if err := authorizeReferencedResource(ctx, store, "agent", "edge-a"); err != nil {
		t.Fatal(err)
	}
	if err := consumeResourceQuota(ctx, store, "http_rule", "edge-a:1", "agent", "edge-a", "rule_count", 1); err != nil {
		t.Fatal(err)
	}
	if !store.called || store.actor.UserID != "system:reconciler" || !store.actor.Bootstrap {
		t.Fatalf("system principal actor = %+v called=%v", store.actor, store.called)
	}
}

func TestMutationExecutorRejectsInvalidL4AndHTTPConflictWithRollback(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local", Platform: "windows-amd64"}); err != nil {
		t.Fatal(err)
	}
	executor := NewMutationExecutor(store, revision.WithClock(func() time.Time {
		return time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC)
	}), revision.WithOperationIDGenerator(func() (string, error) { return "op-invalid-l4", nil }))

	missingProfile := 999
	_, err := executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "missing-ref", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true, Capabilities: []string{"egress_profiles"}}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			return tx.ListHTTPRules(ctx, target.AgentID)
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://missing.example.com",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true,
				EgressProfileID: &missingProfile, Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeNotFound {
		t.Fatalf("missing egress err=%v code=%q", err, revision.ErrorCodeOf(err))
	}
	if rules, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil {
		t.Fatal(listErr)
	} else if len(rules) != 0 {
		t.Fatalf("missing-ref HTTP rules survived: %+v", rules)
	}

	executor = NewMutationExecutor(store, revision.WithClock(func() time.Time {
		return time.Date(2026, 8, 17, 4, 1, 0, 0, time.UTC)
	}), revision.WithOperationIDGenerator(func() (string, error) { return "op-http-l4", nil }))
	_, err = executor.Execute(t.Context(), revision.MutationRequest{
		Kind: "http_rule.create", IdempotencyKey: "http-l4-conflict", Request: map[string]any{"rule": 1},
		Targets: []revision.Target{{AgentID: "local", Local: true}},
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revision.Target) (any, error) {
			return tx.ListHTTPRules(ctx, target.AgentID)
		},
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			if err := tx.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
				ID: 2, AgentID: "local", Name: "listen", Protocol: "tcp", ListenMode: "proxy",
				ListenHost: "0.0.0.0", ListenPort: 8443, Enabled: true, Revision: int(revisions["local"]),
				BackendsJSON: `[{"host":"127.0.0.1","port":9000}]`,
			}}); err != nil {
				return err
			}
			return tx.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
				ID: 1, AgentID: "local", FrontendURL: "https://edge.example.com:8443",
				BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`, Enabled: true, Revision: int(revisions["local"]),
			}})
		},
	})
	if revision.ErrorCodeOf(err) != revision.ErrorCodeConflict {
		t.Fatalf("HTTP/L4 conflict err=%v code=%q", err, revision.ErrorCodeOf(err))
	}
	if rules, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil {
		t.Fatal(listErr)
	} else if len(rules) != 0 {
		t.Fatalf("conflict HTTP rules survived: %+v", rules)
	}

	missingBackendErr := FullSnapshotValidator{}.Validate(t.Context(), revision.SnapshotValidation{
		Target: revision.Target{AgentID: "local", Local: true},
		Snapshot: storage.Snapshot{L4Rules: []storage.L4Rule{{
			ID: 3, AgentID: "local", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 9000, ListenMode: "tcp",
		}}},
	})
	if revision.ErrorCodeOf(missingBackendErr) != revision.ErrorCodeUnprocessable {
		t.Fatalf("missing L4 backend err=%v code=%q", missingBackendErr, revision.ErrorCodeOf(missingBackendErr))
	}
}

func TestPKILifecycleExpiredFailureClosesAndForceRotationIsTargeted(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	expired := testPKIEndpointState("identity-expired", "cert-expired-1", 1, now.Add(-25*time.Hour), now.Add(-time.Hour), "c")
	notDue := testPKIEndpointState("identity-target", "cert-target-1", 1, now.Add(-time.Hour), now.Add(89*24*time.Hour), "d")
	untouched := testPKIEndpointState("identity-other", "cert-other-1", 1, now.Add(-time.Hour), now.Add(89*24*time.Hour), "e")
	repository := newPKIEndpointRotationTestRepository(expired, notDue, untouched)
	rotator := &pkiEndpointRotationTestRotator{
		errByIdentity: map[string]error{"identity-expired": errors.New("issuer unavailable")},
		candidateByIdentity: map[string]PKIEndpointRotationCandidate{
			"identity-target": testPKIEndpointCandidate("cert-target-2", 2, now, "f"),
		},
	}
	svc, err := NewPKILifecycleService(PKILifecycleServiceOptions{
		Policy: DefaultInternalPKIPolicy(), Repository: repository, Rotator: rotator,
		Lease: pkiStaticLeaseGate{}, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := svc.RunEndpointRotation(t.Context(), "identity-expired", false)
	if !errors.Is(err, ErrPKIEndpointFailedClosed) || !failed.FailedClosed || failed.ActiveCertificate != "cert-expired-1" {
		t.Fatalf("expired rotation = (%+v, %v)", failed, err)
	}
	forced, err := svc.ForceRotateEndpoint(t.Context(), "identity-target")
	if err != nil || !forced.Forced || !forced.Activated || forced.ActiveCertificate != "cert-target-2" {
		t.Fatalf("ForceRotateEndpoint = (%+v, %v)", forced, err)
	}
	if repository.state("identity-other").CertificateID != "cert-other-1" {
		t.Fatal("force rotation changed a non-target identity")
	}
}

func TestPluginLifecycleReconcilerWaitsForEveryExactAgentReport(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	store := &lifecycleReconcileStore{
		operation: storage.PluginOperationRow{
			ID: "operation", PluginID: "plugin", Kind: "enable", Status: "applying",
			TargetPackageDigest: digest, TargetRevision: 8, ActorID: "admin",
		},
		statuses: []storage.PluginAgentRuntimeStatusRow{
			{OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin", Revision: 7, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`},
			{OperationID: "operation", AgentID: "edge-b", InstanceID: "instance", PluginID: "plugin", Revision: 8, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "applying", DetailsJSON: `{}`},
		},
	}
	completion := &lifecycleCompletionRecorder{}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	report := storage.PluginGenerationReport{
		OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin",
		Revision: 7, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "active", Sequence: 1,
	}
	result, err := reconciler.Reconcile(t.Context(), report, "agent:edge-a")
	if err != nil || !result.Pending || result.Completed || completion.kind != "" {
		t.Fatalf("partial reconcile = %+v kind=%q err=%v", result, completion.kind, err)
	}
	report.AgentID = "edge-b"
	report.Revision = 8
	result, err = reconciler.Reconcile(t.Context(), report, "agent:edge-b")
	if err != nil || !result.Completed || !result.Applied || completion.kind != "lifecycle" {
		t.Fatalf("terminal reconcile = %+v completion=%+v err=%v", result, completion, err)
	}
	if _, err := reconciler.Reconcile(t.Context(), report, ""); err == nil {
		t.Fatal("empty actor accepted")
	}
}

func TestPluginLifecycleReconcilerCompletesFailedAgentReport(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	store := &lifecycleReconcileStore{
		operation: storage.PluginOperationRow{ID: "operation", PluginID: "plugin", Kind: "configure", Status: "applying", TargetPackageDigest: digest, TargetRevision: 8, ActorID: "admin"},
		statuses: []storage.PluginAgentRuntimeStatusRow{{
			OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin", Revision: 8,
			GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: 2, State: "applying", DetailsJSON: `{}`,
		}},
	}
	completion := &lifecycleCompletionRecorder{}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	report := storage.PluginGenerationReport{OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", PluginID: "plugin",
		Revision: 8, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "failed", Sequence: 1}
	result, err := reconciler.Reconcile(t.Context(), report, "agent:edge-a")
	if err != nil || !result.Completed || result.Applied || completion.kind != "configure" || completion.result.Applied {
		t.Fatalf("failed reconcile = %+v completion=%+v err=%v", result, completion, err)
	}
}

func TestPluginLifecycleReconcilerClosesOperationWhenControlPlaneActivationFails(t *testing.T) {
	t.Parallel()
	activationErr := errors.New("canonical plugin handshake failed")
	completion := &lifecycleCompletionRecorder{plan: controlPlanePluginRuntimePlan{
		Controlled: true,
		Candidates: []pluginhost.Candidate{{InstanceID: "instance"}},
	}}
	reconciler, err := NewPluginLifecycleReconciler(&lifecycleReconcileStore{}, completion)
	if err != nil {
		t.Fatal(err)
	}
	reconciler.SetControlPlaneRuntime(&failingPluginControlPlaneRuntime{err: activationErr})
	operation := storage.PluginOperationRow{ID: "operation", PluginID: "plugin", Kind: "upgrade"}
	agentResults := map[string]any{"local": map[string]any{"state": "applied"}}
	if err := reconciler.completeTrustedRevisionOperation(t.Context(), operation, true, agentResults); err != nil {
		t.Fatalf("completeTrustedRevisionOperation() error = %v", err)
	}
	if completion.kind != "trusted-upgrade" || completion.result.Applied {
		t.Fatalf("completion = %+v", completion)
	}
	if completion.agentResults["local"] == nil {
		t.Fatal("existing Agent result was not preserved")
	}
	runtimeResult, ok := completion.agentResults["control-plane-runtime"].(map[string]any)
	if !ok || runtimeResult["state"] != "failed" || runtimeResult["safe_detail"] != activationErr.Error() {
		t.Fatalf("runtime failure result = %#v", completion.agentResults["control-plane-runtime"])
	}
}

func TestDDNSDisabledWithoutTokenMakesNoCloudflareCall(t *testing.T) {
	t.Parallel()
	cf := &fakeCFClient{}
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "host.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	store := &fakeDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	svc := NewDDNSService(config.Config{}, store, cf, func() time.Time { return time.Unix(1700, 0) })
	svc.reconcileAgent(context.Background(), "a1")
	if cf.calls != 0 {
		t.Fatalf("Cloudflare calls = %d, want 0", cf.calls)
	}
	if store.status("a1").Status != "disabled" {
		t.Fatalf("status = %+v", store.status("a1"))
	}
}

func TestDDNSResolvesTokenPerDomainWithoutMixingEnv(t *testing.T) {
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "www.example.com, other.test",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	cf := &fakeCFClient{}
	store := &fakeDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	svc := NewDDNSService(config.Config{DDNS: config.DDNSRuntimeConfig{Enabled: true, Token: "token-b", TTL: 120}}, store, cf, func() time.Time { return time.Unix(1700, 0) })
	svc.resolveToken = func(_ context.Context, domain string) (string, error) {
		if domain == "www.example.com" {
			return "token-a", nil
		}
		return "token-b", nil
	}
	svc.reconcileAgent(context.Background(), "a1")
	if cf.calls != 2 {
		t.Fatalf("Cloudflare calls = %d, want 2", cf.calls)
	}
	if len(cf.recorded) != 2 || cf.recorded[0].fqdn != "www.example.com" || cf.recorded[0].token != "token-a" {
		t.Fatalf("mapped domain call = %+v, want token-a", cf.recorded)
	}
	if cf.recorded[1].fqdn != "other.test" || cf.recorded[1].token != "token-b" {
		t.Fatalf("unmapped domain call = %+v, want token-b", cf.recorded[1])
	}
}

func TestDDNSFailsWhenDomainHasNoToken(t *testing.T) {
	errTokenUnavailable := errors.New("DNS API token unavailable")
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "missing.example",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	cf := &fakeCFClient{}
	store := &fakeDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	svc := NewDDNSService(config.Config{DDNS: config.DDNSRuntimeConfig{Enabled: true, TTL: 120}}, store, cf, func() time.Time { return time.Unix(1700, 0) })
	svc.resolveToken = func(_ context.Context, domain string) (string, error) {
		return "", fmt.Errorf("%w: %s", errTokenUnavailable, domain)
	}
	svc.reconcileAgent(context.Background(), "a1")
	if cf.calls != 0 {
		t.Fatalf("Cloudflare calls = %d, want 0", cf.calls)
	}
	status := store.status("a1")
	if status.Status != "error" || !strings.Contains(status.LastError, "missing.example") {
		t.Fatalf("status = %+v, want error naming missing.example", status)
	}
}

func TestDDNSMappedUnavailableDoesNotUseEnvToken(t *testing.T) {
	errCredentialUnavailable := errors.New("mapped credential unavailable")
	raw, _ := json.Marshal(storage.DDNSConfig{
		Enabled: true, Domain: "example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	})
	cf := &fakeCFClient{}
	store := &fakeDDNSStore{rows: map[string]storage.AgentRow{
		"a1": {ID: "a1", DdnsConfigJSON: string(raw), LastSeenIPv4: "203.0.113.10"},
	}}
	svc := NewDDNSService(config.Config{DDNS: config.DDNSRuntimeConfig{Enabled: true, Token: "token-b", TTL: 120}}, store, cf, time.Now)
	svc.resolveToken = func(_ context.Context, domain string) (string, error) {
		return "", fmt.Errorf("%w: %s", errCredentialUnavailable, domain)
	}
	svc.reconcileAgent(context.Background(), "a1")
	if cf.calls != 0 {
		t.Fatalf("mapped unavailable must not call Cloudflare, got %d", cf.calls)
	}
	status := store.status("a1")
	if status.Status != "error" || !strings.Contains(status.LastError, "example.com") {
		t.Fatalf("status = %+v, want mapped-token error for example.com", status)
	}
}

func TestRevisionAPIRedactsLeaseIDAndReportsDegradedStatus(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	now := time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-a", Name: "edge-a", Platform: "linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-b", Name: "edge-b", Platform: "linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-ok", Name: "edge-ok", Platform: "linux-amd64"}); err != nil {
		t.Fatal(err)
	}
	snapA := mustOwnerSnapshotArtifact(t, storage.Snapshot{
		Revision: 1,
		Rules: []storage.HTTPRule{{
			ID: 1, AgentID: "edge-a", FrontendURL: "https://a.example.com",
			RelayLayers: [][]int{{10}},
		}},
	})
	snapB := mustOwnerSnapshotArtifact(t, storage.Snapshot{
		Revision: 1,
		RelayListeners: []storage.RelayListener{{
			ID: 10, AgentID: "edge-b", Name: "hop", ListenHost: "0.0.0.0", ListenPort: 443,
			Enabled: true, TLSMode: "terminate", TransportMode: "tls_tcp",
		}},
	})
	snapOK := mustOwnerSnapshotArtifact(t, storage.Snapshot{Revision: 1})
	plan, err := dependency.BuildPlan("op-redact", dependency.ActionApply, []dependency.SnapshotRevision{
		{AgentID: "edge-a", Revision: 1, Snapshot: storage.Snapshot{
			Revision: 1,
			Rules: []storage.HTTPRule{{
				ID: 1, AgentID: "edge-a", FrontendURL: "https://a.example.com",
				RelayLayers: [][]int{{10}},
			}},
		}},
		{AgentID: "edge-b", Revision: 1, Snapshot: storage.Snapshot{
			Revision: 1,
			RelayListeners: []storage.RelayListener{{
				ID: 10, AgentID: "edge-b", Name: "hop", ListenHost: "0.0.0.0", ListenPort: 443,
				Enabled: true, TLSMode: "terminate", TransportMode: "tls_tcp",
			}},
		}},
		{AgentID: "edge-ok", Revision: 1, Snapshot: storage.Snapshot{Revision: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	planPayload, err := plan.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	planDigest := sha256.Sum256(planPayload)
	planID := "dependency-plan-" + hex.EncodeToString(planDigest[:])
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "op-redact", Kind: "http_rule.create", Status: storage.OperationStatusPending,
			PrimaryAgentID: "edge-a", CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []storage.AgentRevisionRow{
			{
				AgentID: "edge-a", Revision: 1, OperationID: "op-redact", State: storage.AgentRevisionStatePending,
				SnapshotArtifactID: snapA.ID, SnapshotDigest: snapA.SHA256, CreatedAt: now, UpdatedAt: now,
			},
			{
				AgentID: "edge-b", Revision: 1, OperationID: "op-redact", State: storage.AgentRevisionStateFailed,
				SnapshotArtifactID: snapB.ID, SnapshotDigest: snapB.SHA256, CreatedAt: now, UpdatedAt: now,
			},
			{
				AgentID: "edge-ok", Revision: 1, OperationID: "op-redact", State: storage.AgentRevisionStateApplied,
				SnapshotArtifactID: snapOK.ID, SnapshotDigest: snapOK.SHA256, CreatedAt: now, UpdatedAt: now,
			},
		},
		Pointers: []storage.AgentRevisionPointerRow{
			{AgentID: "edge-a", DesiredRevision: 1, AppliedRevision: 0, LastKnownGoodRevision: 0, UpdatedAt: now},
			{AgentID: "edge-b", DesiredRevision: 1, AppliedRevision: 0, LastKnownGoodRevision: 0, UpdatedAt: now},
			{AgentID: "edge-ok", DesiredRevision: 1, AppliedRevision: 1, LastKnownGoodRevision: 1, UpdatedAt: now},
		},
		Events: []storage.RevisionEventRow{{
			OperationID: "op-redact", AgentID: "edge-a", Revision: 1, EventType: "lease_claimed",
			PayloadJSON: `{"lease_id":"secret-lease","reason":"claimed"}`, CreatedAt: now,
		}},
		Artifacts: []storage.GenerationArtifactRow{
			snapA, snapB, snapOK,
			{ID: planID, Kind: storage.GenerationArtifactKindDependencyPlan, SHA256: hex.EncodeToString(planDigest[:]), Payload: planPayload, SizeBytes: int64(len(planPayload)), CreatedAt: now},
		},
		ArtifactRefs: []storage.AgentRevisionArtifactRow{
			{AgentID: "edge-a", Revision: 1, ArtifactID: planID, Role: storage.RevisionArtifactRoleDependencyPlan, CreatedAt: now},
			{AgentID: "edge-b", Revision: 1, ArtifactID: planID, Role: storage.RevisionArtifactRoleDependencyPlan, CreatedAt: now},
			{AgentID: "edge-ok", Revision: 1, ArtifactID: planID, Role: storage.RevisionArtifactRoleDependencyPlan, CreatedAt: now},
		},
	}); err != nil {
		t.Fatal(err)
	}
	coord, err := coordinator.New(store, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(store, coord)
	if api == nil {
		t.Fatal("NewRevisionAPI returned nil")
	}
	page, err := api.ListEvents(t.Context(), RevisionEventQuery{OperationID: "op-redact", Limit: 10})
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("ListEvents = %+v err=%v", page, err)
	}
	if _, leaked := page.Events[0].Payload["lease_id"]; leaked {
		t.Fatalf("lease_id leaked: %+v", page.Events[0].Payload)
	}
	status, err := api.GetOperationStatus(t.Context(), "op-redact")
	if err != nil || status.OperationID != "op-redact" || !status.Degraded || status.ApplyStatus != string(dependency.StatusDegraded) {
		t.Fatalf("GetOperationStatus = %+v err=%v", status, err)
	}
	blocked := false
	for _, agent := range status.Agents {
		if agent.AgentID == "edge-a" {
			for _, dep := range agent.BlockedBy {
				if dep == "edge-b" {
					blocked = true
				}
			}
		}
	}
	if !blocked {
		t.Fatalf("blocked_by missing edge-b: %+v", status.Agents)
	}
}

func TestRuleServiceCreateRequiresMutationPrincipalOnRevisionStore(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local", Platform: "windows-amd64"}); err != nil {
		t.Fatal(err)
	}
	svc := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	applies := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applies++
		return nil
	})
	frontend := "https://edge.example.com"
	updatedFrontend := "https://edge-updated.example.com"
	enabled := true
	backends := []HTTPRuleBackend{{URL: "http://127.0.0.1:8080"}}
	_, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL: &frontend, Backends: &backends, Enabled: &enabled,
	})
	if !errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("Create without principal err=%v", err)
	}
	if applies != 0 {
		t.Fatalf("apply trigger fired %d times before principal", applies)
	}
	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil || len(rules) != 0 {
		t.Fatalf("unprincipaled create leaked rules=%+v err=%v", rules, err)
	}

	ctx := WithSystemMutationPrincipal(t.Context(), "system:owner")
	created, err := svc.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: &frontend, Backends: &backends, Enabled: &enabled,
	})
	if err != nil || created.ID == 0 || created.FrontendURL != frontend {
		t.Fatalf("Create with principal = %+v err=%v", created, err)
	}
	if applies != 0 {
		t.Fatalf("apply trigger fired %d times on committed create", applies)
	}
	updated, err := svc.Update(ctx, "local", created.ID, HTTPRuleInput{FrontendURL: &updatedFrontend})
	if err != nil || updated.FrontendURL != updatedFrontend {
		t.Fatalf("Update = %+v err=%v", updated, err)
	}
	if applies != 0 {
		t.Fatalf("apply trigger fired %d times on committed update", applies)
	}
	if _, err := svc.Delete(ctx, "local", created.ID); err != nil {
		t.Fatalf("Delete err=%v", err)
	}
	if applies != 0 {
		t.Fatalf("apply trigger fired %d times on committed delete", applies)
	}
	remaining, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("delete leftover rules=%+v err=%v", remaining, err)
	}

	missingRelay := [][]int{{404}}
	_, err = svc.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: &frontend, Backends: &backends, Enabled: &enabled, RelayLayers: &missingRelay,
	})
	if err == nil || !strings.Contains(err.Error(), "relay listener not found") {
		t.Fatalf("missing relay err=%v", err)
	}
	if leftover, listErr := store.ListHTTPRules(t.Context(), "local"); listErr != nil || len(leftover) != 0 {
		t.Fatalf("missing relay leaked rules=%+v err=%v", leftover, listErr)
	}
}

func mustOwnerSnapshotArtifact(t *testing.T, snapshot storage.Snapshot) storage.GenerationArtifactRow {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	return storage.GenerationArtifactRow{
		ID: "snapshot-" + digest, Kind: "agent_snapshot", SHA256: digest,
		Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: time.Date(2026, 8, 17, 5, 0, 0, 0, time.UTC),
	}
}

var serviceOwnerStoreTemplate struct {
	once sync.Once
	data []byte
	err  error
}

func newServiceOwnerStore(t *testing.T) *storage.GormStore {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed service scenarios run in the full test tier")
	}
	serviceOwnerStoreTemplate.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-service-owner-template-")
		if err != nil {
			serviceOwnerStoreTemplate.err = err
			return
		}
		defer os.RemoveAll(root)
		store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local", TrafficStatsEnabled: true})
		if err != nil {
			serviceOwnerStoreTemplate.err = err
			return
		}
		if err := store.Close(); err != nil {
			serviceOwnerStoreTemplate.err = err
			return
		}
		serviceOwnerStoreTemplate.data, serviceOwnerStoreTemplate.err = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	if serviceOwnerStoreTemplate.err != nil {
		t.Fatal(serviceOwnerStoreTemplate.err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "panel.db"), serviceOwnerStoreTemplate.data, 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := filepath.Join(root, "panel.db") + "?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local",
		SkipBootstrapSchema: true, TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type recordingResourceQuotaStore struct {
	called bool
	actor  storage.QuotaActor
}

func (s *recordingResourceQuotaStore) ConsumeQuotaForResource(ctx context.Context, _, _, _, _, metric string, _ int64) (storage.QuotaDecision, error) {
	s.called = true
	s.actor, _ = storage.QuotaActorFromContext(ctx)
	return storage.QuotaDecision{Metric: metric, Allowed: true}, nil
}

func testPKIEndpointState(identityID, certificateID string, generation int64, notBefore, notAfter time.Time, marker string) PKIEndpointCertificateState {
	return PKIEndpointCertificateState{
		IdentityID: identityID, CertificateID: certificateID, Generation: generation,
		CertificateFingerprintSHA256: strings.Repeat(marker, 64),
		PublicKeyFingerprintSHA256:   strings.Repeat(marker+"0", 32),
		NotBefore:                    notBefore, NotAfter: notAfter,
	}
}

func testPKIEndpointCandidate(certificateID string, generation int64, now time.Time, marker string) PKIEndpointRotationCandidate {
	return PKIEndpointRotationCandidate{
		CertificateID: certificateID, Generation: generation,
		CertificateFingerprintSHA256: strings.Repeat(marker, 64),
		PublicKeyFingerprintSHA256:   strings.Repeat(marker+"0", 32),
		NotBefore:                    now.Add(-time.Minute), NotAfter: now.Add(90 * 24 * time.Hour), Verified: true,
	}
}

type pkiEndpointRotationTestRepository struct {
	states      map[string]PKIEndpointCertificateState
	failures    []PKIEndpointRotationFailure
	activations int
}

func newPKIEndpointRotationTestRepository(states ...PKIEndpointCertificateState) *pkiEndpointRotationTestRepository {
	repository := &pkiEndpointRotationTestRepository{states: map[string]PKIEndpointCertificateState{}}
	for _, state := range states {
		repository.states[state.IdentityID] = state
	}
	return repository
}

func (r *pkiEndpointRotationTestRepository) LoadPKIEndpointCertificate(_ context.Context, identityID string) (PKIEndpointCertificateState, error) {
	state, ok := r.states[identityID]
	if !ok {
		return PKIEndpointCertificateState{}, errors.New("identity not found")
	}
	return state, nil
}

func (r *pkiEndpointRotationTestRepository) RecordPKIEndpointRotationFailure(_ context.Context, failure PKIEndpointRotationFailure) error {
	if validatePKIMutationLeaseFence(failure.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	state := r.states[failure.IdentityID]
	state.FailureCount = failure.FailureCount
	state.NextAttemptAt = failure.NextAttemptAt
	r.states[failure.IdentityID] = state
	r.failures = append(r.failures, failure)
	return nil
}

func (r *pkiEndpointRotationTestRepository) ActivatePKIEndpointCandidate(
	_ context.Context,
	activation PKIEndpointActivation,
	validateCommit PKIEndpointActivationCommitValidator,
) error {
	if validatePKIMutationLeaseFence(activation.Lease) != nil {
		return ErrPKILeaseNotHeld
	}
	if validateCommit == nil {
		return ErrPKILifecycleInvalid
	}
	if err := validateCommit(activation.Event.OccurredAt); err != nil {
		return err
	}
	state := r.states[activation.IdentityID]
	state.CertificateID = activation.Candidate.CertificateID
	state.CertificateFingerprintSHA256 = activation.Candidate.CertificateFingerprintSHA256
	state.PublicKeyFingerprintSHA256 = activation.Candidate.PublicKeyFingerprintSHA256
	state.Generation = activation.Candidate.Generation
	state.NotBefore = activation.Candidate.NotBefore
	state.NotAfter = activation.Candidate.NotAfter
	state.FailureCount = 0
	state.NextAttemptAt = time.Time{}
	r.states[activation.IdentityID] = state
	r.activations++
	return nil
}

func (r *pkiEndpointRotationTestRepository) state(identityID string) PKIEndpointCertificateState {
	return r.states[identityID]
}

type pkiEndpointRotationTestRotator struct {
	errByIdentity       map[string]error
	candidateByIdentity map[string]PKIEndpointRotationCandidate
}

func (r *pkiEndpointRotationTestRotator) StageAndVerifyPKIEndpoint(_ context.Context, active PKIEndpointCertificateState, _ bool) (PKIEndpointRotationCandidate, error) {
	if err := r.errByIdentity[active.IdentityID]; err != nil {
		return PKIEndpointRotationCandidate{}, err
	}
	return r.candidateByIdentity[active.IdentityID], nil
}

type lifecycleReconcileStore struct {
	operation storage.PluginOperationRow
	statuses  []storage.PluginAgentRuntimeStatusRow
}

func (s *lifecycleReconcileStore) RecordPluginAgentRuntimeReport(_ context.Context, report storage.PluginGenerationReport) (storage.PluginAgentRuntimeStatusRow, bool, error) {
	for index := range s.statuses {
		row := &s.statuses[index]
		if row.OperationID == report.OperationID && row.AgentID == report.AgentID && row.InstanceID == report.InstanceID {
			row.State, row.ReportSequence = report.State, report.Sequence
			return *row, false, nil
		}
	}
	return storage.PluginAgentRuntimeStatusRow{}, false, storage.ErrPluginGenerationStale
}

func (s *lifecycleReconcileStore) ListPluginAgentRuntimeStatuses(context.Context, string) ([]storage.PluginAgentRuntimeStatusRow, error) {
	return append([]storage.PluginAgentRuntimeStatusRow(nil), s.statuses...), nil
}

func (s *lifecycleReconcileStore) GetPluginOperation(context.Context, string) (storage.PluginOperationRow, bool, error) {
	return s.operation, true, nil
}

type lifecycleCompletionRecorder struct {
	kind         string
	result       PluginApplyResult
	plan         controlPlanePluginRuntimePlan
	agentResults map[string]any
}

func (r *lifecycleCompletionRecorder) CompleteLifecycleApply(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "lifecycle", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteConfigure(_ context.Context, result PluginApplyResult) (storage.PluginInstanceRow, error) {
	r.kind, r.result = "configure", result
	return storage.PluginInstanceRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteUpgrade(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "upgrade", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteRollback(_ context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.kind, r.result = "rollback", result
	return storage.InstalledPluginRow{}, nil
}
func (r *lifecycleCompletionRecorder) CompleteTrustedRevisionOperation(_ context.Context, operation storage.PluginOperationRow, applied bool, agentResults any) error {
	r.kind = "trusted-" + operation.Kind
	r.result = PluginApplyResult{PluginID: operation.PluginID, Applied: applied}
	r.agentResults, _ = agentResults.(map[string]any)
	return nil
}

func (r *lifecycleCompletionRecorder) controlPlaneRuntimePlan(context.Context, storage.PluginOperationRow) (controlPlanePluginRuntimePlan, error) {
	return r.plan, nil
}

type failingPluginControlPlaneRuntime struct{ err error }

func (r *failingPluginControlPlaneRuntime) ActivateBatch(context.Context, []pluginhost.Candidate) ([]*pluginhost.Instance, error) {
	return nil, r.err
}
func (*failingPluginControlPlaneRuntime) ActiveGeneration(string) (string, bool) { return "", false }
func (*failingPluginControlPlaneRuntime) Stop(context.Context, string) error     { return nil }

type fakeDDNSStore struct {
	rows map[string]storage.AgentRow
}

func (f *fakeDDNSStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	out := make([]storage.AgentRow, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeDDNSStore) UpdateDdnsStatusColumn(_ context.Context, agentID, statusJSON string) error {
	row := f.rows[agentID]
	row.DdnsStatusJSON = statusJSON
	f.rows[agentID] = row
	return nil
}

func (f *fakeDDNSStore) status(id string) storage.DdnsStatus {
	var status storage.DdnsStatus
	_ = json.Unmarshal([]byte(f.rows[id].DdnsStatusJSON), &status)
	return status
}

type fakeCFCall struct {
	token string
	fqdn  string
}

type fakeCFClient struct {
	calls    int
	recorded []fakeCFCall
}

func (c *fakeCFClient) EnsureRecord(_ context.Context, token, fqdn, _, _ string, _ int) (cloudflareRecordOutcome, error) {
	c.calls++
	c.recorded = append(c.recorded, fakeCFCall{token: token, fqdn: fqdn})
	return cloudflareRecordOutcome{}, nil
}
