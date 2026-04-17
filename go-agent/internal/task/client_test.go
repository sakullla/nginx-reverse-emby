package task

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

func TestTaskClientReconnectsAndSendsHello(t *testing.T) {
	type capturedRequest struct {
		AgentToken string
		AgentID    string
		SessionID  string
	}

	requests := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- capturedRequest{
			AgentToken: r.Header.Get("X-Agent-Token"),
			AgentID:    r.URL.Query().Get("agent_id"),
			SessionID:  r.URL.Query().Get("session_id"),
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(": connected\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		AgentName:     "edge-a",
		Version:       "1.0.0",
		Capabilities:  []string{TaskTypeDiagnoseHTTPRule},
		ReconnectWait: 10 * time.Millisecond,
		HTTPClient:    server.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case req := <-requests:
		if req.AgentToken != "token" {
			t.Fatalf("X-Agent-Token = %q, want token", req.AgentToken)
		}
		if req.AgentID != "edge-a" {
			t.Fatalf("agent_id = %q, want edge-a", req.AgentID)
		}
		if req.SessionID == "" {
			t.Fatal("expected non-empty session_id")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task session request")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client shutdown")
	}
}

func TestClientSendHelloEncodesExpectedMessage(t *testing.T) {
	client := NewClient(ClientConfig{
		AgentID:      "edge-a",
		AgentName:    "edge-a",
		Version:      "1.0.0",
		Capabilities: []string{TaskTypeDiagnoseHTTPRule},
	})

	message := client.helloMessage("session-1")
	if message.Type != "hello" {
		t.Fatalf("Type = %q, want hello", message.Type)
	}
	if message.Hello == nil {
		t.Fatal("expected hello payload")
	}

	data, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty encoded message")
	}
}

func TestTaskClientConsumesTaskEventAndReportsLifecycle(t *testing.T) {
	type taskUpdate struct {
		TaskID string
		State  string
		Result map[string]any
		Error  string
	}

	updates := make(chan taskUpdate, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/task-session":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: task\ndata: {\"task_id\":\"task-1\",\"task_type\":\"diagnose_http_rule\",\"deadline\":\"2026-04-14T00:00:00Z\",\"payload\":{\"rule_id\":7}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-tasks/task-1/updates":
			defer r.Body.Close()
			var payload struct {
				State  string         `json:"state"`
				Result map[string]any `json:"result"`
				Error  string         `json:"error"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			updates <- taskUpdate{
				TaskID: "task-1",
				State:  payload.State,
				Result: payload.Result,
				Error:  payload.Error,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		AgentName:     "edge-a",
		Version:       "1.0.0",
		Capabilities:  []string{TaskTypeDiagnoseHTTPRule},
		ReconnectWait: 10 * time.Millisecond,
		HTTPClient:    server.Client(),
		Handler: TaskHandlerFunc(func(_ context.Context, task TaskMessage) (map[string]any, error) {
			if task.TaskID != "task-1" {
				t.Fatalf("TaskID = %q", task.TaskID)
			}
			if task.TaskType != TaskTypeDiagnoseHTTPRule {
				t.Fatalf("TaskType = %q", task.TaskType)
			}
			return map[string]any{
				"summary": map[string]any{
					"avg_latency_ms": 11,
				},
			}, nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	got := make([]taskUpdate, 0, 2)
	for len(got) < 2 {
		select {
		case update := <-updates:
			got = append(got, update)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for task updates")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client shutdown")
	}

	if got[0].State != "running" {
		t.Fatalf("first state = %q", got[0].State)
	}
	if got[1].State != "completed" {
		t.Fatalf("second state = %q", got[1].State)
	}
	summary, ok := got[1].Result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", got[1].Result["summary"])
	}
	if avg, ok := summary["avg_latency_ms"].(float64); !ok || avg != 11 {
		t.Fatalf("avg_latency_ms = %#v", summary["avg_latency_ms"])
	}
}

func TestTaskClientReportsFailedTaskExecution(t *testing.T) {
	updates := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/task-session":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: task\ndata: {\"task_id\":\"task-2\",\"task_type\":\"diagnose_http_rule\",\"deadline\":\"2026-04-14T00:00:00Z\",\"payload\":{\"rule_id\":9}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-tasks/task-2/updates":
			defer r.Body.Close()
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			updates <- payload
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		ReconnectWait: 10 * time.Millisecond,
		HTTPClient:    server.Client(),
		Handler: TaskHandlerFunc(func(context.Context, TaskMessage) (map[string]any, error) {
			return nil, errors.New("probe failed")
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	got := make([]map[string]any, 0, 2)
	for len(got) < 2 {
		select {
		case payload := <-updates:
			got = append(got, payload)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for task updates")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for client shutdown")
	}

	if got[0]["state"] != "running" {
		t.Fatalf("first update = %#v", got[0])
	}
	if got[1]["state"] != "failed" {
		t.Fatalf("second update = %#v", got[1])
	}
	if got[1]["error"] != "probe failed" {
		t.Fatalf("error = %#v", got[1]["error"])
	}
}

func TestNewClientAppliesConfiguredHTTPTransportTimeouts(t *testing.T) {
	client := NewClient(ClientConfig{
		MasterURL:  "https://master.example.com",
		AgentToken: "token",
		HTTPTransport: config.HTTPTransportConfig{
			DialTimeout:           11 * time.Second,
			TLSHandshakeTimeout:   12 * time.Second,
			ResponseHeaderTimeout: 13 * time.Second,
			IdleConnTimeout:       14 * time.Second,
			KeepAlive:             15 * time.Second,
		},
	})

	if client.transport == nil {
		t.Fatal("expected transport to be initialized")
	}
	if client.transport.TLSHandshakeTimeout != 12*time.Second {
		t.Fatalf("TLSHandshakeTimeout = %v", client.transport.TLSHandshakeTimeout)
	}
	if client.transport.ResponseHeaderTimeout != 13*time.Second {
		t.Fatalf("ResponseHeaderTimeout = %v", client.transport.ResponseHeaderTimeout)
	}
	if client.transport.IdleConnTimeout != 14*time.Second {
		t.Fatalf("IdleConnTimeout = %v", client.transport.IdleConnTimeout)
	}
}
