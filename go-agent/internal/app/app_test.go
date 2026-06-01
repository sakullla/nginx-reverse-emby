package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
)

func TestNewBuildsControlPlaneWiring(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
		HTTPTransport: config.HTTPTransportConfig{
			TLSHandshakeTimeout:   22 * time.Second,
			ResponseHeaderTimeout: 23 * time.Second,
		},
	}

	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	if _, ok := app.store.(*core.Filesystem); !ok {
		t.Fatalf("store = %T, want filesystem store", app.store)
	}
	if app.syncClient == nil {
		t.Fatal("syncClient = nil")
	}
	if app.runtime == nil {
		t.Fatal("runtime = nil")
	}
	if app.taskClient == nil {
		t.Fatal("taskClient = nil")
	}
	transport := extractPrivateTransport(t, app.taskClient)
	if transport.ResponseHeaderTimeout != 23*time.Second {
		t.Fatalf("task ResponseHeaderTimeout = %v", transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != 22*time.Second {
		t.Fatalf("task TLSHandshakeTimeout = %v", transport.TLSHandshakeTimeout)
	}
}

func TestNewRegistersConfiguredModules(t *testing.T) {
	tests := []struct {
		name              string
		wireGuardEnabled  bool
		wireGuardExplicit bool
		want              []string
	}{
		{
			name:              "implicit default",
			wireGuardEnabled:  false,
			wireGuardExplicit: false,
			want:              []string{"certs", "diagnostics", "egress", "http", "wireguard", "relay", "l4", "traffic"},
		},
		{
			name:              "explicit disabled",
			wireGuardEnabled:  false,
			wireGuardExplicit: true,
			want:              []string{"certs", "diagnostics", "egress", "http", "relay", "l4", "traffic"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, err := New(Config{
				AgentID:           "agent",
				AgentName:         "agent",
				MasterURL:         "https://master.example.com",
				AgentToken:        "token",
				CurrentVersion:    "0.1.0",
				DataDir:           t.TempDir(),
				WireGuardEnabled:  tc.wireGuardEnabled,
				WireGuardExplicit: tc.wireGuardExplicit,
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(func() { _ = app.Close() })

			if got := app.ModuleNames(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ModuleNames() = %v, want %v", got, tc.want)
			}
		})
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
	app := &App{diagnosticModule: diagnosticModule}

	got, err := app.Diagnose(context.Background(), control.TaskTypeDiagnoseHTTPRule, 77)
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if got["kind"] != "http" || got["rule_id"] != 77 {
		t.Fatalf("Diagnose() = %+v, want http report for rule 77", got)
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

	_, err := app.DiagnoseSnapshot(context.Background(), snapshot, control.TaskTypeDiagnoseHTTPRule, 89)
	if err == nil || !strings.Contains(err.Error(), "no healthy backend candidates") {
		t.Fatalf("DiagnoseSnapshot() error = %v, want registry cache source backoff", err)
	}
}

func TestRunReturnsInitialSyncErrorWhenNoAppliedSnapshot(t *testing.T) {
	errSync := errors.New("sync failed")
	app := newAppWithAllDeps(
		Config{},
		core.NewInMemory(),
		syncClientFunc(func(context.Context, SyncRequest) (Snapshot, error) {
			return Snapshot{}, errSync
		}),
		nil,
		nil,
	)

	if err := app.Run(context.Background()); !errors.Is(err, errSync) {
		t.Fatalf("Run() error = %v, want %v", err, errSync)
	}
}

func TestAdvertisedCapabilitiesIncludeConfiguredModules(t *testing.T) {
	got := advertisedCapabilities(Config{WireGuardEnabled: false, WireGuardExplicit: true})
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "managed_certs", "diagnostics", "egress_profiles", "relay", "traffic_stats"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advertisedCapabilities() = %v, want %v", got, want)
	}
}

type syncClientFunc func(context.Context, SyncRequest) (Snapshot, error)

func (f syncClientFunc) Sync(ctx context.Context, req SyncRequest) (Snapshot, error) {
	return f(ctx, req)
}

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

func extractPrivateTransport(t *testing.T, client any) *http.Transport {
	t.Helper()

	value := reflect.ValueOf(client)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		t.Fatalf("client = %T", client)
	}
	field := value.Elem().FieldByName("transport")
	if !field.IsValid() {
		t.Fatalf("transport field not found on %T", client)
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(*http.Transport)
}

var _ SyncClient = (*control.SyncClient)(nil)
