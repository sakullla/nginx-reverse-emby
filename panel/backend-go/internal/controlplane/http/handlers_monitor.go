package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

const (
	defaultMonitorStreamRefreshInterval = 5 * time.Second
	defaultMonitorStreamMaxAge          = 60 * time.Second
)

func (d Dependencies) handleAgentMonitorStream(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorPayload("streaming unsupported"))
		return
	}

	updates, unsubscribe := d.AgentService.SubscribeMonitorUpdates(r.Context())
	defer unsubscribe()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	snapshot, err := d.AgentService.MonitorSnapshot(r.Context())
	if err != nil {
		writeMonitorStreamMessage(w, flusher, "error", map[string]any{"message": "monitor snapshot unavailable"})
		return
	}
	if !writeMonitorStreamMessage(w, flusher, "snapshot", snapshot) {
		return
	}

	refreshInterval := d.monitorStreamRefreshInterval()
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	maxAge := d.monitorStreamMaxAge()
	maxAgeTimer := time.NewTimer(maxAge)
	defer maxAgeTimer.Stop()
	previousSnapshot := snapshot

	for {
		select {
		case <-r.Context().Done():
			return
		case <-maxAgeTimer.C:
			return
		case <-ticker.C:
			snapshot, err := d.AgentService.MonitorSnapshot(r.Context())
			if err != nil {
				writeMonitorStreamMessage(w, flusher, "error", map[string]any{"message": "monitor snapshot unavailable"})
				return
			}
			snapshot = snapshotWithMonitorRates(snapshot, previousSnapshot)
			if !writeMonitorStreamMessage(w, flusher, "snapshot", snapshot) {
				return
			}
			previousSnapshot = snapshot
		case update, ok := <-updates:
			if !ok {
				return
			}
			if !writeMonitorStreamMessage(w, flusher, "update", update) {
				return
			}
		}
	}
}

func (d Dependencies) monitorStreamRefreshInterval() time.Duration {
	if d.MonitorStreamRefreshInterval > 0 {
		return d.MonitorStreamRefreshInterval
	}
	return defaultMonitorStreamRefreshInterval
}

func (d Dependencies) monitorStreamMaxAge() time.Duration {
	if d.MonitorStreamMaxAge > 0 {
		return d.MonitorStreamMaxAge
	}
	return defaultMonitorStreamMaxAge
}

func snapshotWithMonitorRates(current, previous service.AgentMonitorSnapshot) service.AgentMonitorSnapshot {
	previousByID := make(map[string]service.AgentMonitorAgent, len(previous.Agents))
	for _, agent := range previous.Agents {
		if agent.ID != "" {
			previousByID[agent.ID] = agent
		}
	}
	window := monitorSnapshotWindowSeconds(current.GeneratedAt, previous.GeneratedAt)
	if window <= 0 {
		return current
	}
	for idx := range current.Agents {
		previousAgent, ok := previousByID[current.Agents[idx].ID]
		if !ok {
			continue
		}
		applyMonitorNetworkRates(&current.Agents[idx], previousAgent, window, current.GeneratedAt)
	}
	return current
}

func monitorSnapshotWindowSeconds(currentAt, previousAt string) float64 {
	currentTime, err := time.Parse(time.RFC3339, currentAt)
	if err != nil {
		return 0
	}
	previousTime, err := time.Parse(time.RFC3339, previousAt)
	if err != nil {
		return 0
	}
	return currentTime.Sub(previousTime).Seconds()
}

func applyMonitorNetworkRates(current *service.AgentMonitorAgent, previous service.AgentMonitorAgent, windowSeconds float64, calculatedAt string) {
	if current == nil || current.Metrics.Network == nil || previous.Metrics.Network == nil || windowSeconds <= 0 {
		return
	}
	network := current.Metrics.Network
	previousNetwork := previous.Metrics.Network
	if network.RXBytes != nil && previousNetwork.RXBytes != nil && *network.RXBytes >= *previousNetwork.RXBytes {
		rate := float64(*network.RXBytes-*previousNetwork.RXBytes) / windowSeconds
		network.RXBytesPerSecond = &rate
	}
	if network.TXBytes != nil && previousNetwork.TXBytes != nil && *network.TXBytes >= *previousNetwork.TXBytes {
		rate := float64(*network.TXBytes-*previousNetwork.TXBytes) / windowSeconds
		network.TXBytesPerSecond = &rate
	}
	if network.RXBytesPerSecond != nil || network.TXBytesPerSecond != nil {
		window := windowSeconds
		network.RateWindowSeconds = &window
		network.RateCalculatedAt = calculatedAt
		network.RateAvailable = true
		network.RateUnavailableWhy = ""
	}
}

func writeMonitorStreamMessage(w http.ResponseWriter, flusher http.Flusher, messageType string, payload any) bool {
	data, err := json.Marshal(map[string]any{
		"type":    messageType,
		"payload": payload,
	})
	if err != nil {
		return false
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
