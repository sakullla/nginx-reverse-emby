package service

import (
	"context"
	"errors"
	"sync"
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

func TestTaskServiceStoresCompletedDiagnosticResult(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	session := newStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	record, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}

	err = service.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: "agent-a",
		TaskID:  record.ID,
		State:   "completed",
		Result: map[string]any{
			"summary": map[string]any{"avg_latency_ms": 11},
		},
	})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}

	got, err := service.Get(context.Background(), "agent-a", record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("state = %q, want completed", got.State)
	}
	summary, ok := got.Result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", got.Result["summary"])
	}
	if avg, ok := summary["avg_latency_ms"].(int); !ok || avg != 11 {
		t.Fatalf("avg_latency_ms = %#v", summary["avg_latency_ms"])
	}
}

func TestTaskServiceClosedSessionIsNotReusedForDispatch(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	session := newClosableStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() error = %v", err)
	}

	_, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if !errors.Is(err, errTaskSessionUnavailable) {
		t.Fatalf("CreateAndDispatch() error = %v, want %v", err, errTaskSessionUnavailable)
	}
}

func TestTaskServiceDispatchFailureEvictsStaleSession(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	session := newClosableStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() error = %v", err)
	}

	_, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if !errors.Is(err, errTaskSessionUnavailable) {
		t.Fatalf("CreateAndDispatch() error = %v, want %v", err, errTaskSessionUnavailable)
	}

	if _, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 8},
	}); !errors.Is(err, errTaskSessionUnavailable) {
		t.Fatalf("second CreateAndDispatch() error = %v, want %v", err, errTaskSessionUnavailable)
	}
}

func TestTaskServiceAcceptsImmediateTaskUpdateDuringDispatch(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	session := &immediateUpdateTaskSession{service: service, agentID: "agent-a"}
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	record, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}

	got, err := service.Get(context.Background(), "agent-a", record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != "completed" {
		t.Fatalf("state = %q, want completed", got.State)
	}
	if got.Result["ok"] != true {
		t.Fatalf("result = %#v, want ok true", got.Result)
	}
}

func TestTaskServiceMarksExpiredActiveTaskFailedOnGet(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return now
		},
		TaskTTL: 30 * time.Second,
	})
	session := newStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	record, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}

	now = now.Add(31 * time.Second)
	got, err := service.Get(context.Background(), "agent-a", record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Error != "task deadline exceeded" {
		t.Fatalf("error = %q, want deadline error", got.Error)
	}
	if !got.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt = %s, want %s", got.UpdatedAt, now)
	}
}

func TestTaskServiceRejectsLateUpdateAfterDeadline(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return now
		},
		TaskTTL: 30 * time.Second,
	})
	session := newStubTaskSession("agent-a")
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   session,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	record, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}

	now = now.Add(31 * time.Second)
	err = service.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: "agent-a",
		TaskID:  record.ID,
		State:   "completed",
		Result:  map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatalf("ApplyUpdate() error = %v", err)
	}

	got, err := service.Get(context.Background(), "agent-a", record.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.State != "failed" {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if got.Error != "task deadline exceeded" {
		t.Fatalf("error = %q, want deadline error", got.Error)
	}
	if len(got.Result) != 0 {
		t.Fatalf("result = %#v, want no late result", got.Result)
	}
}

func TestTaskServiceRegisterSessionDoesNotCloseExistingSessionWhileLocked(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{
		Now: func() time.Time {
			return time.Unix(1700000000, 0).UTC()
		},
		TaskTTL: 30 * time.Second,
	})
	blocking := &lockCheckingCloseTaskSession{service: service, agentID: "agent-a"}
	if err := service.RegisterSession(TaskSessionRegistration{
		AgentID:   "agent-a",
		SessionID: "session-1",
		Session:   blocking,
	}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}
	record, err := service.CreateAndDispatch(TaskCreateRequest{
		AgentID: "agent-a",
		Type:    TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("CreateAndDispatch() error = %v", err)
	}
	blocking.taskID = record.ID

	done := make(chan error, 1)
	go func() {
		done <- service.RegisterSession(TaskSessionRegistration{
			AgentID:   "agent-a",
			SessionID: "session-2",
			Session:   newStubTaskSession("agent-a"),
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RegisterSession() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RegisterSession() deadlocked while closing previous session")
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

type closableStubTaskSession struct {
	*stubTaskSession
	closed bool
}

type immediateUpdateTaskSession struct {
	service *TaskService
	agentID string
}

func (s *immediateUpdateTaskSession) SendTask(task TaskEnvelope) error {
	return s.service.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: s.agentID,
		TaskID:  task.ID,
		State:   "completed",
		Result:  map[string]any{"ok": true},
	})
}

func (s *immediateUpdateTaskSession) Close() error {
	return nil
}

type lockCheckingCloseTaskSession struct {
	service *TaskService
	agentID string
	taskID  string
	once    sync.Once
}

func (s *lockCheckingCloseTaskSession) SendTask(TaskEnvelope) error {
	return nil
}

func (s *lockCheckingCloseTaskSession) Close() error {
	s.once.Do(func() {
		_ = s.service.ApplyUpdate(context.Background(), TaskUpdateInput{
			AgentID: s.agentID,
			TaskID:  s.taskID,
			State:   "completed",
		})
	})
	return nil
}

func newClosableStubTaskSession(agentID string) *closableStubTaskSession {
	return &closableStubTaskSession{
		stubTaskSession: newStubTaskSession(agentID),
	}
}

func (s *closableStubTaskSession) SendTask(task TaskEnvelope) error {
	if s.closed {
		return errors.New("session closed")
	}
	return s.stubTaskSession.SendTask(task)
}

func (s *closableStubTaskSession) Close() error {
	s.closed = true
	return nil
}
