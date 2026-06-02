package relay

import (
	"slices"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	identity := strings.TrimSpace(agentID)
	fallback := strings.TrimSpace(agentName)
	if identity == "" && fallback == "" {
		return cloneRelayListeners(listeners)
	}
	filtered := make([]model.RelayListener, 0, len(listeners))
	for _, listener := range listeners {
		listenerAgentID := strings.TrimSpace(listener.AgentID)
		listenerAgentName := strings.TrimSpace(listener.AgentName)
		if (identity != "" && (listenerAgentID == identity || listenerAgentName == identity)) ||
			(fallback != "" && (listenerAgentID == fallback || listenerAgentName == fallback)) {
			filtered = append(filtered, listener)
		}
	}
	return cloneRelayListeners(filtered)
}

func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	cloned := slices.Clone(listeners)
	for i, listener := range listeners {
		cloned[i].BindHosts = slices.Clone(listener.BindHosts)
		cloned[i].CertificateID = clonePtr(listener.CertificateID)
		cloned[i].WireGuardProfileID = clonePtr(listener.WireGuardProfileID)
		cloned[i].PinSet = slices.Clone(listener.PinSet)
		cloned[i].TrustedCACertificateIDs = slices.Clone(listener.TrustedCACertificateIDs)
		cloned[i].Tags = slices.Clone(listener.Tags)
	}
	return cloned
}

func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func relayListenerBindHosts(listener model.RelayListener) []string {
	bindHosts := make([]string, 0, len(listener.BindHosts))
	seen := make(map[string]struct{}, len(listener.BindHosts))
	for _, rawHost := range listener.BindHosts {
		host := strings.TrimSpace(rawHost)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		bindHosts = append(bindHosts, host)
	}
	if len(bindHosts) == 0 && strings.TrimSpace(listener.ListenHost) != "" {
		bindHosts = append(bindHosts, strings.TrimSpace(listener.ListenHost))
	}
	return bindHosts
}
