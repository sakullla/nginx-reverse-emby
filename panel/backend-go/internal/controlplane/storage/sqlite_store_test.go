package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestStoreLoadsAgentsAndRulesFromExistingSQLite(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents() = %v, %v", agents, err)
	}

	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil || len(rules) == 0 {
		t.Fatalf("ListHTTPRules() = %v, %v", rules, err)
	}

	localState, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if localState.DesiredRevision == 0 {
		t.Fatalf("LoadLocalAgentState() returned empty state: %+v", localState)
	}
}

func TestStorePersistsL4RulesAndVersionPolicies(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	err = store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{
		ID:                8,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		UpstreamHost:      "emby",
		UpstreamPort:      8096,
		BackendsJSON:      `[{"host":"emby","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		TagsJSON:          `["edge"]`,
		Revision:          10,
	}})
	if err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	l4Rules, err := store.ListL4Rules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rules) != 1 || l4Rules[0].ListenPort != 8443 || l4Rules[0].Revision != 10 {
		t.Fatalf("ListL4Rules() = %+v", l4Rules)
	}

	err = store.SaveVersionPolicies(t.Context(), []VersionPolicyRow{{
		ID:             "stable",
		Channel:        "stable",
		DesiredVersion: "1.2.3",
		PackagesJSON:   `[{"platform":"linux-amd64","url":"https://example.com/nre-agent","sha256":"abc123"}]`,
		TagsJSON:       `["default"]`,
	}})
	if err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	policies, err := store.ListVersionPolicies(t.Context())
	if err != nil {
		t.Fatalf("ListVersionPolicies() error = %v", err)
	}
	if len(policies) != 1 || policies[0].ID != "stable" || policies[0].DesiredVersion != "1.2.3" {
		t.Fatalf("ListVersionPolicies() = %+v", policies)
	}
}

func TestStorePersistsHTTPRules(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	err = store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{{
		ID:                9,
		AgentID:           "local",
		FrontendURL:       "https://updated.example.com",
		BackendURL:        "http://emby:8096",
		BackendsJSON:      `[{"url":"http://emby:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `["http"]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  false,
		UserAgent:         "",
		CustomHeadersJSON: `[]`,
		Revision:          14,
	}})
	if err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].ID != 9 || rules[0].FrontendURL != "https://updated.example.com" || rules[0].Revision != 14 {
		t.Fatalf("ListHTTPRules() = %+v", rules)
	}
}

func TestStorePersistsRelayListenersAndManagedCertificates(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	certID := 11
	err = store.SaveRelayListeners(t.Context(), "local", []RelayListenerRow{{
		ID:                      3,
		AgentID:                 "local",
		Name:                    "relay-a",
		BindHostsJSON:           `["0.0.0.0"]`,
		ListenHost:              "0.0.0.0",
		ListenPort:              7443,
		PublicHost:              "relay-a.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           &certID,
		TLSMode:                 "pin_or_ca",
		PinSetJSON:              `[{"type":"spki_sha256","value":"abc"}]`,
		TrustedCACertificateIDs: `[10]`,
		AllowSelfSigned:         true,
		TagsJSON:                `["relay"]`,
		Revision:                12,
	}})
	if err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}

	listeners, err := store.ListRelayListeners(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(listeners) != 1 || listeners[0].ID != 3 || listeners[0].CertificateID == nil || *listeners[0].CertificateID != 11 {
		t.Fatalf("ListRelayListeners() = %+v", listeners)
	}

	err = store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              11,
		Domain:          "relay-a.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		LastIssueAt:     "2026-04-10T00:00:00Z",
		LastError:       "",
		MaterialHash:    "hash-a",
		AgentReports:    `{}`,
		ACMEInfo:        `{}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["relay"]`,
		Revision:        13,
	}})
	if err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}

	certs, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(certs) != 1 || certs[0].ID != 11 || certs[0].Domain != "relay-a.example.com" {
		t.Fatalf("ListManagedCertificates() = %+v", certs)
	}
}

func TestStoreSaveManagedCertificatesRemovesMaterialForDeletedDomains(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	domain := "stale.example.com"
	initialRows := []ManagedCertificateRow{{
		ID:              101,
		Domain:          domain,
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		Usage:           "https",
		CertificateType: "uploaded",
		Revision:        1,
	}}
	if err := store.SaveManagedCertificates(t.Context(), initialRows); err != nil {
		t.Fatalf("SaveManagedCertificates(initial) error = %v", err)
	}
	writeManagedCertificateMaterial(t, dataRoot, domain, "old-cert", "old-key")
	if _, ok := store.readManagedCertificateMaterial(domain); !ok {
		t.Fatalf("expected material for %q to exist before delete", domain)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{}); err != nil {
		t.Fatalf("SaveManagedCertificates(delete) error = %v", err)
	}

	certDir := filepath.Join(dataRoot, "managed_certificates", normalizeManagedCertificateHost(domain))
	if _, statErr := os.Stat(certDir); statErr != nil {
		t.Fatalf("expected deleted cert dir to remain until explicit cleanup, stat error = %v", statErr)
	}
	if err := store.CleanupManagedCertificateMaterial(t.Context(), initialRows, []ManagedCertificateRow{}); err != nil {
		t.Fatalf("CleanupManagedCertificateMaterial() error = %v", err)
	}
	if _, statErr := os.Stat(certDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected deleted cert dir to be removed after cleanup, stat error = %v", statErr)
	}
	if _, ok := store.readManagedCertificateMaterial(domain); ok {
		t.Fatalf("expected material for %q to be removed after delete", domain)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              102,
		Domain:          domain,
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "pending",
		Usage:           "https",
		CertificateType: "uploaded",
		Revision:        2,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates(recreate) error = %v", err)
	}

	snapshot, err := store.LoadLocalSnapshot(t.Context(), "local")
	if err != nil {
		t.Fatalf("LoadLocalSnapshot() error = %v", err)
	}
	for _, bundle := range snapshot.Certificates {
		if bundle.Domain == domain {
			t.Fatalf("expected stale material for %q not to appear in snapshot: %+v", domain, bundle)
		}
	}
}

func TestStoreLoadsLocalSnapshotWithHighestRelevantRevision(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{
		ID:                8,
		AgentID:           "local",
		Name:              "TCP 8443",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        8443,
		UpstreamHost:      "emby",
		UpstreamPort:      8096,
		BackendsJSON:      `[{"host":"emby","port":8096}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[3]`,
		Enabled:           true,
		TagsJSON:          `["edge"]`,
		Revision:          10,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	certID := 11
	if err := store.SaveRelayListeners(t.Context(), "local", []RelayListenerRow{{
		ID:                      3,
		AgentID:                 "local",
		Name:                    "relay-a",
		BindHostsJSON:           `["0.0.0.0"]`,
		ListenHost:              "0.0.0.0",
		ListenPort:              7443,
		PublicHost:              "relay-a.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           &certID,
		TLSMode:                 "pin_or_ca",
		PinSetJSON:              `[{"type":"spki_sha256","value":"abc"}]`,
		TrustedCACertificateIDs: `[10]`,
		AllowSelfSigned:         true,
		TagsJSON:                `["relay"]`,
		Revision:                12,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              10,
		Domain:          "__relay-ca.internal",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `[]`,
		Status:          "active",
		LastIssueAt:     "2026-04-09T00:00:00Z",
		LastError:       "",
		MaterialHash:    "hash-ca",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"__relay-ca.internal"}`,
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
		TagsJSON:        `["system:relay-ca"]`,
		Revision:        11,
	}, {
		ID:              11,
		Domain:          "relay-a.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		LastIssueAt:     "2026-04-10T00:00:00Z",
		LastError:       "",
		MaterialHash:    "hash-a",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"relay-a.example.com"}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["relay"]`,
		Revision:        13,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	writeManagedCertificateMaterial(t, dataRoot, "__relay-ca.internal", "relay-ca-cert", "relay-ca-key")
	writeManagedCertificateMaterial(t, dataRoot, "relay-a.example.com", "listener-cert", "listener-key")

	snapshot, err := store.LoadLocalSnapshot(t.Context(), "local")
	if err != nil {
		t.Fatalf("LoadLocalSnapshot() error = %v", err)
	}

	if snapshot.DesiredVersion != "v1.2.3" {
		t.Fatalf("DesiredVersion = %q", snapshot.DesiredVersion)
	}
	if snapshot.Revision != 13 {
		t.Fatalf("Revision = %d", snapshot.Revision)
	}
	if len(snapshot.Rules) != 1 || len(snapshot.L4Rules) != 1 || len(snapshot.RelayListeners) != 1 {
		t.Fatalf("snapshot payload = %+v", snapshot)
	}
	if len(snapshot.Certificates) != 2 {
		t.Fatalf("Certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 2 {
		t.Fatalf("CertificatePolicies = %+v", snapshot.CertificatePolicies)
	}
	if snapshot.Certificates[0].ID != 10 || snapshot.Certificates[0].CertPEM != "relay-ca-cert" || snapshot.Certificates[0].KeyPEM != "relay-ca-key" {
		t.Fatalf("Certificates[0] = %+v", snapshot.Certificates[0])
	}
	if snapshot.Certificates[1].ID != 11 || snapshot.Certificates[1].CertPEM != "listener-cert" || snapshot.Certificates[1].KeyPEM != "listener-key" {
		t.Fatalf("Certificates[1] = %+v", snapshot.Certificates[1])
	}
}

func TestStoreLoadsAgentSnapshotWithReferencedRelayListenersAndTrustCABundleOnly(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "remote-agent-a",
		Name:            "remote-agent-a",
		AgentToken:      "token-remote-agent-a",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 5,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(remote-agent-a) error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "remote-agent-b",
		Name:            "remote-agent-b",
		AgentToken:      "token-remote-agent-b",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 3,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(remote-agent-b) error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "remote-agent-a", []HTTPRuleRow{{
		ID:                9,
		AgentID:           "remote-agent-a",
		FrontendURL:       "https://edge-a.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[11,22]`,
		PassProxyHeaders:  true,
		Revision:          6,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	certID := 11
	if err := store.SaveRelayListeners(t.Context(), "remote-agent-a", []RelayListenerRow{{
		ID:                      11,
		AgentID:                 "remote-agent-a",
		Name:                    "relay-a",
		BindHostsJSON:           `["10.0.0.10"]`,
		ListenHost:              "10.0.0.10",
		ListenPort:              7443,
		PublicHost:              "relay-a.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           &certID,
		TLSMode:                 "pin_only",
		PinSetJSON:              `[{"type":"sha256","value":"pin-a"}]`,
		TrustedCACertificateIDs: `[10]`,
		AllowSelfSigned:         true,
		Revision:                4,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(remote-agent-a) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "remote-agent-b", []RelayListenerRow{{
		ID:              22,
		AgentID:         "remote-agent-b",
		Name:            "relay-b",
		ListenHost:      "relay-b.example.com",
		ListenPort:      8443,
		PublicHost:      "relay-b.example.com",
		PublicPort:      8443,
		Enabled:         true,
		CertificateID:   intPointer(12),
		TLSMode:         "pin_only",
		PinSetJSON:      `[{"type":"sha256","value":"pin-b"}]`,
		Revision:        7,
		BindHostsJSON:   `["relay-b.example.com"]`,
		TagsJSON:        `[]`,
		AllowSelfSigned: false,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(remote-agent-b) error = %v", err)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              10,
		Domain:          "__relay-ca.internal",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `[]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"__relay-ca.internal"}`,
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
		TagsJSON:        `["system:relay-ca"]`,
		Revision:        5,
	}, {
		ID:              11,
		Domain:          "relay-a.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["remote-agent-a"]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"relay-a.example.com"}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["relay"]`,
		Revision:        6,
	}, {
		ID:              12,
		Domain:          "relay-b.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["remote-agent-b"]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"relay-b.example.com"}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["relay"]`,
		Revision:        7,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	writeManagedCertificateMaterial(t, dataRoot, "__relay-ca.internal", "relay-ca-cert", "relay-ca-key")
	writeManagedCertificateMaterial(t, dataRoot, "relay-a.example.com", "relay-a-cert", "relay-a-key")
	writeManagedCertificateMaterial(t, dataRoot, "relay-b.example.com", "relay-b-cert", "relay-b-key")

	if err := store.SaveVersionPolicies(t.Context(), []VersionPolicyRow{{
		ID:             "stable-a",
		Channel:        "stable",
		DesiredVersion: "1.2.3",
		PackagesJSON:   `[{"platform":"windows-amd64","url":"https://example.com/agent-windows-a.zip","sha256":"sha-windows-a"}]`,
		TagsJSON:       `[]`,
	}, {
		ID:             "stable-z",
		Channel:        "stable",
		DesiredVersion: "1.2.3",
		PackagesJSON:   `[{"platform":"windows-amd64","url":"https://example.com/agent-windows-z.zip","sha256":"sha-windows-z"}]`,
		TagsJSON:       `[]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-agent-a", AgentSnapshotInput{
		DesiredVersion:  "1.2.3",
		DesiredRevision: 5,
		CurrentRevision: 1,
		Platform:        "windows-amd64",
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if snapshot.Revision != 7 {
		t.Fatalf("Revision = %d", snapshot.Revision)
	}
	if snapshot.VersionPackage == nil || snapshot.VersionPackage.URL != "https://example.com/agent-windows-a.zip" || snapshot.VersionPackage.SHA256 != "sha-windows-a" {
		t.Fatalf("VersionPackage = %+v", snapshot.VersionPackage)
	}
	if len(snapshot.RelayListeners) != 2 {
		t.Fatalf("RelayListeners = %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[0].ID != 11 || snapshot.RelayListeners[1].ID != 22 {
		t.Fatalf("RelayListeners order/ids = %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[1].AgentID != "remote-agent-b" {
		t.Fatalf("RelayListeners[1].AgentID = %q", snapshot.RelayListeners[1].AgentID)
	}
	if len(snapshot.Certificates) != 2 {
		t.Fatalf("Certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 2 {
		t.Fatalf("CertificatePolicies = %+v", snapshot.CertificatePolicies)
	}
	if !containsCertificateID(snapshot.Certificates, 10) || !containsCertificateID(snapshot.Certificates, 11) {
		t.Fatalf("Certificates missing expected trust/target ids 10/11: %+v", snapshot.Certificates)
	}
	if containsCertificateID(snapshot.Certificates, 12) {
		t.Fatalf("Certificates unexpectedly include cross-agent relay server cert id 12: %+v", snapshot.Certificates)
	}
	if !containsPolicyID(snapshot.CertificatePolicies, 10) || !containsPolicyID(snapshot.CertificatePolicies, 11) {
		t.Fatalf("CertificatePolicies missing expected trust/target ids 10/11: %+v", snapshot.CertificatePolicies)
	}
	if containsPolicyID(snapshot.CertificatePolicies, 12) {
		t.Fatalf("CertificatePolicies unexpectedly include cross-agent relay server cert id 12: %+v", snapshot.CertificatePolicies)
	}
}

func TestStoreLoadAgentSnapshotIgnoresDisabledRelayDependencies(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "remote-disabled",
		Name:            "remote-disabled",
		AgentToken:      "token-remote-disabled",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(remote-disabled) error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "remote-dependency",
		Name:            "remote-dependency",
		AgentToken:      "token-remote-dependency",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(remote-dependency) error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "remote-disabled", []HTTPRuleRow{{
		ID:                1,
		AgentID:           "remote-disabled",
		FrontendURL:       "https://disabled-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           false,
		RelayChainJSON:    `[77]`,
		PassProxyHeaders:  true,
		Revision:          9,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(remote-disabled) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "remote-disabled", []L4RuleRow{{
		ID:                2,
		AgentID:           "remote-disabled",
		Name:              "disabled-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[77]`,
		Enabled:           false,
		Revision:          10,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(remote-disabled) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "remote-dependency", []RelayListenerRow{{
		ID:                      77,
		AgentID:                 "remote-dependency",
		Name:                    "relay-dependency",
		ListenHost:              "relay-dependency.example.com",
		ListenPort:              7443,
		PublicHost:              "relay-dependency.example.com",
		PublicPort:              7443,
		Enabled:                 true,
		CertificateID:           intPointer(31),
		TLSMode:                 "pin_or_ca",
		PinSetJSON:              `[]`,
		TrustedCACertificateIDs: `[30]`,
		AllowSelfSigned:         false,
		BindHostsJSON:           `[]`,
		Revision:                11,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(remote-dependency) error = %v", err)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              30,
		Domain:          "__relay-ca.internal",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `[]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"__relay-ca.internal"}`,
		Usage:           "relay_ca",
		CertificateType: "internal_ca",
		SelfSigned:      true,
		TagsJSON:        `["system:relay-ca"]`,
		Revision:        12,
	}, {
		ID:              31,
		Domain:          "relay-dependency.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["remote-dependency"]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"relay-dependency.example.com"}`,
		Usage:           "relay_tunnel",
		CertificateType: "uploaded",
		SelfSigned:      false,
		TagsJSON:        `["relay"]`,
		Revision:        12,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	writeManagedCertificateMaterial(t, dataRoot, "__relay-ca.internal", "relay-ca-cert", "relay-ca-key")
	writeManagedCertificateMaterial(t, dataRoot, "relay-dependency.example.com", "relay-dependency-cert", "relay-dependency-key")

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-disabled", AgentSnapshotInput{
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		Platform:        "linux-amd64",
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if len(snapshot.Rules) != 0 || len(snapshot.L4Rules) != 0 {
		t.Fatalf("expected disabled rules to be absent from snapshot payload: %+v", snapshot)
	}
	if len(snapshot.RelayListeners) != 0 {
		t.Fatalf("expected disabled relay dependencies to be ignored: %+v", snapshot.RelayListeners)
	}
	if len(snapshot.Certificates) != 0 || len(snapshot.CertificatePolicies) != 0 {
		t.Fatalf("expected disabled relay dependencies to not pull certs/policies: certs=%+v policies=%+v", snapshot.Certificates, snapshot.CertificatePolicies)
	}
}

func TestStoreLoadAgentSnapshotSkipsMalformedL4Rows(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "malformed-l4-agent",
		Name:            "malformed-l4-agent",
		AgentToken:      "token-malformed-l4-agent",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveL4Rules(t.Context(), "malformed-l4-agent", []L4RuleRow{{
		ID:                41,
		AgentID:           "malformed-l4-agent",
		Name:              "valid-rule",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9800,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9801}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		Revision:          8,
	}, {
		ID:                42,
		AgentID:           "malformed-l4-agent",
		Name:              "broken-rule",
		Protocol:          "tcp",
		ListenHost:        "",
		ListenPort:        0,
		BackendsJSON:      `[{"host":"","port":0}]`,
		LoadBalancingJSON: `{}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		Revision:          99,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "malformed-l4-agent", AgentSnapshotInput{
		DesiredRevision: 0,
		CurrentRevision: 0,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if snapshot.Revision != 8 {
		t.Fatalf("Revision = %d", snapshot.Revision)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	if snapshot.L4Rules[0].ID != 41 {
		t.Fatalf("L4Rules[0] = %+v", snapshot.L4Rules[0])
	}
}

func TestStoreLoadAgentSnapshotKeepsEffectiveRevisionWhenCurrentMatches(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	if err := store.SaveHTTPRules(t.Context(), "remote-stable", []HTTPRuleRow{{
		ID:                101,
		AgentID:           "remote-stable",
		FrontendURL:       "https://stable.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  true,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	firstSnapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-stable", AgentSnapshotInput{
		DesiredRevision: 1,
		CurrentRevision: 0,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(first) error = %v", err)
	}
	if firstSnapshot.Revision != 2 {
		t.Fatalf("first snapshot revision = %d", firstSnapshot.Revision)
	}

	secondSnapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-stable", AgentSnapshotInput{
		DesiredRevision: 1,
		CurrentRevision: 2,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(second) error = %v", err)
	}
	if secondSnapshot.Revision != 2 {
		t.Fatalf("second snapshot revision = %d", secondSnapshot.Revision)
	}
}

func TestStoreSavesSuccessfulLocalRuntimeStateIntoLocalAgentState(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	err = store.SaveLocalRuntimeState(t.Context(), "local", RuntimeState{
		CurrentRevision: 9,
		Status:          "active",
	})
	if err != nil {
		t.Fatalf("SaveLocalRuntimeState() error = %v", err)
	}

	state, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if state.CurrentRevision != 9 || state.LastApplyRevision != 9 || state.DesiredRevision != 9 {
		t.Fatalf("LoadLocalAgentState() revisions = %+v", state)
	}
	if state.LastApplyStatus != "success" {
		t.Fatalf("LastApplyStatus = %q", state.LastApplyStatus)
	}
	if state.LastApplyMessage != "" {
		t.Fatalf("LastApplyMessage = %q", state.LastApplyMessage)
	}
}

func TestStoreSaveLocalRuntimeStateUsesExplicitApplyMetadata(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := store.db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	err = store.SaveLocalRuntimeState(t.Context(), "local", RuntimeState{
		CurrentRevision:   9,
		LastApplyRevision: 11,
		LastApplyStatus:   "error",
		LastApplyMessage:  "apply failed",
		Status:            "active",
	})
	if err != nil {
		t.Fatalf("SaveLocalRuntimeState() error = %v", err)
	}

	state, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if state.CurrentRevision != 9 {
		t.Fatalf("CurrentRevision = %d", state.CurrentRevision)
	}
	if state.LastApplyRevision != 11 {
		t.Fatalf("LastApplyRevision = %d", state.LastApplyRevision)
	}
	if state.LastApplyStatus != "error" {
		t.Fatalf("LastApplyStatus = %q", state.LastApplyStatus)
	}
	if state.LastApplyMessage != "apply failed" {
		t.Fatalf("LastApplyMessage = %q", state.LastApplyMessage)
	}
	if state.DesiredRevision != 3 {
		t.Fatalf("DesiredRevision = %d", state.DesiredRevision)
	}
}

func seedSQLiteFixtureFromCanonicalSchema(t *testing.T) string {
	t.Helper()

	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	t.Cleanup(func() {
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	repoRoot := repositoryRoot(t)
	for _, stmt := range loadControlPlaneBaseSchemaStatements(t, repoRoot) {
		execSQLiteStatement(t, db, stmt, false)
	}
	for _, stmt := range loadPrismaMigrationStatements(t, repoRoot) {
		execSQLiteStatement(t, db, stmt, true)
	}

	statements := []string{
		`INSERT INTO agents (
			id, name, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, is_local, mode, desired_version, platform, tags, capabilities
		) VALUES ('local', 'Local Agent', 3, 2, 2, 'success', '', 1, 'pull', 'v1.2.3', 'linux-amd64', '[]', '[]')`,
		`INSERT INTO rules (
			id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect, relay_chain, pass_proxy_headers, user_agent, custom_headers, revision
		) VALUES (1, 'local', 'https://emby.example.com', 'http://emby:8096', '[{"url":"http://emby:8096"}]', '{"strategy":"round_robin"}', 1, '[]', 1, '[]', 1, '', '[]', 3)`,
		`INSERT INTO local_agent_state (
			id, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, desired_version
		) VALUES (1, 3, 2, 2, 'success', '', 'v1.2.3')`,
	}
	for _, stmt := range statements {
		execSQLiteStatement(t, db, stmt, false)
	}

	return dataRoot
}

func writeManagedCertificateMaterial(t *testing.T, dataRoot string, domain string, certPEM string, keyPEM string) {
	t.Helper()

	certDir := filepath.Join(dataRoot, "managed_certificates", normalizeManagedCertificateHost(domain))
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", certDir, err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert"), []byte(certPEM), 0o644); err != nil {
		t.Fatalf("os.WriteFile(cert) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "key"), []byte(keyPEM), 0o644); err != nil {
		t.Fatalf("os.WriteFile(key) error = %v", err)
	}
}

func intPointer(value int) *int {
	return &value
}

func containsCertificateID(values []ManagedCertificateBundle, expected int) bool {
	for _, value := range values {
		if value.ID == expected {
			return true
		}
	}
	return false
}

func containsPolicyID(values []ManagedCertificatePolicy, expected int) bool {
	for _, value := range values {
		if value.ID == expected {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filePath), "..", "..", "..", "..", ".."))
}

func loadControlPlaneBaseSchemaStatements(t *testing.T, repoRoot string) []string {
	t.Helper()

	sourcePath := filepath.Join(repoRoot, "panel", "backend", "storage-prisma-core.js")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", sourcePath, err)
	}

	source := string(sourceBytes)
	const startMarker = "const SCHEMA_STATEMENTS = ["
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("SCHEMA_STATEMENTS not found in %s", sourcePath)
	}

	body := source[start+len(startMarker):]
	end := strings.Index(body, "];")
	if end < 0 {
		t.Fatalf("SCHEMA_STATEMENTS terminator not found in %s", sourcePath)
	}

	var statements []string
	for i := 0; i < end; i++ {
		delimiter := body[i]
		if delimiter != '`' && delimiter != '"' && delimiter != '\'' {
			continue
		}

		statement, nextIndex, ok := readJavaScriptStringLiteral(body, i)
		if !ok {
			t.Fatalf("failed to parse schema statement in %s", sourcePath)
		}
		trimmed := strings.TrimSpace(statement)
		if trimmed != "" {
			statements = append(statements, trimmed)
		}
		i = nextIndex - 1
	}

	if len(statements) == 0 {
		t.Fatalf("no schema statements parsed from %s", sourcePath)
	}
	return statements
}

func readJavaScriptStringLiteral(source string, start int) (string, int, bool) {
	delimiter := source[start]
	var builder strings.Builder
	escaped := false

	for i := start + 1; i < len(source); i++ {
		ch := source[i]
		if escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && delimiter != '`' {
			escaped = true
			continue
		}
		if ch == delimiter {
			return builder.String(), i + 1, true
		}
		builder.WriteByte(ch)
	}

	return "", 0, false
}

func loadPrismaMigrationStatements(t *testing.T, repoRoot string) []string {
	t.Helper()

	migrationsDir := filepath.Join(repoRoot, "panel", "backend", "prisma", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", migrationsDir, err)
	}

	var statements []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		sqlPath := filepath.Join(migrationsDir, entry.Name())
		sqlBytes, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v", sqlPath, err)
		}
		statements = append(statements, splitSQLStatements(string(sqlBytes))...)
	}

	if len(statements) == 0 {
		t.Fatalf("no Prisma migration statements found in %s", migrationsDir)
	}
	return statements
}

func splitSQLStatements(sqlText string) []string {
	rawStatements := strings.Split(sqlText, ";")
	statements := make([]string, 0, len(rawStatements))
	for _, raw := range rawStatements {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

func execSQLiteStatement(t *testing.T, db *gorm.DB, stmt string, allowDuplicate bool) {
	t.Helper()

	if err := db.Exec(stmt).Error; err != nil {
		if allowDuplicate && isIgnorableMigrationError(err) {
			return
		}
		t.Fatalf("db.Exec(%q) error = %v", stmt, err)
	}
}

func isIgnorableMigrationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "duplicate column name") || strings.Contains(message, "already exists")
}
