package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskServiceRegistersSessionAndDispatchesBoundedTask(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestTaskServiceAcceptsImmediateTaskUpdateDuringDispatch(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestTaskServiceCloseStopsPruneGoroutine(t *testing.T) {
	t.Parallel()
	service := NewTaskService(TaskServiceConfig{
		TaskTTL:       30 * time.Second,
		Retention:     time.Minute,
		PruneInterval: time.Minute,
	})

	done := make(chan struct{})
	go func() {
		_ = service.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() did not return within timeout; prune goroutine likely leaked")
	}

	// Idempotent: a second Close must not block or panic.
	if err := service.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestPKIRevokeClosesExistingTaskSession(t *testing.T) {
	service := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = service.Close() })
	session := newClosableStubTaskSession("agent-revoked")
	if err := service.RegisterSession(TaskSessionRegistration{AgentID: "agent-revoked", SessionID: "session-1", Session: session}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}

	if err := service.CloseAgentSessions("agent-revoked"); err != nil {
		t.Fatalf("CloseAgentSessions() error = %v", err)
	}
	if !session.closed {
		t.Fatal("revoked agent task session was not closed")
	}
	if _, err := service.CreateAndDispatch(TaskCreateRequest{AgentID: "agent-revoked", Type: TaskTypeDiagnoseHTTPRule}); !errors.Is(err, errTaskSessionUnavailable) {
		t.Fatalf("CreateAndDispatch() error = %v, want unavailable after revoke", err)
	}
	if err := service.CloseAgentSessions("agent-revoked"); err != nil {
		t.Fatalf("second CloseAgentSessions() error = %v", err)
	}
	reconnect := newClosableStubTaskSession("agent-revoked")
	if err := service.RegisterSession(TaskSessionRegistration{AgentID: "agent-revoked", SessionID: "session-after-revoke", Session: reconnect}); !errors.Is(err, errTaskSessionUnavailable) {
		t.Fatalf("RegisterSession(after revoke) error = %v", err)
	}
	if !reconnect.closed {
		t.Fatal("rejected post-revoke session was not closed")
	}
}

func TestPKISecuritySnapshotPublishWaitsForBoundedTerminalAcknowledgement(t *testing.T) {
	t.Run("completed acknowledgement", func(t *testing.T) {
		service := NewTaskService(TaskServiceConfig{})
		t.Cleanup(func() { _ = service.Close() })
		if err := service.RegisterSession(TaskSessionRegistration{
			AgentID: "agent-success", SessionID: "success",
			Session: &immediateUpdateTaskSession{service: service, agentID: "agent-success"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.PublishPKISecuritySnapshot(t.Context(), map[string]any{"security_revision": 3}, ""); err != nil {
			t.Fatalf("PublishPKISecuritySnapshot(completed) error = %v", err)
		}
	})

	t.Run("failed acknowledgement", func(t *testing.T) {
		service := NewTaskService(TaskServiceConfig{})
		t.Cleanup(func() { _ = service.Close() })
		if err := service.RegisterSession(TaskSessionRegistration{
			AgentID: "agent-failed", SessionID: "failed",
			Session: &failedUpdateTaskSession{service: service, agentID: "agent-failed"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.PublishPKISecuritySnapshot(t.Context(), map[string]any{"security_revision": 3}, ""); err == nil || !strings.Contains(err.Error(), "agent rejected security snapshot") {
			t.Fatalf("PublishPKISecuritySnapshot(failed) error = %v", err)
		}
	})

	t.Run("missing acknowledgement", func(t *testing.T) {
		service := NewTaskService(TaskServiceConfig{})
		t.Cleanup(func() { _ = service.Close() })
		if err := service.RegisterSession(TaskSessionRegistration{AgentID: "agent-no-ack", SessionID: "no-ack", Session: newStubTaskSession("agent-no-ack")}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if err := service.PublishPKISecuritySnapshot(ctx, map[string]any{"security_revision": 3}, ""); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("PublishPKISecuritySnapshot(no ack) error = %v", err)
		}
	})

	t.Run("blocked transport", func(t *testing.T) {
		service := NewTaskService(TaskServiceConfig{})
		t.Cleanup(func() { _ = service.Close() })
		blocked := &blockingContextTaskSession{}
		if err := service.RegisterSession(TaskSessionRegistration{AgentID: "agent-blocked", SessionID: "blocked", Session: blocked}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		started := time.Now()
		if err := service.PublishPKISecuritySnapshot(ctx, map[string]any{"security_revision": 3}, ""); err == nil {
			t.Fatal("PublishPKISecuritySnapshot(blocked) error = nil")
		}
		if time.Since(started) > time.Second || !blocked.isClosed() {
			t.Fatalf("blocked publish was not bounded or closed: elapsed=%v closed=%v", time.Since(started), blocked.isClosed())
		}
	})
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

type failedUpdateTaskSession struct {
	service *TaskService
	agentID string
}

func (s *failedUpdateTaskSession) SendTask(task TaskEnvelope) error {
	return s.service.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: s.agentID, TaskID: task.ID, State: "failed", Error: "agent rejected security snapshot",
	})
}

func (s *failedUpdateTaskSession) Close() error { return nil }

type blockingContextTaskSession struct {
	mu     sync.Mutex
	closed bool
}

type blockingCloseTaskSession struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (s *blockingCloseTaskSession) SendTask(TaskEnvelope) error { return nil }

func (s *blockingCloseTaskSession) Close() error {
	s.calls.Add(1)
	s.once.Do(func() { close(s.started) })
	<-s.release
	return nil
}

func (s *blockingContextTaskSession) SendTask(TaskEnvelope) error {
	return errors.New("context-aware send was not used")
}

func (s *blockingContextTaskSession) SendTaskContext(ctx context.Context, _ TaskEnvelope) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *blockingContextTaskSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *blockingContextTaskSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
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
