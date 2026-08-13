package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginWriteOnlySecretInstallConfigureDetailAndRestart(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test-secret")
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-peer", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "edge-peer-binding", ResourceKind: "agent", ResourceID: "edge-peer", ResourceGroupID: "default", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "other", Name: "Other", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-other", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "edge-other-binding", ResourceKind: "agent", ResourceID: "edge-other", ResourceGroupID: "other", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	cacheRoot := pluginTestCacheRoot(t)
	service := newPluginTestServiceAtRoot(t, store, cacheRoot)
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	service.SetSecretVault(vault)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	schema := `{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string"},"token":{"type":"string","writeOnly":true}},"required":["mode","token"]}`
	ui := `{"schema_version":1,"title":"Secret config","components":[{"type":"secret","id":"token","label":"Token","binding":"/token","required":true}],"actions":[{"type":"submit","id":"save","label":"Save"}]}`
	candidate := pluginCustomCandidateFixture(t, "official.secret-config", "1.0.0", cleanup, schema, "ui_schema: ui.schema.json\n", map[string]string{"ui.schema.json": ui}, "*", "*")
	installed, err := service.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "secret-instance", ResourceGroupID: "default", Targets: []string{"local", "edge-peer"}, Config: json.RawMessage(`{"mode":"observe","token":null}`), SecretReplacements: map[string]json.RawMessage{"/token": json.RawMessage(`"install-plaintext"`)}, ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(instance.PendingConfigJSON, "install-plaintext") || strings.Contains(instance.PendingSecretHandlesJSON, "install-plaintext") || instance.PendingSecretHandlesJSON == "[]" {
		t.Fatalf("plaintext persisted in staged instance: %+v", instance)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"edge-peer": "active", "local": "active"}))
	if err != nil {
		t.Fatal(err)
	}
	writer := authz.Actor{ID: "operator", Permissions: []string{authz.PermissionResourceWrite}, VisibleResourceGroups: []string{"default"}}
	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Targets: []string{"local", "edge-peer"}, Config: json.RawMessage(`{"mode":"block"}`), ActorID: writer.ID, Actor: writer}); err != nil {
		t.Fatalf("in-group writer save = %v", err)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"edge-peer": "active", "local": "active"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "writer-created", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: writer.ID, Actor: writer}); err == nil {
		t.Fatal("writer create was allowed")
	}
	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "other", Targets: []string{"edge-other"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: writer.ID, Actor: writer}); err == nil {
		t.Fatal("writer cross-group retarget was allowed")
	}
	detail, err := service.Detail(ctx, installed.PluginID)
	if err != nil || len(detail.Instances) != 1 || strings.Contains(string(detail.Instances[0].Config), "install-plaintext") || len(detail.Instances[0].SecretFields) != 1 || !detail.Instances[0].SecretFields[0].Present {
		t.Fatalf("detail=%+v error=%v", detail, err)
	}
	enabled, err := service.Enable(ctx, installed.PluginID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil || len(operations) == 0 {
		t.Fatal(err)
	}
	enableOperation := operations[len(operations)-1]
	if enableOperation.ID != enabled.PendingOperationID {
		t.Fatalf("enabled operation=%s pending=%s", enableOperation.ID, enabled.PendingOperationID)
	}
	var handles []storage.PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(instance.SecretHandlesJSON), &handles); err != nil || len(handles) != 1 {
		t.Fatalf("handles=%+v err=%v", handles, err)
	}
	generationID, artifactDigest := strings.Repeat("c", 64), strings.Repeat("d", 64)
	status := storage.PluginAgentRuntimeStatusRow{OperationID: enableOperation.ID, AgentID: "local", InstanceID: instance.ID, PluginID: installed.PluginID, ResourceGroupID: instance.ResourceGroupID, TargetVersion: instance.ConfigVersion, Revision: enableOperation.TargetRevision, GenerationID: generationID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, ConfigVersion: instance.ConfigVersion}
	peerStatus := status
	peerStatus.AgentID = "edge-peer"
	if err := store.StagePluginAgentRuntimeStatuses(ctx, []storage.PluginAgentRuntimeStatusRow{status, peerStatus}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordPluginAgentRuntimeReport(ctx, storage.PluginGenerationReport{OperationID: status.OperationID, AgentID: status.AgentID, InstanceID: status.InstanceID, PluginID: status.PluginID, Revision: status.Revision, GenerationID: status.GenerationID, PackageDigest: status.PackageDigest, ArtifactDigest: status.ArtifactDigest, State: "active", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	requestHandles := []storage.PluginGenerationSecretHandle{{ID: handles[0].ID, Version: handles[0].Version, Digest: handles[0].Digest, Purpose: handles[0].Purpose}}
	redeemed, err := service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles})
	if err != nil || len(redeemed.Secrets) != 1 || redeemed.Secrets[0].Value != `"install-plaintext"` {
		t.Fatalf("applying-window redeemed=%+v err=%v", redeemed, err)
	}
	if _, err := service.RedeemAgentPluginSecrets(ctx, "other-agent", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles}); err == nil {
		t.Fatal("cross-Agent secret redemption was accepted")
	}
	if _, _, err := store.RecordPluginAgentRuntimeReport(ctx, storage.PluginGenerationReport{OperationID: peerStatus.OperationID, AgentID: peerStatus.AgentID, InstanceID: peerStatus.InstanceID, PluginID: peerStatus.PluginID, Revision: peerStatus.Revision, GenerationID: peerStatus.GenerationID, PackageDigest: peerStatus.PackageDigest, ArtifactDigest: peerStatus.ArtifactDigest, State: "active", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	enabled, err = service.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"edge-peer": "active", "local": "active"}))
	if err != nil {
		t.Fatal(err)
	}
	committed, found, err := store.GetPluginAgentRuntimeStatusFence(ctx, status.OperationID, status.AgentID, status.InstanceID)
	if err != nil || !found || committed.AuthoritySlot != "active" {
		t.Fatalf("committed runtime authority=%+v found=%v err=%v", committed, found, err)
	}
	if _, err := service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles}); err != nil {
		t.Fatalf("committed generation could not redeem required secret: %v", err)
	}
	_, err = service.Disable(ctx, installed.PluginID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"edge-peer": "drained", "local": "drained"})); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles}); err == nil {
		t.Fatal("disabled historical generation redeemed current credentials")
	}
	if _, err = service.Enable(ctx, installed.PluginID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"edge-peer": "active", "local": "active"})); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles}); err == nil {
		t.Fatal("re-enabled plugin accepted superseded generation redemption")
	}
	rotating, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "other", Targets: []string{"edge-other"}, Config: json.RawMessage(`{"mode":"block","token":null}`), SecretReplacements: map[string]json.RawMessage{"/token": json.RawMessage(`"rotated-concurrent"`)}, ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	applyRotation := pendingApplyResult(t, store, installed.PluginID, rotating.ID, true, map[string]string{"edge-other": "active"})
	start := make(chan struct{})
	var concurrentRedeem PluginSecretRedemptionResponse
	var concurrentRedeemErr, completeRotationErr error
	var completedRotation storage.PluginInstanceRow
	var race sync.WaitGroup
	race.Add(2)
	go func() {
		defer race.Done()
		<-start
		concurrentRedeem, concurrentRedeemErr = service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles})
	}()
	go func() {
		defer race.Done()
		<-start
		completedRotation, completeRotationErr = service.CompleteConfigure(ctx, applyRotation)
	}()
	close(start)
	race.Wait()
	if completeRotationErr != nil {
		t.Fatal(completeRotationErr)
	}
	if concurrentRedeemErr == nil && (len(concurrentRedeem.Secrets) != 1 || concurrentRedeem.Secrets[0].Value != `"install-plaintext"`) {
		t.Fatalf("concurrent redemption crossed rotation boundary: %+v", concurrentRedeem)
	}
	if _, err := service.RedeemAgentPluginSecrets(ctx, "local", PluginSecretRedemptionRequest{Revision: uint64(status.Revision), GenerationID: generationID, InstanceID: instance.ID, PluginID: installed.PluginID, OperationID: enableOperation.ID, PackageDigest: enabled.ActivePackageDigest, ArtifactDigest: artifactDigest, Handles: requestHandles}); err == nil {
		t.Fatal("completed rebind/rotation accepted superseded credentials")
	}
	instance = completedRotation
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := newPluginTestServiceAtRoot(t, reopened, cacheRoot)
	restartedVault, err := secrets.NewVault(reopened, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	restarted.SetSecretVault(restartedVault)
	detail, err = restarted.Detail(ctx, installed.PluginID)
	if err != nil || strings.Contains(string(detail.Instances[0].Config), "install-plaintext") || !detail.Instances[0].SecretFields[0].Present {
		t.Fatalf("restart detail=%+v error=%v", detail, err)
	}
	preserved, err := restarted.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "other", Targets: []string{"edge-other"}, Config: json.RawMessage(`{"mode":"block","token":null}`), ActorID: "admin"})
	if err != nil || strings.Contains(preserved.PendingConfigJSON, "install-plaintext") || preserved.PendingSecretHandlesJSON != instance.SecretHandlesJSON {
		t.Fatalf("preserved=%+v error=%v", preserved, err)
	}
}

func TestPluginLifecycleStagesDesiredStateAndPreservesActiveOnFailure(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "other", Name: "Other", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-other", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "edge-other-binding", ResourceKind: "agent", ResourceID: "edge-other", ResourceGroupID: "other", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	service := newPluginTestServiceAtRoot(t, store, cacheRoot)

	cleanupRetain := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	packageV1 := pluginCandidateFixtureAtRoot(t, cacheRoot, "official.lifecycle", "1.0.0", []string{"http.inspect"}, cleanupRetain)
	if _, err := service.PluginService.Install(ctx, PluginInstallRequest{Package: packageV1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}}); !errors.Is(err, ErrPluginRiskConfirmation) {
		t.Fatalf("custom test fixture source did not require risk confirmation: %v", err)
	}

	untrusted := packageV1
	untrusted.sourceKind = "unknown"
	_, err = service.Install(ctx, PluginInstallRequest{Package: untrusted, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err == nil || errors.Is(err, ErrPluginRiskConfirmation) {
		t.Fatalf("unknown source kind was not rejected distinctly: %v", err)
	}
	spoofedOfficial := packageV1
	spoofedOfficial.sourceID = marketplace.OfficialSourceID
	if _, err := service.Install(ctx, PluginInstallRequest{Package: spoofedOfficial, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true}); err == nil {
		t.Fatal("non-official source provenance impersonated the official source")
	}
	installed, err := service.Install(ctx, PluginInstallRequest{Package: packageV1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if installed.DesiredLifecycle != "disabled" || installed.CurrentLifecycle != "disabled" {
		t.Fatalf("install state = %+v", installed)
	}
	packageRow, ok, err := store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	wantIdentity := storage.PluginPackageIdentity(packageV1.Package.Digest, packageV1.SignatureTrust.SourceID, packageV1.SignatureTrust.Fingerprint)
	if err != nil || !ok || installed.ActiveSourceID != packageV1.SignatureTrust.SourceID || installed.ActiveSourceKind != packageV1.SignatureTrust.SourceKind || packageRow.SourceID != packageV1.SignatureTrust.SourceID || packageRow.SourceKind != packageV1.SignatureTrust.SourceKind || packageRow.Identity != wantIdentity {
		t.Fatalf("installed lifecycle source provenance = %+v, %+v, %v, %v", installed, packageRow, ok, err)
	}
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
	foundSource := false
	for _, operation := range operations {
		if operation.Status == "succeeded" && operation.SourceID == packageV1.SignatureTrust.SourceID && operation.SourceKind == packageV1.SignatureTrust.SourceKind {
			foundSource = true
		}
	}
	if err != nil || !foundSource {
		t.Fatalf("install operation source provenance = %+v, %v", operations, err)
	}
	for name, request := range map[string]PluginConfigureRequest{
		"empty-group":   {PluginID: installed.PluginID, InstanceID: "scope-empty", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"},
		"missing-group": {PluginID: installed.PluginID, InstanceID: "scope-missing", ResourceGroupID: "missing", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"},
		"cross-group":   {PluginID: installed.PluginID, InstanceID: "scope-cross", ResourceGroupID: "other", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"},
	} {
		if _, err := service.Configure(ctx, request); err == nil {
			t.Fatalf("%s plugin scope was accepted", name)
		}
	}
	deniedCtx := WithResourceAuthorizer(storage.WithQuotaActor(context.Background(), storage.QuotaActor{UserID: "limited"}), func(_ context.Context, kind, id string) error {
		if kind == "agent" && id == "edge-other" {
			return errors.New("forbidden target")
		}
		return nil
	})
	if _, err := service.Configure(deniedCtx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "scope-denied", ResourceGroupID: "other", Targets: []string{"edge-other"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "forged"}); err == nil {
		t.Fatal("cross-group actor target authorization was bypassed")
	}

	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Config: json.RawMessage(`{"other":true}`), ActorID: "admin"}); err == nil {
		t.Fatal("invalid plugin config was accepted")
	}
	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: " invalid ", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("non-canonical instance identity error = %v", err)
	}
	invalidPolicyChains := []string{" shared "}
	if _, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "invalid-chain", ResourceGroupID: "default", Targets: []string{"local"}, PolicyChains: &invalidPolicyChains, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("non-canonical policy chain error = %v", err)
	}
	policyChains := []string{"shared"}
	instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Targets: []string{"local"}, PolicyChains: &policyChains, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if instance.CurrentState != "applying" || instance.ConfigVersion != 0 || instance.PendingVersion != 1 || instance.PendingPolicyChainsJSON != `["shared"]` {
		t.Fatalf("configure did not stage desired config: %+v", instance)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, false, map[string]string{"local": "failed"}))
	if err != nil {
		t.Fatal(err)
	}
	if instance.ConfigVersion != 0 || instance.ConfigJSON != "{}" || instance.PendingVersion != 0 {
		t.Fatalf("failed configure changed current config: %+v", instance)
	}
	instance, err = service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Targets: []string{"local"}, PolicyChains: &policyChains, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"local": "applied"}))
	if err != nil || instance.ConfigVersion != 1 || instance.ConfigJSON != `{"mode":"observe"}` || instance.PolicyChainsJSON != `["shared"]` {
		t.Fatalf("completed config = %+v, %v", instance, err)
	}
	if binding, err := store.GetResourceBinding(ctx, "plugin_instance", instance.ID); err != nil || binding.ResourceGroupID != "default" {
		t.Fatalf("plugin instance ownership binding = %+v, %v", binding, err)
	}
	instance, err = service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "other", Targets: []string{"edge-other"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil || instance.ResourceGroupID != "default" || instance.TargetJSON != `["local"]` || instance.PendingResourceGroupID != "other" || instance.PendingTargetJSON != `["edge-other"]` {
		t.Fatalf("target/resource group was not staged separately: %+v, %v", instance, err)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, false, nil))
	if err != nil || instance.ResourceGroupID != "default" || instance.TargetJSON != `["local"]` || instance.PendingResourceGroupID != "" || instance.PendingTargetJSON != "" {
		t.Fatalf("failed configure changed current target/resource group: %+v, %v", instance, err)
	}

	installed, err = service.Enable(ctx, installed.PluginID, "admin")
	if err != nil || installed.DesiredLifecycle != "enabled" || installed.CurrentLifecycle != "applying" {
		t.Fatalf("enable desired/current = %+v, %v", installed, err)
	}
	stagedInstance, found, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !found || !stagedInstance.DesiredEnabled || stagedInstance.CurrentState != "applying" {
		t.Fatalf("enable did not stage instance runtime intent: %+v found=%v err=%v", stagedInstance, found, err)
	}
	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); !errors.Is(err, ErrPluginUninstallBlocked) {
		t.Fatalf("uninstall while applying = %v", err)
	}
	enableResult := pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"local": "applied"})
	installed, err = service.CompleteLifecycleApply(ctx, enableResult)
	if err != nil || installed.CurrentLifecycle != "active" {
		t.Fatalf("enable completion = %+v, %v", installed, err)
	}
	policies, err := store.LoadAgentPluginPolicies(ctx, "local")
	if err != nil || len(policies) != 1 || len(policies[0].Stages) != 1 {
		t.Fatalf("active policy catalog = %+v, %v", policies, err)
	}
	artifactID := policies[0].Stages[0].ArtifactSource.ArtifactID
	issuedRevision := int64(71)
	sharedStage := policies[0].Stages[0]
	sharedStage.PolicyID = "shared-copy"
	issuedSnapshot := storage.Snapshot{Revision: issuedRevision, PluginPolicies: append(policies, storage.PluginPolicy{ID: "shared-copy", Stages: []storage.PolicyStage{sharedStage}})}
	payload, err := json.Marshal(issuedSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshotSum := sha256.Sum256(payload)
	snapshotDigest := hex.EncodeToString(snapshotSum[:])
	policyArtifacts, policyRefs, err := store.BuildAgentRevisionPolicyArtifacts(ctx, "local", issuedRevision, issuedSnapshot, time.Now().UTC())
	if err != nil || len(policyArtifacts) != 1 || len(policyRefs) != 1 {
		t.Fatalf("revision policy artifact projection = %d/%d, %v", len(policyArtifacts), len(policyRefs), err)
	}
	now := time.Now().UTC()
	if err := store.CreateRevisionLedger(ctx, storage.RevisionLedgerWrite{
		Operation:    storage.OperationRow{ID: "plugin-artifact-revision", Kind: "test", Status: storage.OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Artifacts:    append([]storage.GenerationArtifactRow{{ID: "snapshot-" + snapshotDigest, Kind: "agent_snapshot", SHA256: snapshotDigest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now}}, policyArtifacts...),
		Revisions:    []storage.AgentRevisionRow{{AgentID: "local", Revision: issuedRevision, OperationID: "plugin-artifact-revision", State: storage.AgentRevisionStatePending, SnapshotArtifactID: "snapshot-" + snapshotDigest, SnapshotDigest: snapshotDigest, CreatedAt: now, UpdatedAt: now}},
		ArtifactRefs: policyRefs,
	}); err != nil {
		t.Fatal(err)
	}
	artifact, err := service.ResolveAgentPluginArtifact(ctx, "local", issuedRevision, snapshotDigest, artifactID)
	if err != nil || artifact.SHA256 != policies[0].Stages[0].ArtifactDigest || artifact.SizeBytes <= 0 {
		t.Fatalf("resolved Agent artifact = %+v, %v", artifact, err)
	}
	if _, err := service.ResolveAgentPluginArtifact(ctx, "edge-other", issuedRevision, snapshotDigest, artifactID); !errors.Is(err, ErrPluginArtifactUnavailable) {
		t.Fatalf("cross-Agent artifact resolution error = %v", err)
	}
	if _, err := service.CompleteLifecycleApply(ctx, enableResult); err == nil {
		t.Fatal("replayed lifecycle completion was accepted")
	}
	operations, err = store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	foundOriginal := false
	for _, operation := range operations {
		if operation.Kind == "lifecycle_complete" || operation.Kind == "upgrade_complete" || operation.Kind == "rollback_complete" || operation.Kind == "configure_complete" {
			t.Fatalf("completion inserted a detached operation: %+v", operation)
		}
		if operation.ID == enableResult.OperationID {
			foundOriginal = operation.Status == "succeeded" && operation.CompletedAt != nil
		}
	}
	if !foundOriginal {
		t.Fatal("completion did not finish the originating operation")
	}
	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); !errors.Is(err, ErrPluginUninstallBlocked) {
		t.Fatalf("active plugin uninstall bypassed disable: %v", err)
	}
	installed, err = service.Disable(ctx, installed.PluginID, "admin")
	if err != nil || installed.CurrentLifecycle != "applying" {
		t.Fatalf("disable did not enter applying: %+v, %v", installed, err)
	}
	installed, err = service.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil || installed.CurrentLifecycle != "disabled" {
		t.Fatalf("disable completion = %+v, %v", installed, err)
	}

	packageV2 := pluginCandidateFixtureAtRoot(t, cacheRoot, installed.PluginID, "2.0.0", []string{"http.inspect", "http.respond"}, cleanupRetain)
	if _, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: packageV2, ActorID: "admin"}); !errors.Is(err, ErrPluginPermissionConfirmation) {
		t.Fatalf("upgrade permission increase did not require confirmation: %v", err)
	}
	current, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != packageV1.Package.Digest || current.StagedPackageDigest != "" {
		t.Fatalf("failed upgrade changed package pointers: %+v", current)
	}
	upgrade := PluginUpgradeRequest{PluginID: installed.PluginID, Package: packageV2, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect", "http.respond"}}
	staged, err := service.Upgrade(ctx, upgrade)
	if err != nil || staged.ActivePackageDigest != packageV1.Package.Digest || staged.StagedPackageDigest != packageV2.Package.Digest || staged.CurrentLifecycle != "upgrading" {
		t.Fatalf("staged upgrade = %+v, %v", staged, err)
	}
	if _, err := service.Enable(ctx, installed.PluginID, "admin"); err == nil {
		t.Fatal("enable interleaved with a staged upgrade")
	}
	stale := pendingApplyResult(t, store, installed.PluginID, "", false, nil)
	stale.TargetRevision++
	if _, err := service.CompleteUpgrade(ctx, stale); err == nil {
		t.Fatal("out-of-order upgrade completion was accepted")
	}
	failed, err := service.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", false, map[string]string{"local": "failed"}))
	if err != nil || failed.ActivePackageDigest != packageV1.Package.Digest || failed.StagedPackageDigest != "" {
		t.Fatalf("failed upgrade promoted package: %+v, %v", failed, err)
	}
	staged, err = service.Upgrade(ctx, upgrade)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := service.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"local": "applied"}))
	if err != nil || upgraded.ActivePackageDigest != packageV2.Package.Digest || upgraded.RollbackPackageDigest != packageV1.Package.Digest {
		t.Fatalf("upgrade completion = %+v, %v", upgraded, err)
	}

	rollingBack, err := service.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"})
	if err != nil || rollingBack.ActivePackageDigest != packageV2.Package.Digest || rollingBack.StagedPackageDigest != packageV1.Package.Digest || rollingBack.CurrentLifecycle != "rolling_back" {
		t.Fatalf("staged rollback = %+v, %v", rollingBack, err)
	}
	rollbackFailed, err := service.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", false, nil))
	if err != nil || rollbackFailed.ActivePackageDigest != packageV2.Package.Digest {
		t.Fatalf("failed rollback changed active package: %+v, %v", rollbackFailed, err)
	}
	if _, err := service.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := service.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil || rolledBack.ActivePackageDigest != packageV1.Package.Digest || rolledBack.RollbackPackageDigest != packageV2.Package.Digest {
		t.Fatalf("rollback completion = %+v, %v", rolledBack, err)
	}
	if _, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: packageV2, ActorID: "admin"}); !errors.Is(err, ErrPluginPermissionConfirmation) {
		t.Fatalf("historical retained grants suppressed current-digest permission confirmation: %v", err)
	}

	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: false}); err != nil {
		t.Fatalf("uninstall trusted a client drained flag instead of durable disabled state: %v", err)
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID); err != nil || ok {
		t.Fatalf("plugin remains installed: %v %v", ok, err)
	}
	if retained, ok, err := store.GetPluginInstance(ctx, "instance-1"); err != nil || !ok || retained.ConfigVersion != 1 {
		t.Fatalf("retain cleanup lost instance: %+v, %v, %v", retained, ok, err)
	}
	if retried, err := service.ResolveAgentPluginArtifact(ctx, "local", issuedRevision, snapshotDigest, artifactID); err != nil || !strings.EqualFold(retried.SHA256, artifact.SHA256) {
		t.Fatalf("revision artifact retry after lifecycle mutation = %+v, %v", retried, err)
	}
	if grants, err := store.ListPluginGrants(ctx, installed.PluginID); err != nil || len(grants) == 0 {
		t.Fatalf("retain cleanup lost grants: %+v, %v", grants, err)
	}
	operations, err = store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil || len(operations) < 10 {
		t.Fatalf("uninstall removed append-only operations: %d, %v", len(operations), err)
	}
	packageKinds := map[string]bool{"enable": false, "configure": false, "disable": false, "uninstall": false}
	for _, operation := range operations {
		if operation.TargetPackageDigest == "" {
			continue
		}
		trust := marketplace.SignatureTrust{SourceID: operation.SourceID, SourceKind: operation.SourceKind, KeyID: operation.TargetSignatureKeyID, PublicKey: operation.TargetSignaturePublicKey, Fingerprint: operation.TargetSignatureFingerprint}
		if err := marketplace.ValidateSignatureTrust(trust); err != nil || operation.TargetPackageIdentity != storage.PluginPackageIdentity(operation.TargetPackageDigest, operation.SourceID, operation.TargetSignatureFingerprint) {
			t.Fatalf("operation %s persisted incomplete package provenance: %+v, %v", operation.ID, operation, err)
		}
		if _, ok := packageKinds[operation.Kind]; ok {
			packageKinds[operation.Kind] = true
		}
	}
	for kind, found := range packageKinds {
		if !found {
			t.Fatalf("package-targeting %s operation was not persisted", kind)
		}
	}
	projectedOperations, err := service.Operations(ctx, installed.PluginID)
	if err != nil {
		t.Fatalf("read operations after lifecycle completions: %v", err)
	}
	for _, operation := range projectedOperations {
		var object map[string]any
		if err := json.Unmarshal(operation.AgentResults, &object); err != nil || object == nil {
			t.Fatalf("operation %s result is not an object: %s, %v", operation.ID, operation.AgentResults, err)
		}
	}
	if reinstalled, err := service.Install(ctx, PluginInstallRequest{Package: packageV1, ActorID: "admin-2", ConfirmedPermissions: []string{"http.inspect"}}); err != nil || reinstalled.ActivePackageDigest != packageV1.Package.Digest {
		t.Fatalf("reinstall with retained grants = %+v, %v", reinstalled, err)
	}
}

func TestPackageCacheIsRevalidatedBeforeUpgradeAndRollbackPromotion(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.integrity", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	v2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v2, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	v2SchemaPath := filepath.Join(v2.CachePath, plugins.ConfigSchemaFile)
	v2Schema, _ := os.ReadFile(v2SchemaPath)
	if err := os.WriteFile(v2SchemaPath, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err == nil {
		t.Fatal("tampered staged upgrade cache was promoted")
	}
	current, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != v1.Package.Digest || current.StagedPackageDigest != "" || current.PendingOperationID != "" {
		t.Fatalf("failed integrity check changed upgrade state: %+v", current)
	}
	if err := os.WriteFile(v2SchemaPath, v2Schema, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v2, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	v1SchemaPath := filepath.Join(v1.CachePath, plugins.ConfigSchemaFile)
	v1Schema, _ := os.ReadFile(v1SchemaPath)
	if err := os.WriteFile(v1SchemaPath, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err == nil {
		t.Fatal("tampered rollback cache was promoted")
	}
	current, _, _ = store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != v2.Package.Digest || current.StagedPackageDigest != "" || current.PendingOperationID != "" {
		t.Fatalf("failed rollback integrity check changed state: %+v", current)
	}
	_ = os.WriteFile(v1SchemaPath, v1Schema, 0o600)
}

type corruptActivePackageProjectionStore struct {
	pluginLifecycleStore
	digest string
}

type corruptInstalledCleanupStore struct {
	pluginLifecycleStore
}

func (s *corruptInstalledCleanupStore) GetInstalledPlugin(ctx context.Context, pluginID string) (storage.InstalledPluginRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetInstalledPlugin(ctx, pluginID)
	if err == nil && ok {
		row.CleanupPolicyJSON = `{"instances":"delete","config":"delete","owned_data":"delete","grants":"delete","shared_refs":"retain","audit_events":"retain"}`
	}
	return row, ok, err
}

type corruptPluginReadStore struct {
	pluginLifecycleStore
	mutateInstance  func(*storage.PluginInstanceRow)
	mutateOperation func(*storage.PluginOperationRow)
}

type failAfterPluginMutationStore struct {
	pluginLifecycleStore
	committed bool
}

func (s *failAfterPluginMutationStore) LocalAgentBuild(ctx context.Context) (string, string, bool, error) {
	if s.committed {
		return "", "", false, errors.New("injected later local build failure")
	}
	return s.pluginLifecycleStore.LocalAgentBuild(ctx)
}

func (s *failAfterPluginMutationStore) ApplyPluginMutation(ctx context.Context, mutation storage.PluginMutation) error {
	err := s.pluginLifecycleStore.ApplyPluginMutation(ctx, mutation)
	if err == nil {
		s.committed = true
	}
	return err
}

func (s *corruptPluginReadStore) ListPluginInstances(ctx context.Context, pluginID string) ([]storage.PluginInstanceRow, error) {
	rows, err := s.pluginLifecycleStore.ListPluginInstances(ctx, pluginID)
	if err == nil && len(rows) > 0 && s.mutateInstance != nil {
		s.mutateInstance(&rows[0])
	}
	return rows, err
}

func (s *corruptPluginReadStore) GetPluginInstance(ctx context.Context, instanceID string) (storage.PluginInstanceRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginInstance(ctx, instanceID)
	if err == nil && ok && s.mutateInstance != nil {
		s.mutateInstance(&row)
	}
	return row, ok, err
}

func (s *corruptPluginReadStore) ListPluginOperations(ctx context.Context, pluginID string) ([]storage.PluginOperationRow, error) {
	rows, err := s.pluginLifecycleStore.ListPluginOperations(ctx, pluginID)
	if err == nil && len(rows) > 0 && s.mutateOperation != nil {
		s.mutateOperation(&rows[0])
	}
	return rows, err
}

type configSchemaOverrideStore struct {
	pluginLifecycleStore
	schema string
}

func (s *configSchemaOverrideStore) GetPluginPackage(ctx context.Context, digest string) (storage.PluginPackageRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginPackage(ctx, digest)
	if err == nil && ok {
		row.ConfigSchemaJSON = s.schema
	}
	return row, ok, err
}

func (s *configSchemaOverrideStore) GetPluginPackageByIdentity(ctx context.Context, identity string) (storage.PluginPackageRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginPackageByIdentity(ctx, identity)
	if err == nil && ok {
		row.ConfigSchemaJSON = s.schema
	}
	return row, ok, err
}

func (s *corruptActivePackageProjectionStore) GetPluginPackage(ctx context.Context, digest string) (storage.PluginPackageRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginPackage(ctx, digest)
	if err == nil && ok && strings.EqualFold(digest, s.digest) {
		row.ManifestJSON = `{}`
	}
	return row, ok, err
}

func (s *corruptActivePackageProjectionStore) GetPluginPackageByIdentity(ctx context.Context, identity string) (storage.PluginPackageRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginPackageByIdentity(ctx, identity)
	if err == nil && ok && strings.EqualFold(row.Digest, s.digest) {
		row.ManifestJSON = `{}`
	}
	return row, ok, err
}

func quotaStatusCurrent(statuses []authz.QuotaStatus, policyID string) int64 {
	for _, status := range statuses {
		if status.PolicyID == policyID {
			return status.Current
		}
	}
	return -1
}

func TestPluginUpgradeMigratesAllInstancesAtomicallyAndFailsClosed(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.migration", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"instance-a", "instance-b"} {
		instance, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: id, ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
			t.Fatal(err)
		}
	}
	v2 := pluginCustomCandidateFixture(t, installed.PluginID, "2.0.0", cleanup, `{"type":"object","properties":{"behavior":{"type":"string"}},"required":["behavior"],"additionalProperties":false}`, "migrations:\n  - from: 1.0.0\n    to: 2.0.0\n    file: migrations/1-to-2.json\n", map[string]string{"migrations/1-to-2.json": `{"operations":[{"op":"rename","from":"/mode","path":"/behavior"}]}`}, `*`, `*`)
	staged, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v2, ActorID: "admin"})
	if err != nil || staged.ActivePackageDigest != v1.Package.Digest {
		t.Fatalf("stage migration = %+v, %v", staged, err)
	}
	for _, id := range []string{"instance-a", "instance-b"} {
		instance, _, _ := store.GetPluginInstance(ctx, id)
		if instance.ConfigJSON != `{"mode":"observe"}` || instance.PendingConfigJSON != `{"behavior":"observe"}` {
			t.Fatalf("instance %s migration was not staged atomically: %+v", id, instance)
		}
	}
	if _, err := svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"instance-a", "instance-b"} {
		instance, _, _ := store.GetPluginInstance(ctx, id)
		if instance.ConfigJSON != `{"behavior":"observe"}` || instance.RollbackConfigJSON != `{"mode":"observe"}` {
			t.Fatalf("instance %s migration was not promoted: %+v", id, instance)
		}
	}
	missing := pluginCustomCandidateFixture(t, installed.PluginID, "3.0.0", cleanup, `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`, "", nil, `*`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: missing, ActorID: "admin"}); err == nil {
		t.Fatal("missing migration chain was accepted")
	}
	current, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != v2.Package.Digest || current.StagedPackageDigest != "" {
		t.Fatalf("failed migration changed package pointers: %+v", current)
	}
	invalidResult := pluginCustomCandidateFixture(t, installed.PluginID, "3.0.0", cleanup, `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`, "migrations:\n  - from: 2.0.0\n    to: 3.0.0\n    file: migrations/2-to-3.json\n", map[string]string{"migrations/2-to-3.json": `{"operations":[{"op":"set","path":"/count","value":"bad"}]}`}, `*`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: invalidResult, ActorID: "admin"}); err == nil {
		t.Fatal("migration result violating the new schema was accepted")
	}
	executionFailure := pluginCustomCandidateFixture(t, installed.PluginID, "3.0.1", cleanup, `{"type":"object","properties":{"required_value":{"type":"string"}},"required":["required_value"],"additionalProperties":false}`, "migrations:\n  - from: 2.0.0\n    to: 3.0.1\n    file: migrations/2-to-3.json\n", map[string]string{"migrations/2-to-3.json": `{"operations":[{"op":"remove","path":"/missing"}]}`}, `*`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: executionFailure, ActorID: "admin"}); err == nil {
		t.Fatal("migration execution failure was accepted")
	}
	incompatible := pluginCustomCandidateFixture(t, installed.PluginID, "2.1.0", cleanup, `{"type":"object"}`, "", nil, `>=9.0.0`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: incompatible, ActorID: "admin"}); err == nil {
		t.Fatal("runtime-incompatible upgrade was accepted")
	}
	current, _, _ = store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != v2.Package.Digest {
		t.Fatal("incompatible upgrade changed active package")
	}
}

func pendingApplyResult(t *testing.T, store *storage.GormStore, pluginID, instanceID string, applied bool, agentResults any) PluginApplyResult {
	t.Helper()
	installed, ok, err := store.GetInstalledPlugin(context.Background(), pluginID)
	if err != nil || !ok {
		t.Fatalf("load pending plugin: %v, %v", ok, err)
	}
	result := PluginApplyResult{PluginID: pluginID, InstanceID: instanceID, OperationID: installed.PendingOperationID, TargetRevision: installed.PendingRevision, TargetDigest: installed.PendingTargetDigest, ActorID: "admin", Applied: applied, AgentResults: agentResults}
	if instanceID != "" {
		instance, exists, err := store.GetPluginInstance(context.Background(), instanceID)
		if err != nil || !exists {
			t.Fatalf("load pending instance: %v, %v", exists, err)
		}
		result.ConfigVersion = instance.PendingVersion
	}
	return result
}

func seedPluginAgent(t *testing.T, ctx context.Context, store *storage.GormStore) {
	seedPluginAgentWithID(t, ctx, store, "local")
}

func seedPluginAgentWithID(t *testing.T, ctx context.Context, store *storage.GormStore, agentID string) {
	t.Helper()
	if _, err := store.GetResourceGroup(ctx, "default"); err != nil {
		if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: agentID, Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`, IsLocal: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLocalAgentBuild(ctx, "1.0.0", true); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: agentID + "-binding", ResourceKind: "agent", ResourceID: agentID, ResourceGroupID: "default", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func pluginCandidateFixture(t *testing.T, id, version string, permissionNames []string, cleanup plugins.CleanupPolicy) PluginPackageCandidate {
	return pluginCandidateFixtureAtRoot(t, pluginTestCacheRoot(t), id, version, permissionNames, cleanup)
}

func pluginCandidateFixtureAtRoot(t *testing.T, cacheRoot, id, version string, permissionNames []string, cleanup plugins.CleanupPolicy) PluginPackageCandidate {
	t.Helper()
	staging := t.TempDir()
	artifact := servicePolicyWASMFixture(t)
	artifactDigest := sha256.Sum256(artifact)
	permissionYAML := make([]string, 0, len(permissionNames))
	for _, permission := range permissionNames {
		permissionYAML = append(permissionYAML, "  - "+permission)
	}
	manifest := "schema_version: 1\nid: " + id + "\nversion: " + version + "\nname: Test\ncompatibility: {host: \"*\", agent: \"*\"}\nruntime: {kind: wasm-policy, abi: \"nre:policy/v1\", host_scope: agent, entry: artifacts/policy.wasm, policy_kind: waf}\nartifacts:\n  - {path: artifacts/policy.wasm, sha256: " + strings.ToLower(strings.TrimSpace(hexDigest(artifactDigest[:]))) + ", size: " + strconv.Itoa(len(artifact)) + ", mode: wasm}\nextension_points: [http.request]\npermissions:\n" + strings.Join(permissionYAML, "\n") + "\nconfig_schema: config.schema.json\nresource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 8, input_bytes: 65536, output_bytes: 4096}\nfailure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}\nsignature: {algorithm: ed25519, key_id: test-fixture, file: package.sig}\ncleanup:\n" +
		"  instances: " + cleanup.Instances + "\n  config: " + cleanup.Config + "\n  owned_data: " + cleanup.OwnedData + "\n  grants: " + cleanup.Grants + "\n  shared_refs: " + cleanup.SharedRefs + "\n  audit_events: " + cleanup.AuditEvents + "\n"
	writePluginCandidateFile(t, staging, plugins.PackageManifestFile, manifest)
	writePluginCandidateFile(t, staging, plugins.ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"required":["mode"],"additionalProperties":false}`)
	writePluginCandidateBytes(t, staging, "artifacts/policy.wasm", artifact)
	digest, err := plugins.ComputePackageDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, staging, plugins.PackageDigestFile, digest)
	writePluginCandidateFile(t, staging, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(pluginTestSigningKey(), []byte(digest))))
	validated, err := pluginTestValidator().ValidatePackage(staging, plugins.PackageExpectation{ID: id, Version: version, SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := pluginTestSignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	target, err := marketplace.SignerCachePath(cacheRoot, digest, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, target); err != nil {
		t.Fatal(err)
	}
	validated.Root = target
	return PluginPackageCandidate{Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...), SignatureTrust: trust, CachePath: target, sourceID: trust.SourceID, sourceKind: trust.SourceKind, sourceRiskLabel: marketplace.UntrustedRiskLabel}
}

func bindPluginCandidateToTestSource(t *testing.T, candidate PluginPackageCandidate, sourceID string, signingKey ed25519.PrivateKey) PluginPackageCandidate {
	t.Helper()
	source, err := marketplace.NewSignedCustomSource(sourceID, sourceID, "https://example.com/"+sourceID+".git", "main", "", 0, marketplace.SourceSigner{
		KeyID: candidate.Package.Manifest.Signature.KeyID, SecretRef: "vault-" + sourceID, PublicKey: base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	candidate.SignatureTrust = trust
	candidate.validator = plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "0.0.0-dev", TrustedSigners: map[string]ed25519.PublicKey{trust.KeyID: signingKey.Public().(ed25519.PublicKey)}, TrustedSignerPolicy: plugins.TrustedSignerPolicyExact})
	candidate.sourceID, candidate.sourceKind, candidate.sourceRiskLabel = source.ID, source.Kind, source.RiskLabel
	return candidate
}

func resignPluginCandidateForTestSource(t *testing.T, candidate PluginPackageCandidate, sourceID string, signingKey ed25519.PrivateKey) PluginPackageCandidate {
	t.Helper()
	candidate = bindPluginCandidateToTestSource(t, candidate, sourceID, signingKey)
	root, err := marketplace.SignerCachePath(pluginTestCacheRoot(t), candidate.Package.Digest, candidate.SignatureTrust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(candidate.CachePath, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(candidate.CachePath, sourcePath)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(root, relative)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		contents, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		return os.WriteFile(targetPath, contents, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, root, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(signingKey, []byte(candidate.Package.Digest))))
	candidate.CachePath = root
	validated, err := candidate.validator.ValidatePackage(root, plugins.PackageExpectation{ID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SHA256: candidate.Package.Digest, SignatureKeyID: candidate.Package.Manifest.Signature.KeyID})
	if err != nil {
		t.Fatal(err)
	}
	candidate.Package = validated
	candidate.Runtime = validated.Manifest.Runtime
	candidate.Artifacts = append([]plugins.Artifact(nil), validated.Manifest.Artifacts...)
	return candidate
}

func pluginCustomCandidateFixture(t *testing.T, id, version string, cleanup plugins.CleanupPolicy, schema, manifestExtra string, files map[string]string, hostCompatibility, agentCompatibility string) PluginPackageCandidate {
	t.Helper()
	staging := t.TempDir()
	artifact := servicePolicyWASMFixture(t)
	artifactDigest := sha256.Sum256(artifact)
	manifest := "schema_version: 1\nid: " + id + "\nversion: " + version + "\nname: Test\ncompatibility: {host: \"" + hostCompatibility + "\", agent: \"" + agentCompatibility + "\"}\nruntime: {kind: wasm-policy, abi: \"nre:policy/v1\", host_scope: agent, entry: artifacts/policy.wasm, policy_kind: waf}\nartifacts:\n  - {path: artifacts/policy.wasm, sha256: " + hexDigest(artifactDigest[:]) + ", size: " + strconv.Itoa(len(artifact)) + ", mode: wasm}\nextension_points: [http.request]\npermissions: [http.inspect]\nconfig_schema: config.schema.json\nresource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 8, input_bytes: 65536, output_bytes: 4096}\nfailure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}\nsignature: {algorithm: ed25519, key_id: test-fixture, file: package.sig}\ncleanup:\n" +
		"  instances: " + cleanup.Instances + "\n  config: " + cleanup.Config + "\n  owned_data: " + cleanup.OwnedData + "\n  grants: " + cleanup.Grants + "\n  shared_refs: " + cleanup.SharedRefs + "\n  audit_events: " + cleanup.AuditEvents + "\n" + manifestExtra
	writePluginCandidateFile(t, staging, plugins.PackageManifestFile, manifest)
	writePluginCandidateFile(t, staging, plugins.ConfigSchemaFile, schema)
	writePluginCandidateBytes(t, staging, "artifacts/policy.wasm", artifact)
	for name, value := range files {
		writePluginCandidateFile(t, staging, name, value)
	}
	digest, err := plugins.ComputePackageDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, staging, plugins.PackageDigestFile, digest)
	writePluginCandidateFile(t, staging, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(pluginTestSigningKey(), []byte(digest))))
	validated, err := pluginTestValidator().ValidatePackage(staging, plugins.PackageExpectation{ID: id, Version: version, SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := pluginTestSignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	target, err := marketplace.SignerCachePath(pluginTestCacheRoot(t), digest, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, target); err != nil {
		t.Fatal(err)
	}
	validated.Root = target
	return PluginPackageCandidate{Package: validated, Runtime: validated.Manifest.Runtime, Artifacts: append([]plugins.Artifact(nil), validated.Manifest.Artifacts...), SignatureTrust: trust, CachePath: target, sourceID: trust.SourceID, sourceKind: trust.SourceKind, sourceRiskLabel: marketplace.UntrustedRiskLabel}
}

func pluginInvalidMigrationGraphCandidateFixture(t *testing.T, candidate PluginPackageCandidate, migrations []plugins.Migration) PluginPackageCandidate {
	t.Helper()
	manifestData, err := os.ReadFile(filepath.Join(candidate.CachePath, plugins.PackageManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var graph strings.Builder
	graph.WriteString("\nmigrations:\n")
	for _, migration := range migrations {
		graph.WriteString("  - from: ")
		graph.WriteString(migration.From)
		graph.WriteString("\n    to: ")
		graph.WriteString(migration.To)
		graph.WriteString("\n    file: ")
		graph.WriteString(migration.File)
		graph.WriteByte('\n')
		writePluginCandidateFile(t, candidate.CachePath, migration.File, `{"operations":[{"op":"set","path":"/mode","value":"observe"}]}`)
	}
	writePluginCandidateFile(t, candidate.CachePath, plugins.PackageManifestFile, string(manifestData)+graph.String())
	digest, err := plugins.ComputePackageDigest(candidate.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, candidate.CachePath, plugins.PackageDigestFile, digest)
	writePluginCandidateFile(t, candidate.CachePath, plugins.PackageSignatureFile, base64.StdEncoding.EncodeToString(ed25519.Sign(pluginTestSigningKey(), []byte(digest))))
	target := filepath.Join(filepath.Dir(candidate.CachePath), digest)
	if err := os.Rename(candidate.CachePath, target); err != nil {
		t.Fatal(err)
	}
	candidate.Package.Digest = digest
	candidate.Package.Root = target
	candidate.Package.Manifest.Migrations = append([]plugins.Migration(nil), migrations...)
	candidate.CachePath = target
	return candidate
}

func pluginTestSignatureTrust() (marketplace.SignatureTrust, error) {
	source, err := marketplace.NewSignedCustomSource(pluginTestSourceID, "Test Fixture", "https://example.com/test-fixture.git", "main", "", 0, marketplace.SourceSigner{KeyID: "test-fixture", SecretRef: "vault-test-fixture", PublicKey: base64.StdEncoding.EncodeToString(pluginTestSigningKey().Public().(ed25519.PublicKey))})
	if err != nil {
		return marketplace.SignatureTrust{}, err
	}
	return source.SignatureTrust()
}

func writePluginCandidateFile(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePluginCandidateBytes(t *testing.T, root, name string, value []byte) {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pluginTestSigningKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("nre-service-plugin-test-fixture"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func servicePolicyWASMFixture(t *testing.T) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "plugin-sdk", "policy", "v1", "testdata", "compatible_guest.wasm.hex"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func pluginTestValidator() *plugins.Validator {
	return plugins.NewValidator(plugins.ValidatorOptions{TrustedSigners: map[string]ed25519.PublicKey{"test-fixture": pluginTestSigningKey().Public().(ed25519.PublicKey)}})
}

var pluginTestCacheRoots sync.Map

const pluginTestSourceID = "test-fixture-source"

func pluginTestCacheRoot(t *testing.T) string {
	t.Helper()
	if cached, ok := pluginTestCacheRoots.Load(t); ok {
		return cached.(string)
	}
	root := filepath.Join(t.TempDir(), "plugins", "packages")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	actual, loaded := pluginTestCacheRoots.LoadOrStore(t, root)
	if !loaded {
		t.Cleanup(func() { pluginTestCacheRoots.Delete(t) })
	}
	return actual.(string)
}

type pluginFixtureService struct {
	*PluginService
}

func (s *pluginFixtureService) Install(ctx context.Context, request PluginInstallRequest) (storage.InstalledPluginRow, error) {
	acceptPluginTestFixtureRisk(&request.Package, &request.RiskAccepted)
	return s.PluginService.Install(ctx, request)
}

func (s *pluginFixtureService) Upgrade(ctx context.Context, request PluginUpgradeRequest) (storage.InstalledPluginRow, error) {
	acceptPluginTestFixtureRisk(&request.Package, &request.RiskAccepted)
	return s.PluginService.Upgrade(ctx, request)
}

func acceptPluginTestFixtureRisk(candidate *PluginPackageCandidate, accepted *bool) {
	if candidate.sourceID == pluginTestSourceID && candidate.sourceID == candidate.SignatureTrust.SourceID && candidate.sourceKind == candidate.SignatureTrust.SourceKind {
		*accepted = true
	}
}

func newPluginTestService(t *testing.T, store pluginLifecycleStore) *pluginFixtureService {
	t.Helper()
	return newPluginTestServiceAtRoot(t, store, pluginTestCacheRoot(t))
}

func newPluginTestServiceAtRoot(t *testing.T, store pluginLifecycleStore, cacheRoot string) *pluginFixtureService {
	t.Helper()
	return &pluginFixtureService{PluginService: NewPluginServiceWithValidator(store, plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "0.0.0-dev", TrustedSigners: map[string]ed25519.PublicKey{"test-fixture": pluginTestSigningKey().Public().(ed25519.PublicKey)}}), cacheRoot)}
}

func hexDigest(value []byte) string {
	const alphabet = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = alphabet[item>>4]
		encoded[index*2+1] = alphabet[item&0x0f]
	}
	return string(encoded)
}
