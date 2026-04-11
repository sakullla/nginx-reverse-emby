package service

import (
	"context"
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
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type relayMaterial struct {
	CertPEM string
	KeyPEM  string
}

type relayCertStore struct {
	agents                          []storage.AgentRow
	httpRulesByID                   map[string][]storage.HTTPRuleRow
	l4RulesByID                     map[string][]storage.L4RuleRow
	relayByAgentID                  map[string][]storage.RelayListenerRow
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
}

func (s *relayCertStore) ListAgents(context.Context) ([]storage.AgentRow, error) {
	return append([]storage.AgentRow(nil), s.agents...), nil
}

func (s *relayCertStore) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.httpRulesByID[agentID]...), nil
}

func (s *relayCertStore) ListL4Rules(_ context.Context, agentID string) ([]storage.L4RuleRow, error) {
	return append([]storage.L4RuleRow(nil), s.l4RulesByID[agentID]...), nil
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
	expectedPin := mustSPKIPinFromPEM(t, material.CertPEM)
	if len(listener.PinSet) != 1 || listener.PinSet[0].Value != expectedPin {
		t.Fatalf("listener.PinSet = %+v, want spki pin %q", listener.PinSet, expectedPin)
	}
	if autoCert.MaterialHash != hashRelayMaterial(material.CertPEM, material.KeyPEM) {
		t.Fatalf("auto cert material hash = %q", autoCert.MaterialHash)
	}
}

func TestRelayServiceCreateAutoUsesLegacyRelayCADomainIdentityCandidate(t *testing.T) {
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
			Usage:           "https",
			CertificateType: "internal_ca",
			SelfSigned:      true,
			Revision:        3,
		}},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-legacy-ca"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-legacy-ca.example.com"),
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
}

func TestRelayServiceCreateAutoBootstrapsMissingGlobalRelayCA(t *testing.T) {
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

func TestRelayServiceCreateAutoRejectsMultipleRelayCACandidates(t *testing.T) {
	relayCA := mustCreateSelfSignedCA(t, "__relay-ca.internal")
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
		materialsByHost: map[string]relayMaterial{
			"__relay-ca.internal": relayCA,
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

func TestRelayServiceCreateAutoCanonicalizesExistingRelayCACandidate(t *testing.T) {
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
			Enabled:         false,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-1"]`,
			Status:          "active",
			MaterialHash:    "relay-ca-hash",
			Usage:           "https",
			CertificateType: "internal_ca",
			SelfSigned:      false,
			TagsJSON:        `["legacy"]`,
			Revision:        3,
		}},
	}
	svc := NewRelayListenerService(config.Config{
		EnableLocalAgent: true,
		LocalAgentID:     "local",
	}, store)

	listener, err := svc.Create(context.Background(), "local", RelayListenerInput{
		Name:              stringPtr("relay-canonicalize-ca"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-canonicalize-ca.example.com"),
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
	relayCARow := managedCertificateFromRow(store.managedCerts[0])
	if relayCARow.Domain != "__relay-ca.internal" || relayCARow.Usage != "relay_ca" || relayCARow.CertificateType != "internal_ca" {
		t.Fatalf("relayCARow = %+v", relayCARow)
	}
	if !relayCARow.Enabled || !relayCARow.SelfSigned {
		t.Fatalf("relayCARow flags = %+v", relayCARow)
	}
	if len(relayCARow.TargetAgentIDs) != 1 || relayCARow.TargetAgentIDs[0] != "local" {
		t.Fatalf("relayCARow.TargetAgentIDs = %+v", relayCARow.TargetAgentIDs)
	}
	for _, expectedTag := range []string{"system:relay-ca", "system"} {
		if !containsString(relayCARow.Tags, expectedTag) {
			t.Fatalf("relayCARow.Tags = %+v", relayCARow.Tags)
		}
	}
	if relayCARow.Revision != 4 {
		t.Fatalf("relayCARow.Revision = %d", relayCARow.Revision)
	}
}

func TestRelayServiceCreateAutoRelayCAWithoutTrustModeSourceAutoDerivesTrust(t *testing.T) {
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
		Name:              stringPtr("relay-auto-no-trust-mode"),
		ListenPort:        intPtrService(7443),
		PublicHost:        stringPtr("relay-auto.example.com"),
		Enabled:           boolPtr(true),
		CertificateSource: stringPtr("auto_relay_ca"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.CertificateID == nil {
		t.Fatalf("listener.CertificateID = nil")
	}
	if listener.TLSMode != "pin_and_ca" {
		t.Fatalf("listener.TLSMode = %q", listener.TLSMode)
	}
	if len(listener.PinSet) == 0 || listener.PinSet[0].Value == "" {
		t.Fatalf("listener.PinSet = %+v", listener.PinSet)
	}
	if len(listener.TrustedCACertificateIDs) != 1 || listener.TrustedCACertificateIDs[0] != 10 {
		t.Fatalf("listener.TrustedCACertificateIDs = %+v", listener.TrustedCACertificateIDs)
	}
	if !listener.AllowSelfSigned {
		t.Fatalf("listener.AllowSelfSigned = false")
	}
}

func TestRelayServiceDisabledAutoListenerSkipsIssuanceAndTrustDerivation(t *testing.T) {
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
				ID:             12,
				AgentID:        "edge-2",
				FrontendURL:    "https://app.example.com",
				BackendURL:     "http://upstream:8096",
				BackendsJSON:   `[{"url":"http://upstream:8096"}]`,
				RelayChainJSON: `[1]`,
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

func TestRelayServiceDeleteRejectsReferencedListener(t *testing.T) {
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
				ID:             21,
				AgentID:        "edge-2",
				Protocol:       "tcp",
				ListenHost:     "0.0.0.0",
				ListenPort:     9443,
				UpstreamHost:   "upstream",
				UpstreamPort:   9443,
				BackendsJSON:   `[{"host":"upstream","port":9443}]`,
				RelayChainJSON: `[1]`,
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

func TestRelayServiceUpdateSwitchingAwayFromAutoCertificateCleansUpOldCert(t *testing.T) {
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

func TestRelayServiceUpdateExistingAutoCertWithExplicitNullTrustFieldSuppressesAutoDerive(t *testing.T) {
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

func TestRelayServiceCreateSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
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

func mustCreateSelfSignedCA(t *testing.T, commonName string) relayMaterial {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
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

func mustCreateLeafSignedByCA(t *testing.T, host string, ca relayMaterial) relayMaterial {
	t.Helper()
	caCert, caKey := mustParseCertificatePair(t, ca)
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
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
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})),
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
