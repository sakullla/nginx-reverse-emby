package localagent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestDiagnosticResultSuccess(t *testing.T) {
	result := diagnosticResult("http", 1, "http://backend", 12.5, true)
	if result["kind"] != "http" {
		t.Fatalf("kind = %v", result["kind"])
	}
	if result["rule_id"] != 1 {
		t.Fatalf("rule_id = %v", result["rule_id"])
	}
	summary := result["summary"].(map[string]any)
	if summary["quality"] != "excellent" {
		t.Fatalf("quality = %v", summary["quality"])
	}
	if summary["avg_latency_ms"] != 12.5 {
		t.Fatalf("avg_latency_ms = %v", summary["avg_latency_ms"])
	}
}

func TestDiagnosticResultFailure(t *testing.T) {
	result := diagnosticResult("l4_tcp", 2, "1.2.3.4:8080", 3.2, false)
	summary := result["summary"].(map[string]any)
	if summary["quality"] != "down" {
		t.Fatalf("quality = %v", summary["quality"])
	}
	if summary["avg_latency_ms"] != 3.2 {
		t.Fatalf("avg_latency_ms = %v, want 3.2", summary["avg_latency_ms"])
	}
}

type stubReporter struct {
	mu      sync.Mutex
	updates []service.TaskUpdateInput
}

func (r *stubReporter) RegisterSession(_ service.TaskSessionRegistration) error { return nil }

func (r *stubReporter) ApplyUpdate(_ context.Context, input service.TaskUpdateInput) error {
	r.mu.Lock()
	r.updates = append(r.updates, input)
	r.mu.Unlock()
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
	httpRule storage.HTTPRuleRow
	httpOk   bool
	httpErr  error
	l4Rules  []storage.L4RuleRow
	l4Err    error
}

func (s *stubStore) GetHTTPRule(_ context.Context, _ string, _ int) (storage.HTTPRuleRow, bool, error) {
	return s.httpRule, s.httpOk, s.httpErr
}

func (s *stubStore) ListL4Rules(_ context.Context, _ string) ([]storage.L4RuleRow, error) {
	return s.l4Rules, s.l4Err
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

func TestLocalTaskSessionDiagnoseHTTPRuleSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		httpRule: storage.HTTPRuleRow{
			ID:          10,
			AgentID:     "agent-1",
			FrontendURL: "http://example.com/media",
			BackendURL:  backend.URL,
			Enabled:     true,
		},
		httpOk: true,
	})

	if err := sess.SendTask(service.TaskEnvelope{
		ID:      "task-1",
		Type:    service.TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 10},
	}); err != nil {
		t.Fatalf("SendTask() = %v", err)
	}
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	if update.Result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestLocalTaskSessionDiagnoseHTTPRuleDisabled(t *testing.T) {
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		httpRule: storage.HTTPRuleRow{ID: 5, Enabled: false},
		httpOk:  true,
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

func TestLocalTaskSessionDiagnoseL4TCPRuleSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		l4Rules: []storage.L4RuleRow{
			{
				ID:         20,
				Protocol:   "tcp",
				ListenHost: ln.Addr().(*net.TCPAddr).IP.String(),
				ListenPort: ln.Addr().(*net.TCPAddr).Port,
				Enabled:    true,
			},
		},
	})

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
}

func TestLocalTaskSessionUnsupportedTaskType(t *testing.T) {
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{})

	sess.SendTask(service.TaskEnvelope{
		ID:   "task-4",
		Type: "unknown_type",
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "failed" {
		t.Fatalf("state = %q, want failed", update.State)
	}
}
