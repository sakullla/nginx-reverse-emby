package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func TestNewBuildsRealWiring(t *testing.T) {
	cfg := Config{
		AgentID:        "agent",
		AgentName:      "agent",
		MasterURL:      "https://master.example.com",
		AgentToken:     "token",
		CurrentVersion: "0.1.0",
		DataDir:        t.TempDir(),
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, ok := app.store.(*store.Filesystem); !ok {
		t.Fatalf("expected filesystem store, got %T", app.store)
	}
	if app.syncClient == nil {
		t.Fatal("expected sync client to be initialized")
	}
}

func TestRunReturnsErrorWithoutAppliedSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	errSync := errors.New("boom")
	app := newAppWithDeps(cfg, mem, &testSyncClient{err: errSync})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := app.Run(ctx); !errors.Is(err, errSync) {
		t.Fatalf("expected sync error, got %v", err)
	}
}

func TestRunKeepsRunningWhenAppliedSnapshotExists(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "1.0"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	app := newAppWithDeps(cfg, mem, &testSyncClient{err: errors.New("boom")})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	select {
	case <-done:
		t.Fatal("Run exited early despite fallback snapshot")
	case <-time.After(25 * time.Millisecond):
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("expected nil after cancellation, got %v", err)
	}
}

func TestRunPersistsDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{DesiredVersion: "2.0"}
	app := newAppWithDeps(cfg, mem, &testSyncClient{snapshot: expected})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	time.Sleep(20 * time.Millisecond)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if snap.DesiredVersion != expected.DesiredVersion {
		t.Fatalf("expected desired %s, got %s", expected.DesiredVersion, snap.DesiredVersion)
	}
}

type testSyncClient struct {
	snapshot Snapshot
	err      error
}

func (c *testSyncClient) Sync(_ context.Context, _ Snapshot) (Snapshot, error) {
	if c.err != nil {
		return Snapshot{}, c.err
	}
	return c.snapshot, nil
}
