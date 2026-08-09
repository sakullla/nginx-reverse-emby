package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (d Dependencies) handleAgentPluginArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateRevisionAgent(w, r)
	if !ok {
		return
	}
	if d.PluginArtifactService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("plugin artifact service unavailable"))
		return
	}
	artifact, err := d.PluginArtifactService.ResolveAgentPluginArtifact(r.Context(), agent.ID, r.PathValue("artifactID"))
	if err != nil {
		if errors.Is(err, service.ErrPluginArtifactUnavailable) {
			writeJSON(w, http.StatusNotFound, errorPayload("plugin artifact not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorPayload("plugin artifact integrity check failed"))
		return
	}
	file, err := os.Open(artifact.Path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorPayload("plugin artifact not found"))
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.SizeBytes {
		writeJSON(w, http.StatusInternalServerError, errorPayload("plugin artifact integrity check failed"))
		return
	}
	var verified bytes.Buffer
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(hash, &verified), io.LimitReader(file, artifact.SizeBytes+1)); err != nil || int64(verified.Len()) != artifact.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		writeJSON(w, http.StatusInternalServerError, errorPayload("plugin artifact integrity check failed"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(artifact.SizeBytes, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, &verified)
}

func (d Dependencies) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	var payload service.HeartbeatRequest
	if err := decodeRawMessageMap(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	_, payload.HasAgentURL = body["agent_url"]
	_, payload.HasTags = body["tags"]
	_, payload.HasCapabilities = body["capabilities"]
	payload.LastSeenIP = remoteIPFromRequest(r, d.Config.TrustForwardedHeaders)

	reply, err := d.AgentService.Heartbeat(r.Context(), payload, r.Header.Get("X-Agent-Token"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"sync": heartbeatSyncPayload(reply, d.requestBaseURL(r)),
	})
}

func remoteIPFromRequest(r *http.Request, trustForwardedHeaders bool) string {
	if trustForwardedHeaders {
		forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
		if forwarded != "" {
			return forwarded
		}
	}
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return remoteAddr
}

func heartbeatSyncPayload(reply service.HeartbeatReply, baseURL string) map[string]any {
	payload := map[string]any{
		"has_update":       reply.HasUpdate,
		"desired_version":  reply.DesiredVersion,
		"desired_revision": reply.DesiredRevision,
		"current_revision": reply.CurrentRevision,
		"relay_listeners":  reply.RelayListeners,
		"egress_profiles":  reply.EgressProfiles,
		// ddns_config is dispatched unconditionally (like agent_config) so the agent
		// always sees its current extraction configuration. It is a *storage.DDNSConfig
		// carrying only domain + per-family source/interface — never a Cloudflare
		// credential (R7); CF tokens live only in the master process environment.
		"ddns_config": reply.DDNSConfig,
	}
	if reply.PKISecurity != nil {
		payload["pki_security"] = reply.PKISecurity
	}
	if len(reply.PKICredentials) != 0 {
		payload["pki_credentials"] = reply.PKICredentials
	}
	if reply.PKIStatus != nil {
		payload["pki_status"] = reply.PKIStatus
	}
	payload["agent_config"] = service.AgentRuntimeConfig{
		OutboundProxyURL:     reply.OutboundProxyURL,
		TrafficStatsInterval: reply.TrafficStatsInterval,
		TrafficStatsEnabled:  reply.TrafficStatsEnabled,
		TrafficBlocked:       reply.TrafficBlocked,
		TrafficBlockReason:   reply.TrafficBlockReason,
	}
	if reply.VersionPackage != "" {
		payload["version_package"] = absolutePublicURL(baseURL, reply.VersionPackage)
	}
	if reply.VersionPackageMeta != nil {
		meta := *reply.VersionPackageMeta
		meta.URL = absolutePublicURL(baseURL, meta.URL)
		payload["version_package_meta"] = meta
	}
	if reply.VersionSHA256 != "" {
		payload["version_sha256"] = reply.VersionSHA256
	}
	if reply.HasUpdate {
		payload["rules"] = reply.Rules
		payload["l4_rules"] = reply.L4Rules
		payload["plugin_policies"] = remotePluginPolicies(reply.PluginPolicies)
		payload["certificates"] = reply.Certificates
		payload["certificate_policies"] = reply.CertificatePolicies
	} else if len(reply.RelayListeners) > 0 {
		payload["certificates"] = reply.Certificates
		payload["certificate_policies"] = reply.CertificatePolicies
	}
	return payload
}

func remotePluginPolicies(policies []storage.PluginPolicy) []storage.PluginPolicy {
	if policies == nil {
		return nil
	}
	cloned := make([]storage.PluginPolicy, len(policies))
	for policyIndex, policy := range policies {
		cloned[policyIndex] = policy
		cloned[policyIndex].Stages = make([]storage.PolicyStage, len(policy.Stages))
		for stageIndex, stage := range policy.Stages {
			stage.ArtifactPath = ""
			stage.ExtensionPoints = append([]string(nil), stage.ExtensionPoints...)
			stage.GrantedScopes = append([]string(nil), stage.GrantedScopes...)
			stage.Config = append(json.RawMessage(nil), stage.Config...)
			cloned[policyIndex].Stages[stageIndex] = stage
		}
	}
	return cloned
}

func absolutePublicURL(baseURL string, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return strings.TrimRight(baseURL, "/") + trimmed
	}
	return trimmed
}

func (d Dependencies) handleJoinAgentScript(w http.ResponseWriter, r *http.Request) {
	script, err := d.buildJoinAgentScript(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("join script not available"))
		return
	}

	writeText(w, http.StatusOK, script, "application/x-sh; charset=utf-8")
}

func (d Dependencies) handlePublicAgentAsset(w http.ResponseWriter, r *http.Request) {
	assetName := publicAssetName(r.URL.Path)
	if assetName == "" {
		writeJSON(w, http.StatusNotFound, errorPayload("asset not found"))
		return
	}

	assetPath, ok := resolvePublicAgentAssetPath(d.Config.PublicAgentAssetsDir, assetName)
	if !ok {
		writeJSON(w, http.StatusNotFound, errorPayload("asset not found"))
		return
	}
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, errorPayload("asset not found"))
		return
	}

	serveFile(w, r, assetPath, staticContentType(assetPath), map[string]string{
		"Cache-Control": "public, max-age=300",
	})
}

func publicAssetName(requestPath string) string {
	switch {
	case strings.HasPrefix(requestPath, "/panel-api/public/agent-assets/"):
		return strings.TrimPrefix(requestPath, "/panel-api/public/agent-assets/")
	case strings.HasPrefix(requestPath, "/api/public/agent-assets/"):
		return strings.TrimPrefix(requestPath, "/api/public/agent-assets/")
	default:
		return ""
	}
}

func resolvePublicAgentAssetPath(root string, assetName string) (string, bool) {
	trimmed := strings.TrimSpace(assetName)
	if trimmed == "" {
		return "", false
	}
	if trimmed != filepath.Base(trimmed) {
		return "", false
	}
	if strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return "", false
	}
	return filepath.Join(filepath.Clean(root), trimmed), true
}
