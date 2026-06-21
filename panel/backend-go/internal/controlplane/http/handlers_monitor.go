package http

import (
	"encoding/json"
	"net/http"
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

	for {
		select {
		case <-r.Context().Done():
			return
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
