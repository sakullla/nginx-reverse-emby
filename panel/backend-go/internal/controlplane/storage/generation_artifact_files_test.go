package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestRuntimeRevisionArtifactUsesExternalContentAddressedFile(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	payload := []byte("immutable runtime binary")
	artifactDigest := generationArtifactTestDigest(payload)
	packageDigest := strings.Repeat("b", 64)
	packageIdentity := strings.Repeat("c", 64)
	signerFingerprint := strings.Repeat("d", 64)
	cachePath := filepath.Join(store.dataRoot, "plugins", "packages", "official", packageDigest)
	artifactRelativePath := "artifacts/linux-amd64/plugin"
	packageArtifactPath := filepath.Join(cachePath, filepath.FromSlash(artifactRelativePath))
	if err := os.MkdirAll(filepath.Dir(packageArtifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packageArtifactPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestJSON, err := json.Marshal(pluginsdk.Manifest{Runtime: pluginsdk.Runtime{
		Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: pluginsdk.HostScopeAgent, Entry: "plugin",
	}})
	if err != nil {
		t.Fatal(err)
	}
	packageRow := PluginPackageRow{
		Identity: packageIdentity, Digest: packageDigest, PluginID: "runtime-plugin", Version: "1.0.0",
		RuntimeKind: "rpc-service", RuntimeABI: "nre:rpc/v1", HostScope: pluginsdk.HostScopeAgent,
		SignatureKeyID: "key-1", SignatureFingerprint: signerFingerprint, SignatureVerdict: "verified",
		CachePath: cachePath, ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC(),
	}
	artifactRow := PluginArtifactRow{
		ID: "runtime-artifact", PackageIdentity: packageIdentity, PackageDigest: packageDigest,
		Path: artifactRelativePath, SHA256: artifactDigest, SizeBytes: int64(len(payload)), Mode: "readonly",
		RuntimeKind: "rpc-service", RuntimeABI: "nre:rpc/v1", HostScope: pluginsdk.HostScopeAgent,
	}
	if err := store.db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&artifactRow).Error; err != nil {
		t.Fatal(err)
	}

	revision := int64(9)
	generation := PluginGeneration{
		InstanceID: "instance-1", OperationID: "operation-1", Revision: revision,
		PluginID: packageRow.PluginID, PluginVersion: packageRow.Version, PackageDigest: packageDigest,
		Runtime: PluginGenerationRuntime{Kind: packageRow.RuntimeKind, ABI: packageRow.RuntimeABI, HostScope: packageRow.HostScope, Entry: "plugin"},
		Artifact: PluginGenerationArtifact{
			ArtifactID: artifactRow.ID, PackageIdentity: packageIdentity, RelativePath: artifactRelativePath,
			SHA256: artifactDigest, SizeBytes: int64(len(payload)), Mode: "readonly",
			SignatureVerified: true, SignerKeyID: packageRow.SignatureKeyID, SignerFingerprint: signerFingerprint,
		},
		Config: json.RawMessage(`{}`),
		Target: PluginGenerationTarget{Kind: "agent", ID: "edge-a", ResourceGroupID: "default", Version: 1},
	}
	generation.ID, err = PluginGenerationIdentity(generation)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Revision: revision, PluginGenerations: []PluginGeneration{generation}}
	blobs, refs, err := store.BuildAgentRevisionPolicyArtifacts(t.Context(), "edge-a", revision, snapshot, time.Now().UTC())
	if err != nil || len(blobs) != 1 || len(refs) != 1 {
		t.Fatalf("build runtime revision artifacts = %d blobs, %d refs, %v", len(blobs), len(refs), err)
	}
	if len(blobs[0].Payload) != 0 || blobs[0].ExternalPath == "" {
		t.Fatalf("runtime artifact remained inline: %+v", blobs[0])
	}
	externalPath, err := generationArtifactFilePath(store.dataRoot, blobs[0].ExternalPath, artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	if externalPayload, err := os.ReadFile(externalPath); err != nil || string(externalPayload) != string(payload) {
		t.Fatalf("external runtime artifact = %q, %v", externalPayload, err)
	}

	snapshotPayload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDigest := generationArtifactTestDigest(snapshotPayload)
	if _, err := store.EnsureAgentHeartbeatRevision(t.Context(), "edge-a", snapshot, snapshotPayload, snapshotDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var persisted GenerationArtifactRow
	if err := store.db.Where("id = ?", blobs[0].ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if len(persisted.Payload) != 0 || persisted.ExternalPath == "" {
		t.Fatalf("persisted runtime artifact remained inline: %+v", persisted)
	}

	if err := os.RemoveAll(cachePath); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := store.ResolveAgentRevisionPolicyArtifact(t.Context(), "edge-a", revision, snapshotDigest, artifactRow.ID)
	if err != nil || !found || string(resolved.Payload) != string(payload) {
		t.Fatalf("resolve external runtime artifact = found %t, payload %q, %v", found, resolved.Payload, err)
	}
}

func TestExternalizeRuntimeArtifactsMigratesLegacyBlobAndPrunesOrphan(t *testing.T) {
	store, err := newStorageTestSQLiteStoreForAllTiers(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	payload := []byte("legacy database runtime blob")
	digest := generationArtifactTestDigest(payload)
	legacy := GenerationArtifactRow{
		ID: revisionRuntimeArtifactBlobID(digest), Kind: revisionRuntimeArtifactKind,
		SHA256: digest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: time.Now().Add(-48 * time.Hour).UTC(),
	}
	if err := store.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	externalized, err := store.ExternalizeRuntimeArtifacts(t.Context())
	if err != nil || externalized != 1 {
		t.Fatalf("externalized = %d, %v", externalized, err)
	}
	var persisted GenerationArtifactRow
	if err := store.db.Where("id = ?", legacy.ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if len(persisted.Payload) != 0 || persisted.ExternalPath == "" {
		t.Fatalf("legacy blob was not externalized: %+v", persisted)
	}
	if pending, err := store.runtimeArtifactCompactionPending(t.Context()); err != nil || !pending {
		t.Fatalf("compaction pending = %t, %v", pending, err)
	}
	if err := store.compactExternalizedSQLite(t.Context()); err != nil {
		t.Fatalf("compact externalized SQLite: %v", err)
	}
	if err := store.markRuntimeArtifactCompactionComplete(t.Context()); err != nil {
		t.Fatalf("mark externalized SQLite compaction: %v", err)
	}
	if pending, err := store.runtimeArtifactCompactionPending(t.Context()); err != nil || pending {
		t.Fatalf("compaction pending after marker = %t, %v", pending, err)
	}
	materialized, found, err := store.GetGenerationArtifact(t.Context(), legacy.ID)
	if err != nil || !found || string(materialized.Payload) != string(payload) {
		t.Fatalf("materialized legacy blob = found %t, payload %q, %v", found, materialized.Payload, err)
	}
	path, err := generationArtifactFilePath(store.dataRoot, persisted.ExternalPath, digest)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Delete(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := store.PruneGenerationArtifactFiles(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("pruned external artifacts = %d, %v", deleted, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("orphan runtime artifact still exists: %v", err)
	}
}

func generationArtifactTestDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
