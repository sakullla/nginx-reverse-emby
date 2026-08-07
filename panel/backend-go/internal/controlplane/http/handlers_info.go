package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
)

func (d Dependencies) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"role": "master",
	})
}

func (d Dependencies) handleVerify(w http.ResponseWriter, r *http.Request) {
	_, err := d.authenticatePanelRequest(r)
	if err != nil && !errors.Is(err, authz.ErrUnauthorized) && !errors.Is(err, authz.ErrInvalidCredentials) {
		writeAccessError(w, err)
		return
	}
	authorized := err == nil
	status := http.StatusOK
	if !authorized {
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, map[string]any{
		"ok":   authorized,
		"role": "master",
	})
}

func (d Dependencies) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := d.SystemService.Info(r.Context())
	actor, hasActor := actorFromRequest(r)
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		writeAccessError(w, err)
		return
	}
	agents, err = d.filterAgents(r.Context(), agents)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	visibleDefault := ""
	if hasActor && (actor.Bootstrap || actor.Has(authz.PermissionAll)) {
		visibleDefault = info.DefaultAgentID
	}
	onlineAgents := 0
	for _, agent := range agents {
		if agent.ID == info.DefaultAgentID {
			visibleDefault = info.DefaultAgentID
		}
		if strings.EqualFold(agent.Status, "online") {
			onlineAgents++
		}
	}
	payload := map[string]any{
		"ok":                              true,
		"role":                            info.Role,
		"local_apply_runtime":             info.LocalApplyRuntime,
		"default_agent_id":                visibleDefault,
		"local_agent_enabled":             info.LocalAgentEnabled,
		"proxy_headers_globally_disabled": info.ProxyHeadersGloballyDisabled,
		"app_version":                     info.AppVersion,
		"build_time":                      info.BuildTime,
		"go_version":                      info.GoVersion,
		"project_url":                     info.ProjectURL,
		"started_at":                      info.StartedAt.Format(time.RFC3339),
		"online_agents":                   onlineAgents,
		"total_agents":                    len(agents),
		"traffic_stats_enabled":           info.TrafficStatsEnabled,
	}
	if hasActor && actor.Has(authz.PermissionSystemAdmin) {
		payload["data_dir"] = info.DataDir
	}
	if hasActor && actor.Has(authz.PermissionAll) && d.Config.RegisterToken != "" {
		payload["master_register_token"] = d.Config.RegisterToken
	}
	writeJSON(w, http.StatusOK, payload)
}
