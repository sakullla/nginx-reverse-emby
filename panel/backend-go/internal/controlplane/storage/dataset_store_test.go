//go:build !fast && !integration

package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func storeDatasetFixture(t *testing.T, store *GormStore, revision, prefix string) (DatasetVersionRow, DatasetSnapshot) {
	t.Helper()
	source := sdk.DatasetSource{ID: "regions", Name: "Regions", Format: sdk.DatasetFormatCIDR}
	encodedSource, _ := json.Marshal(source)
	if err := store.PutDatasetSource(t.Context(), DatasetSourceRow{ID: source.ID, ResourceGroupID: "default", SourceJSON: string(encodedSource), RetrievalJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{prefix}}}})
	sum := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: source, Revision: revision, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := index.MarshalBinary()
	row, err := store.StoreDatasetVersion(t.Context(), index.Version(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	return row, DatasetSnapshot{Version: index.Version(), Artifact: DatasetArtifact{ID: row.ArtifactID, Kind: DatasetArtifactKind, SHA256: row.ArtifactSHA256, SizeBytes: row.ArtifactSizeBytes}, Bindings: []DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}
}

func TestDatasetRevisionArtifactsBindIssuedSnapshotAndProtectOfflineLastGood(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, oldData := storeDatasetFixture(t, store, "v1", "192.0.2.0/24")
	if err := store.db.Create(&AgentRow{ID: "edge", LastSeenAt: time.Now().UTC().Format(time.RFC3339Nano)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.PutDatasetBinding(t.Context(), DatasetBindingRow{AgentID: "edge", InstanceID: "instance", SourceID: "regions", VersionDigest: old.Digest, ClassificationsJSON: `[{"name":"cn-44","kind":"region"}]`, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Revision: 1, Datasets: []DatasetSnapshot{oldData}}
	encoded, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(encoded)
	snapshotDigest := hex.EncodeToString(sum[:])
	if _, err := store.EnsureAgentHeartbeatRevision(t.Context(), "edge", snapshot, encoded, snapshotDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&AgentRevisionRow{}).Where("agent_id = ? AND revision = ?", "edge", 1).Updates(map[string]any{"state": AgentRevisionStateApplied, "generation_id": "generation-one"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&AgentRevisionPointerRow{}).Where("agent_id = ?", "edge").Updates(map[string]any{"applied_revision": 1, "last_known_good_revision": 1}).Error; err != nil {
		t.Fatal(err)
	}
	status, err := store.DatasetNodeStatus(t.Context(), "regions", "edge", time.Now().UTC())
	if err != nil || status.Phase != sdk.DatasetNodeApplied || status.Applied != old.Digest {
		t.Fatalf("acknowledged dataset status: %+v %v", status, err)
	}
	newVersion, _ := storeDatasetFixture(t, store, "v2", "198.51.100.0/24")
	if err := store.ActivateDatasetVersion(t.Context(), "regions", newVersion.Digest, map[string]int64{"edge": 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&AgentRow{}).Where("id = ?", "edge").Update("last_seen_at", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)).Error; err != nil {
		t.Fatal(err)
	}
	status, err = store.DatasetNodeStatus(t.Context(), "regions", "edge", time.Now().UTC())
	if err != nil || status.Phase != sdk.DatasetNodeOffline || status.Desired != newVersion.Digest || status.Applied != old.Digest || status.LastGood != old.Digest {
		t.Fatalf("offline desired/applied distinction: %+v %v", status, err)
	}
	artifact, err := store.ResolveAgentRevisionDatasetArtifact(t.Context(), "edge", 1, snapshotDigest, old.ArtifactID)
	if err != nil || artifact.Kind != DatasetArtifactKind || len(artifact.Payload) == 0 {
		t.Fatal("revision lost old dataset", err)
	}
	for _, item := range []struct {
		agent      string
		revision   int64
		digest, id string
	}{{"other", 1, snapshotDigest, old.ArtifactID}, {"edge", 2, snapshotDigest, old.ArtifactID}, {"edge", 1, strings.Repeat("f", 64), old.ArtifactID}, {"edge", 1, snapshotDigest, newVersion.ArtifactID}} {
		if _, err := store.ResolveAgentRevisionDatasetArtifact(t.Context(), item.agent, item.revision, item.digest, item.id); err == nil {
			t.Fatal("foreign dataset artifact authority accepted")
		}
	}
	if _, found, err := store.ResolveAgentRevisionPolicyArtifact(t.Context(), "edge", 1, snapshotDigest, old.ArtifactID); err != nil || found {
		t.Fatal("dataset was exposed as executable policy artifact")
	}
	if err := store.DeleteDatasetSource(t.Context(), "regions"); !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("referenced source deleted: %v", err)
	}
	if err := store.RemoveDatasetBinding(t.Context(), "regions", "edge", "instance"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDatasetSource(t.Context(), "regions"); !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("offline revision reference lost after unbinding: %v", err)
	}
	if _, err := store.PruneGenerationArtifactFiles(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadDatasetIndex(t.Context(), "regions", old.Digest); err != nil {
		t.Fatal("cleanup removed last-good index", err)
	}
	path, err := store.ResolveLocalDatasetArtifact(t.Context(), oldData)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveAgentRevisionDatasetArtifact(t.Context(), "edge", 1, snapshotDigest, old.ArtifactID); err == nil {
		t.Fatal("corrupt old index served")
	}
}

func TestDatasetVersionHistoryRetentionAndImmutableMetadata(t *testing.T) {
	root := t.TempDir()
	store, err := newStorageTestSQLiteStore(t, root, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	var rows []DatasetVersionRow
	for i, prefix := range []string{"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "10.0.0.0/8"} {
		row, _ := storeDatasetFixture(t, store, string(rune('a'+i)), prefix)
		rows = append(rows, row)
	}
	if err := store.DeleteDatasetVersion(t.Context(), "regions", rows[3].Digest); !errors.Is(err, ErrDatasetInUse) {
		t.Fatalf("latest three were not retained: %v", err)
	}
	if err := store.DeleteDatasetVersion(t.Context(), "regions", rows[0].Digest); err != nil {
		t.Fatal("unreferenced old version not removable", err)
	}
	page, err := store.DatasetHistory(t.Context(), sdk.DatasetCatalogRequest{SourceID: "regions", Limit: 1})
	if err != nil || len(page.Versions) != 1 || page.NextCursor == "" {
		t.Fatalf("history pagination: %+v %v", page, err)
	}
	next, err := store.DatasetHistory(t.Context(), sdk.DatasetCatalogRequest{SourceID: "regions", Limit: 1, Cursor: page.NextCursor})
	if err != nil || next.Versions[0].Digest == page.Versions[0].Digest {
		t.Fatal("history cursor duplicated version", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen the actual persisted SQLite/file store, without recreating records.
	reopened, err := NewStore(StoreConfig{Driver: "sqlite", DSN: root + "/panel.db", DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, _, err := reopened.ReadDatasetIndex(t.Context(), "regions", rows[3].Digest); err != nil {
		t.Fatal("restart lost immutable dataset", err)
	}
}
