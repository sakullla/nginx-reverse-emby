package control

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPullRevisionResolvesPackageURLAfterDigestVerification(t *testing.T) {
	snapshotPayload := []byte(`{"desired_version":"2.0.0","desired_revision":1,"version_package":{"url":"/downloads/nre-agent?token=signed","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","platform":"linux-amd64","filename":"nre-agent","size":5},"agent_config":{},"rules":[],"l4_rules":[],"egress_profiles":[],"relay_listeners":[],"wireguard_profiles":[],"certificates":[],"certificate_policies":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(snapshotPayload))
	lease := model.RevisionLease{
		AgentID: "edge-1", Revision: 1, Attempt: 1, LeaseID: "lease-1", SnapshotDigest: digest,
		DesiredVersion: "2.0.0", ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
		DeadlineAt: time.Now().UTC().Add(time.Minute),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/agent-revisions/pull" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"revision": map[string]any{
				"has_update": true, "desired_revision": 1, "lease": lease,
				"snapshot": json.RawMessage(snapshotPayload),
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL + "/panel-api", AgentID: "edge-1", AgentToken: "token",
	}, server.Client())
	pull, err := client.PullRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if pull.Snapshot == nil || pull.Snapshot.VersionPackage == nil {
		t.Fatal("revision package is missing")
	}
	if got, want := pull.Snapshot.VersionPackage.URL, server.URL+"/downloads/nre-agent?token=signed"; got != want {
		t.Fatalf("resolved package URL = %q, want %q", got, want)
	}
	if pull.VerifiedSnapshotDigest != digest {
		t.Fatalf("verified digest = %q, want %q", pull.VerifiedSnapshotDigest, digest)
	}
}
