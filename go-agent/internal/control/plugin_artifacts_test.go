//go:build !integration

package control

import (
	"crypto/sha256"

	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"strings"
	"sync/atomic"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const testPluginSnapshotDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestPluginArtifactPreparationDownloadsAcrossFilesystemsAndPublishesVerifiedCache(t *testing.T) {
	payload := []byte("verified wasm payload")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Agent-Token") != "agent-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/agent-plugin-artifacts/artifact-1" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("revision") != "1" || r.URL.Query().Get("snapshot_digest") != testPluginSnapshotDigest {
			http.Error(w, "invalid revision identity", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/wasm")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	cacheRoot := t.TempDir()
	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "agent-secret", PluginCacheDir: cacheRoot,
	}, server.Client())
	snapshot := pluginArtifactSnapshot("artifact-1", digest, int64(len(payload)))
	if err := client.preparePluginArtifacts(t.Context(), &snapshot, snapshot.Revision, testPluginSnapshotDigest); err != nil {
		t.Fatalf("preparePluginArtifacts() error = %v", err)
	}
	localPath := snapshot.PluginPolicies[0].Stages[0].ArtifactPath
	if !strings.HasPrefix(filepath.Clean(localPath), filepath.Clean(cacheRoot)+string(filepath.Separator)) {
		t.Fatalf("local artifact path %q is outside Agent cache %q", localPath, cacheRoot)
	}
	got, err := os.ReadFile(localPath)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("materialized artifact = %q, error = %v", got, err)
	}
	if err := client.preparePluginArtifacts(t.Context(), &snapshot, snapshot.Revision, testPluginSnapshotDigest); err != nil {
		t.Fatalf("reuse verified artifact error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("artifact requests = %d, want immutable cache reuse", requests.Load())
	}
}

func TestPluginArtifactPreparationDropsFailedOptionalPolicyAndKeepsRequiredPolicy(t *testing.T) {
	goodPayload := []byte("required policy wasm")
	goodDigest := fmt.Sprintf("%x", sha256.Sum256(goodPayload))
	badPayload := []byte("optional policy wasm")
	badDigest := fmt.Sprintf("%x", sha256.Sum256(badPayload))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent-plugin-artifacts/artifact-optional":
			http.Error(w, "optional unavailable", http.StatusServiceUnavailable)
		case "/api/agent-plugin-artifacts/artifact-required":
			_, _ = w.Write(goodPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	requiredSnapshot := pluginArtifactSnapshot("artifact-required", goodDigest, int64(len(goodPayload)))
	optionalSnapshot := pluginArtifactSnapshot("artifact-optional", badDigest, int64(len(badPayload)))
	optional := optionalSnapshot.PluginPolicies[0]
	optional.ID = "optional"
	snapshot := requiredSnapshot
	snapshot.PluginPolicies = []model.PluginPolicy{optional, requiredSnapshot.PluginPolicies[0]}
	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "agent-secret", PluginCacheDir: t.TempDir(),
	}, server.Client())
	if err := client.preparePluginArtifacts(t.Context(), &snapshot, snapshot.Revision, testPluginSnapshotDigest); err != nil {
		t.Fatalf("preparePluginArtifacts() error = %v", err)
	}
	if len(snapshot.PluginPolicies) != 1 || snapshot.PluginPolicies[0].ID != "shared" {
		t.Fatalf("materialized policies = %+v, want only required policy", snapshot.PluginPolicies)
	}
	if snapshot.PluginPolicies[0].Stages[0].ArtifactPath == "" {
		t.Fatal("required policy was not materialized")
	}
}

func TestPluginArtifactPreparationAuthAndDigestFailuresPreserveLastKnownGood(t *testing.T) {
	oldPayload := []byte("last known good")
	oldDigest := fmt.Sprintf("%x", sha256.Sum256(oldPayload))
	newPayload := []byte("expected replacement")
	newDigest := fmt.Sprintf("%x", sha256.Sum256(newPayload))

	for _, test := range []struct {
		name  string
		token string
		body  []byte
	}{
		{name: "authentication", token: "wrong-token", body: newPayload},
		{name: "digest", token: "agent-secret", body: []byte("tampered")},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Agent-Token") != "agent-secret" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				_, _ = w.Write(test.body)
			}))
			t.Cleanup(server.Close)
			cacheRoot := t.TempDir()
			oldDir := filepath.Join(cacheRoot, "sha256", oldDigest[:2])
			if err := os.MkdirAll(oldDir, 0o700); err != nil {
				t.Fatal(err)
			}
			oldPath := filepath.Join(oldDir, oldDigest+".wasm")
			if err := os.WriteFile(oldPath, oldPayload, 0o600); err != nil {
				t.Fatal(err)
			}
			client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: test.token, PluginCacheDir: cacheRoot}, server.Client())
			snapshot := pluginArtifactSnapshot("artifact-new", newDigest, int64(len(newPayload)))
			if err := client.preparePluginArtifacts(t.Context(), &snapshot, snapshot.Revision, testPluginSnapshotDigest); err == nil {
				t.Fatal("preparePluginArtifacts() error = nil")
			}
			got, err := os.ReadFile(oldPath)
			if err != nil || string(got) != string(oldPayload) {
				t.Fatalf("last-known-good artifact changed: %q, error = %v", got, err)
			}
			if snapshot.PluginPolicies[0].Stages[0].ArtifactPath != "" {
				t.Fatal("failed preparation published a runtime artifact path")
			}
		})
	}
}

func pluginArtifactSnapshot(artifactID, digest string, size int64) model.Snapshot {
	return model.Snapshot{Revision: 1, Rules: []model.HTTPRule{{ID: 1, Enabled: true, PolicyRef: &model.PolicyRef{ID: "shared"}}}, PluginGenerations: []model.PluginGeneration{}, PluginDependencies: []model.PluginDependencyEdge{}, PluginPolicies: []model.PluginPolicy{{ID: "shared", Stages: []model.PolicyStage{{
		InstanceID: "instance-1", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: digest,
		ArtifactSource: model.PolicyArtifactSource{
			ArtifactID: artifactID, PackageIdentity: strings.Repeat("b", 64), PackageDigest: strings.Repeat("a", 64),
			RelativePath: "artifacts/policy.wasm", SHA256: digest, SizeBytes: size,
		},
	}}}}}
}
