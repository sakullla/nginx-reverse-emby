package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeartbeatRejectsOversizedBodyBeforeAuthentication(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", strings.NewReader(
		`{"padding":"`+strings.Repeat("x", int(maxAgentHeartbeatBodyBytes))+`"}`,
	))
	response := httptest.NewRecorder()

	Dependencies{}.handleHeartbeat(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s, want %d", response.Code, response.Body.String(), http.StatusRequestEntityTooLarge)
	}
}
