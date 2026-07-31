package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
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
