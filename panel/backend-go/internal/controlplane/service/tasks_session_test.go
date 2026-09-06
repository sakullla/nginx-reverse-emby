//go:build !integration

package service

import (
	"context"
	"errors"
	"testing"
)

type sessionUnregisterProbe struct {
	closed bool
}

func (*sessionUnregisterProbe) SendTask(TaskEnvelope) error { return nil }

func (session *sessionUnregisterProbe) Close() error {
	session.closed = true
	return nil
}

func TestTaskSessionUnregisterDoesNotRemoveReplacement(t *testing.T) {
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })

	oldSession := &sessionUnregisterProbe{}
	newSession := &sessionUnregisterProbe{}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: oldSession}); err != nil {
		t.Fatal(err)
	}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: newSession}); err != nil {
		t.Fatal(err)
	}
	if !oldSession.closed {
		t.Fatal("replacement did not close the superseded session")
	}

	tasks.UnregisterSession("edge-a", oldSession)
	if !tasks.HasSession("edge-a") {
		t.Fatal("superseded handler removed the replacement session")
	}

	tasks.UnregisterSession("edge-a", newSession)
	if tasks.HasSession("edge-a") {
		t.Fatal("current session remained registered after handler exit")
	}
}

type canceledDispatchSession struct {
	closed  bool
	started chan struct{}
}

func (*canceledDispatchSession) SendTask(TaskEnvelope) error { return nil }

func (session *canceledDispatchSession) SendTaskContext(ctx context.Context, _ TaskEnvelope) error {
	close(session.started)
	<-ctx.Done()
	return ctx.Err()
}

func (session *canceledDispatchSession) Close() error {
	session.closed = true
	return nil
}

func TestCanceledTaskDispatchPreservesHealthySession(t *testing.T) {
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })

	session := &canceledDispatchSession{started: make(chan struct{})}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: session}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatchErr := make(chan error, 1)
	go func() {
		_, err := tasks.CreateAndDispatchContext(ctx, TaskCreateRequest{
			AgentID: "edge-a",
			Type:    TaskTypeChannelStatus,
		})
		dispatchErr <- err
	}()
	<-session.started
	cancel()
	err := <-dispatchErr
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatch error = %v, want context.Canceled", err)
	}
	if session.closed {
		t.Fatal("caller cancellation closed the shared agent session")
	}
	if !tasks.HasSession("edge-a") {
		t.Fatal("caller cancellation removed the shared agent session")
	}
	tasks.mu.RLock()
	defer tasks.mu.RUnlock()
	if len(tasks.tasks) != 0 {
		t.Fatalf("canceled dispatch left %d task records, want 0", len(tasks.tasks))
	}
}
