package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIBlockedBootstrapKeepsActualControlRoutesOnline(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false
	cfg.PanelToken = "panel-secret"
	cfg.RegisterToken = "register-secret"
	t.Cleanup(func() {
		cacheRoot := filepath.Join(cfg.DataDir, "plugins", "packages")
		if err := marketplace.DiscardVerifiedCacheRoot(cacheRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("discard verified cache root: %v", err)
		}
	})

	realHandlerFactory := newHandlerWithDependencies
	previousRuntimeFactory := newControlPlanePKIRuntimeFactory
	previousStartupWait := controlPlanePKIStartupWait
	previousAttemptTimeout := controlPlanePKIAttemptTimeout
	t.Cleanup(func() {
		newHandlerWithDependencies = realHandlerFactory
		newControlPlanePKIRuntimeFactory = previousRuntimeFactory
		controlPlanePKIStartupWait = previousStartupWait
		controlPlanePKIAttemptTimeout = previousAttemptTimeout
	})
	var handler http.Handler
	newHandlerWithDependencies = func(cfg config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		resolved, err := realHandlerFactory(cfg, deps)
		handler = resolved
		return resolved, err
	}
	newControlPlanePKIRuntimeFactory = func(ctx context.Context, _ config.Config, _ *storage.GormStore, _ *service.TaskService, _ service.PKIActivationFinalizer, _ *log.Logger, _ string) (controlPlanePKIRuntime, error) {
		<-ctx.Done()
		return controlPlanePKIRuntime{}, ctx.Err()
	}
	controlPlanePKIStartupWait = 50 * time.Millisecond
	controlPlanePKIAttemptTimeout = 100 * time.Millisecond

	started := time.Now()
	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("bounded PKI bootstrap delayed control application for %v", time.Since(started))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = application.Run(ctx)
	})
	if handler == nil {
		t.Fatal("actual control router was not constructed")
	}

	register := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"name":"edge","agent_token":"stable-control-token","register_token":"register-secret"}`))
	registerResponse := httptest.NewRecorder()
	handler.ServeHTTP(registerResponse, register)
	if registerResponse.Code != http.StatusOK {
		t.Fatalf("legacy registration while PKI blocked = %d, body=%s", registerResponse.Code, registerResponse.Body.String())
	}

	heartbeat := httptest.NewRequest(http.MethodPost, "/api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":0}`))
	heartbeat.Header.Set("X-Agent-Token", "stable-control-token")
	heartbeatResponse := httptest.NewRecorder()
	handler.ServeHTTP(heartbeatResponse, heartbeat)
	if heartbeatResponse.Code != http.StatusOK {
		t.Fatalf("heartbeat while PKI blocked = %d, body=%s", heartbeatResponse.Code, heartbeatResponse.Body.String())
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/agent-revisions/pull"},
		{method: http.MethodHead, path: "/api/agents/task-stream"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		request.Header.Set("X-Agent-Token", "stable-control-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusNotFound || response.Code == http.StatusServiceUnavailable {
			t.Fatalf("existing control route %s %s unavailable: %d, body=%s", route.method, route.path, response.Code, response.Body.String())
		}
	}

	overview := httptest.NewRequest(http.MethodGet, "/api/pki/overview", nil)
	overview.Header.Set("X-Panel-Token", cfg.PanelToken)
	overviewResponse := httptest.NewRecorder()
	handler.ServeHTTP(overviewResponse, overview)
	if overviewResponse.Code != http.StatusOK || !bytes.Contains(overviewResponse.Body.Bytes(), []byte(`"runtime_status":"degraded"`)) {
		t.Fatalf("degraded PKI overview = %d, body=%s", overviewResponse.Code, overviewResponse.Body.String())
	}
}

func TestPKIFollowerPromotesAfterLeaseOwnerStops(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = false

	previousRetryInterval := controlPlanePKIRetryInterval
	previousAttemptTimeout := controlPlanePKIAttemptTimeout
	controlPlanePKIRetryInterval = 25 * time.Millisecond
	controlPlanePKIAttemptTimeout = time.Second
	t.Cleanup(func() {
		controlPlanePKIRetryInterval = previousRetryInterval
		controlPlanePKIAttemptTimeout = previousAttemptTimeout
	})

	newRuntime := func() (*storage.GormStore, *service.TaskService, *service.DegradedPKIService, *controlPlanePKISupervisor) {
		store, err := openConfiguredStore(cfg)
		if err != nil {
			t.Fatalf("open shared control store: %v", err)
		}
		tasks := service.NewTaskService(service.TaskServiceConfig{})
		proxy := service.NewDegradedPKIService(service.ErrPKIRuntimeUnavailable)
		relay := service.NewRelayListenerService(cfg, store)
		supervisor := newControlPlanePKISupervisor(cfg, store, tasks, relay, proxy, nil)
		t.Cleanup(func() {
			_ = supervisor.Close()
			_ = tasks.Close()
			_ = store.Close()
		})
		return store, tasks, proxy, supervisor
	}
	_, _, ownerProxy, owner := newRuntime()
	ownerCtx, cancelOwnerBootstrap := context.WithTimeout(context.Background(), 5*time.Second)
	if err := owner.Bootstrap(ownerCtx); err != nil {
		cancelOwnerBootstrap()
		t.Fatalf("owner Bootstrap() error = %v", err)
	}
	cancelOwnerBootstrap()
	if overview, err := ownerProxy.Overview(t.Context()); err != nil || overview.RuntimeStatus != "ready" {
		t.Fatalf("owner overview = %+v, error = %v", overview, err)
	}

	_, _, followerProxy, follower := newRuntime()
	followerBootstrapCtx, cancelFollowerBootstrap := context.WithTimeout(context.Background(), time.Second)
	if err := follower.Bootstrap(followerBootstrapCtx); err == nil {
		cancelFollowerBootstrap()
		t.Fatal("follower unexpectedly acquired the live owner's PKI lease")
	}
	cancelFollowerBootstrap()
	if overview, err := followerProxy.Overview(t.Context()); err != nil || overview.RuntimeStatus != "degraded" {
		t.Fatalf("contending follower overview = %+v, error = %v", overview, err)
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- follower.Run(runCtx) }()
	if err := owner.Close(); err != nil {
		cancelRun()
		t.Fatalf("owner Close() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		overview, err := followerProxy.Overview(t.Context())
		if err == nil && overview.RuntimeStatus == "ready" {
			break
		}
		if time.Now().After(deadline) {
			cancelRun()
			t.Fatalf("follower did not promote after owner shutdown: overview=%+v error=%v", overview, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancelRun()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follower Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follower Run() did not stop")
	}
}

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
	systemCtx := service.WithSystemMutationPrincipal(t.Context(), "system:pki-test")
	listener, err := healthy.RelayListenerService.Create(systemCtx, agent.ID, service.RelayListenerInput{
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
	if _, err := degraded.RelayListenerService.Delete(systemCtx, agent.ID, listener.ID); !errors.Is(err, service.ErrPKIRuntimeUnavailable) {
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
