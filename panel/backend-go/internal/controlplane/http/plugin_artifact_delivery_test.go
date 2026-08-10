package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pluginArtifactServiceStub struct {
	agentID    string
	artifactID string
	revision   int64
	digest     string
	artifact   service.AgentPluginArtifact
	err        error
	calls      int
}

func TestHeartbeatProjectsStableArtifactIdentityWithoutServerFilesystemPath(t *testing.T) {
	snapshotDigest := strings.Repeat("a", 64)
	reply := service.HeartbeatReply{HasUpdate: true, DesiredRevision: 7, SnapshotDigest: snapshotDigest, PluginGenerations: []storage.PluginGeneration{}, PluginDependencies: []storage.PluginDependencyEdge{}, PluginPolicies: []storage.PluginPolicy{{ID: "shared", Stages: []storage.PolicyStage{{
		ArtifactPath:   `C:\panel\data\plugins\packages\secret\policy.wasm`,
		ArtifactSource: storage.PolicyArtifactSource{ArtifactID: "artifact-1"},
	}}}}}
	payload := heartbeatSyncPayload(reply, "https://control.example")
	if payload["desired_revision"] != int64(7) || payload["snapshot_digest"] != snapshotDigest {
		t.Fatalf("heartbeat revision binding = %v/%v", payload["desired_revision"], payload["snapshot_digest"])
	}
	if generations, ok := payload["plugin_generations"].([]storage.PluginGeneration); !ok || generations == nil {
		t.Fatalf("heartbeat plugin_generations presence = %#v", payload["plugin_generations"])
	}
	if dependencies, ok := payload["plugin_dependencies"].([]storage.PluginDependencyEdge); !ok || dependencies == nil {
		t.Fatalf("heartbeat plugin_dependencies presence = %#v", payload["plugin_dependencies"])
	}
	encoded, err := json.Marshal(payload["plugin_policies"])
	if err != nil {
		t.Fatal(err)
	}
	var policies []storage.PluginPolicy
	if err := json.Unmarshal(encoded, &policies); err != nil {
		t.Fatal(err)
	}
	stage := policies[0].Stages[0]
	if stage.ArtifactPath != "" {
		t.Fatalf("remote artifact path leaked server path %q", stage.ArtifactPath)
	}
	if stage.ArtifactSource.ArtifactID != "artifact-1" {
		t.Fatalf("remote artifact identity = %q", stage.ArtifactSource.ArtifactID)
	}
	if strings.Contains(string(encoded), `"url"`) {
		t.Fatalf("remote artifact projection contains transport-specific URL: %s", encoded)
	}
}

func (s *pluginArtifactServiceStub) ResolveAgentPluginArtifact(_ context.Context, agentID string, revision int64, digest, artifactID string) (service.AgentPluginArtifact, error) {
	s.calls++
	s.agentID, s.artifactID = agentID, artifactID
	s.revision, s.digest = revision, digest
	return s.artifact, s.err
}

func TestAgentPluginArtifactRouteRequiresAgentTokenAndStreamsVerifiedArtifact(t *testing.T) {
	payload := []byte("policy wasm")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	artifacts := &pluginArtifactServiceStub{artifact: service.AgentPluginArtifact{Payload: payload, SHA256: digest, SizeBytes: int64(len(payload))}}
	deps := Dependencies{
		AgentService:          fakeAgentService{agentsByToken: map[string]service.AgentSummary{"agent-secret": {ID: "edge-1"}}},
		PluginArtifactService: artifacts,
	}

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/panel-api/agent-plugin-artifacts/artifact-1", nil)
	unauthorizedRequest.SetPathValue("artifactID", "artifact-1")
	deps.handleAgentPluginArtifact(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized || artifacts.calls != 0 {
		t.Fatalf("unauthorized response = %d, resolver calls = %d", unauthorized.Code, artifacts.calls)
	}
	missingBinding := httptest.NewRecorder()
	missingBindingRequest := httptest.NewRequest(http.MethodGet, "/panel-api/agent-plugin-artifacts/artifact-1", nil)
	missingBindingRequest.SetPathValue("artifactID", "artifact-1")
	missingBindingRequest.Header.Set("X-Agent-Token", "agent-secret")
	deps.handleAgentPluginArtifact(missingBinding, missingBindingRequest)
	if missingBinding.Code != http.StatusBadRequest || artifacts.calls != 0 {
		t.Fatalf("missing revision binding response = %d, resolver calls = %d", missingBinding.Code, artifacts.calls)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panel-api/agent-plugin-artifacts/artifact-1?revision=7&snapshot_digest="+digest, nil)
	request.SetPathValue("artifactID", "artifact-1")
	request.Header.Set("X-Agent-Token", "agent-secret")
	deps.handleAgentPluginArtifact(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(payload) {
		t.Fatalf("artifact response = %d %q", response.Code, response.Body.String())
	}
	if artifacts.agentID != "edge-1" || artifacts.revision != 7 || artifacts.digest != digest || artifacts.artifactID != "artifact-1" {
		t.Fatalf("resolver identity = %q/%d/%q/%q", artifacts.agentID, artifacts.revision, artifacts.digest, artifacts.artifactID)
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Type") != "application/wasm" {
		t.Fatalf("artifact headers = %v", response.Header())
	}
}

func TestAgentPluginArtifactRouteRejectsDigestMismatch(t *testing.T) {
	snapshotDigest := strings.Repeat("a", 64)
	deps := Dependencies{
		AgentService: fakeAgentService{agentsByToken: map[string]service.AgentSummary{"agent-secret": {ID: "edge-1"}}},
		PluginArtifactService: &pluginArtifactServiceStub{artifact: service.AgentPluginArtifact{
			Payload: []byte("tampered"), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("expected"))), SizeBytes: int64(len("tampered")),
		}},
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panel-api/agent-plugin-artifacts/artifact-1?revision=7&snapshot_digest="+snapshotDigest, nil)
	request.SetPathValue("artifactID", "artifact-1")
	request.Header.Set("X-Agent-Token", "agent-secret")
	deps.handleAgentPluginArtifact(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("digest mismatch status = %d", response.Code)
	}
}
