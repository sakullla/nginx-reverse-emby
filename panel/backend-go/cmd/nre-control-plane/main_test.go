//go:build integration

package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localAgentRuntimeStub struct {
	start         func(context.Context) error
	applyRevision func(context.Context, storage.Snapshot) error
}

func (s localAgentRuntimeStub) Start(ctx context.Context) error {
	if s.start != nil {
		return s.start(ctx)
	}
	return nil
}

func (s localAgentRuntimeStub) ApplyRevision(ctx context.Context, snapshot storage.Snapshot) error {
	if s.applyRevision != nil {
		return s.applyRevision(ctx, snapshot)
	}
	return nil
}

func (s localAgentRuntimeStub) ApplyRevisionWithDrainTimeout(ctx context.Context, snapshot storage.Snapshot, _ time.Duration) error {
	return s.ApplyRevision(ctx, snapshot)
}

func (s localAgentRuntimeStub) DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error) {
	return map[string]any{}, nil
}

type closeTrackingHandler struct {
	http.Handler
	closed bool
}

func (h *closeTrackingHandler) Close() error {
	h.closed = true
	return nil
}

func TestIntegrationMigrateStorageCommandRequiresSourceAndTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing source driver",
			args: []string{"migrate-storage", "--from-dsn", "./data/panel.db", "--to-driver", "postgres", "--to-dsn", "postgres://nre:nre@postgres:5432/nre?sslmode=disable"},
			want: "--from-driver",
		},
		{
			name: "missing source dsn",
			args: []string{"migrate-storage", "--from-driver", "sqlite", "--to-driver", "postgres", "--to-dsn", "postgres://nre:nre@postgres:5432/nre?sslmode=disable"},
			want: "--from-dsn",
		},
		{
			name: "missing target driver",
			args: []string{"migrate-storage", "--from-driver", "sqlite", "--from-dsn", "./data/panel.db", "--to-dsn", "postgres://nre:nre@postgres:5432/nre?sslmode=disable"},
			want: "--to-driver",
		},
		{
			name: "missing target dsn",
			args: []string{"migrate-storage", "--from-driver", "sqlite", "--from-dsn", "./data/panel.db", "--to-driver", "postgres"},
			want: "--to-dsn",
		},
		{
			name: "same source and target",
			args: []string{"migrate-storage", "--from-driver", "sqlite", "--from-dsn", "./data/panel.db", "--to-driver", "sqlite", "--to-dsn", "./data/panel.db"},
			want: "source and target storage must be different",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseMigrateStorageCommand(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseMigrateStorageCommand() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestIntegrationMigrateStorageCommandDoesNotRunOnNormalStartup(t *testing.T) {
	previousRunMigrateStorageCommand := runMigrateStorageCommand
	previousRunControlPlaneFromEnv := runControlPlaneFromEnv
	t.Cleanup(func() {
		runMigrateStorageCommand = previousRunMigrateStorageCommand
		runControlPlaneFromEnv = previousRunControlPlaneFromEnv
	})

	runMigrateStorageCommand = func(context.Context, migrateStorageCommand) error {
		t.Fatal("migrate-storage command ran during normal startup")
		return nil
	}
	started := false
	runControlPlaneFromEnv = func() error {
		started = true
		return nil
	}

	if err := runMain(nil); err != nil {
		t.Fatalf("runMain(nil) error = %v", err)
	}
	if !started {
		t.Fatal("normal startup path was not run")
	}
}

func TestIntegrationMigrateStorageCommandDoesNotRunControlPlaneFromEnv(t *testing.T) {
	previousRunMigrateStorageCommand := runMigrateStorageCommand
	previousRunControlPlaneFromEnv := runControlPlaneFromEnv
	t.Cleanup(func() {
		runMigrateStorageCommand = previousRunMigrateStorageCommand
		runControlPlaneFromEnv = previousRunControlPlaneFromEnv
	})

	var gotCmd migrateStorageCommand
	runMigrateStorageCommand = func(_ context.Context, cmd migrateStorageCommand) error {
		gotCmd = cmd
		return nil
	}
	runControlPlaneFromEnv = func() error {
		t.Fatal("runControlPlaneFromEnv called for migrate-storage command")
		return nil
	}

	err := runMain([]string{
		"migrate-storage",
		"--from-driver", "sqlite",
		"--from-dsn", "./data/panel.db",
		"--to-driver", "postgres",
		"--to-dsn", "postgres://nre:nre@postgres:5432/nre?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("runMain(migrate-storage) error = %v", err)
	}
	if gotCmd.FromDriver != "sqlite" || gotCmd.ToDriver != "postgres" {
		t.Fatalf("migrate command = %+v", gotCmd)
	}
}

func TestIntegrationMigrateStorageCommandParsesDataRootFlags(t *testing.T) {
	cmd, err := parseMigrateStorageCommand([]string{
		"migrate-storage",
		"--from-driver", "sqlite",
		"--from-dsn", "./old-data/panel.db",
		"--from-data-root", "./old-data",
		"--to-driver", "postgres",
		"--to-dsn", "postgres://nre:nre@postgres:5432/nre?sslmode=disable",
		"--to-data-root", "./new-data",
	})
	if err != nil {
		t.Fatalf("parseMigrateStorageCommand() error = %v", err)
	}
	if cmd.FromDataRoot != "./old-data" {
		t.Fatalf("FromDataRoot = %q, want ./old-data", cmd.FromDataRoot)
	}
	if cmd.ToDataRoot != "./new-data" {
		t.Fatalf("ToDataRoot = %q, want ./new-data", cmd.ToDataRoot)
	}
}

func TestIntegrationMigrateStorageOpensSourceWithoutBootstrapAndTargetWithMigrations(t *testing.T) {
	previousOpenStore := openStore
	t.Cleanup(func() {
		openStore = previousOpenStore
	})

	var gotConfigs []storage.StoreConfig
	openStore = func(cfg storage.StoreConfig) (*storage.GormStore, error) {
		gotConfigs = append(gotConfigs, cfg)
		store, err := storage.NewSQLiteStore(t.TempDir(), "local")
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		return store, nil
	}

	err := runMigrateStorageCommand(context.Background(), migrateStorageCommand{
		FromDriver: "sqlite",
		FromDSN:    "./data/panel.db",
		ToDriver:   "postgres",
		ToDSN:      "postgres://nre:nre@postgres:5432/nre?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("runMigrateStorageCommand() error = %v", err)
	}
	if len(gotConfigs) != 2 {
		t.Fatalf("openStore calls = %d, want 2", len(gotConfigs))
	}
	if !gotConfigs[0].SkipBootstrapSchema {
		t.Fatal("source SkipBootstrapSchema = false, want true")
	}
	if gotConfigs[0].TrafficStatsEnabled {
		t.Fatal("source TrafficStatsEnabled = true, want false")
	}
	if gotConfigs[1].SkipBootstrapSchema {
		t.Fatal("target SkipBootstrapSchema = true, want false")
	}
	if !gotConfigs[1].TrafficStatsEnabled {
		t.Fatal("target TrafficStatsEnabled = false, want true")
	}
}

func TestIntegrationMigrateStorageOpensStoresWithSQLiteDSNDataRoots(t *testing.T) {
	previousOpenStore := openStore
	t.Cleanup(func() {
		openStore = previousOpenStore
	})

	var gotConfigs []storage.StoreConfig
	openStore = func(cfg storage.StoreConfig) (*storage.GormStore, error) {
		gotConfigs = append(gotConfigs, cfg)
		store, err := storage.NewSQLiteStore(t.TempDir(), "local")
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		return store, nil
	}

	sourceRoot := filepath.Join(t.TempDir(), "source")
	targetRoot := filepath.Join(t.TempDir(), "target")
	err := runMigrateStorageCommand(context.Background(), migrateStorageCommand{
		FromDriver: "sqlite",
		FromDSN:    filepath.Join(sourceRoot, "panel.db") + "?_journal_mode=WAL",
		ToDriver:   "sqlite",
		ToDSN:      filepath.Join(targetRoot, "panel.db"),
	})
	if err != nil {
		t.Fatalf("runMigrateStorageCommand() error = %v", err)
	}
	if len(gotConfigs) != 2 {
		t.Fatalf("openStore calls = %d, want 2", len(gotConfigs))
	}
	if gotConfigs[0].DataRoot != sourceRoot {
		t.Fatalf("source DataRoot = %q, want %q", gotConfigs[0].DataRoot, sourceRoot)
	}
	if gotConfigs[1].DataRoot != targetRoot {
		t.Fatalf("target DataRoot = %q, want %q", gotConfigs[1].DataRoot, targetRoot)
	}
}

func TestIntegrationMigrateStorageDefaultsTargetDataRootToSourceDataRoot(t *testing.T) {
	previousOpenStore := openStore
	t.Cleanup(func() {
		openStore = previousOpenStore
	})

	var gotConfigs []storage.StoreConfig
	openStore = func(cfg storage.StoreConfig) (*storage.GormStore, error) {
		gotConfigs = append(gotConfigs, cfg)
		store, err := storage.NewSQLiteStore(t.TempDir(), "local")
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		return store, nil
	}

	sourceRoot := filepath.Join(t.TempDir(), "source")
	err := runMigrateStorageCommand(context.Background(), migrateStorageCommand{
		FromDriver: "sqlite",
		FromDSN:    filepath.Join(sourceRoot, "panel.db"),
		ToDriver:   "postgres",
		ToDSN:      "postgres://nre:nre@postgres:5432/nre?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("runMigrateStorageCommand() error = %v", err)
	}
	if len(gotConfigs) != 2 {
		t.Fatalf("openStore calls = %d, want 2", len(gotConfigs))
	}
	if gotConfigs[0].DataRoot != sourceRoot {
		t.Fatalf("source DataRoot = %q, want %q", gotConfigs[0].DataRoot, sourceRoot)
	}
	if gotConfigs[1].DataRoot != sourceRoot {
		t.Fatalf("target DataRoot = %q, want source root %q", gotConfigs[1].DataRoot, sourceRoot)
	}
}

func TestIntegrationInitializeControlPlaneSkipsLegacySQLiteGuardForPostgres(t *testing.T) {
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

func TestIntegrationNewControlPlaneAppInstallsNoLocalAgentHandlerCleanup(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = false

	previousNewHandlerWithDependencies := newHandlerWithDependencies
	t.Cleanup(func() {
		newHandlerWithDependencies = previousNewHandlerWithDependencies
	})

	handler := &closeTrackingHandler{Handler: http.NewServeMux()}
	newHandlerWithDependencies = func(config.Config, httpapi.Dependencies) (http.Handler, error) {
		return handler, nil
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
	if !handler.closed {
		t.Fatal("handler cleanup was not called")
	}
}

func TestIntegrationNewControlPlaneAppClosesStoresWhenLocalRuntimeFails(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()

	previousOpenConfiguredStore := openConfiguredStore
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	var openedStores []*storage.GormStore
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		store, err := storage.NewSQLiteStore(t.TempDir(), gotCfg.LocalAgentID)
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		openedStores = append(openedStores, store)
		return store, nil
	}
	newLocalAgentRuntime = func(config.Config, localagent.Store) (localAgentRuntime, error) {
		return nil, errors.New("runtime failed")
	}

	if _, err := newControlPlaneApp(cfg, nil); err == nil {
		t.Fatal("newControlPlaneApp() error = nil, want runtime failure")
	}
	if len(openedStores) != 2 {
		t.Fatalf("opened stores = %d, want 2", len(openedStores))
	}
	for i, store := range openedStores {
		_, err := store.ListAgents(t.Context())
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("store %d ListAgents() error = %v, want closed database error", i, err)
		}
	}
}

func TestIntegrationNewControlPlaneAppClosesStoresWhenHandlerBuildFails(t *testing.T) {
	cfg := config.Default()
	cfg.EnableLocalAgent = true
	cfg.DataDir = t.TempDir()

	previousOpenConfiguredStore := openConfiguredStore
	previousNewHandlerWithDependencies := newHandlerWithDependencies
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
		newHandlerWithDependencies = previousNewHandlerWithDependencies
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	var openedStores []*storage.GormStore
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		store, err := storage.NewSQLiteStore(t.TempDir(), gotCfg.LocalAgentID)
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		openedStores = append(openedStores, store)
		return store, nil
	}
	newLocalAgentRuntime = func(config.Config, localagent.Store) (localAgentRuntime, error) {
		return localAgentRuntimeStub{}, nil
	}
	newHandlerWithDependencies = func(config.Config, httpapi.Dependencies) (http.Handler, error) {
		return nil, errors.New("handler failed")
	}

	if _, err := newControlPlaneApp(cfg, nil); err == nil {
		t.Fatal("newControlPlaneApp() error = nil, want handler failure")
	}
	if len(openedStores) != 2 {
		t.Fatalf("opened stores = %d, want 2", len(openedStores))
	}
	for i, store := range openedStores {
		_, err := store.ListAgents(t.Context())
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("store %d ListAgents() error = %v, want closed database error", i, err)
		}
	}
}

func TestIntegrationNewControlPlaneAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
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

func TestIntegrationNewControlPlaneAppProvidesBackupServiceWhenLocalAgentEnabled(t *testing.T) {
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

func TestIntegrationNewControlPlaneAppDoesNotWireMonitorRefreshToRuntimeApply(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local"
	cfg.LocalAgentName = "Local Agent"
	cfg.DataDir = t.TempDir()

	previousNewHandler := newHandler
	previousNewHandlerWithDependencies := newHandlerWithDependencies
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		newHandler = previousNewHandler
		newHandlerWithDependencies = previousNewHandlerWithDependencies
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	applyCalls := 0
	newHandler = func(config.Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newLocalAgentRuntime = func(_ config.Config, store localagent.Store) (localAgentRuntime, error) {
		if sqliteStore, ok := store.(*storage.SQLiteStore); ok {
			t.Cleanup(func() {
				_ = sqliteStore.Close()
			})
		}
		return localAgentRuntimeStub{applyRevision: func(context.Context, storage.Snapshot) error {
			applyCalls++
			return nil
		}}, nil
	}
	newHandlerWithDependencies = func(_ config.Config, deps httpapi.Dependencies) (http.Handler, error) {
		if _, err := deps.AgentService.MonitorSnapshot(t.Context()); err != nil {
			return nil, err
		}
		if applyCalls != 0 {
			return nil, errors.New("AgentService monitor snapshot triggered runtime apply")
		}
		return http.NewServeMux(), nil
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

func TestIntegrationNewControlPlaneAppClosesRouterOwnedStoreWhenLocalAgentEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true
	cfg.TrafficStatsEnabled = true
	cfg.DataDir = t.TempDir()

	previousOpenConfiguredStore := openConfiguredStore
	previousNewHandlerWithDependencies := newHandlerWithDependencies
	previousNewLocalAgentRuntime := newLocalAgentRuntime
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
		newHandlerWithDependencies = previousNewHandlerWithDependencies
		newLocalAgentRuntime = previousNewLocalAgentRuntime
	})

	var openedStores []*storage.GormStore
	handler := &closeTrackingHandler{Handler: http.NewServeMux()}
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		store, err := storage.NewSQLiteStore(t.TempDir(), gotCfg.LocalAgentID)
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
		openedStores = append(openedStores, store)
		return store, nil
	}
	newLocalAgentRuntime = func(config.Config, localagent.Store) (localAgentRuntime, error) {
		return localAgentRuntimeStub{}, nil
	}
	newHandlerWithDependencies = func(config.Config, httpapi.Dependencies) (http.Handler, error) {
		return handler, nil
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
	if !handler.closed {
		t.Fatal("handler cleanup was not called")
	}
	if len(openedStores) != 2 {
		t.Fatalf("opened stores = %d, want service and runtime stores", len(openedStores))
	}
	for i, store := range openedStores {
		_, err := store.ListAgents(t.Context())
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("store %d ListAgents() error = %v, want closed database error", i, err)
		}
	}
}

func TestIntegrationInitializeControlPlaneBootstrapsGlobalRelayCA(t *testing.T) {
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

func TestIntegrationStartManagedCertificateAutoRenewLoopRunsInitialPass(t *testing.T) {
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

func TestIntegrationStartTrafficCleanupLoopRunsInitialPass(t *testing.T) {
	cfg := config.Default()
	cfg.TrafficStatsEnabled = true
	cfg.TrafficCleanupInterval = time.Hour

	previousRunner := runTrafficCleanupPass
	previousDelay := trafficCleanupInitialDelay
	t.Cleanup(func() {
		runTrafficCleanupPass = previousRunner
		trafficCleanupInitialDelay = previousDelay
	})

	called := make(chan struct{}, 1)
	runTrafficCleanupPass = func(context.Context, config.Config) error {
		select {
		case called <- struct{}{}:
		default:
		}
		return nil
	}
	trafficCleanupInitialDelay = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startTrafficCleanupLoop(ctx, cfg, nil)

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial traffic cleanup pass")
	}
}

func TestIntegrationStartTrafficCleanupLoopSkipsWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.TrafficStatsEnabled = false
	cfg.TrafficCleanupInterval = time.Hour

	previousRunner := runTrafficCleanupPass
	previousDelay := trafficCleanupInitialDelay
	t.Cleanup(func() {
		runTrafficCleanupPass = previousRunner
		trafficCleanupInitialDelay = previousDelay
	})

	called := make(chan struct{}, 1)
	runTrafficCleanupPass = func(context.Context, config.Config) error {
		called <- struct{}{}
		return nil
	}
	trafficCleanupInitialDelay = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startTrafficCleanupLoop(ctx, cfg, nil)

	select {
	case <-called:
		t.Fatal("traffic cleanup pass ran while traffic stats disabled")
	case <-time.After(50 * time.Millisecond):
	}
}

type revisionRetentionStoreStub struct {
	policy     storage.RevisionRetentionPolicy
	pruneCalls int
	closeCalls int
	pruneErr   error
	closeErr   error
}

func (s *revisionRetentionStoreStub) PruneRevisionHistory(_ context.Context, policy storage.RevisionRetentionPolicy) (storage.RevisionPruneResult, error) {
	s.policy = policy
	s.pruneCalls++
	return storage.RevisionPruneResult{}, s.pruneErr
}

func (s *revisionRetentionStoreStub) Close() error {
	s.closeCalls++
	return s.closeErr
}

func TestIntegrationRunRevisionRetentionPassUsesDefaultPolicyAndAlwaysClosesStore(t *testing.T) {
	previousOpen := openRevisionRetentionStore
	t.Cleanup(func() { openRevisionRetentionStore = previousOpen })

	store := &revisionRetentionStoreStub{
		pruneErr: errors.New("prune failed"),
		closeErr: errors.New("close failed"),
	}
	openRevisionRetentionStore = func(config.Config) (revisionRetentionStore, error) {
		return store, nil
	}

	err := runRevisionRetentionPass(context.Background(), config.Default())
	if err == nil || !strings.Contains(err.Error(), "prune revision history: prune failed") || !strings.Contains(err.Error(), "close revision retention store: close failed") {
		t.Fatalf("runRevisionRetentionPass() error = %v, want prune and close failures", err)
	}
	if store.pruneCalls != 1 || store.closeCalls != 1 {
		t.Fatalf("calls = prune:%d close:%d, want 1/1", store.pruneCalls, store.closeCalls)
	}
	if store.policy != (storage.RevisionRetentionPolicy{}) {
		t.Fatalf("policy = %+v, want storage defaults", store.policy)
	}

	openRevisionRetentionStore = func(config.Config) (revisionRetentionStore, error) {
		return nil, errors.New("open failed")
	}
	err = runRevisionRetentionPass(context.Background(), config.Default())
	if err == nil || !strings.Contains(err.Error(), "open revision retention store: open failed") {
		t.Fatalf("runRevisionRetentionPass(open) error = %v", err)
	}
}

func TestIntegrationStartRevisionRetentionLoopRunsStartupRetriesAndStopsOnCancel(t *testing.T) {
	previousRunner := runRevisionRetentionPass
	previousInterval := revisionRetentionInterval
	t.Cleanup(func() {
		runRevisionRetentionPass = previousRunner
		revisionRetentionInterval = previousInterval
	})

	var calls atomic.Int32
	called := make(chan int32, 4)
	runRevisionRetentionPass = func(context.Context, config.Config) error {
		call := calls.Add(1)
		called <- call
		if call == 1 {
			return errors.New("startup prune failed")
		}
		return nil
	}
	revisionRetentionInterval = 10 * time.Millisecond
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := startRevisionRetentionLoop(ctx, config.Default(), logger)

	for want := int32(1); want <= 2; want++ {
		select {
		case got := <-called:
			if got != want {
				t.Fatalf("retention call = %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for retention call %d", want)
		}
	}
	if !strings.Contains(logs.String(), "startup") || !strings.Contains(logs.String(), "startup prune failed") {
		t.Fatalf("retention logs = %q, want startup failure context", logs.String())
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not stop after context cancellation")
	}
}

func TestIntegrationLogPanelTokenWarningWarnsWhenPanelTokenMissing(t *testing.T) {
	var buffer bytes.Buffer
	logger := log.New(&buffer, "", 0)

	logPanelTokenWarning(logger, config.Config{})

	output := buffer.String()
	if !strings.Contains(output, "panel token is empty") {
		t.Fatalf("warning output = %q", output)
	}
}

func TestIntegrationLogPanelTokenWarningSkipsWhenPanelTokenConfigured(t *testing.T) {
	var buffer bytes.Buffer
	logger := log.New(&buffer, "", 0)

	logPanelTokenWarning(logger, config.Config{PanelToken: "secret"})

	if buffer.Len() != 0 {
		t.Fatalf("expected no warning, got %q", buffer.String())
	}
}
