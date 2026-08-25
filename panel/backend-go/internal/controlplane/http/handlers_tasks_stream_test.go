//go:build !integration

package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestTaskStreamAcceptsHTTP2WithoutHTTP1FullDuplexOptIn(t *testing.T) {
	tasks := service.NewTaskService(service.TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	deps := Dependencies{
		AgentService: fakeOwnerAgentService{authenticated: service.AgentSummary{ID: "edge-a"}},
		TaskService:  tasks,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream?session_id=session-a", strings.NewReader(""))
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	req.Header.Set("X-Agent-Token", "agent-token")
	recorder := httptest.NewRecorder()

	deps.handleAgentTaskStream(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP/2 task stream status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if tasks.HasSession("edge-a") {
		t.Fatal("finished HTTP/2 stream left a stale task session registered")
	}
}

func TestTaskStreamSignalsUnsupportedHTTP1WriterForSSEFallback(t *testing.T) {
	tasks := service.NewTaskService(service.TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	deps := Dependencies{
		AgentService: fakeOwnerAgentService{authenticated: service.AgentSummary{ID: "edge-a"}},
		TaskService:  tasks,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream?session_id=session-a", strings.NewReader(""))
	req.Header.Set("X-Agent-Token", "agent-token")
	recorder := httptest.NewRecorder()

	deps.handleAgentTaskStream(recorder, req)

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported HTTP/1 task stream status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTaskStreamEchoesPingForBidirectionalLiveness(t *testing.T) {
	recorder := httptest.NewRecorder()
	session := newNDJSONTaskSession(recorder, recorder, func() {}, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream", strings.NewReader("{\"type\":\"ping\",\"ping\":{\"sent_at\":\"2026-08-25T00:00:00Z\"}}\n"))

	if err := (Dependencies{}).readTaskStreamUpdates(request, "edge-a", session); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"ping"`) || !strings.Contains(body, `"sent_at":`) {
		t.Fatalf("ping response = %q", body)
	}
}
