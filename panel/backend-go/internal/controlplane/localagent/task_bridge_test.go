package localagent

import (
	"context"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestTaskRuleID(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    int
		wantErr bool
	}{
		{name: "int value", payload: map[string]any{"rule_id": 42}, want: 42},
		{name: "float64 value", payload: map[string]any{"rule_id": float64(7)}, want: 7},
		{name: "string value", payload: map[string]any{"rule_id": "13"}, want: 13},
		{name: "missing key", payload: map[string]any{}, wantErr: true},
		{name: "invalid type", payload: map[string]any{"rule_id": true}, wantErr: true},
		{name: "invalid string", payload: map[string]any{"rule_id": "abc"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := taskRuleID(tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

type stubReporter struct {
	mu      sync.Mutex
	updates []service.TaskUpdateInput
}

func (r *stubReporter) RegisterSession(_ service.TaskSessionRegistration) error { return nil }

func (r *stubReporter) ApplyUpdate(_ context.Context, input service.TaskUpdateInput) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, input)
	return nil
}

func (r *stubReporter) lastUpdate() service.TaskUpdateInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.updates) == 0 {
		return service.TaskUpdateInput{}
	}
	return r.updates[len(r.updates)-1]
}

type stubStore struct {
	httpRule  storage.HTTPRuleRow
	httpOk    bool
	httpErr   error
	l4Rules   []storage.L4RuleRow
	l4Err     error
	snapshot  storage.Snapshot
	snapErr   error
	snapAgent string
}

func (s *stubStore) GetHTTPRule(_ context.Context, _ string, _ int) (storage.HTTPRuleRow, bool, error) {
	return s.httpRule, s.httpOk, s.httpErr
}

func (s *stubStore) ListL4Rules(_ context.Context, _ string) ([]storage.L4RuleRow, error) {
	return s.l4Rules, s.l4Err
}

func (s *stubStore) LoadLocalSnapshot(_ context.Context, agentID string) (storage.Snapshot, error) {
	s.snapAgent = agentID
	return s.snapshot, s.snapErr
}

func TestLocalTaskSessionRejectsAfterClose(t *testing.T) {
	sess := NewLocalTaskSession("agent-1", &stubReporter{}, &stubStore{})
	if err := sess.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := sess.SendTask(service.TaskEnvelope{ID: "1", Type: service.TaskTypeDiagnoseHTTPRule}); err == nil {
		t.Fatal("expected error after close")
	}
}

func TestLocalTaskSessionDiagnoseHTTPRuleUsesEmbeddedDiagnosticsPayload(t *testing.T) {
	reporter := &stubReporter{}
	store := &stubStore{
		httpRule: storage.HTTPRuleRow{
			ID:      10,
			AgentID: "agent-1",
			Enabled: true,
		},
		httpOk: true,
		snapshot: storage.Snapshot{
			Rules: []storage.HTTPRule{{
				ID:          10,
				AgentID:     "agent-1",
				FrontendURL: "https://media.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Backends:    []storage.HTTPBackend{{URL: "http://127.0.0.1:8096"}},
			}},
		},
	}

	previousRunner := runEmbeddedDiagnostics
	t.Cleanup(func() { runEmbeddedDiagnostics = previousRunner })
	runEmbeddedDiagnostics = func(_ context.Context, dataDir string, snapshot storage.Snapshot, envelope service.TaskEnvelope) (map[string]any, error) {
		if dataDir != "" {
			t.Fatalf("dataDir = %q, want empty default in unit test", dataDir)
		}
		if len(snapshot.Rules) != 1 || snapshot.Rules[0].ID != 10 {
			t.Fatalf("snapshot = %+v", snapshot)
		}
		if envelope.Type != service.TaskTypeDiagnoseHTTPRule {
			t.Fatalf("task type = %q", envelope.Type)
		}
		return map[string]any{
			"kind":    "http",
			"rule_id": 10,
			"summary": map[string]any{"sent": 1, "succeeded": 1, "failed": 0, "quality": "极佳"},
			"backends": []map[string]any{{
				"backend": "http://127.0.0.1:8096",
				"summary": map[string]any{"sent": 1, "succeeded": 1, "failed": 0, "quality": "极佳"},
			}},
			"samples": []map[string]any{{
				"attempt":     1,
				"backend":     "http://127.0.0.1:8096",
				"success":     true,
				"latency_ms":  12.3,
				"status_code": 200,
			}},
		}, nil
	}

	sess := NewLocalTaskSession("agent-1", reporter, store)
	sess.SendTask(service.TaskEnvelope{
		ID:      "task-1",
		Type:    service.TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 10},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	samples, ok := update.Result["samples"].([]map[string]any)
	if !ok {
		t.Fatalf("samples type = %T, want []map[string]any", update.Result["samples"])
	}
	if len(samples) != 1 || samples[0]["attempt"] != 1 {
		t.Fatalf("samples = %+v", samples)
	}
	if store.snapAgent != "agent-1" {
		t.Fatalf("LoadLocalSnapshot() agent = %q", store.snapAgent)
	}
}

func TestLocalTaskSessionDiagnoseHTTPRuleDisabled(t *testing.T) {
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		httpRule: storage.HTTPRuleRow{ID: 5, Enabled: false},
		httpOk:   true,
	})

	sess.SendTask(service.TaskEnvelope{
		ID:      "task-2",
		Type:    service.TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 5},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "failed" {
		t.Fatalf("state = %q, want failed", update.State)
	}
}

func TestLocalTaskSessionDiagnoseL4RuleUsesEmbeddedDiagnostics(t *testing.T) {
	reporter := &stubReporter{}
	store := &stubStore{
		l4Rules: []storage.L4RuleRow{{
			ID:      20,
			AgentID: "agent-1",
			Enabled: true,
		}},
		snapshot: storage.Snapshot{
			L4Rules: []storage.L4Rule{{
				ID:           20,
				AgentID:      "agent-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   25565,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 25565,
				Backends:     []storage.L4Backend{{Host: "127.0.0.1", Port: 25565}},
			}},
		},
	}

	previousRunner := runEmbeddedDiagnostics
	t.Cleanup(func() { runEmbeddedDiagnostics = previousRunner })
	runEmbeddedDiagnostics = func(_ context.Context, _ string, snapshot storage.Snapshot, envelope service.TaskEnvelope) (map[string]any, error) {
		if envelope.Type != service.TaskTypeDiagnoseL4TCPRule {
			t.Fatalf("task type = %q", envelope.Type)
		}
		if len(snapshot.L4Rules) != 1 || snapshot.L4Rules[0].ID != 20 {
			t.Fatalf("snapshot = %+v", snapshot)
		}
		return map[string]any{
			"kind":    "l4_tcp",
			"rule_id": 20,
			"summary": map[string]any{"sent": 1, "succeeded": 1, "failed": 0, "quality": "极佳"},
			"samples": []map[string]any{{"attempt": 1, "backend": "127.0.0.1:25565", "success": true}},
		}, nil
	}

	sess := NewLocalTaskSession("agent-1", reporter, store)
	sess.SendTask(service.TaskEnvelope{
		ID:      "task-3",
		Type:    service.TaskTypeDiagnoseL4TCPRule,
		Payload: map[string]any{"rule_id": 20},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	samples, ok := update.Result["samples"].([]map[string]any)
	if !ok || len(samples) != 1 {
		t.Fatalf("samples = %#v", update.Result["samples"])
	}
}

func TestLocalTaskSessionDiagnoseL4RuleDisabled(t *testing.T) {
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		l4Rules: []storage.L4RuleRow{{ID: 21, Enabled: false}},
	})

	sess.SendTask(service.TaskEnvelope{
		ID:      "task-4",
		Type:    service.TaskTypeDiagnoseL4TCPRule,
		Payload: map[string]any{"rule_id": 21},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "failed" {
		t.Fatalf("state = %q, want failed", update.State)
	}
}

func TestLocalTaskSessionUnsupportedTaskType(t *testing.T) {
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{})

	sess.SendTask(service.TaskEnvelope{
		ID:   "task-5",
		Type: "unknown_type",
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "failed" {
		t.Fatalf("state = %q, want failed", update.State)
	}
}
