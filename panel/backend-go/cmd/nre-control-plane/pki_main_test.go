package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
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
	if captured.PKIService == nil || captured.AgentService == nil {
		t.Fatalf("degraded dependencies: PKI nil=%v agent nil=%v", captured.PKIService == nil, captured.AgentService == nil)
	}
	overview, err := captured.PKIService.Overview(t.Context())
	if err != nil || overview.RuntimeStatus != "degraded" || overview.RecoveryBlocker == nil || overview.RecoveryBlocker.Code == "" {
		t.Fatalf("degraded overview = %+v, error = %v", overview, err)
	}
	agent, err := captured.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "degraded-edge", AgentToken: "existing-control-token",
	}, "")
	if err != nil {
		t.Fatalf("token registration while PKI degraded = %v", err)
	}
	heartbeat, err := captured.AgentService.Heartbeat(t.Context(), service.HeartbeatRequest{}, "existing-control-token")
	if err != nil {
		t.Fatalf("token heartbeat while PKI degraded = %v", err)
	}
	if heartbeat.PKIStatus == nil || heartbeat.PKIStatus.Status != "degraded" || len(heartbeat.RelayListeners) != 0 {
		t.Fatalf("degraded heartbeat exposed relay PKI state: %+v", heartbeat)
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
	enrollmentToken, err := healthy.PKIService.CreateEnrollmentToken(t.Context(), service.PKIEnrollmentTokenRequest{
		Scope: service.PKIEnrollmentTokenScopeNewAgent, CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("CreateEnrollmentToken() error = %v", err)
	}
	agent, err := healthy.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "existing-edge", RegisterToken: enrollmentToken.Token,
		PKIEnrollmentRequestID: "existing-edge-registration", TunnelCSRPEM: mustAnonymousTunnelCSR(t),
	}, "")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if agent.RegistrationControlToken == "" {
		t.Fatal("PKI registration returned no stable control token")
	}
	if _, err := healthy.AgentService.Register(t.Context(), service.RegisterRequest{
		Name: "static-token-takeover", AgentToken: "attacker-chosen-token",
	}, ""); !errors.Is(err, service.ErrAgentUnauthorized) {
		t.Fatalf("static registration takeover error = %v", err)
	}
	updatedAgent, err := healthy.AgentService.Register(t.Context(), service.RegisterRequest{
		AgentID: agent.ID, Name: "existing-edge-updated", AgentToken: agent.RegistrationControlToken,
	}, agent.RegistrationControlToken)
	if err != nil || updatedAgent.ID != agent.ID {
		t.Fatalf("authenticated registration update = %+v, error = %v", updatedAgent, err)
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
	if degraded.PKIService == nil {
		t.Fatal("degraded PKI overview service is unavailable")
	}
	if _, err := degraded.RelayListenerService.Delete(t.Context(), agent.ID, listener.ID); !errors.Is(err, service.ErrPKIEnrollmentAuthorityUnavailable) {
		t.Fatalf("Delete(listener) error = %v, want PKI runtime unavailable", err)
	}
	listeners, err := degraded.RelayListenerService.List(t.Context(), agent.ID)
	if err != nil || len(listeners) != 1 || listeners[0].ID != listener.ID {
		t.Fatalf("listeners after fenced deletion = %+v, error = %v", listeners, err)
	}
}

func mustAnonymousTunnelCSR(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest() error = %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}
