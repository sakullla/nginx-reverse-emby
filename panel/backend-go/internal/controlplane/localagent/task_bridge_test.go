package localagent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func TestBuildDiagnosticsReportAllSuccess(t *testing.T) {
	samples := []probeSample{
		{backend: "http://a", latency: 10, ok: true},
		{backend: "http://b", latency: 20, ok: true},
	}
	report := buildDiagnosticsReport("http", 1, samples)
	if report["kind"] != "http" {
		t.Fatalf("kind = %v", report["kind"])
	}
	summary := report["summary"].(map[string]any)
	if summary["quality"] != "excellent" {
		t.Fatalf("quality = %v", summary["quality"])
	}
	if summary["succeeded"] != 2 {
		t.Fatalf("succeeded = %v", summary["succeeded"])
	}
	if summary["failed"] != 0 {
		t.Fatalf("failed = %v", summary["failed"])
	}
	backends := report["backends"].([]map[string]any)
	if len(backends) != 2 {
		t.Fatalf("backends = %d", len(backends))
	}
}

func TestBuildDiagnosticsReportPartialFailure(t *testing.T) {
	samples := []probeSample{
		{backend: "http://a", latency: 10, ok: true},
		{backend: "http://b", latency: 5, ok: false},
	}
	report := buildDiagnosticsReport("http", 1, samples)
	summary := report["summary"].(map[string]any)
	if summary["quality"] != "degraded" {
		t.Fatalf("quality = %v", summary["quality"])
	}
	if summary["succeeded"] != 1 {
		t.Fatalf("succeeded = %v", summary["succeeded"])
	}
}

func TestBuildDiagnosticsReportAllFailed(t *testing.T) {
	samples := []probeSample{
		{backend: "http://a", latency: 3, ok: false},
	}
	report := buildDiagnosticsReport("l4_tcp", 2, samples)
	summary := report["summary"].(map[string]any)
	if summary["quality"] != "down" {
		t.Fatalf("quality = %v", summary["quality"])
	}
}

func TestParseHTTPBackendsFromRow(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		backendURL string
		want       []string
	}{
		{name: "single BackendURL", json: "", backendURL: "http://upstream:8096", want: []string{"http://upstream:8096"}},
		{name: "BackendsJSON array", json: `[{"url":"http://a:80"},{"url":"http://b:80"}]`, backendURL: "http://fallback", want: []string{"http://a:80", "http://b:80"}},
		{name: "empty both", json: "[]", backendURL: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := storage.HTTPRuleRow{BackendsJSON: tc.json, BackendURL: tc.backendURL}
			got := parseHTTPBackendsFromRow(row)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseL4UpstreamsFromRow(t *testing.T) {
	tests := []struct {
		name         string
		json         string
		upstreamHost string
		upstreamPort int
		want         []string
	}{
		{name: "UpstreamHost fallback", json: "", upstreamHost: "10.0.0.1", upstreamPort: 3306, want: []string{"10.0.0.1:3306"}},
		{name: "BackendsJSON", json: `[{"host":"a","port":80},{"host":"b","port":443}]`, upstreamHost: "fallback", upstreamPort: 9999, want: []string{"a:80", "b:443"}},
		{name: "empty both", json: "[]", upstreamHost: "", upstreamPort: 0, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := storage.L4RuleRow{BackendsJSON: tc.json, UpstreamHost: tc.upstreamHost, UpstreamPort: tc.upstreamPort}
			got := parseL4UpstreamsFromRow(row)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- Stub infrastructure ---

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

// --- Session lifecycle tests ---

func TestLocalTaskSessionRejectsAfterClose(t *testing.T) {
	sess := NewLocalTaskSession("agent-1", &stubReporter{}, &stubStore{})
	if err := sess.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := sess.SendTask(service.TaskEnvelope{ID: "1", Type: service.TaskTypeDiagnoseHTTPRule}); err == nil {
		t.Fatal("expected error after close")
	}
}

// --- HTTP diagnostic tests ---

func TestLocalTaskSessionDiagnoseHTTPRuleProbesBackendURL(t *testing.T) {
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
	summary := update.Result["summary"].(map[string]any)
	if summary["quality"] != "excellent" {
		t.Fatalf("quality = %v", summary["quality"])
	}
}

func TestLocalTaskSessionDiagnoseHTTPRuleProbesAllBackends(t *testing.T) {
	var gotUserAgent string
	var gotHeader string
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer backendA.Close()

	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backendB.Close()

	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		httpRule: storage.HTTPRuleRow{
			ID:               11,
			AgentID:          "agent-1",
			FrontendURL:      "http://example.com/multi",
			BackendsJSON:     ` [{"url":"` + backendA.URL + `"},{"url":"` + backendB.URL + `"}] `,
			UserAgent:        "diagnostic-probe",
			CustomHeadersJSON: `[{"name":"X-Custom","value":"test-val"}]`,
			Enabled:          true,
		},
		httpOk: true,
	})

	sess.SendTask(service.TaskEnvelope{
		ID:      "task-multi",
		Type:    service.TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{"rule_id": 11},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	if gotUserAgent != "diagnostic-probe" {
		t.Fatalf("User-Agent = %q, want diagnostic-probe", gotUserAgent)
	}
	if gotHeader != "test-val" {
		t.Fatalf("X-Custom = %q, want test-val", gotHeader)
	}
	backends := update.Result["backends"].([]map[string]any)
	if len(backends) != 2 {
		t.Fatalf("backends = %d, want 2", len(backends))
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

// --- L4/TCP diagnostic tests ---

func TestLocalTaskSessionDiagnoseL4TCPRuleProbesUpstream(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer upstream.Close()

	upstreamPort := upstream.Addr().(*net.TCPAddr).Port

	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		l4Rules: []storage.L4RuleRow{
			{
				ID:           20,
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   25565,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: upstreamPort,
				Enabled:      true,
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
	summary := update.Result["summary"].(map[string]any)
	if summary["quality"] != "excellent" {
		t.Fatalf("quality = %v", summary["quality"])
	}
	backends := update.Result["backends"].([]map[string]any)
	if len(backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(backends))
	}
	if !strings.Contains(backends[0]["backend"].(string), strconv.Itoa(upstreamPort)) {
		t.Fatalf("backend = %v, expected upstream port %d", backends[0]["backend"], upstreamPort)
	}
}

func TestLocalTaskSessionDiagnoseL4TCPRuleProbesUpstreamNotListener(t *testing.T) {
	// Upstream is closed → should report failure even though the listener
	// address (which is different) might be reachable.
	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		l4Rules: []storage.L4RuleRow{
			{
				ID:           21,
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   25565,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 1, // port 1 is not listening
				Enabled:      true,
			},
		},
	})

	sess.SendTask(service.TaskEnvelope{
		ID:      "task-upstream-down",
		Type:    service.TaskTypeDiagnoseL4TCPRule,
		Payload: map[string]any{"rule_id": 21},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	summary := update.Result["summary"].(map[string]any)
	if summary["quality"] == "excellent" {
		t.Fatal("expected non-excellent quality since upstream port 1 is not listening")
	}
}

func TestLocalTaskSessionDiagnoseL4TCPRuleProbesBackendsJSON(t *testing.T) {
	upstreamA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen A: %v", err)
	}
	defer upstreamA.Close()

	upstreamB, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen B: %v", err)
	}
	defer upstreamB.Close()

	portA := upstreamA.Addr().(*net.TCPAddr).Port
	portB := upstreamB.Addr().(*net.TCPAddr).Port

	reporter := &stubReporter{}
	sess := NewLocalTaskSession("agent-1", reporter, &stubStore{
		l4Rules: []storage.L4RuleRow{
			{
				ID:           22,
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   25565,
				UpstreamHost: "should-not-be-used",
				UpstreamPort: 9999,
				BackendsJSON: fmt.Sprintf(
					`[{"host":"127.0.0.1","port":%d},{"host":"127.0.0.1","port":%d}]`,
					portA, portB,
				),
				Enabled: true,
			},
		},
	})

	sess.SendTask(service.TaskEnvelope{
		ID:      "task-multi-l4",
		Type:    service.TaskTypeDiagnoseL4TCPRule,
		Payload: map[string]any{"rule_id": 22},
	})
	sess.Close()

	update := reporter.lastUpdate()
	if update.State != "completed" {
		t.Fatalf("state = %q, want completed; error = %q", update.State, update.Error)
	}
	backends := update.Result["backends"].([]map[string]any)
	if len(backends) != 2 {
		t.Fatalf("backends = %d, want 2", len(backends))
	}
}

// --- Other tests ---

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
