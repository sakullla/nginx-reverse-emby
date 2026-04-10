package service

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeStore struct {
	agents     []storage.AgentRow
	rulesByID  map[string][]storage.HTTPRuleRow
	localState storage.LocalAgentStateRow
}

func (f fakeStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f fakeStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.rulesByID[agentID]...), nil
}

func (f fakeStore) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return f.localState, nil
}

func TestAgentServiceListSynthesizesLocalAgentAndRemoteStatus(t *testing.T) {
	cfg := config.Config{
		EnableLocalAgent:  true,
		LocalAgentID:      "local",
		LocalAgentName:    "Local Agent",
		HeartbeatInterval: 30 * time.Second,
	}

	now := time.Date(2026, time.April, 10, 22, 0, 0, 0, time.UTC)
	svc := NewAgentService(cfg, fakeStore{
		agents: []storage.AgentRow{{
			ID:                "edge-1",
			Name:              "Edge 1",
			AgentURL:          "http://edge-1:8080",
			Version:           "1.2.3",
			Platform:          "linux-amd64",
			DesiredVersion:    "1.2.3",
			TagsJSON:          `["edge"]`,
			CapabilitiesJSON:  `["http_rules"]`,
			Mode:              "pull",
			DesiredRevision:   4,
			CurrentRevision:   3,
			LastApplyRevision: 3,
			LastApplyStatus:   "success",
			LastApplyMessage:  "",
			LastSeenAt:        now.Add(-15 * time.Second).Format(time.RFC3339),
			LastSeenIP:        "10.0.0.5",
		}},
		rulesByID: map[string][]storage.HTTPRuleRow{
			"local":  {{ID: 1}},
			"edge-1": {{ID: 1}, {ID: 2}},
		},
		localState: storage.LocalAgentStateRow{
			DesiredRevision:   7,
			CurrentRevision:   7,
			LastApplyRevision: 7,
			LastApplyStatus:   "success",
			LastApplyMessage:  "",
			DesiredVersion:    "1.2.3",
		},
	})
	svc.now = func() time.Time { return now }

	agents, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("len(agents) = %d", len(agents))
	}

	if agents[0].ID != "local" || !agents[0].IsLocal || agents[0].Status != "online" {
		t.Fatalf("local agent = %+v", agents[0])
	}
	if agents[0].HTTPRulesCount != 1 {
		t.Fatalf("local HTTPRulesCount = %d", agents[0].HTTPRulesCount)
	}

	if agents[1].ID != "edge-1" || agents[1].Status != "online" {
		t.Fatalf("remote agent = %+v", agents[1])
	}
	if agents[1].HTTPRulesCount != 2 || agents[1].LastSeenIP != "10.0.0.5" {
		t.Fatalf("remote agent counts/ip = %+v", agents[1])
	}
}

func TestAgentServiceListHTTPRulesNormalizesStoredFields(t *testing.T) {
	svc := NewAgentService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, fakeStore{
		rulesByID: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				FrontendURL:       "https://emby.example.com",
				BackendURL:        "http://emby:8096",
				BackendsJSON:      `[]`,
				LoadBalancingJSON: `{}`,
				Enabled:           true,
				TagsJSON:          `["media"]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[1,2]`,
				PassProxyHeaders:  true,
				UserAgent:         "",
				CustomHeadersJSON: `[{"name":"X-Test","value":"1"}]`,
				Revision:          9,
			}},
		},
	})

	rules, err := svc.ListHTTPRules(context.Background(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d", len(rules))
	}

	rule := rules[0]
	if len(rule.Backends) != 1 || rule.Backends[0].URL != "http://emby:8096" {
		t.Fatalf("Backends = %+v", rule.Backends)
	}
	if rule.LoadBalancing.Strategy != "round_robin" {
		t.Fatalf("LoadBalancing = %+v", rule.LoadBalancing)
	}
	if len(rule.CustomHeaders) != 1 || rule.CustomHeaders[0].Name != "X-Test" {
		t.Fatalf("CustomHeaders = %+v", rule.CustomHeaders)
	}
}
