package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeL4Store struct {
	agents            []storage.AgentRow
	httpRulesByID     map[string][]storage.HTTPRuleRow
	l4RulesByID       map[string][]storage.L4RuleRow
	relayByAgent      map[string][]storage.RelayListenerRow
	savedAgent        storage.AgentRow
	loadSnapshotCalls int
	listL4RulesErr    error
	getL4RuleCalls    int
}

func (f *fakeL4Store) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeL4Store) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.httpRulesByID[agentID]...), nil
}

func (f *fakeL4Store) GetHTTPRule(context.Context, string, int) (storage.HTTPRuleRow, bool, error) {
	return storage.HTTPRuleRow{}, false, nil
}

func (f *fakeL4Store) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	if f.listL4RulesErr != nil {
		return nil, f.listL4RulesErr
	}
	return append([]storage.L4RuleRow(nil), f.l4RulesByID[agentID]...), nil
}

func (f *fakeL4Store) GetL4Rule(_ context.Context, agentID string, id int) (storage.L4RuleRow, bool, error) {
	f.getL4RuleCalls++
	for _, row := range f.l4RulesByID[agentID] {
		if row.ID == id {
			return row, true, nil
		}
	}
	return storage.L4RuleRow{}, false, nil
}

func (f *fakeL4Store) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	if agentID == "" {
		rows := make([]storage.RelayListenerRow, 0)
		for _, listeners := range f.relayByAgent {
			rows = append(rows, listeners...)
		}
		return rows, nil
	}
	return append([]storage.RelayListenerRow(nil), f.relayByAgent[agentID]...), nil
}

func (f *fakeL4Store) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return storage.LocalAgentStateRow{}, nil
}

func (f *fakeL4Store) LoadAgentSnapshot(context.Context, string, storage.AgentSnapshotInput) (storage.Snapshot, error) {
	f.loadSnapshotCalls++
	return storage.Snapshot{}, nil
}

func (f *fakeL4Store) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeL4Store) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return nil, nil
}

func (f *fakeL4Store) SaveAgent(_ context.Context, row storage.AgentRow) error {
	f.savedAgent = row
	for i := range f.agents {
		if f.agents[i].ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeL4Store) SaveL4Rules(_ context.Context, agentID string, rows []storage.L4RuleRow) error {
	f.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (f *fakeL4Store) SaveRelayListeners(context.Context, string, []storage.RelayListenerRow) error {
	return nil
}

func (f *fakeL4Store) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (f *fakeL4Store) SaveManagedCertificates(context.Context, []storage.ManagedCertificateRow) error {
	return nil
}

func (f *fakeL4Store) LoadManagedCertificateMaterial(context.Context, string) (storage.ManagedCertificateBundle, bool, error) {
	return storage.ManagedCertificateBundle{}, false, nil
}

func (f *fakeL4Store) SaveManagedCertificateMaterial(context.Context, string, storage.ManagedCertificateBundle) error {
	return nil
}

func (f *fakeL4Store) CleanupManagedCertificateMaterial(context.Context, []storage.ManagedCertificateRow, []storage.ManagedCertificateRow) error {
	return nil
}

func TestL4RuleServiceCreateAllowsRelayChainForUDP(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
				{ID: 8, AgentID: "local", Enabled: true},
				{ID: 9, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("udp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayChain:   &[]int{7},
		RelayLayers:  &[][]int{{7}, {8, 9}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayChain) != 1 || rule.RelayChain[0] != 7 {
		t.Fatalf("RelayChain = %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 2 || len(rule.RelayLayers[1]) != 2 || rule.RelayLayers[1][1] != 9 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayLayersJSON; got != `[[7],[8,9]]` {
		t.Fatalf("persisted relay_layers = %s", got)
	}
}

func TestL4RuleServiceCreatePreservesRelayObfsForRelayLayersOnly(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayLayers:  &[][]int{{7}},
		RelayObfs:    boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be preserved for relay_layers-only rule")
	}
}

func TestL4RuleServiceCreateNormalizesLoadBalancingStrategies(t *testing.T) {
	tests := []struct {
		name     string
		input    *L4LoadBalancing
		expected string
	}{
		{name: "defaults empty input to adaptive", input: nil, expected: "adaptive"},
		{name: "normalizes explicit adaptive", input: &L4LoadBalancing{Strategy: "ADAPTIVE"}, expected: "adaptive"},
		{name: "preserves explicit round robin", input: &L4LoadBalancing{Strategy: "round_robin"}, expected: "round_robin"},
		{name: "preserves explicit random", input: &L4LoadBalancing{Strategy: "RANDOM"}, expected: "random"},
		{name: "normalizes invalid strategy to adaptive", input: &L4LoadBalancing{Strategy: "invalid"}, expected: "adaptive"},
		{name: "normalizes blank strategy to adaptive", input: &L4LoadBalancing{Strategy: "   "}, expected: "adaptive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Create(context.Background(), "local", L4RuleInput{
				Protocol:      stringPtrL4("tcp"),
				ListenPort:    intPtrL4(9000),
				UpstreamHost:  stringPtrL4("upstream"),
				UpstreamPort:  intPtrL4(9001),
				LoadBalancing: tt.input,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if rule.LoadBalancing.Strategy != tt.expected {
				t.Fatalf("Create() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != `{"strategy":"`+tt.expected+`"}` {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}

func TestL4RuleFromRowDefaultsLoadBalancingToAdaptive(t *testing.T) {
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:           1,
		AgentID:      "local",
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   9000,
		UpstreamHost: "upstream",
		UpstreamPort: 9001,
	})

	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("l4RuleFromRow() load_balancing = %+v", rule.LoadBalancing)
	}
}

func TestL4RuleServiceCreateAllocatesGlobalIDsAcrossAgentsInSQLiteStore(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	for _, agentID := range []string{"agent-a", "agent-b"} {
		if err := store.SaveAgent(context.Background(), storage.AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       agentID + "-token",
			CapabilitiesJSON: marshalStringArray([]string{"http_rules", "l4"}),
		}); err != nil {
			t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
		}
	}

	svc := NewL4RuleService(config.Config{}, store)

	first, err := svc.Create(context.Background(), "agent-a", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream-a"),
		UpstreamPort: intPtrL4(9001),
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9100),
		UpstreamHost: stringPtrL4("upstream-b"),
		UpstreamPort: intPtrL4(9101),
	})
	if err != nil {
		t.Fatalf("Create(agent-b) error = %v", err)
	}

	if first.ID != 1 {
		t.Fatalf("first rule id = %d", first.ID)
	}
	if second.ID != 2 {
		t.Fatalf("second rule id = %d", second.ID)
	}
}

func TestL4RuleServiceCreateAllocatesIDsAfterExistingHTTPRulesInSQLiteStore(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	for _, agentID := range []string{"agent-a", "agent-b"} {
		if err := store.SaveAgent(context.Background(), storage.AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       agentID + "-token",
			CapabilitiesJSON: marshalStringArray([]string{"http_rules", "l4"}),
		}); err != nil {
			t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
		}
	}

	httpSvc := NewRuleService(config.Config{}, store)
	l4Svc := NewL4RuleService(config.Config{}, store)

	httpRule, err := httpSvc.Create(context.Background(), "agent-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-a.example.com"),
		BackendURL:  stringPtrRule("http://backend-a.example.internal:8096"),
	})
	if err != nil {
		t.Fatalf("Create HTTP rule error = %v", err)
	}

	l4Rule, err := l4Svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9100),
		UpstreamHost: stringPtrL4("backend-b.example.internal"),
		UpstreamPort: intPtrL4(9101),
	})
	if err != nil {
		t.Fatalf("Create L4 rule error = %v", err)
	}

	if httpRule.ID != 1 {
		t.Fatalf("httpRule.ID = %d", httpRule.ID)
	}
	if l4Rule.ID != 2 {
		t.Fatalf("l4Rule.ID = %d", l4Rule.ID)
	}
}

func TestL4RuleServiceCreateClearsRelayObfsWithoutRelayChain(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayObfs:    boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is empty")
	}
}

func TestL4RuleServiceCreateDetachesCanceledTriggerContext(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	type requestContextKey string
	requestCtx := context.WithValue(context.Background(), requestContextKey("trace"), "l4-create")
	requestCtx, cancel := context.WithCancel(requestCtx)
	cancel()

	triggerCalls := 0
	svc.SetLocalApplyTrigger(func(ctx context.Context) error {
		triggerCalls++
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("trigger ctx err = %v", err)
		}
		if got := ctx.Value(requestContextKey("trace")); got != "l4-create" {
			return fmt.Errorf("trigger ctx trace = %v", got)
		}
		return nil
	})

	rule, err := svc.Create(requestCtx, "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 1 {
		t.Fatalf("Create() rule = %+v", rule)
	}
	if triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d", triggerCalls)
	}
}

func TestL4RuleServiceCreateClearsRelayObfsForUDP(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("udp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayObfs:    boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceUpdateClearsRelayObfsWhenRelayChainRemoved(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:             1,
				AgentID:        "local",
				Name:           "relay rule",
				Protocol:       "tcp",
				ListenHost:     "0.0.0.0",
				ListenPort:     9000,
				UpstreamHost:   "upstream",
				UpstreamPort:   9001,
				RelayChainJSON: `[7]`,
				RelayObfs:      true,
				Enabled:        true,
				Revision:       3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayChain: &[]int{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is removed")
	}
}

func TestL4RuleServiceUpdateClearsRelayLayersWhenRelayChainOnlyUpdate(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				UpstreamHost:    "upstream",
				UpstreamPort:    9001,
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7],[8,9]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      5,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayChain: &[]int{5},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 1 || rule.RelayChain[0] != 5 {
		t.Fatalf("expected relay_chain to update, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 0 {
		t.Fatalf("expected relay_layers to be cleared, got %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayLayersJSON; got != `[]` {
		t.Fatalf("persisted relay_layers = %s", got)
	}
}

func TestL4RuleServiceUpdateClearsRelayChainWhenRelayLayersSupplied(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				UpstreamHost:    "upstream",
				UpstreamPort:    9001,
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      8,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{{8}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 8 {
		t.Fatalf("expected relay_layers to update, got %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestL4RuleServiceUpdateClearsRelayWhenRelayLayersCleared(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				UpstreamHost:    "upstream",
				UpstreamPort:    9001,
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayLayers: &[][]int{},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 0 {
		t.Fatalf("expected relay_layers to be cleared, got %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestL4RuleServiceUpdateDefaultsInvalidLoadBalancingToAdaptive(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				Name:              "relay rule",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				UpstreamHost:      "upstream",
				UpstreamPort:      9001,
				LoadBalancingJSON: `{}`,
				Enabled:           true,
				Revision:          3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol: stringPtrL4("tcp"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
	}
	if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != `{"strategy":"adaptive"}` {
		t.Fatalf("persisted load_balancing_json = %q", got)
	}
}

func TestL4RuleServiceUpdatePreservesExplicitLoadBalancingStrategies(t *testing.T) {
	for _, strategy := range []string{"round_robin", "random"} {
		t.Run(strategy, func(t *testing.T) {
			lbJSON := `{"strategy":"` + strategy + `"}`
			store := &fakeL4Store{
				l4RulesByID: map[string][]storage.L4RuleRow{
					"local": {{
						ID:                1,
						AgentID:           "local",
						Name:              "relay rule",
						Protocol:          "tcp",
						ListenHost:        "0.0.0.0",
						ListenPort:        9000,
						UpstreamHost:      "upstream",
						UpstreamPort:      9001,
						LoadBalancingJSON: lbJSON,
						Enabled:           true,
						Revision:          3,
					}},
				},
			}
			svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

			rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
				Protocol: stringPtrL4("tcp"),
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			if rule.LoadBalancing.Strategy != strategy {
				t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.l4RulesByID["local"][0].LoadBalancingJSON; got != lbJSON {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}

func TestL4RuleServiceUpdatePreservesRelayChainWhenSwitchingToUDP(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:             1,
				AgentID:        "local",
				Name:           "relay rule",
				Protocol:       "tcp",
				ListenHost:     "0.0.0.0",
				ListenPort:     9000,
				UpstreamHost:   "upstream",
				UpstreamPort:   9001,
				RelayChainJSON: `[7]`,
				RelayObfs:      true,
				Enabled:        true,
				Revision:       3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		Protocol: stringPtrL4("udp"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.Protocol != "udp" {
		t.Fatalf("expected protocol udp, got %q", rule.Protocol)
	}
	if len(rule.RelayChain) != 1 || rule.RelayChain[0] != 7 {
		t.Fatalf("expected relay_chain to be preserved for udp, got %+v", rule.RelayChain)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayChainEntries(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:      7,
				AgentID: "local",
				Enabled: true,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayChain:   &[]int{7, 7},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_chain entries must not contain duplicates" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayLayerEntriesAcrossLayers(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
				{ID: 8, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayLayers:  &[][]int{{7, 8}, {7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not repeat listener IDs across layers" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsUnknownRelayLayerListener(t *testing.T) {
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {
				{ID: 7, AgentID: "local", Enabled: true},
			},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayLayers:  &[][]int{{7, 8}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay listener not found: 8" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceDeleteUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  4,
			CurrentRevision:  4,
		}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.l4RulesByID["edge-1"]) != 0 {
		t.Fatalf("l4 rules still stored: %+v", store.l4RulesByID["edge-1"])
	}
	if store.agents[0].DesiredRevision != 5 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
	if store.loadSnapshotCalls != 0 {
		t.Fatalf("LoadAgentSnapshot() calls = %d", store.loadSnapshotCalls)
	}
}

func TestL4RuleServiceCreateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "edge-1", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(50382),
		UpstreamHost: stringPtrL4("127.0.0.1"),
		UpstreamPort: intPtrL4(26967),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Create() revision", rule.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, rule.Revision)
}

func TestL4RuleServiceCreateReassignsPreferredIDWhenHTTPRuleAlreadyUsesIt(t *testing.T) {
	store := &fakeL4Store{
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "http://existing-http.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    2,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           7,
				AgentID:      "local",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     3,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ID:           intPtrL4(9),
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(50382),
		UpstreamHost: stringPtrL4("127.0.0.1"),
		UpstreamPort: intPtrL4(26967),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", rule.ID)
	}
}

func TestL4RuleServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		ListenPort: intPtrL4(50382),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Update() revision", rule.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, rule.Revision)
}

func TestL4RuleServiceDeleteUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           1,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
}

func TestL4RuleServiceGetUsesDirectStoreLookup(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["l4"]`,
		}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           9,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 9001,
				Revision:     3,
			}},
		},
		relayByAgent:   map[string][]storage.RelayListenerRow{},
		listL4RulesErr: context.DeadlineExceeded,
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Get(context.Background(), "edge-1", 9)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rule.ID != 9 {
		t.Fatalf("Get() rule = %+v", rule)
	}
	if store.getL4RuleCalls != 1 {
		t.Fatalf("GetL4Rule() calls = %d", store.getL4RuleCalls)
	}
}

func stringPtrL4(value string) *string {
	return &value
}

func intPtrL4(value int) *int {
	return &value
}

func boolPtrL4(value bool) *bool {
	return &value
}
