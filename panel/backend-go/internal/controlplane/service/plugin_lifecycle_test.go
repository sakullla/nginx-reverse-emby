package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPluginLifecycleStagesDesiredStateAndPreservesActiveOnFailure(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewPluginService(store)

	cleanupRetain := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	packageV1 := pluginCandidateFixture(t, "official.lifecycle", "1.0.0", []string{"http.inspect"}, cleanupRetain)

	_, err = service.Install(ctx, PluginInstallRequest{Package: packageV1, SourceKind: "unknown", ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true})
	if err == nil || errors.Is(err, ErrPluginRiskConfirmation) {
		t.Fatalf("unknown source kind was not rejected distinctly: %v", err)
	}
	installed, err := service.Install(ctx, PluginInstallRequest{Package: packageV1, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	if installed.DesiredLifecycle != "disabled" || installed.CurrentLifecycle != "disabled" {
		t.Fatalf("install state = %+v", installed)
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
	instance, err = service.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: "instance-1", ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err = service.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, map[string]string{"local": "applied"}))
	if err != nil || instance.ConfigVersion != 1 || instance.ConfigJSON != `{"mode":"observe"}` {
		t.Fatalf("completed config = %+v, %v", instance, err)
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
	operations, err := store.ListPluginOperations(ctx, installed.PluginID)
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
	if _, err := service.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: packageV2, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"}); !errors.Is(err, ErrPluginPermissionConfirmation) {
		t.Fatalf("upgrade permission increase did not require confirmation: %v", err)
	}
	current, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != packageV1.Package.Digest || current.StagedPackageDigest != "" {
		t.Fatalf("failed upgrade changed package pointers: %+v", current)
	}
	upgrade := PluginUpgradeRequest{PluginID: installed.PluginID, Package: packageV2, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect", "http.respond"}}
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

	rollingBack, err := service.Rollback(ctx, installed.PluginID, "admin")
	if err != nil || rollingBack.ActivePackageDigest != packageV2.Package.Digest || rollingBack.StagedPackageDigest != packageV1.Package.Digest || rollingBack.CurrentLifecycle != "rolling_back" {
		t.Fatalf("staged rollback = %+v, %v", rollingBack, err)
	}
	rollbackFailed, err := service.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", false, nil))
	if err != nil || rollbackFailed.ActivePackageDigest != packageV2.Package.Digest {
		t.Fatalf("failed rollback changed active package: %+v, %v", rollbackFailed, err)
	}
	if _, err := service.Rollback(ctx, installed.PluginID, "admin"); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := service.CompleteRollback(ctx, pendingApplyResult(t, store, installed.PluginID, "", true, nil))
	if err != nil || rolledBack.ActivePackageDigest != packageV1.Package.Digest || rolledBack.RollbackPackageDigest != packageV2.Package.Digest {
		t.Fatalf("rollback completion = %+v, %v", rolledBack, err)
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
	if reinstalled, err := service.Install(ctx, PluginInstallRequest{Package: packageV1, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin-2", ConfirmedPermissions: []string{"http.inspect"}}); err != nil || reinstalled.ActivePackageDigest != packageV1.Package.Digest {
		t.Fatalf("reinstall with retained grants = %+v, %v", reinstalled, err)
	}
}

func TestPluginUninstallDeleteCleanupRemovesOwnedInstanceAndGrantButKeepsOperations(t *testing.T) {
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := NewPluginService(store)
	cleanupDelete := plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.cleanup", "1.0.0", []string{"http.inspect"}, cleanupDelete)
	installed, err := service.Install(ctx, PluginInstallRequest{Package: candidate, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
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
	ctx := context.Background()
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewPluginService(store)
	cleanup := plugins.CleanupPolicy{Instances: "retain", Config: "retain", OwnedData: "retain", Grants: "retain", SharedRefs: "retain", AuditEvents: "retain"}
	v1 := pluginCandidateFixture(t, "official.migration", "1.0.0", []string{"http.inspect"}, cleanup)
	installed, err := svc.Install(ctx, PluginInstallRequest{Package: v1, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"instance-a", "instance-b"} {
		instance, err := svc.Configure(ctx, PluginConfigureRequest{PluginID: installed.PluginID, InstanceID: id, Config: json.RawMessage(`{"mode":"observe"}`), ActorID: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CompleteConfigure(ctx, pendingApplyResult(t, store, installed.PluginID, instance.ID, true, nil)); err != nil {
			t.Fatal(err)
		}
	}
	v2 := pluginCustomCandidateFixture(t, installed.PluginID, "2.0.0", cleanup, `{"type":"object","properties":{"behavior":{"type":"string"}},"required":["behavior"],"additionalProperties":false}`, "migrations:\n  - from: 1.0.0\n    to: 2.0.0\n    file: migrations/1-to-2.json\n", map[string]string{"migrations/1-to-2.json": `{"operations":[{"op":"rename","from":"/mode","path":"/behavior"}]}`}, `*`, `*`)
	staged, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: v2, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"})
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
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: missing, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"}); err == nil {
		t.Fatal("missing migration chain was accepted")
	}
	current, _, _ := store.GetInstalledPlugin(ctx, installed.PluginID)
	if current.ActivePackageDigest != v2.Package.Digest || current.StagedPackageDigest != "" {
		t.Fatalf("failed migration changed package pointers: %+v", current)
	}
	invalidResult := pluginCustomCandidateFixture(t, installed.PluginID, "3.0.0", cleanup, `{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`, "migrations:\n  - from: 2.0.0\n    to: 3.0.0\n    file: migrations/2-to-3.json\n", map[string]string{"migrations/2-to-3.json": `{"operations":[{"op":"set","path":"/count","value":"bad"}]}`}, `*`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: invalidResult, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"}); err == nil {
		t.Fatal("migration result violating the new schema was accepted")
	}
	executionFailure := pluginCustomCandidateFixture(t, installed.PluginID, "3.0.1", cleanup, `{"type":"object","properties":{"required_value":{"type":"string"}},"required":["required_value"],"additionalProperties":false}`, "migrations:\n  - from: 2.0.0\n    to: 3.0.1\n    file: migrations/2-to-3.json\n", map[string]string{"migrations/2-to-3.json": `{"operations":[{"op":"remove","path":"/missing"}]}`}, `*`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: executionFailure, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"}); err == nil {
		t.Fatal("migration execution failure was accepted")
	}
	incompatible := pluginCustomCandidateFixture(t, installed.PluginID, "2.1.0", cleanup, `{"type":"object"}`, "", nil, `>=9.0.0`, `*`)
	if _, err := svc.Upgrade(ctx, PluginUpgradeRequest{PluginID: installed.PluginID, Package: incompatible, SourceKind: marketplace.SourceKindOfficial, ActorID: "admin"}); err == nil {
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
	return PluginPackageCandidate{Package: validated, CachePath: target}
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
	return PluginPackageCandidate{Package: validated, CachePath: target}
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
