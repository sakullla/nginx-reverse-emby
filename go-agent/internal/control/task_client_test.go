//go:build !integration

package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"

	"io"
	"net/http"
	"net/http/httptest"

	"testing"
	"time"
)

func writeUnavailableTaskStream(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
		t.Fatalf("EnableFullDuplex() error = %v", err)
	}
	scanner := bufio.NewScanner(r.Body)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("task stream request body scanner error = %v", err)
		}
		t.Fatal("expected task stream hello before unavailable response")
	}
	http.NotFound(w, r)
}

func TestTaskClientFallsBackToSSEOnlyWhenStreamUnavailable(t *testing.T) {
	type taskUpdate struct {
		TaskID string         `json:"task_id"`
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	type requestRecord struct {
		Method string
		Path   string
	}

	requests := make(chan requestRecord, 2)
	updates := make(chan taskUpdate, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- requestRecord{Method: r.Method, Path: r.URL.Path}
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/api/agents/task-stream":
			http.Error(w, "not ready", http.StatusNotFound)
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

	client := NewTaskClient(TaskClientConfig{
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

	gotRequests := make([]requestRecord, 0, 2)
	for len(gotRequests) < 2 {
		select {
		case req := <-requests:
			gotRequests = append(gotRequests, req)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for request order")
		}
	}
	if gotRequests[0].Path != "/api/agents/task-stream" {
		t.Fatalf("first request path = %q, want /api/agents/task-stream", gotRequests[0].Path)
	}
	if gotRequests[0].Method != http.MethodHead {
		t.Fatalf("first request method = %q, want HEAD", gotRequests[0].Method)
	}
	if gotRequests[1].Path != "/api/agents/task-session" {
		t.Fatalf("second request path = %q, want /api/agents/task-session", gotRequests[1].Path)
	}

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

	if got[0].TaskID != "task-1" || got[0].State != "running" {
		t.Fatalf("first update = %#v, want task-1 running", got[0])
	}
	if got[1].TaskID != "task-1" || got[1].State != "completed" {
		t.Fatalf("second update = %#v, want task-1 completed", got[1])
	}
	summary, ok := got[1].Result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", got[1].Result["summary"])
	}
	if avg, ok := summary["avg_latency_ms"].(float64); !ok || avg != 11 {
		t.Fatalf("avg_latency_ms = %#v", summary["avg_latency_ms"])
	}
}

func TestTaskClientDoesNotFallbackToSSEOnStream500(t *testing.T) {
	sessionRequested := make(chan struct{}, 1)
	streamRequests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/api/agents/task-stream":
			select {
			case streamRequests <- struct{}{}:
			default:
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/task-session":
			select {
			case sessionRequested <- struct{}{}:
			default:
			}
			http.Error(w, "unexpected session request", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewTaskClient(TaskClientConfig{
		MasterURL:     server.URL,
		AgentToken:    "token",
		AgentID:       "edge-a",
		ReconnectWait: 20 * time.Millisecond,
		HTTPClient:    server.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case <-streamRequests:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream request")
	}

	select {
	case <-sessionRequested:
		t.Fatal("unexpected task-session request after stream 500")
	case <-time.After(150 * time.Millisecond):
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

func TestWriteTaskStreamPayloadStopsBlockedWriteAtContextDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	err := writeTaskStreamPayload(ctx, writer, []byte("blocked"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writeTaskStreamPayload() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("blocked stream write returned after %s", elapsed)
	}
}

func TestTaskClientHTTP2StreamStopsOnCancellation(t *testing.T) {
	streamOpened := make(chan struct{}, 1)
	releaseServer := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseServer)
		}
	}()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodHead:
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(http.StatusOK)
		case http.MethodPost:
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			scanner := bufio.NewScanner(request.Body)
			if scanner.Scan() {
				streamOpened <- struct{}{}
			}
			select {
			case <-request.Context().Done():
			case <-releaseServer:
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := NewTaskClient(TaskClientConfig{
		MasterURL:  server.URL,
		AgentToken: "token",
		AgentID:    "edge-a",
		HTTPClient: server.Client(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	select {
	case <-streamOpened:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("HTTP/2 task stream did not open")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		close(releaseServer)
		released = true
		server.CloseClientConnections()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("HTTP/2 task stream did not stop after cancellation")
	}
}

func TestTaskClientReconnectsWhenTaskStreamStopsAcknowledgingPings(t *testing.T) {
	streamOpened := make(chan struct{}, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodHead && request.URL.Path == "/api/agents/task-stream":
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(http.StatusOK)
		case request.Method == http.MethodPost && request.URL.Path == "/api/agents/task-stream":
			if _, err := bufio.NewReader(request.Body).ReadString('\n'); err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/x-ndjson")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			streamOpened <- struct{}{}
			_, _ = io.Copy(io.Discard, request.Body)
		default:
			http.NotFound(writer, request)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := NewTaskClient(TaskClientConfig{
		MasterURL: server.URL, AgentToken: "token", AgentID: "edge-a", ReconnectWait: 5 * time.Millisecond,
		TaskStreamPingInterval: 10 * time.Millisecond, TaskStreamLivenessTimeout: 50 * time.Millisecond, HTTPClient: server.Client(),
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("TaskClient.Run() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("task client did not stop")
		}
	}()

	for opened := 0; opened < 2; opened++ {
		select {
		case <-streamOpened:
		case <-time.After(time.Second):
			t.Fatalf("task stream opened %d times, want reconnect", opened)
		}
	}
}

func TestHTTPTransportConfiguresHTTP2LivenessChecks(t *testing.T) {
	transport := newHTTPTransport(HTTPTransportConfig{})
	defer transport.CloseIdleConnections()
	if transport.HTTP2 == nil {
		t.Fatal("HTTP/2 liveness configuration is missing")
	}
	if transport.HTTP2.SendPingTimeout != defaultTaskStreamPingInterval ||
		transport.HTTP2.PingTimeout != defaultTaskStreamPingAckTimeout {
		t.Fatalf("HTTP/2 liveness = idle %s ping %s", transport.HTTP2.SendPingTimeout, transport.HTTP2.PingTimeout)
	}
}

func TestTaskClientReportsFailedTaskExecution(t *testing.T) {
	updates := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/task-stream":
			writeUnavailableTaskStream(t, w, r)
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

	client := NewTaskClient(TaskClientConfig{
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
