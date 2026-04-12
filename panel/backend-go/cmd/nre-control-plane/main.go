package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localAgentRuntime interface {
	Start(context.Context) error
	SyncNow(context.Context) error
}

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}

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

var initializeControlPlane = func(ctx context.Context, cfg config.Config) error {
	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	return service.NewRelayListenerService(cfg, store).Bootstrap(ctx)
}

var runManagedCertificateRenewalPass = func(ctx context.Context, cfg config.Config) error {
	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
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

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
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

	serviceStore, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return nil, err
	}

	systemSvc := service.NewSystemService(cfg)
	agentSvc := service.NewAgentService(cfg, serviceStore)
	ruleSvc := service.NewRuleService(cfg, serviceStore)
	l4Svc := service.NewL4RuleService(cfg, serviceStore)
	versionSvc := service.NewVersionPolicyService(serviceStore)
	relaySvc := service.NewRelayListenerService(cfg, serviceStore)
	certSvc := service.NewCertificateService(cfg, serviceStore)

	runtimeStore, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return nil, err
	}
	runtime, err := newLocalAgentRuntime(cfg, runtimeStore)
	if err != nil {
		return nil, err
	}

	agentSvc.SetLocalApplyTrigger(runtime.SyncNow)
	certSvc.SetLocalApplyTrigger(runtime.SyncNow)

	handler, err := newHandlerWithDependencies(cfg, httpapi.Dependencies{
		SystemService:        systemSvc,
		AgentService:         agentSvc,
		RuleService:          ruleSvc,
		L4RuleService:        l4Svc,
		VersionPolicyService: versionSvc,
		RelayListenerService: relaySvc,
		CertificateService:   certSvc,
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
