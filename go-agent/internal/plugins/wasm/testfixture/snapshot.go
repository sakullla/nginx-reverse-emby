package testfixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type Stage struct {
	Kind          model.PolicyKind
	Mode          sdk.PolicyMode
	Action        sdk.PolicyAction
	Handling      sdk.PolicyModeHandling
	MatchDecision bool
}

// Snapshot materializes a small actual immutable index and conformance guests.
func Snapshot(ctx context.Context, root string, revision int64, prefix string, stages []Stage, additionalRegions ...datasets.CIDRClassification) (model.Snapshot, error) {
	regions := []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, CIDRs: []string{prefix}}}
	regions = append(regions, additionalRegions...)
	raw, err := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: regions})
	if err != nil {
		return model.Snapshot{}, err
	}
	sum := sha256.Sum256(raw)
	index, err := datasets.Compile(ctx, datasets.Input{Source: sdk.DatasetSource{ID: "regions", Name: "fixture regions", Format: sdk.DatasetFormatCIDR}, Revision: strconv.FormatInt(revision, 10), FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(sum[:]), Data: raw}, datasets.Limits{})
	if err != nil {
		return model.Snapshot{}, err
	}
	encoded, err := index.MarshalBinary()
	if err != nil {
		return model.Snapshot{}, err
	}
	sum = sha256.Sum256(encoded)
	sha := hex.EncodeToString(sum[:])
	indexPath := filepath.Join(root, sha+".index")
	if err := os.WriteFile(indexPath, encoded, 0o600); err != nil {
		return model.Snapshot{}, err
	}
	snapshot := model.Snapshot{Revision: revision, PluginPolicies: []model.PluginPolicy{{ID: "effective", Revision: revision}}, Datasets: []model.DatasetSnapshot{{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + sha, Kind: model.DatasetArtifactKind, SHA256: sha, SizeBytes: int64(len(encoded)), LocalPath: indexPath}}}}
	for _, item := range stages {
		var wasm []byte
		if item.MatchDecision {
			wasm, err = ConsumptionGuestMatchDecision("regions", "cn-44", nil, 512, false)
		} else {
			wasm, err = ConsumptionGuestDecision("regions", "cn-44", nil, 512, false, item.Action)
		}
		if err != nil {
			return model.Snapshot{}, err
		}
		digest := sha256.Sum256(wasm)
		artifactSHA := hex.EncodeToString(digest[:])
		path := filepath.Join(root, string(item.Kind)+"-"+artifactSHA+".wasm")
		if err := os.WriteFile(path, wasm, 0o600); err != nil {
			return model.Snapshot{}, err
		}
		instance := "fixture-" + string(item.Kind)
		extensions := []string{"http.request", "l4.accept"}
		if item.Kind == model.PolicyKindWAF {
			extensions = []string{"http.request"}
		}
		stage := model.PolicyStage{Kind: item.Kind, PolicyID: instance, PluginID: "fixture.policy", PluginVersion: "1.0.0", InstanceID: instance, PackageDigest: artifactSHA, ArtifactPath: path, ArtifactDigest: artifactSHA, SignatureVerified: true, SignerKeyID: "fixture-key", SignerFingerprint: artifactSHA, ABI: model.PolicyABIV1, ExtensionPoints: extensions, ResourceGroupID: "default", Config: json.RawMessage(`{}`), DeclaredScopes: []string{string(sdk.CapabilityDatasetResolve), string(sdk.CapabilityDatasetQuery), string(sdk.CapabilityPolicyTrustedSource), "http.inspect", "l4.inspect"}, ResourceBudget: model.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096}, FailurePolicy: model.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
		stage.GrantedScopes = append([]string(nil), stage.DeclaredScopes...)
		mode, handling := item.Mode, item.Handling
		if handling == "" {
			handling = sdk.PolicyModeHandlingRaw
		}
		if mode != "" {
			stage.PolicySettings = &sdk.PolicySettingsSnapshot{Version: sdk.PolicySettingsVersion{Revision: 1, InstanceVersion: 1}, Settings: sdk.PolicyModeSettings{Handling: handling, DefaultMode: &mode}}
		}
		snapshot.PluginPolicies[0].Stages = append(snapshot.PluginPolicies[0].Stages, stage)
		snapshot.Datasets[0].Bindings = append(snapshot.Datasets[0].Bindings, model.DatasetInstanceBinding{InstanceID: instance, Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}})
	}
	return snapshot, nil
}
