//go:build !integration

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

	egressProfiles []storage.EgressProfileRow
	listeners      []storage.RelayListenerRow
	managedCerts   []storage.ManagedCertificateRow

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

func (*fakeRuleStore) allowUngovernedMutationsForTests() {}

func (*fakeRuleStore) allowLegacyConfigMutationFallback() {}

type revisionIncapableRuleStore struct {
	ruleStore
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

func newRuleServiceTestStore(t *testing.T) *fakeRuleStore {
	t.Helper()
	return &fakeRuleStore{
		rulesByAgent:   map[string][]storage.HTTPRuleRow{},
		l4RulesByAgent: map[string][]storage.L4RuleRow{},
	}
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

func testConfig() config.Config {
	return config.Config{EnableLocalAgent: true, LocalAgentID: "local"}
}

func TestRuleServiceCRUDUsesRevisionMutationWithoutSynchronousApply(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	ctx := authenticatedServiceMutationContext(t)
	if err := store.SaveManagedCertificates(ctx, []storage.ManagedCertificateRow{{
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
	baselineRevisions, err := store.ListAgentRevisions(ctx, "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(ctx, "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://sub.zouter.skl.onl"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
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
	updated, err := svc.Update(ctx, "local", created.ID, HTTPRuleInput{
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

	deleted, err := svc.Delete(ctx, "local", created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != created.ID {
		t.Fatalf("Delete() id = %d, want %d", deleted.ID, created.ID)
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
	deleteRevision := revisions[len(revisions)-1]
	if _, found, err := store.GetOperationDependencyArtifact(ctx, deleteRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() after delete error = %v", err)
	} else if !found {
		t.Fatal("delete dependency plan artifact was not persisted")
	}
}

func TestRuleServiceCrossAgentRelayPlanAndMissingDependencyAreAtomic(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	ctx := authenticatedServiceMutationContext(t)
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
	created, err := svc.Create(ctx, "local", HTTPRuleInput{
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
	_, err = svc.Create(ctx, "local", HTTPRuleInput{
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
