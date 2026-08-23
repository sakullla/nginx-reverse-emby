//go:build !integration

package service

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const targetAuthorityConfigSchema = `{
  "type":"object",
  "additionalProperties":false,
  "properties":{"token":{"type":"string","writeOnly":true}}
}`

func TestPluginConfigureRejectsRemoteTargetForControlPlaneOnlyManifestWithoutSideEffects(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.control-plane-only", false)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")

	beforeInstalled, ok, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	beforeInstances, err := fixture.store.ListPluginInstances(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	beforeOperations, err := fixture.store.ListPluginOperations(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRuntimeStatuses := pluginRuntimeStatusCount(t, fixture.store, beforeOperations)
	beforeSecrets, err := fixture.store.ListSecrets(ctx, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	beforeLocalRevisions, err := fixture.store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	beforeRemoteRevisions, err := fixture.store.ListAgentRevisions(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", "remote-instance"))
	wantError := "invalid argument: plugin target is ineligible for declared runtime faces: plugin official.control-plane-only only accepts the canonical local target local"
	if !errors.Is(err, ErrPluginTargetIneligible) || err.Error() != wantError {
		t.Fatalf("ConfigureMutation() error = %v, want %q", err, wantError)
	}

	afterInstalled, ok, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() after rejection ok=%v err=%v", ok, err)
	}
	afterInstances, err := fixture.store.ListPluginInstances(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	afterOperations, err := fixture.store.ListPluginOperations(ctx, fixture.pluginID)
	if err != nil {
		t.Fatal(err)
	}
	afterRuntimeStatuses := pluginRuntimeStatusCount(t, fixture.store, afterOperations)
	afterSecrets, err := fixture.store.ListSecrets(ctx, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	afterLocalRevisions, err := fixture.store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	afterRemoteRevisions, err := fixture.store.ListAgentRevisions(ctx, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterInstalled, beforeInstalled) ||
		!reflect.DeepEqual(afterInstances, beforeInstances) ||
		!reflect.DeepEqual(afterOperations, beforeOperations) ||
		afterRuntimeStatuses != beforeRuntimeStatuses ||
		!reflect.DeepEqual(afterSecrets, beforeSecrets) ||
		!reflect.DeepEqual(afterLocalRevisions, beforeLocalRevisions) ||
		!reflect.DeepEqual(afterRemoteRevisions, beforeRemoteRevisions) {
		t.Fatalf("remote target rejection changed durable state: installed=%t instances=%d/%d operations=%d/%d runtime_statuses=%d/%d secrets=%d/%d local_revisions=%d/%d remote_revisions=%d/%d",
			!reflect.DeepEqual(afterInstalled, beforeInstalled), len(beforeInstances), len(afterInstances), len(beforeOperations), len(afterOperations), beforeRuntimeStatuses, afterRuntimeStatuses, len(beforeSecrets), len(afterSecrets), len(beforeLocalRevisions), len(afterLocalRevisions), len(beforeRemoteRevisions), len(afterRemoteRevisions))
	}

	local, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("local", "local-instance"))
	if err != nil {
		t.Fatalf("ConfigureMutation(local) error = %v", err)
	}
	if !reflect.DeepEqual(local.PendingTargets, []string{"local"}) {
		t.Fatalf("local pending targets = %v", local.PendingTargets)
	}
}

func TestPluginConfigureAllowsDualFaceManifestToFormRemoteAgentGeneration(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.dual-face", true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")

	instance, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", "dual-instance"))
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	if !reflect.DeepEqual(instance.PendingTargets, []string{"edge-a"}) {
		t.Fatalf("pending targets = %v", instance.PendingTargets)
	}
	generations, err := fixture.store.LoadAgentPluginGenerations(ctx, "edge-a", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations() error = %v", err)
	}
	if len(generations) != 1 {
		t.Fatalf("Agent generations = %+v", generations)
	}
	generation := generations[0]
	if generation.InstanceID != "dual-instance" || generation.Target.ID != "edge-a" || generation.Runtime.HostScope != "agent" {
		t.Fatalf("Agent generation authority = %+v", generation)
	}
	if generation.Artifact.GOOS != runtime.GOOS || generation.Artifact.GOARCH != runtime.GOARCH || !generation.Artifact.SignatureVerified {
		t.Fatalf("Agent generation artifact = %+v", generation.Artifact)
	}
}

type pluginTargetAuthorityFixture struct {
	pluginID string
	store    *storage.GormStore
	service  *PluginService
}

func pluginRuntimeStatusCount(t *testing.T, store *storage.GormStore, operations []storage.PluginOperationRow) int {
	t.Helper()
	total := 0
	for _, operation := range operations {
		rows, err := store.ListPluginAgentRuntimeStatuses(t.Context(), operation.ID)
		if err != nil {
			t.Fatal(err)
		}
		total += len(rows)
	}
	return total
}

func newPluginTargetAuthorityFixture(t *testing.T, pluginID string, dualFace bool) pluginTargetAuthorityFixture {
	t.Helper()
	store := newServiceOwnerStore(t)
	now := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	if err := store.UpsertBuiltinResourceGroup(t.Context(), storage.ResourceGroupRow{ID: "default", Name: "default", Description: "default", Builtin: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"local", "edge-a"} {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: id, Name: id, Version: "1.0.0", Platform: runtime.GOOS + "-" + runtime.GOARCH, CapabilitiesJSON: `["package_manifest_v1","plugin_generation_v1"]`}); err != nil {
			t.Fatal(err)
		}
	}

	cacheRoot := filepath.Join(t.TempDir(), "plugins", "packages")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })
	key := publishFixtureSigningKey()
	publicKey := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trust := marketplace.SignatureTrust{SourceID: "target-authority-fixture", SourceKind: marketplace.SourceKindCustom, KeyID: "test-fixture", PublicKey: publicKey, Fingerprint: fingerprint}
	validator := plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "0.0.0-dev", TrustedSigners: map[string]ed25519.PublicKey{"test-fixture": key.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact, TargetGOOS: runtime.GOOS, TargetGOARCH: runtime.GOARCH})
	candidate := importPackageCandidate(t, cacheRoot, writePluginTargetAuthorityPackage(t, pluginID, dualFace, key), validator, trust)
	service := NewPluginServiceWithValidator(store, validator, cacheRoot)
	if _, err := service.Install(t.Context(), PluginInstallRequest{Package: candidate, ActorID: "admin", RiskAccepted: true}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if _, err := service.Enable(t.Context(), pluginID, "admin"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	keyBytes := []byte("0123456789abcdef0123456789abcdef")
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": keyBytes}})
	if err != nil {
		t.Fatal(err)
	}
	service.SetSecretVault(vault)
	service.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	return pluginTargetAuthorityFixture{pluginID: pluginID, store: store, service: service}
}

func (f pluginTargetAuthorityFixture) configureRequest(target, instanceID string) PluginConfigureRequest {
	chains := []string{}
	return PluginConfigureRequest{PluginID: f.pluginID, InstanceID: instanceID, ResourceGroupID: "default", Targets: []string{target}, PolicyChains: &chains, Config: json.RawMessage(`{}`), SecretReplacements: map[string]json.RawMessage{"/token": json.RawMessage(`"fixture-secret"`)}, ActorID: "admin", Actor: pluginPublishAdmin()}
}

func writePluginTargetAuthorityPackage(t *testing.T, pluginID string, dualFace bool, key ed25519.PrivateKey) string {
	t.Helper()
	root := t.TempDir()
	writePublishFile(t, root, plugins.ConfigSchemaFile, targetAuthorityConfigSchema)
	artifact, artifactPath := publishRPCArtifact(t, root)
	digest := sha256.Sum256(artifact)
	hostScopes := ""
	if dualFace {
		hostScopes = "  host_scopes: [control-plane, agent]\n"
	}
	manifest := fmt.Sprintf(`schema_version: 1
id: %s
version: 1.0.0
name: Target Authority
compatibility:
  host: "*"
  agent: "*"
runtime:
  kind: rpc-service
  abi: nre:rpc/v1
  host_scope: control-plane
%s  entry: plugin
artifacts:
  - path: %s
    sha256: %s
    size: %d
    mode: executable
    goos: %s
    goarch: %s
extension_points: [dns.provider]
permissions: []
config_schema: config.schema.json
resource_budget:
  timeout_ms: 2000
  memory_bytes: 1048576
  concurrency: 4
  input_bytes: 65536
  output_bytes: 4096
  cpu_millis: 100
  restarts: 1
failure_policy:
  on_error: fail-closed
  on_budget: fail-closed
  restart: on-failure
  core_fallback: preserve
signature:
  algorithm: ed25519
  key_id: test-fixture
  file: package.sig
cleanup:
  instances: delete
  config: delete
  owned_data: delete
  grants: delete
  shared_refs: retain
  audit_events: retain
`, pluginID, hostScopes, artifactPath, hex.EncodeToString(digest[:]), len(artifact), runtime.GOOS, runtime.GOARCH)
	writePublishFile(t, root, plugins.PackageManifestFile, manifest)
	packageDigest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageDigestFile, packageDigest+"\n")
	writePublishFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(packageDigest)))+"\n")
	return root
}
