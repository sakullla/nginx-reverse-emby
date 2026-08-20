package http

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestPluginPackageResolutionContextSurvivesClientDisconnect(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	resolveCtx, cancelResolve := pluginPackageResolutionContext(requestCtx, time.Second)
	defer cancelResolve()
	cancelRequest()
	select {
	case <-resolveCtx.Done():
		t.Fatalf("package resolution was canceled with HTTP client: %v", resolveCtx.Err())
	case <-time.After(20 * time.Millisecond):
	}
	deadline, ok := resolveCtx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
		t.Fatalf("package resolution deadline = %v, ok=%v", deadline, ok)
	}
}

func TestPluginPackageResolutionContextDefaultsShorterThanMarketplaceRefresh(t *testing.T) {
	if service.DefaultPluginPackageResolutionTimeout >= service.DefaultMarketplaceRefreshTimeout {
		t.Fatalf("package resolution timeout %s must be shorter than marketplace refresh %s", service.DefaultPluginPackageResolutionTimeout, service.DefaultMarketplaceRefreshTimeout)
	}
	ctx, cancel := pluginPackageResolutionContext(context.Background(), 0)
	defer cancel()
	deadline, ok := ctx.Deadline()
	until := time.Until(deadline)
	if !ok || until <= 0 || until > service.DefaultPluginPackageResolutionTimeout {
		t.Fatalf("package resolution deadline remaining = %s, ok=%v", until, ok)
	}
}
