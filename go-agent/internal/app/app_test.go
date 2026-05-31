package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	modulehttp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
	agenttask "github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)

func TestNewBuildsRealWiring(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, ok := app.store.(*store.Filesystem); !ok {
		t.Fatalf("expected filesystem store, got %T", app.store)
	}
	if app.syncClient == nil {
		t.Fatal("expected sync client to be initialized")
	}
	if app.httpModule == nil {
		t.Fatal("expected http module to be initialized")
	}
	if app.certApplier == nil {
		t.Fatal("expected certificate applier to be initialized")
	}
	if app.l4Module == nil {
		t.Fatal("expected l4 module to be initialized")
	}
	if app.relayApplier == nil {
		t.Fatal("expected relay applier to be initialized")
	}
}

func TestNewRegistersModulesWhenDependenciesExist(t *testing.T) {
	tests := []struct {
		name              string
		wireGuardEnabled  bool
		wireGuardExplicit bool
		wantNames         []string
	}{
		{
			name:              "explicit enabled",
			wireGuardEnabled:  true,
			wireGuardExplicit: true,
			wantNames:         []string{"certs", "diagnostics", "egress", "http", "wireguard", "relay", "l4", "traffic"},
		},
		{
			name:              "implicit default",
			wireGuardEnabled:  false,
			wireGuardExplicit: false,
			wantNames:         []string{"certs", "diagnostics", "egress", "http", "wireguard", "relay", "l4", "traffic"},
		},
		{
			name:              "explicit disabled",
			wireGuardEnabled:  false,
			wireGuardExplicit: true,
			wantNames:         []string{"certs", "diagnostics", "egress", "http", "relay", "l4", "traffic"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				AgentID:           "agent",
				AgentName:         "agent",
				MasterURL:         "https://master.example.com",
				AgentToken:        "token",
				CurrentVersion:    "0.1.0",
				DataDir:           t.TempDir(),
				WireGuardEnabled:  tc.wireGuardEnabled,
				WireGuardExplicit: tc.wireGuardExplicit,
			}
			app, err := New(cfg)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer app.Close()

			registry := extractPrivateField(t, app, "moduleRegistry").Interface().(*agentmodule.Registry)
			if registry == nil {
				t.Fatal("moduleRegistry = nil")
			}
			if got := registry.Names(); !reflect.DeepEqual(got, tc.wantNames) {
				t.Fatalf("module registry names = %+v, want %+v", got, tc.wantNames)
			}
		})
	}
}

func TestNewUsesRegisteredAdapterModulesAsAppDependencies(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer app.Close()

	registry := extractPrivateField(t, app, "moduleRegistry").Interface().(*agentmodule.Registry)
	certModule := extractPrivateField(t, app, "certModule").Interface().(*modulecerts.Module)
	diagnosticModule := extractPrivateField(t, app, "diagnosticModule").Interface().(*modulediagnostics.Module)
	egressModule := extractPrivateField(t, app, "egressModule").Interface().(*moduleegress.Module)
	l4Module := extractPrivateField(t, app, "l4Module").Interface().(*l4.Module)

	if _, ok := app.certApplier.(*modulecerts.Manager); !ok {
		t.Fatalf("certApplier = %T, want cert manager", app.certApplier)
	}
	relayModule := extractPrivateField(t, app, "relayModule").Interface().(*relay.Module)
	if app.relayApplier != relayModule {
		t.Fatal("relay applier does not come from retained relay module")
	}

	if got := registryModuleByName(registry, "certs"); got != certModule {
		t.Fatal("registry certs module is not the retained cert module")
	}
	if got := registryModuleByName(registry, "diagnostics"); got != diagnosticModule {
		t.Fatal("registry diagnostics module is not the retained diagnostics module")
	}
	if got := registryModuleByName(registry, "egress"); got != egressModule {
		t.Fatal("registry egress module is not the retained egress module")
	}
	if got := registryModuleByName(registry, "relay"); got != relayModule {
		t.Fatal("registry relay module is not the retained relay module")
	}
	if got := registryModuleByName(registry, "l4"); got != l4Module {
		t.Fatal("registry l4 module is not the retained l4 module")
	}
}

func TestNewPropagatesHTTPTransportConfigToSyncAndTaskClients(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
		HTTPTransport: config.HTTPTransportConfig{
			DialTimeout:           21 * time.Second,
			TLSHandshakeTimeout:   22 * time.Second,
			ResponseHeaderTimeout: 23 * time.Second,
			IdleConnTimeout:       24 * time.Second,
			KeepAlive:             25 * time.Second,
		},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	syncClient, ok := app.syncClient.(*agentsync.Client)
	if !ok {
		t.Fatalf("syncClient type = %T", app.syncClient)
	}
	syncClientTransport := extractPrivateTransport(t, syncClient)
	if syncClientTransport == nil {
		t.Fatal("expected sync transport to be initialized")
	}
	if syncClientTransport.ResponseHeaderTimeout != 23*time.Second {
		t.Fatalf("sync ResponseHeaderTimeout = %v", syncClientTransport.ResponseHeaderTimeout)
	}

	if app.taskClient == nil {
		t.Fatal("expected task client to be initialized")
	}
	taskTransport := extractPrivateTransport(t, app.taskClient)
	if taskTransport.ResponseHeaderTimeout != 23*time.Second {
		t.Fatalf("task ResponseHeaderTimeout = %v", taskTransport.ResponseHeaderTimeout)
	}
	if taskTransport.TLSHandshakeTimeout != 22*time.Second {
		t.Fatalf("task TLSHandshakeTimeout = %v", taskTransport.TLSHandshakeTimeout)
	}
}

func TestNewSharesRuntimeBackendCachesWithDiagnosticTaskHandler(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	httpManager := app.httpModule
	if httpManager == nil {
		t.Fatal("httpModule = nil")
	}
	l4Module := app.l4Module
	if l4Module == nil {
		t.Fatal("l4Module = nil")
	}
	if app.taskClient == nil {
		t.Fatal("expected task client")
	}

	if err := app.moduleRegistry.Apply(context.Background(), Snapshot{}, Snapshot{}); err != nil {
		t.Fatalf("module registry Apply() error = %v", err)
	}

	httpProber := app.diagnosticModule.HTTPProber()
	tcpProber := app.diagnosticModule.TCPProber()
	if httpProber == nil || tcpProber == nil {
		t.Fatal("diagnostic probers were not assembled")
	}
	httpDiagnosticCache := extractPrivateField(t, httpProber, "cache").Interface()
	tcpDiagnosticCache := extractPrivateField(t, tcpProber, "cache").Interface()

	if httpDiagnosticCache != httpManager.Cache() {
		t.Fatal("http diagnostic prober does not share the runtime backend cache")
	}
	if tcpDiagnosticCache != l4Module.Cache() {
		t.Fatal("tcp diagnostic prober does not share the runtime backend cache")
	}
}

func TestDiagnoseUsesDiagnosticModuleHandler(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	diagnosticModule := modulediagnostics.NewModule()
	if err := diagnosticModule.Apply(context.Background(), agentmodule.ApplyRequest{
		Next: Snapshot{
			Rules: []model.HTTPRule{{
				ID:          77,
				FrontendURL: "http://frontend.example.test",
				Backends:    []model.HTTPBackend{{URL: backend.URL}},
			}},
		},
	}); err != nil {
		t.Fatalf("diagnostic module Apply() error = %v", err)
	}
	app := &App{
		diagnosticModule: diagnosticModule,
	}

	got, err := app.Diagnose(context.Background(), agenttask.TaskTypeDiagnoseHTTPRule, 77)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if got["kind"] != "http" || got["rule_id"] != 77 {
		t.Fatalf("Diagnose() = %+v, want http report for rule 77", got)
	}
}

func TestDiagnoseSnapshotUsesDiagnosticModuleProbers(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	diagnosticModule := modulediagnostics.NewModule()
	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, diagnosticModule)
	app := &App{
		diagnosticModule: diagnosticModule,
		moduleRegistry:   registry,
	}
	snapshot := Snapshot{
		Rules: []model.HTTPRule{{
			ID:          88,
			FrontendURL: "http://frontend.example.test",
			Backends:    []model.HTTPBackend{{URL: backend.URL}},
		}},
	}

	got, err := app.DiagnoseSnapshot(context.Background(), snapshot, agenttask.TaskTypeDiagnoseHTTPRule, 88)
	if err != nil {
		t.Fatalf("DiagnoseSnapshot() error = %v", err)
	}
	if got["kind"] != "http" || got["rule_id"] != 88 {
		t.Fatalf("DiagnoseSnapshot() = %+v, want http report for rule 88", got)
	}
}

func TestDiagnoseSnapshotUsesRegistryDiagnosticSources(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	cache := backends.NewCache(backends.Config{})
	cache.MarkFailure(backend.Listener.Addr().String())

	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, appProviderModule{
		name:     "http-diagnostics-source",
		provides: agentmodule.ProviderDiagnosticsHTTPSource,
		provider: appDiagnosticSource{cache: cache},
	})
	diagnosticModule := modulediagnostics.NewModule()
	mustRegisterAppModule(t, registry, diagnosticModule)
	app := &App{diagnosticModule: diagnosticModule, moduleRegistry: registry}
	snapshot := Snapshot{
		Rules: []model.HTTPRule{{
			ID:          89,
			FrontendURL: "http://frontend.example.test",
			Backends:    []model.HTTPBackend{{URL: backend.URL}},
		}},
	}

	_, err := app.DiagnoseSnapshot(context.Background(), snapshot, agenttask.TaskTypeDiagnoseHTTPRule, 89)
	if err == nil || !strings.Contains(err.Error(), "no healthy backend candidates") {
		t.Fatalf("DiagnoseSnapshot() error = %v, want registry cache source backoff to remove candidates", err)
	}
}

func TestDiagnoseSnapshotDoesNotConfigureRetainedDiagnosticsModule(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	diagnosticModule := modulediagnostics.NewModule()
	registry := agentmodule.NewRegistry()
	mustRegisterAppModule(t, registry, diagnosticModule)
	app := &App{diagnosticModule: diagnosticModule, moduleRegistry: registry}
	snapshot := Snapshot{
		Rules: []model.HTTPRule{{
			ID:          90,
			FrontendURL: "http://frontend.example.test",
			Backends:    []model.HTTPBackend{{URL: backend.URL}},
		}},
	}

	if _, err := app.DiagnoseSnapshot(context.Background(), snapshot, agenttask.TaskTypeDiagnoseHTTPRule, 90); err != nil {
		t.Fatalf("DiagnoseSnapshot() error = %v", err)
	}
	if diagnosticModule.Handler() != nil {
		t.Fatal("DiagnoseSnapshot configured retained diagnostics handler")
	}
	if diagnosticModule.HTTPProber() != nil || diagnosticModule.TCPProber() != nil {
		t.Fatal("DiagnoseSnapshot configured retained diagnostics probers")
	}
}

type appLifecycleModule struct {
	name   string
	starts []int64
	stops  int
}

func (m *appLifecycleModule) Name() string { return m.name }

func (m *appLifecycleModule) Capabilities() []agentmodule.Capability { return nil }

func (m *appLifecycleModule) Health(context.Context) agentmodule.Health {
	return agentmodule.Health{Status: "healthy"}
}

func (m *appLifecycleModule) Start(_ context.Context, snapshot model.Snapshot) error {
	m.starts = append(m.starts, snapshot.Revision)
	return nil
}

func (m *appLifecycleModule) Stop(context.Context) error {
	m.stops++
	return nil
}

type appCapabilityModule struct {
	name         string
	capabilities []agentmodule.Capability
}

func (m appCapabilityModule) Name() string { return m.name }

func (m appCapabilityModule) Capabilities() []agentmodule.Capability {
	return append([]agentmodule.Capability(nil), m.capabilities...)
}

func (m appCapabilityModule) Health(context.Context) agentmodule.Health {
	return agentmodule.Health{Status: "healthy"}
}

func (m appCapabilityModule) Start(context.Context, model.Snapshot) error { return nil }

func (m appCapabilityModule) Stop(context.Context) error { return nil }

type appProviderModule struct {
	name     string
	provides agentmodule.ProviderRef
	provider any
}

func (m appProviderModule) Name() string { return m.name }

func (m appProviderModule) Descriptor() agentmodule.ModuleDescriptor {
	return agentmodule.ModuleDescriptor{Name: m.name, Provides: []agentmodule.ProviderRef{m.provides}}
}

func (m appProviderModule) RegisterProviders(reg agentmodule.ProviderRegistry) error {
	return reg.Provide(m.provides, m.provider)
}

func (m appProviderModule) Capabilities(agentmodule.SnapshotView) []agentmodule.Capability {
	return nil
}

func (m appProviderModule) Apply(context.Context, agentmodule.ApplyRequest) error { return nil }

func (m appProviderModule) Stop(context.Context) error { return nil }

type appDiagnosticSource struct {
	cache *backends.Cache
}

func (s appDiagnosticSource) Cache() *backends.Cache {
	return s.cache
}

func mustRegisterAppModule(t *testing.T, registry *agentmodule.Registry, candidate any) {
	t.Helper()
	if err := registry.Register(candidate); err != nil {
		t.Fatalf("Register(%T) error = %v", candidate, err)
	}
}

func TestDiagnoseSnapshotAppliesSnapshotCertificatesBeforeTaskHandling(t *testing.T) {
	mem := store.NewInMemory()
	certApplier := &testCertificateApplier{applyErr: errors.New("certificate apply failed")}
	app := newAppWithDeps(Config{}, mem, newTestSyncClient(nil, syncResponse{}), certApplier, nil, nil)
	snapshot := Snapshot{
		Certificates: []model.ManagedCertificateBundle{{
			ID:      7,
			Domain:  "relay.example.com",
			CertPEM: "cert",
			KeyPEM:  "key",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:      7,
			Domain:  "relay.example.com",
			Enabled: true,
			Usage:   "relay_server",
		}},
	}

	_, err := app.DiagnoseSnapshot(context.Background(), snapshot, "unsupported", 99)
	if err == nil || err.Error() != "certificate apply failed" {
		t.Fatalf("DiagnoseSnapshot() error = %v, want certificate apply failed", err)
	}

	calls := certApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("certificate Apply calls = %d, want 1", len(calls))
	}
	if len(calls[0].bundles) != 1 || calls[0].bundles[0].ID != 7 {
		t.Fatalf("certificate bundles = %+v", calls[0].bundles)
	}
	if len(calls[0].policies) != 1 || calls[0].policies[0].ID != 7 {
		t.Fatalf("certificate policies = %+v", calls[0].policies)
	}
}

func TestAppCapabilitySourceUsesRegisteredModuleCapabilities(t *testing.T) {
	registry := agentmodule.NewRegistry()
	if err := registry.Register(appCapabilityModule{name: "wireguard", capabilities: []agentmodule.Capability{{Name: "wireguard", Enabled: true}}}); err != nil {
		t.Fatalf("Register(wireguard) error = %v", err)
	}
	if err := registry.Register(appCapabilityModule{name: "egress", capabilities: []agentmodule.Capability{{Name: "egress_profiles", Enabled: true}}}); err != nil {
		t.Fatalf("Register(egress) error = %v", err)
	}

	cfg := Config{WireGuardEnabled: false, WireGuardExplicit: true}
	got := core.CapabilityNames(appCapabilitySource{cfg: cfg, registry: registry})
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "wireguard", "egress_profiles"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}

func registryModuleByName(registry *agentmodule.Registry, name string) any {
	for _, mod := range registry.Modules() {
		if mod.Name() == name {
			if unwrapper, ok := mod.(interface{ Unwrap() any }); ok {
				return unwrapper.Unwrap()
			}
			return mod
		}
	}
	return nil
}

func extractPrivateTransport(t *testing.T, client any) *http.Transport {
	t.Helper()

	return extractPrivateField(t, client, "transport").Interface().(*http.Transport)
}

func extractPrivateField(t *testing.T, target any, name string) reflect.Value {
	t.Helper()

	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("target = %T", target)
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() {
		t.Fatalf("field %q not found on %T", name, target)
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
}

func TestNewAdvertisesRelayQUICAndConditionalHTTP3IngressCapabilities(t *testing.T) {
	tests := []struct {
		name              string
		http3Enabled      bool
		wireGuardEnabled  bool
		wireGuardExplicit bool
		expectedCaps      []string
	}{
		{
			name:              "http3 disabled",
			http3Enabled:      false,
			wireGuardEnabled:  true,
			wireGuardExplicit: true,
			expectedCaps:      []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "managed_certs", "diagnostics", "egress_profiles", "wireguard", "relay", "traffic_stats"},
		},
		{
			name:              "http3 enabled",
			http3Enabled:      true,
			wireGuardEnabled:  true,
			wireGuardExplicit: true,
			expectedCaps:      []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "http3_ingress", "managed_certs", "diagnostics", "egress_profiles", "wireguard", "relay", "traffic_stats"},
		},
		{
			name:              "wireguard disabled",
			http3Enabled:      false,
			wireGuardEnabled:  false,
			wireGuardExplicit: true,
			expectedCaps:      []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "managed_certs", "diagnostics", "egress_profiles", "relay", "traffic_stats"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("ReadAll() error = %v", err)
				}
				select {
				case requests <- body:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"sync":{"desired_version":"0.1.0","desired_revision":1}}`)
			}))
			defer server.Close()

			cfg := Config{
				AgentID:           "agent",
				AgentName:         "agent",
				MasterURL:         server.URL,
				AgentToken:        "token",
				CurrentVersion:    "0.1.0",
				DataDir:           t.TempDir(),
				HeartbeatInterval: 100 * time.Millisecond,
				HTTP3Enabled:      tc.http3Enabled,
				WireGuardEnabled:  tc.wireGuardEnabled,
				WireGuardExplicit: tc.wireGuardExplicit,
			}
			app, err := New(cfg)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			var body []byte
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := app.syncClient.Sync(ctx, SyncRequest{})
				done <- err
			}()
			select {
			case body = <-requests:
			case <-ctx.Done():
				t.Fatal("timed out waiting for heartbeat request")
			}
			if err := <-done; err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			var payload struct {
				Capabilities []string `json:"capabilities"`
			}
			if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if !reflect.DeepEqual(payload.Capabilities, tc.expectedCaps) {
				t.Fatalf("Capabilities = %+v, want %+v", payload.Capabilities, tc.expectedCaps)
			}
		})
	}
}

func TestRunReturnsErrorWithoutAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	errSync := errors.New("boom")
	client := newTestSyncClient([]syncResponse{{err: errSync}}, syncResponse{})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := app.Run(ctx); !errors.Is(err, errSync) {
		t.Fatalf("expected sync error, got %v", err)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != errSync.Error() {
		t.Fatalf("expected last_sync_error metadata, got %v", state.Metadata)
	}
}

func TestRunTracksCurrentRevisionFromSuccessfulApplies(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline", Revision: 100}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "999"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 101}})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req1 := waitForRequest(t, client, time.Second)
	if req1.CurrentRevision != 100 {
		t.Fatalf("expected first request revision 100, got %d", req1.CurrentRevision)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 101 {
		t.Fatalf("expected second request revision 101 after successful apply, got %d", req2.CurrentRevision)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunStartsAndStopsModuleRegistry(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	applied := Snapshot{DesiredVersion: "stored", Revision: 5}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{err: errors.New("sync failed")})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)
	first := &appLifecycleModule{name: "first"}
	second := &appLifecycleModule{name: "second"}
	registry := agentmodule.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatalf("Register(second) error = %v", err)
	}
	app.moduleRegistry = registry
	app.runtime = agentruntime.NewWithActivator(app.snapshotActivator())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForObservedCalls(t, time.Second, func() []int64 {
		return append([]int64(nil), first.starts...)
	}, 1, "module start")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(first.starts, []int64{5}) || !reflect.DeepEqual(second.starts, []int64{5}) {
		t.Fatalf("module starts first=%+v second=%+v, want exactly one revision 5 apply", first.starts, second.starts)
	}
	if first.stops != 1 || second.stops != 1 {
		t.Fatalf("module stops first=%d second=%d, want 1 each", first.stops, second.stops)
	}
}

func TestSnapshotActivatorAppliesCertificatesThroughRegistryOnlyWhenCertModuleRegistered(t *testing.T) {
	certApplier := &testCertificateApplier{}
	app := newAppWithDeps(Config{}, store.NewInMemory(), newTestSyncClient(nil, syncResponse{}), certApplier, nil, nil)
	certModule := modulecerts.NewModule(certApplier)
	registry := agentmodule.NewRegistry()
	if err := registry.Register(certModule); err != nil {
		t.Fatalf("Register(certs) error = %v", err)
	}
	app.certModule = certModule
	app.moduleRegistry = registry

	next := Snapshot{
		Certificates:        []model.ManagedCertificateBundle{{ID: 7}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{ID: 7}},
	}
	if err := app.snapshotActivator()(context.Background(), Snapshot{}, next); err != nil {
		t.Fatalf("snapshotActivator() error = %v", err)
	}

	calls := certApplier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("certificate apply calls = %d, want 1", len(calls))
	}
}

func TestSnapshotActivatorAppliesCertModuleBeforeLegacyConsumers(t *testing.T) {
	var order []string
	certApplier := &orderingCertificateApplier{
		onApply: func() {
			order = append(order, "cert")
		},
	}
	httpApplier := &testHTTPApplier{
		onApply: func() {
			order = append(order, "http")
		},
	}
	l4Applier := &testL4Applier{
		onApply: func() {
			order = append(order, "l4")
		},
	}
	relayApplier := &testRelayApplier{
		onApply: func() {
			order = append(order, "relay")
		},
	}
	app := newAppWithHTTPDeps(Config{AgentID: "agent-a", AgentName: "agent-a"}, store.NewInMemory(), newTestSyncClient(nil, syncResponse{}), httpApplier, certApplier, l4Applier, relayApplier)
	certModule := modulecerts.NewModule(certApplier)
	registry := agentmodule.NewRegistry()
	if err := registry.Register(certModule); err != nil {
		t.Fatalf("Register(certs) error = %v", err)
	}
	app.certModule = certModule
	app.moduleRegistry = registry

	certID := 7
	next := Snapshot{
		Certificates: []model.ManagedCertificateBundle{{ID: certID}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:      certID,
			Enabled: true,
		}},
		Rules: []model.HTTPRule{{
			FrontendURL: "https://media.example.test",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Enabled:     true,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9000}},
			Enabled:    true,
		}},
		RelayListeners: []model.RelayListener{{
			ID:            51,
			AgentID:       "agent-a",
			Name:          "relay-a",
			ListenHost:    "127.0.0.1",
			BindHosts:     []string{"127.0.0.1"},
			ListenPort:    9443,
			PublicHost:    "relay-a.example.test",
			PublicPort:    29443,
			Enabled:       true,
			CertificateID: &certID,
			TLSMode:       "managed",
		}},
	}

	if err := app.snapshotActivator()(context.Background(), Snapshot{}, next); err != nil {
		t.Fatalf("snapshotActivator() error = %v", err)
	}

	want := []string{"cert", "http", "l4", "relay"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("activation order = %v, want %v", order, want)
	}
}

func TestRunKeepsRunningWhenAppliedSnapshotExists(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "1.0"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{err: errors.New("boom")})
	app := newAppWithDeps(cfg, mem, client, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForSyncReturns(t, client, 1, time.Second)

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("expected nil after cancellation, got %v", err)
	}
}

func TestRunClosesCertificateApplierOnShutdown(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "next", Revision: 2}})
	certApplier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, certApplier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForSyncReturns(t, client, 1, time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if got := certApplier.closeCount(); got != 1 {
		t.Fatalf("expected certificate applier close to be called once, got %d", got)
	}
}

func TestSnapshotActivatorRelayListenerChangeReappliesHTTPRelayAndL4(t *testing.T) {
	previous := Snapshot{
		DesiredVersion: "stable",
		Revision:       7,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://relay-http.example.com",
			Backends: []model.HTTPBackend{
				{URL: "http://10.0.0.10:8096"},
			},
			RelayLayers: [][]int{{51}},
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends: []model.L4Backend{
				{Host: "10.0.0.20", Port: 9000},
			},
			RelayLayers: [][]int{{51}},
		}},
		RelayListeners: []model.RelayListener{{
			ID:         51,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1"},
			ListenPort: 9443,
			PublicHost: "relay-a.example.com",
			PublicPort: 29443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
		}},
	}
	next := previous
	next.Revision = 8
	next.RelayListeners = append([]model.RelayListener(nil), previous.RelayListeners...)
	next.RelayListeners[0].PublicPort = 39443

	httpApplier := &testHTTPApplier{}
	l4Applier := &testL4Applier{}
	relayApplier := &testRelayApplier{}
	app := newAppWithHTTPDeps(Config{}, store.NewInMemory(), newTestSyncClient(nil, syncResponse{}), httpApplier, nil, l4Applier, relayApplier)

	if err := app.snapshotActivator()(context.Background(), previous, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}

	httpCalls := httpApplier.snapshotCalls()
	if len(httpCalls) != 1 {
		t.Fatalf("expected relay-change http apply call, got %d", len(httpCalls))
	}
	l4Calls := l4Applier.snapshotCalls()
	if len(l4Calls) != 1 {
		t.Fatalf("expected relay-change l4 apply call, got %d", len(l4Calls))
	}
	relayCalls := relayApplier.snapshotCalls()
	if len(relayCalls) != 1 {
		t.Fatalf("expected relay-change relay apply call, got %d", len(relayCalls))
	}
	if got := relayCalls[0].listeners[0].PublicPort; got != 39443 {
		t.Fatalf("expected updated relay listener to be applied, got public_port=%d", got)
	}
}

type syncResponse struct {
	snapshot Snapshot
	err      error
}

type applyCall struct {
	bundles  []model.ManagedCertificateBundle
	policies []model.ManagedCertificatePolicy
}

type l4ApplyCall struct {
	rules []model.L4Rule
}

type relayApplyCall struct {
	listeners []model.RelayListener
}

type relayWireGuardApplyCall struct {
	listeners []model.RelayListener
	profiles  []model.WireGuardProfile
}

type relayEgressApplyCall struct {
	listeners      []model.RelayListener
	profiles       []model.WireGuardProfile
	egressProfiles []model.EgressProfile
}

type l4WireGuardApplyCall struct {
	rules     []model.L4Rule
	listeners []model.RelayListener
	profiles  []model.WireGuardProfile
}

type l4EgressApplyCall struct {
	rules             []model.L4Rule
	listeners         []model.RelayListener
	wireGuardProfiles []model.WireGuardProfile
	egressProfiles    []model.EgressProfile
}

type httpApplyCall struct {
	rules []model.HTTPRule
}

type httpEgressApplyCall struct {
	rules             []model.HTTPRule
	listeners         []model.RelayListener
	wireGuardProfiles []model.WireGuardProfile
	egressProfiles    []model.EgressProfile
}

type testCertificateApplier struct {
	mu        sync.Mutex
	calls     []applyCall
	applyErr  error
	reports   []model.ManagedCertificateReport
	reportErr error
	closed    int
}

func (a *testCertificateApplier) Apply(_ context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, applyCall{
		bundles:  append([]model.ManagedCertificateBundle(nil), bundles...),
		policies: append([]model.ManagedCertificatePolicy(nil), policies...),
	})
	return a.applyErr
}

func (a *testCertificateApplier) snapshotCalls() []applyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]applyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testCertificateApplier) ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.reportErr != nil {
		return nil, a.reportErr
	}
	out := make([]model.ManagedCertificateReport, len(a.reports))
	copy(out, a.reports)
	return out, nil
}

func (a *testCertificateApplier) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed++
	return nil
}

func (a *testCertificateApplier) closeCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

type orderingCertificateApplier struct {
	testCertificateApplier
	onApply func()
}

func (a *orderingCertificateApplier) Apply(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
	if a.onApply != nil {
		a.onApply()
	}
	return a.testCertificateApplier.Apply(ctx, bundles, policies)
}

func (a *orderingCertificateApplier) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (a *orderingCertificateApplier) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type testL4Applier struct {
	mu       sync.Mutex
	calls    []l4ApplyCall
	applyErr error
	onApply  func()
}

func (a *testL4Applier) Apply(_ context.Context, rules []model.L4Rule) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onApply != nil {
		a.onApply()
	}
	var copied []model.L4Rule
	if rules != nil {
		copied = make([]model.L4Rule, len(rules))
		copy(copied, rules)
	}
	a.calls = append(a.calls, l4ApplyCall{
		rules: copied,
	})
	return a.applyErr
}

func (a *testL4Applier) snapshotCalls() []l4ApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]l4ApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testL4Applier) Close() error {
	return nil
}

type testWireGuardL4Applier struct {
	testL4Applier
	wgMu    sync.Mutex
	wgCalls []l4WireGuardApplyCall
}

func (a *testWireGuardL4Applier) ApplyWithRelayAndWireGuardProfiles(_ context.Context, rules []model.L4Rule, listeners []model.RelayListener, profiles []model.WireGuardProfile) error {
	a.wgMu.Lock()
	defer a.wgMu.Unlock()
	var copiedRules []model.L4Rule
	if rules != nil {
		copiedRules = make([]model.L4Rule, len(rules))
		copy(copiedRules, rules)
	}
	var copiedListeners []model.RelayListener
	if listeners != nil {
		copiedListeners = make([]model.RelayListener, len(listeners))
		copy(copiedListeners, listeners)
	}
	var copiedProfiles []model.WireGuardProfile
	if profiles != nil {
		copiedProfiles = make([]model.WireGuardProfile, len(profiles))
		copy(copiedProfiles, profiles)
	}
	a.wgCalls = append(a.wgCalls, l4WireGuardApplyCall{
		rules:     copiedRules,
		listeners: copiedListeners,
		profiles:  copiedProfiles,
	})
	return a.applyErr
}

func (a *testWireGuardL4Applier) wireGuardCalls() []l4WireGuardApplyCall {
	a.wgMu.Lock()
	defer a.wgMu.Unlock()
	out := make([]l4WireGuardApplyCall, len(a.wgCalls))
	copy(out, a.wgCalls)
	return out
}

type testEgressL4Applier struct {
	testWireGuardL4Applier
	egressMu    sync.Mutex
	egressCalls []l4EgressApplyCall
}

func (a *testEgressL4Applier) ApplyWithRelayWireGuardAndEgressProfiles(_ context.Context, rules []model.L4Rule, listeners []model.RelayListener, wireGuardProfiles []model.WireGuardProfile, egressProfiles []model.EgressProfile) error {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	a.egressCalls = append(a.egressCalls, l4EgressApplyCall{
		rules:             append([]model.L4Rule(nil), rules...),
		listeners:         append([]model.RelayListener(nil), listeners...),
		wireGuardProfiles: append([]model.WireGuardProfile(nil), wireGuardProfiles...),
		egressProfiles:    append([]model.EgressProfile(nil), egressProfiles...),
	})
	return a.applyErr
}

func (a *testEgressL4Applier) egressProfileCalls() []l4EgressApplyCall {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	out := make([]l4EgressApplyCall, len(a.egressCalls))
	copy(out, a.egressCalls)
	return out
}

type testRelayApplier struct {
	mu       sync.Mutex
	calls    []relayApplyCall
	applyErr error
	onApply  func()
}

func (a *testRelayApplier) Apply(_ context.Context, listeners []model.RelayListener) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onApply != nil {
		a.onApply()
	}
	var copied []model.RelayListener
	if listeners != nil {
		copied = make([]model.RelayListener, len(listeners))
		copy(copied, listeners)
	}
	a.calls = append(a.calls, relayApplyCall{
		listeners: copied,
	})
	return a.applyErr
}

func (a *testRelayApplier) snapshotCalls() []relayApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]relayApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testRelayApplier) Close() error {
	return nil
}

type testWireGuardRelayApplier struct {
	testRelayApplier
	wgMu    sync.Mutex
	wgCalls []relayWireGuardApplyCall
}

func (a *testWireGuardRelayApplier) ApplyWithWireGuardProfiles(_ context.Context, listeners []model.RelayListener, profiles []model.WireGuardProfile) error {
	a.wgMu.Lock()
	defer a.wgMu.Unlock()
	var copiedListeners []model.RelayListener
	if listeners != nil {
		copiedListeners = make([]model.RelayListener, len(listeners))
		copy(copiedListeners, listeners)
	}
	var copiedProfiles []model.WireGuardProfile
	if profiles != nil {
		copiedProfiles = make([]model.WireGuardProfile, len(profiles))
		copy(copiedProfiles, profiles)
	}
	a.wgCalls = append(a.wgCalls, relayWireGuardApplyCall{
		listeners: copiedListeners,
		profiles:  copiedProfiles,
	})
	return a.applyErr
}

func (a *testWireGuardRelayApplier) wireGuardCalls() []relayWireGuardApplyCall {
	a.wgMu.Lock()
	defer a.wgMu.Unlock()
	out := make([]relayWireGuardApplyCall, len(a.wgCalls))
	copy(out, a.wgCalls)
	return out
}

type testEgressRelayApplier struct {
	testRelayApplier
	egressMu    sync.Mutex
	egressCalls []relayEgressApplyCall
}

func (a *testEgressRelayApplier) ApplyWithWireGuardAndEgressProfiles(_ context.Context, listeners []model.RelayListener, profiles []model.WireGuardProfile, egressProfiles []model.EgressProfile) error {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	var copiedListeners []model.RelayListener
	if listeners != nil {
		copiedListeners = make([]model.RelayListener, len(listeners))
		copy(copiedListeners, listeners)
	}
	var copiedProfiles []model.WireGuardProfile
	if profiles != nil {
		copiedProfiles = make([]model.WireGuardProfile, len(profiles))
		copy(copiedProfiles, profiles)
	}
	var copiedEgress []model.EgressProfile
	if egressProfiles != nil {
		copiedEgress = make([]model.EgressProfile, len(egressProfiles))
		copy(copiedEgress, egressProfiles)
	}
	a.egressCalls = append(a.egressCalls, relayEgressApplyCall{
		listeners:      copiedListeners,
		profiles:       copiedProfiles,
		egressProfiles: copiedEgress,
	})
	return a.applyErr
}

func (a *testEgressRelayApplier) egressSnapshotCalls() []relayEgressApplyCall {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	out := make([]relayEgressApplyCall, len(a.egressCalls))
	copy(out, a.egressCalls)
	return out
}

type testHTTPApplier struct {
	mu         sync.Mutex
	calls      []httpApplyCall
	applyErr   error
	failOnCall int
	onApply    func()
}

func (a *testHTTPApplier) Apply(_ context.Context, rules []model.HTTPRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onApply != nil {
		a.onApply()
	}
	var copied []model.HTTPRule
	if rules != nil {
		copied = make([]model.HTTPRule, len(rules))
		copy(copied, rules)
		for i, rule := range rules {
			if rule.CustomHeaders != nil {
				copied[i].CustomHeaders = make([]model.HTTPHeader, len(rule.CustomHeaders))
				copy(copied[i].CustomHeaders, rule.CustomHeaders)
			}
		}
	}
	a.calls = append(a.calls, httpApplyCall{
		rules: copied,
	})
	if a.applyErr != nil && (a.failOnCall == 0 || len(a.calls) == a.failOnCall) {
		return a.applyErr
	}
	return nil
}

func (a *testHTTPApplier) snapshotCalls() []httpApplyCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]httpApplyCall, len(a.calls))
	copy(out, a.calls)
	return out
}

func (a *testHTTPApplier) Close() error {
	return nil
}

type testEgressHTTPApplier struct {
	testHTTPApplier
	egressMu    sync.Mutex
	egressCalls []httpEgressApplyCall
}

func (a *testEgressHTTPApplier) ApplyWithRelayWireGuardAndEgressProfiles(_ context.Context, rules []model.HTTPRule, listeners []model.RelayListener, wireGuardProfiles []model.WireGuardProfile, egressProfiles []model.EgressProfile) error {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	a.egressCalls = append(a.egressCalls, httpEgressApplyCall{
		rules:             append([]model.HTTPRule(nil), rules...),
		listeners:         append([]model.RelayListener(nil), listeners...),
		wireGuardProfiles: append([]model.WireGuardProfile(nil), wireGuardProfiles...),
		egressProfiles:    append([]model.EgressProfile(nil), egressProfiles...),
	})
	return a.applyErr
}

func (a *testEgressHTTPApplier) egressProfileCalls() []httpEgressApplyCall {
	a.egressMu.Lock()
	defer a.egressMu.Unlock()
	out := make([]httpEgressApplyCall, len(a.egressCalls))
	copy(out, a.egressCalls)
	return out
}

type testTrafficBlockHTTPApplier struct {
	testHTTPApplier
	mu    sync.Mutex
	state modulehttp.TrafficBlockState
}

func (a *testTrafficBlockHTTPApplier) UpdateTrafficBlockState(state modulehttp.TrafficBlockState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

func (a *testTrafficBlockHTTPApplier) blockState() modulehttp.TrafficBlockState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *testTrafficBlockHTTPApplier) applyCount() int {
	return len(a.snapshotCalls())
}

func (a *testTrafficBlockHTTPApplier) resetApplyCalls() {
	a.testHTTPApplier.mu.Lock()
	defer a.testHTTPApplier.mu.Unlock()
	a.testHTTPApplier.calls = nil
}

type testTrafficBlockL4Applier struct {
	testL4Applier
	mu    sync.Mutex
	state l4.TrafficBlockState
}

func (a *testTrafficBlockL4Applier) UpdateTrafficBlockState(state l4.TrafficBlockState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

func (a *testTrafficBlockL4Applier) blockState() l4.TrafficBlockState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *testTrafficBlockL4Applier) applyCount() int {
	return len(a.snapshotCalls())
}

func (a *testTrafficBlockL4Applier) resetApplyCalls() {
	a.testL4Applier.mu.Lock()
	defer a.testL4Applier.mu.Unlock()
	a.testL4Applier.calls = nil
}

type testTrafficBlockRelayApplier struct {
	testRelayApplier
	mu    sync.Mutex
	state relay.TrafficBlockState
}

func (a *testTrafficBlockRelayApplier) UpdateTrafficBlockState(state relay.TrafficBlockState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = state
}

func (a *testTrafficBlockRelayApplier) blockState() relay.TrafficBlockState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *testTrafficBlockRelayApplier) applyCount() int {
	return len(a.snapshotCalls())
}

func (a *testTrafficBlockRelayApplier) resetApplyCalls() {
	a.testRelayApplier.mu.Lock()
	defer a.testRelayApplier.mu.Unlock()
	a.testRelayApplier.calls = nil
}

type testSyncClient struct {
	mu        sync.Mutex
	responses []syncResponse
	fallback  syncResponse
	callCount int32
	doneCount int32
	reqCh     chan SyncRequest
	blockCh   chan struct{}
	blockFrom int32
}

func newTestSyncClient(responses []syncResponse, fallback syncResponse) *testSyncClient {
	return &testSyncClient{
		responses: append([]syncResponse(nil), responses...),
		fallback:  fallback,
		reqCh:     make(chan SyncRequest, 16),
	}
}

func (c *testSyncClient) blockFromCall(callNum int32) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blockCh == nil {
		c.blockCh = make(chan struct{})
	}
	c.blockFrom = callNum
	return c.blockCh
}

func (c *testSyncClient) Sync(_ context.Context, request SyncRequest) (Snapshot, error) {
	callNum := atomic.AddInt32(&c.callCount, 1)
	select {
	case c.reqCh <- request:
	default:
	}
	defer atomic.AddInt32(&c.doneCount, 1)
	c.mu.Lock()
	blockCh := c.blockCh
	blockFrom := c.blockFrom
	c.mu.Unlock()
	if blockCh != nil && callNum >= blockFrom {
		<-blockCh
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) > 0 {
		resp := c.responses[0]
		c.responses = c.responses[1:]
		return resp.snapshot, resp.err
	}
	return c.fallback.snapshot, c.fallback.err
}

func waitForRequest(t *testing.T, client *testSyncClient, timeout time.Duration) SyncRequest {
	select {
	case req := <-client.reqCh:
		return req
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for sync request")
	}
	return SyncRequest{}
}

// waitForSyncReturns only proves the fake client returned. Use observable
// applier/store waits for assertions about work performed after Sync returns.
func waitForSyncReturns(t *testing.T, client *testSyncClient, target int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if int(atomic.LoadInt32(&client.doneCount)) >= target {
			return
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d sync calls", target)
}

func waitForObservedCalls[T any](t *testing.T, timeout time.Duration, load func() []T, target int, description string) []T {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var calls []T
	for time.Now().Before(deadline) {
		calls = load()
		if len(calls) >= target {
			return calls
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s calls: got %d, want at least %d", description, len(calls), target)
	return nil
}

func waitForDesiredSnapshot(t *testing.T, timeout time.Duration, st store.Store, predicate func(Snapshot) bool) Snapshot {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var snapshot Snapshot
	for time.Now().Before(deadline) {
		current, err := st.LoadDesiredSnapshot()
		if err != nil {
			t.Fatalf("failed to load desired snapshot: %v", err)
		}
		snapshot = current
		if predicate(current) {
			return current
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for desired snapshot, last snapshot: %+v", snapshot)
	return Snapshot{}
}

func waitForAppliedSnapshot(t *testing.T, timeout time.Duration, st store.Store, predicate func(Snapshot) bool) Snapshot {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var snapshot Snapshot
	for time.Now().Before(deadline) {
		current, err := st.LoadAppliedSnapshot()
		if err != nil {
			t.Fatalf("failed to load applied snapshot: %v", err)
		}
		snapshot = current
		if predicate(current) {
			return current
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for applied snapshot, last snapshot: %+v", snapshot)
	return Snapshot{}
}

func waitForRuntimeState(t *testing.T, timeout time.Duration, predicate func() bool, failureMessage func() string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatal(failureMessage())
}

func waitForLastSyncError(t *testing.T, timeout time.Duration, load func() (store.RuntimeState, error), expected string) store.RuntimeState {
	t.Helper()

	var state store.RuntimeState
	waitForRuntimeState(t, timeout, func() bool {
		current, err := load()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		state = current
		return current.Metadata["last_sync_error"] == expected
	}, func() string {
		return "expected last_sync_error metadata to be persisted"
	})

	return state
}

func TestRunAppliesManagedCertificatesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       21,
			Domain:   "sync.example.com",
			Revision: 3,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Status:          "issued",
			Revision:        3,
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, applier.snapshotCalls, 1, "certificate apply")
	if len(calls) != 1 {
		t.Fatalf("expected one certificate apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].bundles, expected.Certificates) {
		t.Fatalf("unexpected bundles: %+v", calls[0].bundles)
	}
	if !reflect.DeepEqual(calls[0].policies, expected.CertificatePolicies) {
		t.Fatalf("unexpected policies: %+v", calls[0].policies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesManagedCertificatesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "https://frontend.example.com",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Revision:    2,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   4,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
		Certificates: []model.ManagedCertificateBundle{{
			ID:       41,
			Domain:   "stored.example.com",
			Revision: 1,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              41,
			Domain:          "stored.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       99,
			Domain:   "desired.example.com",
			Revision: 9,
			CertPEM:  "OTHER_CERTIFICATE",
			KeyPEM:   "OTHER_PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              99,
			Domain:          "desired.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        9,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, applier.snapshotCalls, 1, "certificate hydration")
	if !reflect.DeepEqual(calls[0].bundles, stored.Certificates) {
		t.Fatalf("unexpected hydrated bundles: %+v", calls[0].bundles)
	}
	if !reflect.DeepEqual(calls[0].policies, stored.CertificatePolicies) {
		t.Fatalf("unexpected hydrated policies: %+v", calls[0].policies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyManagedCertificatesWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForRuntimeState(t, time.Second, func() bool {
		state, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		return state.CurrentRevision == 7
	}, func() string {
		return "expected runtime state to advance to revision 7"
	})
	if calls := applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no certificate apply calls for omitted payload, got %d", len(calls))
	}

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if snap.Certificates != nil {
		t.Fatalf("expected omitted certificate payload to stay nil when nothing was stored before, got %+v", snap.Certificates)
	}
	if snap.CertificatePolicies != nil {
		t.Fatalf("expected omitted certificate policy payload to stay nil when nothing was stored before, got %+v", snap.CertificatePolicies)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunPreservesStoredManagedCertificatePayloadWhenHeartbeatOmitsFields(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Certificates: []model.ManagedCertificateBundle{{
			ID:       41,
			Domain:   "stored.example.com",
			Revision: 1,
			CertPEM:  "CERTIFICATE",
			KeyPEM:   "PRIVATEKEY",
		}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{
			ID:              41,
			Domain:          "stored.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			Revision:        1,
			Usage:           "https",
			CertificateType: "uploaded",
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	applier := &testCertificateApplier{}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, applier.snapshotCalls, 1, "certificate hydration")
	if len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits payload, got %d", len(calls))
	}

	persisted := waitForDesiredSnapshot(t, time.Second, mem, func(snapshot Snapshot) bool {
		return snapshot.DesiredVersion == "ok" && snapshot.Revision == 7 &&
			reflect.DeepEqual(snapshot.Certificates, stored.Certificates) &&
			reflect.DeepEqual(snapshot.CertificatePolicies, stored.CertificatePolicies) &&
			reflect.DeepEqual(snapshot.Rules, stored.Rules) &&
			reflect.DeepEqual(snapshot.L4Rules, stored.L4Rules) &&
			reflect.DeepEqual(snapshot.RelayListeners, stored.RelayListeners)
	})
	waitForRuntimeState(t, time.Second, func() bool {
		state, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		return state.CurrentRevision == 7
	}, func() string {
		return "expected runtime state to advance to revision 7"
	})
	if calls := applier.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits payload, got %d", len(calls))
	}
	if !reflect.DeepEqual(persisted.Certificates, stored.Certificates) {
		t.Fatalf("expected stored certificates to be preserved, got %+v", persisted.Certificates)
	}
	if !reflect.DeepEqual(persisted.CertificatePolicies, stored.CertificatePolicies) {
		t.Fatalf("expected stored certificate policies to be preserved, got %+v", persisted.CertificatePolicies)
	}
	if !reflect.DeepEqual(persisted.Rules, stored.Rules) {
		t.Fatalf("expected stored rules to be preserved, got %+v", persisted.Rules)
	}
	if !reflect.DeepEqual(persisted.L4Rules, stored.L4Rules) {
		t.Fatalf("expected stored l4 rules to be preserved, got %+v", persisted.L4Rules)
	}
	if !reflect.DeepEqual(persisted.RelayListeners, stored.RelayListeners) {
		t.Fatalf("expected stored relay listeners to be preserved, got %+v", persisted.RelayListeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsCertificateApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Certificates:   []model.ManagedCertificateBundle{},
	}})
	applier := &testCertificateApplier{applyErr: errors.New("cert apply failed")}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "cert apply failed")
	if state.Metadata["last_sync_error"] != "cert apply failed" {
		t.Fatalf("expected certificate apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunKeepsRunningAfterStartupCertificateHydrationFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Certificates:   []model.ManagedCertificateBundle{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	applier := &testCertificateApplier{applyErr: errors.New("startup cert apply failed")}
	app := newAppWithDeps(cfg, mem, client, applier, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req := waitForRequest(t, client, time.Second)
	if req.LastApplyStatus != "error" || req.LastApplyMessage != "startup cert apply failed" {
		t.Fatalf("expected startup cert failure to be reported on next heartbeat, got %+v", req)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesHTTPRulesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL:   "http://edge.example.test:18080",
			Backends:      []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			ProxyRedirect: true,
			Revision:      4,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, httpApplier.snapshotCalls, 1, "http apply")
	if len(calls) != 1 {
		t.Fatalf("expected one http apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, expected.Rules) {
		t.Fatalf("unexpected http rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesHTTPRulesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL:      "http://edge.example.test:18080",
			Backends:         []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			PassProxyHeaders: true,
			Revision:         4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://desired.example.test:28080",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8097"}},
			Revision:    9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, httpApplier.snapshotCalls, 1, "http hydration")
	if len(calls) != 1 {
		t.Fatalf("expected one startup http apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, stored.Rules) {
		t.Fatalf("unexpected hydrated http rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyHTTPWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example.test:18080",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Revision:    4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(stored); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, httpApplier.snapshotCalls, 1, "http hydration")
	if len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits http rules, got %d", len(calls))
	}

	persisted := waitForDesiredSnapshot(t, time.Second, mem, func(snapshot Snapshot) bool {
		return snapshot.DesiredVersion == "ok" && snapshot.Revision == 7 &&
			reflect.DeepEqual(snapshot.Rules, stored.Rules)
	})
	waitForRuntimeState(t, time.Second, func() bool {
		state, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		return state.CurrentRevision == 7
	}, func() string {
		return "expected runtime state to advance to revision 7"
	})
	if calls := httpApplier.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("expected only startup hydration call when heartbeat omits http rules, got %d", len(calls))
	}
	if !reflect.DeepEqual(persisted.Rules, stored.Rules) {
		t.Fatalf("expected stored http rules to be preserved, got %+v", persisted.Rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyHTTPRules(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://edge.example.test:18080",
			Backends:    []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			Revision:    4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		Rules:          []model.HTTPRule{},
	}})
	httpApplier := &testHTTPApplier{}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, httpApplier.snapshotCalls, 2, "http clear")
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear http apply calls, got %d", len(calls))
	}
	if calls[1].rules == nil || len(calls[1].rules) != 0 {
		t.Fatalf("expected explicit empty http rules on clear, got %+v", calls[1].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsHTTPApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		Rules:          []model.HTTPRule{},
	}})
	httpApplier := &testHTTPApplier{applyErr: errors.New("http apply failed")}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "http apply failed")
	if state.Metadata["last_sync_error"] != "http apply failed" {
		t.Fatalf("expected http apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunKeepsRunningAfterStartupHTTPHydrationFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		Rules:          []model.HTTPRule{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	httpApplier := &testHTTPApplier{applyErr: errors.New("startup http apply failed")}
	app := newAppWithHTTPDeps(cfg, mem, client, httpApplier, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req := waitForRequest(t, client, time.Second)
	if req.LastApplyStatus != "error" || req.LastApplyMessage != "startup http apply failed" {
		t.Fatalf("expected startup http failure to be reported on next heartbeat, got %+v", req)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesL4RulesFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   4,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, l4Applier.snapshotCalls, 1, "l4 apply")
	if len(calls) != 1 {
		t.Fatalf("expected one l4 apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, expected.L4Rules) {
		t.Fatalf("unexpected l4 rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesL4RulesFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.2",
			ListenPort: 9900,
			Backends:   []model.L4Backend{{Host: "127.0.0.2", Port: 9901}},
			Revision:   9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, l4Applier.snapshotCalls, 1, "l4 hydration")
	if len(calls) != 1 {
		t.Fatalf("expected one startup l4 apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, stored.L4Rules) {
		t.Fatalf("unexpected hydrated l4 rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesMissingAppliedL4RulesFromDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	applied := Snapshot{
		DesiredVersion: "stored",
		Revision:       221,
	}
	desired := Snapshot{
		DesiredVersion: "stored",
		Revision:       221,
		L4Rules: []model.L4Rule{{
			ID:         45,
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 0,
			ListenMode: "wireguard",
			Backends:   []model.L4Backend{},
			Revision:   218,
		}},
		RelayListeners:      []model.RelayListener{},
		WireGuardProfiles:   []model.WireGuardProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(desired); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, l4Applier.snapshotCalls, 1, "l4 hydration")
	if len(calls) != 1 {
		t.Fatalf("expected one startup l4 apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].rules, desired.L4Rules) {
		t.Fatalf("unexpected hydrated l4 rules: %+v", calls[0].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotHydratePartialAppliedSnapshotFromNewerDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	applied := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
	}
	desired := Snapshot{
		DesiredVersion: "stored",
		Revision:       6,
		L4Rules: []model.L4Rule{{
			ID:         45,
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 0,
			ListenMode: "wireguard",
			Backends:   []model.L4Backend{},
			Revision:   6,
		}},
	}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(desired); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForSyncReturns(t, client, 1, time.Second)

	if calls := l4Applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no l4 hydration from newer desired snapshot, got %+v", calls)
	}

	persisted, err := mem.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("failed to load applied snapshot: %v", err)
	}
	if persisted.L4Rules != nil {
		t.Fatalf("expected applied snapshot to stay partial, got %+v", persisted.L4Rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotHydrateNewerAppliedSnapshotFromOlderDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	applied := Snapshot{
		DesiredVersion: "stored",
		Revision:       222,
	}
	desired := Snapshot{
		DesiredVersion: "stored",
		Revision:       221,
		L4Rules: []model.L4Rule{{
			ID:         45,
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   218,
		}},
	}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(desired); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForSyncReturns(t, client, 1, time.Second)
	if calls := l4Applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no l4 hydration from older desired snapshot, got %+v", calls)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunPersistsHydratedAppliedL4RulesFromDesiredSnapshotOnStartup(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	applied := Snapshot{
		DesiredVersion: "stored",
		Revision:       221,
	}
	desired := Snapshot{
		DesiredVersion: "stored",
		Revision:       221,
		L4Rules: []model.L4Rule{{
			ID:         45,
			Protocol:   "tcp",
			ListenHost: "0.0.0.0",
			ListenPort: 0,
			ListenMode: "wireguard",
			Backends:   []model.L4Backend{},
			Revision:   218,
		}},
		RelayListeners:      []model.RelayListener{},
		WireGuardProfiles:   []model.WireGuardProfile{},
		Certificates:        []model.ManagedCertificateBundle{},
		CertificatePolicies: []model.ManagedCertificatePolicy{},
	}
	if err := mem.SaveAppliedSnapshot(applied); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(desired); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	l4Applier := &testL4Applier{applyErr: nil}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	appliedAfter := waitForAppliedSnapshot(t, time.Second, mem, func(snapshot Snapshot) bool {
		return reflect.DeepEqual(snapshot.L4Rules, desired.L4Rules)
	})
	if !reflect.DeepEqual(appliedAfter.L4Rules, desired.L4Rules) {
		t.Fatalf("expected hydrated applied l4 rules to be persisted, got %+v", appliedAfter.L4Rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesRelayListenersFromSyncedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, relayApplier.snapshotCalls, 1, "relay apply")
	if len(calls) != 1 {
		t.Fatalf("expected one relay apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].listeners, expected.RelayListeners) {
		t.Fatalf("unexpected relay listeners: %+v", calls[0].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunHydratesRelayListenersFromStoredAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveDesiredSnapshot(Snapshot{
		DesiredVersion: "desired",
		Revision:       9,
		RelayListeners: []model.RelayListener{{
			ID:         99,
			AgentID:    "desired-agent",
			Name:       "desired-relay",
			ListenHost: "127.0.0.2",
			ListenPort: 9444,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "desired-pin",
			}},
			Revision: 9,
		}},
	}); err != nil {
		t.Fatalf("failed to seed desired snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, relayApplier.snapshotCalls, 1, "relay hydration")
	if len(calls) != 1 {
		t.Fatalf("expected one startup relay apply call, got %d", len(calls))
	}
	if !reflect.DeepEqual(calls[0].listeners, stored.RelayListeners) {
		t.Fatalf("unexpected hydrated relay listeners: %+v", calls[0].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestSnapshotActivatorAppliesTrafficStatsEnabledFromAgentConfig(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	t.Cleanup(func() {
		traffic.SetEnabled(true)
		traffic.Reset()
	})

	app := newAppWithDeps(Config{}, store.NewInMemory(), newTestSyncClient(nil, syncResponse{}), nil, nil, nil)
	disabled := false
	previous := Snapshot{}
	next := Snapshot{
		AgentConfig: model.AgentConfig{
			TrafficStatsEnabled: &disabled,
		},
	}

	if err := app.snapshotActivator()(context.Background(), previous, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}
	if traffic.Enabled() {
		t.Fatal("traffic.Enabled() = true, want false")
	}

	enabled := true
	previous = next
	next.AgentConfig.TrafficStatsEnabled = &enabled
	if err := app.snapshotActivator()(context.Background(), previous, next); err != nil {
		t.Fatalf("snapshotActivator returned error while enabling stats: %v", err)
	}
	if !traffic.Enabled() {
		t.Fatal("traffic.Enabled() = false, want true")
	}
}

func TestSnapshotActivatorUpdatesTrafficBlockStateFromAgentConfigOnlyChange(t *testing.T) {
	previous := Snapshot{
		AgentConfig: model.AgentConfig{
			TrafficBlocked: false,
		},
		Rules: []model.HTTPRule{{
			ID:          1,
			FrontendURL: "http://frontend.example",
			Backends:    []model.HTTPBackend{{URL: "http://backend.example"}},
			Enabled:     true,
		}},
		L4Rules: []model.L4Rule{{
			ID:         2,
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 19000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 19001}},
			Enabled:    true,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         3,
			AgentID:    "agent-a",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			BindHosts:  []string{"127.0.0.1"},
			ListenPort: 19443,
			PublicHost: "127.0.0.1",
			PublicPort: 19443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "spki_sha256",
				Value: "cGlubmVk",
			}},
		}},
	}
	next := previous
	next.AgentConfig.TrafficBlocked = true
	next.AgentConfig.TrafficBlockReason = "monthly quota exceeded"

	httpApplier := &testTrafficBlockHTTPApplier{}
	l4Applier := &testTrafficBlockL4Applier{}
	relayApplier := &testTrafficBlockRelayApplier{}
	app := newAppWithHTTPDeps(Config{}, store.NewInMemory(), newTestSyncClient(nil, syncResponse{}), httpApplier, nil, l4Applier, relayApplier)

	if err := app.snapshotActivator()(context.Background(), previous, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}

	if got := httpApplier.applyCount(); got != 0 {
		t.Fatalf("http Apply calls = %d, want 0 for agent_config-only change", got)
	}
	if got := l4Applier.applyCount(); got != 0 {
		t.Fatalf("l4 Apply calls = %d, want 0 for agent_config-only change", got)
	}
	if got := relayApplier.applyCount(); got != 0 {
		t.Fatalf("relay Apply calls = %d, want 0 for agent_config-only change", got)
	}
	if got := httpApplier.blockState(); got.Blocked != true || got.Reason != "monthly quota exceeded" {
		t.Fatalf("http block state = %+v", got)
	}
	if got := l4Applier.blockState(); got.Blocked != true || got.Reason != "monthly quota exceeded" {
		t.Fatalf("l4 block state = %+v", got)
	}
	if got := relayApplier.blockState(); got.Blocked != true || got.Reason != "monthly quota exceeded" {
		t.Fatalf("relay block state = %+v", got)
	}

	previous = next
	next.AgentConfig.TrafficBlocked = false
	next.AgentConfig.TrafficBlockReason = ""
	if err := app.snapshotActivator()(context.Background(), previous, next); err != nil {
		t.Fatalf("snapshotActivator returned error while clearing block state: %v", err)
	}
	if got := httpApplier.blockState(); got.Blocked != false || got.Reason != "" {
		t.Fatalf("http cleared block state = %+v", got)
	}
	if got := l4Applier.blockState(); got.Blocked != false || got.Reason != "" {
		t.Fatalf("l4 cleared block state = %+v", got)
	}
	if got := relayApplier.blockState(); got.Blocked != false || got.Reason != "" {
		t.Fatalf("relay cleared block state = %+v", got)
	}
}

func TestSnapshotActivatorAppliesL4RulesWithoutWireGuardProfiles(t *testing.T) {
	l4Applier := &testWireGuardL4Applier{}
	app := newAppWithDeps(
		Config{AgentID: "local-agent"},
		store.NewInMemory(),
		newTestSyncClient(nil, syncResponse{}),
		nil,
		l4Applier,
		nil,
	)

	profileID := 9
	next := Snapshot{
		WireGuardProfiles: []model.WireGuardProfile{{
			ID:       profileID,
			Enabled:  true,
			Revision: 1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:           "tcp",
			ListenHost:         "127.0.0.1",
			ListenPort:         8443,
			ListenMode:         "wireguard",
			WireGuardProfileID: &profileID,
			Backends:           []model.L4Backend{{Host: "127.0.0.1", Port: 9443}},
			Revision:           1,
		}},
	}

	if err := app.snapshotActivator()(context.Background(), Snapshot{}, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}

	calls := l4Applier.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(calls))
	}
	if len(l4Applier.wireGuardCalls()) != 0 {
		t.Fatal("snapshotActivator should not call legacy wireguard-aware L4 apply")
	}
}

func TestSnapshotActivatorAppliesL4RulesWithoutEgressProfiles(t *testing.T) {
	l4Applier := &testEgressL4Applier{}
	app := newAppWithDeps(
		Config{AgentID: "local-agent"},
		store.NewInMemory(),
		newTestSyncClient(nil, syncResponse{}),
		nil,
		l4Applier,
		nil,
	)

	profileID := 17
	next := Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:       profileID,
			Name:     "socks exit",
			Type:     "socks",
			ProxyURL: "socks5://127.0.0.1:1080",
			Enabled:  true,
			Revision: 1,
		}},
		L4Rules: []model.L4Rule{{
			Protocol:        "tcp",
			ListenHost:      "127.0.0.1",
			ListenPort:      8443,
			Backends:        []model.L4Backend{{Host: "127.0.0.1", Port: 9443}},
			EgressProfileID: &profileID,
			Revision:        1,
		}},
	}

	if err := app.snapshotActivator()(context.Background(), Snapshot{}, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}

	if len(l4Applier.snapshotCalls()) != 1 {
		t.Fatalf("Apply calls = %d, want 1", len(l4Applier.snapshotCalls()))
	}
	if len(l4Applier.wireGuardCalls()) != 0 {
		t.Fatal("snapshotActivator should not call legacy wireguard-aware L4 apply")
	}
	if len(l4Applier.egressProfileCalls()) != 0 {
		t.Fatal("snapshotActivator should not call legacy egress-aware L4 apply")
	}
}

func TestSnapshotActivatorPassesEgressProfilesToHTTPApplier(t *testing.T) {
	httpApplier := &testEgressHTTPApplier{}
	app := newAppWithHTTPDeps(
		Config{AgentID: "local-agent"},
		store.NewInMemory(),
		newTestSyncClient(nil, syncResponse{}),
		httpApplier,
		nil,
		nil,
		nil,
	)

	profileID := 27
	next := Snapshot{
		EgressProfiles: []model.EgressProfile{{
			ID:       profileID,
			Name:     "socks exit",
			Type:     "socks",
			ProxyURL: "socks5://127.0.0.1:1080",
			Enabled:  true,
			Revision: 1,
		}},
		Rules: []model.HTTPRule{{
			FrontendURL:     "http://media.example.test",
			Backends:        []model.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			EgressProfileID: &profileID,
			Revision:        1,
		}},
	}

	if err := app.snapshotActivator()(context.Background(), Snapshot{}, next); err != nil {
		t.Fatalf("snapshotActivator returned error: %v", err)
	}

	calls := httpApplier.egressProfileCalls()
	if len(calls) != 1 {
		t.Fatalf("ApplyWithRelayWireGuardAndEgressProfiles calls = %d, want 1", len(calls))
	}
	if len(calls[0].egressProfiles) != 1 || calls[0].egressProfiles[0].ID != profileID {
		t.Fatalf("egress profiles passed to http applier = %+v", calls[0].egressProfiles)
	}
}

func TestRunDoesNotApplyL4WhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForRuntimeState(t, time.Second, func() bool {
		state, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		return state.CurrentRevision == 7
	}, func() string {
		return "expected runtime state to advance to revision 7"
	})
	if calls := l4Applier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no l4 apply calls for omitted payload, got %d", len(calls))
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDoesNotApplyRelayWhenHeartbeatOmitsPayload(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok", Revision: 7}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForRuntimeState(t, time.Second, func() bool {
		state, err := mem.LoadRuntimeState()
		if err != nil {
			t.Fatalf("failed to load runtime state: %v", err)
		}
		return state.CurrentRevision == 7
	}, func() string {
		return "expected runtime state to advance to revision 7"
	})
	if calls := relayApplier.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no relay apply calls for omitted payload, got %d", len(calls))
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyL4Rules(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: 9000,
			Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 9001}},
			Revision:   4,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		L4Rules:        []model.L4Rule{},
	}})
	l4Applier := &testL4Applier{}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, l4Applier.snapshotCalls, 2, "l4 clear")
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear l4 apply calls, got %d", len(calls))
	}
	if calls[1].rules == nil || len(calls[1].rules) != 0 {
		t.Fatalf("expected explicit empty l4 rules on clear, got %+v", calls[1].rules)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunAppliesExplicitEmptyRelayListeners(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{{
			ID:         31,
			AgentID:    "remote-agent-5",
			Name:       "relay-a",
			ListenHost: "127.0.0.1",
			ListenPort: 9443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 7,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       7,
		RelayListeners: []model.RelayListener{},
	}})
	relayApplier := &testRelayApplier{}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	calls := waitForObservedCalls(t, time.Second, relayApplier.snapshotCalls, 2, "relay clear")
	if len(calls) != 2 {
		t.Fatalf("expected startup and clear relay apply calls, got %d", len(calls))
	}
	if calls[1].listeners == nil || len(calls[1].listeners) != 0 {
		t.Fatalf("expected explicit empty relay listeners on clear, got %+v", calls[1].listeners)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunClearsStoredL4RulesWhenRelayListenersAreExplicitlyCleared(t *testing.T) {
	cfg := Config{
		AgentID:           "local-agent",
		HeartbeatInterval: 5 * time.Millisecond,
	}
	mem := store.NewInMemory()
	listenPort := pickFreeTCPPort(t)
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules: []model.L4Rule{{
			Protocol:   "tcp",
			ListenHost: "127.0.0.1",
			ListenPort: listenPort,
			Backends: []model.L4Backend{{
				Host: "remote-backend.example.test",
				Port: 26966,
			}},
			RelayLayers: [][]int{{5}},
			Revision:    5,
		}},
		RelayListeners: []model.RelayListener{{
			ID:         5,
			AgentID:    "remote-agent",
			Name:       "remote-hop",
			ListenHost: "relay.remote.example",
			ListenPort: 2443,
			Enabled:    true,
			TLSMode:    "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "sha256",
				Value: "pin-value",
			}},
			Revision: 5,
		}},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "ok",
		Revision:       6,
		L4Rules:        []model.L4Rule{},
		RelayListeners: []model.RelayListener{},
	}})
	l4Applier := newL4RuntimeManagerWithRelay(&testRelayTLSProvider{})
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	applied := waitForAppliedSnapshot(t, time.Second, mem, func(snapshot Snapshot) bool {
		return snapshot.L4Rules != nil && len(snapshot.L4Rules) == 0 &&
			snapshot.RelayListeners != nil && len(snapshot.RelayListeners) == 0
	})
	if applied.L4Rules == nil || len(applied.L4Rules) != 0 {
		t.Fatalf("expected applied l4 rules cleared, got %+v", applied.L4Rules)
	}
	if applied.RelayListeners == nil || len(applied.RelayListeners) != 0 {
		t.Fatalf("expected applied relay listeners cleared, got %+v", applied.RelayListeners)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != "" {
		t.Fatalf("expected no sync error after relay clear, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsL4ApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		L4Rules:        []model.L4Rule{},
	}})
	l4Applier := &testL4Applier{applyErr: errors.New("l4 apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "l4 apply failed")
	if state.Metadata["last_sync_error"] != "l4 apply failed" {
		t.Fatalf("expected l4 apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsRelayApplyFailuresInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{
		DesiredVersion: "2.0",
		Revision:       9,
		RelayListeners: []model.RelayListener{},
	}})
	relayApplier := &testRelayApplier{applyErr: errors.New("relay apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	state := waitForLastSyncError(t, time.Second, mem.LoadRuntimeState, "relay apply failed")
	if state.Metadata["last_sync_error"] != "relay apply failed" {
		t.Fatalf("expected relay apply failure metadata, got %v", state.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunKeepsRunningAfterStartupL4HydrationFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		L4Rules:        []model.L4Rule{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	l4Applier := &testL4Applier{applyErr: errors.New("startup l4 apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, l4Applier, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req := waitForRequest(t, client, time.Second)
	if req.LastApplyStatus != "error" || req.LastApplyMessage != "startup l4 apply failed" {
		t.Fatalf("expected startup l4 failure to be reported on next heartbeat, got %+v", req)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunKeepsRunningAfterStartupRelayHydrationFailure(t *testing.T) {
	cfg := Config{HeartbeatInterval: time.Hour}
	mem := store.NewInMemory()
	stored := Snapshot{
		DesiredVersion: "stored",
		Revision:       5,
		RelayListeners: []model.RelayListener{},
	}
	if err := mem.SaveAppliedSnapshot(stored); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{err: errors.New("heartbeat failed")})
	relayApplier := &testRelayApplier{applyErr: errors.New("startup relay apply failed")}
	app := newAppWithDeps(cfg, mem, client, nil, nil, relayApplier)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req := waitForRequest(t, client, time.Second)
	if req.LastApplyStatus != "error" || req.LastApplyMessage != "startup relay apply failed" {
		t.Fatalf("expected startup relay failure to be reported on next heartbeat, got %+v", req)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestAppCloseResetsRelayTimeoutOverridesWithoutRun(t *testing.T) {
	resetCalls := 0
	app := &App{
		relayTimeoutReset: func() {
			resetCalls++
		},
	}

	if err := app.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if resetCalls != 1 {
		t.Fatalf("relay timeout reset calls = %d", resetCalls)
	}
	if app.relayTimeoutReset != nil {
		t.Fatal("expected relayTimeoutReset to be cleared after Close()")
	}

	if err := app.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if resetCalls != 1 {
		t.Fatalf("relay timeout reset calls after second Close() = %d", resetCalls)
	}
}
