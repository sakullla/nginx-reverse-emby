//go:build !fast && !integration

package service

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bytes"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"runtime"
)

func newPolicyConsumptionFixture(t *testing.T, rpcFace ...bool) (*PluginCapabilityManager, pluginhost.Candidate) {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed WAF attach scenarios run in the full test tier")
	}
	root := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local", TrafficStatsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}

	pluginID := "ip-policy"
	instanceID := "ip-default"
	if len(rpcFace) > 0 && rpcFace[0] {
		pluginID = "routing-plugin"
		instanceID = "routing-default"
	}
	publicKey := plugins.DefaultTrustedSigners()[plugins.OfficialSignatureKeyID]
	encodedKey := base64.StdEncoding.EncodeToString(publicKey)
	fingerprintSum := sha256.Sum256(publicKey)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	digest := strings.Repeat("a", 64)
	sourceID := marketplace.OfficialSourceID
	identity := storage.PluginPackageIdentity(digest, sourceID, fingerprint)
	cacheRoot := filepath.Join(root, "plugins", "packages")
	wasm := []byte("waf-policy-wasm-" + pluginID)
	wasmDigest := sha256.Sum256(wasm)
	rpcDigest := sha256.Sum256([]byte("rpc-" + pluginID))
	artifacts := []plugins.Artifact{
		{Path: "artifacts/linux-amd64/plugin", SHA256: hex.EncodeToString(rpcDigest[:]), Size: int64(len("rpc-" + pluginID)), Mode: "executable", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		{Path: "artifacts/policy.wasm", SHA256: hex.EncodeToString(wasmDigest[:]), Size: int64(len(wasm)), Mode: "wasm"},
	}
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: pluginID, Version: "1.0.0", Name: pluginID,
		Runtime: plugins.Runtime{
			Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
			HostScope: pluginsdk.HostScopeControlPlane, Entry: "plugin", PolicyKind: "ip",
			Policy: &pluginsdk.RuntimePolicy{
				Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1, HostScope: pluginsdk.HostScopeAgent,
				Entry:          "artifacts/policy.wasm",
				ResourceBudget: plugins.ResourceBudget{TimeoutMS: 2, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096},
				FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
			},
		},
		Artifacts:       artifacts,
		ExtensionPoints: []string{pluginsdk.ExtensionUIRoute, pluginsdk.ExtensionHTTPRequest, pluginsdk.ExtensionL4Accept},
		Metadata:        map[string]string{pluginsdk.PolicyModeHandlingMetadataKey: string(pluginsdk.PolicyModeHandlingRaw)},
		Permissions:     []plugins.Permission{{Name: "dataset.bind"}, {Name: "policy.control"}, {Name: "storage.write"}},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 2000, MemoryBytes: 1048576, Concurrency: 8, InputBytes: 65536, OutputBytes: 4096, CPUMillis: 100, Restarts: 1},
		FailurePolicy:   plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "on-failure", CoreFallback: "preserve"},
		Signature:       plugins.Signature{Algorithm: "ed25519", KeyID: plugins.OfficialSignatureKeyID},
	}
	if len(rpcFace) > 0 && rpcFace[0] {
		manifest.Runtime.Policy = nil
		manifest.Runtime.PolicyKind = ""
		manifest.Runtime.HostScopes = []string{pluginsdk.HostScopeControlPlane, pluginsdk.HostScopeAgent}
		manifest.Metadata = nil
		manifest.Artifacts = manifest.Artifacts[:1]
		manifest.ExtensionPoints = []string{pluginsdk.ExtensionUIRoute}
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	row := storage.PluginPackageRow{
		Identity: identity, Digest: digest, PluginID: pluginID, Version: manifest.Version,
		SignatureKeyID: plugins.OfficialSignatureKeyID, SignaturePublicKey: encodedKey, SignatureFingerprint: fingerprint,
		SourceID: sourceID, SourceKind: marketplace.SourceKindOfficial, ManifestJSON: string(manifestJSON),
		SignatureVerdict: "verified", ConfigSchemaJSON: `{"type":"object","properties":{"rules":{"type":"string"}},"additionalProperties":false}`, VerifiedAt: now,
	}
	projected, projectedArtifacts, err := storage.ProjectPluginPackage(row, manifest)
	if err != nil {
		t.Fatal(err)
	}
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cachePath, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "artifacts", "policy.wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cachePath, "artifacts", "linux-amd64"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "artifacts", "linux-amd64", "plugin"), []byte("rpc-"+pluginID), 0600); err != nil {
		t.Fatal(err)
	}
	projected.CachePath = cachePath
	if _, err := marketplace.NewVerifiedCache(cacheRoot, plugins.NewValidator(plugins.ValidatorOptions{}), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(cacheRoot) })

	installOp := storage.PluginOperationRow{
		ID: "op-install-" + pluginID, PluginID: pluginID, Kind: "install", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&installOp, projected); err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity,
		RuntimeKind: projected.RuntimeKind, RuntimeABI: projected.RuntimeABI, HostScope: projected.HostScope,
		ActiveSourceID: projected.SourceID, ActiveSourceKind: projected.SourceKind,
		ActiveSignatureKeyID: projected.SignatureKeyID, ActiveSignaturePublicKey: projected.SignaturePublicKey,
		ActiveSignatureFingerprint: projected.SignatureFingerprint, DesiredLifecycle: "enabled", CurrentLifecycle: "active",
		CleanupPolicyJSON: `{}`, LastOperationID: installOp.ID, StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.InstallPlugin(ctx, storage.PluginInstallTransaction{
		Package: projected, Artifacts: projectedArtifacts, Installed: installed, Operation: installOp,
		Grants: []storage.PluginGrantRow{{ID: "data-grant", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: "dataset.bind"}, {ID: "policy-grant", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: "policy.control"}, {ID: "config-grant", PluginID: pluginID, PackageDigest: digest, PackageIdentity: identity, Permission: "storage.write"}},
		Audit: storage.AuditEventRow{
			ID: "audit-install-" + pluginID, ActorID: "admin", Action: "plugin.install",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("InstallPlugin() error = %v", err)
	}

	installed = mustInstalledPlugin(t, store, pluginID)
	instance := storage.PluginInstanceRow{
		ID: instanceID, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: `[]`,
		PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`,
		ConfigVersion: 1, PendingConfigJSON: "", PendingTargetJSON: "", PendingPolicyChainsJSON: `[]`,
		PendingBindingsJSON: `[]`, PendingSecretHandlesJSON: `[]`, RollbackConfigJSON: "",
		RollbackPolicyChainsJSON: `[]`, RollbackBindingsJSON: `[]`, RollbackSecretHandlesJSON: `[]`,
		DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, UpdatedAt: now,
	}
	configureOp := storage.PluginOperationRow{
		ID: "op-configure-" + pluginID, PluginID: pluginID, Kind: "configure", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&configureOp, projected); err != nil {
		t.Fatal(err)
	}
	nextInstalled := installed
	nextInstalled.LastOperationID = configureOp.ID
	nextInstalled.UpdatedAt = now
	if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{
		PluginID: pluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		Installed: &nextInstalled, ReplaceInstance: &instance,
		Operation: configureOp,
		Audit: storage.AuditEventRow{
			ID: "audit-configure-" + pluginID, ActorID: "admin", Action: "plugin.configure",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	service := NewPluginService(store, cacheRoot)
	service.ConfigureRevisionMutations(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	candidate := pluginhost.Candidate{InstanceID: instanceID, IncarnationID: instance.IncarnationID, ResourceGroupID: "default", Identity: pluginhost.Identity{PluginID: pluginID, Generation: "generation-policy", PackageDigest: digest, Scopes: []string{"dataset.bind", "policy.control", "storage.write"}}, Grants: []string{"dataset.bind", "policy.control", "storage.write"}}
	if err := store.StagePluginRuntime(ctx, storage.PluginRuntimeInstanceRow{InstanceID: instanceID, PluginID: pluginID, HostScope: pluginsdk.HostScopeControlPlane, CandidateGeneration: candidate.Identity.Generation, CandidatePackageDigest: digest, CandidateResourceGroupID: "default"}); err != nil {
		t.Fatal(err)
	}
	datasets := NewDatasetService(service.cfg, store)
	t.Cleanup(func() { _ = datasets.Close() })
	return &PluginCapabilityManager{store: store, plugins: service, datasets: datasets}, candidate
}

func TestPolicyConsumptionTransactionsAndPublishedSnapshots(t *testing.T) {
	manager, candidate := newPolicyConsumptionFixture(t)
	store := manager.datasets.store
	client := datasetHostRuntimeClient(t, manager, &candidate)
	source := pluginsdk.DatasetSource{ID: "classes", Name: "Classes", Format: pluginsdk.DatasetFormatCIDR}
	auth := DatasetAuthorization{Administrator: true, Manage: true, ActorID: "admin", ResourceGroupID: "default"}
	if err := manager.datasets.PutSource(t.Context(), auth, source, DatasetRetrieval{}); err != nil {
		t.Fatal(err)
	}
	classes := []datasets.CIDRClassification{}
	for i := 0; i < 150; i++ {
		classes = append(classes, datasets.CIDRClassification{Name: fmt.Sprintf("region-%03d", i), Kind: pluginsdk.DatasetClassificationRegion, CIDRs: []string{fmt.Sprintf("10.%d.0.0/16", i)}})
	}
	raw, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: classes})
	digest, err := manager.datasets.Upload(t.Context(), auth, source.ID, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.datasets.Control(t.Context(), auth, pluginsdk.DatasetControlRequest{Action: pluginsdk.DatasetControlImport, SourceID: source.ID, Candidate: &pluginsdk.DatasetImportCandidate{Revision: "large-catalog", ExpectedDigest: digest, ArtifactDigest: digest}}); err != nil {
		t.Fatal(err)
	}
	versions, _ := store.ListDatasetVersions(t.Context(), source.ID)
	if len(versions) != 1 {
		t.Fatal("missing compiled version")
	}
	instance, _, _ := store.GetPluginInstance(t.Context(), candidate.InstanceID)
	settings, _ := store.GetPluginPolicySettings(t.Context(), candidate.InstanceID)
	if settings.DefaultMode != "observe" {
		t.Fatal("new IP must start observe", settings)
	}
	req := pluginsdk.DatasetBindingRequest{Action: pluginsdk.DatasetBindingBind, OperationID: "bind-rules", InstanceID: candidate.InstanceID, SourceID: source.ID, Targets: pluginsdk.ExecutionTargetSelection{Mode: pluginsdk.ExecutionTargetsEffective}, Spec: &pluginsdk.DatasetBindingSpec{VersionDigest: versions[0].Digest, Classifications: []pluginsdk.DatasetClassification{{Name: "region-149", Kind: pluginsdk.DatasetClassificationRegion}}}, InstanceUpdate: &pluginsdk.DatasetBindingInstanceUpdate{ExpectedRevision: instance.StateVersion, Config: json.RawMessage(`{"rules":"region-149"}`), PolicyDefaults: &pluginsdk.PolicyDefaultSettingsUpdate{Stage: pluginsdk.PolicyStageIdentity{Kind: "ip", PolicyID: candidate.InstanceID}, Mode: pluginsdk.PolicyModeObserve, ExpectedRevision: settings.Revision}}}
	result, err := client.ManageDatasetBinding(t.Context(), req)
	if err != nil {
		t.Fatal("actual dispatcher atomic bind", err)
	}
	if len(result.Targets) != 1 || result.Targets[0].AgentID != "local" || result.Targets[0].State == "applied" {
		t.Fatalf("desired ACK manufactured apply: %+v", result)
	}
	snapshot := latestWAFCoordinatorSnapshot(t, store, "local")
	if len(snapshot.Datasets) != 1 || snapshot.Datasets[0].Bindings[0].Classifications[0].Name != "region-149" {
		t.Fatal("data absent from actual revision")
	}
	instance, _, _ = store.GetPluginInstance(t.Context(), candidate.InstanceID)
	if instance.ConfigJSON != `{"rules":"region-149"}` || result.InstanceRevision != instance.StateVersion || result.PolicyRevision != settings.Revision+1 {
		t.Fatal("bundle clocks/config mismatch", instance, result)
	}
	again, err := client.ManageDatasetBinding(t.Context(), req)
	if err != nil || again.Revision != result.Revision || again.InstanceRevision != result.InstanceRevision {
		t.Fatal("durable original ACK replay", again, err)
	}
	req.InstanceUpdate.Config = json.RawMessage(`{"rules":"different"}`)
	if _, err := client.ManageDatasetBinding(t.Context(), req); err == nil {
		t.Fatal("operation reused with distinct complete intent")
	}

	applyConsumptionRevision(t, store, "local")
	inspect := pluginsdk.DatasetBindingRequest{Action: pluginsdk.DatasetBindingInspect, InstanceID: instance.ID, SourceID: source.ID, Targets: req.Targets}
	applied, err := client.ManageDatasetBinding(t.Context(), inspect)
	if err != nil || applied.Targets[0].State != "applied" || applied.Targets[0].LastGood == nil {
		t.Fatal("actual coordinator ACK not reflected", applied, err)
	}
	unbind := inspect
	unbind.Action = pluginsdk.DatasetBindingUnbind
	unbind.OperationID = "remove-binding"
	unbind.ExpectedRevision = result.Revision
	removed, err := client.ManageDatasetBinding(t.Context(), unbind)
	if err != nil || removed.Targets[0].Desired != nil || removed.Targets[0].Applied == nil || removed.Targets[0].State != "pending" {
		t.Fatal("removal hid old actual delivery", removed, err)
	}
	applyConsumptionRevision(t, store, "local")
	removed, err = client.ManageDatasetBinding(t.Context(), inspect)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range removed.Targets {
		if target.Applied != nil || target.Desired != nil {
			t.Fatal("ACK retained removed delivery", target)
		}
	}
	candidate.Identity.Generation = "forged"
	if _, err := client.ManageDatasetBinding(t.Context(), pluginsdk.DatasetBindingRequest{Action: pluginsdk.DatasetBindingInspect, InstanceID: instance.ID, SourceID: source.ID, Targets: req.Targets}); err == nil {
		t.Fatal("dead/forged caller accepted")
	}
}

func TestPolicyControlFloorCASAndEntryProjection(t *testing.T) {
	manager, candidate := newPolicyConsumptionFixture(t)
	store := manager.datasets.store
	if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{testHTTPWAFRuleRow(1, "local", "")}); err != nil {
		t.Fatal(err)
	}
	client := datasetHostRuntimeClient(t, manager, &candidate)
	stage := pluginsdk.PolicyStageIdentity{Kind: "ip", PolicyID: candidate.InstanceID}
	entry := &pluginsdk.PolicyEntryTarget{NodeID: "local", Kind: pluginsdk.PolicyEntryHTTP, ID: "1"}
	inspect := pluginsdk.PolicyControlRequest{Action: pluginsdk.PolicyControlInspect, InstanceID: candidate.InstanceID, Stage: stage, Entry: entry}
	initial, err := client.ControlPolicy(t.Context(), inspect)
	if err != nil {
		t.Fatal("inspect actual entry", err)
	}
	mutate := func(action pluginsdk.PolicyControlAction, mode pluginsdk.PolicyMode, op string, at *pluginsdk.PolicyEntryTarget, version pluginsdk.PolicySettingsVersion) (pluginsdk.PolicyControlResponse, error) {
		return client.ControlPolicy(t.Context(), pluginsdk.PolicyControlRequest{Action: action, InstanceID: candidate.InstanceID, OperationID: op, Stage: stage, Entry: at, Mode: mode, ExpectedRevision: &version.Revision, ExpectedInstanceVersion: &version.InstanceVersion})
	}
	observed, err := mutate(pluginsdk.PolicyControlReplaceEntry, pluginsdk.PolicyModeObserve, "entry-observe", entry, initial.Desired.Version)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := latestWAFCoordinatorSnapshot(t, store, "local")
	if len(snapshot.Rules[0].PolicyRef.StageModes) != 1 || snapshot.Rules[0].PolicyRef.StageModes[0].Snapshot.Settings.EntryMode == nil {
		t.Fatal("trusted entry mode absent from published immutable snapshot")
	}
	if _, err := mutate(pluginsdk.PolicyControlReplaceInstance, pluginsdk.PolicyModeEnforce, "default-enforce-conflict", nil, observed.Desired.Version); err == nil {
		t.Fatal("enforce default persisted invalid observe override")
	}
	after, err := client.ControlPolicy(t.Context(), inspect)
	if err != nil || after.Desired.Version != observed.Desired.Version {
		t.Fatal("rejected transaction changed clocks", after, err)
	}
	reset, err := mutate(pluginsdk.PolicyControlResetEntry, "", "entry-reset", entry, after.Desired.Version)
	if err != nil {
		t.Fatal(err)
	}
	enforced, err := mutate(pluginsdk.PolicyControlReplaceInstance, pluginsdk.PolicyModeEnforce, "default-enforce", nil, reset.Desired.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutate(pluginsdk.PolicyControlReplaceEntry, pluginsdk.PolicyModeObserve, "entry-downgrade", entry, enforced.Desired.Version); err == nil {
		t.Fatal("entry weakened global floor")
	}
	forged := *entry
	forged.Kind = pluginsdk.PolicyEntryTCP
	inspect.Entry = &forged
	if _, err := client.ControlPolicy(t.Context(), inspect); err == nil {
		t.Fatal("same numeric ID on wrong protocol authorized")
	}
	inspect.Entry = entry
	if err := store.PutPluginInstanceConfigJSON(t.Context(), candidate.InstanceID, json.RawMessage(`{"rules":"updated"}`)); err != nil {
		t.Fatal(err)
	}
	current, err := client.ControlPolicy(t.Context(), inspect)
	if err != nil {
		t.Fatal(err)
	}
	if current.Desired.Version.InstanceVersion <= enforced.Desired.Version.InstanceVersion || current.Desired.Version.Revision != enforced.Desired.Version.Revision {
		t.Fatal("ordinary Config failed shared instance clock", current)
	}
	replay, err := mutate(pluginsdk.PolicyControlReplaceInstance, pluginsdk.PolicyModeEnforce, "default-enforce", nil, reset.Desired.Version)
	if err != nil || replay.Desired.Version != enforced.Desired.Version {
		t.Fatal("policy historical replay resolved latest state", replay, err)
	}
}

func TestConsumptionReplayDoesNotCrossDeletedInstanceIncarnation(t *testing.T) {
	manager, candidate := newPolicyConsumptionFixture(t)
	store := manager.datasets.store
	if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{testHTTPWAFRuleRow(1, "local", "")}); err != nil {
		t.Fatal(err)
	}
	auth := DatasetAuthorization{Administrator: true, Manage: true, ActorID: "admin", ResourceGroupID: "default"}
	source := pluginsdk.DatasetSource{ID: "incarnation-classes", Name: "Incarnation classes", Format: pluginsdk.DatasetFormatCIDR}
	if err := manager.datasets.PutSource(t.Context(), auth, source, DatasetRetrieval{}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(fmt.Sprintf(`{"schema":"%s","classifications":[{"name":"cn-44","kind":"region","cidrs":["192.0.2.0/24"]}]}`, datasets.CIDRSchema))
	digest, err := manager.datasets.Upload(t.Context(), auth, source.ID, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.datasets.Control(t.Context(), auth, pluginsdk.DatasetControlRequest{Action: pluginsdk.DatasetControlImport, SourceID: source.ID, Candidate: &pluginsdk.DatasetImportCandidate{Revision: "incarnation-v1", ExpectedDigest: digest, ArtifactDigest: digest}}); err != nil {
		t.Fatal(err)
	}
	versions, err := store.ListDatasetVersions(t.Context(), source.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("dataset version unavailable: %+v %v", versions, err)
	}
	client := datasetHostRuntimeClient(t, manager, &candidate)
	entry := &pluginsdk.PolicyEntryTarget{NodeID: "local", Kind: pluginsdk.PolicyEntryHTTP, ID: "1"}
	stage := pluginsdk.PolicyStageIdentity{Kind: "ip", PolicyID: candidate.InstanceID}
	initial, err := client.ControlPolicy(t.Context(), pluginsdk.PolicyControlRequest{Action: pluginsdk.PolicyControlInspect, InstanceID: candidate.InstanceID, Stage: stage, Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	policyRequest := pluginsdk.PolicyControlRequest{Action: pluginsdk.PolicyControlReplaceEntry, OperationID: "incarnation-policy-operation", InstanceID: candidate.InstanceID, Stage: stage, Entry: entry, Mode: pluginsdk.PolicyModeObserve, ExpectedRevision: &initial.Desired.Version.Revision, ExpectedInstanceVersion: &initial.Desired.Version.InstanceVersion}
	if _, err := client.ControlPolicy(t.Context(), policyRequest); err != nil {
		t.Fatal(err)
	}
	datasetRequest := pluginsdk.DatasetBindingRequest{Action: pluginsdk.DatasetBindingBind, OperationID: "incarnation-dataset-operation", InstanceID: candidate.InstanceID, SourceID: source.ID, Targets: pluginsdk.ExecutionTargetSelection{Mode: pluginsdk.ExecutionTargetsEffective}, Spec: &pluginsdk.DatasetBindingSpec{VersionDigest: versions[0].Digest, Classifications: []pluginsdk.DatasetClassification{{Name: "cn-44", Kind: pluginsdk.DatasetClassificationRegion}}}}
	if _, err := client.ManageDatasetBinding(t.Context(), datasetRequest); err != nil {
		t.Fatal(err)
	}
	oldInstance, found, err := store.GetPluginInstance(t.Context(), candidate.InstanceID)
	if err != nil || !found {
		t.Fatal("old instance unavailable", err)
	}
	oldIncarnation := oldInstance.IncarnationID
	if err := manager.plugins.DeleteInstance(t.Context(), PluginDeleteInstanceRequest{PluginID: oldInstance.PluginID, InstanceID: oldInstance.ID, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	if row, err := store.GetPluginDatasetConsumption(t.Context(), oldInstance.ID, source.ID); err != nil || row.Revision != 0 {
		t.Fatalf("instance deletion retained live dataset mutation: %+v %v", row, err)
	}
	if modes, err := store.ListPluginPolicyEntryModes(t.Context(), oldInstance.ID); err != nil || len(modes) != 0 {
		t.Fatalf("instance deletion retained live policy mutation: %+v %v", modes, err)
	}
	recreated := oldInstance
	recreated.IncarnationID = ""
	recreated.StateVersion = 0
	recreated.ConfigVersion = 1
	recreated.PendingConfigJSON, recreated.PendingTargetJSON, recreated.PendingResourceGroupID, recreated.PendingOperationID = "", "", "", ""
	recreated.PendingVersion = 0
	recreated.PendingPolicyChainsJSON, recreated.PendingBindingsJSON, recreated.PendingSecretHandlesJSON = "[]", "[]", "[]"
	recreated.RollbackConfigJSON, recreated.RollbackResourceGroupID = "", ""
	recreated.RollbackVersion = 0
	recreated.RollbackPolicyChainsJSON, recreated.RollbackBindingsJSON, recreated.RollbackSecretHandlesJSON = "[]", "[]", "[]"
	recreated.CurrentState, recreated.StatusSummaryJSON = "active", "{}"
	installed, found, err := store.GetInstalledPlugin(t.Context(), recreated.PluginID)
	if err != nil || !found {
		t.Fatal("installed plugin unavailable", err)
	}
	operation := manager.plugins.operation(t.Context(), recreated.PluginID, "configure", installed.ActivePackageDigest, "admin")
	if err := bindInstalledActiveOperation(&operation, installed); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation.InstanceID, operation.ResourceGroupID, operation.Status, operation.CompletedAt = recreated.ID, recreated.ResourceGroupID, "succeeded", &now
	installed.LastOperationID, installed.UpdatedAt = operation.ID, now
	if err := store.ApplyPluginMutation(t.Context(), storage.PluginMutation{PluginID: recreated.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstance: &recreated, Operation: operation, Audit: pluginLifecycleAudit(operation, "admin", "success", "", now)}); err != nil {
		t.Fatal(err)
	}
	if recreated.IncarnationID == "" || recreated.IncarnationID == oldIncarnation {
		t.Fatalf("delete/recreate reused incarnation: old=%q new=%q", oldIncarnation, recreated.IncarnationID)
	}
	candidate.IncarnationID = recreated.IncarnationID
	if _, err := client.ControlPolicy(t.Context(), policyRequest); err != nil {
		t.Fatal("new incarnation could not reuse policy operation ID", err)
	}
	modes, err := store.ListPluginPolicyEntryModes(t.Context(), recreated.ID)
	if err != nil || len(modes) != 1 || modes[0].EntryID != entry.ID {
		t.Fatalf("new policy operation replayed old ACK without mutation: %+v %v", modes, err)
	}
	if _, err := client.ManageDatasetBinding(t.Context(), datasetRequest); err != nil {
		t.Fatal("new incarnation could not reuse dataset operation ID", err)
	}
	binding, err := store.GetPluginDatasetConsumption(t.Context(), recreated.ID, source.ID)
	if err != nil || binding.Revision != 1 || binding.RecordJSON == "" {
		t.Fatalf("new dataset operation replayed old ACK without mutation: %+v %v", binding, err)
	}
}

func TestConsumptionOperationKeyPreservesLegacyReplayAndScopesNewIncarnations(t *testing.T) {
	base := pluginhost.Candidate{InstanceID: "instance", Identity: pluginhost.Identity{PluginID: "plugin"}}
	legacy := base
	legacy.IncarnationID = "legacy-0123456789abcdef0123456789abcdef"
	first := base
	first.IncarnationID = "instance-0123456789abcdef0123456789abcdef"
	second := base
	second.IncarnationID = "instance-fedcba9876543210fedcba9876543210"
	operationID := "same-operation"
	legacyKey := pluginHostOperationKey(base, operationID)
	if pluginHostOperationKey(legacy, operationID) != legacyKey {
		t.Fatal("legacy migration changed an existing replay key")
	}
	firstKey, secondKey := pluginHostOperationKey(first, operationID), pluginHostOperationKey(second, operationID)
	if firstKey == legacyKey || secondKey == legacyKey || firstKey == secondKey {
		t.Fatal("new instance incarnations shared a replay key")
	}
}

func TestDatasetBindingEmptyTargetsRefreshAndHistoricalReplay(t *testing.T) {
	manager, candidate := newPolicyConsumptionFixture(t, true)
	store := manager.datasets.store
	client := datasetHostRuntimeClient(t, manager, &candidate)
	auth := DatasetAuthorization{Administrator: true, Manage: true, ActorID: "admin", ResourceGroupID: "default"}
	source := pluginsdk.DatasetSource{ID: "regions", Name: "Regions", Format: pluginsdk.DatasetFormatCIDR}
	if err := manager.datasets.PutSource(t.Context(), auth, source, DatasetRetrieval{}); err != nil {
		t.Fatal(err)
	}
	prepare := func(i int) string {
		data := []byte(fmt.Sprintf(`{"schema":"%s","classifications":[{"name":"cn-44","kind":"region","cidrs":["10.%d.0.0/16"]}]}`, datasets.CIDRSchema, i))
		digest, err := manager.datasets.Upload(t.Context(), auth, source.ID, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.datasets.Control(t.Context(), auth, pluginsdk.DatasetControlRequest{Action: pluginsdk.DatasetControlImport, SourceID: source.ID, Candidate: &pluginsdk.DatasetImportCandidate{Revision: fmt.Sprintf("v%d", i), ExpectedDigest: digest, ArtifactDigest: digest}}); err != nil {
			t.Fatal(err)
		}
		versions, _ := store.ListDatasetVersions(t.Context(), source.ID)
		for _, v := range versions {
			var version pluginsdk.DatasetVersion
			_ = json.Unmarshal([]byte(v.VersionJSON), &version)
			if version.Revision == fmt.Sprintf("v%d", i) {
				return v.Digest
			}
		}
		t.Fatal("missing version")
		return ""
	}
	old := prepare(1)
	request := pluginsdk.DatasetBindingRequest{Action: pluginsdk.DatasetBindingBind, OperationID: "empty-bind", InstanceID: candidate.InstanceID, SourceID: source.ID, Targets: pluginsdk.ExecutionTargetSelection{Mode: pluginsdk.ExecutionTargetsEffective}, Spec: &pluginsdk.DatasetBindingSpec{VersionDigest: old, Classifications: []pluginsdk.DatasetClassification{{Kind: pluginsdk.DatasetClassificationRegion, Name: "cn-44"}}}}
	original, err := client.ManageDatasetBinding(t.Context(), request)
	if err != nil || len(original.Targets) != 0 {
		t.Fatal("empty RPC binding", original, err)
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "local")
	if err != nil || found && pointer.DesiredRevision != 0 {
		t.Fatal("empty RPC invented revision", pointer, err)
	}
	latest := prepare(2)
	row, _ := store.GetDatasetSource(t.Context(), source.ID)
	if err := manager.datasets.activate(t.Context(), row, latest, "auto-refresh"); err != nil {
		t.Fatal(err)
	}
	inspect := request
	inspect.Action = pluginsdk.DatasetBindingInspect
	inspect.OperationID = ""
	inspect.Spec = nil
	refreshed, err := client.ManageDatasetBinding(t.Context(), inspect)
	if err != nil || refreshed.Revision != original.Revision+1 || refreshed.Desired.Spec.VersionDigest != latest {
		t.Fatal("logical auto-refresh", refreshed, err)
	}
	replay, err := client.ManageDatasetBinding(t.Context(), request)
	if err != nil || replay.Desired.Spec.VersionDigest != old || replay.Revision != original.Revision {
		t.Fatal("refresh changed original ack", replay, err)
	}
	unbind := inspect
	unbind.Action = pluginsdk.DatasetBindingUnbind
	unbind.OperationID = "empty-unbind"
	unbind.ExpectedRevision = refreshed.Revision
	if _, err := client.ManageDatasetBinding(t.Context(), unbind); err != nil {
		t.Fatal(err)
	}
	prepare(3)
	prepare(4)
	if err := store.DeleteDatasetVersion(t.Context(), source.ID, old); err != nil {
		t.Fatal("unbound old version", err)
	}
	replay, err = client.ManageDatasetBinding(t.Context(), request)
	if err != nil || replay.Revision != original.Revision {
		t.Fatal("missing historical data broke durable ack", replay, err)
	}
}

func applyConsumptionRevision(t *testing.T, store *storage.GormStore, agent string) {
	t.Helper()
	coord, err := coordinator.New(store, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := coord.Claim(t.Context(), agent)
	if err != nil || claimed.Lease == nil {
		t.Fatal("actual revision claim", claimed, err)
	}
	generation := fmt.Sprintf("generation-%d-%s", claimed.Lease.Revision, claimed.Lease.SnapshotDigest[:16])
	if _, err := coord.Start(t.Context(), coordinator.StartRequest{Lease: *claimed.Lease, GenerationID: generation, RuntimeGenerationID: generation, RuntimeSnapshotHash: claimed.Lease.SnapshotDigest}); err != nil {
		t.Fatal(err)
	}
	if _, err := coord.Applied(t.Context(), coordinator.AppliedReport{Lease: *claimed.Lease, GenerationID: generation}); err != nil {
		t.Fatal(err)
	}
}

func TestComposedRuleReferenceUsesExplicitOverlayFormat(t *testing.T) {
	trusted := &storage.PolicyRef{ID: "waf-chain", OverlayFormat: pluginsdk.PolicyOverlayFormatEnvelopeV1, Overlay: json.RawMessage(`{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","policy_id":"waf","payload":{"mode":"deny"}}]}`)}
	ref, err := normalizeRulePolicyRef(nil, trusted)
	if err != nil {
		t.Fatal(err)
	}
	catalog := staticWAFPolicyCatalogStore{policies: []storage.PluginPolicy{{ID: ref.ID, Stages: []storage.PolicyStage{{Kind: "waf", PolicyID: "waf", ExtensionPoints: []string{policyExtensionHTTP}, ResourceBudget: storage.PolicyResourceBudget{InputBytes: 65536}}}}}}
	if err := validateRulePolicyReference(t.Context(), catalog, "local", ref, policyExtensionHTTP); err != nil {
		t.Fatal("explicit envelope mistaken for legacy WAF config", err)
	}
	if _, err := normalizeRulePolicyRef(trusted, nil); err == nil {
		t.Fatal("untrusted request supplied Host overlay metadata")
	}
	forged, err := normalizeRulePolicyRef(&storage.PolicyRef{ID: "waf-chain", Overlay: trusted.Overlay}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRulePolicyReference(t.Context(), catalog, "local", forged, policyExtensionHTTP); err == nil {
		t.Fatal("caller-selected envelope bypassed legacy WAF validation")
	}
	legacy, err := normalizeRulePolicyRef(&storage.PolicyRef{ID: "waf-chain", Overlay: json.RawMessage(`{"mode":"deny"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRulePolicyReference(t.Context(), catalog, "local", legacy, policyExtensionHTTP); err != nil {
		t.Fatal("legal legacy WAF overlay rejected", err)
	}
	if legacy.OverlayFormat != pluginsdk.PolicyOverlayFormatLegacyWAF || legacy.LegacyPolicyID != "waf" {
		t.Fatalf("trusted catalog provenance was not persisted: %+v", legacy)
	}
	badPayload := &storage.PolicyRef{ID: "waf-chain", OverlayFormat: pluginsdk.PolicyOverlayFormatEnvelopeV1, Overlay: json.RawMessage(`{"schema":"nre.policy-overlay/v1","stages":[{"kind":"waf","policy_id":"waf","payload":{"unexpected":true}}]}`)}
	if err := validateRulePolicyReference(t.Context(), catalog, "local", badPayload, policyExtensionHTTP); err == nil {
		t.Fatal("official WAF envelope skipped selected payload validation")
	}
}

func TestPolicyConsumptionRetainsRequiredVaultConfig(t *testing.T) {
	manager, candidate := newPolicyConsumptionFixture(t)
	store := manager.datasets.store
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	manager.plugins.SetSecretVault(vault)
	raw := `"retained-secret-value"`
	purpose := "plugin-config:ip-default:/password"
	metadata, err := vault.Create(t.Context(), secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "fixture-password", purpose, raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(raw))
	handles, _ := json.Marshal([]storage.PluginInstanceSecretHandle{{Pointer: "/password", ID: metadata.ID, Version: metadata.ActiveVersion, Purpose: purpose, Digest: hex.EncodeToString(digest[:])}})
	instance, _, _ := store.GetPluginInstance(t.Context(), candidate.InstanceID)
	instance.SecretHandlesJSON = string(handles)
	owner := consumptionOwner{instance: instance, packageRow: storage.PluginPackageRow{ConfigSchemaJSON: `{"type":"object","properties":{"password":{"type":"string","writeOnly":true},"rules":{"type":"string"}},"required":["password","rules"]}`}}
	err = store.SecurityTransaction(t.Context(), func(tx *storage.GormStore) error {
		public, err := manager.consumptionConfig(t.Context(), tx, owner, json.RawMessage(`{"rules":"next"}`))
		if err != nil {
			return err
		}
		if string(public) != `{"rules":"next"}` {
			t.Fatal("material escaped into public config", string(public))
		}
		return nil
	})
	if err != nil {
		t.Fatal("required retained secret rejected", err)
	}
}
