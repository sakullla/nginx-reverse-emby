package http

import (
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

const attentionCertExpiryWindow = 30 * 24 * time.Hour

type attentionAgentGroup struct {
	Count    int      `json:"count"`
	AgentIDs []string `json:"agent_ids"`
}

type attentionCertItem struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	NotAfter string `json:"not_after"`
}

type attentionCertGroup struct {
	Count int                 `json:"count"`
	Items []attentionCertItem `json:"items"`
}

func (d Dependencies) handleDashboardAttention(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to list agents"))
		return
	}
	agents, err = d.filterAgents(r.Context(), agents)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	if d.accessFilteringActive(r.Context()) {
		agentIDs := make([]string, 0, len(agents))
		for _, agent := range agents {
			agentIDs = append(agentIDs, agent.ID)
		}
		r = r.WithContext(service.WithVisibleTrafficAgents(r.Context(), agentIDs))
	}

	offline := attentionAgentGroup{AgentIDs: []string{}}
	syncFailed := attentionAgentGroup{AgentIDs: []string{}}
	for _, agent := range agents {
		if agent.Status != "online" {
			offline.AgentIDs = append(offline.AgentIDs, agent.ID)
			continue
		}
		applyFailed := agent.LastApplyStatus != "" && agent.LastApplyStatus != "success"
		if !applyFailed {
			continue
		}
		// 与前端 getAgentSyncStatus 一致:落后于目标版本且尚未尝试应用到该版本时算 pending,不算失败
		if agent.DesiredRevision > agent.CurrentRevision && agent.LastApplyRevision < agent.DesiredRevision {
			continue
		}
		syncFailed.AgentIDs = append(syncFailed.AgentIDs, agent.ID)
	}
	offline.Count = len(offline.AgentIDs)
	syncFailed.Count = len(syncFailed.AgentIDs)

	blocked := attentionAgentGroup{AgentIDs: []string{}}
	if d.Config.TrafficStatsEnabled {
		agentNames := make(map[string]string, len(agents))
		for _, a := range agents {
			agentNames[a.ID] = a.Name
		}
		overview, err := d.TrafficService.Overview(r.Context(), "", "day", agentNames)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		for _, agent := range overview.Agents {
			if agent.Blocked {
				blocked.AgentIDs = append(blocked.AgentIDs, agent.AgentID)
			}
		}
	}
	blocked.Count = len(blocked.AgentIDs)

	certs, err := d.CertificateService.List(r.Context(), "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to list certificates"))
		return
	}
	certs, err = d.filterCertificates(r.Context(), "", certs)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	now := time.Now()
	threshold := now.Add(attentionCertExpiryWindow)
	expiring := attentionCertGroup{Items: []attentionCertItem{}}
	for _, cert := range certs {
		if attentionCertificateExpiresSoon(cert, now, threshold) {
			expiring.Items = append(expiring.Items, attentionCertItem{
				ID:       cert.ID,
				Domain:   cert.Domain,
				NotAfter: cert.NotAfter,
			})
		}
	}
	expiring.Count = len(expiring.Items)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"offline":        offline,
		"blocked":        blocked,
		"expiring_certs": expiring,
		"sync_failed":    syncFailed,
		"certs_total":    len(certs),
	})
}

func attentionCertificateExpiresSoon(cert service.ManagedCertificate, now, defaultThreshold time.Time) bool {
	if cert.NotAfter == "" {
		return false
	}
	notAfter, err := time.Parse(time.RFC3339, cert.NotAfter)
	if err != nil || !notAfter.After(now) {
		return false
	}
	threshold := defaultThreshold
	// Let's Encrypt's short-lived IP profile produces certificates whose total
	// lifetime is far below the normal 30-day warning window. Match the Agent's
	// renewal policy for these automatically managed certificates so a freshly
	// issued IP certificate is not permanently reported as about to expire.
	if cert.Scope == "ip" && cert.CertificateType == "acme" && cert.IssuerMode == "local_http01" {
		issuedAt, parseErr := time.Parse(time.RFC3339Nano, cert.LastIssueAt)
		if parseErr == nil && issuedAt.Before(notAfter) {
			lifetime := notAfter.Sub(issuedAt)
			if scaled := lifetime / 3; scaled > 0 && scaled < attentionCertExpiryWindow {
				threshold = now.Add(scaled)
			}
		}
	}
	return !notAfter.After(threshold)
}
