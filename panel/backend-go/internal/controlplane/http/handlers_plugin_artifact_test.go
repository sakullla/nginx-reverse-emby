package http

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type artifactAuthAgentService struct{ AgentService }

func (artifactAuthAgentService) GetByToken(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{ID: "edge-a"}, nil
}

type staticAgentPluginArtifactService struct{ artifact service.AgentPluginArtifact }

func (fake staticAgentPluginArtifactService) ResolveAgentPluginArtifact(context.Context, string, int64, string, string) (service.AgentPluginArtifact, error) {
	return fake.artifact, nil
}

func TestAgentPluginArtifactSupportsResumeRange(t *testing.T) {
	payload := []byte("revision-bound-plugin-artifact")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	request := httptest.NewRequest(http.MethodGet, "/api/agent-plugin-artifacts/artifact-1?revision=7&snapshot_digest="+strings.Repeat("c", 64), nil)
	request.SetPathValue("artifactID", "artifact-1")
	request.Header.Set("X-Agent-Token", "agent-token")
	request.Header.Set("Range", "bytes=9-")
	response := httptest.NewRecorder()

	Dependencies{
		AgentService: artifactAuthAgentService{},
		PluginArtifactService: staticAgentPluginArtifactService{artifact: service.AgentPluginArtifact{
			Payload: payload, SHA256: digest, SizeBytes: int64(len(payload)),
		}},
	}.handleAgentPluginArtifact(response, request)

	if response.Code != http.StatusPartialContent {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if got, want := response.Body.String(), string(payload[9:]); got != want {
		t.Fatalf("range body = %q, want %q", got, want)
	}
	if got, want := response.Header().Get("Content-Range"), "bytes 9-29/30"; got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
}
