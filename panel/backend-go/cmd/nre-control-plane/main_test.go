package main

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localAgentRuntimeStub struct {
	start   func(context.Context) error
	syncNow func(context.Context) error
}

func (s localAgentRuntimeStub) Start(ctx context.Context) error {
	if s.start != nil {
		return s.start(ctx)
	}
	return nil
}

func (s localAgentRuntimeStub) SyncNow(ctx context.Context) error {
	if s.syncNow != nil {
		return s.syncNow(ctx)
	}
	return nil
}

func (s localAgentRuntimeStub) DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error) {
	return map[string]any{}, nil
}

func TestNewLocalAgentStarterUsesConfiguredStore(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.DatabaseDriver = "mysql"
	cfg.DatabaseDSN = "nre:nre@tcp(mysql:3306)/nre?parseTime=true"
	cfg.DataDir = "/tmp/nre-data"
	cfg.LocalAgentID = "edge-1"
	cfg.TrafficStatsEnabled = false

	previousOpenConfiguredStore := openConfiguredStore
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	var gotStoreCfg storage.StoreConfig
	store := &storage.GormStore{}
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		gotStoreCfg = storage.StoreConfigFromConfig(gotCfg)
		return store, nil
	}
	newLocalAgentRuntime = func(_ config.Config, gotStore localagent.Store) (localAgentRuntime, error) {
		if gotStore != store {
			t.Fatalf("store = %p, want %p", gotStore, store)
		}
		return localAgentRuntimeStub{}, nil
	}

	if _, err := newLocalAgentStarter(cfg); err != nil {
		t.Fatalf("newLocalAgentStarter() error = %v", err)
	}
	if gotStoreCfg.Driver != "mysql" {
		t.Fatalf("Driver = %q", gotStoreCfg.Driver)
	}
	if gotStoreCfg.DSN != "nre:nre@tcp(mysql:3306)/nre?parseTime=true" {
		t.Fatalf("DSN = %q", gotStoreCfg.DSN)
	}
	if gotStoreCfg.DataRoot != "/tmp/nre-data" {
		t.Fatalf("DataRoot = %q", gotStoreCfg.DataRoot)
	}
	if gotStoreCfg.LocalAgentID != "edge-1" {
		t.Fatalf("LocalAgentID = %q", gotStoreCfg.LocalAgentID)
	}
	if gotStoreCfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}

func TestInitializeControlPlaneSkipsLegacySQLiteGuardForPostgres(t *testing.T) {
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "edge-1"

	if err := os.WriteFile(filepath.Join(cfg.DataDir, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed legacy marker: %v", err)
	}

	previousOpenConfiguredStore := openConfiguredStore
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
	})

	called := false
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		called = true
		if gotCfg.DatabaseDriver != "postgres" {
			t.Fatalf("DatabaseDriver = %q", gotCfg.DatabaseDriver)
		}
		store, err := storage.NewSQLiteStore(t.TempDir(), gotCfg.LocalAgentID)
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		return store, nil
	}

	if err := initializeControlPlane(context.Background(), cfg); err != nil {
		t.Fatalf("initializeControlPlane() error = %v", err)
	}
	if !called {
		t.Fatal("openConfiguredStore was not called")
	}
}

func TestNewLocalAgentStarterBuildsSQLiteStoreAndInvokesRuntime(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local-test"
	cfg.LocalAgentName = "local-test"

	started := false
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	newLocalAgentRuntime = func(gotCfg config.Config, store localagent.Store) (localAgentRuntime, error) {
		if gotCfg.LocalAgentID != "local-test" {
			t.Fatalf("LocalAgentID = %q", gotCfg.LocalAgentID)
		}
		sqliteStore, ok := store.(*storage.SQLiteStore)
		if !ok {
			t.Fatalf("store type = %T, want *storage.SQLiteStore", store)
		}
		if _, err := sqliteStore.LoadLocalSnapshot(t.Context(), gotCfg.LocalAgentID); err != nil {
			t.Fatalf("LoadLocalSnapshot() error = %v", err)
		}
		t.Cleanup(func() {
			_ = sqliteStore.Close()
		})
		return localAgentRuntimeStub{
			start: func(context.Context) error {
				started = true
				return nil
			},
		}, nil
	}

	starter, err := newLocalAgentStarter(cfg)
	if err != nil {
		t.Fatalf("newLocalAgentStarter() error = %v", err)
	}
	if starter == nil {
		t.Fatal("newLocalAgentStarter() returned nil starter")
	}
	if err := starter(t.Context()); err != nil {
		t.Fatalf("starter() error = %v", err)
	}
	if !started {
		t.Fatal("starter did not invoke runtime Start")
	}
}

func TestNewControlPlaneAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()

	started := make(chan struct{}, 1)

	previousNewHandler := newHandler
	previousNewHandlerWithDependencies := newHandlerWithDependencies
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newHandler = previousNewHandler
		newHandlerWithDependencies = previousNewHandlerWithDependencies
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	newHandler = func(config.Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newHandlerWithDependencies = func(_ config.Config, _ httpapi.Dependencies) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newLocalAgentRuntime = func(_ config.Config, store localagent.Store) (localAgentRuntime, error) {
		if sqliteStore, ok := store.(*storage.SQLiteStore); ok {
			t.Cleanup(func() {
				_ = sqliteStore.Close()
			})
		}
		return localAgentRuntimeStub{
			start: func(context.Context) error {
				started <- struct{}{}
				return nil
			},
		}, nil
	}

	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	select {
	case <-started:
	default:
		t.Fatal("embedded local agent starter was not invoked")
	}
}

func TestNewControlPlaneAppProvidesBackupServiceWhenLocalAgentEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()

	previousNewHandler := newHandler
	previousNewHandlerWithDependencies := newHandlerWithDependencies
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newHandler = previousNewHandler
		newHandlerWithDependencies = previousNewHandlerWithDependencies
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	newHandler = func(config.Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		if deps.BackupService == nil {
			t.Fatal("BackupService = nil, want configured backup service")
		}
		return http.NewServeMux(), nil
	}
	newLocalAgentRuntime = func(_ config.Config, store localagent.Store) (localAgentRuntime, error) {
		if sqliteStore, ok := store.(*storage.SQLiteStore); ok {
			t.Cleanup(func() {
				_ = sqliteStore.Close()
			})
		}
		return localAgentRuntimeStub{}, nil
	}

	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		t.Fatalf("newControlPlaneApp() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestInitializeControlPlaneBootstrapsGlobalRelayCA(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local"

	if err := initializeControlPlane(context.Background(), cfg); err != nil {
		t.Fatalf("initializeControlPlane() error = %v", err)
	}

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	certs, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("len(certs) = %d", len(certs))
	}
	if certs[0].Domain != "__relay-ca.internal" || certs[0].Usage != "relay_ca" || certs[0].CertificateType != "internal_ca" {
		t.Fatalf("relay CA row = %+v", certs[0])
	}
	if !certs[0].Enabled || certs[0].Status != "active" {
		t.Fatalf("relay CA flags = %+v", certs[0])
	}

	bundle, ok, err := store.LoadManagedCertificateMaterial(t.Context(), "__relay-ca.internal")
	if err != nil {
		t.Fatalf("LoadManagedCertificateMaterial() error = %v", err)
	}
	if !ok {
		t.Fatal("expected persisted relay CA material")
	}
	if bundle.CertPEM == "" || bundle.KeyPEM == "" {
		t.Fatalf("relay CA bundle = %+v", bundle)
	}
}

func TestStartManagedCertificateAutoRenewLoopRunsInitialPass(t *testing.T) {
	cfg := config.Default()
	cfg.ManagedDNSCertificatesEnabled = true
	cfg.ManagedCertificateRenewInterval = time.Hour

	previousRunner := runManagedCertificateRenewalPass
	previousDelay := managedCertificateAutoRenewInitialDelay
	t.Cleanup(func() {
		runManagedCertificateRenewalPass = previousRunner
		managedCertificateAutoRenewInitialDelay = previousDelay
	})

	called := make(chan struct{}, 1)
	runManagedCertificateRenewalPass = func(context.Context, config.Config) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}
	managedCertificateAutoRenewInitialDelay = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startManagedCertificateAutoRenewLoop(ctx, cfg, nil)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial managed certificate renewal pass")
	}
}

func TestLogPanelTokenWarningWarnsWhenPanelTokenMissing(t *testing.T) {
	var buffer bytes.Buffer
	logger := log.New(&buffer, "", 0)

	logPanelTokenWarning(logger, config.Config{})

	output := buffer.String()
	if !strings.Contains(output, "panel token is empty") {
		t.Fatalf("warning output = %q", output)
	}
}

func TestLogPanelTokenWarningSkipsWhenPanelTokenConfigured(t *testing.T) {
	var buffer bytes.Buffer
	logger := log.New(&buffer, "", 0)

	logPanelTokenWarning(logger, config.Config{PanelToken: "secret"})

	if buffer.Len() != 0 {
		t.Fatalf("expected no warning, got %q", buffer.String())
	}
}
