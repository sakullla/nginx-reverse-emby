package localagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestDatasetEmbeddedProjectionMaterializesExactVersionAndRejectsFailure(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := sdk.DatasetSource{ID: "regions", Name: "Regions", Format: sdk.DatasetFormatCIDR}
	sourceJSON, _ := json.Marshal(source)
	if err := store.PutDatasetSource(t.Context(), storage.DatasetSourceRow{ID: source.ID, ResourceGroupID: "default", SourceJSON: string(sourceJSON), RetrievalJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{"192.0.2.0/24"}}}})
	sum := sha256.Sum256(raw)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: source, Revision: "v1", FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: raw}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := index.MarshalBinary()
	row, err := store.StoreDatasetVersion(t.Context(), index.Version(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	original := storage.Snapshot{Datasets: []storage.DatasetSnapshot{{Version: index.Version(), Artifact: storage.DatasetArtifact{ID: row.ArtifactID, Kind: storage.DatasetArtifactKind, SHA256: row.ArtifactSHA256, SizeBytes: row.ArtifactSizeBytes, LocalPath: "ignored-untrusted-path"}, Bindings: []storage.DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}}}
	materialized, err := materializeLocalDatasets(t.Context(), store, original)
	if err != nil {
		t.Fatal(err)
	}
	projected := toEmbeddedSnapshot(materialized)
	if len(projected.Datasets) != 1 || projected.Datasets[0].Version != index.Version() || projected.Datasets[0].Artifact.LocalPath == "ignored-untrusted-path" {
		t.Fatal("embedded dataset projection lost version or trusted materialization")
	}
	if original.Datasets[0].Artifact.LocalPath != "ignored-untrusted-path" {
		t.Fatal("materialization mutated caller snapshot")
	}
	if err := projected.Datasets[0].Validate(); err != nil {
		t.Fatal(err)
	}
	bad := original
	bad.Datasets = append([]storage.DatasetSnapshot(nil), original.Datasets...)
	bad.Datasets[0].Version.Revision = "other"
	if _, err := materializeLocalDatasets(t.Context(), store, bad); err == nil {
		t.Fatal("embedded Agent accepted a mismatched index manifest")
	}
	if _, err := materializeLocalDatasets(t.Context(), nil, original); err == nil {
		t.Fatal("missing local resolver silently dropped dataset")
	}
	if err := os.Remove(projected.Datasets[0].Artifact.LocalPath); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeLocalDatasets(t.Context(), store, original); err == nil {
		t.Fatal("missing index silently applied")
	}
}

func TestDatasetEmbeddedProjectionDoesNotAliasNestedAttributePointers(t *testing.T) {
	boolean, integer := true, int64(4)
	source := storage.Snapshot{Datasets: []storage.DatasetSnapshot{{Bindings: []storage.DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "ai", Kind: sdk.DatasetClassificationDomain, Attributes: []sdk.DatasetAttribute{{Name: "!cn", Boolean: &boolean}, {Name: "rank", Integer: &integer}}}}}}}}}
	projected := toEmbeddedSnapshot(source)
	boolean = false
	integer = 5
	attrs := projected.Datasets[0].Bindings[0].Classifications[0].Attributes
	if !*attrs[0].Boolean || *attrs[1].Integer != 4 {
		t.Fatal("embedded dataset shares attribute pointers with control-plane snapshot")
	}
	*attrs[0].Boolean = true
	*attrs[1].Integer = 8
	if boolean || integer != 5 {
		t.Fatal("embedded consumer mutated control-plane source")
	}
}
