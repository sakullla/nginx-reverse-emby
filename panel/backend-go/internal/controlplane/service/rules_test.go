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
	ctx := authenticatedServiceMutationContext(t)

	created, err := svc.Create(ctx, "local", HTTPRuleInput{
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
	ctx := authenticatedServiceMutationContext(t)

	first, err := svc.Create(ctx, "agent-a", HTTPRuleInput{
		FrontendURL: stringPtrRule("http://agent-a.example.com"),
		Backends:    &[]HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}},
	})
	if err != nil {
		t.Fatalf("Create(agent-a) error = %v", err)
	}
	second, err := svc.Create(ctx, "agent-b", HTTPRuleInput{
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

func TestRuleServiceRejectsRevisionIncapableStoreWithoutWritesOrApply(t *testing.T) {
	t.Parallel()
	legacy := &fakeRuleStore{
		rulesByAgent:   map[string][]storage.HTTPRuleRow{"local": {}},
		l4RulesByAgent: map[string][]storage.L4RuleRow{},
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

func TestRuleServiceCreateRollsBackRuleWhenRemoteRevisionBumpFails(t *testing.T) {
	t.Parallel()
	store := &fakeRuleStore{
		agents: []storage.AgentRow{{
			ID:              "edge-a",
			Name:            "edge-a",
			DesiredRevision: 3,
			CurrentRevision: 3,
		}},
		rulesByAgent:   map[string][]storage.HTTPRuleRow{},
		saveAgentErrs:  []error{errors.New("save agent failed")},
		l4RulesByAgent: map[string][]storage.L4RuleRow{},
		egressProfiles: []storage.EgressProfileRow{},
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

func TestConfigMutationTargetsAdvertiseLocalPluginRuntime(t *testing.T) {
	t.Parallel()
	targets := configMutationTargets(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, []string{"local"}, nil)
	if len(targets) != 1 || !targets[0].Local || !agentHasCapability(targets[0].Capabilities, storage.PluginGenerationCapability) {
		t.Fatalf("local mutation target = %+v, want %s capability", targets, storage.PluginGenerationCapability)
	}
}
