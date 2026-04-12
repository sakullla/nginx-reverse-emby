package http

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

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
	payload.LastSeenIP = remoteIPFromRequest(r)

	reply, err := d.AgentService.Heartbeat(r.Context(), payload, r.Header.Get("X-Agent-Token"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"sync": heartbeatSyncPayload(reply, requestBaseURL(r)),
	})
}

func remoteIPFromRequest(r *http.Request) string {
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if forwarded != "" {
		return forwarded
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
		payload["certificates"] = reply.Certificates
		payload["certificate_policies"] = reply.CertificatePolicies
	} else if len(reply.RelayListeners) > 0 {
		payload["certificates"] = reply.Certificates
		payload["certificate_policies"] = reply.CertificatePolicies
	}
	return payload
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
