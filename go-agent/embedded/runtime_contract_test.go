//go:build !integration

package embedded

import (
	"context"
	"sync"
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

	const callers = 4
	start := make(chan struct{})
	errs := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			errs <- second.Close()
		}()
	}
	ready.Wait()
	close(start)
	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
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
