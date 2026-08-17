package http

import (
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

const (
	nreActorHeader         = "X-NRE-Actor"
	nreResourceGroupHeader = "X-NRE-Resource-Group"
	panelSessionActor      = "panel/admin"
)

func (d Dependencies) handlePluginUIRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": pluginhost.ListUIRoutes()})
}

func (d Dependencies) handlePluginResourceGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": pluginhost.ListResourceGroups()})
}

func (d Dependencies) handlePluginUI(w http.ResponseWriter, r *http.Request) {
	routeID, suffix := pluginhost.SplitPluginUIPath(r.URL.Path)
	if routeID == "" {
		writeJSON(w, http.StatusNotFound, errorPayload("plugin UI was not found"))
		return
	}
	if suffix == "" {
		http.Redirect(w, r, r.URL.Path+"/", http.StatusTemporaryRedirect)
		return
	}
	handler, group, ok := pluginhost.Lookup(routeID)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload(routeID+" plugin is unavailable"))
		return
	}

	forward := r.Clone(r.Context())
	forward.URL.Path = suffix
	if r.URL.RawPath != "" {
		_, rawSuffix := pluginhost.SplitPluginUIPath(r.URL.RawPath)
		if rawSuffix != "" {
			forward.URL.RawPath = rawSuffix
		}
	}
	forward.Header.Set(nreActorHeader, panelSessionActor)
	if group.Ref != "" {
		forward.Header.Set(nreResourceGroupHeader, group.Ref)
	}
	handler.ServeHTTP(w, forward)
}

func isPluginUIPage(path string) bool {
	routeID, suffix := pluginhost.SplitPluginUIPath(path)
	if routeID == "" {
		return false
	}
	return !strings.Contains(suffix, "/api/")
}
