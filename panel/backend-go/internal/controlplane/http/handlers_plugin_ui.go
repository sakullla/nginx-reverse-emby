package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

const pluginUIPageCSP = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

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
	routes := pluginhost.ListUIRoutes()
	if catalog, ok := d.PluginService.(PluginUICatalogAPI); ok {
		declared, err := catalog.DeclaredUIRoutes(r.Context())
		if err != nil {
			writePluginError(w, err)
			return
		}
		routes = service.MergePluginUIRoutes(routes, declared)
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
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
		if d.serveDeclaredPluginUIAsset(w, r, routeID, suffix) {
			return
		}
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
	if agentID := strings.TrimSpace(r.URL.Query().Get("agent_id")); agentID != "" {
		forward.Header.Set("X-NRE-Agent", agentID)
	}
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

func (d Dependencies) serveDeclaredPluginUIAsset(w http.ResponseWriter, r *http.Request, routeID, suffix string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	catalog, ok := d.PluginService.(PluginUICatalogAPI)
	if !ok {
		return false
	}
	name, data, err := catalog.OpenUIAsset(r.Context(), routeID, suffix)
	if err != nil {
		if errors.Is(err, service.ErrPluginUIAssetNotFound) || errors.Is(err, service.ErrPluginNotInstalled) {
			return false
		}
		writePluginError(w, err)
		return true
	}
	w.Header().Set("Content-Type", service.PluginUIAssetContentType(name))
	w.Header().Set("Content-Security-Policy", pluginUIPageCSP)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
	return true
}
