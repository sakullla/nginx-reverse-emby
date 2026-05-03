package http

import (
	"net/http"
	"time"
)

func (d Dependencies) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"role": "master",
	})
}

func (d Dependencies) handleVerify(w http.ResponseWriter, r *http.Request) {
	authorized := d.isPanelAuthorized(r)
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
	authorized := d.isPanelAuthorized(r)
	payload := map[string]any{
		"ok":                              true,
		"role":                            info.Role,
		"local_apply_runtime":             info.LocalApplyRuntime,
		"default_agent_id":                info.DefaultAgentID,
		"local_agent_enabled":             info.LocalAgentEnabled,
		"proxy_headers_globally_disabled": info.ProxyHeadersGloballyDisabled,
		"app_version":                     info.AppVersion,
		"build_time":                      info.BuildTime,
		"go_version":                      info.GoVersion,
		"project_url":                     info.ProjectURL,
		"started_at":                      info.StartedAt.Format(time.RFC3339),
		"online_agents":                   info.OnlineAgents,
		"total_agents":                    info.TotalAgents,
		"traffic_stats_enabled":           info.TrafficStatsEnabled,
	}
	if authorized {
		payload["data_dir"] = info.DataDir
	}
	if authorized && d.Config.RegisterToken != "" {
		payload["master_register_token"] = d.Config.RegisterToken
	}
	writeJSON(w, http.StatusOK, payload)
}
