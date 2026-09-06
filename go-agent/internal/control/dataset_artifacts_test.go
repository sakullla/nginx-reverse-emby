package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func datasetControlFixture(t *testing.T) (model.DatasetSnapshot, []byte) {
	t.Helper()
	data, _ := json.Marshal(datasets.CIDRDocument{Schema: datasets.CIDRSchema, Classifications: []datasets.CIDRClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion, DisplayName: "广东省", CIDRs: []string{"192.0.2.0/24"}}}})
	rawDigest := sha256.Sum256(data)
	index, err := datasets.Compile(t.Context(), datasets.Input{Source: sdk.DatasetSource{ID: "regions", Name: "Regions", Format: sdk.DatasetFormatCIDR}, Revision: "v1", FetchedAt: "2026-09-05T00:00:00Z", ExpectedDigest: "sha256:" + hex.EncodeToString(rawDigest[:]), Data: data}, datasets.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := index.MarshalBinary()
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	return model.DatasetSnapshot{Version: index.Version(), Artifact: model.DatasetArtifact{ID: "dataset-" + digest, Kind: model.DatasetArtifactKind, SHA256: digest, SizeBytes: int64(len(payload))}, Bindings: []model.DatasetInstanceBinding{{InstanceID: "instance", Classifications: []sdk.DatasetClassification{{Name: "cn-44", Kind: sdk.DatasetClassificationRegion}}}}}, payload
}

func TestDatasetArtifactPrivateDownloadPreparationAndRestart(t *testing.T) {
	entry, payload := datasetControlFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/agent-dataset-artifacts/"+entry.Artifact.ID || r.Header.Get("X-Agent-Token") != "credential" || r.URL.Query().Get("snapshot_digest") != strings.Repeat("a", 64) || r.URL.Query().Get("revision") != "1" {
			http.Error(w, "denied", 403)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.nre.dataset-index" {
			t.Error("dataset disguised as plugin artifact")
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	cache := t.TempDir()
	client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "credential", PluginCacheDir: cache}, server.Client())
	snapshot := model.Snapshot{Revision: 1, Datasets: []model.DatasetSnapshot{entry}}
	if err := client.prepareDatasetArtifacts(t.Context(), &snapshot, 1, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	path := snapshot.Datasets[0].Artifact.LocalPath
	if !strings.HasSuffix(path, ".nredataset") || !strings.HasPrefix(path, cache+string(filepath.Separator)) {
		t.Fatal("wrong dataset cache ownership")
	}
	restarted := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "credential", PluginCacheDir: cache}, server.Client())
	snapshot.Datasets[0].Artifact.LocalPath = "plugin-chosen-path"
	if err := restarted.prepareDatasetArtifacts(t.Context(), &snapshot, 1, strings.Repeat("a", 64)); err != nil || calls.Load() != 1 || snapshot.Datasets[0].Artifact.LocalPath != path {
		t.Fatal("restart did not verify/reuse immutable dataset cache", err)
	}
	bad := model.Snapshot{Revision: 1, Datasets: model.CloneDatasetSnapshots(snapshot.Datasets)}
	bad.Datasets[0].Version.Revision = "forged"
	if err := client.prepareDatasetArtifacts(t.Context(), &bad, 1, strings.Repeat("a", 64)); err == nil {
		t.Fatal("cached index accepted mismatched desired manifest")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(payload) {
		t.Fatal("failed candidate damaged old index")
	}
}

func TestDatasetArtifactBadCandidateReportsFailureWithoutApplying(t *testing.T) {
	entry, _ := datasetControlFixture(t)
	snapshot := model.Snapshot{Revision: 2, AgentConfig: model.AgentConfig{TrafficStatsInterval: "1s"}, Datasets: []model.DatasetSnapshot{entry}, Rules: []model.HTTPRule{}, L4Rules: []model.L4Rule{}, RelayListeners: []model.RelayListener{}, EgressProfiles: []model.EgressProfile{}, Certificates: []model.ManagedCertificateBundle{}, CertificatePolicies: []model.ManagedCertificatePolicy{}, PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{}, PluginPolicies: []model.PluginPolicy{}}
	encoded, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	var started bool
	var reported model.RevisionReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent-revisions/pull":
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": map[string]any{"has_update": true, "desired_revision": 2, "snapshot": json.RawMessage(encoded), "lease": model.RevisionLease{AgentID: "edge", Revision: 2, Attempt: 1, LeaseID: "lease", SnapshotDigest: digest, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, DeadlineAt: time.Now().Add(time.Minute)}}})
		case "/api/agent-revisions/2/start":
			started = true
			_, _ = w.Write([]byte(`{}`))
		case "/api/agent-revisions/2/report":
			_ = json.NewDecoder(r.Body).Decode(&reported)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte("corrupt index"))
		}
	}))
	defer server.Close()
	client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentID: "edge", AgentToken: "credential", PluginCacheDir: t.TempDir()}, server.Client())
	pull, err := client.PullRevision(t.Context())
	if err == nil || pull.Snapshot != nil {
		t.Fatal("bad data candidate was exposed for application")
	}
	if !started || reported.Status != "failed" || reported.ErrorCode != "dataset-prepare-failed" || reported.Revision != 2 {
		t.Fatalf("failed preparation did not produce actual lease report: %+v", reported)
	}
}
