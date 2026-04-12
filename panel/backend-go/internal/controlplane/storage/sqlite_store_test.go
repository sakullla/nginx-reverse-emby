package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestStoreLoadsAgentsAndRulesFromGORMSeededSQLite(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

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

func TestBootstrapSQLiteSchemaCreatesFreshPanelDatabaseWithoutSQLFixtures(t *testing.T) {
	dataRoot := t.TempDir()

	db, err := openSQLiteForTest(filepath.Join(dataRoot, "panel.db"))
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	state, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if state.ID != 1 || state.LastApplyStatus != "success" {
		t.Fatalf("unexpected local state: %+v", state)
	}

	var localStateRows int64
	if err := store.db.WithContext(t.Context()).Model(&LocalAgentStateRow{}).Count(&localStateRows).Error; err != nil {
		t.Fatalf("count local_agent_state rows error = %v", err)
	}
	if localStateRows != 1 {
		t.Fatalf("expected exactly one local_agent_state row, got %d", localStateRows)
	}
}

func TestBootstrapSQLiteSchemaUpgradesLegacySQLiteAndNormalizesBackfills(t *testing.T) {
	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")

	db, err := openSQLiteForTest(dbPath)
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	legacyLocalStateStatements := []string{
		`CREATE TABLE local_agent_state (
			id INTEGER PRIMARY KEY,
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT,
			last_apply_message TEXT,
			desired_version TEXT
		)`,
		`INSERT INTO local_agent_state (id, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, desired_version) VALUES (2, 1, 1, 1, NULL, NULL, NULL)`,
	}
	for _, stmt := range legacyLocalStateStatements {
		if err := db.WithContext(t.Context()).Exec(stmt).Error; err != nil {
			t.Fatalf("seed legacy local_agent_state failed: %q, err=%v", stmt, err)
		}
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("initial BootstrapSQLiteSchema() error = %v", err)
	}

	legacyDataStatements := []string{
		`INSERT INTO agents (id, name, desired_version, platform) VALUES ('legacy-agent', 'legacy-agent', '', NULL)`,
		`INSERT INTO rules (
			id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
			pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_obfs, revision
		) VALUES (7, 'legacy-agent', 'https://legacy.example.com', 'http://127.0.0.1:8096', NULL, NULL, 1, NULL, 1, NULL, NULL, NULL, '', NULL, 0)`,
		`INSERT INTO l4_rules (
			id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
			load_balancing, tuning, relay_chain, relay_obfs, enabled, tags, revision
		) VALUES (8, 'legacy-agent', 'legacy-l4', 'tcp', '0.0.0.0', 25565, '127.0.0.1', 25566, NULL, NULL, NULL, '', NULL, 1, NULL, 0)`,
		`UPDATE local_agent_state SET desired_version = NULL, last_apply_status = NULL, last_apply_message = NULL WHERE id = 1`,
		`INSERT INTO managed_certificates (
			id, domain, enabled, scope, issuer_mode, target_agent_ids, status, usage, certificate_type, self_signed, tags, acme_info, agent_reports
		) VALUES (5, 'legacy.example.com', 1, 'domain', 'master_cf_dns', NULL, NULL, '', NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO relay_listeners (
			id, agent_id, name, listen_host, listen_port, public_host, public_port, enabled, tls_mode, bind_hosts, pin_set, trusted_ca_certificate_ids, tags
		) VALUES (9, 'legacy-agent', 'legacy-relay', '0.0.0.0', 7443, '', NULL, 1, NULL, '', NULL, NULL, NULL)`,
	}
	for _, stmt := range legacyDataStatements {
		if err := db.WithContext(t.Context()).Exec(stmt).Error; err != nil {
			t.Fatalf("seed legacy data failed: %q, err=%v", stmt, err)
		}
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "legacy-agent")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].DesiredVersion != "" || agents[0].Platform != "" {
		t.Fatalf("unexpected agents after legacy bootstrap: %+v", agents)
	}

	rules, err := store.ListHTTPRules(t.Context(), "legacy-agent")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %+v", rules)
	}
	if !rules[0].PassProxyHeaders || rules[0].UserAgent != "" || rules[0].CustomHeadersJSON != "[]" || rules[0].RelayChainJSON != "[]" {
		t.Fatalf("unexpected rule defaults after legacy bootstrap: %+v", rules[0])
	}
	if rules[0].RelayObfs {
		t.Fatalf("expected relay_obfs legacy backfill to default false: %+v", rules[0])
	}

	l4Rules, err := store.ListL4Rules(t.Context(), "legacy-agent")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rules) != 1 || l4Rules[0].RelayObfs {
		t.Fatalf("expected l4 relay_obfs legacy backfill to default false: %+v", l4Rules)
	}

	certs, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %+v", certs)
	}
	if certs[0].Usage != "https" || certs[0].CertificateType != "acme" || certs[0].SelfSigned {
		t.Fatalf("unexpected cert defaults after legacy bootstrap: %+v", certs[0])
	}

	listeners, err := store.ListRelayListeners(t.Context(), "legacy-agent")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("expected 1 relay listener, got %+v", listeners)
	}
	if listeners[0].BindHostsJSON != `["0.0.0.0"]` || listeners[0].PublicHost != "0.0.0.0" || listeners[0].PublicPort != 7443 {
		t.Fatalf("unexpected relay defaults after legacy bootstrap: %+v", listeners[0])
	}

	localState, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if localState.ID != 1 || localState.DesiredVersion != "" || localState.LastApplyStatus != "success" {
		t.Fatalf("unexpected local agent state after legacy bootstrap: %+v", localState)
	}

	var localStateRows int64
	if err := store.db.WithContext(t.Context()).Model(&LocalAgentStateRow{}).Count(&localStateRows).Error; err != nil {
		t.Fatalf("count local_agent_state rows error = %v", err)
	}
	if localStateRows != 1 {
		t.Fatalf("expected singleton local_agent_state after legacy bootstrap, got %d rows", localStateRows)
	}
}

func TestBootstrapSQLiteSchemaHandlesMalformedRelayBindHostsJSON(t *testing.T) {
	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")

	db, err := openSQLiteForTest(dbPath)
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("initial BootstrapSQLiteSchema() error = %v", err)
	}

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO relay_listeners (
		id, agent_id, name, listen_host, listen_port, public_host, public_port, enabled, bind_hosts, tls_mode, pin_set, trusted_ca_certificate_ids, allow_self_signed, tags, revision
	) VALUES (21, 'legacy-agent', 'bad-json', '10.10.0.5', 7443, '', NULL, 1, 'not-json', 'pin_or_ca', '[]', '[]', 0, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed malformed relay listener error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() with malformed bind_hosts error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "legacy-agent")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	listeners, err := store.ListRelayListeners(t.Context(), "legacy-agent")
	if err != nil {
		t.Fatalf("ListRelayListeners() error = %v", err)
	}
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %+v", listeners)
	}
	if listeners[0].BindHostsJSON != `["10.10.0.5"]` || listeners[0].PublicHost != "10.10.0.5" || listeners[0].PublicPort != 7443 {
		t.Fatalf("unexpected listener fallback values: %+v", listeners[0])
	}
}

func TestStorePersistsL4RulesAndVersionPolicies(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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

func TestStoreLoadAgentSnapshotIncludesRelayObfsFlags(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

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

	if err := store.SaveHTTPRules(t.Context(), "relay-obfs-agent", []HTTPRuleRow{{
		ID:                51,
		AgentID:           "relay-obfs-agent",
		FrontendURL:       "https://relay-obfs-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[77]`,
		RelayObfs:         true,
		PassProxyHeaders:  true,
		Revision:          31,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	if err := store.SaveL4Rules(t.Context(), "relay-obfs-agent", []L4RuleRow{{
		ID:                52,
		AgentID:           "relay-obfs-agent",
		Name:              "relay-obfs-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        19000,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      19001,
		BackendsJSON:      `[{"host":"127.0.0.1","port":19001}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[77]`,
		RelayObfs:         true,
		Enabled:           true,
		Revision:          32,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-obfs-agent", AgentSnapshotInput{
		DesiredRevision: 0,
		CurrentRevision: 0,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || !snapshot.Rules[0].RelayObfs {
		t.Fatalf("expected snapshot HTTP relay_obfs=true: %+v", snapshot.Rules)
	}
	if len(snapshot.L4Rules) != 1 || !snapshot.L4Rules[0].RelayObfs {
		t.Fatalf("expected snapshot L4 relay_obfs=true: %+v", snapshot.L4Rules)
	}
}

func TestStoreLoadAgentSnapshotKeepsEffectiveRevisionWhenCurrentMatches(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
	dataRoot := seedSQLiteFixtureFromGORM(t)

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

func TestStoreSaveLocalRuntimeStatePrefersMetadataOverStaleExplicitApplyMetadata(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromGORM(t)

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
		LastApplyRevision: 2,
		LastApplyStatus:   "success",
		LastApplyMessage:  "",
		Status:            "active",
		Metadata: map[string]string{
			"last_sync_error":     "apply failed",
			"last_apply_revision": "9",
			"last_apply_status":   "error",
			"last_apply_message":  "apply failed",
		},
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
	if state.LastApplyRevision != 9 {
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

func seedSQLiteFixtureFromGORM(t *testing.T) string {
	t.Helper()

	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")
	db, err := openSQLiteForTest(dbPath)
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	t.Cleanup(func() {
		closeSQLiteForTest(t, db)
	})

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:               "local",
		Name:             "Local Agent",
		DesiredVersion:   "v1.2.3",
		DesiredRevision:  3,
		CurrentRevision:  2,
		LastApplyStatus:  "success",
		LastApplyMessage: "",
		IsLocal:          true,
		Mode:             "pull",
		Platform:         "linux-amd64",
		TagsJSON:         "[]",
		CapabilitiesJSON: "[]",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{{
		ID:                1001,
		AgentID:           "local",
		FrontendURL:       "https://emby.example.com",
		BackendURL:        "http://emby:8096",
		BackendsJSON:      `[{"url":"http://emby:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  true,
		UserAgent:         "",
		CustomHeadersJSON: `[]`,
		Revision:          3,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	if err := store.db.WithContext(t.Context()).
		Model(&LocalAgentStateRow{}).
		Where("id = ?", 1).
		Updates(map[string]any{
			"desired_revision":    3,
			"current_revision":    2,
			"last_apply_revision": 2,
			"last_apply_status":   "success",
			"last_apply_message":  "",
			"desired_version":     "v1.2.3",
		}).Error; err != nil {
		t.Fatalf("seed local_agent_state error = %v", err)
	}

	return dataRoot
}

func openSQLiteForTest(dbPath string) (*gorm.DB, error) {
	return gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

func closeSQLiteForTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("sqlDB.Close() error = %v", err)
	}
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
