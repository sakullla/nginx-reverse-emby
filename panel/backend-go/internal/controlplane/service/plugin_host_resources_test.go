package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginHostOutboundRequiresGrantedExactHostname(t *testing.T) {
	t.Parallel()
	candidate := pluginhost.Candidate{
		Identity:       pluginhost.Identity{Scopes: []string{pluginsdk.PermissionHTTPOutbound}},
		Grants:         []string{pluginsdk.PermissionHTTPOutbound},
		GrantSelectors: map[string][]string{pluginsdk.PermissionHTTPOutbound: {"api.cloudflare.com"}},
	}
	if !pluginCandidateAllowsOutboundHost(candidate, "API.CLOUDFLARE.COM.") {
		t.Fatal("exact granted host was denied")
	}
	if pluginCandidateAllowsOutboundHost(candidate, "example.com") || pluginCandidateAllowsOutboundHost(candidate, "sub.api.cloudflare.com") {
		t.Fatal("ungranted outbound host was accepted")
	}
	candidate.Grants = nil
	if pluginCandidateHasGrant(candidate, pluginsdk.PermissionHTTPOutbound) {
		t.Fatal("declared scope was treated as a durable grant")
	}
}

func TestPluginHostMutationsRequireDurableOperationID(t *testing.T) {
	t.Parallel()
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "secret.put"}) {
		t.Fatal("secret.put did not require an operation id")
	}
	getPayload, _ := json.Marshal(pluginHostHTTPRequest{Method: http.MethodGet})
	if pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "http.secret-request", Payload: getPayload}) {
		t.Fatal("GET unexpectedly required a mutation operation id")
	}
	postPayload, _ := json.Marshal(pluginHostHTTPRequest{Method: http.MethodPost})
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: "http.secret-request", Payload: postPayload}) {
		t.Fatal("POST did not require a durable operation id")
	}
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeHTTPRule}) {
		t.Fatal("http.rule did not require a durable operation id")
	}
	if pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimePluginCall}) {
		t.Fatal("plugin.call unexpectedly required a mutation operation id")
	}
}

func TestPluginHostSecretRevealPreservesMaterialUntilResponseEncoding(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test", Keys: map[string][]byte{"test": key}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginhost.Candidate{
		InstanceID:      "cloudflare-dns-default",
		ResourceGroupID: "default",
		Identity: pluginhost.Identity{
			PluginID:   "cloudflare-dns",
			Generation: "generation-1",
		},
		Grants: []string{"secret.use"},
	}
	ref := "vault/cloudflare/map/test"
	want := []byte("non-zero-cloudflare-token")
	if _, err := vault.Create(t.Context(), secrets.OperationContext{
		ActorID:         "plugin/cloudflare-dns",
		CorrelationID:   candidate.Identity.Generation,
		ResourceGroupID: candidate.ResourceGroupID,
	}, pluginHostSecretName(candidate, ref), pluginHostSecretPurpose, string(want)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(pluginHostSecretPayload{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	response := (&PluginCapabilityManager{secretVault: vault}).dispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation: "secret.reveal",
		Payload:   payload,
	})
	if response.Error != nil {
		t.Fatalf("secret.reveal error = %v", response.Error)
	}
	var result struct {
		Material []byte `json:"material"`
	}
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Material, want) {
		t.Fatalf("revealed material = %q, want %q", result.Material, want)
	}
}

func TestPluginHostPluginCallReturnsExecutionPayloadAsIs(t *testing.T) {
	t.Parallel()
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	session := &pluginCallEchoSession{svc: tasks, agentID: "edge-a"}
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: session}); err != nil {
		t.Fatal(err)
	}
	manager := &PluginCapabilityManager{}
	manager.SetTaskService(tasks)
	manager.setPluginCallLookup(func(context.Context, string, string) (pluginCallTarget, error) {
		return pluginCallTarget{InstanceID: "exec-1", Generation: "generation-1", PluginID: "example.plugin"}, nil
	})
	payload, err := json.Marshal(pluginsdk.PluginCallRequest{
		AgentID: "edge-a",
		Name:    "compose.apply",
		Payload: json.RawMessage(`{"yaml":"services:\n  app:\n    image: example\n"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := manager.DispatchPluginHostResource(t.Context(), pluginCallCandidate(), pluginsdk.HostRuntimeCall{
		Operation: pluginsdk.HostRuntimePluginCall,
		Payload:   payload,
	})
	if response.Error != nil {
		t.Fatalf("plugin.call error = %v", response.Error)
	}
	if string(response.Payload) != `{"yaml":"services:\n  app:\n    image: example\n"}` {
		t.Fatalf("plugin.call payload = %s", response.Payload)
	}
	if session.last.Type != TaskTypePluginCall {
		t.Fatalf("task type = %q", session.last.Type)
	}
	if session.last.Payload["name"] != "compose.apply" {
		t.Fatalf("host inspected or rewrote name: %#v", session.last.Payload["name"])
	}
}

func TestPluginHostPluginCallFailClosedWithoutFallback(t *testing.T) {
	t.Parallel()
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	payload, err := json.Marshal(pluginsdk.PluginCallRequest{AgentID: "edge-a", Name: "engine.report", Payload: json.RawMessage(`{"probe":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := pluginCallCandidate()
	lookup := func(pluginID string) func(context.Context, string, string) (pluginCallTarget, error) {
		return func(_ context.Context, gotPluginID, _ string) (pluginCallTarget, error) {
			if gotPluginID != pluginID {
				t.Fatalf("lookup plugin_id = %q, want %q", gotPluginID, pluginID)
			}
			return pluginCallTarget{}, errPluginHostUnavailable
		}
	}

	offline := &PluginCapabilityManager{}
	offline.SetTaskService(tasks)
	offline.setPluginCallLookup(func(context.Context, string, string) (pluginCallTarget, error) {
		return pluginCallTarget{InstanceID: "exec-1", Generation: "generation-1", PluginID: candidate.Identity.PluginID}, nil
	})
	assertPluginCallFailClosed(t, offline.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimePluginCall, Payload: payload}), pluginsdk.ErrorUnavailable)

	missing := &PluginCapabilityManager{}
	missing.SetTaskService(tasks)
	missing.setPluginCallLookup(lookup(candidate.Identity.PluginID))
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: &pluginCallEchoSession{svc: tasks, agentID: "edge-a"}}); err != nil {
		t.Fatal(err)
	}
	assertPluginCallFailClosed(t, missing.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimePluginCall, Payload: payload}), pluginsdk.ErrorUnavailable)

	mismatched := &PluginCapabilityManager{}
	mismatched.SetTaskService(tasks)
	mismatched.setPluginCallLookup(func(context.Context, string, string) (pluginCallTarget, error) {
		return pluginCallTarget{InstanceID: "exec-other", Generation: "generation-1", PluginID: "other.plugin"}, nil
	})
	assertPluginCallFailClosed(t, mismatched.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimePluginCall, Payload: payload}), pluginsdk.ErrorPermissionDenied)
}

func TestPluginHostHTTPRuleCreateAndEmptyCutover(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	rules := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	manager := &PluginCapabilityManager{store: store}
	manager.SetRuleService(rules)
	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionHTTPRule}

	createPayload, err := json.Marshal(pluginsdk.HTTPRuleRequest{Action: pluginsdk.HTTPRuleActionCreate, AgentID: "edge-a", Domain: "app.example.com", Port: 8096})
	if err != nil {
		t.Fatal(err)
	}
	created := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeHTTPRule,
		OperationID: "http-rule-create-1",
		Payload:     createPayload,
	})
	if created.Error != nil {
		t.Fatalf("http.rule create error = %v", created.Error)
	}
	var createdResult struct {
		RuleRef     string `json:"rule_ref"`
		FrontendURL string `json:"frontend_url"`
		BackendURL  string `json:"backend_url"`
	}
	if err := json.Unmarshal(created.Payload, &createdResult); err != nil {
		t.Fatal(err)
	}
	listed, err := rules.List(t.Context(), "edge-a")
	if err != nil || len(listed) != 1 || listed[0].FrontendURL != "http://app.example.com" {
		t.Fatalf("created rules = %+v err=%v", listed, err)
	}
	if len(listed[0].Backends) != 1 || listed[0].Backends[0].URL != "http://127.0.0.1:8096" || createdResult.BackendURL != "http://127.0.0.1:8096" {
		t.Fatalf("created backend = %+v result=%+v", listed[0].Backends, createdResult)
	}

	emptyCutover, err := json.Marshal(map[string]any{"action": "cutover", "agent_id": "edge-a", "rule_ref": ""})
	if err != nil {
		t.Fatal(err)
	}
	denied := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeHTTPRule,
		OperationID: "http-rule-cutover-empty",
		Payload:     emptyCutover,
	})
	if denied.Error == nil || denied.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("empty rule_ref error = %v", denied.Error)
	}
	unchanged, err := rules.List(t.Context(), "edge-a")
	if err != nil || len(unchanged) != 1 || unchanged[0].ID != listed[0].ID || unchanged[0].FrontendURL != listed[0].FrontendURL {
		t.Fatalf("empty cutover mutated rules = %+v err=%v", unchanged, err)
	}

	unknownCutover, err := json.Marshal(pluginsdk.HTTPRuleRequest{Action: pluginsdk.HTTPRuleActionCutover, AgentID: "edge-a", RuleRef: "404"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeHTTPRule,
		OperationID: "http-rule-cutover-unknown",
		Payload:     unknownCutover,
	})
	if unknown.Error == nil || unknown.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("unknown rule_ref error = %v", unknown.Error)
	}
	still, err := rules.List(t.Context(), "edge-a")
	if err != nil || len(still) != 1 || still[0].ID != listed[0].ID {
		t.Fatalf("unknown cutover mutated rules = %+v err=%v", still, err)
	}
}

func TestPluginHostEventEmitRemainsAuditOnly(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	manager := &PluginCapabilityManager{store: store}
	manager.SetTaskService(tasks)
	candidate := pluginCallCandidate()
	candidate.Grants = []string{"event.emit"}
	payload, err := json.Marshal(pluginHostEventPayload{Action: "compose.apply", Result: "success", Metadata: json.RawMessage(`{"yaml":"services: {}"}`)})
	if err != nil {
		t.Fatal(err)
	}
	response := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeEventEmit, Payload: payload})
	if response.Error != nil {
		t.Fatalf("event.emit error = %v", response.Error)
	}
	events, err := store.ListAuditEvents(t.Context(), 10)
	if err != nil || len(events) == 0 {
		t.Fatalf("audit events = %+v err=%v", events, err)
	}
	found := false
	for _, event := range events {
		if event.Action == "compose.apply" && event.ActorID == "plugin/example.plugin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("audit events = %+v", events)
	}
	if tasks.HasSession("edge-a") {
		t.Fatal("event.emit opened an Agent session")
	}
	rules, err := store.ListHTTPRules(t.Context(), "edge-a")
	if err != nil || len(rules) != 0 {
		t.Fatalf("event.emit created rules = %+v err=%v", rules, err)
	}
}

func pluginCallCandidate() pluginhost.Candidate {
	return pluginhost.Candidate{
		InstanceID:      "control-1",
		ResourceGroupID: "default",
		Identity: pluginhost.Identity{
			PluginID:   "example.plugin",
			Generation: "generation-1",
		},
	}
}

func assertPluginCallFailClosed(t *testing.T, response pluginsdk.HostRuntimeResponse, code pluginsdk.ErrorCode) {
	t.Helper()
	if response.Error == nil || response.Error.Code != code {
		t.Fatalf("plugin.call error = %v, want %v", response.Error, code)
	}
	if len(response.Payload) != 0 {
		t.Fatalf("fail-closed plugin.call returned payload %s", response.Payload)
	}
	if strings.Contains(strings.ToLower(response.Error.Message), "docker") {
		t.Fatalf("plugin.call fell back to docker: %v", response.Error)
	}
}

type pluginCallEchoSession struct {
	svc     *TaskService
	agentID string
	last    TaskEnvelope
}

func (s *pluginCallEchoSession) SendTask(envelope TaskEnvelope) error {
	s.last = envelope
	return s.svc.ApplyUpdate(context.Background(), TaskUpdateInput{
		AgentID: s.agentID,
		TaskID:  envelope.ID,
		State:   "completed",
		Result:  map[string]any{"payload": envelope.Payload["payload"]},
	})
}

func (s *pluginCallEchoSession) Close() error { return nil }
