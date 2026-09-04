package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type recordingHeartbeatAgentService struct {
	AgentService
	calls int
}

func (s *recordingHeartbeatAgentService) Heartbeat(context.Context, service.HeartbeatRequest, string) (service.HeartbeatReply, error) {
	s.calls++
	return service.HeartbeatReply{}, nil
}

func TestHeartbeatRejectsOversizedBodyBeforeAuthentication(t *testing.T) {
	agents := &recordingHeartbeatAgentService{}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", strings.NewReader(
		`{"padding":"`+strings.Repeat("x", int(maxAgentHeartbeatBodyBytes))+`"}`,
	))
	response := httptest.NewRecorder()

	Dependencies{AgentService: agents}.handleHeartbeat(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusRequestEntityTooLarge)
	}
	if agents.calls != 0 {
		t.Fatalf("heartbeat service calls = %d, want 0", agents.calls)
	}
}

func TestHeartbeatRejectsTrailingJSONBeforeServiceMutation(t *testing.T) {
	agents := &recordingHeartbeatAgentService{}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", strings.NewReader(
		`{"agent_id":"edge-a"} {"agent_id":"edge-b"}`,
	))
	response := httptest.NewRecorder()

	Dependencies{AgentService: agents}.handleHeartbeat(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusBadRequest)
	}
	if agents.calls != 0 {
		t.Fatalf("heartbeat service calls = %d, want 0", agents.calls)
	}
}

func TestHeartbeatRejectsOversizedTrailingDataBeforeServiceMutation(t *testing.T) {
	agents := &recordingHeartbeatAgentService{}
	request := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", strings.NewReader(
		`{"agent_id":"edge-a"} "`+strings.Repeat("x", int(maxAgentHeartbeatBodyBytes))+`"`,
	))
	response := httptest.NewRecorder()

	Dependencies{AgentService: agents}.handleHeartbeat(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusRequestEntityTooLarge)
	}
	if agents.calls != 0 {
		t.Fatalf("heartbeat service calls = %d, want 0", agents.calls)
	}
}
