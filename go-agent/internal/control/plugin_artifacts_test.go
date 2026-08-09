package control

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

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
		w.Header().Set("Content-Type", "application/wasm")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	cacheRoot := t.TempDir()
	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "agent-secret", PluginCacheDir: cacheRoot,
	}, server.Client())
	snapshot := pluginArtifactSnapshot("artifact-1", digest, int64(len(payload)))
	if err := client.preparePluginArtifacts(t.Context(), &snapshot); err != nil {
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
	if err := client.preparePluginArtifacts(t.Context(), &snapshot); err != nil {
		t.Fatalf("reuse verified artifact error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("artifact requests = %d, want immutable cache reuse", requests.Load())
	}
}

func TestPullRevisionMaterializesEmptyURLArtifactAfterVerifyingPersistedSnapshot(t *testing.T) {
	payload := []byte("persisted revision wasm")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	snapshot := pluginArtifactSnapshot("artifact-revision", digest, int64(len(payload)))
	snapshot.DesiredVersion = "1.2.3"
	snapshot.Revision = 7
	snapshot.Rules = []model.HTTPRule{}
	snapshot.L4Rules = []model.L4Rule{}
	snapshot.EgressProfiles = []model.EgressProfile{}
	snapshot.RelayListeners = []model.RelayListener{}
	snapshot.Certificates = []model.ManagedCertificateBundle{}
	snapshot.CertificatePolicies = []model.ManagedCertificatePolicy{}
	rawSnapshot, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshotDigest := fmt.Sprintf("%x", sha256.Sum256(rawSnapshot))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent-revisions/pull":
			fmt.Fprintf(w, `{"revision":{"has_update":true,"desired_revision":7,"lease":{"agent_id":"edge-1","revision":7,"attempt":1,"lease_id":"lease-7","snapshot_digest":"%s","desired_version":"1.2.3","apply_timeout_seconds":30,"drain_timeout_seconds":15,"deadline_at":"%s"},"snapshot":%s}}`, snapshotDigest, time.Now().Add(time.Minute).UTC().Format(time.RFC3339), rawSnapshot)
		case "/api/agent-plugin-artifacts/artifact-revision":
			if r.Header.Get("X-Agent-Token") != "agent-secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "agent-secret", AgentID: "edge-1", PluginCacheDir: t.TempDir(),
	}, server.Client())
	pull, err := client.PullRevision(t.Context())
	if err != nil {
		t.Fatalf("PullRevision() error = %v", err)
	}
	if pull.VerifiedSnapshotDigest != snapshotDigest || pull.Snapshot == nil {
		t.Fatalf("pull identity = %+v", pull)
	}
	localPath := pull.Snapshot.PluginPolicies[0].Stages[0].ArtifactPath
	if got, readErr := os.ReadFile(localPath); readErr != nil || string(got) != string(payload) {
		t.Fatalf("materialized revision artifact = %q, error = %v", got, readErr)
	}
}

func TestPluginArtifactPreparationSerializesConcurrentPublication(t *testing.T) {
	payload := []byte("one immutable artifact")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "agent-secret", PluginCacheDir: t.TempDir()}, server.Client())
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			snapshot := pluginArtifactSnapshot("artifact-concurrent", digest, int64(len(payload)))
			errors <- client.preparePluginArtifacts(t.Context(), &snapshot)
		}()
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatalf("concurrent preparation error = %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("concurrent artifact requests = %d, want one", requests.Load())
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
			if err := client.preparePluginArtifacts(t.Context(), &snapshot); err == nil {
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
	return model.Snapshot{PluginPolicies: []model.PluginPolicy{{ID: "shared", Stages: []model.PolicyStage{{
		InstanceID: "instance-1", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: digest,
		ArtifactSource: model.PolicyArtifactSource{
			ArtifactID: artifactID, PackageIdentity: strings.Repeat("b", 64), PackageDigest: strings.Repeat("a", 64),
			RelativePath: "artifacts/policy.wasm", SHA256: digest, SizeBytes: size,
		},
	}}}}}
}
