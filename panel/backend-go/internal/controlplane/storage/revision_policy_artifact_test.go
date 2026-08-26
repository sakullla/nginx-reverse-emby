//go:build !fast && !integration

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
)

func TestRevisionPolicyArtifactSurvivesLivePackageRemovalAndBindsRevisionIdentity(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	payload := []byte("immutable revision wasm")
	artifactDigest := hex.EncodeToString(sumBytes(payload))
	packageDigest := strings.Repeat("b", 64)
	packageIdentity := strings.Repeat("c", 64)
	cachePath := filepath.Join(store.dataRoot, "plugins", "packages", packageIdentity)
	artifactPath := filepath.Join(cachePath, "artifacts", "policy.wasm")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	packageRow := PluginPackageRow{Identity: packageIdentity, Digest: packageDigest, PluginID: "official.revision", RuntimeKind: "wasm-policy", RuntimeABI: "nre:policy/v1", HostScope: "agent", SignatureFingerprint: strings.Repeat("d", 64), CachePath: cachePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}
	artifactRow := PluginArtifactRow{ID: "artifact-shared", PackageIdentity: packageIdentity, PackageDigest: packageDigest, Path: "artifacts/policy.wasm", SHA256: artifactDigest, SizeBytes: int64(len(payload)), Mode: "readonly", RuntimeKind: "wasm-policy", RuntimeABI: "nre:policy/v1", HostScope: "agent"}
	if err := store.db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&artifactRow).Error; err != nil {
		t.Fatal(err)
	}

	stage := PolicyStage{PackageDigest: packageDigest, ArtifactDigest: artifactDigest, SignerFingerprint: packageRow.SignatureFingerprint, ArtifactSource: PolicyArtifactSource{ArtifactID: artifactRow.ID, PackageIdentity: packageIdentity, PackageDigest: packageDigest, RelativePath: artifactRow.Path, SHA256: artifactDigest, SizeBytes: int64(len(payload))}}
	revisionNumber := int64(9)
	snapshot := Snapshot{Revision: revisionNumber, PluginPolicies: []PluginPolicy{{ID: "full", Stages: []PolicyStage{stage}}, {ID: "lite", Stages: []PolicyStage{stage}}}}
	now := time.Now().UTC()
	blobs, refs, err := store.BuildAgentRevisionPolicyArtifacts(t.Context(), "edge-a", revisionNumber, snapshot, now)
	if err != nil || len(blobs) != 1 || len(refs) != 1 {
		t.Fatalf("BuildAgentRevisionPolicyArtifacts() = %d blobs, %d refs, %v", len(blobs), len(refs), err)
	}
	snapshotPayload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDigest := hex.EncodeToString(sumBytes(snapshotPayload))
	issued, err := store.EnsureAgentHeartbeatRevision(t.Context(), "edge-a", snapshot, snapshotPayload, snapshotDigest, now)
	if err != nil || issued.Revision != revisionNumber || issued.SnapshotDigest != snapshotDigest {
		t.Fatalf("EnsureAgentHeartbeatRevision() = %+v, %v", issued, err)
	}
	if duplicate, err := store.EnsureAgentHeartbeatRevision(t.Context(), "edge-a", snapshot, snapshotPayload, snapshotDigest, now.Add(time.Second)); err != nil || duplicate.SnapshotDigest != snapshotDigest {
		t.Fatalf("idempotent EnsureAgentHeartbeatRevision() = %+v, %v", duplicate, err)
	}
	var persistedRefs []AgentRevisionArtifactRow
	if err := store.db.Where("agent_id = ? AND revision = ? AND role LIKE ?", "edge-a", revisionNumber, revisionPolicyArtifactRolePrefix+"%").Find(&persistedRefs).Error; err != nil || len(persistedRefs) != 1 {
		t.Fatalf("persisted duplicate artifact refs = %d, %v", len(persistedRefs), err)
	}
	conflictingPayload := append(append([]byte(nil), snapshotPayload...), ' ')
	conflictingDigest := hex.EncodeToString(sumBytes(conflictingPayload))
	if _, err := store.EnsureAgentHeartbeatRevision(t.Context(), "edge-a", snapshot, conflictingPayload, conflictingDigest, now); err == nil || !strings.Contains(err.Error(), "different snapshot digest") {
		t.Fatalf("conflicting heartbeat revision error = %v", err)
	}

	if err := store.db.Delete(&artifactRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Delete(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(artifactPath); err != nil {
		t.Fatal(err)
	}
	resolved, found, err := store.ResolveAgentRevisionPolicyArtifact(t.Context(), "edge-a", revisionNumber, snapshotDigest, artifactRow.ID)
	if err != nil || !found || string(resolved.Payload) != string(payload) {
		t.Fatalf("ResolveAgentRevisionPolicyArtifact() = found %t, payload %q, %v", found, resolved.Payload, err)
	}
	for _, invalid := range []struct {
		agent    string
		revision int64
		digest   string
	}{
		{agent: "edge-b", revision: revisionNumber, digest: snapshotDigest},
		{agent: "edge-a", revision: revisionNumber + 1, digest: snapshotDigest},
		{agent: "edge-a", revision: revisionNumber, digest: strings.Repeat("e", 64)},
	} {
		if _, found, err := store.ResolveAgentRevisionPolicyArtifact(t.Context(), invalid.agent, invalid.revision, invalid.digest, artifactRow.ID); err != nil || found {
			t.Fatalf("invalid binding %q/%d/%q = found %t, %v", invalid.agent, invalid.revision, invalid.digest, found, err)
		}
	}
}

func TestRevisionPolicyArtifactRejectsConflictingDuplicateIdentityBeforeMaterialization(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Deliberately do not seed the live package. Conflict validation must win
	// before the first occurrence can attempt materialization.
	stage := PolicyStage{ArtifactDigest: strings.Repeat("a", 64), PackageDigest: strings.Repeat("b", 64), ArtifactSource: PolicyArtifactSource{ArtifactID: "shared", PackageIdentity: strings.Repeat("c", 64), PackageDigest: strings.Repeat("b", 64), RelativePath: "a.wasm", SHA256: strings.Repeat("a", 64), SizeBytes: 1}}
	conflict := stage
	conflict.ArtifactSource.RelativePath = "other.wasm"
	snapshot := Snapshot{Revision: 1, PluginPolicies: []PluginPolicy{{ID: "a", Stages: []PolicyStage{stage}}, {ID: "b", Stages: []PolicyStage{conflict}}}}
	if _, _, err := store.BuildAgentRevisionPolicyArtifacts(t.Context(), "edge-a", 1, snapshot, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "conflicting snapshot identities") {
		t.Fatalf("conflicting duplicate error = %v", err)
	}
}

func sumBytes(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return digest[:]
}
