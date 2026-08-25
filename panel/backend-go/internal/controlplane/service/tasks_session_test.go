//go:build !integration

package service

import "testing"

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
