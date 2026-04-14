package service

import (
	"testing"
	"time"
)

func TestTaskServiceRegistersSessionAndDispatchesBoundedTask(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	session := newStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:    "agent-a",
		SessionID:  "session-1",
		Session:    session,
		RemoteAddr: "127.0.0.1",
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	task, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}
	if task.ID == "" {
		t.Fatal("expected non-empty task id")
	}

	dispatched := session.WaitForTask(t)
	if dispatched.Type != TaskTypeDiagnoseHTTPRule {
		t.Fatalf("task type = %q, want %q", dispatched.Type, TaskTypeDiagnoseHTTPRule)
	}
	if got, ok := dispatched.Payload["rule_id"].(int); !ok || got != 7 {
		t.Fatalf("payload rule_id = %#v", dispatched.Payload["rule_id"])
	}
}

type stubTaskSession struct {
	agentID string
	tasks   chan TaskEnvelope
}

func newStubTaskSession(agentID string) *stubTaskSession {
	return &stubTaskSession{
		agentID: agentID,
		tasks:   make(chan TaskEnvelope, 1),
	}
}

func (s *stubTaskSession) SendTask(task TaskEnvelope) error {
	s.tasks <- task
	return nil
}

func (s *stubTaskSession) Close() error {
	return nil
}

func (s *stubTaskSession) WaitForTask(t *testing.T) TaskEnvelope {
	t.Helper()

	select {
	case task := <-s.tasks:
		return task
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task dispatch")
		return TaskEnvelope{}
	}
}
