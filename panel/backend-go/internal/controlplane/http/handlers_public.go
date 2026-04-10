package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var payload service.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}

	reply, err := d.AgentService.Heartbeat(r.Context(), payload, r.Header.Get("X-Agent-Token"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"sync": reply,
	})
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

	assetPath := filepath.Join(d.Config.PublicAgentAssetsDir, filepath.FromSlash(assetName))
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
