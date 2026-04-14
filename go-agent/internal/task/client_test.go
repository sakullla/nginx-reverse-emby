package task

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
