package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type relayListenerReadStore struct {
	storage.Store
	rows []storage.RelayListenerRow
}

func (s *relayListenerReadStore) ListRelayListeners(context.Context, string) ([]storage.RelayListenerRow, error) {
	return append([]storage.RelayListenerRow(nil), s.rows...), nil
}

func TestRelayMaterialRollbacksRunAllActions(t *testing.T) {
	t.Parallel()
	calls := make([]string, 0, 3)
	err := runRelayMaterialRollbacks([]func() error{
		func() error { calls = append(calls, "first"); return nil },
		func() error { calls = append(calls, "second"); return errors.New("restore failed") },
		func() error { calls = append(calls, "third"); return nil },
	})
	if err == nil {
		t.Fatal("runRelayMaterialRollbacks() error = nil")
	}
	if got := strings.Join(calls, ","); got != "third,second,first" {
		t.Fatalf("rollback calls = %q, want all actions in reverse order", got)
	}
}

type relayMaterial struct {
	CertPEM string
	KeyPEM  string
}

func TestRelayServiceCRUDUsesRevisionMutationWithoutSynchronousApply(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	svc := NewRelayListenerService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	applyCalls := 0
	svc.SetLocalApplyTrigger(func(context.Context) error {
		applyCalls++
		return errors.New("synchronous apply must not run")
	})
	baselineRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions() baseline error = %v", err)
	}

	created, err := svc.Create(t.Context(), "local", RelayListenerInput{
		Name:       stringPtr("uow-relay"),
		ListenHost: stringPtr("127.0.0.1"),
		ListenPort: intPtrService(19443),
		Enabled:    boolPtr(false),
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
	updated, err := svc.Update(t.Context(), "local", created.ID, RelayListenerInput{
		Name: stringPtr("uow-relay-updated"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "uow-relay-updated" {
		t.Fatalf("Update() name = %q, want uow-relay-updated", updated.Name)
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

func TestRelayServiceUpdateCreatesCrossAgentDependencyPlan(t *testing.T) {
	t.Parallel()
	store := newMutationValidationStore(t)
	for _, agent := range []storage.AgentRow{
		{ID: "rule-edge", Name: "rule-edge", Platform: "linux-amd64", CapabilitiesJSON: `["http_rules"]`},
		{ID: "relay-edge", Name: "relay-edge", Platform: "linux-amd64", CapabilitiesJSON: `[]`},
	} {
		if err := store.SaveAgent(t.Context(), agent); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agent.ID, err)
		}
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-edge", []storage.RelayListenerRow{{
		ID: 70, AgentID: "relay-edge", Name: "relay", ListenHost: "127.0.0.1", ListenPort: 17443,
		PublicHost: "relay-edge.example.test", PublicPort: 17443, Enabled: true, TransportMode: "tls_tcp", Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "rule-edge", []storage.HTTPRuleRow{{
		ID: 71, AgentID: "rule-edge", FrontendURL: "http://relay-consumer.example.test:18084",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, RelayLayersJSON: `[[70]]`, Enabled: true, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	svc := NewRelayListenerService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)
	updated, err := svc.Update(t.Context(), "relay-edge", 70, RelayListenerInput{
		Name:              stringPtr("relay-updated"),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "relay-updated" || updated.CertificateID == nil {
		t.Fatalf("Update() = %+v, want renamed listener with managed certificate", updated)
	}
	ruleRevisions, err := store.ListAgentRevisions(t.Context(), "rule-edge")
	if err != nil {
		t.Fatalf("ListAgentRevisions(rule-edge) error = %v", err)
	}
	relayRevisions, err := store.ListAgentRevisions(t.Context(), "relay-edge")
	if err != nil {
		t.Fatalf("ListAgentRevisions(relay-edge) error = %v", err)
	}
	ruleRevision := ruleRevisions[len(ruleRevisions)-1]
	relayRevision := relayRevisions[len(relayRevisions)-1]
	if ruleRevision.OperationID == "" || relayRevision.OperationID != ruleRevision.OperationID {
		t.Fatalf("operation ids rule=%q relay=%q, want one multi-agent operation", ruleRevision.OperationID, relayRevision.OperationID)
	}
	if _, found, err := store.GetOperationDependencyArtifact(t.Context(), ruleRevision.OperationID); err != nil {
		t.Fatalf("GetOperationDependencyArtifact() error = %v", err)
	} else if !found {
		t.Fatal("relay update dependency plan artifact was not persisted")
	}
}

type rejectAfterRelayMutationStore struct {
	*storage.GormStore
	err error
}

func (s *rejectAfterRelayMutationStore) WithRevisionMutation(ctx context.Context, mutate storage.RevisionMutationFunc) error {
	return s.GormStore.WithRevisionMutation(ctx, func(tx *storage.GormStore) (storage.RevisionMutationDecision, error) {
		decision, err := mutate(tx)
		if err != nil {
			return decision, err
		}
		return decision, s.err
	})
}

func TestRelayServiceCreateRestoresSameDomainMaterialWhenRevisionCommitFails(t *testing.T) {
	store := newMutationValidationStore(t)
	oldMaterial := storage.ManagedCertificateBundle{
		Domain: relayCADomainIdentity, CertPEM: "old-invalid-cert", KeyPEM: "old-invalid-key",
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 10, Domain: relayCADomainIdentity, Enabled: true, Scope: "domain", IssuerMode: "local_http01",
		TargetAgentIDs: `["local"]`, Status: "error", Usage: "relay_ca", CertificateType: "internal_ca",
		SelfSigned: true, TagsJSON: `["system:relay-ca","system"]`, Revision: 1,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	if err := store.SaveManagedCertificateMaterial(t.Context(), oldMaterial.Domain, oldMaterial); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(old) error = %v", err)
	}
	beforeRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(before) error = %v", err)
	}

	originalNonce := relayListenerAutoCertificateNonce
	relayListenerAutoCertificateNonce = func() string { return "rollbacknonce" }
	t.Cleanup(func() { relayListenerAutoCertificateNonce = originalNonce })
	failingStore := &rejectAfterRelayMutationStore{GormStore: store, err: errors.New("forced revision commit failure")}
	svc := NewRelayListenerService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, failingStore)
	_, err = svc.Create(t.Context(), "local", RelayListenerInput{
		Name: stringPtr("rollback-relay"), ListenPort: intPtrService(19444), PublicHost: stringPtr("rollback.example.test"),
		Enabled: boolPtr(true), CertificateSource: stringPtr("auto_relay_ca"), TrustModeSource: stringPtr("auto"),
	})
	if err == nil || !strings.Contains(err.Error(), "forced revision commit failure") {
		t.Fatalf("Create() error = %v, want forced revision commit failure", err)
	}
	restored, found, err := store.LoadManagedCertificateMaterial(t.Context(), oldMaterial.Domain)
	if err != nil || !found {
		t.Fatalf("LoadManagedCertificateMaterial(old) found=%v error=%v", found, err)
	}
	if restored.CertPEM != oldMaterial.CertPEM || restored.KeyPEM != oldMaterial.KeyPEM {
		t.Fatalf("same-domain material after rollback = %+v, want original %+v", restored, oldMaterial)
	}
	leafDomain := relayListenerAutoCertificateDomain(RelayListener{ID: 1, PublicHost: "rollback.example.test"}, "local")
	if _, found, err := store.LoadManagedCertificateMaterial(t.Context(), leafDomain); err != nil {
		t.Fatalf("LoadManagedCertificateMaterial(leaf) error = %v", err)
	} else if found {
		t.Fatalf("new relay leaf material %q survived rollback", leafDomain)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Domain != relayCADomainIdentity {
		t.Fatalf("managed certificate rows after rollback = %+v", rows)
	}
	listeners, err := store.ListRelayListeners(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(listeners) != 0 {
		t.Fatalf("relay listeners after rollback = %+v", listeners)
	}
	afterRevisions, err := store.ListAgentRevisions(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListAgentRevisions(after) error = %v", err)
	}
	if len(afterRevisions) != len(beforeRevisions) {
		t.Fatalf("revision count changed after failed commit: before=%d after=%d", len(beforeRevisions), len(afterRevisions))
	}
}

type relayCertStore struct {
	agents         []storage.AgentRow
	httpRulesByID  map[string][]storage.HTTPRuleRow
	l4RulesByID    map[string][]storage.L4RuleRow
	relayByAgentID map[string][]storage.RelayListenerRow

	managedCerts                    []storage.ManagedCertificateRow
	materialsByHost                 map[string]relayMaterial
	localState                      storage.LocalAgentStateRow
	localSnapshot                   storage.Snapshot
	savedAgent                      storage.AgentRow
	savedAgentCalls                 int
	savedRuntimeState               storage.RuntimeState
	savedRuntimeAgentID             string
	saveRuntimeCalls                int
	saveRelayErr                    error
	saveManagedErr                  error
	saveManagedErrs                 []error
	saveManagedCall                 int
	saveMaterialErrs                []error
	saveMaterialCall                int
	saveMaterialPartialWriteOnError bool
	cleanupCall                     int
	cleanupErrs                     []error
	trafficDeletes                  []trafficScopeDeleteCall
	trafficDeleteErr                error
	trafficDeleteHook               func()
}

func (*relayCertStore) allowLegacyConfigMutationFallback() {}

func (s *relayCertStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.agents...), nil
}

func (s *relayCertStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.httpRulesByID[agentID]...), nil
}

func (s *relayCertStore) GetHTTPRule(_ context.Context, agentID string, id int) (storage.HTTPRuleRow, bool, error) {
	for _, row := range s.httpRulesByID[agentID] {
		if row.ID == id {
			return row, true, nil
		}
	}
	return storage.HTTPRuleRow{}, false, nil
}

func (s *relayCertStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), s.l4RulesByID[agentID]...), nil
}

func (s *relayCertStore) GetL4Rule(_ context.Context, agentID string, id int) (storage.L4RuleRow, bool, error) {
	for _, row := range s.l4RulesByID[agentID] {
		if row.ID == id {
			return row, true, nil
		}
	}
	return storage.L4RuleRow{}, false, nil
}

func (s *relayCertStore) ListRelayListeners(_ context.Context, agentID string) ([]storage.RelayListenerRow, error) {
	if strings.TrimSpace(agentID) == "" {
		rows := make([]storage.RelayListenerRow, 0)
		for _, listeners := range s.relayByAgentID {
			rows = append(rows, listeners...)
		}
		return rows, nil
	}
	return append([]storage.RelayListenerRow(nil), s.relayByAgentID[agentID]...), nil
}

func (s *relayCertStore) ListEgressProfiles(context.Context) ([]storage.EgressProfileRow, error) {
	return nil, nil
}

func (s *relayCertStore) LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error) {
	return s.localState, nil
}

func (s *relayCertStore) LoadAgentSnapshot(_ context.Context, agentID string, input storage.AgentSnapshotInput) (storage.Snapshot, error) {
	maxRevision := 0
	for _, row := range s.managedCerts {
		if containsString(parseStringArray(row.TargetAgentIDs), strings.TrimSpace(agentID)) && row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	if input.DesiredRevision > maxRevision {
		maxRevision = input.DesiredRevision
	}
	if input.CurrentRevision > maxRevision {
		maxRevision = input.CurrentRevision
	}
	return storage.Snapshot{Revision: int64(maxRevision)}, nil
}

func (s *relayCertStore) LoadLocalSnapshot(context.Context, string) (storage.Snapshot, error) {
	return s.localSnapshot, nil
}

func (s *relayCertStore) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (s *relayCertStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), s.managedCerts...), nil
}

func (s *relayCertStore) SaveAgent(_ context.Context, row storage.AgentRow) error {
	s.savedAgent = row
	s.savedAgentCalls++
	for i := range s.agents {
		if s.agents[i].ID == row.ID {
			s.agents[i] = row
			return nil
		}
	}
	s.agents = append(s.agents, row)
	return nil
}

func (s *relayCertStore) SaveL4Rules(_ context.Context, agentID string, rows []storage.L4RuleRow) error {
	s.l4RulesByID[agentID] = append([]storage.L4RuleRow(nil), rows...)
	return nil
}

func (s *relayCertStore) SaveRelayListeners(_ context.Context, agentID string, rows []storage.RelayListenerRow) error {
	if s.saveRelayErr != nil {
		return s.saveRelayErr
	}
	s.relayByAgentID[agentID] = append([]storage.RelayListenerRow(nil), rows...)
	return nil
}

func (s *relayCertStore) SaveEgressProfiles(context.Context, []storage.EgressProfileRow) error {
	return nil
}

func (s *relayCertStore) SaveVersionPolicies(context.Context, []storage.VersionPolicyRow) error {
	return nil
}

func (s *relayCertStore) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	if s.saveManagedCall < len(s.saveManagedErrs) {
		err := s.saveManagedErrs[s.saveManagedCall]
		s.saveManagedCall++
		if err != nil {
			return err
		}
	} else {
		s.saveManagedCall++
	}
	if s.saveManagedErr != nil {
		return s.saveManagedErr
	}
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	return nil
}

func (s *relayCertStore) SaveLocalRuntimeState(_ context.Context, agentID string, state storage.RuntimeState) error {
	s.savedRuntimeAgentID = agentID
	s.savedRuntimeState = state
	s.saveRuntimeCalls++
	return nil
}

func (s *relayCertStore) CleanupManagedCertificateMaterial(_ context.Context, _ []storage.ManagedCertificateRow, _ []storage.ManagedCertificateRow) error {
	s.cleanupCall++
	if len(s.cleanupErrs) > 0 {
		err := s.cleanupErrs[0]
		s.cleanupErrs = s.cleanupErrs[1:]
		return err
	}
	return nil
}

func (s *relayCertStore) DeleteTrafficByScope(_ context.Context, agentID, scopeType, scopeID string) (int64, error) {
	s.trafficDeletes = append(s.trafficDeletes, trafficScopeDeleteCall{
		agentID:   agentID,
		scopeType: scopeType,
		scopeID:   scopeID,
	})
	if s.trafficDeleteHook != nil {
		s.trafficDeleteHook()
	}
	if s.trafficDeleteErr != nil {
		return 0, s.trafficDeleteErr
	}
	return 0, nil
}

func (s *relayCertStore) LoadManagedCertificateMaterial(_ context.Context, domain string) (storage.ManagedCertificateBundle, bool, error) {
	material, ok := s.materialsByHost[domain]
	if !ok {
		return storage.ManagedCertificateBundle{}, false, nil
	}
	return storage.ManagedCertificateBundle{
		Domain:  domain,
		CertPEM: material.CertPEM,
		KeyPEM:  material.KeyPEM,
	}, true, nil
}

func (s *relayCertStore) SaveManagedCertificateMaterial(_ context.Context, domain string, bundle storage.ManagedCertificateBundle) error {
	if s.saveMaterialCall < len(s.saveMaterialErrs) {
		err := s.saveMaterialErrs[s.saveMaterialCall]
		s.saveMaterialCall++
		if err != nil {
			if s.saveMaterialPartialWriteOnError {
				if s.materialsByHost == nil {
					s.materialsByHost = map[string]relayMaterial{}
				}
				s.materialsByHost[domain] = relayMaterial{
					CertPEM: bundle.CertPEM,
					KeyPEM:  bundle.KeyPEM,
				}
			}
			return err
		}
	} else {
		s.saveMaterialCall++
	}
	if s.materialsByHost == nil {
		s.materialsByHost = map[string]relayMaterial{}
	}
	s.materialsByHost[domain] = relayMaterial{
		CertPEM: bundle.CertPEM,
		KeyPEM:  bundle.KeyPEM,
	}
	return nil
}

func TestRelayServiceCreateAutoIssuesCertificateAndDerivesTrust(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    "relay-ca-hash",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        3,
		}},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-auto"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-auto.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if listener.CertificateID == nil || *listener.CertificateID != 11 {
		t.Fatalf("listener.CertificateID = %v", listener.CertificateID)
	}
	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.PinSet) != 1 || listener.PinSet[0].Type != "spki_sha256" {
		t.Fatalf("listener.PinSet = %+v", listener.PinSet)
	}
	if listener.PinSet[0].Value == "" {
		t.Fatalf("listener.PinSet[0].Value is empty")
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if !listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = false")
	}

	if len(store.managedCerts) != 2 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
	autoCert := managedCertificateFromRow(store.managedCerts[1])
	if autoCert.ID != 11 {
		t.Fatalf("auto cert id = %d", autoCert.ID)
	}
	if autoCert.Usage != "relay_tunnel" || autoCert.IssuerMode != "local_http01" {
		t.Fatalf("auto cert usage/issuer = %+v", autoCert)
	}
	if autoCert.CertificateType != "internal_ca" || autoCert.SelfSigned {
		t.Fatalf("auto cert type/self_signed = %+v", autoCert)
	}
	if autoCert.Status != "active" || !autoCert.Enabled {
		t.Fatalf("auto cert status/enabled = %+v", autoCert)
	}
	if len(autoCert.TargetAgentIDs) != 1 || autoCert.TargetAgentIDs[0] != "local" {
		t.Fatalf("auto cert targets = %+v", autoCert.TargetAgentIDs)
	}
	for _, expectedTag := range []string{"auto", "auto:relay-listener", "listener:1", "agent:local"} {
		if !containsString(autoCert.Tags, expectedTag) {
			t.Fatalf("auto cert tags = %+v", autoCert.Tags)
		}
	}
	material, ok := store.materialsByHost[autoCert.Domain]
	if !ok || strings.TrimSpace(material.CertPEM) == "" || strings.TrimSpace(material.KeyPEM) == "" {
		t.Fatalf("auto cert material missing: %+v", store.materialsByHost)
	}
	leaf := mustParseCertificate(t, material.CertPEM)
	if !containsString(leaf.DNSNames, autoCert.Domain) {
		t.Fatalf("auto cert dns names = %+v, want internal domain %q", leaf.DNSNames, autoCert.Domain)
	}
	if !containsString(leaf.DNSNames, "relay-auto.example.com") {
		t.Fatalf("auto cert dns names = %+v, want public host relay-auto.example.com", leaf.DNSNames)
	}
	expectedPin := mustSPKIPinFromPEM(t, material.CertPEM)
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != expectedPin {
		t.Fatalf("listener.PinSet = %+v, want spki pin %q", listener.PinSet, expectedPin)
	}
	if autoCert.MaterialHash != hashRelayMaterial(material.CertPEM, material.KeyPEM) {
		t.Fatalf("auto cert material hash = %q", autoCert.MaterialHash)
	}
}

func TestRelayServiceCreateAutoBootstrapsMissingGlobalRelayCA(t *testing.T) {
	t.Parallel()
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
	})

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-bootstrap-ca"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-bootstrap.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.CertificateID == nil {
		t.Fatalf("listener.CertificateID = nil")
	}
}

func TestNormalizeRelayListenerInputRejectsOverlappingBindHosts(t *testing.T) {
	t.Parallel()
	_, err := normalizeRelayListenerInput(RelayListenerInput{
		Name:       stringPtr("relay-bind-hosts"),
		ListenPort: intPtrService(7443),
		BindHosts:  &[]string{"0.0.0.0", " 0.0.0.0 ", "127.0.0.1", ""},
	}, RelayListener{}, 1, relayNormalizeOptions{
		AllowMissingCertificate: true,
		SkipTrustValidation:     true,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("normalizeRelayListenerInput() error = %v, want ErrInvalidArgument", err)
	}
	if !strings.Contains(err.Error(), "bind_hosts 0.0.0.0 and 127.0.0.1 overlap") {
		t.Fatalf("normalizeRelayListenerInput() error = %v", err)
	}
}

func TestNormalizeRelayListenerInputAllowsSeparateAddressFamilies(t *testing.T) {
	t.Parallel()
	listener, err := normalizeRelayListenerInput(RelayListenerInput{
		Name:       stringPtr("relay-dual-stack"),
		ListenPort: intPtrService(7443),
		BindHosts:  &[]string{"0.0.0.0", "::1"},
	}, RelayListener{}, 1, relayNormalizeOptions{
		AllowMissingCertificate: true,
		SkipTrustValidation:     true,
	})
	if err != nil {
		t.Fatalf("normalizeRelayListenerInput() error = %v", err)
	}
	if len(listener.BindHosts) != 2 {
		t.Fatalf("listener.BindHosts = %+v", listener.BindHosts)
	}
}

func TestValidateRelayLiveBindingTransitionAllowsSupportedWildcardNarrowing(t *testing.T) {
	t.Parallel()
	current := RelayListener{
		ID: 2, Enabled: true, TransportMode: "tls_tcp", ListenPort: 45369,
		BindHosts: []string{"0.0.0.0"},
	}
	next := current
	next.BindHosts = []string{"154.21.88.16"}

	if err := validateRelayLiveBindingTransition(current, next); err != nil {
		t.Fatalf("TLS wildcard narrowing error = %v", err)
	}

	next.TransportMode = "quic"
	current.TransportMode = "quic"
	err := validateRelayLiveBindingTransition(current, next)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("QUIC wildcard narrowing error = %v, want conflict", err)
	}

	current.TransportMode = "tls_tcp"
	next.TransportMode = "tls_tcp"
	current.BindHosts, next.BindHosts = next.BindHosts, current.BindHosts
	err = validateRelayLiveBindingTransition(current, next)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("TLS wildcard expansion error = %v, want conflict", err)
	}

	next.Enabled = false
	if err := validateRelayLiveBindingTransition(current, next); err != nil {
		t.Fatalf("disabled transition error = %v", err)
	}
}

func TestRelayServiceUpdateAllowsSupportedWildcardNarrowing(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            2,
				AgentID:       "edge-1",
				Name:          "relay-live",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    45369,
				PublicHost:    "relay.example.com",
				PublicPort:    45369,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents:        []storage.AgentRow{{ID: "edge-1"}},
	}
	svc := NewRelayListenerService(config.Config{LocalAgentID: "local"}, store)

	updated, err := svc.Update(context.Background(), "edge-1", 2, RelayListenerInput{
		BindHosts: &[]string{"154.21.88.16"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(updated.BindHosts) != 1 || updated.BindHosts[0] != "154.21.88.16" {
		t.Fatalf("updated bind_hosts = %+v", updated.BindHosts)
	}
	if got := store.relayByAgentID["edge-1"][0].BindHostsJSON; got != `["154.21.88.16"]` {
		t.Fatalf("persisted bind_hosts = %s, want concrete binding", got)
	}
}

func TestRelayListenerDefaultsTransportAndObfs(t *testing.T) {
	t.Parallel()
	listener, err := normalizeRelayListenerInput(RelayListenerInput{
		Name:       stringPtr("relay-a"),
		ListenPort: intPtrService(9443),
	}, RelayListener{}, 1, relayNormalizeOptions{
		AllowMissingCertificate: true,
		SkipTrustValidation:     true,
	})
	if err != nil {
		t.Fatalf("normalizeRelayListenerInput() error = %v", err)
	}
	if listener.TransportMode != "tls_tcp" {
		t.Fatalf("TransportMode = %q", listener.TransportMode)
	}
	if !listener.AllowTransportFallback {
		t.Fatal("AllowTransportFallback = false")
	}
	if listener.ObfsMode != "off" {
		t.Fatalf("ObfsMode = %q", listener.ObfsMode)
	}
}

func TestRelayServiceBootstrapPersistsCanonicalRelayCAWhenMissing(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	if err := svc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}

	relayCA := managedCertificateFromRow(store.managedCerts[0])
	if relayCA.Domain != "__relay-ca.internal" || relayCA.Usage != "relay_ca" || relayCA.CertificateType != "internal_ca" {
		t.Fatalf("relayCA = %+v", relayCA)
	}
	if !relayCA.Enabled || !relayCA.SelfSigned || relayCA.Status != "active" {
		t.Fatalf("relayCA flags = %+v", relayCA)
	}
	if len(relayCA.TargetAgentIDs) != 1 || relayCA.TargetAgentIDs[0] != "local" {
		t.Fatalf("relayCA.TargetAgentIDs = %+v", relayCA.TargetAgentIDs)
	}
	if relayCA.MaterialHash == "" {
		t.Fatalf("relayCA.MaterialHash = %q", relayCA.MaterialHash)
	}
	if _, ok := store.materialsByHost["__relay-ca.internal"]; !ok {
		t.Fatal("expected relay CA material to be persisted")
	}
}

func TestRelayServiceCreateAutoRejectsMultipleRelayCACandidates(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal":         relayCA,
			"legacy-relay-ca.example.com": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        3,
			},
			{
				ID:              11,
				Domain:          "legacy-relay-ca.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "legacy-relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				Revision:        4,
			},
		},
	})

	_, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-duplicate-ca"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-duplicate-ca.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "multiple relay ca candidates found") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceDisabledAutoListenerSkipsIssuanceAndTrustDerivation(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-disabled"),
		ListenPort:        intPtrService(7443),
		Enabled:           boolPtr(false),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if listener.CertificateID != nil {
		t.Fatalf("listener.CertificateID = %v", listener.CertificateID)
	}
	if len(listener.PinSet) != 0 || len(listener.TrustedCACertificateIDs) != 0 {
		t.Fatalf("listener trust fields = %+v", listener)
	}
	if len(store.managedCerts) != 0 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
}

func TestRelayServiceRejectsDisablingReferencedListener(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-2": {{
				ID:              12,
				AgentID:         "edge-2",
				FrontendURL:     "https://app.example.com",
				BackendURL:      "http://upstream:8096",
				BackendsJSON:    `[{"url":"http://upstream:8096"}]`,
				RelayChainJSON:  `[2]`,
				RelayLayersJSON: `[[1]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
			{ID: "edge-2"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Update(context.Background(), "edge-1", 1, RelayListenerInput{
		Enabled: boolPtr(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if !strings.Contains(err.Error(), "disable is not allowed") {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestRelayServiceAllowsDisablingListenerReferencedOnlyByLegacyRelayChain(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-2": {{
				ID:              12,
				AgentID:         "edge-2",
				FrontendURL:     "https://app.example.com",
				BackendURL:      "http://upstream:8096",
				BackendsJSON:    `[{"url":"http://upstream:8096"}]`,
				RelayChainJSON:  `[1]`,
				RelayLayersJSON: `[[2]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
			{ID: "edge-2"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	listener, err := svc.Update(context.Background(), "edge-1", 1, RelayListenerInput{
		Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if listener.Enabled {
		t.Fatalf("listener.Enabled = true")
	}
}

func TestRelayServiceCreateRejectsDuplicateBindOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0","127.0.0.1"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"127.0.0.1", "192.168.1.10"},
		ListenPort: intPtrService(7443),
		Enabled:    boolPtr(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "conflicts with relay listener #1") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceUpdateRejectsDuplicateBindOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {
				{
					ID:            1,
					AgentID:       "edge-1",
					Name:          "relay-a",
					BindHostsJSON: `["0.0.0.0"]`,
					ListenHost:    "0.0.0.0",
					ListenPort:    7443,
					PublicHost:    "relay-a.example.com",
					PublicPort:    7443,
					Enabled:       true,
					CertificateID: intPtrStorage(7),
					TLSMode:       "pin_only",
					PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
					TransportMode: "tls_tcp",
					Revision:      1,
				},
				{
					ID:            2,
					AgentID:       "edge-1",
					Name:          "relay-b",
					BindHostsJSON: `["127.0.0.1"]`,
					ListenHost:    "127.0.0.1",
					ListenPort:    8443,
					PublicHost:    "relay-b.example.com",
					PublicPort:    8443,
					Enabled:       true,
					CertificateID: intPtrStorage(8),
					TLSMode:       "pin_only",
					PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
					TransportMode: "tls_tcp",
					Revision:      2,
				},
			},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Update(context.Background(), "edge-1", 2, RelayListenerInput{
		BindHosts:  &[]string{"0.0.0.0"},
		ListenPort: intPtrService(7443),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Update() error = %v", err)
	}
	if !strings.Contains(err.Error(), "conflicts with relay listener #1") {
		t.Fatalf("Update() error = %v", err)
	}
}

func TestRelayServiceCreateRejectsWildcardBindOverlapOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"127.0.0.1"},
		ListenPort: intPtrService(7443),
		Enabled:    boolPtr(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "conflicts with relay listener #1") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceCreateRejectsIPv6WildcardBindOverlapOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["::"]`,
				ListenHost:    "::",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"::1"},
		ListenPort: intPtrService(7443),
		Enabled:    boolPtr(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(err.Error(), "conflicts with relay listener #1") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceCreateRejectsDuplicateWildcardBindOnSameAgent(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"0.0.0.0"},
		ListenPort: intPtrService(7443),
		Enabled:    boolPtr(false),
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceCreateSucceedsWhenNoBindConflict(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	listener, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"0.0.0.0"},
		ListenPort: intPtrService(8443),
		Enabled:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.ID == 0 {
		t.Fatal("expected non-zero listener ID")
	}
}

func TestRelayServiceCreateAllowsBindOnDisabledListenerPort(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       false,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				TransportMode: "tls_tcp",
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	listener, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("relay-b"),
		BindHosts:  &[]string{"0.0.0.0"},
		ListenPort: intPtrService(7443),
		Enabled:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() should succeed when conflicting listener is disabled, got: %v", err)
	}
	if listener.ID == 0 {
		t.Fatal("expected non-zero listener ID")
	}
}

func TestRelayServiceDeleteRejectsReferencedListener(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:            1,
				AgentID:       "edge-1",
				Name:          "relay-a",
				BindHostsJSON: `["0.0.0.0"]`,
				ListenHost:    "0.0.0.0",
				ListenPort:    7443,
				PublicHost:    "relay-a.example.com",
				PublicPort:    7443,
				Enabled:       true,
				CertificateID: intPtrStorage(7),
				TLSMode:       "pin_only",
				PinSetJSON:    `[{"type":"spki_sha256","value":"pin"}]`,
				Revision:      1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-2": {{
				ID:              21,
				AgentID:         "edge-2",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9443,
				UpstreamHost:    "upstream",
				UpstreamPort:    9443,
				BackendsJSON:    `[{"host":"upstream","port":9443}]`,
				RelayChainJSON:  `[2]`,
				RelayLayersJSON: `[[1]]`,
			}},
		},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
			{ID: "edge-2"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	_, err := svc.Delete(context.Background(), "edge-1", 1)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestRelayServiceDeleteIgnoresLegacyRelayChainOnlyReference(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         1,
				AgentID:    "edge-1",
				Name:       "relay-a",
				ListenHost: "0.0.0.0",
				ListenPort: 7443,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSetJSON: `[{"type":"spki_sha256","value":"pin"}]`,
				Revision:   1,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID: map[string][]storage.L4RuleRow{
			"edge-2": {{
				ID:              21,
				AgentID:         "edge-2",
				Protocol:        "tcp",
				ListenHost:      "0.0.0.0",
				ListenPort:      9443,
				UpstreamHost:    "upstream",
				UpstreamPort:    9443,
				BackendsJSON:    `[{"host":"upstream","port":9443}]`,
				RelayChainJSON:  `[1]`,
				RelayLayersJSON: `[[2]]`,
			}},
		},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
			{ID: "edge-2"},
		},
	}
	svc := NewRelayListenerService(config.Config{
		LocalAgentID: "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.relayByAgentID["edge-1"]) != 0 {
		t.Fatalf("listeners still stored: %+v", store.relayByAgentID["edge-1"])
	}
}

func TestRelayServiceDeleteRejectsRelayLayerOnlyReference(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         2,
				AgentID:    "edge-1",
				Name:       "relay-b",
				ListenHost: "0.0.0.0",
				ListenPort: 7444,
				Enabled:    true,
				TLSMode:    "pin_only",
				PinSetJSON: `[{"type":"spki_sha256","value":"pin"}]`,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{
			"edge-2": {{
				ID:              22,
				AgentID:         "edge-2",
				FrontendURL:     "https://relay.example.com",
				RelayChainJSON:  `[1]`,
				RelayLayersJSON: `[[1,2]]`,
			}},
		},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{
			{ID: "edge-1"},
			{ID: "edge-2"},
		},
	}
	svc := NewRelayListenerService(config.Config{LocalAgentID: "local"}, store)

	_, err := svc.Delete(context.Background(), "edge-1", 2)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete() error = %v", err)
	}
	if !strings.Contains(err.Error(), "referenced by HTTP rule #22") {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestRelayServiceDeleteCascadesRelayListenerTraffic(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         13,
				AgentID:    "edge-1",
				Name:       "relay-c",
				ListenHost: "0.0.0.0",
				ListenPort: 7445,
				Enabled:    true,
				TLSMode:    "passthrough",
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{{
			ID: "edge-1",
		}},
	}
	svc := NewRelayListenerService(config.Config{LocalAgentID: "local"}, store)

	if _, err := svc.Delete(context.Background(), "edge-1", 13); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(store.trafficDeletes) != 1 {
		t.Fatalf("traffic deletes = %+v, want one scope delete", store.trafficDeletes)
	}
	if got := store.trafficDeletes[0]; got != (trafficScopeDeleteCall{agentID: "edge-1", scopeType: "relay_listener", scopeID: "13"}) {
		t.Fatalf("traffic delete = %+v", got)
	}
}

func TestRelayServiceDeleteTrafficCleanupIsBestEffortAfterApply(t *testing.T) {
	t.Parallel()
	order := []string{}
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:         14,
				AgentID:    "local",
				Name:       "relay-d",
				ListenHost: "0.0.0.0",
				ListenPort: 7446,
				Enabled:    true,
				TLSMode:    "passthrough",
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		agents: []storage.AgentRow{{
			ID: "local",
		}},
		trafficDeleteErr: errors.New("cleanup failed"),
		trafficDeleteHook: func() {
			order = append(order, "cleanup")
		},
	}
	svc := NewRelayListenerService(config.Config{LocalAgentID: "local", EnableLocalAgent: true}, store)
	svc.SetLocalApplyTrigger(func(context.Context) error {
		order = append(order, "apply")
		return nil
	})

	if _, err := svc.Delete(context.Background(), "local", 14); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(order) != 2 || order[0] != "apply" || order[1] != "cleanup" {
		t.Fatalf("order = %+v, want apply then cleanup", order)
	}
}

func TestRelayServiceUpdateSwitchingAwayFromAutoCertificateCleansUpOldCert(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	manualMaterial := mustCreateSelfSignedCA(t, "manual.example.com")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(11),
				TLSMode:                 "pin_and_ca",
				PinSetJSON:              `[{"type":"spki_sha256","value":"old-pin"}]`,
				TrustedCACertificateIDs: `[10]`,
				AllowSelfSigned:         true,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
			"manual.example.com":  manualMaterial,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              11,
				Domain:          "listener-1.relay.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "auto-cert-hash",
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["auto","auto:relay-listener","listener:1","agent:local"]`,
				Revision:        2,
			},
			{
				ID:              20,
				Domain:          "manual.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "manual-hash",
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
				SelfSigned:      true,
				Revision:        3,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		CertificateSource: stringPtr("existing_certificate"),
		CertificateID:     intPtrService(20),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if listener.CertificateID == nil || *listener.CertificateID != 20 {
		t.Fatalf("listener.CertificateID = %v", listener.CertificateID)
	}
	if listener.TLSMode != "pin_only" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 0 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if !listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = false")
	}
	for _, row := range store.managedCerts {
		if row.ID == 11 {
			t.Fatalf("auto cert still present after update: %+v", row)
		}
	}
}

func TestRelayServiceUpdateAutoRelayCAKeepsExplicitCertificateID(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	manualMaterial := mustCreateSelfSignedCA(t, "manual.example.com")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(20),
				TLSMode:                 "pin_only",
				PinSetJSON:              `[{"type":"spki_sha256","value":"manual-pin"}]`,
				TrustedCACertificateIDs: `[]`,
				AllowSelfSigned:         true,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
			"manual.example.com":  manualMaterial,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              20,
				Domain:          "manual.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "manual-hash",
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
				SelfSigned:      true,
				Revision:        3,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		CertificateSource: stringPtr("auto_relay_ca"),
		CertificateID:     intPtrService(20),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if listener.CertificateID == nil {
		t.Fatalf("listener.CertificateID = nil")
	}
	if *listener.CertificateID != 20 {
		t.Fatalf("listener.CertificateID = %d, want 20", *listener.CertificateID)
	}
	if listener.TLSMode != "pin_only" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 0 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if len(store.managedCerts) != 2 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
	for _, row := range store.managedCerts {
		if row.ID != 10 && row.ID != 20 {
			t.Fatalf("unexpected cert after update: %+v", row)
		}
	}
}

func TestRelayServiceUpdateAutoRelayCAWithExplicitNullCertificateIDReplacesManualCertificate(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(20),
				TLSMode:                 "pin_only",
				PinSetJSON:              `[{"type":"spki_sha256","value":"manual-pin"}]`,
				TrustedCACertificateIDs: `[]`,
				AllowSelfSigned:         true,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              20,
				Domain:          "manual.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "manual-hash",
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
				SelfSigned:      true,
				Revision:        3,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		CertificateSource: stringPtr("auto_relay_ca"),
		HasCertificateID:  true,
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if listener.CertificateID == nil || *listener.CertificateID == 20 {
		t.Fatalf("listener.CertificateID = %v, want auto-issued cert id", listener.CertificateID)
	}
	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if len(store.managedCerts) != 3 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
}

func TestRelayServiceUpdateExistingAutoCertWithoutTrustFieldsAutoDerivesTrust(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	autoMaterial := mustCreateLeafSignedByCA(t, "listener-1.relay.internal", relayCA)
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(11),
				TLSMode:                 "pin_only",
				PinSetJSON:              `[{"type":"spki_sha256","value":"stale-pin"}]`,
				TrustedCACertificateIDs: `[]`,
				AllowSelfSigned:         false,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal":       relayCA,
			"listener-1.relay.internal": autoMaterial,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(relayCA.CertPEM, relayCA.KeyPEM),
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              11,
				Domain:          "listener-1.relay.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(autoMaterial.CertPEM, autoMaterial.KeyPEM),
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				SelfSigned:      false,
				TagsJSON:        `["auto","auto:relay-listener","listener:1","agent:local"]`,
				Revision:        2,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		Name: stringPtr("relay-a-renamed"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if listener.CertificateID == nil || *listener.CertificateID != 11 {
		t.Fatalf("listener.CertificateID = %v", listener.CertificateID)
	}
	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	expectedPin := mustSPKIPinFromPEM(t, autoMaterial.CertPEM)
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != expectedPin {
		t.Fatalf("listener.PinSet = %+v, want %q", listener.PinSet, expectedPin)
	}
	if !listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = false")
	}
}

func TestRelayServiceUpdateExistingAutoCertRotatesWhenPublicHostChanges(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	oldAutoMaterial := mustCreateLeafSignedByCA(t, "198.51.100.10", relayCA)
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "198.51.100.10",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(11),
				TLSMode:                 "pin_and_ca",
				PinSetJSON:              `[{"type":"spki_sha256","value":"old-pin"}]`,
				TrustedCACertificateIDs: `[10]`,
				AllowSelfSigned:         false,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal":       relayCA,
			"listener-1.relay.internal": oldAutoMaterial,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(relayCA.CertPEM, relayCA.KeyPEM),
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              11,
				Domain:          "listener-1.relay.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(oldAutoMaterial.CertPEM, oldAutoMaterial.KeyPEM),
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				SelfSigned:      false,
				TagsJSON:        `["auto","auto:relay-listener","listener:1","agent:local"]`,
				Revision:        2,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		PublicHost: stringPtr("203.0.113.20"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if listener.CertificateID == nil || *listener.CertificateID == 11 {
		t.Fatalf("listener.CertificateID = %v, want rotated auto certificate", listener.CertificateID)
	}
	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	var autoCert ManagedCertificate
	for _, row := range store.managedCerts {
		if row.ID == *listener.CertificateID {
			autoCert = managedCertificateFromRow(row)
			break
		}
	}
	if autoCert.ID == 0 {
		t.Fatalf("rotated auto cert %d not found", *listener.CertificateID)
	}
	material, ok := store.materialsByHost[autoCert.Domain]
	if !ok {
		t.Fatalf("rotated auto cert material missing for %q", autoCert.Domain)
	}
	leaf := mustParseCertificate(t, material.CertPEM)
	if !containsIP(leaf.IPAddresses, "203.0.113.20") {
		t.Fatalf("rotated cert ip SANs = %+v, want 203.0.113.20", leaf.IPAddresses)
	}
	if containsIP(leaf.IPAddresses, "198.51.100.10") {
		t.Fatalf("rotated cert ip SANs = %+v, still contains old public host", leaf.IPAddresses)
	}
	for _, row := range store.managedCerts {
		if row.ID == 11 {
			t.Fatalf("old auto cert still present after rotation: %+v", row)
		}
	}
}

func TestRelayServiceUpdateExistingAutoCertWithExplicitNullTrustFieldSuppressesAutoDerive(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	autoMaterial := mustCreateLeafSignedByCA(t, "listener-1.relay.internal", relayCA)
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(11),
				TLSMode:                 "pin_only",
				PinSetJSON:              `[{"type":"spki_sha256","value":"stale-pin"}]`,
				TrustedCACertificateIDs: `[]`,
				AllowSelfSigned:         false,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal":       relayCA,
			"listener-1.relay.internal": autoMaterial,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(relayCA.CertPEM, relayCA.KeyPEM),
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              11,
				Domain:          "listener-1.relay.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(autoMaterial.CertPEM, autoMaterial.KeyPEM),
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				SelfSigned:      false,
				TagsJSON:        `["auto","auto:relay-listener","listener:1","agent:local"]`,
				Revision:        2,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "local", 1, RelayListenerInput{
		Name:       stringPtr("relay-a-renamed"),
		HasTLSMode: true,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if listener.CertificateID == nil || *listener.CertificateID != 11 {
		t.Fatalf("listener.CertificateID = %v", listener.CertificateID)
	}
	if listener.TLSMode != "pin_only" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != "stale-pin" {
		t.Fatalf("listener.PinSet = %+v", listener.PinSet)
	}
	if len(listener.TrustedCACertificateIDs) != 0 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = true")
	}
}

func TestRelayServiceCreateAutoRelayCAWithExplicitTrustFieldsDoesNotAutoDerive(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    hashRelayMaterial(relayCA.CertPEM, relayCA.KeyPEM),
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        3,
		}},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:                    stringPtr("relay-explicit-trust"),
		ListenPort:              intPtrService(9443),
		Enabled:                 boolPtr(true),
		CertificateSource:       stringPtr("auto_relay_ca"),
		TLSMode:                 stringPtr("pin_only"),
		PinSet:                  &[]RelayPin{{Type: "spki_sha256", Value: "manual-pin"}},
		TrustedCACertificateIDs: &[]int{},
		AllowSelfSigned:         boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.CertificateID == nil {
		t.Fatalf("listener.CertificateID = nil")
	}
	if listener.TLSMode != "pin_only" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != "manual-pin" {
		t.Fatalf("listener.PinSet = %+v", listener.PinSet)
	}
	if len(listener.TrustedCACertificateIDs) != 0 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = true")
	}
}

func TestRelayServiceCreateRollsBackAutoCertificateWhenListenerSaveFails(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    "relay-ca-hash",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        3,
		}},
		saveRelayErr: errors.New("save relay listeners failed"),
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-auto"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-auto.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err == nil || !strings.Contains(err.Error(), "save relay listeners failed") {
		t.Fatalf("Create() error = %v", err)
	}
	if len(store.managedCerts) != 1 {
		t.Fatalf("len(store.managedCerts) = %d", len(store.managedCerts))
	}
	if len(store.relayByAgentID["local"]) != 0 {
		t.Fatalf("listeners unexpectedly persisted: %+v", store.relayByAgentID["local"])
	}
}

func TestRelayServiceCreateRollbackFailureRemainsServerError(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    "relay-ca-hash",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        3,
		}},
		saveRelayErr:    errors.New("save relay listeners failed"),
		saveManagedErrs: []error{nil, errors.New("rollback managed certs failed")},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	_, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-auto"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-auto.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err == nil {
		t.Fatalf("Create() error = nil")
	}
	if errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() error unexpectedly marked invalid argument: %v", err)
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestRelayServiceDeleteCleansUpUnusedAutoCertificate(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:                      1,
				AgentID:                 "local",
				Name:                    "relay-a",
				BindHostsJSON:           `["0.0.0.0"]`,
				ListenHost:              "0.0.0.0",
				ListenPort:              7443,
				PublicHost:              "relay-a.example.com",
				PublicPort:              7443,
				Enabled:                 true,
				CertificateID:           intPtrStorage(11),
				TLSMode:                 "pin_and_ca",
				PinSetJSON:              `[{"type":"spki_sha256","value":"old-pin"}]`,
				TrustedCACertificateIDs: `[10]`,
				AllowSelfSigned:         true,
				Revision:                2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              11,
				Domain:          "listener-1.relay.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "auto-cert-hash",
				Usage:           "relay_tunnel",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["auto","auto:relay-listener","listener:1","agent:local"]`,
				Revision:        2,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "local", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.relayByAgentID["local"]) != 0 {
		t.Fatalf("listeners still stored: %+v", store.relayByAgentID["local"])
	}
	if len(store.managedCerts) != 1 || store.managedCerts[0].ID != 10 {
		t.Fatalf("managed certs after delete = %+v", store.managedCerts)
	}
}

func TestRelayServiceDeleteThenRecreateAutoCertificateUsesFreshIdentityDomain(t *testing.T) {
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{"local": {}},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    "relay-ca-hash",
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	originalNonce := relayListenerAutoCertificateNonce
	defer func() { relayListenerAutoCertificateNonce = originalNonce }()
	nonces := []string{"oldnonce0001", "newnonce0002"}
	relayListenerAutoCertificateNonce = func() string {
		value := nonces[0]
		nonces = nonces[1:]
		return value
	}

	first, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-a"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-a.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if first.CertificateID == nil || *first.CertificateID != 11 {
		t.Fatalf("first.CertificateID = %v", first.CertificateID)
	}
	firstAutoCert := managedCertificateFromRow(store.managedCerts[1])
	if firstAutoCert.Domain != "listener-1.relay-a-example-com.local-oldnonce0001.relay.internal" {
		t.Fatalf("firstAutoCert.Domain = %q", firstAutoCert.Domain)
	}

	deleted, err := svc.Delete(context.Background(), "local", 1)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 1 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}

	recreated, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-a"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-a.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if recreated.CertificateID == nil || *recreated.CertificateID != 11 {
		t.Fatalf("recreated.CertificateID = %v", recreated.CertificateID)
	}

	autoCert := managedCertificateFromRow(store.managedCerts[1])
	if autoCert.Domain == firstAutoCert.Domain {
		t.Fatalf("autoCert.Domain reused deleted identity %q", autoCert.Domain)
	}
	if autoCert.Domain != "listener-1.relay-a-example-com.local-newnonce0002.relay.internal" {
		t.Fatalf("autoCert.Domain = %q", autoCert.Domain)
	}
}

func TestRelayServiceDeleteUpdatesRemoteAgentDesiredRevision(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "Edge 1",
			DesiredRevision: 4,
			CurrentRevision: 4,
		}},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         5,
				AgentID:    "edge-1",
				Name:       "remote-hop",
				ListenHost: "0.0.0.0",
				ListenPort: 2443,
				PublicHost: "relay.example.com",
				PublicPort: 2443,
				Enabled:    true,
				TLSMode:    "pin_only",
				Revision:   4,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts:  []storage.ManagedCertificateRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 5)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 5 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	if len(store.relayByAgentID["edge-1"]) != 0 {
		t.Fatalf("relay listeners still stored: %+v", store.relayByAgentID["edge-1"])
	}
	if store.agents[0].DesiredRevision != 5 {
		t.Fatalf("remote desired_revision = %d", store.agents[0].DesiredRevision)
	}
}

func TestRelayServiceCreateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "Edge 1",
			DesiredRevision: 9,
			CurrentRevision: 9,
		}},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         5,
				AgentID:    "edge-1",
				Name:       "existing-hop",
				ListenHost: "0.0.0.0",
				ListenPort: 2443,
				PublicHost: "relay.example.com",
				PublicPort: 2443,
				Enabled:    false,
				Revision:   4,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts:  []storage.ManagedCertificateRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "edge-1", RelayListenerInput{
		Name:       stringPtr("remote-hop"),
		ListenPort: intPtrService(3443),
		Enabled:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Create() revision", listener.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, listener.Revision)
}

func TestRelayServiceCreateReassignsPreferredIDWhenListenerAlreadyUsesIt(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"local": {{
				ID:         9,
				AgentID:    "local",
				Name:       "existing-hop",
				ListenHost: "0.0.0.0",
				ListenPort: 2443,
				PublicHost: "existing.example.com",
				PublicPort: 2443,
				Enabled:    false,
				Revision:   2,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts:  []storage.ManagedCertificateRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		ID:         intPtrService(9),
		Name:       stringPtr("new-hop"),
		ListenPort: intPtrService(3443),
		Enabled:    boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.ID != 10 {
		t.Fatalf("Create() id = %d, want 10", listener.ID)
	}
}

func TestRelayServiceUpdateUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "Edge 1",
			DesiredRevision: 9,
			CurrentRevision: 9,
		}},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         5,
				AgentID:    "edge-1",
				Name:       "remote-hop",
				ListenHost: "0.0.0.0",
				ListenPort: 2443,
				PublicHost: "relay.example.com",
				PublicPort: 2443,
				Enabled:    false,
				Revision:   4,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts:  []storage.ManagedCertificateRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Update(context.Background(), "edge-1", 5, RelayListenerInput{
		Name: stringPtr("remote-hop-updated"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertRevisionAboveFloor(t, "Update() revision", listener.Revision, 9)
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
	assertRevisionNotBehind(t, "remote desired_revision", store.agents[0].DesiredRevision, listener.Revision)
}

func TestRelayServiceDeleteUsesRevisionAboveRemoteAgentSyncFloor(t *testing.T) {
	t.Parallel()
	store := &relayCertStore{
		agents: []storage.AgentRow{{
			ID:              "edge-1",
			Name:            "Edge 1",
			DesiredRevision: 9,
			CurrentRevision: 9,
		}},
		relayByAgentID: map[string][]storage.RelayListenerRow{
			"edge-1": {{
				ID:         5,
				AgentID:    "edge-1",
				Name:       "remote-hop",
				ListenHost: "0.0.0.0",
				ListenPort: 2443,
				PublicHost: "relay.example.com",
				PublicPort: 2443,
				Enabled:    false,
				Revision:   4,
			}},
		},
		httpRulesByID: map[string][]storage.HTTPRuleRow{},
		l4RulesByID:   map[string][]storage.L4RuleRow{},
		managedCerts:  []storage.ManagedCertificateRow{},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	deleted, err := svc.Delete(context.Background(), "edge-1", 5)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted.ID != 5 {
		t.Fatalf("deleted.ID = %d", deleted.ID)
	}
	assertRevisionAboveFloor(t, "remote desired_revision", store.agents[0].DesiredRevision, 9)
}

func TestRelayServiceCreateSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
		},
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              10,
			Domain:          "__relay-ca.internal",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			MaterialHash:    "relay-ca-hash",
			Usage:           "relay_ca",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			TagsJSON:        `["system:relay-ca","system"]`,
			Revision:        3,
		}},
		cleanupErrs: []error{errors.New("cleanup failed")},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-auto"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-auto.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.CertificateID == nil {
		t.Fatalf("listener.CertificateID = nil")
	}
	if len(store.relayByAgentID["local"]) != 1 {
		t.Fatalf("listener rows not committed: %+v", store.relayByAgentID["local"])
	}
	if len(store.managedCerts) != 2 {
		t.Fatalf("managed cert rows not committed: %+v", store.managedCerts)
	}
}

func TestRelayServiceCreateWithUploadedCertificateSignedByRelayCAAutoDerivesCATrust(t *testing.T) {
	t.Parallel()
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	manualLeaf := mustCreateLeafSignedByCA(t, "manual.example.com", relayCA)
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
			"manual.example.com":  manualLeaf,
		},
		managedCerts: []storage.ManagedCertificateRow{
			{
				ID:              10,
				Domain:          "__relay-ca.internal",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(relayCA.CertPEM, relayCA.KeyPEM),
				Usage:           "relay_ca",
				CertificateType: "internal_ca",
				SelfSigned:      true,
				TagsJSON:        `["system:relay-ca","system"]`,
				Revision:        1,
			},
			{
				ID:              20,
				Domain:          "manual.example.com",
				Enabled:         true,
				Scope:           "domain",
				IssuerMode:      "local_http01",
				TargetAgentIDs:  `["local"]`,
				Status:          "active",
				MaterialHash:    hashRelayMaterial(manualLeaf.CertPEM, manualLeaf.KeyPEM),
				Usage:           "relay_tunnel",
				CertificateType: "uploaded",
				SelfSigned:      false,
				Revision:        2,
			},
		},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-uploaded"),
		ListenPort:        intPtrService(8443),
		CertificateSource: stringPtr("existing_certificate"),
		CertificateID:     intPtrService(20),
		TrustModeSource:   stringPtr("auto"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	expectedPin := mustSPKIPinFromPEM(t, manualLeaf.CertPEM)
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != expectedPin {
		t.Fatalf("listener.PinSet = %+v, want %q", listener.PinSet, expectedPin)
	}
	if !listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = false")
	}
}

func intPtrService(value int) *int {
	return &value
}

func intPtrStorage(value int) *int {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

var relayTestCAOnce sync.Once
var relayTestCA relayMaterial

func mustCreateSelfSignedCA(t *testing.T, commonName string) relayMaterial {
	t.Helper()
	// Relay tests use isolated stores, so their immutable built-in CA fixture can be shared.
	if commonName == "__relay-ca.internal" {
		relayTestCAOnce.Do(func() {
			relayTestCA = createSelfSignedCA(t, commonName)
		})
		return relayTestCA
	}
	return createSelfSignedCA(t, commonName)
}

func createSelfSignedCA(t *testing.T, commonName string) relayMaterial {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          mustSerialNumber(t),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return relayMaterial{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})),
	}
}

func mustMarshalECPrivateKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	return der
}

func mustCreateLeafSignedByCA(t *testing.T, host string, ca relayMaterial) relayMaterial {
	t.Helper()
	caCert, caKey := mustParseCertificatePair(t, ca)
	// Leaf behavior is algorithm-agnostic; P-256 avoids repeated RSA key generation.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: mustSerialNumber(t),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(825 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	if ip := net.ParseIP(host); ip != nil {
		template.DNSNames = nil
		template.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return relayMaterial{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalECPrivateKey(t, privateKey)})),
	}
}

func mustParseCertificatePair(t *testing.T, material relayMaterial) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	certBlock, _ := pem.Decode([]byte(material.CertPEM))
	if certBlock == nil {
		t.Fatal("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	keyBlock, _ := pem.Decode([]byte(material.KeyPEM))
	if keyBlock == nil {
		t.Fatal("failed to decode key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS1PrivateKey() error = %v", err)
	}
	return cert, key
}

func mustParseCertificate(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return cert
}

func mustSPKIPinFromPEM(t *testing.T, certPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		t.Fatal("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	spki, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey() error = %v", err)
	}
	sum := sha256.Sum256(spki)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func containsIP(values []net.IP, target string) bool {
	parsed := net.ParseIP(target)
	if parsed == nil {
		return false
	}
	for _, value := range values {
		if value.Equal(parsed) {
			return true
		}
	}
	return false
}

func hashRelayMaterial(certPEM string, keyPEM string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n---\n%s", certPEM, keyPEM)))
	return fmt.Sprintf("%x", sum[:])
}

func mustSerialNumber(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("rand.Int() error = %v", err)
	}
	return serial
}

func TestRelayServiceListAndGetHideStoredUnsupportedRows(t *testing.T) {
	t.Parallel()
	store := &relayListenerReadStore{rows: []storage.RelayListenerRow{
		{ID: 1, AgentID: "local", Name: "ordinary", ListenHost: "127.0.0.1", ListenPort: 9443, TransportMode: "tls_tcp"},
		{ID: 2, AgentID: "local", Name: "retired", ListenHost: "127.0.0.1", ListenPort: 51820, TransportMode: "unsupported"},
	}}
	svc := NewRelayListenerService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	listeners, err := svc.List(t.Context(), "local")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listeners) != 1 || listeners[0].ID != 1 {
		t.Fatalf("List() = %+v, want only ordinary listener", listeners)
	}
	if _, err := svc.listenerByID(t.Context(), "local", 2); !errors.Is(err, ErrRelayListenerNotFound) {
		t.Fatalf("listenerByID(retired) error = %v, want ErrRelayListenerNotFound", err)
	}
}
