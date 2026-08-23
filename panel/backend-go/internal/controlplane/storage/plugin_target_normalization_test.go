package storage

import (
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginLegacyTargetNormalizationIsManifestBoundAndIdempotent(t *testing.T) {
	store := newStorageMigrationTestStore(t, "local")
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	seedPluginTargetNormalizationPackage(t, store, "official.control-only", "a", false, now)
	seedPluginTargetNormalizationPackage(t, store, "official.dual-face", "b", true, now)

	controlInstalled := pluginTargetNormalizationInstalled("official.control-only", "a", now)
	controlInstalled.PendingOperationID, controlInstalled.PendingKind = "op-control-pending", "configure"
	controlInstalled.PendingRevision = 17
	controlInstalled.CurrentLifecycle = "applying"
	dualInstalled := pluginTargetNormalizationInstalled("official.dual-face", "b", now)
	if err := store.db.Create(&[]InstalledPluginRow{controlInstalled, dualInstalled}).Error; err != nil {
		t.Fatal(err)
	}
	controlInstance := pluginTargetNormalizationInstance("instance-control", controlInstalled.PluginID, now)
	controlInstance.PendingConfigJSON = `{"token":"pending"}`
	controlInstance.PendingVersion = 2
	controlInstance.PendingOperationID = controlInstalled.PendingOperationID
	controlInstance.PendingResourceGroupID = "default"
	controlInstance.PendingTargetJSON = `["edge-a"]`
	controlInstance.PendingPolicyChainsJSON = `["chain-a"]`
	controlInstance.PendingBindingsJSON = `[{"consumer":{"kind":"http_rule","id":"1"},"target_agent_id":"edge-a"}]`
	controlInstance.PendingSecretHandlesJSON = `[{"pointer":"/token","id":"secret-a","version":1,"digest":"digest","purpose":"test"}]`
	dualInstance := pluginTargetNormalizationInstance("instance-dual", dualInstalled.PluginID, now)
	if err := store.db.Create(&[]PluginInstanceRow{controlInstance, dualInstance}).Error; err != nil {
		t.Fatal(err)
	}
	operations := []PluginOperationRow{
		pluginTargetNormalizationOperation("op-control-active", controlInstalled.PluginID, controlInstance.ID, "succeeded", now),
		pluginTargetNormalizationOperation(controlInstalled.PendingOperationID, controlInstalled.PluginID, controlInstance.ID, "applying", now),
		pluginTargetNormalizationOperation("op-dual-active", dualInstalled.PluginID, dualInstance.ID, "succeeded", now),
	}
	if err := store.db.Create(&operations).Error; err != nil {
		t.Fatal(err)
	}
	audits := make([]AuditEventRow, 0, len(operations))
	for _, operation := range operations {
		audits = append(audits, AuditEventRow{ID: "audit-" + operation.ID, ActorID: "admin", Action: "plugin." + operation.Kind, TargetKind: "plugin", TargetID: operation.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now})
	}
	if err := store.db.Create(&audits).Error; err != nil {
		t.Fatal(err)
	}
	statuses := []PluginAgentRuntimeStatusRow{
		pluginTargetNormalizationStatus("op-control-active", "edge-a", controlInstance, "active", now),
		pluginTargetNormalizationStatus("op-control-active", "local", controlInstance, "active", now),
		pluginTargetNormalizationStatus(controlInstalled.PendingOperationID, "edge-a", controlInstance, "pending", now),
		pluginTargetNormalizationStatus("op-dual-active", "edge-a", dualInstance, "active", now),
	}
	if err := store.db.Create(&statuses).Error; err != nil {
		t.Fatal(err)
	}
	beforeGenerations, err := store.LoadAgentPluginGenerations(t.Context(), "edge-a", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations() before normalization error = %v", err)
	}

	beforeOperations, beforeAudits := pluginTargetNormalizationHistory(t, store)
	var beforeRevisions []AgentRevisionRow
	if err := store.db.Order("agent_id, revision").Find(&beforeRevisions).Error; err != nil {
		t.Fatal(err)
	}
	if err := normalizeLegacyControlPlanePluginTargets(t.Context(), store.db, "local"); err != nil {
		t.Fatalf("normalizeLegacyControlPlanePluginTargets() error = %v", err)
	}

	var gotControl, gotDual PluginInstanceRow
	if err := store.db.Where("id = ?", controlInstance.ID).First(&gotControl).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Where("id = ?", dualInstance.ID).First(&gotDual).Error; err != nil {
		t.Fatal(err)
	}
	if gotControl.TargetJSON != `["local"]` || gotControl.PendingOperationID != "" || gotControl.PendingTargetJSON != "" || gotControl.PendingVersion != 0 || gotControl.CurrentState != "active" {
		t.Fatalf("normalized control-plane instance = %+v", gotControl)
	}
	if !reflect.DeepEqual(gotDual, dualInstance) {
		t.Fatalf("dual-face instance changed:\n got  %+v\n want %+v", gotDual, dualInstance)
	}
	var gotInstalled InstalledPluginRow
	if err := store.db.Where("plugin_id = ?", controlInstalled.PluginID).First(&gotInstalled).Error; err != nil {
		t.Fatal(err)
	}
	if gotInstalled.PendingOperationID != "" || gotInstalled.PendingKind != "" || gotInstalled.PendingRevision != 0 || gotInstalled.CurrentLifecycle != "active" {
		t.Fatalf("normalized installed plugin = %+v", gotInstalled)
	}
	var gotStatuses []PluginAgentRuntimeStatusRow
	if err := store.db.Order("plugin_id, operation_id").Find(&gotStatuses).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range gotStatuses {
		want := "retired"
		if status.PluginID == dualInstalled.PluginID {
			want = "active"
		}
		if status.AuthoritySlot != want {
			t.Fatalf("runtime status %s/%s authority = %q, want %q", status.PluginID, status.OperationID, status.AuthoritySlot, want)
		}
	}
	afterGenerations, err := store.LoadAgentPluginGenerations(t.Context(), "edge-a", runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		t.Fatalf("LoadAgentPluginGenerations() after normalization error = %v", err)
	}
	if !reflect.DeepEqual(afterGenerations, beforeGenerations) || len(afterGenerations) != 1 || afterGenerations[0].PluginID != dualInstalled.PluginID {
		t.Fatalf("dual-face generations changed:\n got  %+v\n want %+v", afterGenerations, beforeGenerations)
	}
	afterOperations, afterAudits := pluginTargetNormalizationHistory(t, store)
	if !reflect.DeepEqual(afterOperations, beforeOperations) || !reflect.DeepEqual(afterAudits, beforeAudits) {
		t.Fatal("normalization rewrote plugin operation or audit history")
	}
	var revisions []AgentRevisionRow
	if err := store.db.Find(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(revisions, beforeRevisions) {
		t.Fatalf("normalization changed revisions:\n got  %+v\n want %+v", revisions, beforeRevisions)
	}

	first := pluginTargetNormalizationSnapshot(t, store)
	if err := normalizeLegacyControlPlanePluginTargets(t.Context(), store.db, "local"); err != nil {
		t.Fatalf("second normalizeLegacyControlPlanePluginTargets() error = %v", err)
	}
	second := pluginTargetNormalizationSnapshot(t, store)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("normalization is not idempotent:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestPluginLegacyTargetNormalizationFailsClosedWithoutAuthoritativeManifest(t *testing.T) {
	store := newStorageMigrationTestStore(t, "local")
	now := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	installed := pluginTargetNormalizationInstalled("official.missing-authority", "c", now)
	instance := pluginTargetNormalizationInstance("instance-missing", installed.PluginID, now)
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	status := pluginTargetNormalizationStatus("op-missing", "edge-a", instance, "active", now)
	if err := store.db.Create(&status).Error; err != nil {
		t.Fatal(err)
	}

	before := pluginTargetNormalizationSnapshot(t, store)
	err := normalizeLegacyControlPlanePluginTargets(t.Context(), store.db, "local")
	if err == nil || !strings.Contains(err.Error(), "authoritative package is unavailable") {
		t.Fatalf("normalizeLegacyControlPlanePluginTargets() error = %v", err)
	}
	after := pluginTargetNormalizationSnapshot(t, store)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("fail-closed normalization changed data:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func seedPluginTargetNormalizationPackage(t *testing.T, store *GormStore, pluginID, digestSeed string, agentFace bool, now time.Time) {
	t.Helper()
	digest := strings.Repeat(digestSeed, 64)
	identity := strings.Repeat(strings.ToUpper(digestSeed), 64)
	hostScopes := []string(nil)
	if agentFace {
		hostScopes = []string{pluginsdk.HostScopeAgent}
	}
	artifactDigest := strings.Repeat("f", 64)
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: pluginID, Version: "1.0.0", Name: pluginID,
		Runtime:         plugins.Runtime{Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1, HostScope: pluginsdk.HostScopeControlPlane, HostScopes: hostScopes, Entry: "plugin"},
		Artifacts:       []plugins.Artifact{{Path: "plugin", SHA256: artifactDigest, Size: 10, Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}},
		ExtensionPoints: []string{"dns.provider"},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 1000, MemoryBytes: 1024, Concurrency: 1, InputBytes: 1024, OutputBytes: 1024},
		FailurePolicy:   plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	row := PluginPackageRow{Identity: identity, Digest: digest, PluginID: pluginID, Version: manifest.Version, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, EntryPath: manifest.Runtime.Entry, SignatureKeyID: "fixture", SignatureFingerprint: strings.Repeat("e", 64), SignatureVerdict: "verified", ResourceBudgetJSON: `{}`, FailurePolicyJSON: `{}`, CachePath: "fixture", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}
	if err := store.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if agentFace {
		artifact := PluginArtifactRow{ID: "artifact-" + pluginID, PackageIdentity: identity, PackageDigest: digest, Path: "plugin", SHA256: artifactDigest, SizeBytes: 10, Mode: "executable", RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
		if err := store.db.Create(&artifact).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func pluginTargetNormalizationInstalled(pluginID, digestSeed string, now time.Time) InstalledPluginRow {
	return InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: strings.Repeat(digestSeed, 64), ActivePackageIdentity: strings.Repeat(strings.ToUpper(digestSeed), 64), RuntimeKind: pluginsdk.RuntimeRPCService, RuntimeABI: pluginsdk.RPCABIV1, HostScope: pluginsdk.HostScopeControlPlane, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "install-" + pluginID, StateVersion: 3, InstalledAt: now, UpdatedAt: now}
}

func pluginTargetNormalizationInstance(id, pluginID string, now time.Time) PluginInstanceRow {
	return PluginInstanceRow{ID: id, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: `["edge-a"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 1, PendingConfigJSON: "", PendingTargetJSON: "", PendingPolicyChainsJSON: `[]`, PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: "", RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 2, UpdatedAt: now}
}

func pluginTargetNormalizationOperation(id, pluginID, instanceID, status string, now time.Time) PluginOperationRow {
	return PluginOperationRow{ID: id, PluginID: pluginID, InstanceID: instanceID, ResourceGroupID: "default", Kind: "configure", Status: status, AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}
}

func pluginTargetNormalizationStatus(operationID, agentID string, instance PluginInstanceRow, authority string, now time.Time) PluginAgentRuntimeStatusRow {
	return PluginAgentRuntimeStatusRow{OperationID: operationID, AgentID: agentID, InstanceID: instance.ID, PluginID: instance.PluginID, ResourceGroupID: instance.ResourceGroupID, TargetVersion: instance.ConfigVersion, AuthoritySlot: authority, Revision: 7, GenerationID: strings.Repeat("d", 64), PackageDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64), ConfigVersion: instance.ConfigVersion, State: "active", DetailsJSON: `{}`, BudgetJSON: `{}`, UpdatedAt: now}
}

func pluginTargetNormalizationHistory(t *testing.T, store *GormStore) ([]PluginOperationRow, []AuditEventRow) {
	t.Helper()
	var operations []PluginOperationRow
	if err := store.db.Order("id").Find(&operations).Error; err != nil {
		t.Fatal(err)
	}
	var audits []AuditEventRow
	if err := store.db.Order("id").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	return operations, audits
}

type pluginTargetNormalizationState struct {
	Installed []InstalledPluginRow
	Instances []PluginInstanceRow
	Statuses  []PluginAgentRuntimeStatusRow
	Revisions []AgentRevisionRow
}

func pluginTargetNormalizationSnapshot(t *testing.T, store *GormStore) pluginTargetNormalizationState {
	t.Helper()
	var result pluginTargetNormalizationState
	if err := store.db.Order("plugin_id").Find(&result.Installed).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Order("id").Find(&result.Instances).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Order("plugin_id, operation_id").Find(&result.Statuses).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Order("agent_id, revision").Find(&result.Revisions).Error; err != nil {
		t.Fatal(err)
	}
	return result
}
