package app

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestRunStopsCleanlyOnContextCancel(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"

	application := New(cfg, http.NewServeMux(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}
}

func TestNewWiresServerErrorLog(t *testing.T) {
	cfg := config.Default()
	var sink bytes.Buffer
	logger := log.New(&sink, "test ", 0)

	application := New(cfg, http.NewServeMux(), logger, nil)
	if application.server.ErrorLog != logger {
		t.Fatal("expected injected logger to be used as server ErrorLog")
	}
}

func TestRunStartsLocalAgentBeforeReturningWhenContextAlreadyCanceled(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true

	var started atomic.Bool
	application := New(cfg, http.NewServeMux(), nil, func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		started.Store(true)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !started.Load() {
		t.Fatal("expected embedded local agent starter to run before Run() returns")
	}
}
