package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginLifecycleStagesDesiredStateAndPreservesActiveOnFailure(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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
	service := NewPluginService(store)

	cleanupRetain := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	packageV1 := pluginCandidateFixture(t, "official.lifecycle", "1.0.0", []string{"http.inspect"}, cleanupRetain)

	untrusted := packageV1
	untrusted.sourceKind = "unknown"
	_, err = service.Install(ctx, PluginInstallRequest{Package: untrusted, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err == nil || errors.Is(err, ErrPluginRiskConfirmation) {
		t.Fatalf("unknown source kind was not rejected distinctly: %v", err)
	}
	spoofedOfficial := packageV1
	spoofedOfficial.sourceID = "community"
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
	_, ok, err := store.GetPluginPackage(ctx, installed.ActivePackageDigest)
	if err != nil || !ok || installed.ActiveSourceID != marketplace.OfficialSourceID || installed.ActiveSourceKind != marketplace.SourceKindOfficial {
		t.Fatalf("installed lifecycle source provenance = %+v, %v, %v", installed, ok, err)
	}
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
	foundSource := false
	for _, operation := range operations {
		if operation.Status == "succeeded" && operation.SourceID == marketplace.OfficialSourceID && operation.SourceKind == marketplace.SourceKindOfficial {
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
	instance, err := service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if instance.CurrentState != "applying" || instance.ConfigVersion != 0 || instance.PendingVersion != 1 {
		t.Fatalf("configure did not stage desired config: %+v", instance)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, false, map[string]string{"local": "failed"}))
	if err != nil {
		t.Fatal(err)
	}
	if instance.ConfigVersion != 0 || instance.ConfigJSON != "{}" || instance.PendingVersion != 0 {
		t.Fatalf("failed configure changed current config: %+v", instance)
	}
	instance, err = service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Targets: []string{"local"}, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"local": "applied"}))
	if err != nil || instance.ConfigVersion != 1 || instance.ConfigJSON != `{"mode":"observe"}` {
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
	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); !errors.Is(err, ErrPluginUninstallBlocked) {
		t.Fatalf("uninstall while applying = %v", err)
	}
	enableResult := pendingApplyResult(t, store, installed.PluginID, "", true, map[string]string{"local": "applied"})
	installed, err = service.CompleteLifecycleApply(ctx, enableResult)
	if err != nil || installed.CurrentLifecycle != "active" {
		t.Fatalf("enable completion = %+v, %v", installed, err)
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

	packageV2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect", "http.respond"}, cleanupRetain)
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

	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: false}); !errors.Is(err, ErrPluginUninstallBlocked) {
		t.Fatalf("uninstall without drained confirmation = %v", err)
	}
	if err := service.Uninstall(ctx, PluginUninstallRequest{PluginID: installed.PluginID, ActorID: "admin", Drained: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID); err != nil || ok {
		t.Fatalf("plugin remains installed: %v %v", ok, err)
	}
	if retained, ok, err := store.GetPluginInstance(ctx, "instance-1"); err != nil || !ok || retained.ConfigVersion != 1 {
		t.Fatalf("retain cleanup lost instance: %+v, %v, %v", retained, ok, err)
	}
	if grants, err := store.ListPluginGrants(ctx, installed.PluginID); err != nil || len(grants) == 0 {
		t.Fatalf("retain cleanup lost grants: %+v, %v", grants, err)
	}
	if operations, err := store.ListPluginOperations(ctx, installed.PluginID); err != nil || len(operations) < 10 {
		t.Fatalf("uninstall removed append-only operations: %d, %v", len(operations), err)
	}
	if reinstalled, err := service.Install(ctx, PluginInstallRequest{Package: packageV1, ActorID: "admin-2", ConfirmedPermissions: []string{"http.inspect"}}); err != nil || reinstalled.ActivePackageDigest != packageV1.Package.Digest {
		t.Fatalf("reinstall with retained grants = %+v, %v", reinstalled, err)
	}
}

func TestPluginProvenanceIsPerLifecycleAssociationAndRollbackSurvivesSourceDeletion(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	customV1 := pluginCandidateFixture(t, "source.association", "1.0.0", []string{"http.inspect"}, cleanup)
	customV1.sourceID, customV1.sourceKind, customV1.sourceRiskLabel = "community", marketplace.SourceKindCustom, marketplace.UntrustedRiskLabel
	customSource, _ := marketplace.NewCustomSource("community", "Community", "https://example.com/community.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, customSource); err != nil {
		t.Fatal(err)
	}
	svc := NewPluginService(store)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: customV1, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err != nil || installed.ActiveSourceKind != marketplace.SourceKindCustom {
		t.Fatalf("custom install provenance = %+v, %v", installed, err)
	}
	officialV2 := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: officialV2, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	installed, err = svc.CompleteUpgrade(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil || installed.ActiveSourceKind != marketplace.SourceKindOfficial || installed.RollbackSourceKind != marketplace.SourceKindCustom {
		t.Fatalf("upgrade provenance swap = %+v, %v", installed, err)
	}
	if _, err := store.DeleteMarketplaceSource(ctx, customSource.ID); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || first.ActiveSourceKind != marketplace.SourceKindOfficial {
		t.Fatalf("official same-digest install = %+v, %v", first, err)
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
	svc := NewPluginService(store)
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
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-new", Version: "2.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "edge-old", Version: "1.5.0", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "default", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	svc := NewPluginService(store)
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
	svc := NewPluginService(store)
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
	svc := NewPluginService(store)
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
	svc := NewPluginService(store)
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
	svc := NewPluginService(store)
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
			installed, err := NewPluginService(store).Install(ctx, PluginInstallRequest{Package: active, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
			if err != nil {
				t.Fatal(err)
			}
			candidate := pluginCandidateFixture(t, installed.PluginID, "2.0.0", []string{"http.inspect"}, cleanup)
			service := NewPluginService(test.corrupt(t, store, active))
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
	svc := NewPluginService(store)
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

func TestPluginUninstallRejectsCleanupProjectionThatDiffersFromVerifiedManifest(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.cleanup-integrity", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := NewPluginService(store).Install(ctx, PluginInstallRequest{Package: candidate, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewPluginService(&corruptInstalledCleanupStore{pluginLifecycleStore: store})
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
	svc := NewPluginService(store)
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
	if _, err := svc.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]any{"local": "applied"})); err != nil {
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

func (s *corruptPluginReadStore) ListPluginInstances(ctx context.Context, pluginID string) ([]storage.PluginInstanceRow, error) {
	rows, err := s.pluginLifecycleStore.ListPluginInstances(ctx, pluginID)
	if err == nil && len(rows) > 0 && s.mutateInstance != nil {
		s.mutateInstance(&rows[0])
	}
	return rows, err
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
	base := NewPluginService(store)
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
		"pending config": func(row *storage.PluginInstanceRow) {
			row.PendingConfigJSON = `[]`
		},
		"pending targets": func(row *storage.PluginInstanceRow) {
			row.PendingTargetJSON, row.PendingResourceGroupID = `{}`, "default"
		},
		"status summary": func(row *storage.PluginInstanceRow) { row.StatusSummaryJSON = `[]` },
	} {
		t.Run(name, func(t *testing.T) {
			svc := NewPluginService(&corruptPluginReadStore{pluginLifecycleStore: store, mutateInstance: mutate})
			if _, err := svc.Detail(ctx, installed.PluginID); !errors.Is(err, ErrPluginReadProjection) {
				t.Fatalf("malformed %s detail error = %v", name, err)
			}
		})
	}
	operationStore := &corruptPluginReadStore{pluginLifecycleStore: store, mutateOperation: func(row *storage.PluginOperationRow) { row.AgentResultsJSON = `[]` }}
	if _, err := NewPluginService(operationStore).Operations(ctx, installed.PluginID); !errors.Is(err, ErrPluginReadProjection) {
		t.Fatalf("malformed agent results error = %v", err)
	}
}

func (s *corruptActivePackageProjectionStore) GetPluginPackage(ctx context.Context, digest string) (storage.PluginPackageRow, bool, error) {
	row, ok, err := s.pluginLifecycleStore.GetPluginPackage(ctx, digest)
	if err == nil && ok && strings.EqualFold(digest, s.digest) {
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
	service := NewPluginService(store)
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
}

func TestPluginUpgradeMigratesAllInstancesAtomicallyAndFailsClosed(t *testing.T) {
	ctx := WithSystemMutationPrincipal(context.Background(), "test")
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedPluginAgent(t, ctx, store)
	svc := NewPluginService(store)
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
	t.Helper()
	if _, err := store.GetResourceGroup(ctx, "default"); err != nil {
		if err := store.CreateResourceGroup(ctx, storage.ResourceGroupRow{ID: "default", Name: "Default", Builtin: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`, IsLocal: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, storage.ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "default", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func pluginCandidateFixture(t *testing.T, id, version string, permissionNames []string, cleanup plugins.CleanupPolicy) PluginPackageCandidate {
	t.Helper()
	staging := t.TempDir()
	permissionYAML := make([]string, 0, len(permissionNames))
	for _, permission := range permissionNames {
		permissionYAML = append(permissionYAML, "  - "+permission)
	}
	manifest := "schema_version: 1\nid: " + id + "\nversion: " + version + "\nname: Test\ncompatibility: {host: \"*\", agent: \"*\"}\nextension_points: [http.request]\npermissions:\n" + strings.Join(permissionYAML, "\n") + "\nconfig_schema: config.schema.json\ncleanup:\n" +
		"  instances: " + cleanup.Instances + "\n  config: " + cleanup.Config + "\n  owned_data: " + cleanup.OwnedData + "\n  grants: " + cleanup.Grants + "\n  shared_refs: " + cleanup.SharedRefs + "\n  audit_events: " + cleanup.AuditEvents + "\n"
	writePluginCandidateFile(t, staging, plugins.PackageManifestFile, manifest)
	writePluginCandidateFile(t, staging, plugins.ConfigSchemaFile, `{"type":"object","properties":{"mode":{"type":"string"}},"required":["mode"],"additionalProperties":false}`)
	digest, err := plugins.ComputePackageDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, staging, plugins.PackageDigestFile, digest)
	validated, err := plugins.NewValidator(plugins.ValidatorOptions{}).ValidatePackage(staging, plugins.PackageExpectation{ID: id, Version: version, SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	target := filepath.Join(cacheRoot, digest)
	if err := os.Rename(staging, target); err != nil {
		t.Fatal(err)
	}
	validated.Root = target
	return PluginPackageCandidate{Package: validated, CachePath: target, sourceID: marketplace.OfficialSourceID, sourceKind: marketplace.SourceKindOfficial}
}

func pluginCustomCandidateFixture(t *testing.T, id, version string, cleanup plugins.CleanupPolicy, schema, manifestExtra string, files map[string]string, hostCompatibility, agentCompatibility string) PluginPackageCandidate {
	t.Helper()
	staging := t.TempDir()
	manifest := "schema_version: 1\nid: " + id + "\nversion: " + version + "\nname: Test\ncompatibility: {host: \"" + hostCompatibility + "\", agent: \"" + agentCompatibility + "\"}\nextension_points: [http.request]\npermissions: [http.inspect]\nconfig_schema: config.schema.json\ncleanup:\n" +
		"  instances: " + cleanup.Instances + "\n  config: " + cleanup.Config + "\n  owned_data: " + cleanup.OwnedData + "\n  grants: " + cleanup.Grants + "\n  shared_refs: " + cleanup.SharedRefs + "\n  audit_events: " + cleanup.AuditEvents + "\n" + manifestExtra
	writePluginCandidateFile(t, staging, plugins.PackageManifestFile, manifest)
	writePluginCandidateFile(t, staging, plugins.ConfigSchemaFile, schema)
	for name, value := range files {
		writePluginCandidateFile(t, staging, name, value)
	}
	digest, err := plugins.ComputePackageDigest(staging)
	if err != nil {
		t.Fatal(err)
	}
	writePluginCandidateFile(t, staging, plugins.PackageDigestFile, digest)
	validated, err := plugins.NewValidator(plugins.ValidatorOptions{}).ValidatePackage(staging, plugins.PackageExpectation{ID: id, Version: version, SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), digest)
	if err := os.Rename(staging, target); err != nil {
		t.Fatal(err)
	}
	validated.Root = target
	return PluginPackageCandidate{Package: validated, CachePath: target, sourceID: marketplace.OfficialSourceID, sourceKind: marketplace.SourceKindOfficial}
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
