//go:build !integration

package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRevisionProtocolUsesAuthenticatedPathsAndPayloads(t *testing.T) {
	snapshotJSON := []byte(`{"desired_version":"1.2.3","desired_revision":7,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"egress_profiles":[],"certificates":[],"certificate_policies":[],"plugin_generations":[],"plugin_dependencies":[],"plugin_policies":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(snapshotJSON))
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Second)

	type expectedRequest struct {
		path string
		body any
	}
	expected := []expectedRequest{
		{path: "/api/agent-revisions/pull"},
		{path: "/api/agent-revisions/7/start", body: model.RevisionStart{
			AgentID: "edge-1", Revision: 7, RetryCycle: 2, Attempt: 3,
			LeaseID: "lease-7", GenerationID: "generation-7",
		}},
		{path: "/api/agent-revisions/7/report", body: model.RevisionReport{
			AgentID: "edge-1", Revision: 7, RetryCycle: 2, Attempt: 3,
			LeaseID: "lease-7", GenerationID: "generation-7", Status: "applied",
		}},
	}
	requestIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIndex >= len(expected) {
			t.Fatalf("unexpected revision request %s", r.URL.Path)
		}
		want := expected[requestIndex]
		requestIndex++
		if r.Method != http.MethodPost || r.URL.Path != want.path {
			t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, want.path)
		}
		if got := r.Header.Get("X-Agent-Token"); got != "agent-secret" {
			t.Fatalf("X-Agent-Token = %q, want agent-secret", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if want.body == nil {
			if len(body) != 0 {
				t.Fatalf("pull body = %s, want empty", body)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"revision":{"has_update":true,"desired_revision":7,"lease":{"agent_id":"edge-1","revision":7,"retry_cycle":2,"attempt":3,"lease_id":"lease-7","snapshot_digest":"%s","desired_version":"1.2.3","apply_timeout_seconds":30,"drain_timeout_seconds":15,"deadline_at":"%s"},"snapshot":%s}}`, digest, deadline.Format(time.RFC3339), snapshotJSON)
			return
		}
		wantBody, err := json.Marshal(want.body)
		if err != nil {
			t.Fatalf("marshal expected body: %v", err)
		}
		if !bytes.Equal(body, wantBody) {
			t.Fatalf("body = %s, want %s", body, wantBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentToken: "agent-secret", AgentID: "edge-1",
	}, server.Client())
	pull, err := client.PullRevision(t.Context())
	if err != nil {
		t.Fatalf("PullRevision() error = %v", err)
	}
	if pull.Snapshot == nil || pull.Snapshot.Revision != 7 || pull.VerifiedSnapshotDigest != digest {
		t.Fatalf("PullRevision() = %+v, want verified revision 7", pull)
	}
	if err := client.StartRevision(t.Context(), expected[1].body.(model.RevisionStart)); err != nil {
		t.Fatalf("StartRevision() error = %v", err)
	}
	if err := client.ReportRevision(t.Context(), expected[2].body.(model.RevisionReport)); err != nil {
		t.Fatalf("ReportRevision() error = %v", err)
	}
	if requestIndex != len(expected) {
		t.Fatalf("request count = %d, want %d", requestIndex, len(expected))
	}
}

func TestPullRevisionRejectsSnapshotWhoseRawDigestDoesNotMatchLease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revision":{"has_update":true,"desired_revision":7,"lease":{"agent_id":"edge-1","revision":7,"attempt":1,"lease_id":"lease-7","snapshot_digest":"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff","apply_timeout_seconds":60,"drain_timeout_seconds":600,"deadline_at":"2099-01-01T00:00:00Z"},"snapshot":{"desired_version":"1.2.3","desired_revision":7,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"egress_profiles":[],"certificates":[],"certificate_policies":[]}}}`))
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "agent-secret"}, server.Client())
	_, err := client.PullRevision(t.Context())
	if err == nil || !strings.Contains(err.Error(), "snapshot digest") {
		t.Fatalf("PullRevision() error = %v, want snapshot digest mismatch", err)
	}
}

func TestHeartbeatPluginLogsAreAcknowledgedOnlyAfterAuthenticatedDecodedSuccess(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "non-2xx", statusCode: http.StatusUnauthorized, body: `{"message":"unauthorized"}`},
		{name: "malformed response", statusCode: http.StatusOK, body: `{`},
		{name: "malformed sync", statusCode: http.StatusOK, body: `{"sync":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			acknowledged := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "token", AgentID: "edge-1"}, server.Client())
			_, err := client.Sync(t.Context(), SyncRequest{
				PluginLogs: []model.PluginRuntimeLogReport{testPluginRuntimeLogReport(1)},
				PluginLogsAcknowledged: func() error {
					acknowledged++
					return nil
				},
			})
			if err == nil {
				t.Fatal("Sync() accepted failed heartbeat")
			}
			if acknowledged != 0 {
				t.Fatalf("failed heartbeat acknowledged %d plugin log batches", acknowledged)
			}
		})
	}
}

func testPluginRuntimeLogReport(sequence uint64) model.PluginRuntimeLogReport {
	return model.PluginRuntimeLogReport{
		Revision: 7, GenerationID: "generation-7", InstanceID: "instance-7", PluginID: "example.rpc", AgentID: "edge-1",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Sequence: sequence,
		Entries: []model.PluginRuntimeLogEntry{{Level: "info", Message: "safe"}},
	}
}

func TestSyncClientRedeemPluginSecretsDoesNotExposeResponseBody(t *testing.T) {
	const secret = "never-expose-response-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, secret, http.StatusForbidden)
	}))
	defer server.Close()
	client := NewSyncClient(SyncClientConfig{MasterURL: server.URL, AgentToken: "agent-token"}, server.Client())
	_, err := client.RedeemPluginSecrets(t.Context(), model.PluginSecretRedemptionRequest{
		Revision: 7, GenerationID: "generation-a", InstanceID: "instance-a", PluginID: "example.rpc", OperationID: "operation-a",
		PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64),
		Handles: []model.PluginSecretHandle{{ID: "secret-a", Version: 2, Digest: strings.Repeat("c", 64), Purpose: "plugin-config:instance-a:/token"}},
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("redemption error = %v", err)
	}
}

func TestHeartbeatSyncPreservesExplicitEmptyCertificatePayloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sync":{"desired_version":"1.2.3","desired_revision":7,"certificates":[],"certificate_policies":[]}}`)
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL:      server.URL,
		AgentToken:     "token",
		AgentID:        "node",
		AgentName:      "local",
		CurrentVersion: "0.1.0",
		Platform:       "linux-amd64",
	}, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := client.Sync(ctx, SyncRequest{CurrentRevision: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if snap.Certificates == nil || len(snap.Certificates) != 0 {
		t.Fatalf("expected explicit empty certificates slice, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies == nil || len(snap.CertificatePolicies) != 0 {
		t.Fatalf("expected explicit empty certificate policies slice, got %+v", snap.CertificatePolicies)
	}
}
