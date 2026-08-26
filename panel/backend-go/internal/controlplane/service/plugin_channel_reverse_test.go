//go:build exhaustive && !integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	mu          sync.Mutex
	results     map[string]map[string]any
	failWith    error
	failFor     map[string]error
	blockFor    map[string]struct{}
	started     chan struct{}
	startedOnce sync.Once
	tasks       []recordedAgentTask
}

func (f *fakeChannelTaskDispatcher) DispatchAgentTask(ctx context.Context, agentID, taskType string, payload map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	f.mu.Lock()
	f.tasks = append(f.tasks, recordedAgentTask{agentID: agentID, taskType: taskType, payload: payload})
	key := agentID + "/" + taskType
	fail := f.failWith
	if per, ok := f.failFor[key]; ok {
		fail = per
	}
	_, block := f.blockFor[key]
	result, ok := f.results[key]
	started := f.started
	f.mu.Unlock()
	if started != nil {
		f.startedOnce.Do(func() { close(started) })
	}
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fail != nil {
		return nil, fail
	}
	if ok {
		return result, nil
	}
	return map[string]any{"session_id": payload["session_id"], "state": pluginsdk.ChannelReverseStateOffline, "last_error": "no session"}, nil
}

func (f *fakeChannelTaskDispatcher) dispatchedTo(agentID, taskType string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var payloads []map[string]any
	for _, task := range f.tasks {
		if task.agentID == agentID && task.taskType == taskType {
			payloads = append(payloads, task.payload)
		}
	}
	return payloads
}

func (f *fakeChannelTaskDispatcher) taskCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
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
	return dispatchChannelReverseContext(t, t.Context(), manager, operationID, request)
}

func dispatchChannelReverseContext(t *testing.T, ctx context.Context, manager *PluginCapabilityManager, operationID string, request pluginsdk.ChannelReverseRequest) pluginsdk.HostRuntimeResponse {
	t.Helper()
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return manager.DispatchPluginHostResource(ctx, channelReverseCandidate(), pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeChannelReverse,
		OperationID: operationID,
		Payload:     payload,
	})
}

func decodeChannelReverseResult(t *testing.T, response pluginsdk.HostRuntimeResponse) struct {
	SessionRef string `json:"session_ref"`
	State      string `json:"state"`
	BridgeHost string `json:"bridge_host"`
	BridgePort int    `json:"bridge_port"`
	LastError  string `json:"last_error"`
} {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("channel.reverse error = %v", response.Error)
	}
	var result struct {
		SessionRef string `json:"session_ref"`
		State      string `json:"state"`
		BridgeHost string `json:"bridge_host"`
		BridgePort int    `json:"bridge_port"`
		LastError  string `json:"last_error"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
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
	// Co-located agents (no last-seen addresses) dial and bind the loopback.
	if entryTasks[0]["listen_host"] != "127.0.0.1" {
		t.Fatalf("co-located entry listen_host = %v", entryTasks[0]["listen_host"])
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

func TestPluginHostChannelReverseCrossNetworkDialsEntryAndBindsReachableIngress(t *testing.T) {
	t.Parallel()
	manager, sessions, store := newChannelReverseManager(t)
	for _, agent := range []storage.AgentRow{
		{ID: "entry-a", Name: "entry", LastSeenIP: "203.0.113.10"},
		{ID: "exit-b", Name: "exit", LastSeenIP: "198.51.100.7"},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}

	response := dispatchChannelReverse(t, manager, "channel-cross-1", pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "10.9.8.7", BackendPort: 9000,
	})
	if response.Error != nil {
		t.Fatalf("cross-network ensure error = %v", response.Error)
	}

	entryTasks := sessions.dispatchedTo("entry-a", TaskTypeChannelEnsure)
	if len(entryTasks) != 1 {
		t.Fatalf("entry ensure tasks = %+v", entryTasks)
	}
	// A non-loopback dial must not leave the ingress bound to the loopback:
	// the entry agent is asked to bind all interfaces.
	if listenHost, _ := entryTasks[0]["listen_host"].(string); listenHost != "" {
		t.Fatalf("cross-network entry listen_host = %q, want wildcard binding", listenHost)
	}
	exitTasks := sessions.dispatchedTo("exit-b", TaskTypeChannelEnsure)
	if len(exitTasks) != 1 {
		t.Fatalf("exit ensure tasks = %+v", exitTasks)
	}
	if exitTasks[0]["dial_address"] != "203.0.113.10:5000" {
		t.Fatalf("cross-network dial address = %v", exitTasks[0]["dial_address"])
	}
	// The explicit backend host is authoritative over the exit address
	// heuristics.
	if exitTasks[0]["backend_address"] != "10.9.8.7:9000" {
		t.Fatalf("cross-network backend address = %v", exitTasks[0]["backend_address"])
	}
}

func TestPluginHostChannelReverseRejectsUnaddressableSessionRef(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)

	for _, ref := range []string{"session-1", "channel/entry-a/other-exit"} {
		response := dispatchChannelReverse(t, manager, "channel-ref-1", pluginsdk.ChannelReverseRequest{
			Action: pluginsdk.ChannelReverseActionEnsure, SessionRef: ref,
			EntryAgentID: "entry-a", ExitAgentID: "exit-b",
			Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
		})
		if response.Error == nil || response.Error.Code != pluginsdk.ErrorInvalidArgument {
			t.Fatalf("custom ref %q ensure error = %v", ref, response.Error)
		}
	}
	if len(sessions.tasks) != 0 {
		t.Fatalf("rejected session refs still dispatched tasks: %+v", sessions.tasks)
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

	statusWithoutOperation := dispatchChannelReverse(t, manager, "", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: "channel/entry-a/exit-b",
	})
	if statusWithoutOperation.Error != nil {
		t.Fatalf("status without operation id error = %v", statusWithoutOperation.Error)
	}

	teardownWithoutOperation := dispatchChannelReverse(t, manager, "", pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionTeardown, SessionRef: "channel/entry-a/exit-b",
	})
	if teardownWithoutOperation.Error == nil || teardownWithoutOperation.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("teardown without operation id error = %v", teardownWithoutOperation.Error)
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

func TestPluginHostChannelReverseStatusSynthesizesBothEnds(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)
	sessionRef := "channel/entry-a/exit-b"

	for _, tc := range []struct {
		name           string
		results        map[string]map[string]any
		wantState      string
		wantBridgeHost string
		wantBridgePort int
	}{
		{
			name: "both online uses entry bridge",
			results: map[string]map[string]any{
				"entry-a/" + TaskTypeChannelStatus: {
					"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
					"bridge_address": "127.0.0.1:6000",
				},
				"exit-b/" + TaskTypeChannelStatus: {
					"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
					"bridge_address": "10.0.0.1:1",
				},
			},
			wantState: pluginsdk.ChannelReverseStateOnline, wantBridgeHost: "127.0.0.1", wantBridgePort: 6000,
		},
		{
			name: "entry missing is offline",
			results: map[string]map[string]any{
				"exit-b/" + TaskTypeChannelStatus: {
					"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
					"bridge_address": "10.0.0.1:1",
				},
			},
			wantState: pluginsdk.ChannelReverseStateOffline,
		},
		{
			name: "exit missing is offline and keeps entry bridge",
			results: map[string]map[string]any{
				"entry-a/" + TaskTypeChannelStatus: {
					"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
					"bridge_address": "127.0.0.1:6000",
				},
			},
			wantState: pluginsdk.ChannelReverseStateOffline, wantBridgeHost: "127.0.0.1", wantBridgePort: 6000,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions.mu.Lock()
			sessions.results = tc.results
			sessions.mu.Unlock()
			result := decodeChannelReverseResult(t, dispatchChannelReverse(t, manager, "", pluginsdk.ChannelReverseRequest{
				Action: pluginsdk.ChannelReverseActionStatus, SessionRef: sessionRef,
			}))
			if result.State != tc.wantState {
				t.Fatalf("state = %q, want %q", result.State, tc.wantState)
			}
			if result.BridgeHost != tc.wantBridgeHost || result.BridgePort != tc.wantBridgePort {
				t.Fatalf("bridge = %s:%d, want %s:%d", result.BridgeHost, result.BridgePort, tc.wantBridgeHost, tc.wantBridgePort)
			}
			if result.SessionRef != sessionRef {
				t.Fatalf("session_ref = %q", result.SessionRef)
			}
		})
	}
}

func TestPluginHostChannelReverseStatusErrorsAndCancel(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)
	sessionRef := "channel/entry-a/exit-b"
	request := pluginsdk.ChannelReverseRequest{Action: pluginsdk.ChannelReverseActionStatus, SessionRef: sessionRef}

	sessions.mu.Lock()
	sessions.results = map[string]map[string]any{
		"entry-a/" + TaskTypeChannelStatus: {
			"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
			"bridge_address": "127.0.0.1:6000",
		},
		"exit-b/" + TaskTypeChannelStatus: {
			"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
		},
	}
	sessions.failFor = map[string]error{"entry-a/" + TaskTypeChannelStatus: errors.New("entry lookup failed")}
	sessions.mu.Unlock()
	entryFailed := dispatchChannelReverse(t, manager, "", request)
	if entryFailed.Error == nil {
		t.Fatal("entry lookup error returned success")
	}

	sessions.mu.Lock()
	sessions.failFor = map[string]error{"exit-b/" + TaskTypeChannelStatus: errors.New("exit lookup failed")}
	sessions.mu.Unlock()
	exitFailed := dispatchChannelReverse(t, manager, "", request)
	if exitFailed.Error == nil {
		t.Fatal("exit lookup error returned success")
	}

	sessions.mu.Lock()
	sessions.failFor = nil
	sessions.blockFor = map[string]struct{}{
		"entry-a/" + TaskTypeChannelStatus: {},
		"exit-b/" + TaskTypeChannelStatus:  {},
	}
	sessions.started = make(chan struct{})
	sessions.startedOnce = sync.Once{}
	sessions.mu.Unlock()

	canceled, cancel := context.WithCancel(context.Background())
	done := make(chan pluginsdk.HostRuntimeResponse, 1)
	go func() {
		done <- dispatchChannelReverseContext(t, canceled, manager, "", request)
	}()
	select {
	case <-sessions.started:
	case <-time.After(2 * time.Second):
		t.Fatal("status lookup did not start waiting")
	}
	cancel()
	select {
	case response := <-done:
		if response.Error == nil {
			t.Fatal("canceled status lookup returned success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled status lookup did not stop waiting")
	}

	deadline, cancelDeadline := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelDeadline()
	timedOut := dispatchChannelReverseContext(t, deadline, manager, "", request)
	if timedOut.Error == nil {
		t.Fatal("deadline status lookup returned success")
	}
}

func TestPluginHostChannelReverseStatusSkipsMutationOutcome(t *testing.T) {
	t.Parallel()
	manager, sessions, store := newChannelReverseManager(t)
	sessionRef := "channel/entry-a/exit-b"
	operationID := "channel-status-cached-1"
	sessions.mu.Lock()
	sessions.results = map[string]map[string]any{
		"entry-a/" + TaskTypeChannelStatus: {
			"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
			"bridge_address": "127.0.0.1:6000",
		},
		"exit-b/" + TaskTypeChannelStatus: {
			"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
		},
	}
	sessions.mu.Unlock()

	first := decodeChannelReverseResult(t, dispatchChannelReverse(t, manager, operationID, pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: sessionRef,
	}))
	if first.State != pluginsdk.ChannelReverseStateOnline {
		t.Fatalf("first status = %+v", first)
	}

	sessions.mu.Lock()
	sessions.results = map[string]map[string]any{
		"entry-a/" + TaskTypeChannelStatus: {
			"session_id": sessionRef, "state": pluginsdk.ChannelReverseStateOnline,
			"bridge_address": "127.0.0.1:6000",
		},
	}
	sessions.mu.Unlock()
	second := decodeChannelReverseResult(t, dispatchChannelReverse(t, manager, operationID, pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionStatus, SessionRef: sessionRef,
	}))
	if second.State != pluginsdk.ChannelReverseStateOffline {
		t.Fatalf("repeated status reused a mutation outcome: %+v", second)
	}

	record, found, err := store.GetIdempotencyRecord(t.Context(), pluginHostOperationScope, pluginHostOperationKey(channelReverseCandidate(), operationID))
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatalf("status lookup stored mutation outcome: %+v", record)
	}
}

func TestPluginHostChannelReverseMutationsReplayOperation(t *testing.T) {
	t.Parallel()
	manager, sessions, _ := newChannelReverseManager(t)
	ensure := pluginsdk.ChannelReverseRequest{
		Action:       pluginsdk.ChannelReverseActionEnsure,
		EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	}
	first := decodeChannelReverseResult(t, dispatchChannelReverse(t, manager, "channel-ensure-replay", ensure))
	if first.State != pluginsdk.ChannelReverseStateOnline {
		t.Fatalf("ensure result = %+v", first)
	}
	afterEnsure := sessions.taskCount()
	replayed := decodeChannelReverseResult(t, dispatchChannelReverse(t, manager, "channel-ensure-replay", ensure))
	if replayed != first {
		t.Fatalf("replayed ensure = %+v, want %+v", replayed, first)
	}
	if sessions.taskCount() != afterEnsure {
		t.Fatalf("ensure replay dispatched extra tasks: %d -> %d", afterEnsure, sessions.taskCount())
	}

	teardown := pluginsdk.ChannelReverseRequest{Action: pluginsdk.ChannelReverseActionTeardown, SessionRef: first.SessionRef}
	tornDown := dispatchChannelReverse(t, manager, "channel-teardown-replay", teardown)
	if tornDown.Error != nil {
		t.Fatalf("teardown error = %v", tornDown.Error)
	}
	afterTeardown := sessions.taskCount()
	replayedTeardown := dispatchChannelReverse(t, manager, "channel-teardown-replay", teardown)
	if replayedTeardown.Error != nil {
		t.Fatalf("replayed teardown error = %v", replayedTeardown.Error)
	}
	if sessions.taskCount() != afterTeardown {
		t.Fatalf("teardown replay dispatched extra tasks: %d -> %d", afterTeardown, sessions.taskCount())
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
		Action: pluginsdk.ChannelReverseActionEnsure, SessionRef: "channel/entry-a/exit-b",
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
