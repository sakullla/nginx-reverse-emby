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
	agents       []storage.AgentRow
	rulesByAgent map[string][]storage.HTTPRuleRow
	listeners    []storage.RelayListenerRow
	managedCerts []storage.ManagedCertificateRow

	saveHTTPRulesErrs []error
	saveManagedErrs   []error
}

func (f *fakeRuleStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeRuleStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), f.rulesByAgent[agentID]...), nil
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

func (f *fakeRuleStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	if err := popRuleStoreError(&f.saveManagedErrs); err != nil {
		return err
	}
	f.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
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

func stringPtrRule(value string) *string {
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
