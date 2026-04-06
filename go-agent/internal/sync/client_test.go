package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHeartbeatSync(t *testing.T) {
	type captured struct {
		Method string
		Path   string
		Header http.Header
		Body   []byte
	}
	reqs := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqs <- captured{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Sync struct {
				DesiredVersion string `json:"desired_version"`
			} `json:"sync"`
		}{Sync: struct {
			DesiredVersion string `json:"desired_version"`
		}{DesiredVersion: "1.2.3"}})
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		MasterURL:      server.URL,
		AgentToken:     "token",
		AgentID:        "node",
		AgentName:      "local",
		CurrentVersion: "0.1.0",
		Platform:       "linux-amd64",
	}, server.Client())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	snap, err := client.Sync(ctx, SyncRequest{CurrentRevision: 42})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if snap.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired_version=1.2.3, got %q", snap.DesiredVersion)
	}

	select {
	case req := <-reqs:
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.Path != "/api/agents/heartbeat" {
			t.Fatalf("expected heartbeat path, got %s", req.Path)
		}
		if req.Header.Get("x-agent-token") != "token" {
			t.Fatal("missing x-agent-token header")
		}
		if !bytes.Contains(req.Body, []byte(`"current_revision"`)) {
			t.Fatalf("expected current_revision in heartbeat payload")
		}
		var payload struct {
			Name            string `json:"name"`
			CurrentRevision int    `json:"current_revision"`
			Version         string `json:"version"`
			Platform        string `json:"platform"`
		}
		if err := json.NewDecoder(bytes.NewReader(req.Body)).Decode(&payload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}
		if payload.Name != "local" || payload.Version != "0.1.0" || payload.Platform != "linux-amd64" {
			t.Fatalf("unexpected payload %+v", payload)
		}
		if payload.CurrentRevision != 42 {
			t.Fatalf("expected current_revision=42, got %d", payload.CurrentRevision)
		}
	case <-ctx.Done():
		t.Fatalf("heartbeat not sent")
	}
}
