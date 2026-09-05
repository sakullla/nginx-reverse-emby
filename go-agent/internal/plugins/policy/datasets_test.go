//go:build !integration

package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func datasetPolicySnapshot(t *testing.T, revision int64, prefix string) model.Snapshot {
	t.Helper()
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{prefix}}}})
	sum := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: sdk.DatasetSource{ID: "regions", Name: "Region fixture", Format: sdk.DatasetFormatCIDR}, Revision: prefix, FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := index.MarshalBinary()
	digest := sha256.Sum256(encoded)
	sha := hex.EncodeToString(digest[:])
	path := filepath.Join(t.TempDir(), sha+".nredataset")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	policy := testPolicy("shared", model.PolicyKindIP)
	return model.Snapshot{Revision: revision, PluginPolicies: []model.PluginPolicy{policy}, Datasets: []model.DatasetSnapshot{{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + sha, Kind: model.DatasetArtifactKind, SHA256: sha, SizeBytes: int64(len(encoded)), LocalPath: path}, Bindings: []model.DatasetInstanceBinding{{InstanceID: policy.Stages[0].InstanceID, Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}}}
}

func datasetPolicyAuthorization(generation string) sdk.PolicyHostCallAuthorization {
	return sdk.PolicyHostCallAuthorization{InstanceID: "shared-instance-ip", Generation: generation, EntryID: "entry-1", DeclaredScopes: []string{string(sdk.CapabilityDatasetQuery)}, GrantedScopes: []string{string(sdk.CapabilityDatasetQuery)}}
}
func datasetPolicyQuery(reference sdk.DatasetReference) sdk.DatasetQueryRequest {
	return sdk.DatasetQueryRequest{Reference: reference, Address: "192.0.2.1", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}, Budget: sdk.DatasetQueryBudget{MaxDurationMicros: 2000, MaxResponseBytes: 32768}}
}

func TestDatasetGenerationSharesVersionWithIndependentInstanceHandles(t *testing.T) {
	snapshot := datasetPolicySnapshot(t, 1, "192.0.2.0/24")
	snapshot.PluginGenerations = []model.PluginGeneration{{InstanceID: "ss-instance", ID: "ss-generation"}}
	snapshot.Datasets[0].Bindings = append(snapshot.Datasets[0].Bindings, model.DatasetInstanceBinding{InstanceID: "ss-instance", Classifications: snapshot.Datasets[0].Bindings[0].Classifications})
	provider, err := prepareDatasetGeneration(t.Context(), "ip-generation", snapshot, &datasetIndexPool{})
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	ipAuth := datasetPolicyAuthorization("ip-generation")
	ssAuth := ipAuth
	ssAuth.InstanceID = "ss-instance"
	ssAuth.Generation = "ss-generation"
	open := sdk.DatasetOpenRequest{SourceID: "regions", VersionDigest: snapshot.Datasets[0].Version.Digest}
	ip, err := provider.Open(ipAuth, open)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := provider.Open(ssAuth, open)
	if err != nil {
		t.Fatal(err)
	}
	if ip.Handle == ss.Handle || ip.Generation == ss.Generation || ip.VersionDigest != ss.VersionDigest {
		t.Fatal("shared source handle ownership was not isolated")
	}
	for _, item := range []struct {
		auth sdk.PolicyHostCallAuthorization
		ref  sdk.DatasetReference
	}{{ipAuth, ip}, {ssAuth, ss}} {
		response, err := provider.Query(t.Context(), item.auth, datasetPolicyQuery(item.ref))
		if err != nil || response.Status != sdk.DatasetQueryOK || !response.Matches[0].Matched {
			t.Fatal("authorized shared-source query failed", err)
		}
	}
	response, err := provider.Query(t.Context(), ssAuth, datasetPolicyQuery(ip))
	if err != nil || response.Status != sdk.DatasetQueryUnauthorized {
		t.Fatal("SS reused IP instance handle")
	}
	foreign := ssAuth
	foreign.InstanceID = "unbound-instance"
	if _, err := provider.Open(foreign, open); err == nil {
		t.Fatal("unbound instance acquired shared source")
	}
}

func TestDatasetGenerationPublishesAtomicallyRetainsOldAndRevokesHandles(t *testing.T) {
	registry := module.NewRegistry()
	owner := NewModule(&testGenerationFactory{}, nil)
	if err := registry.Register(owner); err != nil {
		t.Fatal(err)
	}
	first := datasetPolicySnapshot(t, 1, "192.0.2.0/24")
	firstContext, _ := module.NewGenerationContext(model.Snapshot{}, first)
	candidate, err := registry.PrepareGeneration(t.Context(), firstContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, _ := candidate.Publish()
	defer view.Destroy(t.Context())
	value, found := view.Resolve(ProviderDatasets)
	if !found {
		t.Fatal("dataset provider not in generation view")
	}
	provider := value.(*DatasetGeneration)
	auth := datasetPolicyAuthorization(view.ID())
	reference, err := provider.Open(auth, sdk.DatasetOpenRequest{SourceID: "regions", VersionDigest: first.Datasets[0].Version.Digest})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Query(t.Context(), auth, datasetPolicyQuery(reference))
	if err != nil || response.Status != sdk.DatasetQueryOK || !response.Matches[0].Matched {
		t.Fatalf("actual index query: %+v %v", response, err)
	}
	failed := model.CloneDatasetSnapshots(first.Datasets)
	failed[0].Bindings[0].Classifications[0].Name = "missing"
	next := first
	next.Revision = 2
	next.Datasets = failed
	nextContext, _ := module.NewGenerationContext(first, next)
	if _, err := registry.PrepareGeneration(t.Context(), nextContext); err == nil {
		t.Fatal("disappeared bound category published")
	}
	if registry.ActiveGeneration() != view {
		t.Fatal("failed dataset candidate replaced old generation")
	}
	second := datasetPolicySnapshot(t, 2, "198.51.100.0/24")
	secondContext, _ := module.NewGenerationContext(first, second)
	prepared, err := registry.PrepareGeneration(t.Context(), secondContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	newView, oldView := prepared.Publish()
	defer newView.Destroy(t.Context())
	if oldView != view {
		t.Fatal("old dataset view lost before drain")
	}
	response, err = provider.Query(t.Context(), auth, datasetPolicyQuery(reference))
	if err != nil || !response.Matches[0].Matched {
		t.Fatal("old session mixed new data version")
	}
	value, _ = newView.Resolve(ProviderDatasets)
	nextProvider := value.(*DatasetGeneration)
	nextAuth := datasetPolicyAuthorization(newView.ID())
	nextRef, err := nextProvider.Open(nextAuth, sdk.DatasetOpenRequest{SourceID: "regions", VersionDigest: second.Datasets[0].Version.Digest})
	if err != nil {
		t.Fatal(err)
	}
	response, err = nextProvider.Query(t.Context(), nextAuth, datasetPolicyQuery(nextRef))
	if err != nil || response.Status != sdk.DatasetQueryOK || response.Matches[0].Matched {
		t.Fatal("new generation reused old data")
	}
	if err := oldView.Destroy(t.Context()); err != nil {
		t.Fatal(err)
	}
	response, err = provider.Query(t.Context(), auth, datasetPolicyQuery(reference))
	if err != nil || response.Status != sdk.DatasetQueryStaleReference {
		t.Fatalf("destroyed generation remained usable: %+v %v", response, err)
	}
	if len(owner.datasetPool.values) != 1 {
		t.Fatal("retired index not released")
	}
	_ = newView.Destroy(t.Context())
	if owner.datasetPool.memory != 0 || len(owner.datasetPool.values) != 0 {
		t.Fatal("dataset index memory leaked after close")
	}
}

func TestDatasetGenerationRejectsAuthorityBudgetAndPreparationFailures(t *testing.T) {
	snapshot := datasetPolicySnapshot(t, 1, "192.0.2.0/24")
	pool := &datasetIndexPool{}
	provider, err := prepareDatasetGeneration(t.Context(), "gen-1", snapshot, pool)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	auth := datasetPolicyAuthorization("gen-1")
	reference, err := provider.Open(auth, sdk.DatasetOpenRequest{SourceID: "regions", VersionDigest: snapshot.Datasets[0].Version.Digest})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*sdk.PolicyHostCallAuthorization, *sdk.DatasetQueryRequest)
		status sdk.DatasetQueryStatus
	}{
		{"grant", func(a *sdk.PolicyHostCallAuthorization, _ *sdk.DatasetQueryRequest) { a.GrantedScopes = nil }, sdk.DatasetQueryUnauthorized},
		{"instance", func(a *sdk.PolicyHostCallAuthorization, _ *sdk.DatasetQueryRequest) { a.InstanceID = "foreign" }, sdk.DatasetQueryUnauthorized},
		{"generation", func(a *sdk.PolicyHostCallAuthorization, _ *sdk.DatasetQueryRequest) { a.Generation = "old" }, sdk.DatasetQueryUnauthorized},
		{"forged", func(_ *sdk.PolicyHostCallAuthorization, r *sdk.DatasetQueryRequest) {
			r.Reference.Handle = strings.Repeat("a", 64)
		}, sdk.DatasetQueryStaleReference},
		{"budget", func(_ *sdk.PolicyHostCallAuthorization, r *sdk.DatasetQueryRequest) {
			r.Budget.MaxDurationMicros = 2001
		}, sdk.DatasetQueryBudgetExceeded},
		{"scope", func(_ *sdk.PolicyHostCallAuthorization, r *sdk.DatasetQueryRequest) {
			r.Classifications[0].Name = "other"
		}, sdk.DatasetQueryUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, request := auth, datasetPolicyQuery(reference)
			test.mutate(&a, &request)
			response, err := provider.Query(t.Context(), a, request)
			if err != nil || response.Status != test.status || len(response.Matches) != 0 {
				t.Fatalf("failure became non-match: %+v %v", response, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	response, err := provider.Query(ctx, auth, datasetPolicyQuery(reference))
	if err == nil && response.Status == sdk.DatasetQueryOK {
		t.Fatal("cancelled budget returned normal match")
	}
	for _, variant := range []string{"missing", "digest", "manifest"} {
		t.Run(variant, func(t *testing.T) {
			next := snapshot
			next.Datasets = model.CloneDatasetSnapshots(snapshot.Datasets)
			switch variant {
			case "missing":
				next.Datasets[0].Artifact.LocalPath = filepath.Join(t.TempDir(), "missing")
			case "digest":
				next.Datasets[0].Artifact.LocalPath = filepath.Join(t.TempDir(), "bad")
				if err := os.WriteFile(next.Datasets[0].Artifact.LocalPath, make([]byte, next.Datasets[0].Artifact.SizeBytes), 0o600); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				next.Datasets[0].Version.Revision = "wrong"
			}
			if _, err := prepareDatasetGeneration(t.Context(), "new", next, &datasetIndexPool{}); err == nil {
				t.Fatal("invalid candidate accepted")
			}
		})
	}
	// Restart reconstructs the same immutable index from the verified cache.
	restarted, err := prepareDatasetGeneration(t.Context(), "restart", snapshot, &datasetIndexPool{})
	if err != nil {
		t.Fatal(err)
	}
	restarted.Close()
}
