package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeRuleStore struct {
	agents             []storage.AgentRow
	rulesByAgent       map[string][]storage.HTTPRuleRow
	l4RulesByAgent     map[string][]storage.L4RuleRow
	wireGuardByAgentID map[string][]storage.WireGuardProfileRow
	egressProfiles     []storage.EgressProfileRow
	listeners          []storage.RelayListenerRow
	managedCerts       []storage.ManagedCertificateRow

	listHTTPRulesErr  error
	saveHTTPRulesErrs []error
	saveManagedErrs   []error
	saveAgentErrs     []error
	cleanupErrs       []error
	materialByDomain  map[string]bool
	cleanupCallCount  int
	getHTTPRuleCalls  int
	trafficDeletes    []trafficScopeDeleteCall
	trafficDeleteErr  error
	trafficDeleteHook func()
}

func (*fakeRuleStore) allowLegacyConfigMutationFallback() {}

type revisionIncapableRuleStore struct {
	ruleStore
}

func TestRuleServiceRejectsRevisionIncapableStoreWithoutWritesOrApply(t *testing.T) {
	t.Parallel()
	legacy := &fakeRuleStore{
		rulesByAgent:       map[string][]storage.HTTPRuleRow{"local": {}},
		l4RulesByAgent:     map[string][]storage.L4RuleRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewRuleService(testConfig(), &revisionIncapableRuleStore{ruleStore: legacy})
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return nil
	})

	_, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://revision-store-required.example.test"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err == nil || !strings.Contains(err.Error(), "revision mutation store") {
		t.Fatalf("Create() error = %v, want revision mutation store error", err)
	}
	if len(legacy.rulesByAgent["local"]) != 0 {
		t.Fatalf("rules after rejected create = %+v", legacy.rulesByAgent["local"])
	}
	if applyCalls != 0 {
		t.Fatalf("synchronous apply calls = %d, want 0", applyCalls)
	}
}

type trafficScopeDeleteCall struct {
	agentID   string
	scopeType string
	scopeID   string
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

func (f *fakeRuleStore) ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error) {
	return append([]storage.EgressProfileRow(nil), f.egressProfiles...), nil
}

func (f *fakeRuleStore) SaveEgressProfiles(_ context.Context, rows []storage.EgressProfileRow) error {
	f.egressProfiles = append([]storage.EgressProfileRow(nil), rows...)
	return nil
}

func (f *fakeRuleStore) SaveHTTPRules(_ context.Context, agentID string, rows []storage.HTTPRuleRow) error {
	if err := popRuleStoreError(&f.saveHTTPRulesErrs); err != nil {
		return err
	}
	f.rulesByAgent[agentID] = append([]storage.HTTPRuleRow(nil), rows...)
	return nil
}

func (f *fakeRuleStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	if err := popRuleStoreError(&f.saveAgentErrs); err != nil {
		return err
	}
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

func (f *fakeRuleStore) DeleteTrafficByScope(_ context.Context, agentID, scopeType, scopeID string) (int64, error) {
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

func ruleStoreAgentByID(t *testing.T, store *fakeRuleStore, agentID string) storage.AgentRow {
	t.Helper()
	for _, row := range store.agents {
		if row.ID == agentID {
			return row
		}
	}
	t.Fatalf("agent %q not found", agentID)
	return storage.AgentRow{}
}

func newRuleServiceTestStore(t *testing.T) *fakeRuleStore {
	t.Helper()
	return &fakeRuleStore{
		rulesByAgent:       map[string][]storage.HTTPRuleRow{},
		l4RulesByAgent:     map[string][]storage.L4RuleRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
}

func testConfig() config.Config {
	return config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
}

func TestRuleServiceCRUDUsesRevisionMutationWithoutSynchronousApply(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 1, Domain: "sub.zouter.skl.onl", Enabled: true, Scope: "domain", IssuerMode: "local_http01",
		TargetAgentIDs: `["local"]`, Status: "active", Usage: "https", CertificateType: "acme", Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	svc := NewRuleService(testConfig(), store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baselineRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://sub.zouter.skl.onl"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	updated, err := svc.Update(t.Context(), "local", created.ID, HTTPRuleInput{
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

	deleted, err := svc.Delete(t.Context(), "local", created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("Delete() id = %d, want %d", deleted.ID, created.ID)
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
	deleteRevision := revisions[len(revisions)-1]
	if _, found, err := store.GetOperationDependencyArtifact(t.Context(), deleteRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() after delete error = %v", err)
	} else if !found {
		t.Fatal("delete dependency plan artifact was not persisted")
	}
}

func TestRuleServiceCreateIgnoresUnsupportedStoredSharedResources(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	ctx := t.Context()

	if err := store.SaveL4Rules(ctx, "local", []storage.L4RuleRow{{
		ID: 91, AgentID: "local", Name: "legacy tunnel", Protocol: "tcp",
		ListenHost: "10.91.0.1", ListenPort: 0, ListenMode: "wireguard",
		WireGuardProfileID: intPtrRule(191), Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := store.SaveRelayListeners(ctx, "local", []storage.RelayListenerRow{{
		ID: 92, AgentID: "local", Name: "legacy relay", ListenHost: "10.92.0.1",
		ListenPort: 51820, PublicHost: "legacy.example.test", PublicPort: 51820,
		TransportMode: "wireguard", WireGuardProfileID: intPtrRule(192), Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveEgressProfiles(ctx, []storage.EgressProfileRow{{
		ID: 93, Name: "legacy egress", Type: "wireguard", WireGuardConfigJSON: `{}`,
		Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	intent, err := store.LoadLocalIntentSnapshot(ctx, "local")
	if err != nil {
		t.Fatalf("LoadLocalIntentSnapshot() error = %v", err)
	}
	if len(intent.L4Rules) == 0 || len(intent.RelayListeners) == 0 {
		t.Fatalf("intent snapshot omitted seeded unsupported rows: %+v", intent)
	}
	if intent.L4Rules[0].ListenMode != "wireguard" || intent.RelayListeners[0].TransportMode != "wireguard" {
		t.Fatalf("intent snapshot normalized unsupported rows unexpectedly: %+v", intent)
	}
	if intent.L4Rules[0].ListenPort != 0 || len(intent.L4Rules[0].Backends) != 0 {
		t.Fatalf("intent snapshot changed unsupported L4 payload unexpectedly: %+v", intent.L4Rules[0])
	}
	if err := validateSnapshotResources(intent); err != nil {
		t.Fatalf("validateSnapshotResources() with unsupported stored resources error = %v", err)
	}
	if err := (FullSnapshotValidator{}).Validate(ctx, revision.SnapshotValidation{
		Target:   revision.Target{AgentID: "local", Capabilities: defaultLocalCapabilities},
		Snapshot: intent,
	}); err != nil {
		t.Fatalf("Validate() with unsupported stored resources error = %v", err)
	}

	svc := NewRuleService(testConfig(), store)
	created, err := svc.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://ordinary-after-legacy.example.test:18084"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err != nil {
		t.Fatalf("Create() with unsupported stored resources error = %v", err)
	}
	if created.ID <= 0 || created.WireGuardEntryEnabled || created.WireGuardProfileID != nil {
		t.Fatalf("Create() result = %+v, want ordinary rule without retired fields", created)
	}
}

func TestRuleServiceCrossAgentRelayPlanAndMissingDependencyAreAtomic(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "relay-edge", Name: "relay-edge", Platform: "linux-amd64", CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-edge", []storage.RelayListenerRow{{
		ID: 70, AgentID: "relay-edge", Name: "relay", ListenHost: "127.0.0.1", ListenPort: 17443,
		PublicHost: "relay-edge.example.test", PublicPort: 17443, Enabled: true, TransportMode: "tls_tcp", Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	svc := NewRuleService(testConfig(), store)
	created, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://relay-plan.example.test:18082"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers: &[][]int{{70}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	localRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local) error = %v", err)
	}
	relayRevisions, err := store.ListAgentRevisions(t.Context(), "relay-edge")
	if err != nil {
		t.Fatalf("ListAgentRevisions(relay-edge) error = %v", err)
	}
	localRevision := localRevisions[len(localRevisions)-1]
	relayRevision := relayRevisions[len(relayRevisions)-1]
	if localRevision.OperationID == "" || relayRevision.OperationID != localRevision.OperationID {
		t.Fatalf("operation ids local=%q relay=%q, want one multi-agent operation", localRevision.OperationID, relayRevision.OperationID)
	}
	if _, found, err := store.GetOperationDependencyArtifact(t.Context(), localRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", err)
	} else if !found {
		t.Fatal("multi-agent dependency plan artifact was not persisted")
	}

	beforeLocalRevisionCount := len(localRevisions)
	_, err = svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://missing-relay.example.test:18083"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers: &[][]int{{999}},
	})
	if err == nil {
		t.Fatal("Create() with missing relay error = nil")
	}
	rows, listErr := store.ListHTTPRules(t.Context(), "local")
	if listErr != nil {
		t.Fatalf("ListHTTPRules() error = %v", listErr)
	}
	if len(rows) != 1 || rows[0].ID != created.ID {
		t.Fatalf("HTTP rules after rejected mutation = %+v, want only rule %d", rows, created.ID)
	}
	localRevisions, err = store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(local) after rejection error = %v", err)
	}
	if len(localRevisions) != beforeLocalRevisionCount {
		t.Fatalf("local revision count after rejected mutation = %d, want %d", len(localRevisions), beforeLocalRevisionCount)
	}
}

type egressProfileSeedStore interface {
	SaveEgressProfiles(context.Context, []storage.EgressProfileRow) error
}

func seedEgressProfile(t *testing.T, store egressProfileSeedStore, row storage.EgressProfileRow) int {
	t.Helper()
	if err := store.SaveEgressProfiles(context.Background(), []storage.EgressProfileRow{row}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	return row.ID
}

func TestRuleServiceCreateRejectsDisabledEgressProfile(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 17, Name: "off", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: false})
	svc := NewRuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL:     stringPtrRule("https://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Create() error = %v, want disabled egress profile validation", err)
	}
}

func TestRuleServiceCreateAcceptsEnabledSOCKSEgressProfile(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 18, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewRuleService(testConfig(), store)
	rule, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL:     stringPtrRule("https://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.EgressProfileID == nil || *rule.EgressProfileID != profileID {
		t.Fatalf("EgressProfileID = %v, want %d", rule.EgressProfileID, profileID)
	}
	if rows := store.rulesByAgent["local"]; len(rows) != 1 || rows[0].EgressProfileID == nil || *rows[0].EgressProfileID != profileID {
		t.Fatalf("persisted EgressProfileID = %+v, want %d", rows, profileID)
	}
}

func TestRuleServiceCreateRejectsEgressProfileWhenRemoteExecutorLacksCapability(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules"}),
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 22, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Create(t.Context(), "edge-a", HTTPRuleInput{
		FrontendURL:     stringPtrRule("http://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "agent does not support egress profiles") {
		t.Fatalf("Create() error = %v, want egress profile capability validation", err)
	}
}

func TestRuleServiceCreateRejectsRelayedEgressProfileWhenFinalHopLacksCapability(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules", "egress_profiles"}),
	}, {
		ID:               "relay-a",
		Name:             "Relay A",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic"}),
	}}
	store.listeners = []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 23, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Create(t.Context(), "edge-a", HTTPRuleInput{
		FrontendURL:     stringPtrRule("http://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers:     &[][]int{{7}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "Relay A") || !strings.Contains(err.Error(), "egress profiles") {
		t.Fatalf("Create() error = %v, want relay final-hop egress profile capability validation", err)
	}
}

func TestRuleServiceCreateBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules", "egress_profiles"}),
		DesiredRevision:  4,
		CurrentRevision:  4,
	}, {
		ID:               "relay-a",
		Name:             "Relay A",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
		DesiredRevision:  10,
		CurrentRevision:  10,
	}}
	store.listeners = []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 24, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Create(t.Context(), "edge-a", HTTPRuleInput{
		FrontendURL:     stringPtrRule("http://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers:     &[][]int{{7}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if row := ruleStoreAgentByID(t, store, "relay-a"); row.DesiredRevision != 20 {
		t.Fatalf("relay-a DesiredRevision = %d, want egress profile revision 20", row.DesiredRevision)
	}
}

func TestRuleServiceUpdateBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules", "egress_profiles"}),
		DesiredRevision:  4,
		CurrentRevision:  4,
	}, {
		ID:               "relay-a",
		Name:             "Relay A",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
		DesiredRevision:  10,
		CurrentRevision:  10,
	}}
	store.rulesByAgent["edge-a"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "edge-a",
		FrontendURL:       "http://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[]`,
		PassProxyHeaders:  true,
		CustomHeadersJSON: `[]`,
		Revision:          4,
	}}
	store.listeners = []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 25, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Update(t.Context(), "edge-a", 1, HTTPRuleInput{
		RelayLayers:     &[][]int{{7}},
		EgressProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if row := ruleStoreAgentByID(t, store, "relay-a"); row.DesiredRevision != 20 {
		t.Fatalf("relay-a DesiredRevision = %d, want egress profile revision 20", row.DesiredRevision)
	}
}

func TestRuleServiceUpdateBumpsPreviousRelayedEgressProfileFinalHopWhenCleared(t *testing.T) {
	t.Parallel()
	profileID := 27
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules", "egress_profiles"}),
		DesiredRevision:  4,
		CurrentRevision:  4,
	}, {
		ID:               "relay-a",
		Name:             "Relay A",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
		DesiredRevision:  10,
		CurrentRevision:  10,
	}}
	store.rulesByAgent["edge-a"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "edge-a",
		FrontendURL:       "http://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[[7]]`,
		PassProxyHeaders:  true,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &profileID,
		Revision:          4,
	}}
	store.listeners = []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}
	seedEgressProfile(t, store, storage.EgressProfileRow{ID: profileID, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Update(t.Context(), "edge-a", 1, HTTPRuleInput{
		EgressProfileID: intPtrRule(0),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if row := ruleStoreAgentByID(t, store, "relay-a"); row.DesiredRevision <= 10 {
		t.Fatalf("relay-a DesiredRevision = %d, want bumped above current revision 10", row.DesiredRevision)
	}
}

func TestRuleServiceDeleteBumpsRelayedEgressProfileFinalHopRevision(t *testing.T) {
	t.Parallel()
	profileID := 26
	store := newRuleServiceTestStore(t)
	store.agents = []storage.AgentRow{{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: marshalStringArray([]string{"http_rules", "egress_profiles"}),
		DesiredRevision:  4,
		CurrentRevision:  4,
	}, {
		ID:               "relay-a",
		Name:             "Relay A",
		CapabilitiesJSON: marshalStringArray([]string{"relay_quic", "egress_profiles"}),
		DesiredRevision:  10,
		CurrentRevision:  10,
	}}
	store.rulesByAgent["edge-a"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "edge-a",
		FrontendURL:       "http://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[[7]]`,
		PassProxyHeaders:  true,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &profileID,
		Revision:          4,
	}}
	store.listeners = []storage.RelayListenerRow{{
		ID:            7,
		AgentID:       "relay-a",
		Name:          "relay-a",
		ListenHost:    "127.0.0.1",
		ListenPort:    9443,
		PublicHost:    "relay-a.example.test",
		PublicPort:    9443,
		Enabled:       true,
		TransportMode: "tls_tcp",
	}}
	seedEgressProfile(t, store, storage.EgressProfileRow{ID: profileID, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true, Revision: 20})
	svc := NewRuleService(testConfig(), store)

	_, err := svc.Delete(t.Context(), "edge-a", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if row := ruleStoreAgentByID(t, store, "relay-a"); row.DesiredRevision <= 10 {
		t.Fatalf("relay-a DesiredRevision = %d, want bumped above current revision 10", row.DesiredRevision)
	}
}

func TestRuleServiceCreateRejectsUnsupportedEgressProfileType(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 20, Name: "bogus", Type: "bogus", Enabled: true})
	svc := NewRuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL:     stringPtrRule("https://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		EgressProfileID: &profileID,
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "does not support HTTP rules") {
		t.Fatalf("Create() error = %v, want unsupported egress profile type validation", err)
	}
}

func TestRuleServiceCreateRejectsNegativeEgressProfileID(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	svc := NewRuleService(testConfig(), store)
	_, err := svc.Create(t.Context(), "local", HTTPRuleInput{
		FrontendURL:     stringPtrRule("https://media.example.test"),
		Backends:        &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		EgressProfileID: intPtrRule(-1),
	})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress_profile_id") {
		t.Fatalf("Create() error = %v, want negative egress_profile_id validation", err)
	}
}

func TestRuleServiceUpdateRejectsUnknownEgressProfile(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.rulesByAgent["local"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		ProxyRedirect:     true,
		PassProxyHeaders:  true,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		RelayLayersJSON:   `[]`,
		Revision:          1,
	}}
	profileID := 404
	svc := NewRuleService(testConfig(), store)
	_, err := svc.Update(t.Context(), "local", 1, HTTPRuleInput{EgressProfileID: &profileID})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Update() error = %v, want unknown egress profile validation", err)
	}
}

func TestRuleServiceUpdateAcceptsEnabledHTTPEgressProfile(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.rulesByAgent["local"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		ProxyRedirect:     true,
		PassProxyHeaders:  true,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		RelayLayersJSON:   `[]`,
		Revision:          1,
	}}
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 19, Name: "http", Type: "http", ProxyURL: "http://127.0.0.1:8080", Enabled: true})
	svc := NewRuleService(testConfig(), store)
	rule, err := svc.Update(t.Context(), "local", 1, HTTPRuleInput{EgressProfileID: &profileID})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.EgressProfileID == nil || *rule.EgressProfileID != profileID {
		t.Fatalf("EgressProfileID = %v, want %d", rule.EgressProfileID, profileID)
	}
	if rows := store.rulesByAgent["local"]; len(rows) != 1 || rows[0].EgressProfileID == nil || *rows[0].EgressProfileID != profileID {
		t.Fatalf("persisted EgressProfileID = %+v, want %d", rows, profileID)
	}
}

func TestRuleServiceUpdateRejectsNegativeEgressProfileID(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	store.rulesByAgent["local"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		ProxyRedirect:     true,
		PassProxyHeaders:  true,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		RelayLayersJSON:   `[]`,
		Revision:          1,
	}}
	svc := NewRuleService(testConfig(), store)
	_, err := svc.Update(t.Context(), "local", 1, HTTPRuleInput{EgressProfileID: intPtrRule(-1)})
	if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), "egress_profile_id") {
		t.Fatalf("Update() error = %v, want negative egress_profile_id validation", err)
	}
}

func TestRuleServiceUpdateClearsEgressProfileWithZero(t *testing.T) {
	t.Parallel()
	store := newRuleServiceTestStore(t)
	profileID := seedEgressProfile(t, store, storage.EgressProfileRow{ID: 21, Name: "socks", Type: "socks", ProxyURL: "socks5://127.0.0.1:1080", Enabled: true})
	store.rulesByAgent["local"] = []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://media.example.test",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		ProxyRedirect:     true,
		PassProxyHeaders:  true,
		TagsJSON:          `[]`,
		CustomHeadersJSON: `[]`,
		RelayLayersJSON:   `[]`,
		EgressProfileID:   &profileID,
		Revision:          1,
	}}
	svc := NewRuleService(testConfig(), store)
	rule, err := svc.Update(t.Context(), "local", 1, HTTPRuleInput{EgressProfileID: intPtrRule(0)})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if rule.EgressProfileID != nil {
		t.Fatalf("EgressProfileID = %v, want nil", rule.EgressProfileID)
	}
	if rows := store.rulesByAgent["local"]; len(rows) != 1 || rows[0].EgressProfileID != nil {
		t.Fatalf("persisted EgressProfileID = %+v, want nil", rows)
	}
}

func TestRuleServiceCreateNormalizesAndPersists(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{
			{ID: 7, AgentID: "local", Enabled: true, Revision: 1},
			{ID: 8, AgentID: "local", Enabled: true, Revision: 1},
			{ID: 9, AgentID: "local", Enabled: true, Revision: 1},
		},
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
		RelayLayers:      &[][]int{{7}, {8, 9}},
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
	if rule.BackendURL != "" || len(rule.Backends) != 1 || rule.Backends[0].URL != "http://upstream-a:8096" {
		t.Fatalf("Create() backends = %+v", rule.Backends)
	}
	if rule.LoadBalancing.Strategy != "random" {
		t.Fatalf("Create() load_balancing = %+v", rule.LoadBalancing)
	}
	if len(rule.Tags) != 1 || rule.Tags[0] != "edge" {
		t.Fatalf("Create() tags = %+v", rule.Tags)
	}
	if len(rule.RelayLayers) != 2 || len(rule.RelayLayers[1]) != 2 || rule.RelayLayers[1][1] != 9 {
		t.Fatalf("Create() relay_layers = %+v", rule.RelayLayers)
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
	if got := store.rulesByAgent["local"][1].RelayLayersJSON; got != `[[7],[8,9]]` {
		t.Fatalf("persisted relay_layers = %s", got)
	}
	if got := store.rulesByAgent["local"][1].BackendURL; got != "" {
		t.Fatalf("persisted backend_url = %q", got)
	}
	if got := store.rulesByAgent["local"][1].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestRuleServiceCreateRejectsBackendURLOnly(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://new.example.com"),
		BackendURL:  stringPtrRule("http://upstream-a:8096"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestRuleServiceUpdateRejectsBackendURLOnly(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:           3,
				AgentID:      "local",
				FrontendURL:  "https://before.example.com",
				BackendsJSON: `[{"url":"http://emby:8096"}]`,
				Enabled:      true,
				Revision:     10,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "local", 3, HTTPRuleInput{
		BackendURL: stringPtrRule("http://upstream-a:8096"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestHTTPRuleFromRowDoesNotSynthesizeLegacyBackendFields(t *testing.T) {
	t.Parallel()
	rule := httpRuleFromRow(storage.HTTPRuleRow{
		ID:             1,
		AgentID:        "local",
		FrontendURL:    "https://legacy.example.com",
		BackendURL:     "http://legacy:8096",
		RelayChainJSON: `[7]`,
		Enabled:        true,
	})

	if rule.BackendURL != "" || len(rule.Backends) != 0 {
		t.Fatalf("legacy backend fields were synthesized: backend_url=%q backends=%+v", rule.BackendURL, rule.Backends)
	}
	if len(rule.RelayChain) != 0 {
		t.Fatalf("legacy relay_chain was synthesized: %+v", rule.RelayChain)
	}
}

func TestRuleServiceCreateRejectsRelayChainOnly(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		listeners:    []storage.RelayListenerRow{{ID: 7, AgentID: "local", Enabled: true, Revision: 1}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayChain:  &[]int{7},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v, want ErrInvalidArgument", err)
	}
}

func TestRuleServiceCreatePreservesRelayObfsForRelayLayersOnly(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{{
			ID:       7,
			AgentID:  "local",
			Enabled:  true,
			Revision: 1,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://layer-obfs.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7}},
		RelayObfs:   boolPtrRule(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !rule.RelayObfs {
		t.Fatalf("expected relay_obfs to be preserved for relay_layers-only rule")
	}
}

func TestRuleServiceCreateNormalizesLoadBalancingStrategies(t *testing.T) {
	t.Parallel()
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
				Backends:      &[]HTTPRuleBackend{{URL: "http://upstream-a:8096"}},
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
	t.Parallel()
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
		RelayLayers:   &[][]int{{5, 6}},
		RelayObfs:     boolPtrRule(true),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if rule.FrontendURL != "https://after.example.com" {
		t.Fatalf("Update() frontend_url = %q", rule.FrontendURL)
	}
	if rule.BackendURL != "" || len(rule.Backends) != 1 || rule.Backends[0].URL != "http://emby:8096" {
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
	if len(rule.RelayChain) != 0 {
		t.Fatalf("Update() relay_chain = %+v", rule.RelayChain)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 2 || rule.RelayLayers[0][1] != 6 {
		t.Fatalf("Update() relay_layers = %+v", rule.RelayLayers)
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
	if store.rulesByAgent["local"][0].BackendURL != "" {
		t.Fatalf("persisted backend fallback = %q", store.rulesByAgent["local"][0].BackendURL)
	}
	if store.rulesByAgent["local"][0].LoadBalancingJSON != `{"strategy":"adaptive"}` {
		t.Fatalf("persisted load_balancing_json = %q", store.rulesByAgent["local"][0].LoadBalancingJSON)
	}
}

func TestRuleServiceUpdatePreservesExplicitLoadBalancingStrategies(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestRuleServiceDeleteCascadesHTTPRuleTraffic(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          11,
				AgentID:     "local",
				FrontendURL: "https://one.example.com",
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if _, err := svc.Delete(context.Background(), "local", 11); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.trafficDeletes) != 1 {
		t.Fatalf("traffic deletes = %+v, want one scope delete", store.trafficDeletes)
	}
	if got := store.trafficDeletes[0]; got != (trafficScopeDeleteCall{agentID: "local", scopeType: "http_rule", scopeID: "11"}) {
		t.Fatalf("traffic delete = %+v", got)
	}
}

func TestRuleServiceDeleteTrafficCleanupIsBestEffortAfterApply(t *testing.T) {
	t.Parallel()
	order := []string{}
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          12,
				AgentID:     "local",
				FrontendURL: "https://one.example.com",
			}},
		},
		trafficDeleteErr: errors.New("cleanup failed"),
		trafficDeleteHook: func() {
			order = append(order, "cleanup")
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		order = append(order, "apply")
		return nil
	})

	if _, err := svc.Delete(context.Background(), "local", 12); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(order) != 2 || order[0] != "apply" || order[1] != "cleanup" {
		t.Fatalf("order = %+v, want apply then cleanup", order)
	}
}

func TestRuleServiceCreateRejectsUnknownRelayLayerListener(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
		RelayLayers: &[][]int{{999}},
	})
	if err == nil {
		t.Fatalf("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay listener not found: 999" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRollsBackRelayLayerDefaultProfileOnSaveError(t *testing.T) {
	t.Parallel()
	relayProfileID := 41
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "local", Name: "local"},
			{ID: "relay-a", Name: "relay-a", CapabilitiesJSON: `["wireguard"]`, DesiredRevision: 5, CurrentRevision: 5},
			{ID: "relay-b", Name: "relay-b", CapabilitiesJSON: `["wireguard"]`},
		},
		listeners: []storage.RelayListenerRow{
			{ID: 7, AgentID: "relay-a", Enabled: true, TransportMode: "tls_tcp"},
			{ID: 8, AgentID: "relay-b", Enabled: true, TransportMode: "wireguard", WireGuardProfileID: &relayProfileID},
		},
		rulesByAgent:       map[string][]storage.HTTPRuleRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
		saveHTTPRulesErrs:  []error{errors.New("save http failed")},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://relay-layer.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7}, {8}},
	})
	if err == nil || !strings.Contains(err.Error(), "save http failed") {
		t.Fatalf("Create() error = %v, want save failure", err)
	}
	if got := len(store.wireGuardByAgentID["relay-a"]); got != 0 {
		t.Fatalf("relay-a WireGuardProfiles after failed create = %+v, want none", store.wireGuardByAgentID["relay-a"])
	}
	if got := ruleStoreAgentByID(t, store, "relay-a").DesiredRevision; got != 5 {
		t.Fatalf("relay-a DesiredRevision after failed create = %d, want 5", got)
	}
}

func TestRuleServiceUpdateRollsBackRelayLayerDefaultProfileOnSaveError(t *testing.T) {
	t.Parallel()
	relayProfileID := 41
	store := &fakeRuleStore{
		agents: []storage.AgentRow{
			{ID: "local", Name: "local"},
			{ID: "relay-a", Name: "relay-a", CapabilitiesJSON: `["wireguard"]`, DesiredRevision: 5, CurrentRevision: 5},
			{ID: "relay-b", Name: "relay-b", CapabilitiesJSON: `["wireguard"]`},
		},
		listeners: []storage.RelayListenerRow{
			{ID: 7, AgentID: "relay-a", Enabled: true, TransportMode: "tls_tcp"},
			{ID: 8, AgentID: "relay-b", Enabled: true, TransportMode: "wireguard", WireGuardProfileID: &relayProfileID},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:                3,
				AgentID:           "local",
				FrontendURL:       "http://relay-layer.example.com",
				BackendsJSON:      `[{"url":"http://upstream:8096"}]`,
				LoadBalancingJSON: `{"strategy":"adaptive"}`,
				Enabled:           true,
				Revision:          10,
			}},
		},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
		saveHTTPRulesErrs:  []error{errors.New("save http failed")},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "local", 3, HTTPRuleInput{
		RelayLayers: &[][]int{{7}, {8}},
	})
	if err == nil || !strings.Contains(err.Error(), "save http failed") {
		t.Fatalf("Update() error = %v, want save failure", err)
	}
	if got := len(store.wireGuardByAgentID["relay-a"]); got != 0 {
		t.Fatalf("relay-a WireGuardProfiles after failed update = %+v, want none", store.wireGuardByAgentID["relay-a"])
	}
	if got := ruleStoreAgentByID(t, store, "relay-a").DesiredRevision; got != 5 {
		t.Fatalf("relay-a DesiredRevision after failed update = %d, want 5", got)
	}
}

func TestRuleServiceCreateAllowsCrossAgentTLSRelayListener(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{ID: "remote-relay", Name: "remote-relay"}},
		listeners: []storage.RelayListenerRow{{
			ID:            7,
			AgentID:       "remote-relay",
			Enabled:       true,
			TransportMode: "tls_tcp",
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://cross-tls.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(rule.RelayLayers) != 1 || len(rule.RelayLayers[0]) != 1 || rule.RelayLayers[0][0] != 7 {
		t.Fatalf("RelayLayers = %+v", rule.RelayLayers)
	}
}

func TestRuleServiceCreateClearsRelayObfsWithoutRelayChain(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{rulesByAgent: map[string][]storage.HTTPRuleRow{}}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				FrontendURL:     "https://relay.example.com",
				BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
				RelayLayersJSON: `[[7]]`,
				RelayObfs:       true,
				Enabled:         true,
				Revision:        2,
			}},
		},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
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

func TestRuleServiceUpdateRejectsRelayChainOnly(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				FrontendURL:     "https://relay.example.com",
				BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
				RelayLayersJSON: `[[7],[8,9]]`,
				Enabled:         true,
				Revision:        2,
			}},
		},
		listeners: []storage.RelayListenerRow{{
			ID:      5,
			AgentID: "local",
			Enabled: true,
		}},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
		RelayChain: &[]int{5},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v, want ErrInvalidArgument", err)
	}
}

func TestRuleServiceUpdateClearsRelayChainWhenRelayLayersSupplied(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				FrontendURL:     "https://relay.example.com",
				BackendURL:      "http://127.0.0.1:8096",
				BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        2,
			}},
		},
		listeners: []storage.RelayListenerRow{{
			ID:      8,
			AgentID: "local",
			Enabled: true,
		}},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
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
	if got := store.rulesByAgent["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestRuleServiceUpdateClearsRelayWhenRelayLayersCleared(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:              1,
				AgentID:         "local",
				FrontendURL:     "https://relay.example.com",
				BackendURL:      "http://127.0.0.1:8096",
				BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
				RelayChainJSON:  `[7]`,
				RelayLayersJSON: `[[7]]`,
				Enabled:         true,
				Revision:        2,
			}},
		},
	}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	rule, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
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
	if got := store.rulesByAgent["local"][0].RelayChainJSON; got != `[]` {
		t.Fatalf("persisted relay_chain = %s", got)
	}
}

func TestRuleServiceCreateRejectsInvalidRelayLayerEntry(t *testing.T) {
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7, 0}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must be positive integer listener IDs" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRejectsDuplicateRelayLayerEntries(t *testing.T) {
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7, 7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not contain duplicates" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRejectsDuplicateRelayLayerEntriesAcrossLayers(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		listeners: []storage.RelayListenerRow{
			{ID: 7, AgentID: "local", Enabled: true},
			{ID: 8, AgentID: "local", Enabled: true},
		},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://duplicate-layers.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://upstream:8096"}},
		RelayLayers: &[][]int{{7, 8}, {7}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if err.Error() != "invalid argument: relay_layers entries must not repeat listener IDs across layers" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateRejectsDuplicateFrontendBindingOnSameAgent(t *testing.T) {
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8097"}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if err.Error() != "invalid argument: frontend_url conflicts with existing rule: 1" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceUpdateRejectsDuplicateFrontendBindingOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:           1,
				AgentID:      "local",
				FrontendURL:  "http://media.example.com/emby",
				BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`,
				Enabled:      true,
				Revision:     2,
			}, {
				ID:           2,
				AgentID:      "local",
				FrontendURL:  "http://media.example.com/jellyfin",
				BackendsJSON: `[{"url":"http://127.0.0.1:8097"}]`,
				Enabled:      true,
				Revision:     3,
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", rule.ID)
	}
}

func TestRuleServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
				Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStore(t, dataRoot, "local")
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(context.Background(), "agent-b", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-b.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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

	l4Svc := NewL4RuleService(config.Config{}, store)
	httpSvc := NewRuleService(config.Config{}, store)

	l4Rule, err := l4Svc.Create(context.Background(), "agent-a", L4RuleInput{
		Protocol:   stringPtrL4("tcp"),
		ListenPort: intPtrL4(9000),
		Backends:   &[]L4Backend{{Host: "backend-a.example.internal", Port: 9001}},
	})
	if err != nil {
		t.Fatalf("Create L4 rule error = %v", err)
	}

	httpRule, err := httpSvc.Create(context.Background(), "agent-b", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-b.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://backend-b.example.internal:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Setenv("CF_TOKEN", "test-token")
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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

func TestRuleServiceCreateHTTPSMasterCFDNSDefersLocalApplyUntilCertificateIssued(t *testing.T) {
	t.Setenv("CF_TOKEN", "test-token")
	dispatcher := ManagedCertificateDispatcher()
	dispatcher.Wait()
	issued := make(chan int, 1)
	dispatcher.SetSignFunc(func(_ context.Context, certID int) error {
		issued <- certID
		return nil
	})
	defer func() {
		dispatcher.Wait()
		dispatcher.SetSignFunc(nil)
	}()

	store := &fakeRuleStore{
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)
	localApplyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		localApplyCalls++
		return nil
	})

	if _, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://panel.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8080"}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	dispatcher.Wait()

	if localApplyCalls != 0 {
		t.Fatalf("local apply calls = %d, want 0 before certificate material exists", localApplyCalls)
	}
	if len(store.rulesByAgent["local"]) != 1 {
		t.Fatalf("rules = %+v, want created HTTPS rule", store.rulesByAgent["local"])
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.IssuerMode != "master_cf_dns" || cert.Status != "issuing" {
		t.Fatalf("cert issuer/status = %s/%s, want master_cf_dns/issuing", cert.IssuerMode, cert.Status)
	}
	select {
	case certID := <-issued:
		if certID != cert.ID {
			t.Fatalf("issued cert id = %d, want %d", certID, cert.ID)
		}
	default:
		t.Fatal("background certificate issue was not submitted")
	}
}

func TestRuleServiceCreateHTTPSMasterCFDNSDefersLocalRelayCallerApplyUntilCertificateIssued(t *testing.T) {
	t.Setenv("CF_TOKEN", "test-token")
	dispatcher := ManagedCertificateDispatcher()
	dispatcher.Wait()
	issued := make(chan int, 1)
	dispatcher.SetSignFunc(func(_ context.Context, certID int) error {
		issued <- certID
		return nil
	})
	defer func() {
		dispatcher.Wait()
		dispatcher.SetSignFunc(nil)
	}()

	wireGuardProfileID := 17
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "remote-relay",
			Name:            "remote-relay",
			DesiredRevision: 2,
			CurrentRevision: 2,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{},
		listeners: []storage.RelayListenerRow{{
			ID:                 7,
			AgentID:            "remote-relay",
			Enabled:            true,
			TransportMode:      "wireguard",
			WireGuardProfileID: &wireGuardProfileID,
		}},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{
			"local": {{
				ID:       9,
				AgentID:  "local",
				Name:     "local-default",
				Enabled:  true,
				TagsJSON: `["system:default-wireguard"]`,
			}},
		},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent:              true,
		LocalAgentID:                  "local",
		ManagedDNSCertificatesEnabled: true,
	}, store)
	localApplyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		localApplyCalls++
		return nil
	})

	if _, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay-panel.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8080"}},
		RelayLayers: &[][]int{{7}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	dispatcher.Wait()

	if localApplyCalls != 0 {
		t.Fatalf("local apply calls = %d, want 0 before certificate material exists", localApplyCalls)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("managed cert count = %d", len(store.managedCerts))
	}
	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.IssuerMode != "master_cf_dns" || cert.Status != "issuing" {
		t.Fatalf("cert issuer/status = %s/%s, want master_cf_dns/issuing", cert.IssuerMode, cert.Status)
	}
	select {
	case certID := <-issued:
		if certID != cert.ID {
			t.Fatalf("issued cert id = %d, want %d", certID, cert.ID)
		}
	default:
		t.Fatal("background certificate issue was not submitted")
	}
}

func TestRuleServiceCreateHTTPSRemoteDomainRejectsMasterCFDNSForNonLocalTarget(t *testing.T) {
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "local ACME issuance for IP HTTPS") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceCreateHTTPSIPUsesLocalHTTP01WhenAgentSupportsLocalACME(t *testing.T) {
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "no available unified certificate issuer for no-issuer.example.com") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRuleServiceUpdateHTTPSCleanupDetachesOrDeletesManagedCertificate(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("managed certs should roll back, got %d rows", len(store.managedCerts))
	}
}

func TestRuleServiceCreateRollsBackRuleWhenRemoteRevisionBumpFails(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "edge-a",
			Name:            "edge-a",
			DesiredRevision: 3,
			CurrentRevision: 3,
		}},
		rulesByAgent:       map[string][]storage.HTTPRuleRow{},
		saveAgentErrs:      []error{errors.New("save agent failed")},
		l4RulesByAgent:     map[string][]storage.L4RuleRow{},
		egressProfiles:     []storage.EgressProfileRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://rollback.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err == nil {
		t.Fatal("Create() error = nil")
	}
	if got := store.rulesByAgent["edge-a"]; len(got) != 0 {
		t.Fatalf("rules after failed revision bump = %+v, want rollback to empty", got)
	}
}

func TestRuleServiceUpdateRollsBackRuleWhenRemoteRevisionBumpFails(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "edge-a",
			Name:            "edge-a",
			DesiredRevision: 3,
			CurrentRevision: 3,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-a": {{
				ID:                1,
				AgentID:           "edge-a",
				FrontendURL:       "http://rollback.example.com",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `[]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[]`,
				RelayLayersJSON:   `[]`,
				PassProxyHeaders:  true,
				CustomHeadersJSON: `[]`,
				Revision:          3,
			}},
		},
		saveAgentErrs:      []error{errors.New("save agent failed")},
		l4RulesByAgent:     map[string][]storage.L4RuleRow{},
		egressProfiles:     []storage.EgressProfileRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Update(context.Background(), "edge-a", 1, HTTPRuleInput{
		Backends: &[]HTTPRuleBackend{{URL: "http://127.0.0.1:9096"}},
	})
	if err == nil {
		t.Fatal("Update() error = nil")
	}
	got := store.rulesByAgent["edge-a"]
	if len(got) != 1 || got[0].BackendsJSON != `[{"url":"http://127.0.0.1:8096"}]` {
		t.Fatalf("rules after failed revision bump = %+v, want original backend", got)
	}
}

func TestRuleServiceUpdateRollbackPreservesManagedCertificateMaterial(t *testing.T) {
	t.Parallel()
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

func TestRuleServiceUpdateLocalApplyFailurePreservesManagedCertificateMaterial(t *testing.T) {
	t.Parallel()
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
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		return errors.New("local apply failed")
	})

	_, err := svc.Update(context.Background(), "local", 1, HTTPRuleInput{
		FrontendURL: stringPtrRule("http://stale-auto.example.com"),
	})
	if err == nil {
		t.Fatal("Update() error = nil")
	}
	if got := store.rulesByAgent["local"]; len(got) != 1 || got[0].FrontendURL != "https://stale-auto.example.com" {
		t.Fatalf("rules after failed local apply = %+v, want original HTTPS rule", got)
	}
	if len(store.managedCerts) != 1 || store.managedCerts[0].Domain != "stale-auto.example.com" {
		t.Fatalf("managed certs after failed local apply = %+v, want original cert", store.managedCerts)
	}
	if !store.materialByDomain["stale-auto.example.com"] {
		t.Fatalf("material was deleted during local apply rollback path")
	}
}

func TestRuleServiceDeleteRollsBackRuleWhenRemoteRevisionBumpFails(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "edge-a",
			Name:            "edge-a",
			DesiredRevision: 3,
			CurrentRevision: 3,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"edge-a": {{
				ID:                1,
				AgentID:           "edge-a",
				FrontendURL:       "http://rollback.example.com",
				BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
				LoadBalancingJSON: `{"strategy":"round_robin"}`,
				Enabled:           true,
				TagsJSON:          `[]`,
				ProxyRedirect:     true,
				RelayChainJSON:    `[]`,
				RelayLayersJSON:   `[]`,
				PassProxyHeaders:  true,
				CustomHeadersJSON: `[]`,
				Revision:          3,
			}},
		},
		saveAgentErrs:      []error{errors.New("save agent failed")},
		l4RulesByAgent:     map[string][]storage.L4RuleRow{},
		egressProfiles:     []storage.EgressProfileRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewRuleService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Delete(context.Background(), "edge-a", 1)
	if err == nil {
		t.Fatal("Delete() error = nil")
	}
	got := store.rulesByAgent["edge-a"]
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("rules after failed delete revision bump = %+v, want original rule", got)
	}
}

func TestRuleServiceDeleteSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	t.Parallel()
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
