package task

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func TestDiagnosticHandlerExecutesHTTPRuleProbeFromAppliedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(model.Snapshot{
		Rules: []model.HTTPRule{{
			ID:          7,
			FrontendURL: "https://edge.example.test/emby",
			BackendURL:  server.URL + "/healthz",
		}},
	}); err != nil {
		t.Fatalf("SaveAppliedSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{
		Attempts:   2,
		Timeout:    time.Second,
		HTTPClient: server.Client(),
	}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-1",
		TaskType:   TaskTypeDiagnoseHTTPRule,
		RawPayload: map[string]any{"rule_id": 7},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", result["summary"])
	}
	if summary["succeeded"] != 2 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestDiagnosticHandlerExecutesTCPL4ProbeFromDesiredSnapshot(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	mem := store.NewInMemory()
	addr := ln.Addr().(*net.TCPAddr)
	if err := mem.SaveDesiredSnapshot(model.Snapshot{
		L4Rules: []model.L4Rule{{
			ID:           9,
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			UpstreamHost: "127.0.0.1",
			UpstreamPort: addr.Port,
		}},
	}); err != nil {
		t.Fatalf("SaveDesiredSnapshot() error = %v", err)
	}

	handler := NewDiagnosticHandler(mem, diagnostics.NewHTTPProber(diagnostics.HTTPProberConfig{}), diagnostics.NewTCPProber(diagnostics.TCPProberConfig{
		Attempts: 1,
		Timeout:  time.Second,
	}))

	result, err := handler.HandleTask(context.Background(), TaskMessage{
		TaskID:     "task-2",
		TaskType:   TaskTypeDiagnoseL4TCPRule,
		RawPayload: map[string]any{"rule_id": 9},
	})
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %#v", result["summary"])
	}
	if summary["succeeded"] != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	<-done
}
