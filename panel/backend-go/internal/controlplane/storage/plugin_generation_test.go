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
)

func TestRevisionLedgerMaterializesAndAuthorizesPluginRuntimeArtifact(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
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
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	packageDigest := strings.Repeat("a", 64)
	artifactDigest := strings.Repeat("b", 64)
	manifest := plugins.Manifest{
		SchemaVersion: 1, ID: "runtime.rpc", Version: "1.2.3", Name: "Runtime RPC",
		Runtime:         plugins.Runtime{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "agent", Entry: "artifacts/linux-amd64/plugin"},
		Artifacts:       []plugins.Artifact{{Path: "artifacts/linux-amd64/plugin", SHA256: artifactDigest, Size: 42, Mode: "executable", GOOS: "linux", GOARCH: "amd64"}},
		ExtensionPoints: []string{"relay.manage", "relay.manage"},
		ResourceBudget:  plugins.ResourceBudget{TimeoutMS: 100, MemoryBytes: 4096, Concurrency: 1, InputBytes: 1024, OutputBytes: 512, CPUMillis: 10, Restarts: 2},
		FailurePolicy:   plugins.FailurePolicy{OnError: "preserve-old", OnBudget: "preserve-old", Restart: "bounded", CoreFallback: "continue"},
		Signature:       plugins.Signature{Algorithm: "ed25519", KeyID: "release", File: "package.sig"},
	}
	manifestJSON, _ := json.Marshal(manifest)
	packageRow, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: packageDigest, PluginID: manifest.ID, Version: manifest.Version, SignatureFingerprint: strings.Repeat("c", 64), CachePath: `C:\private\cache`, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{
		&packageRow,
		&artifacts,
		&InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: packageDigest, ActivePackageIdentity: packageRow.Identity, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, DesiredLifecycle: "enabled", CurrentLifecycle: "active", CleanupPolicyJSON: `{}`, LastOperationID: "operation-active", StateVersion: 1, InstalledAt: now, UpdatedAt: now},
		&PluginInstanceRow{ID: "instance-rpc", PluginID: manifest.ID, ResourceGroupID: "group-a", TargetJSON: `["local"]`, PolicyChainsJSON: `[]`, SecretHandlesJSON: `[{"id":"secret-1","version":2,"digest":"` + strings.Repeat("d", 64) + `","purpose":"token"}]`, BindingsJSON: `[]`, ConfigJSON: `{"enabled":true}`, ConfigVersion: 7, DesiredEnabled: true, CurrentState: "active", StatusSummaryJSON: `{}`, StateVersion: 1, UpdatedAt: now},
		&PluginGrantRow{ID: "grant-rpc", PluginID: manifest.ID, PackageDigest: packageDigest, PackageIdentity: packageRow.Identity, Permission: "relay.manage", ResourceSelector: "relay:public", GrantedBy: "admin", GrantedAt: now},
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
	if len(generations) != 1 {
		t.Fatalf("generations = %+v", generations)
	}
	generation := generations[0]
	if generation.PluginID != manifest.ID || generation.OperationID != "operation-active" || generation.Artifact.PackageIdentity != packageRow.Identity || !generation.Artifact.SignatureVerified || generation.Target.ID != "local" || generation.Target.Version != 7 || len(generation.SecretHandles) != 1 || generation.Grants[0].ResourceKind != "relay" {
		t.Fatalf("generation projection = %+v", generation)
	}
	encoded, _ := json.Marshal(generation)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "cache_path") || generation.Artifact.LocalPath != "" {
		t.Fatalf("generation leaked local package location: %s", encoded)
	}
}

func TestLoadAgentPluginGenerationsSkipsNonTargetBeforePackageResolution(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
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
	store, err := NewSQLiteStore(t.TempDir(), "local")
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
	if err != nil || replayed || updated.State != "active" {
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
}
