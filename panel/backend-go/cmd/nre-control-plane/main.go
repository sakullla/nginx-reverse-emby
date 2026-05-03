package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var (
	appVersion = "dev"
	buildTime  = "dev"
	goVersion  = "dev"
)

type localAgentRuntime interface {
	Start(context.Context) error
	SyncNow(context.Context) error
	DiagnoseSnapshot(context.Context, storage.Snapshot, service.TaskEnvelope) (map[string]any, error)
}

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cfg.AppVersion = appVersion
	cfg.BuildTime = buildTime
	cfg.GoVersion = goVersion
	logPanelTokenWarning(log.Default(), cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := initializeControlPlane(ctx, cfg); err != nil {
		log.Fatal(err)
	}
	startManagedCertificateAutoRenewLoop(ctx, cfg, nil)

	application, err := newControlPlaneApp(cfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := application.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func logPanelTokenWarning(logger *log.Logger, cfg config.Config) {
	if strings.TrimSpace(cfg.PanelToken) != "" {
		return
	}
	if logger == nil {
		logger = log.Default()
	}
	logger.Println("[security] panel token is empty; panel API authentication is disabled")
}

var newHandler = func(cfg config.Config) (http.Handler, error) {
	return httpapi.NewRouter(httpapi.Dependencies{Config: cfg})
}

var newHandlerWithDependencies = func(cfg config.Config, deps httpapi.Dependencies) (http.Handler, error) {
	deps.Config = cfg
	return httpapi.NewRouter(deps)
}

var newLocalAgentRuntime = func(cfg config.Config, store localagent.Store) (localAgentRuntime, error) {
	return localagent.NewRuntime(cfg, store)
}

var openConfiguredStore = storage.NewConfiguredStore

func guardLegacyNonSQLiteState(dataDir string) error {
	dbPath := filepath.Join(dataDir, "panel.db")
	if _, err := os.Stat(dbPath); err == nil {
		return nil
	}

	if v := strings.TrimSpace(os.Getenv("PANEL_STORAGE_BACKEND")); v != "" && !strings.EqualFold(v, "sqlite") {
		return fmt.Errorf("detected legacy storage backend %q in PANEL_STORAGE_BACKEND; migrate data to SQLite before starting the pure-Go control plane", v)
	}

	legacyMarkers := []string{
		filepath.Join(dataDir, "state.json"),
		filepath.Join(dataDir, "agents.json"),
		filepath.Join(dataDir, "prisma", "schema.prisma"),
	}
	for _, p := range legacyMarkers {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("detected legacy state file %q; migrate data to panel.db before starting the pure-Go control plane", p)
		}
	}

	entries, err := os.ReadDir(dataDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".db") && !strings.EqualFold(name, "panel.db") {
				return fmt.Errorf("detected legacy database file %q; migrate data to panel.db before starting the pure-Go control plane", name)
			}
		}
	}

	return nil
}

var initializeControlPlane = func(ctx context.Context, cfg config.Config) error {
	if databaseDriverUsesSQLite(cfg.DatabaseDriver) {
		if err := guardLegacyNonSQLiteState(cfg.DataDir); err != nil {
			return err
		}
	}
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	return service.NewRelayListenerService(cfg, store).Bootstrap(ctx)
}

func databaseDriverUsesSQLite(driver string) bool {
	driver = strings.ToLower(strings.TrimSpace(driver))
	return driver == "" || driver == "sqlite"
}

var runManagedCertificateRenewalPass = func(ctx context.Context, cfg config.Config) error {
	store, err := openConfiguredStore(cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	return service.NewCertificateService(cfg, store).RunRenewalPass(ctx)
}

var managedCertificateAutoRenewInitialDelay = 10 * time.Second

func startManagedCertificateAutoRenewLoop(ctx context.Context, cfg config.Config, logger *log.Logger) {
	if !cfg.ManagedDNSCertificatesEnabled || cfg.ManagedCertificateRenewInterval <= 0 {
		return
	}
	if logger == nil {
		logger = log.Default()
	}

	go func() {
		initialTimer := time.NewTimer(managedCertificateAutoRenewInitialDelay)
		defer initialTimer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-initialTimer.C:
			if err := runManagedCertificateRenewalPass(ctx, cfg); err != nil {
				logger.Printf("[cert] initial auto renew cycle failed: %v", err)
			}
		}

		ticker := time.NewTicker(cfg.ManagedCertificateRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runManagedCertificateRenewalPass(ctx, cfg); err != nil {
					logger.Printf("[cert] managed certificate auto renew cycle failed: %v", err)
				}
			}
		}
	}()
}

var newLocalAgentStarter = func(cfg config.Config) (app.LocalAgentStarter, error) {
	if !cfg.EnableLocalAgent {
		return nil, nil
	}

	store, err := openConfiguredStore(cfg)
	if err != nil {
		return nil, err
	}

	runtime, err := newLocalAgentRuntime(cfg, store)
	if err != nil {
		return nil, err
	}
	return runtime.Start, nil
}

func newControlPlaneApp(cfg config.Config, logger *log.Logger) (*app.App, error) {
	if !cfg.EnableLocalAgent {
		handler, err := newHandler(cfg)
		if err != nil {
			return nil, err
		}
		return app.New(cfg, handler, logger, nil), nil
	}

	serviceStore, err := openConfiguredStore(cfg)
	if err != nil {
		return nil, err
	}

	systemSvc := service.NewSystemService(cfg, serviceStore)
	agentSvc := service.NewAgentService(cfg, serviceStore)
	ruleSvc := service.NewRuleService(cfg, serviceStore)
	l4Svc := service.NewL4RuleService(cfg, serviceStore)
	versionSvc := service.NewVersionPolicyService(serviceStore)
	relaySvc := service.NewRelayListenerService(cfg, serviceStore)
	certSvc := service.NewCertificateService(cfg, serviceStore)

	runtimeStore, err := openConfiguredStore(cfg)
	if err != nil {
		return nil, err
	}
	runtime, err := newLocalAgentRuntime(cfg, runtimeStore)
	if err != nil {
		return nil, err
	}

	agentSvc.SetLocalApplyTrigger(runtime.SyncNow)
	ruleSvc.SetLocalApplyTrigger(runtime.SyncNow)
	l4Svc.SetLocalApplyTrigger(runtime.SyncNow)
	relaySvc.SetLocalApplyTrigger(runtime.SyncNow)
	certSvc.SetLocalApplyTrigger(runtime.SyncNow)

	taskSvc := service.NewTaskService(service.TaskServiceConfig{})

	localTaskSession := localagent.NewLocalTaskSessionWithDiagnostics(cfg.LocalAgentID, taskSvc, serviceStore, runtime)
	if err := localTaskSession.Register(); err != nil {
		log.Printf("[local-agent] failed to register local task session: %v", err)
	}

	handler, err := newHandlerWithDependencies(cfg, httpapi.Dependencies{
		SystemService:        systemSvc,
		AgentService:         agentSvc,
		RuleService:          ruleSvc,
		L4RuleService:        l4Svc,
		VersionPolicyService: versionSvc,
		RelayListenerService: relaySvc,
		CertificateService:   certSvc,
		BackupService:        service.NewBackupService(cfg, serviceStore),
		TaskService:          taskSvc,
	})
	if err != nil {
		return nil, err
	}

	controlPlaneApp := app.New(cfg, handler, logger, runtime.Start)
	controlPlaneApp.SetCleanup(func() error {
		runtimeErr := runtimeStore.Close()
		serviceErr := serviceStore.Close()
		if runtimeErr != nil {
			return runtimeErr
		}
		return serviceErr
	})
	return controlPlaneApp, nil
}
