package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeL4Store struct {
	agents       []storage.AgentRow
	l4RulesByID  map[string][]storage.L4RuleRow
	relayByAgent map[string][]storage.RelayListenerRow
}

func (f *fakeL4Store) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), f.agents...), nil
}

func (f *fakeL4Store) ListHTTPRules(context.Context, string) ([]storage.HTTPRuleRow, error) {
	return nil, nil
}

func (f *fakeL4Store) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), f.l4RulesByID[agentID]...), nil
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
	return storage.Snapshot{}, nil
}

func (f *fakeL4Store) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (f *fakeL4Store) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return nil, nil
}

func (f *fakeL4Store) SaveAgent(context.Context, storage.AgentRow) error {
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

func TestL4RuleServiceCreateRejectsRelayChainForUDP(t *testing.T) {
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
		Protocol:     stringPtrL4("udp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayChain:   &[]int{7},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_chain is only supported for tcp protocol" {
		t.Fatalf("Create() error = %v", err)
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

func TestL4RuleServiceUpdateClearsRelayObfsWhenSwitchingToUDP(t *testing.T) {
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
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be cleared for udp, got %+v", rule.RelayChain)
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

func stringPtrL4(value string) *string {
	return &value
}

func intPtrL4(value int) *int {
	return &value
}

func boolPtrL4(value bool) *bool {
	return &value
}
