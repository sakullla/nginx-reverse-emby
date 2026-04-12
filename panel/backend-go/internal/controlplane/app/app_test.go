package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
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

func TestRunAlreadyCanceledContextTreatsLocalAgentCanceledAsGraceful(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true

	application := New(cfg, http.NewServeMux(), nil, func(context.Context) error {
		return context.Canceled
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := application.Run(ctx); err != nil {
		t.Fatalf("expected nil for already-canceled graceful shutdown, got %v", err)
	}
}

func TestRunReturnsNilWhenLocalAgentCancelsOnGracefulShutdown(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true

	application := New(cfg, http.NewServeMux(), nil, func(ctx context.Context) error {
		<-ctx.Done()
		return context.Canceled
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil on graceful shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}
}

func TestRunReturnsLocalAgentErrorAndShutsDownServer(t *testing.T) {
	addr := testFreeAddress(t)
	cfg := config.Default()
	cfg.ListenAddr = addr
	cfg.EnableLocalAgent = true

	triggerErr := make(chan struct{})
	localErr := errors.New("local agent failed")
	application := New(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-triggerErr:
			return localErr
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- application.Run(ctx)
	}()

	waitForHTTPUp(t, addr)
	close(triggerErr)

	select {
	case err := <-done:
		if !errors.Is(err, localErr) {
			t.Fatalf("expected local agent error %v, got %v", localErr, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return local agent error")
	}

	waitForHTTPDown(t, addr)
}

func TestRunServerFailureCancelsAndWaitsForLocalAgent(t *testing.T) {
	addr := testFreeAddress(t)
	occupied, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to occupy address %s: %v", addr, err)
	}
	defer occupied.Close()

	cfg := config.Default()
	cfg.ListenAddr = addr
	cfg.EnableLocalAgent = true

	localCanceled := make(chan struct{})
	application := New(cfg, http.NewServeMux(), nil, func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		close(localCanceled)
		return context.Canceled
	})

	start := time.Now()
	runErr := application.Run(context.Background())
	if runErr == nil {
		t.Fatal("expected server startup failure")
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("expected Run() to wait for local agent shutdown after server failure")
	}

	select {
	case <-localCanceled:
	default:
		t.Fatal("expected local agent to be canceled before Run() returns")
	}
}

func TestRunFailsWhenLocalAgentExitsNilUnexpectedly(t *testing.T) {
	cfg := config.Default()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.EnableLocalAgent = true

	application := New(cfg, http.NewServeMux(), nil, func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})

	err := application.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when local agent exits without error while app is running")
	}
	if !errors.Is(err, ErrLocalAgentExitedUnexpectedly) {
		t.Fatalf("expected ErrLocalAgentExitedUnexpectedly, got %v", err)
	}
}

func testFreeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate test listener: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func waitForHTTPUp(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	url := fmt.Sprintf("http://%s/", addr)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for server %s to start", addr)
}

func waitForHTTPDown(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	url := fmt.Sprintf("http://%s/", addr)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for server %s to stop", addr)
}
