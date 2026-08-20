package http

import (
	"context"
	"testing"
	"time"
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
