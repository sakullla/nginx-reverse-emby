package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type recordedAgentTask struct {
	agentID  string
	taskType string
	payload  map[string]any
}

type fakeChannelTaskDispatcher struct {
	results  map[string]map[string]any
	failWith error
	tasks    []recordedAgentTask
}

func (f *fakeChannelTaskDispatcher) DispatchAgentTask(_ context.Context, agentID, taskType string, payload map[string]any) (map[string]any, error) {
	f.tasks = append(f.tasks, recordedAgentTask{agentID: agentID, taskType: taskType, payload: payload})
	if f.failWith != nil {
		return nil, f.failWith
	}
	if result, ok := f.results[agentID+"/"+taskType]; ok {
		return result, nil
	}
	return map[string]any{"session_id": payload["session_id"], "state": pluginsdk.ChannelReverseStateOffline, "last_error": "no session"}, nil
}

func (f *fakeChannelTaskDispatcher) dispatchedTo(agentID, taskType string) []map[string]any {
	var payloads []map[string]any
	for _, task := range f.tasks {
		if task.agentID == agentID && task.taskType == taskType {
			payloads = append(payloads, task.payload)
		}
	}
	return payloads
}

func newChannelReverseManager(t *testing.T) (*PluginCapabilityManager, *fakeChannelTaskDispatcher, *storage.GormStore) {
	t.Helper()
	store := newServiceOwnerStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "entry-a", Name: "entry"},
		{ID: "exit-b", Name: "exit"},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}
	manager := &PluginCapabilityManager{store: store}
	sessions := &fakeChannelTaskDispatcher{results: map[string]map[string]any{
		"entry-a/" + TaskTypeChannelEnsure: {"session_id": "session-1", "state": pluginsdk.ChannelReverseStateOnline,
			"ingress_address": "127.0.0.1:5000", "bridge_address": "127.0.0.1:6000"},
		"exit-b/" + TaskTypeChannelEnsure: {"session_id": "session-1", "state": pluginsdk.ChannelReverseStateOnline},
	}}
	manager.SetChannelTaskDispatcher(sessions)
	return manager, sessions, store
}

func channelReverseCandidate() pluginhost.Candidate {
	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionChannelReverse}
	return candidate
}

func dispatchChannelReverse(t *testing.T, manager *PluginCapabilityManager, operationID string, request pluginsdk.ChannelReverseRequest) pluginsdk.HostRuntimeResponse {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return manager.DispatchPluginHostResource(t.Context(), channelReverseCandidate(), pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeChannelReverse,
		OperationID: operationID,
		Payload:     payload,
	})
}

func TestPluginHostChannelReverseEnsureAppliesBothRoles(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)

	response := dispatchChannelReverse(t, manager, "channel-ensure-1", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if response.Error != nil {
		t.Fatalf("channel.reverse ensure error = %v", response.Error)
	}
	var result struct {
		SessionRef string `json:"session_ref"`
		State      string `json:"state"`
		BridgeHost string `json:"bridge_host"`
		BridgePort int    `json:"bridge_port"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionRef == "" || result.State != pluginsdk.ChannelReverseStateOnline {
		t.Fatalf("ensure result = %+v", result)
	}
	if result.BridgeHost != "127.0.0.1" || result.BridgePort != 6000 {
		t.Fatalf("bridge endpoint = %s:%d", result.BridgeHost, result.BridgePort)
	}

	entryTasks := sessions.dispatchedTo("entry-a", TaskTypeChannelEnsure)
	if len(entryTasks) != 1 || entryTasks[0]["role"] != "entry" || entryTasks[0]["session_id"] != result.SessionRef ||
		entryTasks[0]["exit_agent_id"] != "exit-b" || entryTasks[0]["protocol"] != "tcp" {
		t.Fatalf("entry ensure tasks = %+v", entryTasks)
	}
	exitTasks := sessions.dispatchedTo("exit-b", TaskTypeChannelEnsure)
	if len(exitTasks) != 1 || exitTasks[0]["role"] != "exit" {
		t.Fatalf("exit ensure tasks = %+v", exitTasks)
	}
	if exitTasks[0]["dial_address"] != "127.0.0.1:5000" || exitTasks[0]["backend_address"] != "127.0.0.1:9000" {
		t.Fatalf("exit ensure payload = %+v", exitTasks[0])
	}
}

func TestPluginHostChannelReverseEnsureWithoutSessionRefMintsOwnerEncoding(t *testing.T) {
	t.Parallel()
	manager, _, _ := newChannelReverseManager(t)

	response := dispatchChannelReverse(t, manager, "channel-ensure-2", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolUDP, BackendHost: "127.0.0.1", BackendPort: 5353,
	})
	if response.Error != nil {
		t.Fatalf("channel.reverse ensure error = %v", response.Error)
	}
	var result struct {
		SessionRef string `json:"session_ref"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionRef != "channel/entry-a/exit-b" {
		t.Fatalf("minted session ref = %q", result.SessionRef)
	}
}

func TestPluginHostChannelReverseTeardownAndStatusUseSessionOwners(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)

	status := dispatchChannelReverse(t, manager, "channel-status-1", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: "channel/entry-a/exit-b",
	})
	if status.Error != nil {
		t.Fatalf("channel.reverse status error = %v", status.Error)
	}
	if len(sessions.dispatchedTo("entry-a", TaskTypeChannelStatus)) != 1 ||
		len(sessions.dispatchedTo("exit-b", TaskTypeChannelStatus)) != 1 {
		t.Fatalf("status did not reach both agents: %+v", sessions.tasks)
	}

	teardown := dispatchChannelReverse(t, manager, "channel-teardown-1", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionTeardown, SessionRef: "channel/entry-a/exit-b",
	})
	if teardown.Error != nil {
		t.Fatalf("channel.reverse teardown error = %v", teardown.Error)
	}
	if len(sessions.dispatchedTo("entry-a", TaskTypeChannelTeardown)) != 1 ||
		len(sessions.dispatchedTo("exit-b", TaskTypeChannelTeardown)) != 1 {
		t.Fatalf("teardown did not reach both agents: %+v", sessions.tasks)
	}

	foreign := dispatchChannelReverse(t, manager, "channel-teardown-2", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionTeardown, SessionRef: "someone/elses/session",
	})
	if foreign.Error == nil || foreign.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("foreign session teardown error = %v", foreign.Error)
	}
}

func TestPluginHostChannelReverseRequiresGrantOperationAndDispatcher(t *testing.T) {
	t.Parallel()
	manager, _, _ := newChannelReverseManager(t)

	ungranted := pluginCallCandidate()
	payload, err := json.Marshal(pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if err != nil {
		t.Fatal(err)
	}
	denied := manager.DispatchPluginHostResource(t.Context(), ungranted, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeChannelReverse,
		OperationID: "channel-deny-1",
		Payload:     payload,
	})
	if denied.Error == nil || denied.Error.Code != pluginsdk.ErrorPermissionDenied {
		t.Fatalf("ungranted channel.reverse error = %v", denied.Error)
	}

	missingOperation := manager.DispatchPluginHostResource(t.Context(), channelReverseCandidate(), pluginsdk.HostRuntimeCall{
		Operation: pluginsdk.HostRuntimeChannelReverse,
		Payload:   payload,
	})
	if missingOperation.Error == nil || missingOperation.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("channel.reverse without operation id error = %v", missingOperation.Error)
	}

	unknownAgent := dispatchChannelReverse(t, manager, "channel-deny-2", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "ghost-agent",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if unknownAgent.Error == nil || unknownAgent.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("unknown exit agent error = %v", unknownAgent.Error)
	}

	unbound := dispatchChannelReverse(t, manager, "channel-deny-3", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: "channel/entry-a/ghost-agent",
	})
	if unbound.Error == nil || unbound.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("status for unknown owner error = %v", unbound.Error)
	}

	unwired := &PluginCapabilityManager{store: newServiceOwnerStore(t)}
	unboundResponse := dispatchChannelReverse(t, unwired, "channel-deny-4", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: "channel/entry-a/exit-b",
	})
	if unboundResponse.Error == nil || unboundResponse.Error.Code != pluginsdk.ErrorUnavailable {
		t.Fatalf("unbound dispatcher error = %v", unboundResponse.Error)
	}
}

func TestPluginHostChannelReverseReportsOfflineStateAndLastError(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)
	sessions.results = map[string]map[string]any{
		"entry-a/" + TaskTypeChannelEnsure: {"session_id": "session-1", "state": pluginsdk.ChannelReverseStateOnline,
			"ingress_address": "127.0.0.1:5000", "bridge_address": "127.0.0.1:6000"},
		"exit-b/" + TaskTypeChannelEnsure: {"session_id": "session-1", "state": pluginsdk.ChannelReverseStateOffline, "last_error": "dial refused"},
	}

	response := dispatchChannelReverse(t, manager, "channel-offline-1", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionEnsure, SessionRef: "session-1",
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if response.Error != nil {
		t.Fatalf("offline ensure error = %v", response.Error)
	}
	var result struct {
		State     string `json:"state"`
		LastError string `json:"last_error"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != pluginsdk.ChannelReverseStateOffline || !strings.Contains(result.LastError, "dial refused") {
		t.Fatalf("offline result = %+v", result)
	}
}

func TestPluginHostChannelReverseRelayChainProjectsListeners(t *testing.T) {
	t.Parallel()
	manager, sessions, store := newChannelReverseManager(t)
	if err := store.SaveRelayListeners(t.Context(), "relay-agent", []storage.RelayListenerRow{{
		ID: 71, AgentID: "relay-agent", Name: "hop",
		ListenHost: "10.0.0.9", ListenPort: 4443, PublicHost: "relay.example.test", PublicPort: 4443,
		Enabled: true, TLSMode: "pki_mtls", TransportMode: "tls_tcp",
	}}); err != nil {
		t.Fatal(err)
	}
	manager.SetChannelListenerProjector(func(_ context.Context, _ string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
		for index := range listeners {
			listeners[index].PKIIdentityID = "identity-71"
			listeners[index].PKIIdentityState = "active"
		}
		return listeners, nil
	})

	response := dispatchChannelReverse(t, manager, "channel-relay-1", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
		RelayChain: []int{71},
	})
	if response.Error != nil {
		t.Fatalf("channel.reverse relay ensure error = %v", response.Error)
	}
	exitTasks := sessions.dispatchedTo("exit-b", TaskTypeChannelEnsure)
	if len(exitTasks) != 1 {
		t.Fatalf("exit ensure tasks = %+v", exitTasks)
	}
	chain, _ := exitTasks[0]["relay_chain"].([]map[string]any)
	// The payload round-trips through JSON on the agent wire, so decode it.
	encoded, err := json.Marshal(exitTasks[0]["relay_chain"])
	if err != nil {
		t.Fatal(err)
	}
	var hops []struct {
		Address    string `json:"address"`
		ServerName string `json:"server_name"`
		Listener   struct {
			ID               int    `json:"id"`
			AgentID          string `json:"agent_id"`
			TLSMode          string `json:"tls_mode"`
			PKIIdentityID    string `json:"pki_identity_id"`
			PKIIdentityState string `json:"pki_identity_state"`
		} `json:"listener"`
	}
	if err := json.Unmarshal(encoded, &hops); err != nil {
		t.Fatal(err)
	}
	if len(hops) != 1 || hops[0].Address != "relay.example.test:4443" || hops[0].ServerName != "relay.example.test" {
		t.Fatalf("relay hops = %+v (raw %v)", hops, chain)
	}
	if hops[0].Listener.ID != 71 || hops[0].Listener.AgentID != "relay-agent" || hops[0].Listener.TLSMode != "pki_mtls" ||
		hops[0].Listener.PKIIdentityID != "identity-71" || hops[0].Listener.PKIIdentityState != "active" {
		t.Fatalf("projected listener = %+v", hops[0].Listener)
	}
}

func TestPluginHostChannelReverseRejectsUnknownRelayListener(t *testing.T) {
	t.Parallel()
	manager, _, _ := newChannelReverseManager(t)
	response := dispatchChannelReverse(t, manager, "channel-relay-2", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
		RelayChain: []int{999},
	})
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("unknown relay listener error = %v", response.Error)
	}
}

func TestPluginHostChannelReverseSurfacesSessionUnavailable(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "entry-a", Name: "entry"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "exit-b", Name: "exit"}); err != nil {
		t.Fatal(err)
	}
	manager := &PluginCapabilityManager{store: store}
	manager.SetChannelTaskDispatcher(&fakeChannelTaskDispatcher{failWith: errTaskSessionUnavailable})
	response := dispatchChannelReverse(t, manager, "channel-unavailable-1", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorUnavailable {
		t.Fatalf("session unavailable error = %v", response.Error)
	}
}
