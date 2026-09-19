package rpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"os"
	"path/filepath"
	"testing"
)

func managedDatasetView(t *testing.T, revision int64, cidr string) (*module.GenerationView, *policy.DatasetGeneration) {
	t.Helper()
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, CIDRs: []string{cidr}}}})
	sum := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: sdk.DatasetSource{ID: "regions", Name: "regions", Format: sdk.DatasetFormatCIDR}, Revision: cidr, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := index.MarshalBinary()
	sum = sha256.Sum256(encoded)
	sha := hex.EncodeToString(sum[:])
	path := filepath.Join(t.TempDir(), "index")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := model.Snapshot{Revision: revision, PluginGenerations: []model.PluginGeneration{{ID: "provider-definition", InstanceID: "instance"}}, Datasets: []model.DatasetSnapshot{{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + sha, Kind: model.DatasetArtifactKind, SHA256: sha, SizeBytes: int64(len(encoded)), LocalPath: path}, Bindings: []model.DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}}}
	registry := module.NewRegistry()
	if err := registry.Register(policy.NewModule(nil, nil)); err != nil {
		t.Fatal(err)
	}
	generation, err := module.NewGenerationContext(model.Snapshot{}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := registry.PrepareGeneration(t.Context(), generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, _ := candidate.Publish()
	t.Cleanup(func() { view.Destroy(t.Context()) })
	value, _ := view.Resolve(policy.ProviderDatasets)
	return view, value.(*policy.DatasetGeneration)
}
