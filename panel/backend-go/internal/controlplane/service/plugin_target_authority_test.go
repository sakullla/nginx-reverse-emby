//go:build exhaustive && !integration

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
	"strings"
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

	wantError := "invalid argument: plugin target is ineligible for declared runtime faces: plugin official.control-plane-only only accepts the canonical local target local"
	for _, test := range []struct {
		name       string
		target     string
		instanceID string
	}{
		{name: "remote", target: "edge-a", instanceID: "remote-instance"},
		{name: "whitespace alias", target: " local ", instanceID: "alias-instance"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err = fixture.service.ConfigureMutation(ctx, fixture.configureRequest(test.target, test.instanceID))
			if !errors.Is(err, ErrPluginTargetIneligible) || err.Error() != wantError {
				t.Fatalf("ConfigureMutation() error = %v, want %q", err, wantError)
			}
		})
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

func TestPluginDeleteInstanceIgnoresTargetAgentDeletedOutOfBand(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.stale-agent", true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
	instanceID := strings.Repeat("f", 64)

	if _, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", instanceID)); err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	instance := mustPluginInstanceByID(t, fixture.store, instanceID)
	installed, ok, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	if _, err := fixture.service.CompleteConfigure(ctx, PluginApplyResult{
		PluginID: fixture.pluginID, InstanceID: instance.ID, OperationID: instance.PendingOperationID,
		TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest,
		ConfigVersion: instance.PendingVersion, ActorID: "admin", Applied: true, AgentResults: map[string]any{},
	}); err != nil {
		t.Fatalf("CompleteConfigure() error = %v", err)
	}
	if err := fixture.store.DeleteAgent(ctx, "edge-a"); err != nil {
		t.Fatalf("DeleteAgent() error = %v", err)
	}

	err = fixture.service.DeleteInstanceMutation(ctx, PluginDeleteInstanceRequest{
		PluginID: fixture.pluginID, InstanceID: instanceID, ActorID: "admin", Actor: pluginPublishAdmin(),
	})
	if err != nil {
		t.Fatalf("DeleteInstanceMutation() error = %v", err)
	}
	if _, found, err := fixture.store.GetPluginInstance(ctx, instanceID); err != nil || found {
		t.Fatalf("GetPluginInstance() after delete = (_, %v, %v), want deleted", found, err)
	}
}

func TestPluginConfigureAllowsDualFaceEmptyTargetsToFormRemoteAgentGeneration(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.dual-management-only", true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
	request := fixture.configureRequest("edge-a", "management-only-instance")
	request.Targets = []string{}

	instance, err := fixture.service.ConfigureMutation(ctx, request)
	if err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	if len(instance.PendingTargets) != 0 {
		t.Fatalf("implicit dual-face pending targets = %v", instance.PendingTargets)
	}
	localGenerations, err := fixture.store.LoadAgentPluginGenerations(ctx, "local", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations(local) error = %v", err)
	}
	if len(localGenerations) != 0 {
		t.Fatalf("embedded local Agent generations = %+v", localGenerations)
	}
	generations, err := fixture.store.LoadAgentPluginGenerations(ctx, "edge-a", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations(edge-a) error = %v", err)
	}
	if len(generations) != 1 {
		t.Fatalf("implicit remote Agent generations = %+v", generations)
	}
	generation := generations[0]
	if generation.InstanceID != "management-only-instance" || generation.Target.ID != "edge-a" || generation.Runtime.HostScope != "agent" {
		t.Fatalf("implicit remote Agent generation authority = %+v", generation)
	}
}

func TestPluginEnableCompletesDualFaceEmptyTargetsWithoutAgentRuntime(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.dual-enable-empty", true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
	request := fixture.configureRequest("edge-a", "dual-enable-instance")
	request.Targets = []string{}
	if _, err := fixture.service.ConfigureMutation(ctx, request); err != nil {
		t.Fatalf("ConfigureMutation() error = %v", err)
	}
	if err := fixture.service.reconcilePendingPluginOperation(ctx, fixture.pluginID); err != nil {
		t.Fatalf("reconcile configure error = %v", err)
	}
	if _, err := fixture.service.DisableMutation(ctx, fixture.pluginID, "admin"); err != nil {
		t.Fatalf("DisableMutation() error = %v", err)
	}
	if err := fixture.service.reconcilePendingPluginOperation(ctx, fixture.pluginID); err != nil {
		t.Fatalf("reconcile disable error = %v", err)
	}
	if _, err := fixture.service.EnableMutation(ctx, fixture.pluginID, "admin"); err != nil {
		t.Fatalf("EnableMutation() error = %v", err)
	}
	if err := fixture.service.reconcilePendingPluginOperation(ctx, fixture.pluginID); err != nil {
		t.Fatalf("reconcile enable error = %v", err)
	}
	installed, ok, err := fixture.store.GetInstalledPlugin(ctx, fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	if installed.CurrentLifecycle != "active" || installed.PendingOperationID != "" || installed.PendingKind != "" {
		t.Fatalf("enabled plugin still pending: %+v", installed)
	}
}

func TestPluginConfigureRejectsSecondGlobalControlPlaneInstance(t *testing.T) {
	fixture := newPluginTargetAuthorityFixture(t, "official.singleton-app", true, true)
	ctx := WithSystemMutationPrincipal(t.Context(), "system:test")

	if _, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", "singleton-a")); err != nil {
		t.Fatalf("first ConfigureMutation() error = %v", err)
	}
	completePluginTargetConfigure(t, fixture, "singleton-a")

	_, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", "singleton-b"))
	if !errors.Is(err, ErrPluginConflict) || !strings.Contains(err.Error(), "already has an instance") {
		t.Fatalf("second ConfigureMutation() error = %v, want singleton conflict", err)
	}
	instances, listErr := fixture.store.ListPluginInstances(ctx, fixture.pluginID)
	if listErr != nil || len(instances) != 1 || instances[0].ID != "singleton-a" {
		t.Fatalf("instances after rejected configure = %+v, err=%v", instances, listErr)
	}
}

func TestPluginDetailProjectsManifestDerivedFacesAndTargetEligibility(t *testing.T) {
	t.Run("control-plane only", func(t *testing.T) {
		fixture := newPluginTargetAuthorityFixture(t, "official.detail-control", false)
		ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
		if _, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("local", "detail-control-instance")); err != nil {
			t.Fatalf("ConfigureMutation() error = %v", err)
		}
		detail, err := fixture.service.Detail(ctx, fixture.pluginID)
		if err != nil {
			t.Fatalf("Detail() error = %v", err)
		}
		wantFaces := []PluginDeploymentFace{{FaceID: "local-management", HostScope: "control-plane"}}
		if !reflect.DeepEqual(detail.Faces, wantFaces) || detail.TargetEligibility.CanonicalLocalTargetID != "local" || detail.TargetEligibility.AgentTargetsAllowed {
			t.Fatalf("control-plane detail projection = faces:%+v eligibility:%+v", detail.Faces, detail.TargetEligibility)
		}
		if len(detail.AgentStatuses) != 0 {
			t.Fatalf("control-plane Agent statuses = %+v", detail.AgentStatuses)
		}
	})

	t.Run("dual face", func(t *testing.T) {
		fixture := newPluginTargetAuthorityFixture(t, "official.detail-dual", true)
		ctx := WithSystemMutationPrincipal(t.Context(), "system:test")
		if _, err := fixture.service.ConfigureMutation(ctx, fixture.configureRequest("edge-a", "detail-dual-instance")); err != nil {
			t.Fatalf("ConfigureMutation() error = %v", err)
		}
		detail, err := fixture.service.Detail(ctx, fixture.pluginID)
		if err != nil {
			t.Fatalf("Detail() error = %v", err)
		}
		wantFaces := []PluginDeploymentFace{{FaceID: "local-management", HostScope: "control-plane"}, {FaceID: "agent-execution", HostScope: "agent"}}
		if !reflect.DeepEqual(detail.Faces, wantFaces) || detail.TargetEligibility.CanonicalLocalTargetID != "local" || !detail.TargetEligibility.AgentTargetsAllowed {
			t.Fatalf("dual-face detail projection = faces:%+v eligibility:%+v", detail.Faces, detail.TargetEligibility)
		}
		if len(detail.AgentStatuses) == 0 {
			t.Fatal("dual-face detail omitted Agent execution statuses")
		}
		for _, status := range detail.AgentStatuses {
			if status.FaceID != "agent-execution" {
				t.Fatalf("Agent status face_id = %q", status.FaceID)
			}
		}
	})
}

func TestPluginAgentFaceStatusSummaryExcludesControlPlaneRuntimeFailure(t *testing.T) {
	summary, err := pluginAgentFaceStatusSummary(`{"control-plane-runtime":{"state":"failed"},"edge-a/instance-a":{"state":"active"}}`)
	if err != nil {
		t.Fatalf("pluginAgentFaceStatusSummary() error = %v", err)
	}
	if strings.Contains(string(summary), "control-plane-runtime") || !strings.Contains(string(summary), "edge-a/instance-a") {
		t.Fatalf("Agent face status summary = %s", summary)
	}
}

func TestPluginAgentStatusUsesPerAgentRuntimeRevision(t *testing.T) {
	digest := strings.Repeat("a", 64)
	statuses := appendPluginAgentStatuses(nil,
		map[string]storage.AgentRow{"edge-a": {ID: "edge-a", CurrentRevision: 439, DesiredRevision: 1}},
		map[string]storage.PluginOperationRow{"operation": {ID: "operation", Kind: "configure", Status: "succeeded", TargetRevision: 457}},
		map[string]storage.PluginAgentRuntimeStatusRow{"operation\x00edge-a\x00instance": {
			OperationID: "operation", AgentID: "edge-a", InstanceID: "instance", Revision: 439,
			GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "active",
		}},
		storage.InstalledPluginRow{}, storage.PluginInstanceRow{ID: "instance", CurrentState: "active"},
		[]string{"edge-a"}, "active", "operation", json.RawMessage(`{}`),
	)
	if len(statuses) != 1 || statuses[0].TargetRevision != 439 || statuses[0].CurrentRevision != 439 {
		t.Fatalf("Agent status revisions = %+v, want per-Agent target/current revision 439", statuses)
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

func newPluginTargetAuthorityFixture(t *testing.T, pluginID string, dualFace bool, singletonSurface ...bool) pluginTargetAuthorityFixture {
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
	candidate := importPackageCandidate(t, cacheRoot, writePluginTargetAuthorityPackage(t, pluginID, dualFace, key, len(singletonSurface) > 0 && singletonSurface[0]), validator, trust)
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

func completePluginTargetConfigure(t *testing.T, fixture pluginTargetAuthorityFixture, instanceID string) {
	t.Helper()
	instance := mustPluginInstanceByID(t, fixture.store, instanceID)
	installed, ok, err := fixture.store.GetInstalledPlugin(t.Context(), fixture.pluginID)
	if err != nil || !ok {
		t.Fatalf("GetInstalledPlugin() ok=%v err=%v", ok, err)
	}
	if _, err := fixture.service.CompleteConfigure(t.Context(), PluginApplyResult{
		PluginID: fixture.pluginID, InstanceID: instance.ID, OperationID: instance.PendingOperationID,
		TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest,
		ConfigVersion: instance.PendingVersion, ActorID: "admin", Applied: true, AgentResults: map[string]any{},
	}); err != nil {
		t.Fatalf("CompleteConfigure() error = %v", err)
	}
}

func (f pluginTargetAuthorityFixture) configureRequest(target, instanceID string) PluginConfigureRequest {
	chains := []string{}
	return PluginConfigureRequest{PluginID: f.pluginID, InstanceID: instanceID, ResourceGroupID: "default", Targets: []string{target}, PolicyChains: &chains, Config: json.RawMessage(`{}`), SecretReplacements: map[string]json.RawMessage{"/token": json.RawMessage(`"fixture-secret"`)}, ActorID: "admin", Actor: pluginPublishAdmin()}
}

func writePluginTargetAuthorityPackage(t *testing.T, pluginID string, dualFace bool, key ed25519.PrivateKey, singletonSurface bool) string {
	t.Helper()
	root := t.TempDir()
	writePublishFile(t, root, plugins.ConfigSchemaFile, targetAuthorityConfigSchema)
	artifact, artifactPath := publishRPCArtifact(t, root)
	digest := sha256.Sum256(artifact)
	hostScopes := ""
	if dualFace {
		hostScopes = "  host_scopes: [control-plane, agent]\n"
	}
	extensions := "extension_points: [dns.provider]\n"
	if singletonSurface {
		extensions = "extension_points: [dns.provider, ui.route]\nui_route_id: singleton-app\n"
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
%spermissions: []
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
`, pluginID, hostScopes, artifactPath, hex.EncodeToString(digest[:]), len(artifact), runtime.GOOS, runtime.GOARCH, extensions)
	writePublishFile(t, root, plugins.PackageManifestFile, manifest)
	packageDigest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writePublishFile(t, root, plugins.PackageDigestFile, packageDigest+"\n")
	writePublishFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(packageDigest)))+"\n")
	return root
}
