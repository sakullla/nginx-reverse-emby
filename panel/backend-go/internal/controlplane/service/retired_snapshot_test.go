package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRetiredRuntimeSnapshotRemovesRetiredResourceClosure(t *testing.T) {
	t.Parallel()
	retiredEgressID := 21
	input := storage.Snapshot{
		Rules: []storage.HTTPRule{
			{ID: 1, FrontendURL: "https://ordinary.example.test", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8001"}}},
			{ID: 2, FrontendURL: "https://entry.example.test", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8002"}}, WireGuardEntryEnabled: true},
			{ID: 3, FrontendURL: "https://relay.example.test", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8003"}}, RelayLayers: [][]int{{11}}},
			{ID: 4, FrontendURL: "https://egress.example.test", Backends: []storage.HTTPBackend{{URL: "http://127.0.0.1:8004"}}, EgressProfileID: &retiredEgressID},
		},
		L4Rules: []storage.L4Rule{
			{ID: 5, ListenMode: "tcp", RelayLayers: [][]int{{10}}},
			{ID: 6, ListenMode: "wireguard"},
			{ID: 7, ListenMode: "tcp", RelayLayers: [][]int{{11}}},
			{ID: 8, ListenMode: "tcp", EgressProfileID: &retiredEgressID},
		},
		RelayListeners: []storage.RelayListener{
			{ID: 10, TransportMode: "tls_tcp"},
			{ID: 11, TransportMode: "wireguard"},
		},
		WireGuardProfiles: []storage.WireGuardProfile{{ID: 12, Mode: "generic_wireguard"}},
		EgressProfiles: []storage.EgressProfile{
			{ID: 20, Type: "socks"},
			{ID: retiredEgressID, Type: "wireguard"},
		},
	}

	filtered, err := retiredRuntimeSnapshot(context.Background(), nil, revision.Target{AgentID: "local"}, input)
	if err != nil {
		t.Fatalf("retiredRuntimeSnapshot() error = %v", err)
	}
	if got := snapshotHTTPRuleIDs(filtered.Rules); fmt.Sprint(got) != "[1]" {
		t.Fatalf("HTTP rule ids = %v, want [1]", got)
	}
	if got := snapshotL4RuleIDs(filtered.L4Rules); fmt.Sprint(got) != "[5]" {
		t.Fatalf("L4 rule ids = %v, want [5]", got)
	}
	if len(filtered.RelayListeners) != 1 || filtered.RelayListeners[0].ID != 10 {
		t.Fatalf("relay listeners = %+v, want ordinary listener 10", filtered.RelayListeners)
	}
	if len(filtered.EgressProfiles) != 1 || filtered.EgressProfiles[0].ID != 20 {
		t.Fatalf("egress profiles = %+v, want ordinary profile 20", filtered.EgressProfiles)
	}
	if len(filtered.WireGuardProfiles) != 0 {
		t.Fatalf("wireguard profiles leaked: %+v", filtered.WireGuardProfiles)
	}
	if len(input.Rules) != 4 || len(input.L4Rules) != 4 || len(input.RelayListeners) != 2 || len(input.EgressProfiles) != 2 || len(input.WireGuardProfiles) != 1 {
		t.Fatalf("transform mutated input snapshot: %+v", input)
	}
}

func TestRetiredRuntimeSnapshotUsesPersistedIDsOutsideTargetSnapshot(t *testing.T) {
	store := newMutationValidationStore(t)
	ctx := t.Context()
	if err := store.SaveRelayListeners(ctx, "relay-agent", []storage.RelayListenerRow{{
		ID: 61, AgentID: "relay-agent", Name: "retired relay", TransportMode: "wireguard",
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID: 62, Name: "retired egress", Type: "wireguard",
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	egressID := 62
	input := storage.Snapshot{
		Rules: []storage.HTTPRule{{
			ID: 1, AgentID: "rule-agent", RelayLayers: [][]int{{61}},
		}},
		L4Rules: []storage.L4Rule{{
			ID: 2, AgentID: "rule-agent", ListenMode: "tcp", EgressProfileID: &egressID,
		}},
	}

	filtered, err := retiredRuntimeSnapshot(ctx, store, revision.Target{AgentID: "rule-agent"}, input)
	if err != nil {
		t.Fatalf("retiredRuntimeSnapshot() error = %v", err)
	}
	if len(filtered.Rules) != 0 || len(filtered.L4Rules) != 0 {
		t.Fatalf("filtered snapshot retained cross-agent retired references: %+v", filtered)
	}
}

func TestMutationExecutorRejectsExplicitReferencesToKnownRetiredResources(t *testing.T) {
	store := newMutationValidationStore(t)
	ctx := t.Context()
	if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID: 31, AgentID: "local", Name: "retired relay", TransportMode: "wireguard", Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID: 41, Name: "retired egress", Type: "wireguard", WireGuardConfigJSON: `{}`, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}

	relayLayers := [][]int{{31}}
	retiredEgressID := 41
	type httpUpdateRequest struct {
		ID    int
		Input HTTPRuleInput
	}
	type l4UpdateRequest struct {
		ID    int
		Input L4RuleInput
	}
	tests := []struct {
		name    string
		request any
	}{
		{name: "HTTP create relay", request: HTTPRuleInput{RelayLayers: &relayLayers}},
		{name: "HTTP create egress", request: HTTPRuleInput{EgressProfileID: &retiredEgressID}},
		{name: "L4 create relay", request: L4RuleInput{RelayLayers: &relayLayers}},
		{name: "L4 create egress", request: L4RuleInput{EgressProfileID: &retiredEgressID}},
		{name: "HTTP update relay", request: httpUpdateRequest{ID: 1, Input: HTTPRuleInput{RelayLayers: &relayLayers}}},
		{name: "HTTP update egress", request: httpUpdateRequest{ID: 1, Input: HTTPRuleInput{EgressProfileID: &retiredEgressID}}},
		{name: "L4 update relay", request: l4UpdateRequest{ID: 1, Input: L4RuleInput{RelayLayers: &relayLayers}}},
		{name: "L4 update egress", request: l4UpdateRequest{ID: 1, Input: L4RuleInput{EgressProfileID: &retiredEgressID}}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operationID := fmt.Sprintf("op-retired-reference-%d", index)
			mutationCalls := 0
			executor := NewMutationExecutor(
				store,
				revision.WithClock(func() time.Time { return time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC) }),
				revision.WithOperationIDGenerator(func() (string, error) { return operationID, nil }),
			)
			_, err := executor.Execute(ctx, revision.MutationRequest{
				Kind: "rule.create", Request: test.request,
				Targets: []revision.Target{{AgentID: "local", Local: true}},
				ResourceState: func(context.Context, *storage.GormStore, revision.Target) (any, error) {
					return "unchanged", nil
				},
				Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
					mutationCalls++
					return nil
				},
			})
			if revision.ErrorCodeOf(err) != revision.ErrorCodeNotFound {
				t.Fatalf("Execute() error = %v, code = %q, want generic not_found", err, revision.ErrorCodeOf(err))
			}
			if strings.Contains(strings.ToLower(err.Error()), "wireguard") {
				t.Fatalf("Execute() exposed retired implementation detail: %v", err)
			}
			if mutationCalls != 0 {
				t.Fatalf("mutation calls = %d, want validation before writes", mutationCalls)
			}
			if _, found, getErr := store.GetOperation(ctx, operationID); getErr != nil {
				t.Fatalf("GetOperation() error = %v", getErr)
			} else if found {
				t.Fatalf("operation %q survived rejected reference", operationID)
			}
			for _, agentID := range []string{"local"} {
				revisions, listErr := store.ListAgentRevisions(ctx, agentID)
				if listErr != nil {
					t.Fatalf("ListAgentRevisions(%s) error = %v", agentID, listErr)
				}
				for _, row := range revisions {
					if row.OperationID == operationID {
						t.Fatalf("revision survived rejected reference: %+v", row)
					}
				}
			}
		})
	}
}

func TestUnrelatedMutationFiltersRetiredRuntimeArtifactsAndPreservesIntentRows(t *testing.T) {
	store := newMutationValidationStore(t)
	ctx := t.Context()
	retiredEgressID := 53
	if err := store.SaveWireGuardProfiles(ctx, "local", []storage.WireGuardProfileRow{{
		ID: 51, AgentID: "local", Name: "retired profile", Mode: "generic_wireguard",
		AddressesJSON: `["10.51.0.1/24"]`, PeersJSON: `[]`, DNSJSON: `[]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID: 52, AgentID: "local", Name: "retired relay", ListenHost: "10.52.0.1",
		TransportMode: "wireguard", WireGuardProfileID: intPtrRule(51), Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID: retiredEgressID, Name: "retired egress", Type: "wireguard", WireGuardConfigJSON: `{}`,
		Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(ctx, "local", []storage.HTTPRuleRow{{
		ID: 55, AgentID: "local", FrontendURL: "http://retired-relay.example.test:18055",
		BackendsJSON: `[{"url":"http://127.0.0.1:8055"}]`, RelayLayersJSON: `[[52]]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(ctx, "local", []storage.L4RuleRow{
		{
			ID: 54, AgentID: "local", Name: "retired entry", Protocol: "tcp", ListenMode: "wireguard",
			WireGuardProfileID: intPtrRule(51), Enabled: true, Revision: 1,
		},
		{
			ID: 56, AgentID: "local", Name: "retired egress dependency", Protocol: "tcp", ListenMode: "tcp",
			ListenHost: "127.0.0.1", ListenPort: 18056,
			BackendsJSON: `[{"host":"127.0.0.1","port":8056}]`, EgressProfileID: &retiredEgressID,
			Enabled: true, Revision: 1,
		},
	}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	intentBefore, err := store.LoadLocalIntentSnapshot(ctx, "local")
	if err != nil {
		t.Fatalf("LoadLocalIntentSnapshot(before) error = %v", err)
	}
	if len(intentBefore.WireGuardProfiles) != 1 || len(intentBefore.RelayListeners) != 1 || len(intentBefore.EgressProfiles) != 1 || len(intentBefore.Rules) != 1 || len(intentBefore.L4Rules) != 2 {
		t.Fatalf("seeded intent snapshot is incomplete: %+v", intentBefore)
	}

	service := NewRuleService(testConfig(), store)
	service.mutationExecutor = NewMutationExecutor(
		store,
		revision.WithClock(func() time.Time { return time.Date(2026, 7, 21, 1, 30, 0, 0, time.UTC) }),
		revision.WithOperationIDGenerator(func() (string, error) { return "op-unrelated-after-retired", nil }),
	)
	created, err := service.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://ordinary-after-retired.example.test:18057"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8057"}},
	})
	if err != nil {
		t.Fatalf("Create(unrelated) error = %v", err)
	}
	if created.ID <= 0 {
		t.Fatalf("created rule = %+v", created)
	}

	revisions, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	var createdRevision storage.AgentRevisionRow
	for _, row := range revisions {
		if row.OperationID == "op-unrelated-after-retired" {
			createdRevision = row
		}
	}
	if createdRevision.SnapshotArtifactID == "" {
		t.Fatalf("revision for unrelated mutation not found: %+v", revisions)
	}
	artifact, found, err := store.GetGenerationArtifact(ctx, createdRevision.SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact() found=%v error=%v", found, err)
	}
	var delivered storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &delivered); err != nil {
		t.Fatalf("json.Unmarshal(snapshot artifact) error = %v", err)
	}
	if len(delivered.WireGuardProfiles) != 0 || len(delivered.RelayListeners) != 0 || len(delivered.EgressProfiles) != 0 || len(delivered.L4Rules) != 0 {
		t.Fatalf("runtime artifact leaked retired resources: %+v", delivered)
	}
	if len(delivered.Rules) != 1 || delivered.Rules[0].ID != created.ID {
		t.Fatalf("runtime HTTP rules = %+v, want only created ordinary rule %d", delivered.Rules, created.ID)
	}
	planArtifact, found, err := store.GetOperationDependencyArtifact(ctx, "op-unrelated-after-retired")
	if err != nil || !found {
		t.Fatalf("GetOperationDependencyArtifact() found=%v error=%v", found, err)
	}
	var plan dependency.Plan
	if err := json.Unmarshal(planArtifact.Payload, &plan); err != nil {
		t.Fatalf("json.Unmarshal(dependency plan) error = %v", err)
	}
	if len(plan.Edges) != 0 || len(plan.Nodes) != 1 || plan.Nodes[0].AgentID != "local" {
		t.Fatalf("dependency plan = %+v, want one ordinary local node without dangling edges", plan)
	}

	intentAfter, err := store.LoadLocalIntentSnapshot(ctx, "local")
	if err != nil {
		t.Fatalf("LoadLocalIntentSnapshot(after) error = %v", err)
	}
	if len(intentAfter.WireGuardProfiles) != 1 || len(intentAfter.RelayListeners) != 1 || len(intentAfter.EgressProfiles) != 1 || len(intentAfter.L4Rules) != 2 {
		t.Fatalf("unrelated mutation changed retired storage intent: %+v", intentAfter)
	}
	if len(intentAfter.Rules) != 2 {
		t.Fatalf("intent HTTP rules = %+v, want legacy dependency plus created ordinary rule", intentAfter.Rules)
	}
}

func snapshotHTTPRuleIDs(rules []storage.HTTPRule) []int {
	ids := make([]int, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return ids
}

func snapshotL4RuleIDs(rules []storage.L4Rule) []int {
	ids := make([]int, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return ids
}
