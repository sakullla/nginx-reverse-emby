package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestControlProtocolUnchangedUsesTokenWithoutTunnelClientCertificate(t *testing.T) {
	facts := &controlProtocolFacts{paths: make(map[string]int)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		facts.record(request)
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/agents/heartbeat":
			_ = json.NewEncoder(response).Encode(map[string]any{"sync": map[string]any{
				"desired_revision": 7, "agent_config": map[string]any{},
				"rules": []model.HTTPRule{}, "l4_rules": []model.L4Rule{},
				"relay_listeners": []model.RelayListener{}, "egress_profiles": []model.EgressProfile{},
				"certificates": []model.ManagedCertificateBundle{}, "certificate_policies": []model.ManagedCertificatePolicy{},
			}})
		case "/api/agent-revisions/pull":
			_ = json.NewEncoder(response).Encode(map[string]any{"revision": map[string]any{
				"has_update": false, "desired_revision": 7,
			}})
		case "/api/agents/task-stream":
			response.Header().Set("Content-Type", "application/x-ndjson")
		default:
			http.NotFound(response, request)
		}
	}))
	// Request a certificate so the assertion proves the control clients do not
	// carry the relay tunnel credential. Authentication remains X-Agent-Token.
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequestClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)

	syncClient := NewSyncClient(SyncClientConfig{
		MasterURL: server.URL, AgentID: "edge-control", AgentName: "edge-control", AgentToken: "control-token",
	}, server.Client())
	if _, err := syncClient.Sync(t.Context(), SyncRequest{}); err != nil {
		t.Fatalf("heartbeat Sync() error = %v", err)
	}
	if _, err := syncClient.PullRevision(t.Context()); err != nil {
		t.Fatalf("PullRevision() error = %v", err)
	}
	taskClient := NewTaskClient(TaskClientConfig{
		MasterURL: server.URL, AgentID: "edge-control", AgentName: "edge-control", AgentToken: "control-token",
		HTTPClient: server.Client(), Handler: TaskHandlerFunc(func(context.Context, TaskMessage) (map[string]any, error) {
			return nil, nil
		}),
	})
	if err := taskClient.probeStreamSession(t.Context(), "control-probe"); err != nil {
		t.Fatalf("task stream probe error = %v", err)
	}

	paths, tokenFailures, clientCertificates := facts.snapshot()
	for _, path := range []string{"/api/agents/heartbeat", "/api/agent-revisions/pull", "/api/agents/task-stream"} {
		if paths[path] != 1 {
			t.Fatalf("control request count for %s = %d, all paths = %+v", path, paths[path], paths)
		}
	}
	if tokenFailures != 0 || clientCertificates != 0 {
		t.Fatalf("control authentication token_failures=%d client_certificates=%d", tokenFailures, clientCertificates)
	}
}

type controlProtocolFacts struct {
	mu                 sync.Mutex
	paths              map[string]int
	tokenFailures      int
	clientCertificates int
}

func (f *controlProtocolFacts) record(request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths[request.URL.Path]++
	if request.Header.Get("X-Agent-Token") != "control-token" {
		f.tokenFailures++
	}
	if request.TLS != nil {
		f.clientCertificates += len(request.TLS.PeerCertificates)
	}
}

func (f *controlProtocolFacts) snapshot() (map[string]int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := make(map[string]int, len(f.paths))
	for path, count := range f.paths {
		paths[path] = count
	}
	return paths, f.tokenFailures, f.clientCertificates
}
