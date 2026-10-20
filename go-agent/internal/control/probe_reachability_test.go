//go:build !integration

package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeTaskStampsExecutingNodeWithoutRewritingStatus(t *testing.T) {
	client := NewTaskClient(TaskClientConfig{
		AgentID: "edge-a",
		Handler: TaskHandlerFunc(func(_ context.Context, task TaskMessage) (map[string]any, error) {
			if task.TaskType != TaskTypeProbeBackendReachability {
				t.Fatalf("TaskType = %q", task.TaskType)
			}
			return map[string]any{
				ProbeResultTarget:    "http://10.0.0.8:8096/emby",
				ProbeResultCheckedAt: "2026-10-20T08:00:00Z",
				ProbeResultStatus:    ProbeStatusUntested,
				ProbeResultError:     "dial evidence is incomplete",
			}, nil
		}),
	})

	var completed map[string]any
	err := client.handleTaskMessage(context.Background(), TaskMessage{
		TaskID:   "probe-1",
		TaskType: TaskTypeProbeBackendReachability,
	}, func(_ context.Context, taskID string, payload map[string]any) error {
		if taskID != "probe-1" {
			t.Fatalf("task id = %s", taskID)
		}
		if payload["state"] == "completed" {
			completed, _ = payload["result"].(map[string]any)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("handleTaskMessage() error = %v", err)
	}
	if completed[ProbeResultAgentID] != "edge-a" {
		t.Fatalf("agent_id = %#v", completed[ProbeResultAgentID])
	}
	if completed[ProbeResultStatus] != ProbeStatusUntested {
		t.Fatalf("status = %#v, want untested", completed[ProbeResultStatus])
	}
}

func TestNonProbeTaskResultDoesNotGainProbeIdentity(t *testing.T) {
	client := NewTaskClient(TaskClientConfig{
		AgentID: "edge-a",
		Handler: TaskHandlerFunc(func(context.Context, TaskMessage) (map[string]any, error) {
			return map[string]any{"kind": "http"}, nil
		}),
	})
	var completed map[string]any
	if err := client.handleTaskMessage(context.Background(), TaskMessage{
		TaskID:   "diag-1",
		TaskType: TaskTypeDiagnoseHTTPRule,
	}, func(_ context.Context, _ string, payload map[string]any) error {
		if payload["state"] == "completed" {
			completed, _ = payload["result"].(map[string]any)
		}
		return nil
	}); err != nil {
		t.Fatalf("handleTaskMessage() error = %v", err)
	}
	if _, ok := completed[ProbeResultAgentID]; ok {
		t.Fatalf("diagnose result = %#v", completed)
	}
}

func TestSuccessfulHeartbeatDoesNotCreateProbeOrChangeRelayTrust(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/heartbeat" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"sync": {
				"has_update": false,
				"desired_revision": 7,
				"probe_backend_reachability": {"status": "connected", "agent_id": "heartbeat"},
				"relay_listeners": [{
					"id": 12,
					"agent_id": "service-1",
					"listen_host": "127.0.0.1",
					"listen_port": 12202,
					"public_host": "192.0.2.12",
					"public_port": 12202,
					"enabled": true,
					"tls_mode": "pin_and_ca",
					"pin_set": [{"type": "spki_sha256", "value": "pin-value"}],
					"trusted_ca_certificate_ids": [9],
					"allow_self_signed": true,
					"pki_identity_id": "identity-keep"
				}]
			}
		}`)
	}))
	defer server.Close()

	client := NewSyncClient(SyncClientConfig{
		MasterURL:  server.URL,
		AgentToken: "control-token",
		AgentID:    "edge-a",
	}, server.Client())
	snapshot, err := client.Sync(context.Background(), SyncRequest{CurrentRevision: 7})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if strings.Contains(requestBody, TaskTypeProbeBackendReachability) {
		t.Fatalf("heartbeat request carried a probe: %s", requestBody)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), TaskTypeProbeBackendReachability) || strings.Contains(string(encoded), `"status":"connected"`) {
		t.Fatalf("heartbeat snapshot produced a probe result: %s", encoded)
	}
	if len(snapshot.RelayListeners) != 1 {
		t.Fatalf("relay listeners = %#v", snapshot.RelayListeners)
	}
	listener := snapshot.RelayListeners[0]
	if listener.TLSMode != "pin_and_ca" || !listener.AllowSelfSigned || listener.PKIIdentityID != "identity-keep" {
		t.Fatalf("relay trust changed: %+v", listener)
	}
	if len(listener.PinSet) != 1 || listener.PinSet[0].Type != "spki_sha256" || listener.PinSet[0].Value != "pin-value" {
		t.Fatalf("pin set changed: %+v", listener.PinSet)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 9 {
		t.Fatalf("trusted CAs changed: %+v", listener.TrustedCACertificateIDs)
	}
}
