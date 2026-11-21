package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
	handler, group, owner, instance, generation, ok := pluginhost.LookupRuntimeUI(routeID)
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
	for _, key := range []string{"X-NRE-Listener-Grant", "X-NRE-Listener-ID", "X-NRE-Listener-Certificate-Domain", "X-NRE-Agent", nreResourceGroupHeader} {
		forward.Header.Del(key)
	}
	forward.Header.Set(nreActorHeader, panelSessionActor)
	if agentID := strings.TrimSpace(r.URL.Query().Get("agent_id")); agentID != "" {
		forward.Header.Set("X-NRE-Agent", agentID)
	}
	if group.Ref != "" {
		forward.Header.Set(nreResourceGroupHeader, group.Ref)
	}
	if routeID == storage.ListenerCertificatePlugin && owner == routeID && instance != "" && generation != "" {
		if api, ok := d.PluginCapabilityService.(pluginListenerCertificateAPI); ok {
			done, release := d.preparePluginListenerCertificate(w, forward, suffix, instance, generation, api)
			if release != nil {
				defer release()
			}
			if done {
				return
			}
		}
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

type pluginListenerCertificateAPI interface {
	ListenerCertificateCatalog(context.Context, authz.Actor, string, string, string) ([]storage.PublicCertificateProjection, error)
	BeginListenerCertificateGrant(context.Context, authz.Actor, string, string, string, string, int) (string, storage.ListenerCertificateRef, func(), error)
}

func (d Dependencies) preparePluginListenerCertificate(w http.ResponseWriter, r *http.Request, suffix, instance, generation string, api pluginListenerCertificateAPI) (bool, func()) {
	queryAgent := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	actor, authenticated := actorFromRequest(r)
	if suffix == "/api/public-certificates" && r.Method == http.MethodGet && queryAgent != "" && r.URL.Query().Get("listener_binding") == "1" {
		if !authenticated {
			writeAccessError(w, authz.ErrUnauthorized)
			return true, nil
		}
		rows, err := api.ListenerCertificateCatalog(r.Context(), actor, instance, generation, queryAgent)
		if err != nil {
			writePluginError(w, err)
			return true, nil
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "certificates": rows})
		return true, nil
	}
	if r.Method != http.MethodPost {
		return false, nil
	}
	deleteID := ""
	if suffix != "/api/listens" {
		path, ok := strings.CutPrefix(suffix, "/api/listens/")
		id, okDelete := strings.CutSuffix(path, "/delete")
		if !ok || !okDelete || id == "" || strings.Contains(id, "/") {
			return false, nil
		}
		deleteID = id
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorPayload("listener request is too large"))
		return true, nil
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	var input struct {
		AgentID       string `json:"agent_id"`
		Protocol      string `json:"protocol"`
		CertificateID int    `json:"certificate_id"`
	}
	if len(data) > 0 && json.Unmarshal(data, &input) != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid listener request"))
		return true, nil
	}
	if deleteID == "" && (!strings.EqualFold(strings.TrimSpace(input.Protocol), "anytls") || input.CertificateID <= 0) {
		return false, nil
	}
	if !authenticated {
		writeAccessError(w, authz.ErrUnauthorized)
		return true, nil
	}
	agent := strings.TrimSpace(input.AgentID)
	if agent == "" {
		agent = queryAgent
	}
	if queryAgent != "" && queryAgent != agent {
		writeAccessError(w, authz.ErrForbidden)
		return true, nil
	}
	token, ref, release, err := api.BeginListenerCertificateGrant(r.Context(), actor, instance, generation, agent, deleteID, input.CertificateID)
	if err != nil {
		writePluginError(w, err)
		return true, release
	}
	if token != "" {
		r.Header.Set("X-NRE-Listener-Grant", token)
		r.Header.Set("X-NRE-Listener-ID", ref.ID)
		r.Header.Set("X-NRE-Listener-Certificate-Domain", ref.Domain)
	}
	return false, release
}
