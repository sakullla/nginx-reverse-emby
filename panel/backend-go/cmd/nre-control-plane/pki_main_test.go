package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestPKIProductionRuntimeUsesExistingControlPlaneApplication(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false

	previousNewHandlerWithDependencies := newHandlerWithDependencies
	t.Cleanup(func() { newHandlerWithDependencies = previousNewHandlerWithDependencies })
	var captured httpapi.Dependencies
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		captured = deps
		return http.NewServeMux(), nil
	}
	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}
	if captured.PKIService == nil || captured.AgentService == nil || captured.TaskService == nil {
		t.Fatalf("production PKI dependencies are missing")
	}
	overview, err := captured.PKIService.Overview(t.Context())
	if err != nil || overview.PKIDomainID == "" || overview.PKIEpoch != 1 {
		t.Fatalf("PKI overview = %+v, error = %v", overview, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestPKIDamagedMasterKeyKeepsTokenControlProtocolOnline(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false
	cfg.PKIMasterKeyFile = filepath.Join(cfg.DataDir, "damaged-master.key")
	if err := os.WriteFile(cfg.PKIMasterKeyFile, []byte("not-a-valid-32-byte-master-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousNewHandlerWithDependencies := newHandlerWithDependencies
	t.Cleanup(func() { newHandlerWithDependencies = previousNewHandlerWithDependencies })
	var captured httpapi.Dependencies
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		captured = deps
		return http.NewServeMux(), nil
	}
	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = application.Run(ctx)
	})
	if captured.PKIService != nil || captured.AgentService == nil {
		t.Fatalf("degraded dependencies: PKI nil=%v agent nil=%v", captured.PKIService == nil, captured.AgentService == nil)
	}
	agent, err := captured.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "degraded-edge", AgentToken: "existing-control-token",
	}, "")
	if err != nil {
		t.Fatalf("token registration while PKI degraded = %v", err)
	}
	if _, err := captured.AgentService.Heartbeat(t.Context(), service.HeartbeatRequest{}, "existing-control-token"); err != nil {
		t.Fatalf("token heartbeat while PKI degraded = %v", err)
	}
	if agent.ID == "" {
		t.Fatal("degraded registration returned empty agent ID")
	}
}

func TestPKIDamagedMasterKeyFencesExistingListenerDeletion(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false

	previousNewHandlerWithDependencies := newHandlerWithDependencies
	t.Cleanup(func() { newHandlerWithDependencies = previousNewHandlerWithDependencies })
	var healthy httpapi.Dependencies
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		healthy = deps
		return http.NewServeMux(), nil
	}
	healthyApp, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp(healthy) error = %v", err)
	}
	agent, err := healthy.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "existing-edge", AgentToken: "existing-control-token",
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	name, listenHost, publicHost, port := "existing-relay", "0.0.0.0", "relay.example.test", 9443
	listener, err := healthy.RelayListenerService.Create(t.Context(), agent.ID, service.RelayListenerInput{
		Name: &name, ListenHost: &listenHost, ListenPort: &port, PublicHost: &publicHost, PublicPort: &port,
	})
	if err != nil {
		t.Fatalf("Create(listener) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := healthyApp.Run(ctx); err != nil {
		t.Fatalf("Run(healthy shutdown) error = %v", err)
	}

	masterKeyFile := filepath.Join(cfg.DataDir, "pki", "master.key")
	if err := os.WriteFile(masterKeyFile, []byte("not-a-valid-32-byte-master-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	var degraded httpapi.Dependencies
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		degraded = deps
		return http.NewServeMux(), nil
	}
	degradedApp, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp(degraded) error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = degradedApp.Run(ctx)
	})
	if degraded.PKIService != nil {
		t.Fatal("degraded PKI service is unexpectedly available")
	}
	if _, err := degraded.RelayListenerService.Delete(t.Context(), agent.ID, listener.ID); !errors.Is(err, service.ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("Delete(listener) error = %v, want PKI runtime unavailable", err)
	}
	listeners, err := degraded.RelayListenerService.List(t.Context(), agent.ID)
	if err != nil || len(listeners) != 1 || listeners[0].ID != listener.ID {
		t.Fatalf("listeners after fenced deletion = %+v, error = %v", listeners, err)
	}
}
