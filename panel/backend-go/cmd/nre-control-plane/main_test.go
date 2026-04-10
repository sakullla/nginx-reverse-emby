package main

import (
	"context"
	"net/http"
	"testing"

	controlplaneapp "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestNewControlPlaneAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true

	started := make(chan struct{}, 1)

	previousNewHandler := newHandler
	previousNewLocalAgentStarter := newLocalAgentStarter
	t.Cleanup(func() {
		newHandler = previousNewHandler
		newLocalAgentStarter = previousNewLocalAgentStarter
	})

	newHandler = func(config.Config) (http.Handler, error) {
		return http.NewServeMux(), nil
	}
	newLocalAgentStarter = func(config.Config) (controlplaneapp.LocalAgentStarter, error) {
		return func(context.Context) error {
			started <- struct{}{}
			return nil
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
