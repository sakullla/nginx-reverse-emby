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

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestPluginWriteOnlySecretInstallConfigureDetailAndRestart(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test-secret")
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
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
	instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "secret-instance", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe","token":null}`), SecretReplacements: map[string]json.RawMessage{"/token": json.RawMessage(`"install-plaintext"`)}, ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(instance.PendingConfigJSON, "install-plaintext") || strings.Contains(instance.PendingSecretHandlesJSON, "install-plaintext") || instance.PendingSecretHandlesJSON == "[]" {
		t.Fatalf("plaintext persisted in staged instance: %+v", instance)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"local": "active"}))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.Detail(ctx, installed.PluginID)
	if err != nil || len(detail.Instances) != 1 || strings.Contains(string(detail.Instances[0].Config), "install-plaintext") || len(detail.Instances[0].SecretFields) != 1 || !detail.Instances[0].SecretFields[0].Present {
		t.Fatalf("detail=%+v error=%v", detail, err)
	}
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
	preserved, err := restarted.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"block","token":null}`), ActorID: "admin"})
	if err != nil || strings.Contains(preserved.PendingConfigJSON, "install-plaintext") || preserved.PendingSecretHandlesJSON != instance.SecretHandlesJSON {
		t.Fatalf("preserved=%+v error=%v", preserved, err)
	}
}

func TestPluginLifecycleStagesDesiredStateAndPreservesActiveOnFailure(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
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

func TestPluginInstallRejectsNonCanonicalScalarPermissionAndKeepsFailureProvenance(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := newPluginTestService(t, store)
	retain := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "noncanonical.install", "1.0.0", []string{"http.inspect"}, retain)
	manifestPath := filepath.Join(candidate.CachePath, plugins.PackageManifestFile)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(string(manifest), "  - http.inspect", "  - ' http.inspect '", 1))
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := plugins.ComputePackageDigest(candidate.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.CachePath, plugins.PackageDigestFile), []byte(digest), 0o600); err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(pluginTestSigningKey(), []byte(digest))
	if err := os.WriteFile(filepath.Join(candidate.CachePath, plugins.PackageSignatureFile), []byte(base64.StdEncoding.EncodeToString(signature)), 0o600); err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.SignerCachePath(pluginTestCacheRoot(t), digest, candidate.SignatureTrust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(candidate.CachePath, cachePath); err != nil {
		t.Fatal(err)
	}
	candidate.CachePath = cachePath
	candidate.Package.Root = cachePath
	candidate.Package.Digest = digest
	candidate.Package.Manifest.Permissions[0].Name = " http.inspect "

	if _, err := service.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}}); err == nil || !strings.Contains(err.Error(), "canonical whitespace") {
		t.Fatalf("install accepted or misclassified a non-canonical signed scalar permission: %v", err)
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, candidate.Package.Manifest.ID); err != nil || ok {
		t.Fatalf("failed install persisted plugin: ok=%v err=%v", ok, err)
	}
	operations, err := store.ListPluginOperations(ctx, candidate.Package.Manifest.ID)
	if err != nil || len(operations) != 1 {
		t.Fatalf("failed install operations = %+v, %v", operations, err)
	}
	operation := operations[0]
	if operation.Status != "failed" || operation.TargetPackageIdentity == "" || operation.SourceID != candidate.SignatureTrust.SourceID || operation.TargetSignatureKeyID != candidate.SignatureTrust.KeyID || operation.TargetSignaturePublicKey != candidate.SignatureTrust.PublicKey || operation.TargetSignatureFingerprint != candidate.SignatureTrust.Fingerprint {
		t.Fatalf("failed install lost package provenance: %+v", operation)
	}
}

func TestPluginLifecycleRejectsDigestNamedPackageOutsideConfiguredCacheRoot(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	candidate := pluginCandidateFixture(t, "outside.cache", "1.0.0", []string{"http.inspect"}, plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"})
	outside := filepath.Join(t.TempDir(), candidate.Package.Digest)
	if err := os.Rename(candidate.CachePath, outside); err != nil {
		t.Fatal(err)
	}
	candidate.CachePath = outside
	candidate.Package.Root = outside
	if _, err := newPluginTestService(t, store).Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}}); err == nil || !strings.Contains(err.Error(), "outside the managed root") {
		t.Fatalf("outside-root lifecycle package error = %v", err)
	}
}

func TestPluginLifecycleKeepsSameDigestSignerVariantsAcrossRestartAndRollback(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	seedPluginAgent(t, ctx, store)
	service := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	first := pluginCandidateFixture(t, "variant.lifecycle", "1.0.0", []string{"http.inspect"}, cleanup)
	first = bindPluginCandidateToTestSource(t, first, "variant-source-a", pluginTestSigningKey())
	secondSeed := sha256.Sum256([]byte("nre-service-plugin-second-signer"))
	second := resignPluginCandidateForTestSource(t, first, "variant-source-b", ed25519.NewKeyFromSeed(secondSeed[:]))
	if first.Package.Digest != second.Package.Digest || first.SignatureTrust.KeyID != second.SignatureTrust.KeyID || first.SignatureTrust.Fingerprint == second.SignatureTrust.Fingerprint {
		t.Fatalf("same-content signer fixture is invalid: first=%+v second=%+v", first.SignatureTrust, second.SignatureTrust)
	}

	installed, err := service.Install(ctx, PluginInstallRequest{Package: first, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity := installed.ActivePackageIdentity
	staged, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: second, ActorID: "admin", RiskAccepted: true})
	if err != nil {
		t.Fatal(err)
	}
	if staged.StagedPackageDigest != installed.ActivePackageDigest || staged.StagedPackageIdentity == "" || staged.StagedPackageIdentity == firstIdentity || staged.PendingTargetIdentity != staged.StagedPackageIdentity {
		t.Fatalf("same-digest signer upgrade did not stage an isolated variant: %+v", staged)
	}
	secondIdentity := staged.StagedPackageIdentity
	upgraded, err := service.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.ActivePackageDigest != installed.ActivePackageDigest || upgraded.ActivePackageIdentity != secondIdentity || upgraded.RollbackPackageIdentity != firstIdentity {
		t.Fatalf("same-digest signer promotion lost variant references: %+v", upgraded)
	}
	for _, identity := range []string{firstIdentity, secondIdentity} {
		if _, ok, err := store.GetPluginPackageByIdentity(ctx, identity); err != nil || !ok {
			t.Fatalf("stored package variant %s = %v, %v", identity, ok, err)
		}
		if artifacts, err := store.ListPluginArtifactsByIdentity(ctx, identity); err != nil || len(artifacts) != 1 || artifacts[0].PackageIdentity != identity {
			t.Fatalf("stored artifact variant %s = %+v, %v", identity, artifacts, err)
		}
	}
	legacy := upgraded
	legacy.ActivePackageIdentity, legacy.RollbackPackageIdentity = "", ""
	legacy.LastOperationID = "legacy-variant-backfill"
	legacy.UpdatedAt = time.Now().UTC()
	if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{
		PluginID: legacy.PluginID, ExpectedActive: upgraded.ActivePackageDigest, ExpectedStateVersion: upgraded.StateVersion, Installed: &legacy,
		Operation: storage.PluginOperationRow{ID: legacy.LastOperationID, PluginID: legacy.PluginID, Kind: "legacy_backfill_fixture", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: legacy.UpdatedAt, CompletedAt: &legacy.UpdatedAt},
		Audit:     storage.AuditEventRow{ID: "legacy-variant-backfill-audit", ActorID: "admin", Action: "plugin.legacy_backfill_fixture", TargetKind: "plugin", TargetID: legacy.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: legacy.UpdatedAt},
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service = newPluginTestService(t, store)
	restarted, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok || restarted.ActivePackageIdentity != secondIdentity || restarted.RollbackPackageIdentity != firstIdentity {
		t.Fatalf("restart source-aware variant backfill = %+v, %v, %v", restarted, ok, err)
	}
	detail, err := service.Detail(ctx, installed.PluginID)
	if err != nil || detail.Plugin.ActiveSourceID != "variant-source-b" || detail.Package.Digest != installed.ActivePackageDigest {
		t.Fatalf("restarted active signer variant = %+v, %v", detail, err)
	}
	rolledBack, err := service.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err = service.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.ActivePackageIdentity != firstIdentity || rolledBack.RollbackPackageIdentity != secondIdentity || rolledBack.ActiveSourceID != "variant-source-a" {
		t.Fatalf("restarted rollback selected the wrong signer variant: %+v", rolledBack)
	}
}

func TestPackageLifecycleMissingExactVariantKeepsAuditSigner(t *testing.T) {
	for _, kind := range []string{"enable", "disable", "configure", "uninstall", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			ctx := WithSystemMutationPrincipal(context.Background(), "test")
			dataRoot := t.TempDir()
			store, err := storage.NewSQLiteStore(dataRoot, "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			seedPluginAgent(t, ctx, store)
			service := newPluginTestService(t, store)
			cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
			first := bindPluginCandidateToTestSource(t, pluginCandidateFixture(t, "missing.variant."+kind, "1.0.0", []string{"http.inspect"}, cleanup), "missing-source-a", pluginTestSigningKey())
			secondSeed := sha256.Sum256([]byte("missing-variant-sibling-" + kind))
			second := resignPluginCandidateForTestSource(t, first, "missing-source-b", ed25519.NewKeyFromSeed(secondSeed[:]))
			installed, err := service.Install(ctx, PluginInstallRequest{Package: first, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: second, ActorID: "admin", RiskAccepted: true}); err != nil {
				t.Fatal(err)
			}
			promoteSibling := kind == "rollback"
			installed, err = service.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", promoteSibling, nil))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "disable" {
				installed, err = service.Enable(ctx, installed.PluginID, "admin")
				if err != nil {
					t.Fatal(err)
				}
				installed, err = service.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
				if err != nil {
					t.Fatal(err)
				}
			}
			missingIdentity := installed.ActivePackageIdentity
			missingTrust := first.SignatureTrust
			if promoteSibling {
				missingIdentity = installed.RollbackPackageIdentity
			}
			db, err := gorm.Open(sqlite.Open(filepath.Join(dataRoot, "panel.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			if result := db.Where("identity = ?", missingIdentity).Delete(&storage.PluginPackageRow{}); result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("delete exact package row = %d, %v", result.RowsAffected, result.Error)
			}
			switch kind {
			case "enable":
				_, err = service.Enable(ctx, installed.PluginID, "admin")
			case "disable":
				installed, err = service.Disable(ctx, installed.PluginID, "admin")
			case "configure":
				_, err = service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "missing-instance", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
			case "uninstall":
				err = service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true})
			case "rollback":
				_, err = service.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"})
			}
			if kind == "disable" {
				if err != nil || installed.CurrentLifecycle != "applying" || installed.DesiredLifecycle != "disabled" {
					t.Fatalf("missing exact disable result = %+v, %v", installed, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "unavailable") {
				t.Fatalf("missing exact %s package error = %v", kind, err)
			}
			operations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			var persisted *storage.PluginOperationRow
			for index := range operations {
				if operations[index].Kind == kind && (operations[index].Status == "failed" || kind == "disable" && operations[index].Status == "applying") {
					persisted = &operations[index]
				}
			}
			if persisted == nil || persisted.TargetPackageIdentity != missingIdentity || persisted.SourceID != missingTrust.SourceID || persisted.TargetSignatureFingerprint != missingTrust.Fingerprint || persisted.TargetSignaturePublicKey != missingTrust.PublicKey {
				t.Fatalf("missing exact %s operation rebound to sibling: %+v", kind, persisted)
			}
			if kind == "disable" {
				audits, err := store.ListAuditEvents(ctx, 100)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, audit := range audits {
					if audit.Action == "plugin.disable" && audit.Result == "accepted" && strings.Contains(audit.MetadataJSON, `"operation_id":"`+persisted.ID+`"`) && strings.Contains(audit.MetadataJSON, `"source_id":"`+missingTrust.SourceID+`"`) {
						found = true
					}
				}
				if !found {
					t.Fatal("missing exact disable did not persist its bound audit")
				}
			}
		})
	}
}

func TestMigratedLegacyPackageProjectionIsLifecycleConsumable(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := storage.NewSQLiteStore(sourceRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := storage.NewSQLiteStore(targetRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetRoot, "plugins", "packages")) })
	seedPluginAgent(t, ctx, source)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "migrated.lifecycle", "1.0.0", []string{"http.inspect"}, cleanup)
	candidate = bindPluginCandidateToTestSource(t, candidate, "migration-source", pluginTestSigningKey())
	sourceCache := filepath.Join(sourceRoot, "plugins", "packages", candidate.Package.Digest)
	if err := os.MkdirAll(filepath.Dir(sourceCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(candidate.CachePath, sourceCache); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	legacyPackage := storage.PluginPackageRow{
		Digest: candidate.Package.Digest, PluginID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version,
		SourceID: candidate.SignatureTrust.SourceID, SourceKind: candidate.SignatureTrust.SourceKind, SourceRiskLabel: marketplace.UntrustedRiskLabel,
		SignatureKeyID: candidate.SignatureTrust.KeyID, SignaturePublicKey: candidate.SignatureTrust.PublicKey, SignatureFingerprint: candidate.SignatureTrust.Fingerprint,
		CachePath: sourceCache, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now,
	}
	installed := storage.InstalledPluginRow{
		PluginID: candidate.Package.Manifest.ID, ActivePackageDigest: candidate.Package.Digest,
		ActiveSourceID: candidate.sourceID, ActiveSourceKind: candidate.sourceKind, ActiveSourceRiskLabel: candidate.sourceRiskLabel,
		DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "legacy-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	legacyDB, err := gorm.Open(sqlite.Open(filepath.Join(sourceRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	legacySQLDB, err := legacyDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacySQLDB.Close() })
	legacyRows := []any{
		&legacyPackage,
		&installed,
		&storage.PluginGrantRow{ID: "legacy-grant", PluginID: installed.PluginID, PackageDigest: installed.ActivePackageDigest, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: now},
		&storage.PluginOperationRow{ID: "legacy-install", PluginID: installed.PluginID, Kind: "install", Status: "succeeded", TargetPackageDigest: installed.ActivePackageDigest, AgentResultsJSON: `{}`, ActorID: "admin", SourceID: candidate.sourceID, SourceKind: candidate.sourceKind, SourceRiskLabel: candidate.sourceRiskLabel, CreatedAt: now, CompletedAt: &now},
		&storage.AuditEventRow{ID: "legacy-install-audit", ActorID: "admin", Action: "plugin.install", TargetKind: "plugin", TargetID: installed.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
	}
	for _, row := range legacyRows {
		if err := legacyDB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	migrated, ok, err := target.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok || migrated.ActivePackageIdentity != storage.PluginPackageIdentity(candidate.Package.Digest, candidate.SignatureTrust.SourceID, candidate.SignatureTrust.Fingerprint) {
		t.Fatalf("migrated package identity = %+v, %v, %v", migrated, ok, err)
	}
	service := newPluginTestServiceAtRoot(t, target, filepath.Join(targetRoot, "plugins", "packages"))
	detail, err := service.Detail(ctx, installed.PluginID)
	if err != nil || detail.Package.Runtime.Kind != candidate.Package.Manifest.Runtime.Kind || len(detail.Package.Artifacts) != 1 {
		t.Fatalf("migrated package detail = %+v, %v", detail, err)
	}
	if enabled, err := service.Enable(ctx, installed.PluginID, "admin"); err != nil || enabled.CurrentLifecycle != "applying" {
		t.Fatalf("migrated package lifecycle enable = %+v, %v", enabled, err)
	}
}

func TestPluginProvenanceIsPerLifecycleAssociationAndRollbackSurvivesSourceDeletion(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	customV1 := pluginCandidateFixture(t, "source.association", "1.0.0", []string{"http.inspect"}, cleanup)
	customV1.sourceID, customV1.sourceKind, customV1.sourceRiskLabel = "community", marketplace.SourceKindCustom, marketplace.UntrustedRiskLabel
	customSource, _ := marketplaceTestSource("community")
	customV1.SignatureTrust, err = customSource.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, customSource); err != nil {
		t.Fatal(err)
	}
	svc := newPluginTestService(t, store)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: customV1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil || installed.ActiveSourceKind != marketplace.SourceKindCustom {
		t.Fatalf("custom install provenance = %+v, %v", installed, err)
	}
	fixtureV2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: fixtureV2, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	installed, err = svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil || installed.ActiveSourceID != fixtureV2.SignatureTrust.SourceID || installed.ActiveSourceKind != fixtureV2.SignatureTrust.SourceKind || installed.RollbackSourceID != customSource.ID || installed.RollbackSourceKind != marketplace.SourceKindCustom {
		t.Fatalf("upgrade provenance swap = %+v, %v", installed, err)
	}
	if _, err := store.DeleteMarketplaceSource(ctx, customSource.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	svc = newPluginTestService(t, store)
	if _, err := svc.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	var rollback storage.PluginOperationRow
	for _, operation := range operations {
		if operation.Kind == "rollback" {
			rollback = operation
		}
	}
	if rollback.SourceID != customSource.ID || rollback.SourceKind != marketplace.SourceKindCustom || rollback.SourceRiskLabel != marketplace.UntrustedRiskLabel {
		t.Fatalf("rollback lost deleted-source provenance: %+v", rollback)
	}
	var auditFound bool
	audits, _ := store.ListAuditEvents(ctx, 100)
	for _, audit := range audits {
		if audit.Action == "plugin.rollback" && strings.Contains(audit.MetadataJSON, `"source_kind":"custom"`) {
			auditFound = true
		}
	}
	if !auditFound {
		t.Fatal("rollback audit lost custom source risk provenance")
	}

	// The package cache identity is digest-only, but a later lifecycle
	// association must still reflect its own source in either acquisition order.
	same := pluginCandidateFixture(t, "same.digest", "1.0.0", []string{"http.inspect"}, cleanup)
	first, err := svc.Install(ctx, PluginInstallRequest{Package: same, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil || first.ActiveSourceID != same.SignatureTrust.SourceID || first.ActiveSourceKind != same.SignatureTrust.SourceKind {
		t.Fatalf("fixture same-digest install = %+v, %v", first, err)
	}
	if err := svc.Uninstall(ctx, PluginUninstallRequest{PluginID: first.PluginID, ActorID: "admin", Drained: true}); err != nil {
		t.Fatal(err)
	}
	same.sourceID, same.sourceKind, same.sourceRiskLabel = "community", marketplace.SourceKindCustom, marketplace.UntrustedRiskLabel
	second, err := svc.Install(ctx, PluginInstallRequest{Package: same, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil || second.ActiveSourceKind != marketplace.SourceKindCustom {
		t.Fatalf("custom same-digest reinstall inherited cache provenance: %+v, %v", second, err)
	}
}

func TestConfigureEnforcesApplicationQuotaAtomically(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "quota-admin")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "quota.plugin", "1.0.0", nil, cleanup)
	if _, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "quota-admin"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := storage.QuotaPolicyRow{ID: "plugin-applications", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "application_count", Limit: 0, ExceedAction: "reject", CreatedAt: now, UpdatedAt: now}
	if err := store.UpsertQuotaPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	request := PluginConfigureRequest{PluginID: "quota.plugin", InstanceID: "quota-instance", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "quota-admin"}
	if _, err := svc.Configure(ctx, request); !errors.Is(err, storage.ErrQuotaExceeded) {
		t.Fatalf("quota rejection error = %v", err)
	}
	if _, ok, err := store.GetPluginInstance(ctx, request.InstanceID); err != nil || ok {
		t.Fatalf("quota-rejected instance persisted: %v, %v", ok, err)
	}
	if _, err := store.GetResourceBinding(ctx, "plugin_instance", request.InstanceID); err == nil {
		t.Fatal("quota-rejected instance binding persisted")
	}
	policy.Limit, policy.UpdatedAt = 1, now.Add(time.Second)
	if err := store.UpsertQuotaPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	configured, err := svc.Configure(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteConfigure(ctx, pendingApplyResult(t, store, candidate.Package.Manifest.ID, configured.ID, true, nil)); err != nil {
		t.Fatal(err)
	}
	second := request
	second.InstanceID = "quota-instance-two"
	if _, err := svc.Configure(ctx, second); !errors.Is(err, storage.ErrQuotaExceeded) {
		t.Fatalf("second application quota error = %v", err)
	}
}

func TestPluginLifecycleUsesTrustedOriginProvenanceAndRejectsIncompatibleTargets(t *testing.T) {
	ctx := storage.WithQuotaActor(context.Background(), storage.QuotaActor{UserID: "trusted-admin", SessionID: "trusted-session", CorrelationID: "trusted-request"})
	ctx = WithResourceAuthorizer(ctx, func(context.Context, string, string) error { return nil })
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Version: "1.5.0", CapabilitiesJSON: `["package_manifest_v1"]`, IsLocal: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLocalAgentBuild(ctx, "1.5.0", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-new", Version: "2.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-old", Version: "1.5.0", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "default", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCustomCandidateFixture(t, "official.targets", "1.0.0", cleanup, `{"type":"object","additionalProperties":false}`, "", nil, `*`, `>=1.0.0 <2.0.0`)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "forged-admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"edge-new", "edge-old", "missing"} {
		if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "bad-" + target, ResourceGroupID: "default", Targets: []string{target}, Config: json.RawMessage(`{}`), ActorID: "forged-admin"}); err == nil {
			t.Fatalf("incompatible or incapable target %s was accepted", target)
		}
		if _, ok, _ := store.GetPluginInstance(ctx, "bad-"+target); ok {
			t.Fatalf("failed target validation persisted instance %s", target)
		}
	}
	instance, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "compatible", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{}`), ActorID: "forged-admin"})
	if err != nil {
		t.Fatal(err)
	}
	result := pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)
	result.ActorID = "forged-reporter"
	if _, err := svc.CompleteConfigure(ctx, result); err != nil {
		t.Fatal(err)
	}
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if operation.ActorID != "trusted-admin" || operation.SessionID != "trusted-session" || operation.CorrelationID != "trusted-request" {
			t.Fatalf("operation lost trusted origin provenance: %+v", operation)
		}
	}
	audits, err := store.ListAuditEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.TargetID == installed.PluginID && (audit.ActorID != "trusted-admin" || audit.SessionID != "trusted-session" || audit.CorrelationID != "trusted-request") {
			t.Fatalf("lifecycle audit used forged or completion provenance: %+v", audit)
		}
	}
}

func TestPluginPersistedSchemaKeepsExactLargeNumericEnum(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCustomCandidateFixture(t, "official.numeric", "1.0.0", cleanup, `{"type":"object","properties":{"id":{"enum":[9007199254740993]}},"required":["id"],"additionalProperties":false}`, "", nil, `*`, `*`)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "adjacent", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"id":9007199254740992}`), ActorID: "admin"}); err == nil {
		t.Fatal("persisted schema rounded adjacent large enum value")
	}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "exact", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"id":9007199254740993}`), ActorID: "admin"}); err != nil {
		t.Fatalf("persisted schema rejected exact large enum: %v", err)
	}
}

func TestPluginRollbackRequiresExactPermissionIncreaseConfirmation(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.rollback-permissions", "1.0.0", []string{"http.inspect", "http.respond"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect", "http.respond"}})
	if err != nil {
		t.Fatal(err)
	}
	v2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v2, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin"}); !errors.Is(err, ErrPluginPermissionConfirmation) {
		t.Fatalf("rollback permission increase was not rejected: %v", err)
	}
	if _, err := svc.Rollback(ctx, PluginRollbackRequest{PluginID: installed.PluginID, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect", "http.respond"}}); err != nil {
		t.Fatalf("exactly confirmed rollback was rejected: %v", err)
	}
}

func TestHistoricalRetainedGrantCannotAuthorizeDifferentActiveDigest(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.history", "1.0.0", []string{"http.respond"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, ActorID: "admin", ConfirmedPermissions: []string{"http.respond"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); err != nil {
		t.Fatal(err)
	}
	v2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
	installed, err = svc.Install(ctx, PluginInstallRequest{Package: v2, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	v3 := pluginCandidateFixture(t, installed.PluginID, "3.0.0", []string{"http.inspect", "http.respond"}, cleanup)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v3, ActorID: "admin"}); !errors.Is(err, ErrPluginPermissionConfirmation) {
		t.Fatalf("historical retained grant bypassed active-digest confirmation: %v", err)
	}
}

func TestPackageCacheIsRevalidatedBeforeUpgradeAndRollbackPromotion(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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

func TestPluginUpgradeRevalidatesActivePackageBeforeStaging(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*testing.T, *storage.GormStore, PluginPackageCandidate) pluginLifecycleStore
	}{
		{
			name: "persisted manifest projection",
			corrupt: func(_ *testing.T, store *storage.GormStore, active PluginPackageCandidate) pluginLifecycleStore {
				return &corruptActivePackageProjectionStore{pluginLifecycleStore: store, digest: active.Package.Digest}
			},
		},
		{
			name: "verified cache contents",
			corrupt: func(t *testing.T, store *storage.GormStore, active PluginPackageCandidate) pluginLifecycleStore {
				t.Helper()
				if err := os.WriteFile(filepath.Join(active.CachePath, plugins.ConfigSchemaFile), []byte(`{"tampered":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return store
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := WithSystemMutationPrincipal(context.Background(), "test")
			store, err := storage.NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
			active := pluginCandidateFixture(t, "official.active-integrity", "1.0.0", []string{"http.inspect"}, cleanup)
			installed, err := newPluginTestService(t, store).Install(ctx, PluginInstallRequest{Package: active, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
			if err != nil {
				t.Fatal(err)
			}
			candidate := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
			service := newPluginTestService(t, test.corrupt(t, store, active))
			if _, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: candidate, ActorID: "admin"}); err == nil {
				t.Fatal("upgrade staged from a corrupt active package")
			}
			current, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
			if err != nil || !ok {
				t.Fatalf("load installed plugin = %v, %v", ok, err)
			}
			if current.ActivePackageDigest != active.Package.Digest || current.StagedPackageDigest != "" || current.PendingOperationID != "" {
				t.Fatalf("active integrity failure created staged state: %+v", current)
			}
		})
	}
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

func TestPluginDisableDoesNotDependOnActivePackageIntegrity(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.disable-recovery", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enable(ctx, installed.PluginID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.CachePath, plugins.ConfigSchemaFile), []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	disabled, err := svc.Disable(ctx, installed.PluginID, "admin")
	if err != nil {
		t.Fatalf("disable rejected corrupt active package: %v", err)
	}
	if disabled.DesiredLifecycle != "disabled" || disabled.PendingKind != "disable" || disabled.PendingOperationID == "" {
		t.Fatalf("disable did not create the recoverable CAS transition: %+v", disabled)
	}
}

func TestPluginLifecycleRechecksCurrentHostCompatibilityAfterRestart(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCustomCandidateFixture(t, "compatibility.restart", "1.0.0", cleanup, `{"type":"object"}`, "", nil, ">=1.0.0 <2.0.0", "*")
	trusted := map[string]ed25519.PublicKey{"test-fixture": pluginTestSigningKey().Public().(ed25519.PublicKey)}
	beforeUpgrade := NewPluginServiceWithValidator(store, plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "1.5.0", TrustedSigners: trusted}), pluginTestCacheRoot(t))
	installed, err := beforeUpgrade.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil {
		t.Fatal(err)
	}

	afterUpgrade := NewPluginServiceWithValidator(store, plugins.NewValidator(plugins.ValidatorOptions{HostVersion: "2.0.0", TrustedSigners: trusted}), pluginTestCacheRoot(t))
	if _, err := afterUpgrade.Detail(ctx, installed.PluginID); err != nil {
		t.Fatalf("integrity-only detail failed after host drift: %v", err)
	}
	if _, err := afterUpgrade.Enable(ctx, installed.PluginID, "admin"); err == nil || !strings.Contains(err.Error(), "host 2.0.0 is outside") {
		t.Fatalf("host-incompatible package was enabled after restart: %v", err)
	}
}

func TestPluginUninstallRejectsCleanupProjectionThatDiffersFromVerifiedManifest(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.cleanup-integrity", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := newPluginTestService(t, store).Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := newPluginTestService(t, &corruptInstalledCleanupStore{pluginLifecycleStore: store})
	if err := svc.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); err == nil || !strings.Contains(err.Error(), "cleanup policy differs") {
		t.Fatalf("uninstall cleanup projection error = %v", err)
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID); err != nil || !ok {
		t.Fatalf("failed cleanup verification removed installed plugin: %v, %v", ok, err)
	}
	grants, err := store.ListPluginGrants(ctx, installed.PluginID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("failed cleanup verification removed grants: %+v, %v", grants, err)
	}
}

func TestPluginReadContractUsesVerifiedCacheWithoutMarketplaceSource(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "default", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLocalAgentBuild(ctx, "1.0.0", true); err != nil {
		t.Fatal(err)
	}
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.read-contract", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "read-instance", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}
	listed, err := svc.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].PluginID != installed.PluginID {
		t.Fatalf("installed plugin list = %+v, %v", listed, err)
	}
	detail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Package.Manifest.ID != installed.PluginID || len(detail.Instances) != 1 || len(detail.Grants) != 1 || len(detail.AgentStatuses) != 1 || detail.AgentStatuses[0].AgentID != "local" || detail.AgentStatuses[0].TargetScope != "active" || !detail.AgentStatuses[0].Available {
		t.Fatalf("installed plugin detail is incomplete: %+v", detail)
	}
	if string(detail.Instances[0].StatusSummary) != `{}` {
		t.Fatalf("nil configure result was not normalized to an object: %s", detail.Instances[0].StatusSummary)
	}
	operations, err := svc.Operations(ctx, installed.PluginID)
	if err != nil || len(operations) == 0 || string(operations[len(operations)-1].AgentResults) != `{}` {
		t.Fatalf("configure operation result projection = %+v, %v", operations, err)
	}
	if _, err := svc.Enable(ctx, installed.PluginID, "admin"); err != nil {
		t.Fatal(err)
	}
	enablingDetail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(enablingDetail.AgentStatuses) != 1 || enablingDetail.AgentStatuses[0].OperationKind != "enable" || enablingDetail.AgentStatuses[0].OperationStatus != "applying" || enablingDetail.AgentStatuses[0].OperationID == "" || enablingDetail.AgentStatuses[0].TargetRevision == 0 {
		t.Fatalf("enable pending status = %+v", enablingDetail.AgentStatuses)
	}
	if _, err := svc.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Disable(ctx, installed.PluginID, "admin"); err != nil {
		t.Fatal(err)
	}
	disablingDetail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disablingDetail.AgentStatuses) != 1 || disablingDetail.AgentStatuses[0].OperationKind != "disable" || disablingDetail.AgentStatuses[0].OperationStatus != "applying" || disablingDetail.AgentStatuses[0].OperationID == "" || disablingDetail.AgentStatuses[0].TargetRevision == 0 {
		t.Fatalf("disable pending status = %+v", disablingDetail.AgentStatuses)
	}
	if _, err := svc.CompleteLifecycleApply(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Version: "9.9.9", CapabilitiesJSON: `["package_manifest_v1"]`, IsLocal: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetLocalAgentBuild(ctx, "", false); err != nil {
		t.Fatal(err)
	}
	disabledLocalDetail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledLocalDetail.AgentStatuses) != 1 || disabledLocalDetail.AgentStatuses[0].Available {
		t.Fatalf("stale local row overrode disabled embedded local truth: %+v", disabledLocalDetail.AgentStatuses)
	}
	if err := store.SetLocalAgentBuild(ctx, "1.0.0", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "legacy-local", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`, IsLocal: true, Mode: "local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "legacy-local-instance", ResourceGroupID: "default", Targets: []string{"legacy-local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("legacy local identity remained targetable: %v", err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "edge-binding", ResourceKind: "agent", ResourceID: "edge", ResourceGroupID: "default", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Targets: []string{"edge"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	pendingDetail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingDetail.AgentStatuses) != 2 || pendingDetail.AgentStatuses[0].TargetScope != "active" || pendingDetail.AgentStatuses[0].AgentID != "local" || pendingDetail.AgentStatuses[1].TargetScope != "pending" || pendingDetail.AgentStatuses[1].AgentID != "edge" || pendingDetail.AgentStatuses[1].OperationID == "" || pendingDetail.AgentStatuses[1].OperationStatus != "applying" {
		t.Fatalf("target-changing configure status = %+v", pendingDetail.AgentStatuses)
	}
	if len(pendingDetail.Instances[0].PendingTargets) != 1 || pendingDetail.Instances[0].PendingTargets[0] != "edge" || len(pendingDetail.Instances[0].PendingConfig) == 0 {
		t.Fatalf("pending instance projection = %+v", pendingDetail.Instances[0])
	}
	v2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect", "http.respond"}, cleanup)
	preview, err := svc.PackageDetail(ctx, v2, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.PermissionDiff.Added) != 1 || preview.PermissionDiff.Added[0] != "http.respond" || len(preview.PermissionDiff.Removed) != 0 {
		t.Fatalf("candidate permission diff = %+v", preview.PermissionDiff)
	}
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

func TestPluginReadProjectionFailsClosedOnMalformedPersistedJSON(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	base := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.read-fail-closed", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := base.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "corrupt-read", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*storage.PluginInstanceRow){
		"targets": func(row *storage.PluginInstanceRow) { row.TargetJSON = `{}` },
		"config":  func(row *storage.PluginInstanceRow) { row.ConfigJSON = `[]` },
		"null config": func(row *storage.PluginInstanceRow) {
			row.ConfigJSON = `null`
		},
		"pending config": func(row *storage.PluginInstanceRow) {
			row.PendingConfigJSON = `[]`
		},
		"null pending config": func(row *storage.PluginInstanceRow) {
			row.PendingConfigJSON = `null`
		},
		"pending targets": func(row *storage.PluginInstanceRow) {
			row.PendingTargetJSON, row.PendingResourceGroupID = `{}`, "default"
		},
		"status summary": func(row *storage.PluginInstanceRow) { row.StatusSummaryJSON = `[]` },
	} {
		t.Run(name, func(t *testing.T) {
			svc := newPluginTestServiceAtRoot(t, &corruptPluginReadStore{pluginLifecycleStore: store, mutateInstance: mutate}, base.cacheRoot)
			if _, err := svc.Detail(ctx, installed.PluginID); !errors.Is(err, ErrPluginReadProjection) {
				t.Fatalf("malformed %s detail error = %v", name, err)
			}
		})
	}
	operationStore := &corruptPluginReadStore{pluginLifecycleStore: store, mutateOperation: func(row *storage.PluginOperationRow) { row.AgentResultsJSON = `[]` }}
	if _, err := newPluginTestServiceAtRoot(t, operationStore, base.cacheRoot).Operations(ctx, installed.PluginID); !errors.Is(err, ErrPluginReadProjection) {
		t.Fatalf("malformed agent results error = %v", err)
	}
}

func TestPluginResultObjectCanonicalization(t *testing.T) {
	for name, input := range map[string]any{
		"nil":      nil,
		"raw null": json.RawMessage(`null`),
		"object":   map[string]any{"edge": "applied"},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := encodePluginResultObject(input)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			if err := json.Unmarshal([]byte(encoded), &object); err != nil || object == nil {
				t.Fatalf("canonical result = %s, %v", encoded, err)
			}
		})
	}
	for name, input := range map[string]any{"array": []string{"bad"}, "scalar": 1, "string": "bad"} {
		t.Run(name, func(t *testing.T) {
			if _, err := encodePluginResultObject(input); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("non-object result error = %v", err)
			}
		})
	}
	if _, err := pluginReadJSONObject(`null`); !errors.Is(err, ErrPluginReadProjection) {
		t.Fatalf("strict config null projection error = %v", err)
	}
	legacy, err := pluginReadStatusObject(`null`)
	if err != nil || string(legacy) != `{}` {
		t.Fatalf("legacy null projection = %s, %v", legacy, err)
	}
}

func TestConfigureProjectionFailureHasNoPersistentSideEffects(t *testing.T) {
	for name, mutate := range map[string]func(*storage.PluginInstanceRow){
		"config": func(row *storage.PluginInstanceRow) { row.ConfigJSON = `null` },
		"status": func(row *storage.PluginInstanceRow) { row.StatusSummaryJSON = `[]` },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := WithSystemMutationPrincipal(context.Background(), "test")
			store, err := storage.NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			seedPluginAgent(t, ctx, store)
			base := newPluginTestService(t, store)
			cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
			candidate := pluginCandidateFixture(t, "official.projection-"+name, "1.0.0", []string{"http.inspect"}, cleanup)
			installed, err := base.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "projection-instance", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := base.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
				t.Fatal(err)
			}
			beforeInstalled, _, err := store.GetInstalledPlugin(ctx, installed.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			beforeInstance, _, err := store.GetPluginInstance(ctx, instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil {
				t.Fatal(err)
			}

			svc := newPluginTestService(t, &corruptPluginReadStore{pluginLifecycleStore: store, mutateInstance: mutate})
			_, err = svc.ConfigureMutation(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
			if !errors.Is(err, ErrPluginReadProjection) {
				t.Fatalf("configure projection error = %v", err)
			}
			afterInstalled, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
			afterInstance, _, _ := store.GetPluginInstance(ctx, instance.ID)
			afterOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			if afterInstalled != beforeInstalled || afterInstance != beforeInstance || len(afterOperations) != len(beforeOperations) {
				t.Fatalf("projection failure mutated state: installed=%+v instance=%+v operations=%d want %d", afterInstalled, afterInstance, len(afterOperations), len(beforeOperations))
			}
		})
	}
}

func TestConfigureRejectsNestedReadOnlyInputWithoutSideEffectsAndKeepsDisplaySchema(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	schema := `{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"status":{"type":"string","readOnly":true},
			"metadata":{"type":"object","properties":{"status":{"type":"string","readOnly":true}}},
			"items":{"type":"array","items":{"type":"object","properties":{"status":{"type":"string","readOnly":true}}}}
		},
		"required":["name"],
		"additionalProperties":false
	}`
	candidate := pluginCustomCandidateFixture(t, "official.readonly-config", "1.0.0", cleanup, schema, "", nil, "*", "*")
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	beforeInstalled, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok {
		t.Fatalf("installed plugin baseline = %+v, %v, %v", beforeInstalled, ok, err)
	}
	beforeOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	for index, test := range []struct {
		raw, pointer string
	}{
		{`{"name":"safe","status":"forged-secret"}`, "/status"},
		{`{"name":"safe","metadata":{"status":"forged-secret"}}`, "/metadata/status"},
		{`{"name":"safe","items":[{"status":"forged-secret"}]}`, "/items/0/status"},
	} {
		instanceID := "readonly-rejected-" + strconv.Itoa(index)
		_, err := svc.ConfigureMutation(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instanceID, ResourceGroupID: "default", Config: json.RawMessage(test.raw), ActorID: "admin"})
		if err == nil || !strings.Contains(err.Error(), test.pointer) || strings.Contains(err.Error(), "forged-secret") {
			t.Fatalf("readOnly config %s error = %v, want redacted pointer %q", test.raw, err, test.pointer)
		}
		if _, exists, err := store.GetPluginInstance(ctx, instanceID); err != nil || exists {
			t.Fatalf("readOnly rejection persisted instance %q = %v, %v", instanceID, exists, err)
		}
		afterInstalled, _, err := store.GetInstalledPlugin(ctx, installed.PluginID)
		if err != nil {
			t.Fatal(err)
		}
		afterOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
		if err != nil {
			t.Fatal(err)
		}
		if afterInstalled != beforeInstalled || len(afterOperations) != len(beforeOperations) {
			t.Fatalf("readOnly rejection mutated plugin state: installed=%+v operations=%d want %d", afterInstalled, len(afterOperations), len(beforeOperations))
		}
	}

	configured, err := svc.ConfigureMutation(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "readonly-display", ResourceGroupID: "default", Config: json.RawMessage(`{"name":"safe","metadata":{},"items":[{}]}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("writable config rejected: %v", err)
	}
	if strings.Contains(string(configured.PendingConfig), "status") {
		t.Fatalf("configure response fabricated readOnly values: %s", configured.PendingConfig)
	}
	detail, err := svc.Detail(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	properties, _ := detail.Package.ConfigSchema["properties"].(map[string]any)
	metadata, _ := properties["metadata"].(map[string]any)
	metadataProperties, _ := metadata["properties"].(map[string]any)
	status, _ := metadataProperties["status"].(map[string]any)
	if status["readOnly"] != true {
		t.Fatalf("readOnly display schema was not preserved: %+v", detail.Package.ConfigSchema)
	}
}

func TestPluginConfigureCreateUpdateRejectsReadOnlyArrayItemsAndAllowsNamedDisplayField(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	base := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	validSchema := `{
		"type":"object",
		"properties":{
			"items":{"type":"array","minItems":1,"items":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}},
			"status":{"type":"string","readOnly":true}
		},
		"required":["items"],
		"additionalProperties":false
	}`
	candidate := pluginCustomCandidateFixture(t, "official.readonly-array-items", "1.0.0", cleanup, validSchema, "", nil, "*", "*")
	installed, err := base.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}

	instance, err := base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "existing", ResourceGroupID: "default", Config: json.RawMessage(`{"items":[{"name":"created"}]}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("create with optional named readOnly display property rejected: %v", err)
	}
	if _, err := base.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}
	instance, err = base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Config: json.RawMessage(`{"items":[{"name":"updated"}]}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("update with optional named readOnly display property rejected: %v", err)
	}
	if _, err := base.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}

	invalidSchema := `{
		"type":"object",
		"properties":{"items":{"type":"array","minItems":1,"items":{"type":"object","readOnly":true}}},
		"required":["items"],
		"additionalProperties":false
	}`
	service := newPluginTestService(t, &configSchemaOverrideStore{pluginLifecycleStore: store, schema: invalidSchema})
	beforeInstance, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok {
		t.Fatalf("existing instance baseline = %+v, %v, %v", beforeInstance, ok, err)
	}
	beforeOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []struct {
		name       string
		instanceID string
	}{
		{name: "create", instanceID: "new-invalid"},
		{name: "update", instanceID: instance.ID},
	} {
		t.Run(operation.name, func(t *testing.T) {
			_, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: operation.instanceID, ResourceGroupID: "default", Config: json.RawMessage(`{"items":[{}]}`), ActorID: "admin"})
			if err == nil || !strings.Contains(err.Error(), "readOnly is only valid on named object properties") {
				t.Fatalf("Configure() error = %v, want readOnly array item rejection", err)
			}
		})
	}
	if _, exists, err := store.GetPluginInstance(ctx, "new-invalid"); err != nil || exists {
		t.Fatalf("rejected create persisted instance = %v, %v", exists, err)
	}
	afterInstance, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok || afterInstance != beforeInstance {
		t.Fatalf("rejected update changed instance: before=%+v after=%+v, %v, %v", beforeInstance, afterInstance, ok, err)
	}
	afterOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil || len(afterOperations) != len(beforeOperations) {
		t.Fatalf("rejected configure changed operations: before=%d after=%d, %v", len(beforeOperations), len(afterOperations), err)
	}
}

func TestPluginConfigureCreateUpdateRejectsInconsistentSchemaWithoutSideEffects(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	base := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	validSchema := `{
		"type":"object",
		"properties":{
			"items":{"type":"array","minItems":1e0,"maxItems":1.0,"items":{"type":"string"}},
			"name":{"type":"string","minLength":3e0,"maxLength":3.0},
			"level":{"type":"number","minimum":1e0,"maximum":1.0},
			"status":{"type":"string","readOnly":true}
		},
		"required":["items","name","level"],
		"additionalProperties":false
	}`
	candidate := pluginCustomCandidateFixture(t, "official.consistent-schema", "1.0.0", cleanup, validSchema, "", nil, "*", "*")
	installed, err := base.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "existing-boundary", ResourceGroupID: "default", Config: json.RawMessage(`{"items":["a"],"name":"one","level":1}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("boundary create rejected: %v", err)
	}
	if _, err := base.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}
	instance, err = base.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instance.ID, ResourceGroupID: "default", Config: json.RawMessage(`{"items":["b"],"name":"two","level":1.0}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("boundary update rejected: %v", err)
	}
	if _, err := base.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}

	invalidSchemas := []struct {
		name   string
		schema string
		config string
		marker string
	}{
		{
			name:   "closed object missing required property schema",
			schema: `{"type":"object","properties":{"known":{"type":"string"}},"required":["missing"],"additionalProperties":false}`,
			marker: `required property "missing" is absent from properties`,
		},
		{
			name:   "reversed array length range",
			schema: `{"type":"object","properties":{"items":{"type":"array","minItems":2e0,"maxItems":1.0,"items":{"type":"string"}}},"required":["items"],"additionalProperties":false}`,
			marker: "minItems exceeds maxItems",
		},
		{
			name:   "reversed string length range",
			schema: `{"type":"object","properties":{"name":{"type":"string","minLength":3e0,"maxLength":2.0}},"required":["name"],"additionalProperties":false}`,
			marker: "minLength exceeds maxLength",
		},
		{
			name:   "enum disjoint from declared type",
			schema: `{"type":"object","properties":{"value":{"type":"integer","enum":["1"]}},"required":["value"],"additionalProperties":false}`,
			config: `{"value":"1"}`,
		},
		{
			name:   "enum excluded by numeric range",
			schema: `{"type":"object","properties":{"value":{"type":"number","enum":[1,2],"minimum":3,"maximum":4}},"required":["value"],"additionalProperties":false}`,
			config: `{"value":1}`,
		},
		{
			name:   "integer range contains no integer",
			schema: `{"type":"object","properties":{"value":{"type":"integer","minimum":1.1,"maximum":1.9}},"required":["value"],"additionalProperties":false}`,
			config: `{"value":1}`,
		},
		{
			name:   "integer enum and range contain no multiple",
			schema: `{"type":"object","properties":{"value":{"type":"integer","enum":[3],"minimum":2,"maximum":4,"multipleOf":2}},"required":["value"],"additionalProperties":false}`,
			config: `{"value":3}`,
		},
	}
	baselineInstalled, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok {
		t.Fatalf("installed plugin baseline = %+v, %v, %v", baselineInstalled, ok, err)
	}
	baselineInstance, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok {
		t.Fatalf("existing instance baseline = %+v, %v, %v", baselineInstance, ok, err)
	}
	baselineOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	baselineAudits, err := store.ListAuditEvents(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for schemaIndex, invalid := range invalidSchemas {
		t.Run(invalid.name, func(t *testing.T) {
			service := newPluginTestService(t, &configSchemaOverrideStore{pluginLifecycleStore: store, schema: invalid.schema})
			createID := "invalid-schema-" + strconv.Itoa(schemaIndex)
			config := invalid.config
			if config == "" {
				config = `{}`
			}
			for _, operation := range []struct {
				name       string
				instanceID string
			}{
				{name: "create", instanceID: createID},
				{name: "update", instanceID: instance.ID},
			} {
				t.Run(operation.name, func(t *testing.T) {
					_, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: operation.instanceID, ResourceGroupID: "default", Config: json.RawMessage(config), ActorID: "admin"})
					if err == nil || (invalid.marker != "" && !strings.Contains(err.Error(), invalid.marker)) {
						t.Fatalf("Configure() error = %v, want containing %q", err, invalid.marker)
					}
				})
			}
			afterInstalled, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
			if err != nil || !ok || afterInstalled != baselineInstalled {
				t.Fatalf("rejected configure changed plugin state: before=%+v after=%+v, %v, %v", baselineInstalled, afterInstalled, ok, err)
			}
			if _, exists, err := store.GetPluginInstance(ctx, createID); err != nil || exists {
				t.Fatalf("rejected create persisted instance = %v, %v", exists, err)
			}
			afterInstance, ok, err := store.GetPluginInstance(ctx, instance.ID)
			if err != nil || !ok || afterInstance != baselineInstance {
				t.Fatalf("rejected update changed instance: before=%+v after=%+v, %v, %v", baselineInstance, afterInstance, ok, err)
			}
			afterOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil || len(afterOperations) != len(baselineOperations) {
				t.Fatalf("rejected configure changed operations: before=%d after=%d, %v", len(baselineOperations), len(afterOperations), err)
			}
			afterAudits, err := store.ListAuditEvents(ctx, 1000)
			if err != nil || len(afterAudits) != len(baselineAudits) {
				t.Fatalf("rejected configure changed audits: before=%d after=%d, %v", len(baselineAudits), len(afterAudits), err)
			}
		})
	}
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

func TestConfigureMutationReturnsPrevalidatedDetailWithoutPostCommitProviderRead(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	base := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.prevalidated-response", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := base.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	provider := &failAfterPluginMutationStore{pluginLifecycleStore: store}
	detail, err := newPluginTestService(t, provider).ConfigureMutation(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "prevalidated-response", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatalf("configure mutation re-read provider after commit: %v", err)
	}
	if detail.ID != "prevalidated-response" || detail.PendingOperationID == "" || detail.PendingVersion != 1 || !provider.committed {
		t.Fatalf("prevalidated configure response = %+v, committed = %v", detail, provider.committed)
	}
	if _, _, _, err := provider.LocalAgentBuild(ctx); err == nil {
		t.Fatal("later provider failure was not armed")
	}
	row, ok, err := store.GetPluginInstance(ctx, detail.ID)
	if err != nil || !ok || row.PendingOperationID != detail.PendingOperationID || row.PendingVersion != detail.PendingVersion || row.StateVersion != detail.StateVersion {
		t.Fatalf("successful response does not match committed configure: %+v, %v, %v", row, ok, err)
	}
}

func TestPluginCustomLocalAgentIDIsTheCanonicalDefaultTarget(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "embedded-custom")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgentWithID(t, ctx, store, "embedded-custom")
	svc := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.custom-local", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := svc.ConfigureMutation(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "custom-default", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(configured.Targets) != 1 || configured.Targets[0] != "embedded-custom" || len(configured.PendingTargets) != 1 || configured.PendingTargets[0] != "embedded-custom" {
		t.Fatalf("custom default target projection = %+v", configured)
	}
	row, ok, err := store.GetPluginInstance(ctx, configured.ID)
	if err != nil || !ok || row.TargetJSON != `["embedded-custom"]` || row.PendingTargetJSON != `["embedded-custom"]` {
		t.Fatalf("custom default target persistence = %+v, %v, %v", row, ok, err)
	}
	binding, err := store.GetResourceBinding(ctx, "plugin_instance", configured.ID)
	if err != nil || binding.ParentResourceID != "embedded-custom" {
		t.Fatalf("custom default target binding = %+v, %v", binding, err)
	}
	legacy := newPluginTestService(t, &corruptPluginReadStore{pluginLifecycleStore: store, mutateInstance: func(row *storage.PluginInstanceRow) { row.TargetJSON = `null` }})
	detail, err := legacy.Detail(ctx, installed.PluginID)
	if err != nil || len(detail.Instances) != 1 || len(detail.Instances[0].Targets) != 1 || detail.Instances[0].Targets[0] != "embedded-custom" {
		t.Fatalf("legacy null target detail = %+v, %v", detail.Instances, err)
	}
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

func TestPluginUninstallDeleteCleanupRemovesOwnedInstanceAndGrantButKeepsOperations(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	now := time.Now().UTC()
	if err := store.UpsertQuotaPolicy(ctx, storage.QuotaPolicyRow{ID: "plugin-cleanup-quota", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "application_count", Limit: 10, ExceedAction: "reject", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := newPluginTestService(t, store)
	cleanupDelete := plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.cleanup", "1.0.0", []string{"http.inspect"}, cleanupDelete)
	installed, err := service.Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "delete-instance", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
		t.Fatal(err)
	}
	quotaManager := authz.NewManager(store, authz.Options{})
	statuses, err := quotaManager.ListQuotaStatus(ctx, authz.Actor{Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	if current := quotaStatusCurrent(statuses, "plugin-cleanup-quota"); current != 1 {
		t.Fatalf("configured plugin quota current = %d, want 1", current)
	}
	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetPluginInstance(ctx, instance.ID); err != nil || ok {
		t.Fatalf("delete cleanup retained instance: %v, %v", ok, err)
	}
	if grants, err := store.ListPluginGrants(ctx, installed.PluginID); err != nil || len(grants) != 0 {
		t.Fatalf("delete cleanup retained grants: %+v, %v", grants, err)
	}
	if operations, err := store.ListPluginOperations(ctx, installed.PluginID); err != nil || len(operations) == 0 || operations[len(operations)-1].Kind != "uninstall" {
		t.Fatalf("uninstall operation not retained: %+v, %v", operations, err)
	}
	statuses, err = quotaManager.ListQuotaStatus(ctx, authz.Actor{Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	if current := quotaStatusCurrent(statuses, "plugin-cleanup-quota"); current != 0 {
		t.Fatalf("uninstalled plugin quota current = %d, want 0", current)
	}
	usage, err := store.ListQuotaUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range usage {
		if row.Metric == "application_count" && row.SubjectKind == "resource_group" && row.SubjectID == "default" && row.Current != 0 {
			t.Fatalf("uninstalled plugin durable quota usage = %+v", row)
		}
	}
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
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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

func TestPluginUpgradeRejectsInvalidMigrationGraphBeforeStaging(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	service := newPluginTestService(t, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	active := pluginCandidateFixture(t, "official.invalid-migration-graph", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := service.Install(ctx, PluginInstallRequest{Package: active, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	instanceIDs := []string{"graph-instance-a", "graph-instance-b"}
	for _, instanceID := range instanceIDs {
		instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: instanceID, ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name       string
		version    string
		migrations []plugins.Migration
	}{
		{
			name:    "duplicate from",
			version: "3.0.0",
			migrations: []plugins.Migration{
				{From: "1.0.0", To: "2.0.0", File: "migrations/duplicate-a.json"},
				{From: "1.0.0", To: "3.0.0", File: "migrations/duplicate-b.json"},
			},
		},
		{
			name:    "cycle",
			version: "3.0.1",
			migrations: []plugins.Migration{
				{From: "1.0.0", To: "2.0.0", File: "migrations/cycle-a.json"},
				{From: "2.0.0", To: "1.0.0", File: "migrations/cycle-b.json"},
			},
		},
		{
			name:    "manifest version unreachable",
			version: "3.0.2",
			migrations: []plugins.Migration{
				{From: "1.0.0", To: "2.0.0", File: "migrations/unreachable.json"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := pluginCustomCandidateFixture(t, installed.PluginID, test.version, cleanup, `{"type":"object","properties":{"mode":{"type":"string"}},"required":["mode"],"additionalProperties":false}`, "", nil, "*", "*")
			managedCandidate, err := marketplace.SignerCachePath(service.cacheRoot, candidate.Package.Digest, candidate.SignatureTrust.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(managedCandidate), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(candidate.CachePath, managedCandidate); err != nil {
				t.Fatal(err)
			}
			candidate.CachePath = managedCandidate
			candidate.Package.Root = managedCandidate
			candidate = pluginInvalidMigrationGraphCandidateFixture(t, candidate, test.migrations)
			beforeInstalled, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
			if err != nil || !ok {
				t.Fatalf("installed baseline = %+v, %v, %v", beforeInstalled, ok, err)
			}
			beforeInstances := make(map[string]storage.PluginInstanceRow, len(instanceIDs))
			for _, instanceID := range instanceIDs {
				instance, ok, err := store.GetPluginInstance(ctx, instanceID)
				if err != nil || !ok {
					t.Fatalf("instance %s baseline = %+v, %v, %v", instanceID, instance, ok, err)
				}
				beforeInstances[instanceID] = instance
			}
			beforeOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil {
				t.Fatal(err)
			}
			beforeAudits, err := store.ListAuditEvents(ctx, 1000)
			if err != nil {
				t.Fatal(err)
			}

			_, err = service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: candidate, ActorID: "admin"})
			if err == nil || !strings.Contains(err.Error(), "revalidate cached package") {
				t.Fatalf("Upgrade() error = %v, want candidate package revalidation failure", err)
			}

			afterInstalled, ok, loadErr := store.GetInstalledPlugin(ctx, installed.PluginID)
			if loadErr != nil || !ok || afterInstalled != beforeInstalled {
				t.Fatalf("invalid graph changed installed package state: before=%+v after=%+v, %v, %v", beforeInstalled, afterInstalled, ok, loadErr)
			}
			for _, instanceID := range instanceIDs {
				afterInstance, ok, loadErr := store.GetPluginInstance(ctx, instanceID)
				if loadErr != nil || !ok || afterInstance != beforeInstances[instanceID] {
					t.Fatalf("invalid graph changed instance %s: before=%+v after=%+v, %v, %v", instanceID, beforeInstances[instanceID], afterInstance, ok, loadErr)
				}
			}
			if _, persisted, loadErr := store.GetPluginPackage(ctx, candidate.Package.Digest); loadErr != nil || persisted {
				t.Fatalf("invalid graph persisted candidate package = %v, %v", persisted, loadErr)
			}
			afterOperations, err := store.ListPluginOperations(ctx, installed.PluginID)
			if err != nil || len(afterOperations) != len(beforeOperations)+1 {
				t.Fatalf("invalid graph operation count: before=%d after=%d, %v", len(beforeOperations), len(afterOperations), err)
			}
			failure := afterOperations[len(afterOperations)-1]
			if failure.Kind != "upgrade" || failure.Status != "failed" || failure.ErrorClass != "validation" || !strings.EqualFold(failure.TargetPackageDigest, candidate.Package.Digest) {
				t.Fatalf("invalid graph failure operation is not traceable: %+v", failure)
			}
			afterAudits, err := store.ListAuditEvents(ctx, 1000)
			if err != nil || len(afterAudits) != len(beforeAudits)+1 {
				t.Fatalf("invalid graph audit count: before=%d after=%d, %v", len(beforeAudits), len(afterAudits), err)
			}
			var failureAuditFound bool
			for _, audit := range afterAudits {
				if audit.Action == "plugin.upgrade" && audit.TargetID == installed.PluginID && audit.Result == "failure" && strings.Contains(audit.MetadataJSON, `"operation_id":"`+failure.ID+`"`) {
					failureAuditFound = true
					break
				}
			}
			if !failureAuditFound {
				t.Fatalf("invalid graph failure audit does not reference operation %s", failure.ID)
			}
		})
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
