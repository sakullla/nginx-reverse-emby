package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

// pluginHostChannelMaxRelayHops bounds the optional relay chain a reverse
// channel session may dial through.
const pluginHostChannelMaxRelayHops = 32

// pluginHostChannelListenerProjector attaches canonical tunnel PKI references
// to relay listener projections before they reach an agent.
type pluginHostChannelListenerProjector func(context.Context, string, []storage.RelayListener) ([]storage.RelayListener, error)

type pluginHostChannelAgentLookupStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

type pluginHostChannelListenerStore interface {
	ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error)
}

func (manager *PluginCapabilityManager) pluginHostChannelAgentRow(ctx context.Context, agentID string) (storage.AgentRow, error) {
	store, ok := manager.store.(pluginHostChannelAgentLookupStore)
	if !ok {
		return storage.AgentRow{}, errPluginHostUnavailable
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return storage.AgentRow{}, err
	}
	for _, row := range rows {
		if row.ID == agentID {
			return row, nil
		}
	}
	return storage.AgentRow{}, errPluginHostInvalid
}

func (manager *PluginCapabilityManager) pluginHostChannelReverse(ctx context.Context, candidate pluginhost.Candidate, raw json.RawMessage) (map[string]any, error) {
	var input pluginsdk.ChannelReverseRequest
	if decodePluginHostPayload(raw, &input) != nil || input.Validate() != nil {
		return nil, errPluginHostInvalid
	}
	sessions := pluginHostState(manager).channelTaskDispatcher()
	if sessions == nil {
		return nil, errPluginHostUnavailable
	}
	ctx = WithSystemMutationPrincipal(ctx, "plugin/"+candidate.Identity.PluginID)
	switch input.Action {
	case pluginsdk.ChannelReverseActionEnsure:
		return manager.pluginHostChannelEnsure(ctx, sessions, input)
	case pluginsdk.ChannelReverseActionStatus, pluginsdk.ChannelReverseActionTeardown:
		return manager.pluginHostChannelLookup(ctx, sessions, input)
	default:
		return nil, errPluginHostInvalid
	}
}

// pluginHostChannelEnsure applies the session on both agents: the entry agent
// binds the tunnel ingress and loopback bridge first, then the exit agent
// dials out. The returned bridge endpoint is the L4 rule backend the caller
// may point at.
func (manager *PluginCapabilityManager) pluginHostChannelEnsure(ctx context.Context, sessions pluginHostChannelSessions, input pluginsdk.ChannelReverseRequest) (map[string]any, error) {
	entryID := strings.TrimSpace(input.EntryAgentID)
	exitID := strings.TrimSpace(input.ExitAgentID)
	entryRow, err := manager.pluginHostChannelAgentRow(ctx, entryID)
	if err != nil {
		return nil, err
	}
	exitRow, err := manager.pluginHostChannelAgentRow(ctx, exitID)
	if err != nil {
		return nil, err
	}
	hops, err := manager.pluginHostChannelRelayHops(ctx, input.RelayChain)
	if err != nil {
		return nil, err
	}

	sessionID := strings.TrimSpace(input.SessionRef)
	if sessionID == "" {
		sessionID = pluginHostChannelSessionID(entryID, exitID)
	}

	entryStatus, err := sessions.DispatchAgentTask(ctx, entryID, TaskTypeChannelEnsure, map[string]any{
		"session_id":     sessionID,
		"role":           "entry",
		"protocol":       input.Protocol,
		"entry_agent_id": entryID,
		"exit_agent_id":  exitID,
		"listen_host":    "127.0.0.1",
	})
	if err != nil {
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	bridgeHost, bridgePort, err := pluginHostChannelHostPort(entryStatus["bridge_address"])
	if err != nil {
		return nil, errPluginHostInvalid
	}
	_, ingressPort, err := pluginHostChannelHostPort(entryStatus["ingress_address"])
	if err != nil {
		return nil, errPluginHostInvalid
	}

	dialHost := pluginHostChannelDialHost(entryRow, exitRow)
	candidates := pluginHostChannelBackendHosts(entryRow, exitRow)

	var lastResult map[string]any
	for index, backendHost := range candidates {
		payload := map[string]any{
			"session_id":      sessionID,
			"role":            "exit",
			"protocol":        input.Protocol,
			"entry_agent_id":  entryID,
			"exit_agent_id":   exitID,
			"dial_address":    net.JoinHostPort(dialHost, strconv.Itoa(ingressPort)),
			"backend_address": net.JoinHostPort(backendHost, strconv.Itoa(input.BackendPort)),
		}
		if len(hops) > 0 {
			payload["relay_chain"] = hops
		}
		result, dispatchErr := sessions.DispatchAgentTask(ctx, exitID, TaskTypeChannelEnsure, payload)
		if dispatchErr != nil {
			if errors.Is(dispatchErr, errTaskSessionUnavailable) {
				return nil, errPluginHostUnavailable
			}
			return nil, dispatchErr
		}
		lastResult = result
		if index < len(candidates)-1 && pluginHostChannelState(result) != pluginsdk.ChannelReverseStateOnline {
			continue
		}
		break
	}

	result := pluginHostChannelResult(sessionID, lastResult)
	result["bridge_host"] = bridgeHost
	result["bridge_port"] = bridgePort
	return result, nil
}

func (manager *PluginCapabilityManager) pluginHostChannelLookup(ctx context.Context, sessions pluginHostChannelSessions, input pluginsdk.ChannelReverseRequest) (map[string]any, error) {
	sessionID := strings.TrimSpace(input.SessionRef)
	entryID, exitID, err := manager.pluginHostChannelSessionOwners(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	taskType := TaskTypeChannelStatus
	if input.Action == pluginsdk.ChannelReverseActionTeardown {
		taskType = TaskTypeChannelTeardown
	}
	payload := map[string]any{"session_id": sessionID}
	if _, err := sessions.DispatchAgentTask(ctx, entryID, taskType, payload); err != nil {
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	exitStatus, err := sessions.DispatchAgentTask(ctx, exitID, taskType, payload)
	if err != nil {
		if errors.Is(err, errTaskSessionUnavailable) {
			return nil, errPluginHostUnavailable
		}
		return nil, err
	}
	return pluginHostChannelResult(sessionID, exitStatus), nil
}

// pluginHostChannelSessionOwners resolves the agents a session ref was minted
// for. Teardown and status authority follows the session owners recorded in
// the host-minted ref.
func (manager *PluginCapabilityManager) pluginHostChannelSessionOwners(ctx context.Context, sessionRef string) (string, string, error) {
	owners := pluginHostChannelSessionRefOwners(sessionRef)
	if len(owners) != 2 {
		return "", "", errPluginHostInvalid
	}
	entryID, exitID := owners[0], owners[1]
	if _, err := manager.pluginHostChannelAgentRow(ctx, entryID); err != nil {
		return "", "", err
	}
	if _, err := manager.pluginHostChannelAgentRow(ctx, exitID); err != nil {
		return "", "", err
	}
	return entryID, exitID, nil
}

// pluginHostChannelRelayHops resolves the requested relay listener chain into
// agent wire hops. An empty chain keeps the direct dial.
func (manager *PluginCapabilityManager) pluginHostChannelRelayHops(ctx context.Context, relayChain []int) ([]map[string]any, error) {
	if len(relayChain) == 0 {
		return nil, nil
	}
	if len(relayChain) > pluginHostChannelMaxRelayHops {
		return nil, errPluginHostInvalid
	}
	store, ok := manager.store.(pluginHostChannelListenerStore)
	if !ok {
		return nil, errPluginHostUnavailable
	}
	rows, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	byID := make(map[int]storage.RelayListenerRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	listeners := make([]storage.RelayListener, 0, len(relayChain))
	for _, id := range relayChain {
		row, ok := byID[id]
		if !ok || !row.Enabled {
			return nil, errPluginHostInvalid
		}
		listeners = append(listeners, pluginHostChannelListenerFromRow(row))
	}
	prepared := listeners
	if project := pluginHostState(manager).channelListenerProjector(); project != nil {
		prepared, err = project(ctx, listeners[0].AgentID, listeners)
		if err != nil {
			return nil, errPluginHostInvalid
		}
	}
	hops := make([]map[string]any, 0, len(prepared))
	for _, listener := range prepared {
		if !listener.Enabled {
			return nil, errPluginHostInvalid
		}
		host := strings.TrimSpace(listener.PublicHost)
		if host == "" {
			host = strings.TrimSpace(listener.ListenHost)
		}
		port := listener.PublicPort
		if port <= 0 {
			port = listener.ListenPort
		}
		serverName := host
		if public := strings.TrimSpace(listener.PublicHost); public != "" {
			serverName = public
		}
		hops = append(hops, map[string]any{
			"address":     net.JoinHostPort(host, strconv.Itoa(port)),
			"server_name": serverName,
			"listener":    listener,
		})
	}
	return hops, nil
}

func pluginHostChannelListenerFromRow(row storage.RelayListenerRow) storage.RelayListener {
	return storage.RelayListener{
		ID: row.ID, AgentID: row.AgentID, Name: row.Name,
		ListenHost: strings.TrimSpace(row.ListenHost), BindHosts: pluginHostChannelStringSlice(row.BindHostsJSON),
		ListenPort: row.ListenPort, PublicHost: strings.TrimSpace(row.PublicHost), PublicPort: row.PublicPort,
		Enabled: true, TLSMode: strings.TrimSpace(row.TLSMode),
		TransportMode: strings.TrimSpace(row.TransportMode), AllowTransportFallback: row.AllowTransportFallback,
		ObfsMode: strings.TrimSpace(row.ObfsMode), PinSet: pluginHostChannelRelayPins(row.PinSetJSON),
		TrustedCACertificateIDs: pluginHostChannelIntSlice(row.TrustedCACertificateIDs), AllowSelfSigned: row.AllowSelfSigned,
		Tags: pluginHostChannelStringSlice(row.TagsJSON), Revision: int64(row.Revision),
	}
}

func pluginHostChannelStringSlice(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return nil
	}
	return values
}

func pluginHostChannelIntSlice(raw string) []int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var values []int
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return nil
	}
	return values
}

func pluginHostChannelRelayPins(raw string) []storage.RelayPin {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var pins []storage.RelayPin
	if err := json.Unmarshal([]byte(trimmed), &pins); err != nil {
		return nil
	}
	return pins
}

// pluginHostChannelDialHost picks the address the exit agent dials the entry
// ingress on: the entry agent's last reported address when the agents are not
// co-located, otherwise the loopback.
func pluginHostChannelDialHost(entryRow, exitRow storage.AgentRow) string {
	entryHost := strings.TrimSpace(entryRow.LastSeenIP)
	exitHost := strings.TrimSpace(exitRow.LastSeenIP)
	if entryHost != "" && entryHost != exitHost {
		return entryHost
	}
	return "127.0.0.1"
}

// pluginHostChannelBackendHosts returns exit-side backend hosts in preference
// order. The final entry is the loopback fallback.
func pluginHostChannelBackendHosts(entryRow, exitRow storage.AgentRow) []string {
	exitHost := strings.TrimSpace(exitRow.LastSeenIP)
	entryHost := strings.TrimSpace(entryRow.LastSeenIP)
	if exitHost != "" && exitHost != entryHost {
		return []string{exitHost, "127.0.0.1"}
	}
	return []string{"127.0.0.1"}
}

func pluginHostChannelHostPort(value any) (string, int, error) {
	text, _ := value.(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", 0, errPluginHostInvalid
	}
	host, portText, err := net.SplitHostPort(text)
	if err != nil {
		return "", 0, errPluginHostInvalid
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 || strings.TrimSpace(host) == "" {
		return "", 0, errPluginHostInvalid
	}
	return host, port, nil
}

func pluginHostChannelState(result map[string]any) string {
	if result == nil {
		return ""
	}
	state, _ := result["state"].(string)
	return strings.TrimSpace(state)
}

func pluginHostChannelResult(sessionID string, result map[string]any) map[string]any {
	state := pluginHostChannelState(result)
	if state != pluginsdk.ChannelReverseStateOnline {
		state = pluginsdk.ChannelReverseStateOffline
	}
	output := map[string]any{
		"session_ref": sessionID,
		"state":       state,
	}
	if result != nil {
		if lastError, ok := result["last_error"].(string); ok && strings.TrimSpace(lastError) != "" {
			output["last_error"] = strings.TrimSpace(lastError)
		}
	}
	return output
}

// pluginHostChannelSessionID mints a stable, owner-encoding session
// reference: "channel/<entry>/<exit>". Both identifiers are agent ids, which
// the SDK already constrains to policy-identity characters.
func pluginHostChannelSessionID(entryID, exitID string) string {
	return fmt.Sprintf("channel/%s/%s", strings.TrimSpace(entryID), strings.TrimSpace(exitID))
}

// pluginHostChannelSessionRefOwners decodes a host-minted session ref.
func pluginHostChannelSessionRefOwners(sessionRef string) []string {
	parts := strings.Split(strings.TrimSpace(sessionRef), "/")
	if len(parts) != 3 || parts[0] != "channel" {
		return nil
	}
	owners := make([]string, 0, 2)
	for _, part := range parts[1:] {
		if strings.TrimSpace(part) == "" {
			return nil
		}
		owners = append(owners, strings.TrimSpace(part))
	}
	return owners
}
