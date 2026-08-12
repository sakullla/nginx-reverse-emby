package service

import (
	"context"
	"errors"
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
	ctx := authenticatedServiceMutationContext(t)

	first, err := svc.Create(ctx, "agent-a", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "upstream-a", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(ctx, "agent-b", L4RuleInput{
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

func stringPtrL4(value string) *string {
	return &value
}

func intPtrL4(value int) *int {
	return &value
}

func boolPtrL4(value bool) *bool {
	return &value
}
