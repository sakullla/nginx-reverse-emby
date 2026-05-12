package task

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if r.Method != http.MethodGet || r.URL.Path != "/api/agents/task-session" {
			http.NotFound(w, r)
			return
		}
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

func TestTaskClientSupportsMasterURLWithApiPrefix(t *testing.T) {
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/agents/task-session" {
			http.NotFound(w, r)
			return
		}
		requests <- r.URL.Path
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
		MasterURL:     server.URL + "/panel-api",
		AgentToken:    "token",
		AgentID:       "edge-a",
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
	case path := <-requests:
		if path != "/api/agents/task-session" {
			t.Fatalf("path = %q", path)
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/task-stream":
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/task-stream":
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

	client := NewClient(ClientConfig{
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
		TaskID string         `json:"task_id"`
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
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

func TestTaskClientUsesNDJSONTaskStreamForLifecycleUpdates(t *testing.T) {
	type taskUpdate struct {
		TaskID string         `json:"task_id"`
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	type streamMessage struct {
		Type   string      `json:"type"`
		Update *taskUpdate `json:"update,omitempty"`
	}

	firstPath := make(chan string, 1)
	updates := make(chan taskUpdate, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case firstPath <- r.URL.Path:
		default:
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/task-stream" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-ndjson" {
			t.Fatalf("Content-Type = %q, want application/x-ndjson", got)
		}
		if got := r.Header.Get("X-Agent-Token"); got != "token" {
			t.Fatalf("X-Agent-Token = %q, want token", got)
		}
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Fatalf("EnableFullDuplex() error = %v", err)
		}

		bodyDone := make(chan struct{})
		go func() {
			defer close(bodyDone)
			defer r.Body.Close()
			scanner := bufio.NewScanner(r.Body)
			for scanner.Scan() {
				var msg streamMessage
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					t.Errorf("Unmarshal() error = %v", err)
					return
				}
				if msg.Type != "update" || msg.Update == nil {
					continue
				}
				updates <- taskUpdate{
					TaskID: msg.Update.TaskID,
					State:  msg.Update.State,
					Result: msg.Update.Result,
					Error:  msg.Update.Error,
				}
			}
			if err := scanner.Err(); err != nil && r.Context().Err() == nil {
				t.Errorf("body scanner error = %v", err)
			}
		}()

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("{\"type\":\"task\",\"task\":{\"task_id\":\"task-1\",\"task_type\":\"diagnose_http_rule\",\"deadline\":\"2026-05-11T10:00:00Z\",\"payload\":{\"rule_id\":7}}}\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		<-bodyDone
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

	select {
	case path := <-firstPath:
		if path != "/api/agents/task-stream" {
			t.Fatalf("first request path = %q, want /api/agents/task-stream", path)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream request")
	}

	got := make([]taskUpdate, 0, 2)
	for len(got) < 2 {
		select {
		case update := <-updates:
			got = append(got, update)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for stream task updates")
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

func TestTaskClientHandlesLargeNDJSONTaskStreamMessage(t *testing.T) {
	type taskUpdate struct {
		TaskID string         `json:"task_id"`
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	type streamMessage struct {
		Type   string      `json:"type"`
		Update *taskUpdate `json:"update,omitempty"`
	}

	largeBlob := strings.Repeat("x", 70*1024)
	updates := make(chan taskUpdate, 2)
	seenBlob := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/task-stream" {
			http.NotFound(w, r)
			return
		}
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Fatalf("EnableFullDuplex() error = %v", err)
		}

		bodyDone := make(chan struct{})
		go func() {
			defer close(bodyDone)
			defer r.Body.Close()
			scanner := bufio.NewScanner(r.Body)
			for scanner.Scan() {
				var msg streamMessage
				if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
					t.Errorf("Unmarshal() error = %v", err)
					return
				}
				if msg.Type != "update" || msg.Update == nil {
					continue
				}
				updates <- taskUpdate{
					TaskID: msg.Update.TaskID,
					State:  msg.Update.State,
					Result: msg.Update.Result,
					Error:  msg.Update.Error,
				}
			}
			if err := scanner.Err(); err != nil && r.Context().Err() == nil {
				t.Errorf("body scanner error = %v", err)
			}
		}()

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		msg := Message{
			Type: "task",
			Task: &TaskMessage{
				TaskID:   "task-1",
				TaskType: TaskTypeDiagnoseHTTPRule,
				Deadline: "2026-05-11T10:00:00Z",
				RawPayload: map[string]any{
					"blob": largeBlob,
				},
			},
		}
		data, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		_, _ = w.Write(append(data, '\n'))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		<-bodyDone
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
			blob, ok := task.RawPayload["blob"].(string)
			if !ok {
				t.Fatalf("payload blob = %#v", task.RawPayload["blob"])
			}
			seenBlob <- blob
			return map[string]any{"ok": true}, nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case got := <-seenBlob:
		if got != largeBlob {
			t.Fatalf("payload blob length = %d, want %d", len(got), len(largeBlob))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler payload")
	}

	got := make([]taskUpdate, 0, 2)
	for len(got) < 2 {
		select {
		case update := <-updates:
			got = append(got, update)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for stream task updates")
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
}

func TestTaskClientDoesNotSendStreamHelloBeforeOK(t *testing.T) {
	bodyBytes := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/task-stream" {
			http.NotFound(w, r)
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.EnableFullDuplex(); err != nil {
			t.Fatalf("EnableFullDuplex() error = %v", err)
		}
		if err := controller.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatalf("SetReadDeadline() error = %v", err)
		}
		buf := make([]byte, 1)
		n, _ := r.Body.Read(buf)
		bodyBytes <- n
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:  server.URL,
		AgentToken: "token",
		AgentID:    "edge-a",
		HTTPClient: server.Client(),
	})

	err := client.runStreamSession(context.Background())
	if err == nil {
		t.Fatal("runStreamSession() error = nil, want non-200 error")
	}

	select {
	case n := <-bodyBytes:
		if n != 0 {
			t.Fatalf("stream body read %d byte(s) before 200 OK, want none", n)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream body read")
	}
}

func TestTaskClientUsesTaskDeadlineForHandlerContext(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	handlerDeadline := make(chan time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/task-session":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("event: task\ndata: {\"task_id\":\"task-1\",\"task_type\":\"diagnose_http_rule\",\"deadline\":%q,\"payload\":{\"rule_id\":7}}\n\n", deadline)))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-tasks/task-1/updates":
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
		Handler: TaskHandlerFunc(func(ctx context.Context, _ TaskMessage) (map[string]any, error) {
			got, ok := ctx.Deadline()
			if !ok {
				return nil, errors.New("handler context has no deadline")
			}
			handlerDeadline <- got
			return map[string]any{"ok": true}, nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx)
	}()

	select {
	case got := <-handlerDeadline:
		want, err := time.Parse(time.RFC3339, deadline)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		if got.Before(want.Add(-time.Second)) || got.After(want.Add(time.Second)) {
			t.Fatalf("handler deadline = %s, want near %s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler deadline")
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
