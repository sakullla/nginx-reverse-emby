package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeL4Store struct {
	agents        []storage.AgentRow
	httpRulesByID map[string][]storage.HTTPRuleRow
	l4RulesByID   map[string][]storage.L4RuleRow
	relayByAgent  map[string][]storage.RelayListenerRow

	egressProfiles    []storage.EgressProfileRow
	savedAgent        storage.AgentRow
	loadSnapshotCalls int
	listL4RulesErr    error
	listL4RulesErrs   []error
	saveL4RulesErr    error
	saveAgentErrs     []error

	getL4RuleCalls    int
	trafficDeletes    []trafficScopeDeleteCall
	trafficDeleteErr  error
	trafficDeleteHook func()
}

func (*fakeL4Store) allowLegacyConfigMutationFallback() {}

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
	if err := popRuleStoreError(&f.listL4RulesErrs); err != nil {
		return nil, err
	}
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

func (f *fakeL4Store) ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error) {
	return append([]storage.EgressProfileRow(nil), f.egressProfiles...), nil
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
	if err := popRuleStoreError(&f.saveAgentErrs); err != nil {
		return err
	}
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
	if f.saveL4RulesErr != nil {
		return f.saveL4RulesErr
	}
	f.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (f *fakeL4Store) SaveRelayListeners(context.Context, string, []storage.RelayListenerRow) error {
	return nil
}

func (f *fakeL4Store) SaveEgressProfiles(_ context.Context, rows []storage.EgressProfileRow) error {
	f.egressProfiles = append([]storage.EgressProfileRow(nil), rows...)
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

func (f *fakeL4Store) DeleteTrafficByScope(_ context.Context, agentID, scopeType, scopeID string) (int64, error) {
	f.trafficDeletes = append(f.trafficDeletes, trafficScopeDeleteCall{
		agentID:   agentID,
		scopeType: scopeType,
		scopeID:   scopeID,
	})
	if f.trafficDeleteHook != nil {
		f.trafficDeleteHook()
	}
	if f.trafficDeleteErr != nil {
		return 0, f.trafficDeleteErr
	}
	return 0, nil
}

func newL4RuleServiceTestStore(t *testing.T) *fakeL4Store {
	t.Helper()
	return &fakeL4Store{
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		relayByAgent:  map[string][]storage.RelayListenerRow{},
	}
}

func l4StoreAgentByID(t *testing.T, store *fakeL4Store, agentID string) storage.AgentRow {
	t.Helper()
	for _, row := range store.agents {
		if row.ID == agentID {
			return row
		}
	}
	t.Fatalf("agent %q not found", agentID)
	return storage.AgentRow{}
}

func TestL4RuleServiceCRUDUsesRevisionMutationWithoutSynchronousApply(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	svc := NewL4RuleService(testConfig(), store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baselineRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenHost: stringPtrL4("127.0.0.1"),
		ListenPort: intPtrL4(19090),
		Backends:   &[]L4Backend{{Host: "127.0.0.1", Port: 8096}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls after create = %d, want 0", applyCalls)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after create error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+1 {
		t.Fatalf("revision count after create = %d, want baseline + 1 (%d)", len(revisions), len(baselineRevisions)+1)
	}
	createRevision := revisions[len(revisions)-1]
	if _, found, err := store.GetOperationDependencyArtifact(t.Context(), createRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() after create error = %v", err)
	} else if !found {
		t.Fatal("create dependency plan artifact was not persisted")
	}
	updated, err := svc.Update(t.Context(), "local", created.ID, L4RuleInput{
		Tags: &[]string{"updated"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "updated" {
		t.Fatalf("Update() tags = %v, want [updated]", updated.Tags)
	}
	revisions, err = store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after update error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+2 {
		t.Fatalf("revision count after update = %d, want baseline + 2 (%d)", len(revisions), len(baselineRevisions)+2)
	}

	if _, err := svc.Delete(t.Context(), "local", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls after delete = %d, want 0", applyCalls)
	}
	revisions, err = store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after delete error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+3 {
		t.Fatalf("revision count after delete = %d, want baseline + 3 (%d)", len(revisions), len(baselineRevisions)+3)
	}
}

func TestL4RuleServiceCreateRejectsUDPHTTPProxyEgressProfile(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 20, Name: "http", Type: "http", ProxyURL: "http://127.0.0.1:8080", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:        stringPtrL4("udp"),
		ListenPort:      intPtrL4(5353),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 53}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "UDP") {
		t.Fatalf("Create() error = %v, want UDP HTTP profile validation", err)
	}
}

func TestL4RuleServiceCreateAcceptsUDPSOCKSEgressProfile(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 21, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	rule, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:        stringPtrL4("udp"),
		ListenPort:      intPtrL4(5353),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 53}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.EgressProfileID == nil || *rule.EgressProfileID != profileID {
		t.Fatalf("EgressProfileID = %v, want %d", rule.EgressProfileID, profileID)
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 1 || rows[0].EgressProfileID == nil || *rows[0].EgressProfileID != profileID {
		t.Fatalf("persisted EgressProfileID = %+v, want %d", rows, profileID)
	}
}

func TestL4RuleMutationRejectsUnauthorizedReferencedEgressProfile(t *testing.T) {
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 22, Name: "hidden", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	wantErr := errors.New("hidden referenced resource")
	ctx := WithResourceAuthorizer(t.Context(), func(_ context.Context, kind, id string) error {
		if kind == "egress_profile" && id == "22" {
			return wantErr
		}
		return nil
	})
	_, err := svc.Create(ctx, "local", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(5354),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 53}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Create() error = %v, want referenced resource denial", err)
	}
	if len(store.l4RulesByID["local"]) != 0 {
		t.Fatalf("persisted rules = %+v, want none", store.l4RulesByID["local"])
	}
}

func TestL4RuleServiceCreateRejectsEgressProfileWhenRemoteExecutorLacksCapability(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		relayByAgent:  map[string][]storage.RelayListenerRow{},
	}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 27, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(t.Context(), "edge-a", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(9000),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 9001}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "agent does not support egress profiles") {
		t.Fatalf("Create() error = %v, want egress profile capability validation", err)
	}
}

func TestL4RuleServiceCreateRejectsRelayedEgressProfileWhenFinalHopLacksCapability(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4", "egress_profiles"}),
		}, {
			ID:               "relay-a",
			Name:             "Relay A",
			CapabilitiesJSON: marshalStringArray([]string{"relay_quic"}),
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"relay-a": {{
				ID:            8,
				AgentID:       "relay-a",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				ListenPort:    9443,
				PublicHost:    "relay-a.example.test",
				PublicPort:    9443,
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 28, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(t.Context(), "edge-a", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(9000),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 9001}},
		RelayLayers:     &[][]int{{8}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "Relay A") || !strings.Contains(err.Error(), "egress profiles") {
		t.Fatalf("Create() error = %v, want relay final-hop egress profile capability validation", err)
	}
}

func TestL4RuleServiceCreateBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4", "egress_profiles"}),
			DesiredRevision:  4,
			CurrentRevision:  4,
		}, {
			ID:               "relay-a",
			Name:             "Relay A",
			CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
			DesiredRevision:  10,
			CurrentRevision:  10,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"relay-a": {{
				ID:            8,
				AgentID:       "relay-a",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				ListenPort:    9443,
				PublicHost:    "relay-a.example.test",
				PublicPort:    9443,
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 29, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(t.Context(), "edge-a", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(9000),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 9001}},
		RelayLayers:     &[][]int{{8}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if row := l4StoreAgentByID(t, store, "relay-a"); row.DesiredRevision != 20 {
		t.Fatalf("relay-a DesiredRevision = %d, want egress profile revision 20", row.DesiredRevision)
	}
}

func TestL4RuleServiceUpdateBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4", "egress_profiles"}),
			DesiredRevision:  4,
			CurrentRevision:  4,
		}, {
			ID:               "relay-a",
			Name:             "Relay A",
			CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
			DesiredRevision:  10,
			CurrentRevision:  10,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-a": {{
				ID:           1,
				AgentID:      "edge-a",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				BackendsJSON: `[{"host":"127.0.0.1","port":9001}]`,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"relay-a": {{
				ID:            8,
				AgentID:       "relay-a",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				ListenPort:    9443,
				PublicHost:    "relay-a.example.test",
				PublicPort:    9443,
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 30, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(t.Context(), "edge-a", 1, L4RuleInput{
		RelayLayers:     &[][]int{{8}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if row := l4StoreAgentByID(t, store, "relay-a"); row.DesiredRevision != 20 {
		t.Fatalf("relay-a DesiredRevision = %d, want egress profile revision 20", row.DesiredRevision)
	}
}

func TestL4RuleServiceUpdateBumpsPreviousRelayedEgressProfileFinalHopWhenCleared(t *testing.T) {
	t.Parallel()
	profileID := 32
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4", "egress_profiles"}),
			DesiredRevision:  4,
			CurrentRevision:  4,
		}, {
			ID:               "relay-a",
			Name:             "Relay A",
			CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
			DesiredRevision:  10,
			CurrentRevision:  10,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-a": {{
				ID:              1,
				AgentID:         "edge-a",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"127.0.0.1","port":9001}]`,
				RelayLayersJSON: `[[8]]`,
				EgressProfileID: &profileID,
				Enabled:         true,
				Revision:        4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"relay-a": {{
				ID:            8,
				AgentID:       "relay-a",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				ListenPort:    9443,
				PublicHost:    "relay-a.example.test",
				PublicPort:    9443,
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	seedEgressProfile(t, store, storage.EgressProfileRow{ID: profileID, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(t.Context(), "edge-a", 1, L4RuleInput{
		EgressProfileID: intPtrL4(0),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if row := l4StoreAgentByID(t, store, "relay-a"); row.DesiredRevision <= 10 {
		t.Fatalf("relay-a DesiredRevision = %d, want bumped above current revision 10", row.DesiredRevision)
	}
}

func TestL4RuleServiceDeleteBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	profileID := 31
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-a",
			Name:             "Edge A",
			CapabilitiesJSON: marshalStringArray([]string{"l4", "egress_profiles"}),
			DesiredRevision:  4,
			CurrentRevision:  4,
		}, {
			ID:               "relay-a",
			Name:             "Relay A",
			CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
			DesiredRevision:  10,
			CurrentRevision:  10,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-a": {{
				ID:              1,
				AgentID:         "edge-a",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"127.0.0.1","port":9001}]`,
				RelayLayersJSON: `[[8]]`,
				EgressProfileID: &profileID,
				Enabled:         true,
				Revision:        4,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"relay-a": {{
				ID:            8,
				AgentID:       "relay-a",
				Name:          "relay-a",
				ListenHost:    "127.0.0.1",
				ListenPort:    9443,
				PublicHost:    "relay-a.example.test",
				PublicPort:    9443,
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	seedEgressProfile(t, store, storage.EgressProfileRow{ID: profileID, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Delete(t.Context(), "edge-a", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if row := l4StoreAgentByID(t, store, "relay-a"); row.DesiredRevision <= 10 {
		t.Fatalf("relay-a DesiredRevision = %d, want bumped above current revision 10", row.DesiredRevision)
	}
}

func TestL4RuleServiceCreateRejectsTCPHiddenStoredEgressProfile(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 24, Name: "bogus", Type: "bogus", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(8443),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 8096}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress profile 24 not found") {
		t.Fatalf("Create() error = %v, want hidden egress profile to be unavailable", err)
	}
}

func TestL4RuleServiceCreateRejectsUDPHiddenStoredEgressProfile(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 25, Name: "bogus", Type: "bogus", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:        stringPtrL4("udp"),
		ListenPort:      intPtrL4(5353),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 53}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress profile 25 not found") {
		t.Fatalf("Create() error = %v, want hidden egress profile to be unavailable", err)
	}
}

func TestL4RuleServiceCreateRejectsNegativeEgressProfileID(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", L4RuleInput{
		Protocol:        stringPtrL4("tcp"),
		ListenPort:      intPtrL4(8443),
		Backends:        &[]L4Backend{{Host: "127.0.0.1", Port: 8096}},
		EgressProfileID: intPtrL4(-1),
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress_profile_id") {
		t.Fatalf("Create() error = %v, want negative egress_profile_id validation", err)
	}
}

func TestL4RuleServiceUpdateRejectsDisabledEgressProfile(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	store.l4RulesByID["local"] = []storage.L4RuleRow{{
		ID:                1,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[]`,
		ListenMode:        "tcp",
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          1,
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 22, Name: "off", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: false})
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Update(t.Context(), "local", 1, L4RuleInput{EgressProfileID: &profileID})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Update() error = %v, want disabled egress profile validation", err)
	}
}

func TestL4RuleServiceUpdateAcceptsEnabledHTTPEgressProfileForTCP(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	store.l4RulesByID["local"] = []storage.L4RuleRow{{
		ID:                1,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[]`,
		ListenMode:        "tcp",
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          1,
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 23, Name: "http", Type: "http", ProxyURL: "http://127.0.0.1:8080", Enabled: true})
	svc := NewL4RuleService(testConfig(), store)
	rule, err := svc.Update(t.Context(), "local", 1, L4RuleInput{EgressProfileID: &profileID})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.EgressProfileID == nil || *rule.EgressProfileID != profileID {
		t.Fatalf("EgressProfileID = %v, want %d", rule.EgressProfileID, profileID)
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 1 || rows[0].EgressProfileID == nil || *rows[0].EgressProfileID != profileID {
		t.Fatalf("persisted EgressProfileID = %+v, want %d", rows, profileID)
	}
}

func TestL4RuleServiceUpdateRejectsNegativeEgressProfileID(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	store.l4RulesByID["local"] = []storage.L4RuleRow{{
		ID:                1,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[]`,
		ListenMode:        "tcp",
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          1,
	}}
	svc := NewL4RuleService(testConfig(), store)
	_, err := svc.Update(t.Context(), "local", 1, L4RuleInput{EgressProfileID: intPtrL4(-1)})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress_profile_id") {
		t.Fatalf("Update() error = %v, want negative egress_profile_id validation", err)
	}
}

func TestL4RuleServiceUpdateClearsEgressProfileWithZero(t *testing.T) {
	t.Parallel()
	store := newL4RuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 26, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	store.l4RulesByID["local"] = []storage.L4RuleRow{{
		ID:                1,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[]`,
		ListenMode:        "tcp",
		EgressProfileID:   &profileID,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          1,
	}}
	svc := NewL4RuleService(testConfig(), store)
	rule, err := svc.Update(t.Context(), "local", 1, L4RuleInput{EgressProfileID: intPtrL4(0)})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.EgressProfileID != nil {
		t.Fatalf("EgressProfileID = %v, want nil", rule.EgressProfileID)
	}
	if rows := store.l4RulesByID["local"]; len(rows) != 1 || rows[0].EgressProfileID != nil {
		t.Fatalf("persisted EgressProfileID = %+v, want nil", rows)
	}
}

func TestL4RuleServiceCreateAllowsRelayLayersForUDP(t *testing.T) {
	t.Parallel()
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
		Protocol:    stringPtrL4("udp"),
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}, {8, 9}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("RelayChain = %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 2 || len(rule.RelayLayers[1]) != 2 || rule.RelayLayers[1][1] != 9 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
	if got := store.l4RulesByID["local"][0].RelayLayersJSON; got != `[[7],[8,9]]` {
		t.Fatalf("persisted relay_layers = %s", got)
	}
	if got := store.l4RulesByID["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
	if row := store.l4RulesByID["local"][0]; row.UpstreamHost != "" || row.UpstreamPort != 0 {
		t.Fatalf("persisted upstream fields = %q:%d", row.UpstreamHost, row.UpstreamPort)
	}
}

func TestL4RuleServiceCreateRejectsUpstreamOnlyForTCPMode(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceUpdateRejectsUpstreamOnlyForTCPMode(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           1,
				AgentID:      "local",
				Name:         "tcp",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				BackendsJSON: `[{"host":"upstream","port":9001}]`,
				Enabled:      true,
				Revision:     3,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		UpstreamHost: stringPtrL4("other-upstream"),
		UpstreamPort: intPtrL4(9002),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleFromRowDoesNotSynthesizeLegacyBackendFields(t *testing.T) {
	t.Parallel()
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:             1,
		AgentID:        "local",
		Protocol:       "tcp",
		ListenHost:     "0.0.0.0",
		ListenPort:     9000,
		UpstreamHost:   "legacy",
		UpstreamPort:   9001,
		RelayChainJSON: `[7]`,
		Enabled:        true,
	})

	if rule.UpstreamHost != "" || rule.UpstreamPort != 0 || len(rule.Backends) != 0 {
		t.Fatalf("legacy upstream fields were synthesized: upstream=%q:%d backends=%+v", rule.UpstreamHost, rule.UpstreamPort, rule.Backends)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("legacy relay_chain was synthesized: %+v", rule.RelayChain)
	}
}

func TestL4RuleJSONOmitsLegacyFields(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(L4Rule{
		ID:           1,
		AgentID:      "local",
		Name:         "tcp",
		Protocol:     "tcp",
		ListenHost:   "0.0.0.0",
		ListenPort:   25565,
		UpstreamHost: "legacy",
		UpstreamPort: 25566,
		Backends:     []L4Backend{{Host: "upstream", Port: 25567}},
		RelayChain:   []int{7},
		RelayLayers:  [][]int{{7}},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("json.Marshal(L4Rule) error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("json.Unmarshal(L4Rule) error = %v", err)
	}
	for _, key := range []string{"upstream_host", "upstream_port", "relay_chain"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("L4Rule JSON exposed legacy field %q: %s", key, raw)
		}
	}
	if _, ok := payload["backends"]; !ok {
		t.Fatalf("L4Rule JSON missing canonical backends: %s", raw)
	}
	if _, ok := payload["relay_layers"]; !ok {
		t.Fatalf("L4Rule JSON missing canonical relay_layers: %s", raw)
	}
}

func TestL4RuleServiceCreateRejectsRelayChainOnly(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"local": {{ID: 7, AgentID: "local", Enabled: true}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayChain: &[]int{7},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceCreatePreservesRelayObfsForRelayLayersOnly(t *testing.T) {
	t.Parallel()
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
		Protocol:    stringPtrL4("tcp"),
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
		RelayObfs:   boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be preserved for relay_layers-only rule")
	}
}

func TestL4RuleServiceCreateNormalizesLoadBalancingStrategies(t *testing.T) {
	t.Parallel()
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
				Backends:      &[]L4Backend{{Host: "upstream", Port: 9001}},
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
	t.Parallel()
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

func TestL4RuleFromRowClearsProxyEntryFieldsForTCPMode(t *testing.T) {
	t.Parallel()
	rule := l4RuleFromRow(storage.L4RuleRow{
		ID:                 1,
		AgentID:            "local",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         9000,
		UpstreamHost:       "upstream",
		UpstreamPort:       9001,
		ListenMode:         " TCP ",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
	})

	if rule.ListenMode != "tcp" {
		t.Fatalf("ListenMode = %q", rule.ListenMode)
	}
	if rule.ProxyEntryAuth != (L4ProxyEntryAuth{}) {
		t.Fatalf("ProxyEntryAuth = %+v, want cleared", rule.ProxyEntryAuth)
	}
}

func TestNormalizeL4RuleInputAcceptsProxyEntryRelayLayers(t *testing.T) {
	t.Parallel()
	protocol := "tcp"
	listenMode := "proxy"
	relayLayers := [][]int{{101}}
	input := L4RuleInput{
		Protocol:       &protocol,
		ListenHost:     stringPtrL4("127.0.0.1"),
		ListenPort:     intPtrL4(1080),
		ListenMode:     &listenMode,
		ProxyEntryAuth: &L4ProxyEntryAuth{Enabled: true, Username: "u", Password: "p"},
		RelayLayers:    &relayLayers,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ListenMode != "proxy" || len(rule.RelayLayers) != 1 || rule.RelayLayers[0][0] != 101 {
		t.Fatalf("proxy entry relay layers = %+v", rule)
	}
	if !rule.ProxyEntryAuth.Enabled || rule.ProxyEntryAuth.Username != "u" || rule.ProxyEntryAuth.Password != "p" {
		t.Fatalf("ProxyEntryAuth = %+v", rule.ProxyEntryAuth)
	}
}

func TestNormalizeL4RuleInputAllowsUDPProxyEntryRelayLayers(t *testing.T) {
	t.Parallel()
	protocol := "udp"
	listenMode := "proxy"
	relayLayers := [][]int{{101}}
	input := L4RuleInput{
		Protocol:    &protocol,
		ListenHost:  stringPtrL4("127.0.0.1"),
		ListenPort:  intPtrL4(1080),
		ListenMode:  &listenMode,
		RelayLayers: &relayLayers,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.Protocol != "udp" || rule.ListenMode != "proxy" || len(rule.RelayLayers) != 1 || rule.RelayLayers[0][0] != 101 {
		t.Fatalf("rule = %+v, want udp proxy relay entry", rule)
	}
}

func TestNormalizeL4RuleInputAllowsProxyUDPForSOCKS5(t *testing.T) {
	t.Parallel()
	rule, err := normalizeL4RuleInput(L4RuleInput{
		Name:       stringPtrL4("udp proxy"),
		Protocol:   stringPtrL4("udp"),
		ListenMode: stringPtrL4("proxy"),
		ListenPort: intPtrL4(1080),
	}, L4Rule{}, 8)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.Protocol != "udp" || rule.ListenMode != "proxy" {
		t.Fatalf("rule = %+v, want udp proxy entry", rule)
	}
}

func TestEnsureUniqueL4ListenRejectsUDPProxyEntryWithoutSamePortTCP(t *testing.T) {
	t.Parallel()
	next := L4Rule{
		ID:         2,
		Name:       "udp",
		Protocol:   "udp",
		ListenMode: "proxy",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		Enabled:    true,
	}
	err := ensureUniqueL4Listen([]L4Rule{{
		ID:         1,
		Name:       "unrelated tcp",
		Protocol:   "tcp",
		ListenMode: "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 8080,
		Enabled:    true,
	}}, next, 0)
	if err == nil || !strings.Contains(err.Error(), "same-port TCP SOCKS5 proxy entry") {
		t.Fatalf("ensureUniqueL4Listen() error = %v, want same-port TCP SOCKS5 proxy entry rejection", err)
	}
}

func TestEnsureUniqueL4ListenIgnoresDisabledUDPProxyEntryWithoutSamePortTCP(t *testing.T) {
	t.Parallel()
	next := L4Rule{
		ID:         2,
		Name:       "udp",
		Protocol:   "udp",
		ListenMode: "proxy",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		Enabled:    false,
	}
	err := ensureUniqueL4Listen([]L4Rule{{
		ID:         1,
		Name:       "unrelated tcp",
		Protocol:   "tcp",
		ListenMode: "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 8080,
		Enabled:    true,
	}}, next, 0)
	if err != nil {
		t.Fatalf("ensureUniqueL4Listen() error = %v, want disabled UDP proxy entry to be ignored", err)
	}
}

func TestValidateL4RuleSetRejectsUDPProxyEntryWithoutSamePortTCP(t *testing.T) {
	t.Parallel()
	err := validateL4RuleSet([]L4Rule{{
		ID:         2,
		Name:       "udp",
		Protocol:   "udp",
		ListenMode: "proxy",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		Enabled:    true,
	}})
	if err == nil || !strings.Contains(err.Error(), "same-port TCP SOCKS5 proxy entry") {
		t.Fatalf("validateL4RuleSet() error = %v, want same-port TCP SOCKS5 proxy entry rejection", err)
	}
}

func TestValidateL4RuleSetIgnoresDisabledUDPProxyEntryWithoutSamePortTCP(t *testing.T) {
	t.Parallel()
	err := validateL4RuleSet([]L4Rule{{
		ID:         2,
		Name:       "udp",
		Protocol:   "udp",
		ListenMode: "proxy",
		ListenHost: "0.0.0.0",
		ListenPort: 1080,
		Enabled:    false,
	}})
	if err != nil {
		t.Fatalf("validateL4RuleSet() error = %v, want disabled UDP proxy entry to be ignored", err)
	}
}

func TestL4RuleServiceUpdateRejectsDisablingTCPProxyControlWhenUDPDependsOnIt(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			CapabilitiesJSON: `["l4"]`,
			DesiredRevision:  4,
			CurrentRevision:  4,
		}},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:                 1,
				AgentID:            "edge-1",
				Name:               "tcp control",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
				Enabled:            true,
				Revision:           4,
			}, {
				ID:           2,
				AgentID:      "edge-1",
				Name:         "udp data",
				Protocol:     "udp",
				ListenHost:   "0.0.0.0",
				ListenPort:   1080,
				BackendsJSON: `[]`,
				ListenMode:   "proxy",
				Enabled:      true,
				Revision:     4,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "edge-1", 1, L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(1080),
		ListenHost: stringPtrL4("0.0.0.0"),
		ListenMode: stringPtrL4("proxy"),
		Enabled:    boolPtrL4(false),
	})
	if err == nil || !strings.Contains(err.Error(), "same-port TCP SOCKS5 proxy entry") {
		t.Fatalf("Update() error = %v, want same-port TCP SOCKS5 proxy entry rejection", err)
	}
	if len(store.l4RulesByID["edge-1"]) != 2 {
		t.Fatalf("l4 rules after rejected update = %+v, want unchanged pair", store.l4RulesByID["edge-1"])
	}
	if store.agents[0].DesiredRevision != 4 {
		t.Fatalf("remote desired_revision = %d, want unchanged 4", store.agents[0].DesiredRevision)
	}
}

func TestL4RuleServiceUpdateProxyEntryClearsBackendFields(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           1,
				AgentID:      "local",
				Name:         "forwarding rule",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   9000,
				UpstreamHost: "upstream",
				UpstreamPort: 9001,
				BackendsJSON: `[{"host":"upstream","port":9001}]`,
				Enabled:      true,
				Revision:     3,
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

	listenMode := "proxy"
	rule, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		ListenMode:  &listenMode,
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(rule.Backends) != 0 || rule.UpstreamHost != "" || rule.UpstreamPort != 0 {
		t.Fatalf("rule backend fields = backends=%+v upstream=%q:%d", rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	}
	row := store.l4RulesByID["local"][0]
	if row.BackendsJSON != "[]" || row.UpstreamHost != "" || row.UpstreamPort != 0 {
		t.Fatalf("persisted backend fields = backends=%s upstream=%q:%d", row.BackendsJSON, row.UpstreamHost, row.UpstreamPort)
	}
}

func TestL4RuleServiceCreateAllocatesGlobalIDsAcrossAgentsInSQLiteStore(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStore(t, dataRoot, "local")
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
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream-a", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9100),
		Backends:   &[]L4Backend{{Host: "upstream-b", Port: 9101}},
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
	t.Parallel()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStore(t, dataRoot, "local")
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://backend-a.example.internal:8096"}},
	})
	if err != nil {
		t.Fatalf("Create HTTP rule error = %v", err)
	}

	l4Rule, err := l4Svc.Create(context.Background(), "agent-b", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9100),
		Backends:   &[]L4Backend{{Host: "backend-b.example.internal", Port: 9101}},
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
	t.Parallel()
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayObfs:  boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared when relay_chain is empty")
	}
}

func TestL4RuleServiceCreateDetachesCanceledTriggerContext(t *testing.T) {
	t.Parallel()
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
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
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
	t.Parallel()
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:   stringPtrL4("udp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayObfs:  boolPtrL4(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceUpdateClearsRelayObfsWhenRelayChainRemoved(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				RelayObfs:       true,
				Enabled:         true,
				Revision:        3,
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
		RelayLayers: &[][]int{},
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

func TestL4RuleServiceUpdateRejectsRelayChainOnly(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
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

	_, err := svc.Update(context.Background(), "local", 1, L4RuleInput{
		RelayChain: &[]int{5},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestL4RuleServiceUpdateClearsRelayChainWhenRelayLayersSupplied(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
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
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
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
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:                1,
				AgentID:           "local",
				Name:              "relay rule",
				Protocol:          "tcp",
				ListenHost:        "0.0.0.0",
				ListenPort:        9000,
				BackendsJSON:      `[{"host":"upstream","port":9001}]`,
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
	t.Parallel()
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
						BackendsJSON:      `[{"host":"upstream","port":9001}]`,
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

func TestL4RuleServiceUpdatePreservesRelayLayersWhenSwitchingToUDP(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				Name:            "relay rule",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9000,
				BackendsJSON:    `[{"host":"upstream","port":9001}]`,
				RelayLayersJSON: `[[7]]`,
				RelayObfs:       true,
				Enabled:         true,
				Revision:        3,
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
	if len(rule.RelayChain) != 0 {
		t.Fatalf("expected relay_chain to be neutral, got %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("expected relay_layers to be preserved for udp, got %+v", rule.RelayLayers)
	}
	if rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be cleared for udp protocol")
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayLayerEntries(t *testing.T) {
	t.Parallel()
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
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not contain duplicates" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsDuplicateRelayLayerEntriesAcrossLayers(t *testing.T) {
	t.Parallel()
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
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 8}, {7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not repeat listener IDs across layers" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsUnknownRelayLayerListener(t *testing.T) {
	t.Parallel()
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
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7, 8}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay listener not found: 8" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateAllowsCrossAgentTLSRelayListener(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents:      []storage.AgentRow{{ID: "remote-relay", Name: "remote-relay", CapabilitiesJSON: `["l4"]`}},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		relayByAgent: map[string][]storage.RelayListenerRow{
			"remote-relay": {{
				ID:            7,
				AgentID:       "remote-relay",
				Enabled:       true,
				TransportMode: "tls_tcp",
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		ListenPort:  intPtrL4(9000),
		Backends:    &[]L4Backend{{Host: "upstream", Port: 9001}},
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestL4RuleServiceDeleteRollsBackRuleWhenAllocatorFailsAfterSave(t *testing.T) {
	t.Parallel()
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
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
				Enabled:      true,
				Revision:     4,
			}},
		},
		relayByAgent:    map[string][]storage.RelayListenerRow{},
		listL4RulesErrs: []error{nil, errors.New("allocator list l4 failed")},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "edge-1", 1)
	if err == nil {
		t.Fatal("Delete() error = nil")
	}
	got := store.l4RulesByID["edge-1"]
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("l4 rules after allocator failure = %+v, want original rule", got)
	}
}

func TestL4RuleServiceDeleteUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	t.Parallel()
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
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
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

func TestL4RuleServiceDeleteRejectsTCPProxyControlWhenUDPDependsOnIt(t *testing.T) {
	t.Parallel()
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
				ID:                 1,
				AgentID:            "edge-1",
				Name:               "tcp control",
				Protocol:           "tcp",
				ListenHost:         "0.0.0.0",
				ListenPort:         1080,
				BackendsJSON:       `[]`,
				ListenMode:         "proxy",
				ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
				Enabled:            true,
				Revision:           4,
			}, {
				ID:           2,
				AgentID:      "edge-1",
				Name:         "udp data",
				Protocol:     "udp",
				ListenHost:   "0.0.0.0",
				ListenPort:   1080,
				BackendsJSON: `[]`,
				ListenMode:   "proxy",
				Enabled:      true,
				Revision:     4,
			}},
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "edge-1", 1)
	if err == nil || !strings.Contains(err.Error(), "same-port TCP SOCKS5 proxy entry") {
		t.Fatalf("Delete() error = %v, want same-port TCP SOCKS5 proxy entry rejection", err)
	}
	if len(store.l4RulesByID["edge-1"]) != 2 {
		t.Fatalf("l4 rules after rejected delete = %+v, want unchanged pair", store.l4RulesByID["edge-1"])
	}
	if store.agents[0].DesiredRevision != 4 {
		t.Fatalf("remote desired_revision = %d, want unchanged 4", store.agents[0].DesiredRevision)
	}
}

func TestL4RuleServiceDeleteCascadesL4RuleTraffic(t *testing.T) {
	t.Parallel()
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "edge-1",
			CapabilitiesJSON: `["l4"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-1": {{
				ID:           12,
				AgentID:      "edge-1",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
				Enabled:      true,
			}},
		},
		relayByAgent: map[string][]storage.RelayListenerRow{},
	}
	svc := NewL4RuleService(config.Config{LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "edge-1", 12); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.trafficDeletes) != 1 {
		t.Fatalf("traffic deletes = %+v, want one scope delete", store.trafficDeletes)
	}
	if got := store.trafficDeletes[0]; got != (trafficScopeDeleteCall{agentID: "edge-1", scopeType: "l4_rule", scopeID: "12"}) {
		t.Fatalf("traffic delete = %+v", got)
	}
}

func TestL4RuleServiceDeleteTrafficCleanupIsBestEffortAfterApply(t *testing.T) {
	t.Parallel()
	order := []string{}
	store := &fakeL4Store{
		agents: []storage.AgentRow{{
			ID:               "local",
			CapabilitiesJSON: `["l4"]`,
		}},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"local": {{
				ID:           13,
				AgentID:      "local",
				Protocol:     "tcp",
				ListenHost:   "0.0.0.0",
				ListenPort:   50381,
				UpstreamHost: "127.0.0.1",
				UpstreamPort: 26966,
				Enabled:      true,
			}},
		},
		relayByAgent:     map[string][]storage.RelayListenerRow{},
		trafficDeleteErr: errors.New("cleanup failed"),
		trafficDeleteHook: func() {
			order = append(order, "cleanup")
		},
	}
	svc := NewL4RuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		order = append(order, "apply")
		return nil
	})

	if _, err := svc.Delete(context.Background(), "local", 13); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(order) != 2 || order[0] != "apply" || order[1] != "cleanup" {
		t.Fatalf("order = %+v, want apply then cleanup", order)
	}
}

func TestL4RuleServiceCreateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
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
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(50382),
		Backends:   &[]L4Backend{{Host: "127.0.0.1", Port: 26967}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Create() revision", rule.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, rule.Revision)
}

func TestL4RuleServiceCreateReassignsPreferredIDWhenHTTPRuleAlreadyUsesIt(t *testing.T) {
	t.Parallel()
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
		ID:         intPtrL4(9),
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(50382),
		Backends:   &[]L4Backend{{Host: "127.0.0.1", Port: 26967}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", rule.ID)
	}
}

func TestL4RuleServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
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
				BackendsJSON: `[{"host":"127.0.0.1","port":26966}]`,
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
	t.Parallel()
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
	t.Parallel()
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
