//go:build !integration

package service

import (
	"context"
	"errors"
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

func (*fakeL4Store) allowUngovernedMutationsForTests() {}

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
	ctx := authenticatedServiceMutationContext(t)
	svc := NewL4RuleService(testConfig(), store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baselineRevisions, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(ctx, "local", L4RuleInput{
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
	revisions, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after create error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+1 {
		t.Fatalf("revision count after create = %d, want baseline + 1 (%d)", len(revisions), len(baselineRevisions)+1)
	}
	createRevision := revisions[len(revisions)-1]
	if _, found, err := store.GetOperationDependencyArtifact(ctx, createRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() after create error = %v", err)
	} else if !found {
		t.Fatal("create dependency plan artifact was not persisted")
	}
	updated, err := svc.Update(ctx, "local", created.ID, L4RuleInput{
		Tags: &[]string{"updated"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "updated" {
		t.Fatalf("Update() tags = %v, want [updated]", updated.Tags)
	}
	revisions, err = store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after update error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+2 {
		t.Fatalf("revision count after update = %d, want baseline + 2 (%d)", len(revisions), len(baselineRevisions)+2)
	}

	if _, err := svc.Delete(ctx, "local", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls after delete = %d, want 0", applyCalls)
	}
	revisions, err = store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() after delete error = %v", err)
	}
	if len(revisions) != len(baselineRevisions)+3 {
		t.Fatalf("revision count after delete = %d, want baseline + 3 (%d)", len(revisions), len(baselineRevisions)+3)
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

func stringPtrL4(value string) *string {
	return &value
}

func intPtrL4(value int) *int {
	return &value
}

func boolPtrL4(value bool) *bool {
	return &value
}
