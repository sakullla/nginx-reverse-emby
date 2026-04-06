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

func TestRunRefreshesCurrentRevisionFromRuntimeState(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "100"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	app := newAppWithDeps(cfg, mem, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	req1 := waitForRequest(t, client, time.Second)
	if req1.CurrentRevision != 100 {
		t.Fatalf("expected first request revision 100, got %d", req1.CurrentRevision)
	}

	if err := mem.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "200"},
	}); err != nil {
		t.Fatalf("failed to update runtime state: %v", err)
	}

	req2 := waitForRequest(t, client, time.Second)
	if req2.CurrentRevision != 200 {
		t.Fatalf("expected second request revision 200, got %d", req2.CurrentRevision)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
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

func TestRunRecordsSaveDesiredSnapshotFailures(t *testing.T) {
	cfg := Config{HeartbeatInterval: 5 * time.Millisecond}
	inner := store.NewInMemory()
	fs := &failingStore{delegate: inner, failOnNthSave: 2}
	if err := fs.SaveAppliedSnapshot(Snapshot{DesiredVersion: "baseline"}); err != nil {
		t.Fatalf("failed to seed applied snapshot: %v", err)
	}
	if err := fs.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{"current_revision": "1"},
	}); err != nil {
		t.Fatalf("failed to seed runtime state: %v", err)
	}

	client := newTestSyncClient(nil, syncResponse{snapshot: Snapshot{DesiredVersion: "ok"}})
	app := newAppWithDeps(cfg, fs, client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx)
	}()

	waitForRequest(t, client, time.Second) // initial request
	waitForCalls(t, client, 2, time.Second) // second request triggers failure

	state, err := fs.LoadRuntimeState()
	if err != nil {
		t.Fatalf("failed to load runtime state: %v", err)
	}
	if state.Metadata["last_sync_error"] != "persistence fail" {
		t.Fatalf("expected persistence failure metadata, got %v", state.Metadata)
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
	reqCh     chan SyncRequest
}

func newTestSyncClient(responses []syncResponse, fallback syncResponse) *testSyncClient {
	return &testSyncClient{
		responses: append([]syncResponse(nil), responses...),
		fallback:  fallback,
		reqCh:     make(chan SyncRequest, 16),
	}
}

func (c *testSyncClient) Sync(_ context.Context, request SyncRequest) (Snapshot, error) {
	atomic.AddInt32(&c.callCount, 1)
	select {
	case c.reqCh <- request:
	default:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) > 0 {
		resp := c.responses[0]
		c.responses = c.responses[1:]
		return resp.snapshot, resp.err
	}
	return c.fallback.snapshot, c.fallback.err
}

func waitForRequest(t *testing.T, client *testSyncClient, timeout time.Duration) SyncRequest {
	select {
	case req := <-client.reqCh:
		return req
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for sync request")
	}
	return SyncRequest{}
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

type failingStore struct {
	delegate      store.Store
	failOnNthSave int
	saveCount     int
}

func (f *failingStore) SaveDesiredSnapshot(snapshot Snapshot) error {
	f.saveCount++
	if f.failOnNthSave > 0 && f.saveCount >= f.failOnNthSave {
		return errors.New("persistence fail")
	}
	return f.delegate.SaveDesiredSnapshot(snapshot)
}

func (f *failingStore) LoadDesiredSnapshot() (Snapshot, error) {
	return f.delegate.LoadDesiredSnapshot()
}

func (f *failingStore) SaveAppliedSnapshot(snapshot Snapshot) error {
	return f.delegate.SaveAppliedSnapshot(snapshot)
}

func (f *failingStore) LoadAppliedSnapshot() (Snapshot, error) {
	return f.delegate.LoadAppliedSnapshot()
}

func (f *failingStore) SaveRuntimeState(state store.RuntimeState) error {
	return f.delegate.SaveRuntimeState(state)
}

func (f *failingStore) LoadRuntimeState() (store.RuntimeState, error) {
	return f.delegate.LoadRuntimeState()
}
