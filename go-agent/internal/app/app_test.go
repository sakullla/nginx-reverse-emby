package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	client := newTestSyncClient([]syncResponse{{err: errSync}}, syncResponse{})
	app := newAppWithDeps(cfg, mem, client)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := app.Run(ctx); !errors.Is(err, errSync) {
		t.Fatalf("expected sync error, got %v", err)
	}

	state, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != errSync.Error() {
		t.Fatalf("expected last_sync_error metadata, got %v", state.Metadata)
	}
}

func TestRunKeepsRunningWhenAppliedSnapshotExists(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "1.0"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	client := newTestSyncClient(nil, syncResponse{err: errors.New("boom")})
	app := newAppWithDeps(cfg, mem, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("expected nil after cancellation, got %v", err)
	}
}

func TestRunPersistsDesiredSnapshot(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	expected := Snapshot{DesiredVersion: "2.0"}
	client := newTestSyncClient(nil, syncResponse{snapshot: expected})
	app := newAppWithDeps(cfg, mem, client)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)

	snap, err := mem.LoadDesiredSnapshot()
	if err != nil {
		t.Fatalf("failed to load desired snapshot: %v", err)
	}
	if snap.DesiredVersion != expected.DesiredVersion {
		t.Fatalf("expected desired %s, got %s", expected.DesiredVersion, snap.DesiredVersion)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunRecordsSyncErrorsInRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	state := store.RuntimeState{
		Metadata: map[string]string{
			"current_revision": "7",
			"foo":              "bar",
		},
	}
	if err := mem.SaveRuntimeState(state); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient([]syncResponse{
		{err: errors.New("boom")},
		{snapshot: Snapshot{DesiredVersion: "new"}},
	}, syncResponse{})

	app := newAppWithDeps(cfg, mem, client)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForCalls(t, client, 1, time.Second)
	current, err := mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if current.Metadata["last_sync_error"] != "boom" {
		t.Fatalf("expected failure metadata, got %v", current.Metadata)
	}
	if current.Metadata["foo"] != "bar" {
		t.Fatalf("expected other metadata preserved, got %v", current.Metadata)
	}

	waitForCalls(t, client, 2, time.Second)
	current, err = mem.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if _, ok := current.Metadata["last_sync_error"]; ok {
		t.Fatalf("expected failure metadata cleared, got %v", current.Metadata)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

type syncResponse struct {
	snapshot Snapshot
	err      error
}

type testSyncClient struct {
	mu        sync.Mutex
	responses []syncResponse
	fallback  syncResponse
	callCount int32
}

func newTestSyncClient(responses []syncResponse, fallback syncResponse) *testSyncClient {
	return &testSyncClient{
		responses: append([]syncResponse(nil), responses...),
		fallback:  fallback,
	}
}

func (c *testSyncClient) Sync(_ context.Context, _ SyncRequest) (Snapshot, error) {
	atomic.AddInt32(&c.callCount, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) > 0 {
		resp := c.responses[0]
		c.responses = c.responses[1:]
		return resp.snapshot, resp.err
	}
	return c.fallback.snapshot, c.fallback.err
}

func waitForCalls(t *testing.T, client *testSyncClient, target int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if int(atomic.LoadInt32(&client.callCount)) >= target {
			return
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d sync calls", target)
}
