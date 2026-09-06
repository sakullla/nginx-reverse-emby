package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
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

type datasetDesiredStore struct {
	*storage.GormStore
	desired storage.Snapshot
	reads   atomic.Int32
}

func (store *datasetDesiredStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	store.reads.Add(1)
	return store.desired, nil
}

type datasetRuntimeSink struct{}

func (datasetRuntimeSink) Save(context.Context, goagentembedded.RuntimeState) error { return nil }

func TestDatasetApprovedApplyAndRollbackIgnoreBrokenUnapprovedDesired(t *testing.T) {
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			source := sdk.DatasetSource{ID: "regions", Name: "Regions", Format: sdk.DatasetFormatCIDR}
			sourceJSON, _ := json.Marshal(source)
			if err := store.PutDatasetSource(t.Context(), storage.DatasetSourceRow{ID: source.ID, ResourceGroupID: "default", SourceJSON: string(sourceJSON), RetrievalJSON: `{}`}); err != nil {
				t.Fatal(err)
			}
			makeDataset := func(revision, prefix string) storage.DatasetSnapshot {
				t.Helper()
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
				return storage.DatasetSnapshot{Version: index.Version(), Artifact: storage.DatasetArtifact{ID: row.ArtifactID, Kind: storage.DatasetArtifactKind, SHA256: row.ArtifactSHA256, SizeBytes: row.ArtifactSizeBytes}, Bindings: []storage.DatasetInstanceBinding{{InstanceID: "policy-instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}
			}
			a := makeDataset("A", "192.0.2.0/24")
			b := makeDataset("B", "198.51.100.0/24")
			bPath, err := store.ResolveLocalDatasetArtifact(t.Context(), b)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "missing" {
				err = os.Remove(bPath)
			} else {
				err = os.WriteFile(bPath, []byte("corrupt"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			wasm := compatfixture.PolicyV1GuestWASM()
			wasmPath := filepath.Join(t.TempDir(), "policy.wasm")
			if err := os.WriteFile(wasmPath, wasm, 0o600); err != nil {
				t.Fatal(err)
			}
			wasmSum := sha256.Sum256(wasm)
			digest := hex.EncodeToString(wasmSum[:])
			stage := storage.PolicyStage{Kind: "ip", PolicyID: "policy-reference", PluginID: "reference", PluginVersion: "1.0.0", InstanceID: "policy-instance", PackageDigest: digest, ArtifactPath: wasmPath, ArtifactDigest: digest, SignatureVerified: true, SignerKeyID: "test-key", SignerFingerprint: "test-fingerprint", ABI: sdk.PolicyABIV1, ExtensionPoints: []string{"http.request"}, DeclaredScopes: []string{"http.inspect", "state.read"}, GrantedScopes: []string{"http.inspect", "state.read"}, ResourceGroupID: "default", Config: json.RawMessage(`{"mode":"compat"}`), ResourceBudget: storage.PolicyResourceBudget{TimeoutMS: 2, MemoryBytes: 1 << 20, Concurrency: 1, InputBytes: 4096, OutputBytes: 4096}, FailurePolicy: storage.PolicyFailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"}}
			approved := storage.Snapshot{Revision: 7, Datasets: []storage.DatasetSnapshot{a}, PluginPolicies: []storage.PluginPolicy{{ID: "policy", Revision: 7, Stages: []storage.PolicyStage{stage}}}}
			desired := approved
			desired.Revision = 9
			desired.Datasets = []storage.DatasetSnapshot{b}
			owner := &datasetDesiredStore{GormStore: store, desired: desired}
			syncSource := NewSyncSource(owner, "local")
			runtimeDir := t.TempDir()
			assertApplied := func(revision int64) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(runtimeDir, "embedded-agent-state", "applied-snapshot.json"))
				if err != nil {
					t.Fatal(err)
				}
				var snapshot goagentembedded.Snapshot
				if json.Unmarshal(data, &snapshot) != nil || snapshot.Revision != revision || len(snapshot.Datasets) != 1 || snapshot.Datasets[0].Version.Digest != a.Version.Digest {
					t.Fatalf("approved A was not persisted at revision %d: %s", revision, data)
				}
			}
			start := func() (*Runtime, func()) {
				t.Helper()
				embedded, err := goagentembedded.New(goagentembedded.Config{AgentID: "local", AgentName: "local", DataDir: runtimeDir, HeartbeatInterval: time.Hour}, syncSourceAdapter{source: syncSource}, datasetRuntimeSink{})
				if err != nil {
					t.Fatal(err)
				}
				runCtx, cancel := context.WithCancel(t.Context())
				finished := make(chan error, 1)
				go func() { finished <- embedded.Run(runCtx) }()
				outer := &Runtime{runtime: embedded, source: syncSource}
				return outer, func() {
					cancel()
					select {
					case <-finished:
					case <-time.After(5 * time.Second):
						t.Error("embedded runtime failed to stop")
					}
					if err := embedded.Close(); err != nil {
						t.Error(err)
					}
				}
			}
			first, stop := start()
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := first.ApplyRevision(ctx, approved); err != nil {
				stop()
				t.Fatal("bad unapproved desired B blocked approved A", err)
			}
			assertApplied(7)
			rollback := approved
			rollback.Revision = 8
			if err := first.ApplyRevision(ctx, rollback); err != nil {
				stop()
				t.Fatal("bad B blocked approved rollback revision", err)
			}
			assertApplied(8)
			if owner.reads.Load() < 2 {
				stop()
				t.Fatal("test bypassed actual embedded telemetry sync chain")
			}
			if _, err := first.DiagnoseSnapshot(ctx, desired, service.TaskEnvelope{Type: service.TaskTypeDiagnoseHTTPRule, Payload: map[string]any{"rule_id": 1}}); err == nil || !strings.Contains(err.Error(), "dataset") {
				stop()
				t.Fatalf("diagnosis stopped validating its explicit candidate: %v", err)
			}
			stop()
			second, stopSecond := start()
			defer stopSecond()
			if err := second.ApplyRevision(ctx, rollback); err != nil {
				t.Fatal("restart could not restore complete A while desired B was broken", err)
			}
			assertApplied(8)
		})
	}
}
