//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRemoteOnlyControlPlaneStartsDDNSReconciliation(t *testing.T) {
	if testing.Short() {
		t.Skip("full control-plane lifecycle runs in the full test tier")
	}
	cloudflareCall := make(chan struct{}, 1)
	cloudflare := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			_, _ = writer.Write([]byte(`{"success":true,"result":[{"id":"zone-1","name":"example.com"}],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone-1/dns_records":
			_, _ = writer.Write([]byte(`{"success":true,"result":[],"result_info":{"total_pages":1}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone-1/dns_records":
			select {
			case cloudflareCall <- struct{}{}:
			default:
			}
			_, _ = writer.Write([]byte(`{"success":true,"result":{"id":"record-1"}}`))
		default:
			http.Error(writer, "unexpected Cloudflare request", http.StatusNotFound)
		}
	}))
	t.Cleanup(cloudflare.Close)

	previousHandlerFactory := newHandlerWithDependencies
	var deps httpapi.Dependencies
	newHandlerWithDependencies = func(_ config.Config, input httpapi.Dependencies) (http.Handler, error) {
		deps = input
		return http.NewServeMux(), nil
	}
	t.Cleanup(func() { newHandlerWithDependencies = previousHandlerFactory })

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false
	cfg.DDNS.Enabled = true
	cfg.DDNS.Token = "cloudflare-token"
	cfg.DDNS.APIBase = cloudflare.URL
	cfg.DDNS.Timeout = time.Second
	cfg.DDNS.Interval = time.Hour
	cfg.DDNS.TTL = 120
	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = application.Run(ctx)
	})
	if deps.AgentService == nil {
		t.Fatal("remote-only handler did not receive the shared agent service")
	}

	agent, err := deps.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "remote-edge", AgentToken: "agent-token", Capabilities: []string{"http_rules"}, HasCapabilities: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	ddnsConfig := storage.DDNSConfig{
		Enabled: true, Domain: "media.example.com",
		IPv4: storage.DDNSFamily{Enabled: true, Source: "public_api"},
	}
	if _, err := deps.AgentService.Update(t.Context(), agent.ID, service.UpdateAgentRequest{DdnsConfig: &ddnsConfig}); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.AgentService.Heartbeat(t.Context(), service.HeartbeatRequest{
		LastSeenIPv4: "203.0.113.10", CurrentRevision: 0,
	}, "agent-token"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-cloudflareCall:
	case <-time.After(2 * time.Second):
		t.Fatal("remote-only heartbeat did not trigger Cloudflare reconciliation")
	}
}
