//go:build exhaustive && !integration

package embedded

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegrationRuntimeRevisionPersistenceLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	firstSource := newEmbeddedRuntimeSource(Snapshot{Revision: 9})
	firstSink := &embeddedRuntimeSink{states: make(chan RuntimeState, 16)}
	first := newEmbeddedRuntimeFixture(t, dataDir, firstSource, firstSink)

	firstCtx, firstCancel := context.WithCancel(t.Context())
	firstExit := make(chan error, 1)
	go func() { firstExit <- first.Run(firstCtx) }()
	waitEmbeddedSyncRequest(t, firstSource)
	assertEmbeddedRevisionNotPublished(t, firstSink, 9)
	if err := first.ApplyRevision(firstCtx, Snapshot{}); err == nil {
		t.Fatal("ApplyRevision() accepted a snapshot without an approved revision")
	}
	if err := first.ApplyRevision(firstCtx, Snapshot{Revision: 7}); err != nil {
		t.Fatalf("ApplyRevision(approved) error = %v", err)
	}
	waitEmbeddedRuntimeRevision(t, firstSink, 7)
	firstCancel()
	if err := waitEmbeddedRuntimeExit(t, firstExit); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	secondSource := newEmbeddedRuntimeSource(Snapshot{Revision: 7})
	second := newEmbeddedRuntimeFixture(t, dataDir, secondSource, &embeddedRuntimeSink{states: make(chan RuntimeState, 16)})
	secondCtx, secondCancel := context.WithCancel(t.Context())
	secondExit := make(chan error, 1)
	go func() { secondExit <- second.Run(secondCtx) }()
	request := waitEmbeddedSyncRequest(t, secondSource)
	if request.CurrentRevision != 7 {
		t.Fatalf("reopened sync revision = %d, want 7", request.CurrentRevision)
	}
	secondCancel()
	if err := waitEmbeddedRuntimeExit(t, secondExit); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestSanitizeSnapshotContract(t *testing.T) {
	edge := PluginDependencyEdge{
		Consumer:           PluginDependencyConsumer{Kind: "http_rule", ID: "1", ResourceGroupID: "default"},
		ProviderInstanceID: "rpc-1",
		Target:             PluginDependencyTarget{AgentID: "local", ResourceGroupID: "default", Version: 1},
	}
	for _, tc := range []struct {
		name         string
		dependencies []PluginDependencyEdge
		wantNil      bool
	}{
		{name: "nil dependency presence", wantNil: true},
		{name: "explicit empty dependency presence", dependencies: []PluginDependencyEdge{}},
		{name: "populated dependency presence", dependencies: []PluginDependencyEdge{edge}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeSnapshot(Snapshot{
				DesiredVersion:     "2.0.0",
				VersionPackage:     &VersionPackage{URL: "https://example.invalid/agent", SHA256: "digest"},
				PluginDependencies: tc.dependencies,
			})
			if got.DesiredVersion != "" || got.VersionPackage != nil {
				t.Fatalf("self-update fields survived sanitization: version=%q package=%+v", got.DesiredVersion, got.VersionPackage)
			}
			if (got.PluginDependencies == nil) != tc.wantNil || len(got.PluginDependencies) != len(tc.dependencies) {
				t.Fatalf("dependency presence = %#v, want %#v", got.PluginDependencies, tc.dependencies)
			}
			if len(tc.dependencies) == 1 && got.PluginDependencies[0] != edge {
				t.Fatalf("populated dependency = %+v, want %+v", got.PluginDependencies[0], edge)
			}
		})
	}
}

func TestRuntimeConcurrentCloseMemoizesFailure(t *testing.T) {
	wantErr := errors.New("embedded app close failed")
	started := make(chan struct{})
	release := make(chan struct{})
	dependency := &embeddedCloseFailureApp{closeFn: func() error {
		close(started)
		<-release
		return wantErr
	}}
	runtime := &Runtime{app: dependency}

	const callers = 4
	errs := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			errs <- runtime.Close()
		}()
	}
	ready.Wait()
	<-started
	select {
	case err := <-errs:
		t.Fatalf("Close() returned before the dependency completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	for range callers {
		if err := <-errs; !errors.Is(err, wantErr) {
			t.Fatalf("concurrent Close() error = %v, want %v", err, wantErr)
		}
	}
	if err := runtime.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("memoized Close() error = %v, want %v", err, wantErr)
	}
	if calls := dependency.closeCalls.Load(); calls != 1 {
		t.Fatalf("embedded app Close() calls = %d, want 1", calls)
	}
}

type embeddedCloseFailureApp struct {
	closeCalls atomic.Int32
	closeFn    func() error
}

func (*embeddedCloseFailureApp) Run(context.Context) error     { return nil }
func (*embeddedCloseFailureApp) SyncNow(context.Context) error { return nil }
func (app *embeddedCloseFailureApp) Close() error {
	app.closeCalls.Add(1)
	return app.closeFn()
}

type embeddedRuntimeSource struct {
	snapshot Snapshot
	requests chan SyncRequest
}

func newEmbeddedRuntimeSource(snapshot Snapshot) *embeddedRuntimeSource {
	return &embeddedRuntimeSource{snapshot: snapshot, requests: make(chan SyncRequest, 8)}
}

func (source *embeddedRuntimeSource) Sync(_ context.Context, request SyncRequest) (Snapshot, error) {
	source.requests <- request
	return source.snapshot, nil
}

type embeddedRuntimeSink struct{ states chan RuntimeState }

func (sink *embeddedRuntimeSink) Save(_ context.Context, state RuntimeState) error {
	sink.states <- state
	return nil
}

func newEmbeddedRuntimeFixture(t *testing.T, dataDir string, source SyncSource, sink StateSink) *Runtime {
	t.Helper()
	runtime, err := New(Config{
		AgentID: "local", AgentName: "local", DataDir: dataDir, HeartbeatInterval: time.Hour,
	}, source, sink)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func waitEmbeddedSyncRequest(t *testing.T, source *embeddedRuntimeSource) SyncRequest {
	t.Helper()
	select {
	case request := <-source.requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for embedded sync request")
		return SyncRequest{}
	}
}

func assertEmbeddedRevisionNotPublished(t *testing.T, sink *embeddedRuntimeSink, revision int64) {
	t.Helper()
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case state := <-sink.states:
			if state.CurrentRevision == revision {
				t.Fatalf("unapproved revision %d was published", revision)
			}
		case <-timer.C:
			return
		}
	}
}

func waitEmbeddedRuntimeRevision(t *testing.T, sink *embeddedRuntimeSink, revision int64) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case state := <-sink.states:
			if state.CurrentRevision == revision {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for runtime revision %d", revision)
		}
	}
}

func waitEmbeddedRuntimeExit(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for embedded runtime exit")
		return nil
	}
}
