package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeL4Rule}) {
		t.Fatal("l4.rule did not require a durable operation id")
	}
	if pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimePluginCall}) {
		t.Fatal("plugin.call unexpectedly required a mutation operation id")
	}
	statusPayload, _ := json.Marshal(pluginsdk.ChannelReverseRequest{Action: pluginsdk.ChannelReverseActionStatus, SessionRef: "channel/entry-a/exit-b"})
	if pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeChannelReverse, Payload: statusPayload}) {
		t.Fatal("channel.reverse status unexpectedly required a mutation operation id")
	}
	ensurePayload, _ := json.Marshal(pluginsdk.ChannelReverseRequest{
		Action: pluginsdk.ChannelReverseActionEnsure, EntryAgentID: "entry-a", ExitAgentID: "exit-b",
		Protocol: pluginsdk.L4RuleProtocolTCP, BackendHost: "127.0.0.1", BackendPort: 9000,
	})
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeChannelReverse, Payload: ensurePayload}) {
		t.Fatal("channel.reverse ensure did not require a durable operation id")
	}
	teardownPayload, _ := json.Marshal(pluginsdk.ChannelReverseRequest{Action: pluginsdk.ChannelReverseActionTeardown, SessionRef: "channel/entry-a/exit-b"})
	if !pluginHostCallRequiresOperation(pluginsdk.HostRuntimeCall{Operation: pluginsdk.HostRuntimeChannelReverse, Payload: teardownPayload}) {
		t.Fatal("channel.reverse teardown did not require a durable operation id")
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

func TestPluginHostRuntimeServicesUnboundUntilSetters(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	manager := &PluginCapabilityManager{store: store}
	tasksBound, rulesBound := manager.HostRuntimeServicesBound()
	if tasksBound || rulesBound {
		t.Fatal("production composition left TaskService/RuleService bound before setters")
	}
	lookup := func(context.Context, string, string) (pluginCallTarget, error) {
		return pluginCallTarget{InstanceID: "exec-1", Generation: "generation-1", PluginID: "example.plugin"}, nil
	}
	manager.setPluginCallLookup(lookup)
	callPayload, err := json.Marshal(pluginsdk.PluginCallRequest{AgentID: "edge-a", Name: "engine.report", Payload: json.RawMessage(`{"probe":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginCallFailClosed(t, manager.DispatchPluginHostResource(t.Context(), pluginCallCandidate(), pluginsdk.HostRuntimeCall{
		Operation: pluginsdk.HostRuntimePluginCall,
		Payload:   callPayload,
	}), pluginsdk.ErrorUnavailable)

	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionHTTPRule}
	rulePayload, err := json.Marshal(pluginsdk.HTTPRuleRequest{Action: pluginsdk.HTTPRuleActionCreate, AgentID: "edge-a", Domain: "app.example.com", Port: 8096})
	if err != nil {
		t.Fatal(err)
	}
	denied := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeHTTPRule,
		OperationID: "http-rule-unbound",
		Payload:     rulePayload,
	})
	if denied.Error == nil || denied.Error.Code != pluginsdk.ErrorUnavailable {
		t.Fatalf("unbound http.rule error = %v", denied.Error)
	}

	tasks := NewTaskService(TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	if err := tasks.RegisterSession(TaskSessionRegistration{AgentID: "edge-a", Session: &pluginCallEchoSession{svc: tasks, agentID: "edge-a"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "edge-a", Name: "edge-a"}); err != nil {
		t.Fatal(err)
	}
	rules := NewRuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	manager.SetTaskService(tasks)
	manager.SetRuleService(rules)
	tasksBound, rulesBound = manager.HostRuntimeServicesBound()
	if !tasksBound || !rulesBound {
		t.Fatal("setters left TaskService/RuleService unbound")
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

func TestPluginHostL4RuleLifecycleCarriesPluginAttribution(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	rules := NewL4RuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	manager := &PluginCapabilityManager{store: store}
	manager.SetL4RuleService(rules)
	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionL4Rule}

	createPayload, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionCreate, AgentID: "local", Name: "edge-tcp", Protocol: pluginsdk.L4RuleProtocolTCP,
		ListenPort: 9000, Backends: []pluginsdk.L4RuleBackend{{Host: "127.0.0.1", Port: 9001}},
		Tags: []string{"team-edge", "plugin", "plugin:other.plugin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-create-1",
		Payload:     createPayload,
	})
	if created.Error != nil {
		t.Fatalf("l4.rule create error = %v", created.Error)
	}
	var createdResult struct {
		RuleRef string `json:"rule_ref"`
		AgentID string `json:"agent_id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(created.Payload, &createdResult); err != nil {
		t.Fatal(err)
	}
	if createdResult.RuleRef == "" || createdResult.AgentID != "local" || !createdResult.Enabled {
		t.Fatalf("l4.rule create result = %+v", createdResult)
	}
	listed, err := rules.List(t.Context(), "local")
	if err != nil || len(listed) != 1 {
		t.Fatalf("created rules = %+v err=%v", listed, err)
	}
	rule := listed[0]
	for _, want := range []string{"team-edge", "plugin", "plugin:example.plugin", "plugin-instance:control-1"} {
		if !slicesContains(rule.Tags, want) {
			t.Fatalf("created rule tags = %v, missing %q", rule.Tags, want)
		}
	}
	if slicesContains(rule.Tags, "plugin:other.plugin") {
		t.Fatalf("spoofed attribution tag survived: %v", rule.Tags)
	}
	binding, err := store.GetResourceBinding(t.Context(), "l4_rule", "local:"+createdResult.RuleRef)
	if err != nil || strings.TrimSpace(binding.ResourceGroupID) == "" {
		t.Fatalf("created rule resource binding = %+v err=%v", binding, err)
	}

	updatePayload, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionUpdate, AgentID: "local", RuleRef: createdResult.RuleRef,
		Name: "edge-tcp-renamed", Tags: []string{"team-core"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-update-1",
		Payload:     updatePayload,
	})
	if updated.Error != nil {
		t.Fatalf("l4.rule update error = %v", updated.Error)
	}
	rule, err = rules.Get(t.Context(), "local", rule.ID)
	if err != nil || rule.Name != "edge-tcp-renamed" || rule.ListenPort != 9000 {
		t.Fatalf("updated rule = %+v err=%v", rule, err)
	}
	if !slicesContains(rule.Tags, "team-core") || !slicesContains(rule.Tags, "plugin:example.plugin") || slicesContains(rule.Tags, "team-edge") {
		t.Fatalf("updated rule tags = %v", rule.Tags)
	}

	for action, wantEnabled := range map[string]bool{pluginsdk.L4RuleActionDisable: false, pluginsdk.L4RuleActionEnable: true} {
		togglePayload, err := json.Marshal(pluginsdk.L4RuleRequest{Action: action, AgentID: "local", RuleRef: createdResult.RuleRef})
		if err != nil {
			t.Fatal(err)
		}
		toggled := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
			Operation:   pluginsdk.HostRuntimeL4Rule,
			OperationID: "l4-rule-" + action + "-1",
			Payload:     togglePayload,
		})
		if toggled.Error != nil {
			t.Fatalf("l4.rule %s error = %v", action, toggled.Error)
		}
		rule, err = rules.Get(t.Context(), "local", rule.ID)
		if err != nil || rule.Enabled != wantEnabled {
			t.Fatalf("l4.rule %s left rule = %+v err=%v", action, rule, err)
		}
	}

	deletePayload, err := json.Marshal(pluginsdk.L4RuleRequest{Action: pluginsdk.L4RuleActionDelete, AgentID: "local", RuleRef: createdResult.RuleRef})
	if err != nil {
		t.Fatal(err)
	}
	deleted := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-delete-1",
		Payload:     deletePayload,
	})
	if deleted.Error != nil {
		t.Fatalf("l4.rule delete error = %v", deleted.Error)
	}
	remaining, err := rules.List(t.Context(), "local")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("delete left rules = %+v err=%v", remaining, err)
	}
	repeated := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-delete-2",
		Payload:     deletePayload,
	})
	if repeated.Error == nil || repeated.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("repeated delete error = %v", repeated.Error)
	}
}

func TestPluginHostL4RuleRequiresGrantOwnershipAndBoundService(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	rules := NewL4RuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	manager := &PluginCapabilityManager{store: store}
	manager.SetL4RuleService(rules)
	candidate := pluginCallCandidate()
	candidate.Grants = []string{pluginsdk.PermissionL4Rule}

	createPayload, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionCreate, AgentID: "local", Protocol: pluginsdk.L4RuleProtocolTCP,
		ListenPort: 9100, Backends: []pluginsdk.L4RuleBackend{{Host: "127.0.0.1", Port: 9101}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ungranted := pluginCallCandidate()
	denied := manager.DispatchPluginHostResource(t.Context(), ungranted, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-denied-1",
		Payload:     createPayload,
	})
	if denied.Error == nil || denied.Error.Code != pluginsdk.ErrorPermissionDenied {
		t.Fatalf("ungranted l4.rule error = %v", denied.Error)
	}
	if listed, err := rules.List(t.Context(), "local"); err != nil || len(listed) != 0 {
		t.Fatalf("ungranted l4.rule created rules = %+v err=%v", listed, err)
	}

	created := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-create-owned",
		Payload:     createPayload,
	})
	if created.Error != nil {
		t.Fatalf("l4.rule create error = %v", created.Error)
	}
	var createdResult struct {
		RuleRef string `json:"rule_ref"`
	}
	if err := json.Unmarshal(created.Payload, &createdResult); err != nil {
		t.Fatal(err)
	}

	other := pluginhost.Candidate{
		InstanceID:      "control-9",
		ResourceGroupID: "default",
		Identity:        pluginhost.Identity{PluginID: "other.plugin", Generation: "generation-1"},
		Grants:          []string{pluginsdk.PermissionL4Rule},
	}
	disablePayload, err := json.Marshal(pluginsdk.L4RuleRequest{Action: pluginsdk.L4RuleActionDisable, AgentID: "local", RuleRef: createdResult.RuleRef})
	if err != nil {
		t.Fatal(err)
	}
	crossPlugin := manager.DispatchPluginHostResource(t.Context(), other, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-cross-plugin-1",
		Payload:     disablePayload,
	})
	if crossPlugin.Error == nil || crossPlugin.Error.Code != pluginsdk.ErrorPermissionDenied {
		t.Fatalf("cross-plugin l4.rule error = %v", crossPlugin.Error)
	}
	rule, err := rules.Get(t.Context(), "local", 1)
	if err == nil && !rule.Enabled {
		t.Fatalf("cross-plugin l4.rule mutated rule = %+v", rule)
	}

	unbound := &PluginCapabilityManager{store: store}
	unboundResponse := unbound.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-unbound-1",
		Payload:     createPayload,
	})
	if unboundResponse.Error == nil || unboundResponse.Error.Code != pluginsdk.ErrorUnavailable {
		t.Fatalf("unbound l4.rule error = %v", unboundResponse.Error)
	}
}

func TestPluginHostL4RuleUninstallCascadeLeavesNoOrphanRules(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "local", Name: "local"}); err != nil {
		t.Fatal(err)
	}
	rules := NewL4RuleService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	manager := &PluginCapabilityManager{store: store}
	manager.SetL4RuleService(rules)

	owner := pluginCallCandidate()
	owner.Grants = []string{pluginsdk.PermissionL4Rule}
	createOwned, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionCreate, AgentID: "local", Protocol: pluginsdk.L4RuleProtocolTCP,
		ListenPort: 9200, Backends: []pluginsdk.L4RuleBackend{{Host: "127.0.0.1", Port: 9201}},
	})
	if err != nil {
		t.Fatal(err)
	}
	owned := manager.DispatchPluginHostResource(t.Context(), owner, pluginsdk.HostRuntimeCall{
		Operation: pluginsdk.HostRuntimeL4Rule, OperationID: "l4-rule-cascade-owned", Payload: createOwned,
	})
	if owned.Error != nil {
		t.Fatalf("l4.rule create error = %v", owned.Error)
	}
	var ownedResult struct {
		RuleRef string `json:"rule_ref"`
	}
	if err := json.Unmarshal(owned.Payload, &ownedResult); err != nil {
		t.Fatal(err)
	}

	other := pluginhost.Candidate{
		InstanceID:      "control-9",
		ResourceGroupID: "default",
		Identity:        pluginhost.Identity{PluginID: "other.plugin", Generation: "generation-1"},
		Grants:          []string{pluginsdk.PermissionL4Rule},
	}
	createOther, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionCreate, AgentID: "local", Protocol: pluginsdk.L4RuleProtocolTCP,
		ListenPort: 9300, Backends: []pluginsdk.L4RuleBackend{{Host: "127.0.0.1", Port: 9301}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if kept := manager.DispatchPluginHostResource(t.Context(), other, pluginsdk.HostRuntimeCall{
		Operation: pluginsdk.HostRuntimeL4Rule, OperationID: "l4-rule-cascade-other", Payload: createOther,
	}); kept.Error != nil {
		t.Fatalf("other plugin l4.rule create error = %v", kept.Error)
	}

	installPluginForUninstall(t, store, "example.plugin")
	now := time.Now().UTC()
	installed, found, err := store.GetInstalledPlugin(t.Context(), "example.plugin")
	if err != nil || !found {
		t.Fatalf("installed plugin = %+v found=%v err=%v", installed, found, err)
	}
	packageRow, found, err := store.GetPluginPackageByIdentity(t.Context(), installed.ActivePackageIdentity)
	if err != nil || !found {
		t.Fatalf("plugin package found=%v err=%v", found, err)
	}
	operation := storage.PluginOperationRow{
		ID: "op-uninstall-example.plugin", PluginID: "example.plugin", Kind: "uninstall", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `[]`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPluginMutation(t.Context(), storage.PluginMutation{
		PluginID: "example.plugin", ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
		DeletePlugin: true, DeleteInstances: true, DeleteGrants: true,
		Operation: operation,
		Audit: storage.AuditEventRow{
			ID: "audit-uninstall-example.plugin", ActorID: "admin", Action: "plugin.uninstall",
			TargetKind: "plugin", TargetID: "example.plugin", Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("uninstall mutation error = %v", err)
	}

	remaining, err := rules.List(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ListenPort != 9300 {
		t.Fatalf("uninstall cascade left rules = %+v", remaining)
	}
	if !slicesContains(remaining[0].Tags, "plugin:other.plugin") {
		t.Fatalf("surviving rule lost attribution: %v", remaining[0].Tags)
	}
	if _, err := store.GetResourceBinding(t.Context(), "l4_rule", "local:"+ownedResult.RuleRef); err == nil {
		t.Fatal("orphan resource binding survived the uninstall cascade")
	}
}

func installPluginForUninstall(t *testing.T, store *storage.GormStore, pluginID string) {
	t.Helper()
	now := time.Now().UTC()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fingerprintSum := sha256.Sum256(publicKey)
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	digest := strings.Repeat("a", 64)
	identity := storage.PluginPackageIdentity(digest, "test-source", fingerprint)
	packageRow := storage.PluginPackageRow{
		Digest: digest, Identity: identity, PluginID: pluginID, Version: "1.0.0",
		SourceID: "test-source", SourceKind: "custom", SignatureKeyID: "test-key",
		SignaturePublicKey: base64.StdEncoding.EncodeToString(publicKey), SignatureFingerprint: fingerprint,
		CachePath: "cache/" + pluginID, ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now,
	}
	operation := storage.PluginOperationRow{
		ID: "op-install-" + pluginID, PluginID: pluginID, Kind: "install", Status: "succeeded",
		ActorID: "admin", AgentResultsJSON: `[]`, CreatedAt: now, CompletedAt: &now,
	}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	installed := storage.InstalledPluginRow{
		PluginID: pluginID, ActivePackageDigest: digest, ActivePackageIdentity: identity,
		RuntimeKind: pluginsdk.RuntimeRPCService, RuntimeABI: pluginsdk.RPCABIV1, HostScope: "control-plane",
		ActiveSourceID: "test-source", ActiveSourceKind: "custom", ActiveSignatureKeyID: "test-key",
		ActiveSignaturePublicKey: packageRow.SignaturePublicKey, ActiveSignatureFingerprint: fingerprint,
		DesiredLifecycle: "disabled", CurrentLifecycle: "disabled",
		CleanupPolicyJSON: `{"instances":"delete","grants":"delete"}`, LastOperationID: operation.ID,
		StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}
	if err := store.InstallPlugin(t.Context(), storage.PluginInstallTransaction{
		Package: packageRow, Installed: installed, Operation: operation,
		Audit: storage.AuditEventRow{
			ID: "audit-install-" + pluginID, ActorID: "admin", Action: "plugin.install",
			TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now,
		},
	}); err != nil {
		t.Fatalf("InstallPlugin() error = %v", err)
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
