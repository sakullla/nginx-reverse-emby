package app

import (
	"context"
	"testing"
	"time"
)

func TestNewAppBuildsDefaultRuntime(t *testing.T) {
	app, err := New(Config{AgentID: "local"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if app == nil {
		t.Fatal("expected non-nil app")
	}
}

func TestRunWaitsForCancellation(t *testing.T) {
	app, err := New(Config{AgentID: "local"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	select {
	case err := <-done:
		t.Fatalf("Run returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after cancellation")
	}
}
