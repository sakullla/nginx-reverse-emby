package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/localagent"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

var newLocalAgentStarter = func(cfg config.Config) (app.LocalAgentStarter, error) {
	if !cfg.EnableLocalAgent {
		return nil, nil
	}

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		return nil, err
	}

	runtime, err := localagent.NewRuntime(cfg, store)
	if err != nil {
		return nil, err
	}
	return runtime.Start, nil
}

func newControlPlaneApp(cfg config.Config, logger *log.Logger) (*app.App, error) {
	handler, err := newHandler(cfg)
	if err != nil {
		return nil, err
	}

	startLocalAgent, err := newLocalAgentStarter(cfg)
	if err != nil {
		return nil, err
	}

	return app.New(cfg, handler, logger, startLocalAgent), nil
}
