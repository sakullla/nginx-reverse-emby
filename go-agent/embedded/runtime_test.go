package embedded

import (
	"context"
	"testing"
	"time"
)

type runtimeTestSource struct {
	snapshot Snapshot
	requests chan SyncRequest
}

func newRuntimeTestSource(snapshot Snapshot) *runtimeTestSource {
	return &runtimeTestSource{
		snapshot: snapshot,
		requests: make(chan SyncRequest, 8),
	}
}

func (s *runtimeTestSource) Sync(_ context.Context, request SyncRequest) (Snapshot, error) {
	s.requests <- request
	return s.snapshot, nil
}

type runtimeTestSink struct {
	states chan RuntimeState
}

func newRuntimeTestSink() *runtimeTestSink {
	return &runtimeTestSink{states: make(chan RuntimeState, 8)}
}

func (s *runtimeTestSink) Save(_ context.Context, state RuntimeState) error {
	s.states <- state
	return nil
}

func TestRunIgnoresSelfUpdateStateInEmbeddedMode(t *testing.T) {
	source := newRuntimeTestSource(Snapshot{
		DesiredVersion: "2.0.0",
		Revision:       5,
		VersionPackage: &VersionPackage{
			URL:    "https://example.invalid/nre-agent",
			SHA256: "deadbeef",
		},
	})
	sink := newRuntimeTestSink()

	runtime, err := New(Config{
		AgentID:           "local",
		AgentName:         "local",
		DataDir:           t.TempDir(),
		CurrentVersion:    "1.0.0",
		HeartbeatInterval: 5 * time.Millisecond,
	}, source, sink)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Run(ctx)
	}()

	waitForSyncRequest(t, source, 2*time.Second)
	cancel()

	if err := waitForRuntimeExit(t, errCh, 2*time.Second); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunPersistsAppliedRevisionAcrossRuntimeRecreation(t *testing.T) {
	dataDir := t.TempDir()

	firstSource := newRuntimeTestSource(Snapshot{Revision: 7})
	firstSink := newRuntimeTestSink()
	firstRuntime, err := New(Config{
		AgentID:           "local",
		AgentName:         "local",
		DataDir:           dataDir,
		HeartbeatInterval: 5 * time.Millisecond,
	}, firstSource, firstSink)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}

	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstErrCh := make(chan error, 1)
	go func() {
		firstErrCh <- firstRuntime.Run(firstCtx)
	}()

	waitForRuntimeState(t, firstSink, 2*time.Second)
	firstCancel()

	if err := waitForRuntimeExit(t, firstErrCh, 2*time.Second); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}

	secondSource := newRuntimeTestSource(Snapshot{Revision: 7})
	secondSink := newRuntimeTestSink()
	secondRuntime, err := New(Config{
		AgentID:           "local",
		AgentName:         "local",
		DataDir:           dataDir,
		HeartbeatInterval: 5 * time.Millisecond,
	}, secondSource, secondSink)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}

	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondErrCh := make(chan error, 1)
	go func() {
		secondErrCh <- secondRuntime.Run(secondCtx)
	}()

	request := waitForSyncRequest(t, secondSource, 2*time.Second)
	secondCancel()

	if err := waitForRuntimeExit(t, secondErrCh, 2*time.Second); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if request.CurrentRevision != 7 {
		t.Fatalf("second sync CurrentRevision = %d, want 7", request.CurrentRevision)
	}
}

func waitForSyncRequest(t *testing.T, source *runtimeTestSource, timeout time.Duration) SyncRequest {
	t.Helper()
	select {
	case request := <-source.requests:
		return request
	case <-time.After(timeout):
		t.Fatal("timed out waiting for sync request")
		return SyncRequest{}
	}
}

func waitForRuntimeState(t *testing.T, sink *runtimeTestSink, timeout time.Duration) RuntimeState {
	t.Helper()
	select {
	case state := <-sink.states:
		return state
	case <-time.After(timeout):
		t.Fatal("timed out waiting for runtime state")
		return RuntimeState{}
	}
}

func waitForRuntimeExit(t *testing.T, errCh <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(timeout):
		t.Fatal("timed out waiting for runtime exit")
		return nil
	}
}
