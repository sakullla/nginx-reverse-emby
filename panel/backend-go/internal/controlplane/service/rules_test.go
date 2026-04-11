package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeRuleStore struct {
	agents       []storage.AgentRow
	rulesByAgent map[string][]storage.HTTPRuleRow
	listeners    []storage.RelayListenerRow
}

func (f *fakeRuleStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeRuleStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.rulesByAgent[agentID]...), nil
}

func (f *fakeRuleStore) SaveHTTPRules(_ context.Context, agentID string, rows []storage.HTTPRuleRow) error {
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
		RelayChain:       &[]int{-1, 0, 7},
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
		RelayChain:    &[]int{5, -10, 6},
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
	if rule.LoadBalancing.Strategy != "round_robin" {
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

func TestRuleServiceCreateUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "Edge 1",
			DesiredRevision: 4,
			CurrentRevision: 2,
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
		FrontendURL: stringPtrRule("https://new.example.com"),
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

func stringPtrRule(value string) *string {
	return &value
}

func boolPtrRule(value bool) *bool {
	return &value
}
