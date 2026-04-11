package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type relayCertStore struct {
	agents          []storage.AgentRow
	httpRulesByID   map[string][]storage.HTTPRuleRow
	l4RulesByID     map[string][]storage.L4RuleRow
	relayByAgentID  map[string][]storage.RelayListenerRow
	managedCerts    []storage.ManagedCertificateRow
	localState      storage.LocalAgentStateRow
	saveRelayErr    error
	saveManagedErr  error
	saveManagedErrs []error
	saveManagedCall int
	cleanupCall     int
	cleanupErrs     []error
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

func (s *relayCertStore) ListVersionPolicies(context.Context) ([]storage.VersionPolicyRow, error) {
	return nil, nil
}

func (s *relayCertStore) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), s.managedCerts...), nil
}

func (s *relayCertStore) SaveAgent(context.Context, storage.AgentRow) error {
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

func (s *relayCertStore) CleanupManagedCertificateMaterial(_ context.Context, _ []storage.ManagedCertificateRow, _ []storage.ManagedCertificateRow) error {
	s.cleanupCall++
	if len(s.cleanupErrs) > 0 {
		err := s.cleanupErrs[0]
		s.cleanupErrs = s.cleanupErrs[1:]
		return err
	}
	return nil
}

func TestRelayServiceCreateAutoIssuesCertificateAndDerivesTrust(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
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
	if autoCert.CertificateType != "internal_ca" || !autoCert.SelfSigned {
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

func TestRelayServiceUpdateAutoRelayCAReplacesExistingManualCertificate(t *testing.T) {
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
	if *listener.CertificateID == 20 {
		t.Fatalf("listener.CertificateID kept manual cert: %d", *listener.CertificateID)
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
	created, _, ok := findManagedCertificateByID(store.managedCerts, *listener.CertificateID)
	if !ok {
		t.Fatalf("auto cert %d not found", *listener.CertificateID)
	}
	if !isAutoRelayListenerCertificate(created, 1) {
		t.Fatalf("created cert is not auto relay listener cert: %+v", created)
	}
}

func TestRelayServiceCreateRollsBackAutoCertificateWhenListenerSaveFails(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
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
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
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

func TestRelayServiceCreateSucceedsWhenCleanupFailsPostCommit(t *testing.T) {
	store := &relayCertStore{
		relayByAgentID: map[string][]storage.RelayListenerRow{},
		httpRulesByID:  map[string][]storage.HTTPRuleRow{},
		l4RulesByID:    map[string][]storage.L4RuleRow{},
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
