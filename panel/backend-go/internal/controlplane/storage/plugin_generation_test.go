package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
)

func TestRevisionLedgerMaterializesAndAuthorizesPluginRuntimeArtifact(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	payload := []byte("signed rpc runtime")
	artifactSum := sha256.Sum256(payload)
	artifactDigest := hex.EncodeToString(artifactSum[:])
	packageDigest := strings.Repeat("a", 64)
	signerFingerprint := strings.Repeat("c", 64)
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: "runtime.ledger", Version: "1.0.0", Name: "Runtime Ledger",
		Runtime:         plugins.Runtime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "artifacts/linux-amd64/plugin"},
		Artifacts:       []plugins.Artifact{{Path: "artifacts/linux-amd64/plugin", SHA256: artifactDigest, Size: int64(len(payload)), Mode: "executable", GOOS: "linux", GOARCH: "amd64"}},
		ExtensionPoints: []string{"relay.manage"}, ResourceBudget: plugins.ResourceBudget{TimeoutMS: 100, MemoryBytes: 4096, Concurrency: 1, InputBytes: 1024, OutputBytes: 512},
		FailurePolicy: plugins.FailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
		Signature:     plugins.Signature{Algorithm: "ed25519", KeyID: "release", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	cacheRoot := t.TempDir()
	artifactPath := filepath.Join(cacheRoot, filepath.FromSlash(manifest.Runtime.Entry))
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: packageDigest, PluginID: manifest.ID, Version: manifest.Version, SignatureFingerprint: signerFingerprint, SignaturePublicKey: strings.Repeat("d", 64), CachePath: cacheRoot, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&artifacts).Error; err != nil {
		t.Fatal(err)
	}
	generation := PluginGeneration{
		ID: strings.Repeat("e", 64), InstanceID: "instance-ledger", OperationID: "operation-ledger", Revision: 7,
		PluginID: manifest.ID, PluginVersion: manifest.Version, PackageDigest: packageDigest,
		Runtime:  PluginGenerationRuntime{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, Entry: manifest.Runtime.Entry},
		Artifact: PluginGenerationArtifact{ArtifactID: artifacts[0].ID, PackageIdentity: packageRow.Identity, RelativePath: artifacts[0].Path, SHA256: artifactDigest, SizeBytes: int64(len(payload)), Mode: artifacts[0].Mode, GOOS: artifacts[0].GOOS, GOARCH: artifacts[0].GOARCH, SignatureVerified: true, SignerKeyID: packageRow.SignatureKeyID, SignerFingerprint: signerFingerprint},
	}
	snapshot := Snapshot{Revision: 7, PluginGenerations: []PluginGeneration{generation}, PluginPolicies: []PluginPolicy{}}
	blobs, refs, err := store.BuildAgentRevisionPolicyArtifacts(t.Context(), "local", 7, snapshot, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) != 1 || blobs[0].Kind != revisionRuntimeArtifactKind || string(blobs[0].Payload) != string(payload) || len(refs) != 1 {
		t.Fatalf("runtime revision artifacts = %+v refs=%+v", blobs, refs)
	}
	snapshotPayload, _ := json.Marshal(snapshot)
	snapshotSum := sha256.Sum256(snapshotPayload)
	snapshotDigest := hex.EncodeToString(snapshotSum[:])
	if _, err := store.EnsureAgentHeartbeatRevision(t.Context(), "local", snapshot, snapshotPayload, snapshotDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := store.ResolveAgentRevisionPolicyArtifact(t.Context(), "local", 7, snapshotDigest, artifacts[0].ID)
	if err != nil || !found || string(resolved.Payload) != string(payload) {
		t.Fatalf("resolved runtime artifact = found=%v row=%+v err=%v", found, resolved, err)
	}
}

func TestLoadAgentPluginGenerationsProjectsOnlyTargetRPCContract(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	packageDigest := strings.Repeat("a", 64)
	artifactDigest := strings.Repeat("b", 64)
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: "runtime.rpc", Version: "1.2.3", Name: "Runtime RPC",
		Runtime:              plugins.Runtime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "artifacts/linux-amd64/plugin"},
		Artifacts:            []plugins.Artifact{{Path: "artifacts/linux-amd64/plugin", SHA256: artifactDigest, Size: 42, Mode: "executable", GOOS: "linux", GOARCH: "amd64"}},
		ExtensionPoints:      []string{pluginsdk.ExtensionHTTPBackendProvider},
		HTTPBackendProviders: []pluginsdk.HTTPBackendProviderDescriptor{{ID: "default", DisplayName: "Default"}},
		Permissions:          []plugins.Permission{{Name: pluginsdk.PermissionHTTPOutbound}},
		ResourceBudget:       plugins.ResourceBudget{TimeoutMS: 100, MemoryBytes: 4096, Concurrency: 1, InputBytes: 1024, OutputBytes: 512, CPUMillis: 10, Restarts: 2},
		FailurePolicy:        plugins.FailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
		Signature:            plugins.Signature{Algorithm: "ed25519", KeyID: "release", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: packageDigest, PluginID: manifest.ID, Version: manifest.Version, SignatureFingerprint: strings.Repeat("c", 64), CachePath: `C:\private\cache`, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	consumerOwner := ResourceBindingRow{ID: "http-owner", ResourceKind: "http_rule", ResourceID: "local:1", ResourceGroupID: "group-a", ParentResourceKind: "agent", ParentResourceID: "local", UpdatedAt: now}
	bindingsJSON := `[]`
	rows := []any{
		&packageRow,
		&artifacts,
		&InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: packageDigest, ActivePackageIdentity: packageRow.Identity, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "operation-active", StateVersion: 1, InstalledAt: now, UpdatedAt: now},
		&PluginInstanceRow{ID: "instance-rpc", PluginID: manifest.ID, ResourceGroupID: "group-a", TargetJSON: `["local"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[{"id":"secret-1","version":2,"digest":"` + strings.Repeat("d", 64) + `","purpose":"token"}]`, BindingsJSON: bindingsJSON, ConfigJSON: `{"enabled":true}`, ConfigVersion: 7, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now},
		&PluginInstanceRow{ID: "instance-optional", PluginID: manifest.ID, ResourceGroupID: "group-a", TargetJSON: `["local"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{"enabled":true}`, ConfigVersion: 8, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now},
		&PluginGrantRow{ID: "grant-rpc", GrantKey: "grant-rpc", PluginID: manifest.ID, PackageDigest: packageDigest, PackageIdentity: packageRow.Identity, Permission: "relay.manage", ResourceSelector: "relay:public", GrantedBy: "admin", GrantedAt: now},
		&PluginGrantRow{ID: "grant-http", GrantKey: "grant-http", PluginID: manifest.ID, PackageDigest: packageDigest, PackageIdentity: packageRow.Identity, Permission: pluginsdk.PermissionHTTPOutbound, GrantedBy: "admin", GrantedAt: now},
		&HTTPRuleRow{ID: 1, AgentID: "local", FrontendURL: "https://plugin.example.test", BackendsJSON: `[{"kind":"plugin_provider","plugin_provider":{"instance_id":"instance-rpc","provider_id":"default"}}]`, Enabled: true, Revision: 3},
		&consumerOwner,
	}
	for _, row := range rows {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	generations, err := store.LoadAgentPluginGenerations(t.Context(), "local", "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if len(generations) != 2 {
		t.Fatalf("generations = %+v", generations)
	}
	generation := generations[1]
	if generation.PluginID != manifest.ID || generation.OperationID != "operation-active" || generation.Artifact.PackageIdentity != packageRow.Identity || !generation.Artifact.SignatureVerified || generation.Target.ID != "local" || generation.Target.Version != 7 || len(generation.SecretHandles) != 1 || !pluginGenerationContainsString(generation.RequiredFeatures, pluginsdk.RPCFeatureHTTPBackendProviderV1) {
		t.Fatalf("generation projection = %+v", generation)
	}
	encoded, _ := json.Marshal(generation)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "cache_path") || generation.Artifact.LocalPath != "" {
		t.Fatalf("generation leaked local package location: %s", encoded)
	}
	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PluginGenerations) != 2 || len(snapshot.PluginDependencies) != 1 {
		t.Fatalf("production plugin projection generations=%+v dependencies=%+v", snapshot.PluginGenerations, snapshot.PluginDependencies)
	}
	edge := snapshot.PluginDependencies[0]
	if edge.Consumer.Kind != PluginDependencyConsumerHTTPRule || edge.Consumer.ID != "1" || edge.Consumer.ResourceGroupID != "group-a" || !ValidPluginDependencyConsumerVersion(edge.Consumer.Version) || edge.ProviderInstanceID != "instance-rpc" || edge.Target.AgentID != "local" || edge.Target.ResourceGroupID != "group-a" || edge.Target.Version != 7 {
		t.Fatalf("production plugin dependency = %+v", edge)
	}
	if err := store.db.Model(&HTTPRuleRow{}).Where("agent_id = ? AND id = ?", "local", 1).Update("backends", `[{"url":"http://127.0.0.1:8096"}]`).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PluginGenerations) != 2 || len(snapshot.PluginDependencies) != 0 {
		t.Fatalf("URL-switched core consumer projection generations=%+v dependencies=%+v", snapshot.PluginGenerations, snapshot.PluginDependencies)
	}
	if err := store.db.Model(&HTTPRuleRow{}).Where("agent_id = ? AND id = ?", "local", 1).Update("backends", `[{"kind":"plugin_provider","plugin_provider":{"instance_id":"instance-rpc","provider_id":"default"}}]`).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil || len(snapshot.PluginDependencies) != 1 {
		t.Fatalf("restored provider dependency=%+v err=%v", snapshot.PluginDependencies, err)
	}
	if err := store.db.Where("agent_id = ? AND id = ?", "local", 1).Delete(&HTTPRuleRow{}).Error; err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{Platform: "linux-amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PluginDependencies) != 0 {
		t.Fatalf("deleted core consumer dependencies=%+v", snapshot.PluginDependencies)
	}
}

func TestLoadAgentPluginGenerationsSkipsNonTargetBeforePackageResolution(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.db.Create(&InstalledPluginRow{
		PluginID: "windows.only", ActivePackageDigest: strings.Repeat("a", 64), ActivePackageIdentity: "missing@1",
		DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "operation", StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&PluginInstanceRow{
		ID: "windows-instance", PluginID: "windows.only", ResourceGroupID: "group", TargetJSON: `["windows-agent"]`,
		PolicyChainsJSON: `[]`, SecretHandlesJSON: `[]`, BindingsJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 1,
		DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	generations, err := store.LoadAgentPluginGenerations(t.Context(), "linux-agent", "linux-amd64")
	if err != nil {
		t.Fatalf("non-targeted missing platform package blocked snapshot: %v", err)
	}
	if len(generations) != 0 {
		t.Fatalf("non-targeted generations = %+v", generations)
	}
}

func TestPluginAgentRuntimeReportFencesReplayAndStaleIdentity(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.AutoMigrate(&PluginAgentRuntimeStatusRow{}); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("e", 64)
	row := PluginAgentRuntimeStatusRow{OperationID: "operation", AgentID: "edge", InstanceID: "instance", PluginID: "plugin", Revision: 9, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: 3}
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []PluginAgentRuntimeStatusRow{row}); err != nil {
		t.Fatal(err)
	}
	report := PluginGenerationReport{OperationID: row.OperationID, AgentID: row.AgentID, InstanceID: row.InstanceID, PluginID: row.PluginID, Revision: row.Revision, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, State: "active", Sequence: 1, SafeDetail: "runtime ready", Details: json.RawMessage(`{"ready":true}`), ReportedAt: time.Now().UTC()}
	updated, replayed, err := store.RecordPluginAgentRuntimeReport(t.Context(), report)
	if err != nil || replayed || updated.State != "active" || updated.AuthoritySlot != "pending" {
		t.Fatalf("first report = %+v replay=%v err=%v", updated, replayed, err)
	}
	if !strings.Contains(updated.DetailsJSON, `"safe_detail":"runtime ready"`) {
		t.Fatalf("runtime safe detail was not persisted: %s", updated.DetailsJSON)
	}
	if _, replayed, err := store.RecordPluginAgentRuntimeReport(t.Context(), report); err != nil || !replayed {
		t.Fatalf("identical replay = %v, %v", replayed, err)
	}
	crossChannelReplay := report
	crossChannelReplay.ReportedAt = report.ReportedAt.Add(time.Minute)
	crossChannelReplay.SafeDetail = ""
	crossChannelReplay.Details = json.RawMessage(`{"ready":true,"safe_detail":"runtime ready"}`)
	if _, replayed, err := store.RecordPluginAgentRuntimeReport(t.Context(), crossChannelReplay); err != nil || !replayed {
		t.Fatalf("cross-channel replay = %v, %v", replayed, err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return completePluginAgentRuntimeAuthorityTx(tx, PluginOperationRow{ID: row.OperationID, Status: "succeeded"})
	}); err != nil {
		t.Fatal(err)
	}
	postAck := report
	postAck.Sequence = 2
	postAck.State = "degraded"
	postAck.ErrorCode = "rpc_runtime_failed"
	postAck.SafeDetail = "restart backoff"
	updated, replayed, err = store.RecordPluginAgentRuntimeReport(t.Context(), postAck)
	if err != nil || replayed || updated.State != "degraded" || updated.ReportSequence != 2 || !strings.Contains(updated.DetailsJSON, "restart backoff") {
		t.Fatalf("post-ack runtime report = %+v replay=%v err=%v", updated, replayed, err)
	}
	stale := report
	stale.GenerationID = strings.Repeat("f", 64)
	stale.Sequence = 3
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), stale); !errors.Is(err, ErrPluginGenerationStale) {
		t.Fatalf("stale report error = %v", err)
	}
	nextDigest := strings.Repeat("a", 64)
	next := row
	next.OperationID, next.Revision, next.GenerationID = "operation-next", 10, nextDigest
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []PluginAgentRuntimeStatusRow{next}); err != nil {
		t.Fatal(err)
	}
	previous, found, err := store.GetPluginAgentRuntimeStatusFence(t.Context(), row.OperationID, row.AgentID, row.InstanceID)
	if err != nil || !found || previous.AuthoritySlot != "active" {
		t.Fatalf("active authority during prepare = %+v found=%v err=%v", previous, found, err)
	}
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), PluginGenerationReport{OperationID: next.OperationID, AgentID: next.AgentID, InstanceID: next.InstanceID, PluginID: next.PluginID, Revision: next.Revision, GenerationID: next.GenerationID, PackageDigest: next.PackageDigest, ArtifactDigest: next.ArtifactDigest, State: "active", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	previous, found, err = store.GetPluginAgentRuntimeStatusFence(t.Context(), row.OperationID, row.AgentID, row.InstanceID)
	if err != nil || !found || previous.AuthoritySlot != "active" {
		t.Fatalf("active authority was cut over before completion = %+v found=%v err=%v", previous, found, err)
	}
	nextPending, found, err := store.GetPluginAgentRuntimeStatusFence(t.Context(), next.OperationID, next.AgentID, next.InstanceID)
	if err != nil || !found || nextPending.AuthoritySlot != "pending" || nextPending.State != "active" {
		t.Fatalf("ready candidate authority = %+v found=%v err=%v", nextPending, found, err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return completePluginAgentRuntimeAuthorityTx(tx, PluginOperationRow{ID: next.OperationID, Status: "succeeded"})
	}); err != nil {
		t.Fatal(err)
	}
	previous, found, err = store.GetPluginAgentRuntimeStatusFence(t.Context(), row.OperationID, row.AgentID, row.InstanceID)
	if err != nil || !found || previous.AuthoritySlot != "retired" {
		t.Fatalf("superseded authority after completion = %+v found=%v err=%v", previous, found, err)
	}
	postSupersede := postAck
	postSupersede.Sequence = 3
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), postSupersede); !errors.Is(err, ErrPluginGenerationStale) {
		t.Fatalf("superseded report error = %v", err)
	}
	failed := next
	failed.OperationID, failed.Revision, failed.GenerationID = "operation-failed", 11, strings.Repeat("b", 64)
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []PluginAgentRuntimeStatusRow{failed}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), PluginGenerationReport{OperationID: failed.OperationID, AgentID: failed.AgentID, InstanceID: failed.InstanceID, PluginID: failed.PluginID, Revision: failed.Revision, GenerationID: failed.GenerationID, PackageDigest: failed.PackageDigest, ArtifactDigest: failed.ArtifactDigest, State: "active", Sequence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return completePluginAgentRuntimeAuthorityTx(tx, PluginOperationRow{ID: failed.OperationID, Status: "failed"})
	}); err != nil {
		t.Fatal(err)
	}
	failedStatus, found, err := store.GetPluginAgentRuntimeStatusFence(t.Context(), failed.OperationID, failed.AgentID, failed.InstanceID)
	if err != nil || !found || failedStatus.AuthoritySlot != "retired" {
		t.Fatalf("failed candidate authority = %+v found=%v err=%v", failedStatus, found, err)
	}
	current, found, err := store.GetPluginAgentRuntimeStatusFence(t.Context(), next.OperationID, next.AgentID, next.InstanceID)
	if err != nil || !found || current.AuthoritySlot != "active" {
		t.Fatalf("failed candidate displaced active authority = %+v found=%v err=%v", current, found, err)
	}
}

func TestPluginAgentRuntimeReportLogsKeepImmutableRebindAuthority(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := strings.Repeat("d", 64)
	status := PluginAgentRuntimeStatusRow{
		OperationID: "configure-rebind", AgentID: "edge-a", InstanceID: "instance-a", PluginID: "plugin-a",
		Revision: 12, GenerationID: digest, PackageDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64),
		ConfigVersion: 4, ResourceGroupID: "group-before-rebind", TargetVersion: 4,
	}
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []PluginAgentRuntimeStatusRow{status}); err != nil {
		t.Fatal(err)
	}
	// The mutable instance has already been rebound while the staged runtime
	// report remains owned by its immutable operation/generation authority.
	instance := PluginInstanceRow{
		ID: status.InstanceID, PluginID: status.PluginID, ResourceGroupID: "group-after-rebind", TargetJSON: `["edge-a"]`,
		PolicyChainsJSON: `[]`, BindingsJSON: `[]`, SecretHandlesJSON: `[]`, ConfigJSON: `{}`, ConfigVersion: 5,
		DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 2, UpdatedAt: now,
	}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	report := PluginGenerationReport{
		OperationID: status.OperationID, AgentID: status.AgentID, InstanceID: status.InstanceID, PluginID: status.PluginID,
		Revision: status.Revision, GenerationID: status.GenerationID, PackageDigest: status.PackageDigest, ArtifactDigest: status.ArtifactDigest,
		State: "active", Sequence: 1, SafeDetail: "candidate ready", ReportedAt: now,
	}
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: status.InstanceID, ResourceGroupID: status.ResourceGroupID})
	if err != nil || len(pending.Rows) != 1 {
		t.Fatalf("pending-rebind logs = %+v err=%v", pending, err)
	}
	row := pending.Rows[0]
	if row.ResourceGroupID != status.ResourceGroupID || row.OperationID != status.OperationID || row.GenerationID != status.GenerationID || row.Revision != status.Revision || row.PackageDigest != status.PackageDigest || row.ArtifactDigest != status.ArtifactDigest {
		t.Fatalf("pending-rebind log authority = %+v", row)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return completePluginAgentRuntimeAuthorityTx(tx, PluginOperationRow{ID: status.OperationID, Status: "succeeded"})
	}); err != nil {
		t.Fatal(err)
	}
	report.Sequence = 2
	report.State = "degraded"
	report.SafeDetail = "bounded restart"
	if _, _, err := store.RecordPluginAgentRuntimeReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	completed, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: status.InstanceID, ResourceGroupID: status.ResourceGroupID})
	if err != nil || len(completed.Rows) != 2 {
		t.Fatalf("completed-rebind logs = %+v err=%v", completed, err)
	}
	foreign, err := store.ListPluginRuntimeLogs(t.Context(), PluginRuntimeLogQuery{InstanceID: status.InstanceID, ResourceGroupID: instance.ResourceGroupID})
	if err != nil || len(foreign.Rows) != 0 {
		t.Fatalf("logs leaked into mutable instance group: %+v err=%v", foreign, err)
	}
}
