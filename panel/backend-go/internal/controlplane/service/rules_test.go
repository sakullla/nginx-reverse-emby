package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeRuleStore struct {
	agents         []storage.AgentRow
	rulesByAgent   map[string][]storage.HTTPRuleRow
	l4RulesByAgent map[string][]storage.L4RuleRow
	listeners      []storage.RelayListenerRow
	managedCerts   []storage.ManagedCertificateRow

	listHTTPRulesErr  error
	saveHTTPRulesErrs []error
	saveManagedErrs   []error
	cleanupErrs       []error
	materialByDomain  map[string]bool
	cleanupCallCount  int
	getHTTPRuleCalls  int
}

func (f *fakeRuleStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeRuleStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	if f.listHTTPRulesErr != nil {
		return nil, f.listHTTPRulesErr
	}
	return append([]storage.HTTPRuleRow(nil), f.rulesByAgent[agentID]...), nil
}

func (f *fakeRuleStore) GetHTTPRule(_ context.Context, agentID string, id int) (storage.HTTPRuleRow, bool, error) {
	f.getHTTPRuleCalls++
	for _, row := range f.rulesByAgent[agentID] {
		if row.ID == id {
			return row, true, nil
		}
	}
	return storage.HTTPRuleRow{}, false, nil
}

func (f *fakeRuleStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), f.l4RulesByAgent[agentID]...), nil
}

func (f *fakeRuleStore) SaveHTTPRules(_ context.Context, agentID string, rows []storage.HTTPRuleRow) error {
	if err := popRuleStoreError(&f.saveHTTPRulesErrs); err != nil {
		return err
	}
	f.rulesByAgent[agentID] = append([]storage.HTTPRuleRow(nil), rows...)
	return nil
}

func (f *fakeRuleStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	for i, agent := range f.agents {
		if agent.ID == row.ID {
			f.agents[i] = row
			return nil
		}
	}
	f.agents = append(f.agents, row)
	return nil
}

func (f *fakeRuleStore) ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error) {
	return append([]storage.RelayListenerRow(nil), f.listeners...), nil
}

func (f *fakeRuleStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), f.managedCerts...), nil
}

func (f *fakeRuleStore) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return storage.LocalAgentStateRow{}, nil
}

func (f *fakeRuleStore) LoadAgentSnapshot(context.Context, string, storage.AgentSnapshotInput) (storage.Snapshot, error) {
	return storage.Snapshot{}, nil
}

func (f *fakeRuleStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	if err := popRuleStoreError(&f.saveManagedErrs); err != nil {
		return err
	}
	f.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (f *fakeRuleStore) CleanupManagedCertificateMaterial(_ context.Context, previous []storage.ManagedCertificateRow, next []storage.ManagedCertificateRow) error {
	f.cleanupCallCount++
	if err := popRuleStoreError(&f.cleanupErrs); err != nil {
		return err
	}
	if f.materialByDomain == nil {
		return nil
	}
	nextDomains := make(map[string]struct{}, len(next))
	for _, row := range next {
		nextDomains[strings.TrimSpace(row.Domain)] = struct{}{}
	}
	for _, row := range previous {
		domain := strings.TrimSpace(row.Domain)
		if domain == "" {
			continue
		}
		if _, ok := nextDomains[domain]; ok {
			continue
		}
		delete(f.materialByDomain, domain)
	}
	return nil
}

func TestRuleServiceCreateNormalizesAndPersists(t *testing.T) {
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{{
			ID:       7,
			AgentID:  "local",
			Enabled:  true,
			Revision: 1,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://existing.example.com",
				BackendURL:        "http://emby:8096",
				BackendsJSON:      `[{"url":"http://emby:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `["existing"]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[3]`,
				PassProxyHeaders:  true,
				UserAgent:         "",
				CustomHeadersJSON: `[]`,
				Revision:          7,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule(" https://new.example.com "),
		Backends: &[]HTTPRuleBackend{
			{URL: ""},
			{URL: " http://upstream-a:8096 "},
		},
		LoadBalancing:    &HTTPLoadBalancing{Strategy: "RANDOM"},
		Tags:             &[]string{" edge ", ""},
		RelayChain:       &[]int{7},
		RelayObfs:        boolPtrRule(true),
		CustomHeaders:    &[]HTTPCustomHeader{{Name: "", Value: "drop"}, {Name: " X-Test ", Value: "1"}},
		PassProxyHeaders: boolPtrRule(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rule.ID != 2 || rule.Revision != 8 {
		t.Fatalf("Create() rule id/revision = %+v", rule)
	}
	if rule.FrontendURL != "https://new.example.com" {
		t.Fatalf("Create() frontend_url = %q", rule.FrontendURL)
	}
	if rule.BackendURL != "http://upstream-a:8096" || len(rule.Backends) != 1 {
		t.Fatalf("Create() backends = %+v", rule.Backends)
	}
	if rule.LoadBalancing.Strategy != "random" {
		t.Fatalf("Create() load_balancing = %+v", rule.LoadBalancing)
	}
	if len(rule.Tags) != 1 || rule.Tags[0] != "edge" {
		t.Fatalf("Create() tags = %+v", rule.Tags)
	}
	if len(rule.RelayChain) != 1 || rule.RelayChain[0] != 7 {
		t.Fatalf("Create() relay_chain = %+v", rule.RelayChain)
	}
	if !rule.RelayObfs {
		t.Fatalf("Create() relay_obfs = false")
	}
	if rule.PassProxyHeaders {
		t.Fatalf("Create() pass_proxy_headers = true")
	}
	if len(rule.CustomHeaders) != 1 || rule.CustomHeaders[0].Name != "X-Test" {
		t.Fatalf("Create() custom_headers = %+v", rule.CustomHeaders)
	}
	if got := len(store.rulesByAgent["local"]); got != 2 {
		t.Fatalf("persisted rules = %d", got)
	}
}

func TestRuleServiceCreateNormalizesLoadBalancingStrategies(t *testing.T) {
	tests := []struct {
		name     string
		input    *HTTPLoadBalancing
		expected string
	}{
		{name: "defaults empty input to adaptive", input: nil, expected: "adaptive"},
		{name: "normalizes explicit adaptive", input: &HTTPLoadBalancing{Strategy: "ADAPTIVE"}, expected: "adaptive"},
		{name: "preserves explicit round robin", input: &HTTPLoadBalancing{Strategy: "round_robin"}, expected: "round_robin"},
		{name: "preserves explicit random", input: &HTTPLoadBalancing{Strategy: "RANDOM"}, expected: "random"},
		{name: "normalizes invalid strategy to adaptive", input: &HTTPLoadBalancing{Strategy: "invalid"}, expected: "adaptive"},
		{name: "normalizes blank strategy to adaptive", input: &HTTPLoadBalancing{Strategy: "   "}, expected: "adaptive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeRuleStore{
				rulesByAgent: map[string][]storage.HTTPRuleRow{},
			}
			svc := NewRuleService(config.Config{
				EnableLocalAgent: true,
				LocalAgentID:     "local",
			}, store)

			rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
				FrontendURL:   stringPtrRule("https://new.example.com"),
				BackendURL:    stringPtrRule("http://upstream-a:8096"),
				LoadBalancing: tt.input,
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			if rule.LoadBalancing.Strategy != tt.expected {
				t.Fatalf("Create() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.rulesByAgent["local"][0].LoadBalancingJSON; got != `{"strategy":"`+tt.expected+`"}` {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}

func TestRuleServiceUpdateNormalizesAndPersists(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID: "edge",
		}},
		listeners: []storage.RelayListenerRow{{
			ID:       5,
			AgentID:  "local",
			Enabled:  true,
			Revision: 1,
		}, {
			ID:       6,
			AgentID:  "edge",
			Enabled:  true,
			Revision: 2,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                3,
				AgentID:           "local",
				FrontendURL:       "https://before.example.com",
				BackendURL:        "http://emby:8096",
				BackendsJSON:      `[{"url":"http://emby:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `["existing"]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[5]`,
				PassProxyHeaders:  true,
				UserAgent:         "Legacy",
				CustomHeadersJSON: `[{"name":"X-Legacy","value":"1"}]`,
				Revision:          10,
			}},
			"edge": {{
				ID:       1,
				AgentID:  "edge",
				Revision: 15,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Update(context.Background(), "local", 3, HTTPRuleInput{
		FrontendURL:   stringPtrRule(" https://after.example.com "),
		LoadBalancing: &HTTPLoadBalancing{Strategy: "invalid"},
		UserAgent:     stringPtrRule(" MyAgent "),
		CustomHeaders: &[]HTTPCustomHeader{{Name: "  ", Value: "drop"}, {Name: "X-New", Value: "2"}},
		Tags:          &[]string{"", "  media"},
		RelayChain:    &[]int{5, 6},
		RelayObfs:     boolPtrRule(true),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rule.FrontendURL != "https://after.example.com" {
		t.Fatalf("Update() frontend_url = %q", rule.FrontendURL)
	}
	if rule.BackendURL != "http://emby:8096" || len(rule.Backends) != 1 || rule.Backends[0].URL != "http://emby:8096" {
		t.Fatalf("Update() backends fallback = %+v", rule.Backends)
	}
	if rule.LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
	}
	if rule.UserAgent != "MyAgent" {
		t.Fatalf("Update() user_agent = %q", rule.UserAgent)
	}
	if len(rule.CustomHeaders) != 1 || rule.CustomHeaders[0].Name != "X-New" {
		t.Fatalf("Update() custom_headers = %+v", rule.CustomHeaders)
	}
	if len(rule.Tags) != 1 || rule.Tags[0] != "media" {
		t.Fatalf("Update() tags = %+v", rule.Tags)
	}
	if len(rule.RelayChain) != 2 || rule.RelayChain[0] != 5 || rule.RelayChain[1] != 6 {
		t.Fatalf("Update() relay_chain = %+v", rule.RelayChain)
	}
	if !rule.RelayObfs {
		t.Fatalf("Update() relay_obfs = false")
	}
	if !rule.Enabled {
		t.Fatalf("Update() enabled fallback = false")
	}
	if !rule.ProxyRedirect {
		t.Fatalf("Update() proxy_redirect fallback = false")
	}
	if !rule.PassProxyHeaders {
		t.Fatalf("Update() pass_proxy_headers fallback = false")
	}
	if rule.Revision != 16 {
		t.Fatalf("Update() revision = %d", rule.Revision)
	}
	if store.rulesByAgent["local"][0].Revision != 16 {
		t.Fatalf("persisted revision = %d", store.rulesByAgent["local"][0].Revision)
	}
	if store.rulesByAgent["local"][0].BackendURL != "http://emby:8096" {
		t.Fatalf("persisted backend fallback = %q", store.rulesByAgent["local"][0].BackendURL)
	}
	if store.rulesByAgent["local"][0].LoadBalancingJSON != `{"strategy":"adaptive"}` {
		t.Fatalf("persisted load_balancing_json = %q", store.rulesByAgent["local"][0].LoadBalancingJSON)
	}
}

func TestRuleServiceUpdatePreservesExplicitLoadBalancingStrategies(t *testing.T) {
	for _, strategy := range []string{"round_robin", "random"} {
		t.Run(strategy, func(t *testing.T) {
			lbJSON := `{"strategy":"` + strategy + `"}`
			store := &fakeRuleStore{
				listeners: []storage.RelayListenerRow{{
					ID:       5,
					AgentID:  "local",
					Enabled:  true,
					Revision: 10,
				}},
				rulesByAgent: map[string][]storage.HTTPRuleRow{
					"local": {{
						ID:                3,
						AgentID:           "local",
						FrontendURL:       "https://before.example.com",
						BackendURL:        "http://emby:8096",
						BackendsJSON:      `[{"url":"http://emby:8096"}]`,
						LoadBalancingJSON: lbJSON,
						Enabled:           true,
						TagsJSON:          `["existing"]`,
						ProxyRedirect:     true,
						RelayChainJSON:    `[5]`,
						PassProxyHeaders:  true,
						UserAgent:         "Legacy",
						CustomHeadersJSON: `[{"name":"X-Legacy","value":"1"}]`,
						Revision:          10,
					}},
				},
			}
			svc := NewRuleService(config.Config{
				EnableLocalAgent: true,
				LocalAgentID:     "local",
			}, store)

			rule, err := svc.Update(context.Background(), "local", 3, HTTPRuleInput{
				FrontendURL: stringPtrRule("https://after.example.com"),
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			if rule.LoadBalancing.Strategy != strategy {
				t.Fatalf("Update() load_balancing = %+v", rule.LoadBalancing)
			}
			if got := store.rulesByAgent["local"][0].LoadBalancingJSON; got != lbJSON {
				t.Fatalf("persisted load_balancing_json = %q", got)
			}
		})
	}
}
func TestRuleServiceDeletePersistsRemoval(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          1,
				AgentID:     "local",
				FrontendURL: "https://one.example.com",
			}, {
				ID:          2,
				AgentID:     "local",
				FrontendURL: "https://two.example.com",
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("Delete() id = %d", deleted.ID)
	}
	if got := len(store.rulesByAgent["local"]); got != 1 {
		t.Fatalf("persisted rules = %d", got)
	}
	if store.rulesByAgent["local"][0].ID != 2 {
		t.Fatalf("remaining rule = %+v", store.rulesByAgent["local"][0])
	}
}

func TestRuleServiceCreateRejectsUnknownRelayChainListener(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
		RelayChain:  &[]int{999},
	})
	if err == nil {
		t.Fatalf("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay listener not found: 999" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateClearsRelayObfsWithoutRelayChain(t *testing.T) {
	store := &fakeRuleStore{rulesByAgent: map[string][]storage.HTTPRuleRow{}}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
		RelayObfs:   boolPtrRule(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is empty")
	}
}

func TestRuleServiceUpdateClearsRelayObfsWhenRelayChainRemoved(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:             1,
				AgentID:        "local",
				FrontendURL:    "https://relay.example.com",
				BackendURL:     "http://127.0.0.1:8096",
				BackendsJSON:   `[{"url":"http://127.0.0.1:8096"}]`,
				RelayChainJSON: `[7]`,
				RelayObfs:      true,
				Enabled:        true,
				Revision:       2,
			}},
		},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
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

func TestRuleServiceCreateRejectsInvalidRelayChainEntry(t *testing.T) {
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{{
			ID:      7,
			AgentID: "local",
			Enabled: true,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://invalid.example.com"),
		BackendURL:  stringPtrRule("http://upstream:8096"),
		RelayChain:  &[]int{7, 0},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_chain entries must be positive integer listener IDs" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRejectsDuplicateRelayChainEntries(t *testing.T) {
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{{
			ID:      7,
			AgentID: "local",
			Enabled: true,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://duplicate.example.com"),
		BackendURL:  stringPtrRule("http://upstream:8096"),
		RelayChain:  &[]int{7, 7},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_chain entries must not contain duplicates" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRejectsDuplicateFrontendBindingOnSameAgent(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          1,
				AgentID:     "local",
				FrontendURL: "http://media.example.com/emby",
				BackendURL:  "http://127.0.0.1:8096",
				Enabled:     true,
				Revision:    2,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://media.example.com/emby/"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8097"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: frontend_url conflicts with existing rule: 1" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceUpdateRejectsDuplicateFrontendBindingOnSameAgent(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          1,
				AgentID:     "local",
				FrontendURL: "http://media.example.com/emby",
				BackendURL:  "http://127.0.0.1:8096",
				Enabled:     true,
				Revision:    2,
			}, {
				ID:          2,
				AgentID:     "local",
				FrontendURL: "http://media.example.com/jellyfin",
				BackendURL:  "http://127.0.0.1:8097",
				Enabled:     true,
				Revision:    3,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 2, HTTPRuleInput{
		FrontendURL: stringPtrRule("http://media.example.com/emby"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if err.Error() != "invalid argument: frontend_url conflicts with existing rule: 1" {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestRuleServiceCreateUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
			DesiredRevision:  4,
			CurrentRevision:  2,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://existing.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    4,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://new.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rule.Revision != 5 {
		t.Fatalf("Create() revision = %d", rule.Revision)
	}
	if store.agents[0].DesiredRevision != 5 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRuleServiceCreateDoesNotRegressRemoteDesiredRevisionBelowCurrentRevision(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules"]`,
			DesiredRevision:  4,
			CurrentRevision:  9,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "https://existing.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    4,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://new.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rule.Revision != 10 {
		t.Fatalf("Create() revision = %d", rule.Revision)
	}
	if store.agents[0].DesiredRevision != 10 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRuleServiceCreateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          1,
				AgentID:     "edge-1",
				FrontendURL: "http://existing.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    4,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://new.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if rule.Revision != 10 {
		t.Fatalf("Create() revision = %d", rule.Revision)
	}
	if store.agents[0].DesiredRevision != 10 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRuleServiceCreateReassignsPreferredIDWhenL4RuleAlreadyUsesIt(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          7,
				AgentID:     "local",
				FrontendURL: "http://existing-http.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    2,
			}},
		},
		l4RulesByAgent: map[string][]storage.L4RuleRow{
			"local": {{
				ID:         9,
				AgentID:    "local",
				ListenHost: "0.0.0.0",
				ListenPort: 25565,
				Revision:   3,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		ID:          intPtrRule(9),
		FrontendURL: stringPtrRule("http://new-http.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", rule.ID)
	}
}

func TestRuleServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:                1,
				AgentID:           "edge-1",
				FrontendURL:       "http://existing.example.com",
				BackendURL:        "http://127.0.0.1:8096",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				Enabled:           true,
				Revision:          4,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Update(context.Background(), "edge-1", 1, HTTPRuleInput{
		FrontendURL: stringPtrRule("http://updated.example.com"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rule.Revision != 10 {
		t.Fatalf("Update() revision = %d", rule.Revision)
	}
	if store.agents[0].DesiredRevision != 10 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRuleServiceDeleteUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
			DesiredRevision:  9,
			CurrentRevision:  9,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:                1,
				AgentID:           "edge-1",
				FrontendURL:       "http://existing.example.com",
				BackendURL:        "http://127.0.0.1:8096",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				Enabled:           true,
				Revision:          4,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if deleted.ID != 1 {
		t.Fatalf("Delete() id = %d", deleted.ID)
	}
	if store.agents[0].DesiredRevision != 10 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRuleServiceGetUsesDirectStoreLookup(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-1": {{
				ID:          7,
				AgentID:     "edge-1",
				FrontendURL: "https://lookup.example.com",
				BackendURL:  "http://127.0.0.1:8096",
				Revision:    3,
			}},
		},
		listHTTPRulesErr: errors.New("ListHTTPRules should not be used by Get"),
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Get(context.Background(), "edge-1", 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if rule.ID != 7 {
		t.Fatalf("Get() rule = %+v", rule)
	}
	if store.getHTTPRuleCalls != 1 {
		t.Fatalf("GetHTTPRule() calls = %d", store.getHTTPRuleCalls)
	}
}

func TestRuleServiceCreateHTTPSAutoCreatesManagedCertificateForLocalOrRemoteAgent(t *testing.T) {
	testCases := []struct {
		name    string
		agentID string
		agents  []storage.AgentRow
	}{
		{
			name:    "local",
			agentID: "local",
		},
		{
			name:    "remote",
			agentID: "edge-1",
			agents: []storage.AgentRow{{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeRuleStore{
				agents:       append([]storage.AgentRow(nil), tc.agents...),
				rulesByAgent: map[string][]storage.HTTPRuleRow{},
			}
			svc := NewRuleService(config.Config{
				EnableLocalAgent: true,
				LocalAgentID:     "local",
			}, store)

			created, err := svc.Create(context.Background(), tc.agentID, HTTPRuleInput{
				FrontendURL: stringPtrRule("https://media.example.com"),
				BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
				Tags:        &[]string{" media ", " edge "},
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if created.ID != 1 {
				t.Fatalf("Create() rule id = %d", created.ID)
			}
			if len(store.managedCerts) != 1 {
				t.Fatalf("managed cert count = %d", len(store.managedCerts))
			}

			cert := managedCertificateFromRow(store.managedCerts[0])
			if cert.Domain != "media.example.com" || !cert.Enabled || cert.Scope != "domain" {
				t.Fatalf("created cert mismatch = %+v", cert)
			}
			if cert.IssuerMode != "local_http01" {
				t.Fatalf("cert issuer_mode = %q", cert.IssuerMode)
			}
			if cert.Usage != "https" || cert.CertificateType != "acme" {
				t.Fatalf("cert usage/type = %s/%s", cert.Usage, cert.CertificateType)
			}
			if len(cert.TargetAgentIDs) != 1 || cert.TargetAgentIDs[0] != tc.agentID {
				t.Fatalf("cert target_agent_ids = %+v", cert.TargetAgentIDs)
			}
			if !containsString(cert.Tags, "auto") {
				t.Fatalf("cert tags missing auto: %+v", cert.Tags)
			}
			if !containsString(cert.Tags, "auto_target:"+tc.agentID) {
				t.Fatalf("cert tags missing auto_target: %+v", cert.Tags)
			}
			if !containsString(cert.Tags, "media") || !containsString(cert.Tags, "edge") {
				t.Fatalf("cert tags missing rule tags: %+v", cert.Tags)
			}
		})
	}
}

func TestRuleServiceCreateHTTPSPersistsManagedCertificateInSQLiteStore(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	created, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://sqlite.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != 1 {
		t.Fatalf("Create() rule id = %d", created.ID)
	}

	certs, err := store.ListManagedCertificates(context.Background())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("managed cert count = %d", len(certs))
	}

	cert := managedCertificateFromRow(certs[0])
	if cert.Domain != "sqlite.example.com" || cert.IssuerMode != "local_http01" || cert.Status != "pending" {
		t.Fatalf("persisted cert = %+v", cert)
	}
	if len(cert.TargetAgentIDs) != 1 || cert.TargetAgentIDs[0] != "local" {
		t.Fatalf("persisted target_agent_ids = %+v", cert.TargetAgentIDs)
	}
	if cert.Revision != 1 {
		t.Fatalf("persisted cert revision = %d", cert.Revision)
	}
}

func TestRuleServiceCreateAllocatesGlobalIDsAcrossAgentsInSQLiteStore(t *testing.T) {
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
			ID:         agentID,
			Name:       agentID,
			AgentToken: agentID + "-token",
		}); err != nil {
			t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
		}
	}

	svc := NewRuleService(config.Config{}, store)

	first, err := svc.Create(context.Background(), "agent-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-a.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(context.Background(), "agent-b", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-b.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
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

func TestRuleServiceCreateAllocatesIDsAfterExistingL4RulesInSQLiteStore(t *testing.T) {
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

	l4Svc := NewL4RuleService(config.Config{}, store)
	httpSvc := NewRuleService(config.Config{}, store)

	l4Rule, err := l4Svc.Create(context.Background(), "agent-a", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("backend-a.example.internal"),
		UpstreamPort: intPtrL4(9001),
	})
	if err != nil {
		t.Fatalf("Create L4 rule error = %v", err)
	}

	httpRule, err := httpSvc.Create(context.Background(), "agent-b", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-b.example.com"),
		BackendURL:  stringPtrRule("http://backend-b.example.internal:8096"),
	})
	if err != nil {
		t.Fatalf("Create HTTP rule error = %v", err)
	}

	if l4Rule.ID != 1 {
		t.Fatalf("l4Rule.ID = %d", l4Rule.ID)
	}
	if httpRule.ID != 2 {
		t.Fatalf("httpRule.ID = %d", httpRule.ID)
	}
}

func TestRuleServiceCreateHTTPRuleDoesNotProvisionManagedCertificate(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              1,
			Domain:          "existing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			TagsJSON:        `["manual"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
	}
	before := store.managedCerts[0]
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://plain.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	if store.managedCerts[0] != before {
		t.Fatalf("managed cert changed unexpectedly: before=%+v after=%+v", before, store.managedCerts[0])
	}
}

func TestRuleServiceCreateHTTPRuleDoesNotCleanupStaleAutoManagedCertificate(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              1,
				Domain:          "manual.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				TagsJSON:        `["manual"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        4,
			},
			{
				ID:              2,
				Domain:          "stale-auto.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				TagsJSON:        `["auto","auto_target:local"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        5,
			},
		},
	}
	before := append([]storage.ManagedCertificateRow(nil), store.managedCerts...)
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://plain.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != len(before) {
		t.Fatalf("managed cert count changed: before=%d after=%d", len(before), len(store.managedCerts))
	}
	for i := range before {
		if store.managedCerts[i] != before[i] {
			t.Fatalf("managed cert[%d] changed unexpectedly: before=%+v after=%+v", i, before[i], store.managedCerts[i])
		}
	}
}

func TestRuleServiceUpdateDoesNotCleanupAutoRelayListenerCertificate(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://relay.example.com",
				BackendURL:        "https://origin.example.com",
				BackendsJSON:      `[{"url":"https://origin.example.com"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				ProxyRedirect:     true,
				PassProxyHeaders:  false,
				Revision:          7,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              5,
				Domain:          "relay.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				TagsJSON:        `["manual"]`,
				Usage:           "https",
				CertificateType: "acme",
				Status:          "active",
				Revision:        10,
			},
			{
				ID:              6,
				Domain:          "relay-auto.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				TagsJSON:        `["relay","auto","auto:relay-listener","listener:1","agent:local"]`,
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				Status:          "active",
				Revision:        11,
			},
		},
	}
	before := append([]storage.ManagedCertificateRow(nil), store.managedCerts...)
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
		UserAgent: stringPtrRule("relay-check"),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if len(store.managedCerts) != len(before) {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	for i := range before {
		if store.managedCerts[i] != before[i] {
			t.Fatalf("managed cert[%d] changed unexpectedly: before=%+v after=%+v", i, before[i], store.managedCerts[i])
		}
	}
}

func TestRuleServiceCreateHTTPSReusesMatchingCertificateAndAddsAutoTarget(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              9,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["other-agent"]`,
			TagsJSON:        `["existing"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        8,
		}},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://media.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.ID != 9 {
		t.Fatalf("cert id = %d", cert.ID)
	}
	if !containsString(cert.TargetAgentIDs, "edge-1") || !containsString(cert.TargetAgentIDs, "other-agent") {
		t.Fatalf("target_agent_ids = %+v", cert.TargetAgentIDs)
	}
	if !containsString(cert.Tags, "auto_target:edge-1") {
		t.Fatalf("tags missing auto target = %+v", cert.Tags)
	}
	if cert.Revision != 9 {
		t.Fatalf("cert revision = %d", cert.Revision)
	}
}

func TestRuleServiceCreateHTTPSPrefersExactOverWildcardMatch(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","local_acme","cert_install"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              1,
				Domain:          "*.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["edge-1"]`,
				TagsJSON:        `["wildcard"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        100,
			},
			{
				ID:              2,
				Domain:          "media.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["another-agent"]`,
				TagsJSON:        `["exact"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        1,
			},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://media.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	exact := managedCertificateFromRow(store.managedCerts[1])
	if !containsString(exact.TargetAgentIDs, "edge-1") {
		t.Fatalf("exact cert target_agent_ids = %+v", exact.TargetAgentIDs)
	}
	wildcard := managedCertificateFromRow(store.managedCerts[0])
	if len(wildcard.TargetAgentIDs) != 1 || wildcard.TargetAgentIDs[0] != "edge-1" {
		t.Fatalf("wildcard cert should remain untouched: %+v", wildcard.TargetAgentIDs)
	}
}

func TestRuleServiceCreateHTTPSDomainUsesMasterCFDNSWhenManagedDNSEnabled(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)

	if _, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://cf-managed.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.IssuerMode != "master_cf_dns" {
		t.Fatalf("issuer_mode = %q", cert.IssuerMode)
	}
}

func TestRuleServiceCreateHTTPSRemoteDomainRejectsMasterCFDNSForNonLocalTarget(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://cf-managed.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "master_cf_dns certificates must target only the local master agent") {
		t.Fatalf("Create() error = %v", err)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
}

func TestRuleServiceCreateHTTPSRemoteDomainReusesExistingMasterCFDNSWildcardWithoutRetargeting(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              15,
			Domain:          "*.managed.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			TagsJSON:        `["wildcard"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        6,
		}},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://edge.managed.example.com"),
		BackendURL:  stringPtrRule("https://origin.example.net"),
		Backends:    &[]HTTPRuleBackend{{URL: "https://origin.example.net"}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Domain != "*.managed.example.com" {
		t.Fatalf("cert domain = %q", cert.Domain)
	}
	if len(cert.TargetAgentIDs) != 1 || cert.TargetAgentIDs[0] != "local" {
		t.Fatalf("target_agent_ids = %+v", cert.TargetAgentIDs)
	}
	if cert.Revision != 6 {
		t.Fatalf("cert revision = %d", cert.Revision)
	}
	if containsString(cert.Tags, managedCertificateAutoTargetTag("edge-1")) {
		t.Fatalf("tags unexpectedly include remote auto target: %+v", cert.Tags)
	}
}

func TestRuleServiceCreateHTTPSDomainFallsBackToLocalHTTP01WhenManagedDNSDisabled(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://local-http01.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.IssuerMode != "local_http01" {
		t.Fatalf("issuer_mode = %q", cert.IssuerMode)
	}
}

func TestRuleServiceCreateHTTPSIPRequiresLocalACME(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://192.168.1.10"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "local ACME issuance for IP HTTPS") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateHTTPSIPUsesLocalHTTP01WhenAgentSupportsLocalACME(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://192.168.1.10"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Scope != "ip" {
		t.Fatalf("scope = %q", cert.Scope)
	}
	if cert.IssuerMode != "local_http01" {
		t.Fatalf("issuer_mode = %q", cert.IssuerMode)
	}
	if cert.Domain != "192.168.1.10" {
		t.Fatalf("domain = %q", cert.Domain)
	}
}

func TestRuleServiceCreateHTTPSIPv6LiteralUsesLocalHTTP01WhenAgentSupportsLocalACME(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://[2001:db8::10]"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Scope != "ip" {
		t.Fatalf("scope = %q", cert.Scope)
	}
	if cert.IssuerMode != "local_http01" {
		t.Fatalf("issuer_mode = %q", cert.IssuerMode)
	}
	if cert.Domain != "2001:db8::10" {
		t.Fatalf("domain = %q", cert.Domain)
	}
}

func TestRuleServiceCreateHTTPSDomainFailsWhenNoIssuerAvailable(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			Name:             "Edge 1",
			CapabilitiesJSON: `["http_rules","cert_install"]`,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://no-issuer.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "no available unified certificate issuer for no-issuer.example.com") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceUpdateHTTPSCleanupDetachesOrDeletesManagedCertificate(t *testing.T) {
	t.Run("detaches when not fully auto", func(t *testing.T) {
		store := &fakeRuleStore{
			agents: []storage.AgentRow{{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
			}},
			rulesByAgent: map[string][]storage.HTTPRuleRow{
				"edge-1": {{
					ID:                1,
					AgentID:           "edge-1",
					FrontendURL:       "https://media.example.com",
					BackendURL:        "http://127.0.0.1:8096",
					BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
					LoadBalancingJSON: `{"strategy":"round_robin"}`,
					Enabled:           true,
					TagsJSON:          `[]`,
					ProxyRedirect:     true,
					RelayChainJSON:    `[]`,
					PassProxyHeaders:  true,
					UserAgent:         "",
					CustomHeadersJSON: `[]`,
					Revision:          4,
				}},
			},
			managedCerts: []storage.ManagedCertificateRow{{
				ID:              11,
				Domain:          "media.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["edge-1"]`,
				TagsJSON:        `["manual","auto_target:edge-1"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        6,
			}},
		}
		svc := NewRuleService(config.Config{
			EnableLocalAgent: true,
			LocalAgentID:     "local",
		}, store)

		if _, err := svc.Update(context.Background(), "edge-1", 1, HTTPRuleInput{
			FrontendURL: stringPtrRule("http://media.example.com"),
		}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		if len(store.managedCerts) != 1 {
			t.Fatalf("managed cert count = %d", len(store.managedCerts))
		}
		cert := managedCertificateFromRow(store.managedCerts[0])
		if len(cert.TargetAgentIDs) != 0 {
			t.Fatalf("target_agent_ids = %+v", cert.TargetAgentIDs)
		}
		if containsString(cert.Tags, "auto_target:edge-1") {
			t.Fatalf("auto_target tag should be removed, got %+v", cert.Tags)
		}
		if !containsString(cert.Tags, "manual") {
			t.Fatalf("manual tag should remain, got %+v", cert.Tags)
		}
	})

	t.Run("deletes when fully auto", func(t *testing.T) {
		store := &fakeRuleStore{
			agents: []storage.AgentRow{{
				ID:               "edge-1",
				Name:             "Edge 1",
				CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
			}},
			rulesByAgent: map[string][]storage.HTTPRuleRow{
				"edge-1": {{
					ID:                1,
					AgentID:           "edge-1",
					FrontendURL:       "https://media.example.com",
					BackendURL:        "http://127.0.0.1:8096",
					BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
					LoadBalancingJSON: `{"strategy":"round_robin"}`,
					Enabled:           true,
					TagsJSON:          `[]`,
					ProxyRedirect:     true,
					RelayChainJSON:    `[]`,
					PassProxyHeaders:  true,
					UserAgent:         "",
					CustomHeadersJSON: `[]`,
					Revision:          4,
				}},
			},
			managedCerts: []storage.ManagedCertificateRow{{
				ID:              11,
				Domain:          "media.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["edge-1"]`,
				TagsJSON:        `["auto","auto_target:edge-1"]`,
				Usage:           "https",
				CertificateType: "acme",
				Revision:        6,
			}},
		}
		svc := NewRuleService(config.Config{
			EnableLocalAgent: true,
			LocalAgentID:     "local",
		}, store)

		if _, err := svc.Delete(context.Background(), "edge-1", 1); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if len(store.managedCerts) != 0 {
			t.Fatalf("managed cert count = %d", len(store.managedCerts))
		}
	})
}

func TestRuleServiceCleanupIgnoresDisabledAndNonHTTPSRulesForCertRetention(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://media.example.com",
				BackendURL:        "http://127.0.0.1:8096",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `[]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[]`,
				PassProxyHeaders:  true,
				UserAgent:         "",
				CustomHeadersJSON: `[]`,
				Revision:          3,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              7,
			Domain:          "media.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			TagsJSON:        `["auto","auto_target:local"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        10,
		}},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
		Enabled: boolPtrRule(false),
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
}

func TestRuleServiceCreateRollsBackManagedCertificatesWhenRuleSaveFails(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent:      map[string][]storage.HTTPRuleRow{},
		saveHTTPRulesErrs: []error{errors.New("save rules failed")},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://rollback.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed certs should roll back, got %d rows", len(store.managedCerts))
	}
}

func TestRuleServiceUpdateRollbackPreservesManagedCertificateMaterial(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://stale-auto.example.com",
				BackendURL:        "http://127.0.0.1:8096",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `[]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[]`,
				PassProxyHeaders:  true,
				CustomHeadersJSON: `[]`,
				Revision:          7,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              3,
			Domain:          "stale-auto.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			TagsJSON:        `["auto","auto_target:local"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        8,
		}},
		materialByDomain: map[string]bool{
			"stale-auto.example.com": true,
		},
		saveHTTPRulesErrs: []error{errors.New("save rules failed")},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
		FrontendURL: stringPtrRule("http://stale-auto.example.com"),
	})
	if err == nil {
		t.Fatal("Update() error = nil")
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed certs should roll back, got %d rows", len(store.managedCerts))
	}
	if store.managedCerts[0].Domain != "stale-auto.example.com" {
		t.Fatalf("managed cert domain after rollback = %q", store.managedCerts[0].Domain)
	}
	if !store.materialByDomain["stale-auto.example.com"] {
		t.Fatalf("material was deleted during rollback path")
	}
	if store.cleanupCallCount != 0 {
		t.Fatalf("cleanup should not run on rollback path, cleanupCallCount = %d", store.cleanupCallCount)
	}
}

func TestRuleServiceDeleteSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://stale-auto.example.com",
				BackendURL:        "http://127.0.0.1:8096",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `[]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[]`,
				PassProxyHeaders:  true,
				CustomHeadersJSON: `[]`,
				Revision:          7,
			}},
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              3,
			Domain:          "stale-auto.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			TagsJSON:        `["auto","auto_target:local"]`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        8,
		}},
		cleanupErrs: []error{errors.New("cleanup failed")},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("Delete() id = %d", deleted.ID)
	}
	if len(store.rulesByAgent["local"]) != 0 {
		t.Fatalf("rules still persisted after delete: %+v", store.rulesByAgent["local"])
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed certs should remain committed despite cleanup failure, got %d rows", len(store.managedCerts))
	}
}

func stringPtrRule(value string) *string {
	return &value
}

func intPtrRule(value int) *int {
	return &value
}

func boolPtrRule(value bool) *bool {
	return &value
}

func popRuleStoreError(queue *[]error) error {
	if len(*queue) == 0 {
		return nil
	}
	err := (*queue)[0]
	*queue = (*queue)[1:]
	return err
}
