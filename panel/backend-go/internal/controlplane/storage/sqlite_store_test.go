package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/curve25519"
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

func TestBootstrapSQLiteSchemaCreatesProxyColumnsWithDefaults(t *testing.T) {
	dataRoot := t.TempDir()

	db, err := openSQLiteForTest(filepath.Join(dataRoot, "panel.db"))
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() error = %v", err)
	}

	agentColumns := loadSQLiteTableInfo(t, db, "agents")
	assertSQLiteColumnContract(t, agentColumns, "outbound_proxy_url", 1, `""`)
	assertSQLiteColumnContract(t, agentColumns, "traffic_stats_interval", 1, `""`)

	l4Columns := loadSQLiteTableInfo(t, db, "l4_rules")
	assertSQLiteColumnContract(t, l4Columns, "listen_mode", 1, `"tcp"`)
	assertSQLiteColumnContract(t, l4Columns, "proxy_entry_auth", 1, `"{}"`)
	assertSQLiteColumnContract(t, l4Columns, "proxy_egress_mode", 1, `""`)
	assertSQLiteColumnContract(t, l4Columns, "proxy_egress_url", 1, `""`)
	assertSQLiteColumnContract(t, l4Columns, "wireguard_egress_uri", 1, `""`)
}

func TestSQLiteColumnContractIncludesEgressProfiles(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	columns := loadSQLiteTableInfo(t, store.db, "egress_profiles")

	assertSQLiteColumnContract(t, columns, "id", 1, "")
	assertSQLiteColumnContract(t, columns, "name", 1, "")
	assertSQLiteColumnContract(t, columns, "type", 1, "")
	assertSQLiteColumnContract(t, columns, "proxy_url", 1, `""`)
	assertSQLiteColumnContract(t, columns, "wireguard_config_json", 1, `""`)
	assertSQLiteColumnContract(t, columns, "enabled", 1, "1")
	assertSQLiteColumnContract(t, columns, "description", 1, `""`)
	assertSQLiteColumnContract(t, columns, "revision", 1, "0")

	httpColumns := loadSQLiteTableInfo(t, store.db, "rules")
	assertSQLiteColumnContract(t, httpColumns, "egress_profile_id", 0, "")

	l4Columns := loadSQLiteTableInfo(t, store.db, "l4_rules")
	assertSQLiteColumnContract(t, l4Columns, "egress_profile_id", 0, "")
}

func TestStoreSaveListEgressProfilesPreservesSecretMaterial(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	row := EgressProfileRow{
		ID:          41,
		Name:        "socks exit",
		Type:        "socks",
		ProxyURL:    "socks5://user:secret@127.0.0.1:1080",
		Enabled:     true,
		Description: "lab",
		Revision:    7,
	}
	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{row}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}

	got, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(got) != 1 || got[0].ProxyURL != row.ProxyURL {
		t.Fatalf("profiles = %+v, want raw proxy secret", got)
	}
}

func TestStoreSaveListEgressProfilesPersistsDisabledProfile(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{{
		ID:      41,
		Name:    "disabled exit",
		Type:    "socks",
		Enabled: false,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}

	got, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("profiles = %+v, want one disabled profile", got)
	}
}

func TestStoreSaveListEgressProfilesOrdersAndReplacesFullSet(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{
		{ID: 3, Name: "third", Type: "socks", Enabled: true},
		{ID: 1, Name: "first", Type: "http", Enabled: true},
		{ID: 2, Name: "second", Type: "wireguard", Enabled: true},
	}); err != nil {
		t.Fatalf("SaveEgressProfiles(initial) error = %v", err)
	}

	got, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles(initial) error = %v", err)
	}
	if len(got) != 3 || got[0].ID != 1 || got[1].ID != 2 || got[2].ID != 3 {
		t.Fatalf("initial profiles = %+v, want ordered by id", got)
	}

	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{
		{ID: 2, Name: "second only", Type: "wireguard", Enabled: true},
	}); err != nil {
		t.Fatalf("SaveEgressProfiles(smaller) error = %v", err)
	}

	got, err = store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles(smaller) error = %v", err)
	}
	if len(got) != 1 || got[0].ID != 2 || got[0].Name != "second only" {
		t.Fatalf("profiles after smaller replace = %+v", got)
	}

	if err := store.SaveEgressProfiles(t.Context(), nil); err != nil {
		t.Fatalf("SaveEgressProfiles(empty) error = %v", err)
	}

	got, err = store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles(empty) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("profiles after empty replace = %+v, want none", got)
	}
}

func TestStoreLoadAgentSnapshotUsesEgressProfileRevision(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{{
		ID:       41,
		Name:     "socks exit",
		Type:     "socks",
		ProxyURL: "socks5://127.0.0.1:1080",
		Enabled:  true,
		Revision: 7,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{
		DesiredRevision: 1,
		CurrentRevision: 2,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 7 {
		t.Fatalf("snapshot revision = %d, want egress profile revision 7", snapshot.Revision)
	}
	if len(snapshot.EgressProfiles) != 1 || snapshot.EgressProfiles[0].ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("snapshot EgressProfiles = %+v, want raw proxy URL", snapshot.EgressProfiles)
	}
}

func TestStoreLoadAgentSnapshotIncludesPersistedRuleEgressProfileIDs(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	egressProfileID := 41
	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{{
		ID:       egressProfileID,
		Name:     "socks exit",
		Type:     "socks",
		ProxyURL: "socks5://127.0.0.1:1080",
		Enabled:  true,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{{
		ID:                1001,
		AgentID:           "local",
		FrontendURL:       "https://emby.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[]`,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &egressProfileID,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{
		ID:                 2001,
		AgentID:            "local",
		Name:               "tcp",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         25565,
		BackendsJSON:       `[{"host":"127.0.0.1","port":25566}]`,
		LoadBalancingJSON:  `{"strategy":"adaptive"}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "tcp",
		ProxyEntryAuthJSON: `{}`,
		Enabled:            true,
		TagsJSON:           `[]`,
		EgressProfileID:    &egressProfileID,
		Revision:           1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || snapshot.Rules[0].EgressProfileID == nil || *snapshot.Rules[0].EgressProfileID != egressProfileID {
		t.Fatalf("snapshot Rules = %+v, want egress_profile_id %d", snapshot.Rules, egressProfileID)
	}
	if len(snapshot.L4Rules) != 1 || snapshot.L4Rules[0].EgressProfileID == nil || *snapshot.L4Rules[0].EgressProfileID != egressProfileID {
		t.Fatalf("snapshot L4Rules = %+v, want egress_profile_id %d", snapshot.L4Rules, egressProfileID)
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
	if listeners[0].TransportMode != "tls_tcp" {
		t.Fatalf("expected legacy transport_mode default tls_tcp, got %+v", listeners[0])
	}
	if !listeners[0].AllowTransportFallback {
		t.Fatalf("expected legacy allow_transport_fallback default true, got %+v", listeners[0])
	}
	if listeners[0].ObfsMode != "off" {
		t.Fatalf("expected legacy obfs_mode default off, got %+v", listeners[0])
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

func TestBootstrapSchemaMigratesLegacyHTTPRuleFieldsToCanonical(t *testing.T) {
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

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name) VALUES ('legacy-http-agent', 'legacy-http-agent')`).Error; err != nil {
		t.Fatalf("seed legacy agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO rules (
		id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
		pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_layers, relay_obfs, revision
	) VALUES (71, 'legacy-http-agent', 'https://legacy-http.example.com', 'http://127.0.0.1:8096', '[]', NULL, 1, '[]', 0,
		1, '', '[]', '[11,22,33]', '[]', 0, 1)`).Error; err != nil {
		t.Fatalf("seed legacy http rule error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() migration error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "legacy-http-agent")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	rules, err := store.ListHTTPRules(t.Context(), "legacy-http-agent")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %+v", rules)
	}
	if rules[0].BackendsJSON != `[{"url":"http://127.0.0.1:8096"}]` {
		t.Fatalf("unexpected migrated HTTP backends: %+v", rules[0])
	}
	if rules[0].RelayLayersJSON != `[[11],[22],[33]]` {
		t.Fatalf("unexpected migrated HTTP relay layers: %+v", rules[0])
	}
}

func TestBootstrapSchemaMigratesLegacyL4RuleFieldsToCanonical(t *testing.T) {
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

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name) VALUES ('legacy-l4-agent', 'legacy-l4-agent')`).Error; err != nil {
		t.Fatalf("seed legacy agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO l4_rules (
		id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
		load_balancing, tuning, relay_chain, relay_layers, relay_obfs, enabled, tags, revision
	) VALUES (72, 'legacy-l4-agent', 'legacy-l4', 'tcp', '0.0.0.0', 25565, '127.0.0.1', 25566, '[]',
		NULL, NULL, '[44,55,66]', '[]', 0, 1, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed legacy l4 rule error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() migration error = %v", err)
	}

	store, err := NewSQLiteStore(dataRoot, "legacy-l4-agent")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	rules, err := store.ListL4Rules(t.Context(), "legacy-l4-agent")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %+v", rules)
	}
	if rules[0].BackendsJSON != `[{"host":"127.0.0.1","port":25566}]` {
		t.Fatalf("unexpected migrated L4 backends: %+v", rules[0])
	}
	if rules[0].RelayLayersJSON != `[[44],[55],[66]]` {
		t.Fatalf("unexpected migrated L4 relay layers: %+v", rules[0])
	}
}

func TestBootstrapSchemaMigratesLegacyRuleFieldsOutsideSQLiteLegacyBootstrap(t *testing.T) {
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

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name) VALUES ('general-bootstrap-agent', 'general-bootstrap-agent')`).Error; err != nil {
		t.Fatalf("seed legacy agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO rules (
		id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
		pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_layers, relay_obfs, revision
	) VALUES (73, 'general-bootstrap-agent', 'https://general-http.example.com', 'http://10.0.0.10:8096', '[]', NULL, 1, '[]', 0,
		1, '', '[]', '[101,102]', '[]', 0, 1)`).Error; err != nil {
		t.Fatalf("seed legacy http rule error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO l4_rules (
		id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
		load_balancing, tuning, relay_chain, relay_layers, relay_obfs, enabled, tags, revision
	) VALUES (74, 'general-bootstrap-agent', 'general-l4', 'tcp', '0.0.0.0', 25565, '10.0.0.11', 25566, '[]',
		NULL, NULL, '[201,202]', '[]', 0, 1, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed legacy l4 rule error = %v", err)
	}

	if err := BootstrapSchema(t.Context(), db, SchemaOptions{TrafficStatsEnabled: true, SQLiteLegacyMigrations: false}); err != nil {
		t.Fatalf("general BootstrapSchema() migration error = %v", err)
	}

	var httpRows []legacyHTTPRuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&HTTPRuleRow{}).
		Select("id", "agent_id", "backend_url", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 73).
		Find(&httpRows).Error; err != nil {
		t.Fatalf("query migrated HTTP row error = %v", err)
	}
	if len(httpRows) != 1 {
		t.Fatalf("expected 1 HTTP row, got %+v", httpRows)
	}
	if httpRows[0].BackendsJSON != `[{"url":"http://10.0.0.10:8096"}]` {
		t.Fatalf("unexpected migrated HTTP backends: %+v", httpRows[0])
	}
	if httpRows[0].RelayLayersJSON != `[[101],[102]]` {
		t.Fatalf("unexpected migrated HTTP relay layers: %+v", httpRows[0])
	}

	var l4Rows []legacyL4RuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&L4RuleRow{}).
		Select("id", "agent_id", "upstream_host", "upstream_port", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 74).
		Find(&l4Rows).Error; err != nil {
		t.Fatalf("query migrated L4 row error = %v", err)
	}
	if len(l4Rows) != 1 {
		t.Fatalf("expected 1 L4 row, got %+v", l4Rows)
	}
	if l4Rows[0].BackendsJSON != `[{"host":"10.0.0.11","port":25566}]` {
		t.Fatalf("unexpected migrated L4 backends: %+v", l4Rows[0])
	}
	if l4Rows[0].RelayLayersJSON != `[[201],[202]]` {
		t.Fatalf("unexpected migrated L4 relay layers: %+v", l4Rows[0])
	}
}

func TestBootstrapSchemaPreservesCanonicalHTTPAndL4FieldsAcrossRepeatedRuns(t *testing.T) {
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

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name) VALUES ('canonical-agent', 'canonical-agent')`).Error; err != nil {
		t.Fatalf("seed canonical agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO rules (
		id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
		pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_layers, relay_obfs, revision
	) VALUES (81, 'canonical-agent', 'https://canonical-http.example.com', 'http://legacy-http.example.com', '[{"url":"http://canonical-http.example.com"}]', NULL, 1, '[]', 0,
		1, '', '[]', '[91]', '[[92]]', 0, 1)`).Error; err != nil {
		t.Fatalf("seed canonical http rule error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO l4_rules (
		id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
		load_balancing, tuning, relay_chain, relay_layers, relay_obfs, enabled, tags, revision
	) VALUES (82, 'canonical-agent', 'canonical-l4', 'tcp', '0.0.0.0', 25565, 'legacy-l4.example.com', 25566, '[{"host":"canonical-l4.example.com","port":25567}]',
		NULL, NULL, '[93]', '[[94]]', 0, 1, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed canonical l4 rule error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() migration error = %v", err)
	}

	var httpRows []legacyHTTPRuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&HTTPRuleRow{}).
		Select("id", "agent_id", "backend_url", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 81).
		Find(&httpRows).Error; err != nil {
		t.Fatalf("query canonical HTTP row error = %v", err)
	}
	if len(httpRows) != 1 {
		t.Fatalf("canonical HTTP rows = %+v", httpRows)
	}
	if httpRows[0].BackendsJSON != `[{"url":"http://canonical-http.example.com"}]` || httpRows[0].RelayLayersJSON != `[[92]]` {
		t.Fatalf("canonical HTTP values changed after bootstrap: %+v", httpRows[0])
	}

	var l4Rows []legacyL4RuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&L4RuleRow{}).
		Select("id", "agent_id", "upstream_host", "upstream_port", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 82).
		Find(&l4Rows).Error; err != nil {
		t.Fatalf("query canonical L4 row error = %v", err)
	}
	if len(l4Rows) != 1 {
		t.Fatalf("canonical L4 rows = %+v", l4Rows)
	}
	if l4Rows[0].BackendsJSON != `[{"host":"canonical-l4.example.com","port":25567}]` || l4Rows[0].RelayLayersJSON != `[[94]]` {
		t.Fatalf("canonical L4 values changed after bootstrap: %+v", l4Rows[0])
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("second BootstrapSQLiteSchema() migration error = %v", err)
	}

	httpRows = nil
	if err := db.WithContext(t.Context()).
		Model(&HTTPRuleRow{}).
		Select("id", "agent_id", "backend_url", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 81).
		Find(&httpRows).Error; err != nil {
		t.Fatalf("requery canonical HTTP row error = %v", err)
	}
	l4Rows = nil
	if err := db.WithContext(t.Context()).
		Model(&L4RuleRow{}).
		Select("id", "agent_id", "upstream_host", "upstream_port", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 82).
		Find(&l4Rows).Error; err != nil {
		t.Fatalf("requery canonical L4 row error = %v", err)
	}
	if httpRows[0].BackendsJSON != `[{"url":"http://canonical-http.example.com"}]` || httpRows[0].RelayLayersJSON != `[[92]]` {
		t.Fatalf("canonical HTTP values changed on second bootstrap: %+v", httpRows[0])
	}
	if l4Rows[0].BackendsJSON != `[{"host":"canonical-l4.example.com","port":25567}]` || l4Rows[0].RelayLayersJSON != `[[94]]` {
		t.Fatalf("canonical L4 values changed on second bootstrap: %+v", l4Rows[0])
	}
}

func TestBootstrapSchemaDoesNotOverwriteMalformedCanonicalFields(t *testing.T) {
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

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name) VALUES ('malformed-canonical-agent', 'malformed-canonical-agent')`).Error; err != nil {
		t.Fatalf("seed malformed canonical agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO rules (
		id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
		pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_layers, relay_obfs, revision
	) VALUES (83, 'malformed-canonical-agent', 'https://malformed-http.example.com', 'http://legacy-http.example.com', 'not-json', NULL, 1, '[]', 0,
		1, '', '[]', '[95]', '[[]]', 0, 1)`).Error; err != nil {
		t.Fatalf("seed malformed HTTP rule error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO l4_rules (
		id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
		load_balancing, tuning, relay_chain, relay_layers, relay_obfs, enabled, tags, revision
	) VALUES (84, 'malformed-canonical-agent', 'malformed-l4', 'tcp', '0.0.0.0', 25568, 'legacy-l4.example.com', 25569, '[{}]',
		NULL, NULL, '[96]', 'not-json', 0, 1, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed malformed L4 rule error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() migration error = %v", err)
	}

	var httpRows []legacyHTTPRuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&HTTPRuleRow{}).
		Select("id", "agent_id", "backend_url", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 83).
		Find(&httpRows).Error; err != nil {
		t.Fatalf("query malformed HTTP row error = %v", err)
	}
	if len(httpRows) != 1 {
		t.Fatalf("malformed HTTP rows = %+v", httpRows)
	}
	if httpRows[0].BackendsJSON != "not-json" || httpRows[0].RelayLayersJSON != `[[]]` {
		t.Fatalf("malformed HTTP canonical values were overwritten: %+v", httpRows[0])
	}

	var l4Rows []legacyL4RuleMigrationRow
	if err := db.WithContext(t.Context()).
		Model(&L4RuleRow{}).
		Select("id", "agent_id", "upstream_host", "upstream_port", "backends", "relay_chain", "relay_layers").
		Where("id = ?", 84).
		Find(&l4Rows).Error; err != nil {
		t.Fatalf("query malformed L4 row error = %v", err)
	}
	if len(l4Rows) != 1 {
		t.Fatalf("malformed L4 rows = %+v", l4Rows)
	}
	if l4Rows[0].BackendsJSON != `[{}]` || l4Rows[0].RelayLayersJSON != "not-json" {
		t.Fatalf("malformed L4 canonical values were overwritten: %+v", l4Rows[0])
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

func TestBootstrapSQLiteSchemaDoesNotRetryExistingRelayTransportColumns(t *testing.T) {
	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")
	traceLogger := &schemaTraceLogger{}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: traceLogger,
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	defer closeSQLiteForTest(t, db)

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("initial BootstrapSQLiteSchema() error = %v", err)
	}

	traceLogger.Reset()
	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("second BootstrapSQLiteSchema() error = %v", err)
	}

	if traceLogger.duplicateRelayColumnStatements != 0 {
		t.Fatalf("expected no duplicate relay column ALTER statements, got %d", traceLogger.duplicateRelayColumnStatements)
	}
}

func TestNormalizeRelayListenerRowAppliesLegacyTransportDefaultsWithoutClobberingExplicitFalse(t *testing.T) {
	legacy := RelayListenerRow{
		ListenHost:             "0.0.0.0",
		PublicHost:             "",
		TransportMode:          "",
		AllowTransportFallback: false,
		ObfsMode:               "",
	}
	normalizeRelayListenerRow(&legacy)
	if legacy.TransportMode != "tls_tcp" {
		t.Fatalf("legacy TransportMode = %q", legacy.TransportMode)
	}
	if !legacy.AllowTransportFallback {
		t.Fatalf("legacy AllowTransportFallback = %v", legacy.AllowTransportFallback)
	}
	if legacy.ObfsMode != "off" {
		t.Fatalf("legacy ObfsMode = %q", legacy.ObfsMode)
	}

	explicit := RelayListenerRow{
		ListenHost:             "0.0.0.0",
		PublicHost:             "",
		TransportMode:          "quic",
		AllowTransportFallback: false,
		ObfsMode:               "off",
	}
	normalizeRelayListenerRow(&explicit)
	if explicit.TransportMode != "quic" {
		t.Fatalf("explicit TransportMode = %q", explicit.TransportMode)
	}
	if explicit.AllowTransportFallback {
		t.Fatalf("explicit AllowTransportFallback = %v", explicit.AllowTransportFallback)
	}
	if explicit.ObfsMode != "off" {
		t.Fatalf("explicit ObfsMode = %q", explicit.ObfsMode)
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

func TestStoreSaveWireGuardClientsRequiresProfileID(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if err := store.SaveWireGuardClients(t.Context(), "local", 7, []WireGuardClientRow{{
		ID:             1,
		AgentID:        "local",
		ProfileID:      7,
		Name:           "phone",
		PrivateKey:     "private",
		PublicKey:      "public",
		PresharedKey:   "psk",
		Address:        "10.8.0.2/32",
		AllowedIPsJSON: `["10.8.0.2/32"]`,
		DNSJSON:        `[]`,
		Enabled:        true,
	}}); err != nil {
		t.Fatalf("SaveWireGuardClients(seed) error = %v", err)
	}

	err = store.SaveWireGuardClients(t.Context(), "local", 0, nil)
	if err == nil {
		t.Fatal("SaveWireGuardClients(profileID 0) error = nil, want error")
	}
	clients, err := store.ListWireGuardClients(t.Context(), "local", 7)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients after invalid save = %+v, want original client", clients)
	}
}

func TestDeleteAgentRemovesWireGuardSecrets(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	profileIDs := map[string]int{"edge-1": 7, "edge-2": 8}
	clientIDs := map[string]int{"edge-1": 1, "edge-2": 2}
	for _, agentID := range []string{"edge-1", "edge-2"} {
		if err := store.SaveAgent(t.Context(), AgentRow{ID: agentID}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
		if err := store.SaveWireGuardProfiles(t.Context(), agentID, []WireGuardProfileRow{{
			ID:            profileIDs[agentID],
			Name:          "wg",
			PrivateKey:    "profile-private-" + agentID,
			AddressesJSON: `["10.8.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			TagsJSON:      `[]`,
			Enabled:       true,
		}}); err != nil {
			t.Fatalf("SaveWireGuardProfiles(%s) error = %v", agentID, err)
		}
		if err := store.SaveWireGuardClients(t.Context(), agentID, profileIDs[agentID], []WireGuardClientRow{{
			ID:             clientIDs[agentID],
			Name:           "phone",
			PrivateKey:     "client-private-" + agentID,
			PublicKey:      "public-" + agentID,
			PresharedKey:   "psk-" + agentID,
			Address:        "10.8.0.2/32",
			AllowedIPsJSON: `["10.8.0.2/32"]`,
			DNSJSON:        `[]`,
			Enabled:        true,
		}}); err != nil {
			t.Fatalf("SaveWireGuardClients(%s) error = %v", agentID, err)
		}
	}

	if err := store.DeleteAgent(t.Context(), "edge-1"); err != nil {
		t.Fatalf("DeleteAgent() error = %v", err)
	}

	profiles, err := store.ListWireGuardProfiles(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-1) error = %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("deleted agent profiles = %+v, want none", profiles)
	}
	clients, err := store.ListWireGuardClients(t.Context(), "edge-1", 0)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-1) error = %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("deleted agent clients = %+v, want none", clients)
	}

	profiles, err = store.ListWireGuardProfiles(t.Context(), "edge-2")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(edge-2) error = %v", err)
	}
	clients, err = store.ListWireGuardClients(t.Context(), "edge-2", 0)
	if err != nil {
		t.Fatalf("ListWireGuardClients(edge-2) error = %v", err)
	}
	if len(profiles) != 1 || len(clients) != 1 {
		t.Fatalf("other agent profiles=%+v clients=%+v, want preserved rows", profiles, clients)
	}
}

func TestStoreSaveWireGuardClientProfileMutationRollsBackClientsWhenProfilesFail(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	profile := WireGuardProfileRow{
		ID:            7,
		AgentID:       "local",
		Name:          "wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "private",
		AddressesJSON: `["10.8.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		TagsJSON:      `[]`,
		Revision:      1,
	}
	badProfiles := []WireGuardProfileRow{profile, profile}
	err = store.SaveWireGuardClientProfileMutation(t.Context(), "local", 7, []WireGuardClientRow{{
		ID:             1,
		AgentID:        "local",
		ProfileID:      7,
		Name:           "phone",
		PrivateKey:     "private",
		PublicKey:      "public",
		PresharedKey:   "psk",
		Address:        "10.8.0.2/32",
		AllowedIPsJSON: `["10.8.0.2/32"]`,
		DNSJSON:        `[]`,
		Enabled:        true,
	}}, badProfiles)
	if err == nil {
		t.Fatal("SaveWireGuardClientProfileMutation() error = nil, want profile save failure")
	}

	clients, err := store.ListWireGuardClients(t.Context(), "local", 7)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(clients) != 0 {
		t.Fatalf("clients after failed profile mutation = %+v, want rollback", clients)
	}
}

func TestStoreMutateWireGuardClientProfileReadsAndWritesCurrentRowsInTransaction(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	profile := WireGuardProfileRow{
		ID:            7,
		AgentID:       "local",
		Name:          "wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "private",
		AddressesJSON: `["10.8.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		TagsJSON:      `[]`,
		Revision:      1,
	}
	if err := store.SaveWireGuardProfiles(t.Context(), "local", []WireGuardProfileRow{profile}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveWireGuardClients(t.Context(), "local", 7, []WireGuardClientRow{{
		ID:             1,
		AgentID:        "local",
		ProfileID:      7,
		Name:           "phone",
		PrivateKey:     "private-a",
		PublicKey:      "public-a",
		PresharedKey:   "psk-a",
		Address:        "10.8.0.2/32",
		AllowedIPsJSON: `["10.8.0.2/32"]`,
		DNSJSON:        `[]`,
		Enabled:        true,
	}}); err != nil {
		t.Fatalf("SaveWireGuardClients() error = %v", err)
	}

	var observedClientCount int
	err = store.MutateWireGuardClientProfile(t.Context(), "local", 7, func(state WireGuardClientProfileMutation) (WireGuardClientProfileMutation, error) {
		observedClientCount = len(state.Clients)
		state.Clients = append(state.Clients, WireGuardClientRow{
			ID:             2,
			AgentID:        "local",
			ProfileID:      7,
			Name:           "tablet",
			PrivateKey:     "private-b",
			PublicKey:      "public-b",
			PresharedKey:   "psk-b",
			Address:        "10.8.0.3/32",
			AllowedIPsJSON: `["10.8.0.3/32"]`,
			DNSJSON:        `[]`,
			Enabled:        true,
		})
		state.Profiles[state.ProfileIndex].Revision = 2
		return state, nil
	})
	if err != nil {
		t.Fatalf("MutateWireGuardClientProfile() error = %v", err)
	}
	if observedClientCount != 1 {
		t.Fatalf("callback observed %d clients, want current 1", observedClientCount)
	}
	clients, err := store.ListWireGuardClients(t.Context(), "local", 7)
	if err != nil {
		t.Fatalf("ListWireGuardClients() error = %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients after mutation = %+v, want 2", clients)
	}
	profiles, err := store.ListWireGuardProfiles(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(profiles) != 1 || profiles[0].Revision != 2 {
		t.Fatalf("profiles after mutation = %+v, want revision 2", profiles)
	}
}

func TestSQLiteStorePersistsAgentOutboundProxyURL(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	agent := AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: `["l4_rules","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("ListAgents() len = %d", len(agents))
	}
	got := agents[0]
	if got.OutboundProxyURL != agent.OutboundProxyURL {
		t.Fatalf("OutboundProxyURL = %q, want %q", got.OutboundProxyURL, agent.OutboundProxyURL)
	}
}

func TestSQLiteStorePersistsAgentTrafficStatsInterval(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	agent := AgentRow{
		ID:                   "edge-a",
		Name:                 "Edge A",
		CapabilitiesJSON:     `["http_rules"]`,
		TrafficStatsInterval: "30s",
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("ListAgents() len = %d", len(agents))
	}
	got := agents[0]
	if got.TrafficStatsInterval != agent.TrafficStatsInterval {
		t.Fatalf("TrafficStatsInterval = %q, want %q", got.TrafficStatsInterval, agent.TrafficStatsInterval)
	}
}

func TestSQLiteStorePersistsL4ProxyEntryFields(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	row := L4RuleRow{
		ID:                 10,
		AgentID:            "edge-a",
		Name:               "proxy-entry",
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         1080,
		UpstreamHost:       "",
		UpstreamPort:       0,
		BackendsJSON:       `[]`,
		LoadBalancingJSON:  `{"strategy":"round_robin"}`,
		TuningJSON:         `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:     `[101]`,
		RelayLayersJSON:    `[[101]]`,
		ListenMode:         "proxy",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
		ProxyEgressMode:    "relay",
		ProxyEgressURL:     "socks://user:pass@127.0.0.1:1080",
		Enabled:            true,
		TagsJSON:           `[]`,
		Revision:           1,
	}
	if err := store.SaveL4Rules(ctx, "edge-a", []L4RuleRow{row}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	rows, err := store.ListL4Rules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListL4Rules() len = %d", len(rows))
	}
	got := rows[0]
	if got.ListenMode != row.ListenMode ||
		got.ProxyEntryAuthJSON != row.ProxyEntryAuthJSON ||
		got.ProxyEgressMode != row.ProxyEgressMode ||
		got.ProxyEgressURL != row.ProxyEgressURL {
		t.Fatalf("proxy fields not persisted: %+v", got)
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

func TestStorePersistsHTTPRuleRelayLayers(t *testing.T) {
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
		FrontendURL:       "https://fanout.example.com",
		BackendURL:        "http://emby:8096",
		BackendsJSON:      `[{"url":"http://emby:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[1,4]`,
		RelayLayersJSON:   `[[1,2],[4,5]]`,
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
	if len(rules) != 1 || rules[0].RelayLayersJSON != `[[1,2],[4,5]]` {
		t.Fatalf("ListHTTPRules() relay_layers = %+v", rules)
	}
}

func TestStorePersistsL4RuleRelayLayers(t *testing.T) {
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
		ID:                7,
		AgentID:           "local",
		Name:              "fanout-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9000,
		UpstreamHost:      "backend",
		UpstreamPort:      9001,
		BackendsJSON:      `[{"host":"backend","port":9001}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:    `[7,9]`,
		RelayLayersJSON:   `[[7,8],[9]]`,
		Enabled:           true,
		TagsJSON:          `[]`,
		Revision:          15,
	}})
	if err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	rules, err := store.ListL4Rules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rules) != 1 || rules[0].RelayLayersJSON != `[[7,8],[9]]` {
		t.Fatalf("ListL4Rules() relay_layers = %+v", rules)
	}
}

func TestStoreNormalizesAdaptiveLoadBalancingForHTTPAndL4(t *testing.T) {
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

	if err := store.SaveHTTPRules(t.Context(), "local", []HTTPRuleRow{{
		ID:                77,
		AgentID:           "local",
		FrontendURL:       "https://adaptive-http.example.com",
		BackendURL:        "http://emby:8096",
		BackendsJSON:      `[{"url":"http://emby:8096"}]`,
		LoadBalancingJSON: `{}`,
		Enabled:           true,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  true,
		Revision:          17,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	httpRules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListHTTPRules() error = %v", err)
	}
	if len(httpRules) != 1 || parseLoadBalancingStrategy(httpRules[0].LoadBalancingJSON).Strategy != "adaptive" {
		t.Fatalf("ListHTTPRules() = %+v", httpRules)
	}
	if httpRules[0].LoadBalancingJSON != `{"strategy":"adaptive"}` {
		t.Fatalf("http load_balancing_json = %q", httpRules[0].LoadBalancingJSON)
	}

	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{
		ID:                78,
		AgentID:           "local",
		Name:              "adaptive-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		UpstreamHost:      "upstream",
		UpstreamPort:      9444,
		BackendsJSON:      `[{"host":"upstream","port":9444}]`,
		LoadBalancingJSON: `{}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		Revision:          18,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	l4Rules, err := store.ListL4Rules(t.Context(), "local")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(l4Rules) != 1 || parseL4LoadBalancingStrategy(t, l4Rules[0].LoadBalancingJSON).Strategy != "adaptive" {
		t.Fatalf("ListL4Rules() = %+v", l4Rules)
	}
	if l4Rules[0].LoadBalancingJSON != `{"strategy":"adaptive"}` {
		t.Fatalf("l4 load_balancing_json = %q", l4Rules[0].LoadBalancingJSON)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || snapshot.Rules[0].LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("snapshot HTTP rules = %+v", snapshot.Rules)
	}
	if len(snapshot.L4Rules) != 1 || snapshot.L4Rules[0].LoadBalancing.Strategy != "adaptive" {
		t.Fatalf("snapshot L4 rules = %+v", snapshot.L4Rules)
	}
}

func parseL4LoadBalancingStrategy(t *testing.T, raw string) LoadBalancing {
	t.Helper()

	var lb LoadBalancing
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &lb); err != nil {
		t.Fatalf("json.Unmarshal(load_balancing) error = %v", err)
	}
	if strings.TrimSpace(lb.Strategy) == "" {
		return LoadBalancing{Strategy: "adaptive"}
	}
	return LoadBalancing{Strategy: strings.ToLower(strings.TrimSpace(lb.Strategy))}
}

func TestSnapshotHTTPRulesUsesCanonicalBackendsAndRelayLayersOnly(t *testing.T) {
	rules := SnapshotHTTPRules([]HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://app.example.com",
		BackendURL:        "http://legacy.example.com",
		BackendsJSON:      `[{"url":"http://canonical.example.com"}]`,
		LoadBalancingJSON: `{}`,
		Enabled:           true,
		RelayChainJSON:    `[101]`,
		RelayLayersJSON:   `[[201,202]]`,
		CustomHeadersJSON: `[]`,
		Revision:          3,
	}})

	if len(rules) != 1 {
		t.Fatalf("expected one http rule, got %d", len(rules))
	}
	if rules[0].BackendURL != "" {
		t.Fatalf("BackendURL = %q, want empty legacy compatibility field", rules[0].BackendURL)
	}
	if len(rules[0].RelayChain) != 0 {
		t.Fatalf("RelayChain = %+v, want empty legacy compatibility field", rules[0].RelayChain)
	}
	if len(rules[0].Backends) != 1 || rules[0].Backends[0].URL != "http://canonical.example.com" {
		t.Fatalf("Backends = %+v", rules[0].Backends)
	}
	if len(rules[0].RelayLayers) != 1 || len(rules[0].RelayLayers[0]) != 2 || rules[0].RelayLayers[0][0] != 201 || rules[0].RelayLayers[0][1] != 202 {
		t.Fatalf("RelayLayers = %+v", rules[0].RelayLayers)
	}
}

func TestSnapshotL4RulesUsesCanonicalBackendsAndRelayLayersOnly(t *testing.T) {
	wireGuardProfileID := 7
	rules := SnapshotL4Rules([]L4RuleRow{{
		ID:                  1,
		AgentID:             "local",
		Name:                "canonical-l4",
		Protocol:            "tcp",
		ListenHost:          "127.0.0.1",
		ListenPort:          9443,
		UpstreamHost:        "legacy.example.com",
		UpstreamPort:        9444,
		BackendsJSON:        `[{"host":"canonical.example.com","port":9445}]`,
		LoadBalancingJSON:   `{}`,
		TuningJSON:          `{}`,
		RelayChainJSON:      `[101]`,
		RelayLayersJSON:     `[[201,202]]`,
		ListenMode:          "wireguard",
		WireGuardProfileID:  &wireGuardProfileID,
		WireGuardListenHost: "10.44.0.1",
		Enabled:             true,
		Revision:            3,
	}})

	if len(rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(rules))
	}
	if rules[0].UpstreamHost != "" {
		t.Fatalf("UpstreamHost = %q, want empty legacy compatibility field", rules[0].UpstreamHost)
	}
	if rules[0].UpstreamPort != 0 {
		t.Fatalf("UpstreamPort = %d, want zero legacy compatibility field", rules[0].UpstreamPort)
	}
	if len(rules[0].RelayChain) != 0 {
		t.Fatalf("RelayChain = %+v, want empty legacy compatibility field", rules[0].RelayChain)
	}
	if len(rules[0].Backends) != 1 || rules[0].Backends[0].Host != "canonical.example.com" || rules[0].Backends[0].Port != 9445 {
		t.Fatalf("Backends = %+v", rules[0].Backends)
	}
	if len(rules[0].RelayLayers) != 1 || len(rules[0].RelayLayers[0]) != 2 || rules[0].RelayLayers[0][0] != 201 || rules[0].RelayLayers[0][1] != 202 {
		t.Fatalf("RelayLayers = %+v", rules[0].RelayLayers)
	}
	if rules[0].WireGuardProfileID == nil || *rules[0].WireGuardProfileID != wireGuardProfileID || rules[0].WireGuardListenHost != "10.44.0.1" {
		t.Fatalf("WireGuard fields = profile %v listen_host %q", rules[0].WireGuardProfileID, rules[0].WireGuardListenHost)
	}
}

func TestSnapshotRelayListenersPreservesWireGuardProfileID(t *testing.T) {
	wireGuardProfileID := 12
	listeners := snapshotRelayListeners([]RelayListenerRow{{
		ID:                 1,
		AgentID:            "local",
		Name:               "wg-relay",
		BindHostsJSON:      `["0.0.0.0"]`,
		ListenHost:         "0.0.0.0",
		ListenPort:         7443,
		PublicHost:         "relay.example.test",
		PublicPort:         7443,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &wireGuardProfileID,
		Revision:           4,
	}}, map[string]string{"local": "Local"})

	if len(listeners) != 1 {
		t.Fatalf("listeners = %+v", listeners)
	}
	if listeners[0].TransportMode != "wireguard" || listeners[0].WireGuardProfileID == nil || *listeners[0].WireGuardProfileID != wireGuardProfileID {
		t.Fatalf("listener = %+v", listeners[0])
	}
}

func TestSnapshotL4RulesPreservesProxyEntryPasswordAndTrimsUsername(t *testing.T) {
	rules := SnapshotL4Rules([]L4RuleRow{{
		ID:                 1,
		AgentID:            "local",
		Protocol:           "tcp",
		ListenHost:         "127.0.0.1",
		ListenPort:         1080,
		BackendsJSON:       `[]`,
		LoadBalancingJSON:  `{}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[101]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "proxy",
		ProxyEntryAuthJSON: `{"enabled":true,"username":" u ","password":" p "}`,
		ProxyEgressMode:    "proxy",
		ProxyEgressURL:     "socks://127.0.0.1:1080",
		Enabled:            true,
		Revision:           3,
	}})

	if len(rules) != 1 {
		t.Fatalf("expected one l4 rule, got %d", len(rules))
	}
	auth := rules[0].ProxyEntryAuth
	if !auth.Enabled {
		t.Fatalf("ProxyEntryAuth.Enabled = false")
	}
	if auth.Username != "u" {
		t.Fatalf("ProxyEntryAuth.Username = %q", auth.Username)
	}
	if auth.Password != " p " {
		t.Fatalf("ProxyEntryAuth.Password = %q", auth.Password)
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

func TestLoadAgentSnapshotIncludesLocalAgentConfig(t *testing.T) {
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
		ID:                   "local",
		Name:                 "local",
		IsLocal:              true,
		OutboundProxyURL:     "socks://127.0.0.1:1080",
		TrafficStatsInterval: "30s",
		TrafficBlocked:       true,
		TrafficBlockReason:   "monthly quota exceeded",
	}); err != nil {
		t.Fatalf("SaveAgent(local) error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(local) error = %v", err)
	}
	if snapshot.AgentConfig.OutboundProxyURL != "socks://127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL = %q", snapshot.AgentConfig.OutboundProxyURL)
	}
	if snapshot.AgentConfig.TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval = %q", snapshot.AgentConfig.TrafficStatsInterval)
	}
	if !snapshot.AgentConfig.TrafficBlocked || snapshot.AgentConfig.TrafficBlockReason != "monthly quota exceeded" {
		t.Fatalf("AgentConfig traffic block = %+v", snapshot.AgentConfig)
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
		RelayLayersJSON:   `[[3]]`,
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

func TestStoreLoadsAgentSnapshotWithReferencedRelayListenersAndCertificates(t *testing.T) {
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
		RelayLayersJSON:   `[[11,22]]`,
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
	if snapshot.RelayListeners[1].AgentName != "remote-agent-b" {
		t.Fatalf("RelayListeners[1].AgentName = %q", snapshot.RelayListeners[1].AgentName)
	}
	if len(snapshot.Certificates) != 3 {
		t.Fatalf("Certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 3 {
		t.Fatalf("CertificatePolicies = %+v", snapshot.CertificatePolicies)
	}
	if !containsCertificateID(snapshot.Certificates, 10) || !containsCertificateID(snapshot.Certificates, 11) || !containsCertificateID(snapshot.Certificates, 12) {
		t.Fatalf("Certificates missing expected relay dependency ids 10/11/12: %+v", snapshot.Certificates)
	}
	if !containsPolicyID(snapshot.CertificatePolicies, 10) || !containsPolicyID(snapshot.CertificatePolicies, 11) || !containsPolicyID(snapshot.CertificatePolicies, 12) {
		t.Fatalf("CertificatePolicies missing expected relay dependency ids 10/11/12: %+v", snapshot.CertificatePolicies)
	}
}

func TestStoreLoadsAgentSnapshotWithRelayLayerOnlyListeners(t *testing.T) {
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

	for _, agentID := range []string{"relay-layer-agent", "relay-layer-peer", "relay-layer-l4-peer"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:             agentID,
			Name:           agentID,
			AgentToken:     "token-" + agentID,
			DesiredVersion: "1.2.3",
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	if err := store.SaveHTTPRules(t.Context(), "relay-layer-agent", []HTTPRuleRow{{
		ID:                41,
		AgentID:           "relay-layer-agent",
		FrontendURL:       "https://layer-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[11]`,
		RelayLayersJSON:   `[[11,22]]`,
		PassProxyHeaders:  true,
		Revision:          5,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "relay-layer-agent", []L4RuleRow{{
		ID:                42,
		AgentID:           "relay-layer-agent",
		Name:              "layer-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9000,
		UpstreamHost:      "backend",
		UpstreamPort:      9001,
		BackendsJSON:      `[{"host":"backend","port":9001}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		RelayChainJSON:    `[11]`,
		RelayLayersJSON:   `[[11,33]]`,
		Enabled:           true,
		Revision:          6,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "relay-layer-agent", []RelayListenerRow{{
		ID:         11,
		AgentID:    "relay-layer-agent",
		Name:       "relay-local",
		ListenHost: "127.0.0.1",
		ListenPort: 7443,
		PublicHost: "relay-local.example.com",
		PublicPort: 7443,
		Enabled:    true,
		Revision:   7,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-layer-agent) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-layer-peer", []RelayListenerRow{{
		ID:         22,
		AgentID:    "relay-layer-peer",
		Name:       "relay-http-peer",
		ListenHost: "127.0.0.1",
		ListenPort: 8443,
		PublicHost: "relay-http-peer.example.com",
		PublicPort: 8443,
		Enabled:    true,
		Revision:   8,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-layer-peer) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-layer-l4-peer", []RelayListenerRow{{
		ID:         33,
		AgentID:    "relay-layer-l4-peer",
		Name:       "relay-l4-peer",
		ListenHost: "127.0.0.1",
		ListenPort: 9443,
		PublicHost: "relay-l4-peer.example.com",
		PublicPort: 9443,
		Enabled:    true,
		Revision:   9,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-layer-l4-peer) error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-layer-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.RelayListeners) != 3 {
		t.Fatalf("RelayListeners = %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[0].ID != 11 || snapshot.RelayListeners[1].ID != 22 || snapshot.RelayListeners[2].ID != 33 {
		t.Fatalf("RelayListeners order/ids = %+v", snapshot.RelayListeners)
	}
}

func TestStoreLoadAgentSnapshotDoesNotIncludeWireGuardProfilesForRemoteRelayListeners(t *testing.T) {
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

	for _, agentID := range []string{"relay-wg-client", "relay-wg-peer"} {
		capabilities := `["wireguard"]`
		if agentID == "relay-wg-peer" {
			capabilities = `["wireguard"]`
		}
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: capabilities,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
	profileID := 17
	unrelatedProfileID := 18
	if err := store.SaveWireGuardProfiles(t.Context(), "relay-wg-peer", []WireGuardProfileRow{
		{
			ID:            profileID,
			AgentID:       "relay-wg-peer",
			Name:          "peer-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "peer-private-key",
			ListenPort:    51820,
			AddressesJSON: `["10.90.0.2/32"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      12,
		},
		{
			ID:            unrelatedProfileID,
			AgentID:       "relay-wg-peer",
			Name:          "unrelated-peer-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "unrelated-private-key",
			ListenPort:    51821,
			AddressesJSON: `["10.91.0.2/32"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      14,
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(peer) error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "relay-wg-client", []HTTPRuleRow{{
		ID:                51,
		AgentID:           "relay-wg-client",
		FrontendURL:       "https://relay-wg-client.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		RelayLayersJSON:   `[[44]]`,
		Enabled:           true,
		Revision:          5,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-wg-peer", []RelayListenerRow{{
		ID:                 44,
		AgentID:            "relay-wg-peer",
		Name:               "relay-wg-peer",
		ListenHost:         "10.90.0.2",
		BindHostsJSON:      `["10.90.0.2"]`,
		ListenPort:         7443,
		PublicHost:         "relay-wg-peer.example.com",
		PublicPort:         7443,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &profileID,
		Revision:           13,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(peer) error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-wg-client", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.RelayListeners) != 1 || snapshot.RelayListeners[0].ID != 44 {
		t.Fatalf("RelayListeners = %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[0].WireGuardProfileID == nil || *snapshot.RelayListeners[0].WireGuardProfileID != profileID {
		t.Fatalf("Relay listener WireGuardProfileID = %v", snapshot.RelayListeners[0].WireGuardProfileID)
	}
	if len(snapshot.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want no remote relay owner profiles", snapshot.WireGuardProfiles)
	}
	for _, profile := range snapshot.WireGuardProfiles {
		if profile.PrivateKey == "peer-private-key" || profile.PrivateKey == "unrelated-private-key" {
			t.Fatalf("leaked remote relay WireGuard private key in profile %+v", profile)
		}
	}

	ownerSnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-wg-peer", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(owner) error = %v", err)
	}
	if len(ownerSnapshot.WireGuardProfiles) != 1 {
		t.Fatalf("owner WireGuardProfiles = %+v, want referenced local owner profile", ownerSnapshot.WireGuardProfiles)
	}
	if ownerSnapshot.WireGuardProfiles[0].ID != profileID || ownerSnapshot.WireGuardProfiles[0].AgentID != "relay-wg-peer" || ownerSnapshot.WireGuardProfiles[0].PrivateKey != "peer-private-key" {
		t.Fatalf("owner WireGuardProfiles[0] = %+v", ownerSnapshot.WireGuardProfiles[0])
	}
}

func TestStoreLoadAgentSnapshotDoesNotRecoverRemoteWireGuardProfilesByNumericID(t *testing.T) {
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

	for _, agentID := range []string{"wg-id-client", "wg-id-peer"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: `["wireguard"]`,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}
	profileID := 77
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-id-peer", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-id-peer",
		Name:          "peer-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "numeric-id-peer-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.77.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      11,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(peer) error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "wg-id-client", []HTTPRuleRow{{
		ID:                       771,
		AgentID:                  "wg-id-client",
		FrontendURL:              "http://numeric-id-http.internal",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "10.77.0.1",
		WireGuardEntryListenPort: 8080,
		Revision:                 12,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-id-client", []L4RuleRow{{
		ID:                 772,
		AgentID:            "wg-id-client",
		Name:               "numeric-id-l4",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         9443,
		BackendsJSON:       `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON:  `{"strategy":"adaptive"}`,
		TuningJSON:         `{}`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Enabled:            true,
		Revision:           13,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-id-client", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || len(snapshot.L4Rules) != 1 {
		t.Fatalf("snapshot rules = http %+v l4 %+v", snapshot.Rules, snapshot.L4Rules)
	}
	if len(snapshot.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want no remote numeric-id profile recovery", snapshot.WireGuardProfiles)
	}
	for _, profile := range snapshot.WireGuardProfiles {
		if profile.PrivateKey == "numeric-id-peer-private-key" {
			t.Fatalf("leaked remote WireGuard private key by numeric ID in profile %+v", profile)
		}
	}
}

func TestStoreLoadAgentSnapshotDoesNotIncludeLocalWireGuardProfileForRemoteRelayNumericCollision(t *testing.T) {
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
		ID:               "wg-collision-target",
		Name:             "wg-collision-target",
		AgentToken:       "token-wg-collision-target",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(target) error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:               "wg-collision-peer",
		Name:             "wg-collision-peer",
		AgentToken:       "token-wg-collision-peer",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent(peer) error = %v", err)
	}

	profileID := 88
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-collision-target", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-collision-target",
		Name:          "target-unrelated",
		Mode:          "generic_wireguard",
		PrivateKey:    "target-private-key",
		ListenPort:    51888,
		AddressesJSON: `["10.88.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      8,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(target) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "wg-collision-peer", []RelayListenerRow{{
		ID:                 881,
		AgentID:            "wg-collision-peer",
		Name:               "remote-wireguard-relay",
		ListenHost:         "10.88.0.2",
		BindHostsJSON:      `["10.88.0.2"]`,
		ListenPort:         7443,
		PublicHost:         "wg-collision-peer.example.com",
		PublicPort:         7443,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &profileID,
		Revision:           9,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(peer) error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "wg-collision-target", []HTTPRuleRow{{
		ID:                882,
		AgentID:           "wg-collision-target",
		FrontendURL:       "https://wg-collision-target.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		RelayLayersJSON:   `[[881]]`,
		Enabled:           true,
		Revision:          10,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-collision-target", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.RelayListeners) != 1 || snapshot.RelayListeners[0].ID != 881 {
		t.Fatalf("RelayListeners = %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[0].WireGuardProfileID == nil || *snapshot.RelayListeners[0].WireGuardProfileID != profileID {
		t.Fatalf("Relay listener WireGuardProfileID = %v", snapshot.RelayListeners[0].WireGuardProfileID)
	}
	if len(snapshot.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want no local profile from remote relay numeric collision", snapshot.WireGuardProfiles)
	}
	for _, profile := range snapshot.WireGuardProfiles {
		if profile.PrivateKey == "target-private-key" {
			t.Fatalf("leaked local WireGuard private key via remote relay numeric collision: %+v", profile)
		}
	}
}

func TestStoreLoadAgentSnapshotGeneratesWireGuardPeerForCrossAgentRelay(t *testing.T) {
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

	for _, agentID := range []string{"wg-relay-caller", "wg-relay-owner"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: `["wireguard","l4","relay"]`,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	remoteProfileID := 71
	remotePrivateKey := "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-relay-owner", []WireGuardProfileRow{{
		ID:             remoteProfileID,
		AgentID:        "wg-relay-owner",
		Name:           "owner-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     remotePrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "relay-owner.example.com:51820",
		AddressesJSON:  `["10.71.0.254/32"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1280,
		Enabled:        true,
		TagsJSON:       `["system:default-wireguard"]`,
		Revision:       12,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(owner) error = %v", err)
	}

	localProfileID := 72
	localPrivateKey := "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-relay-caller", []WireGuardProfileRow{{
		ID:            localProfileID,
		AgentID:       "wg-relay-caller",
		Name:          "caller-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    localPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.72.0.1/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		MTU:           1280,
		Enabled:       true,
		TagsJSON:      `["system:default-wireguard"]`,
		Revision:      13,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(caller) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "wg-relay-owner", []RelayListenerRow{{
		ID:                 711,
		AgentID:            "wg-relay-owner",
		Name:               "owner-relay-wg",
		ListenHost:         "10.71.0.1",
		BindHostsJSON:      `["10.71.0.1"]`,
		ListenPort:         7443,
		PublicHost:         "relay-owner.example.com",
		PublicPort:         51820,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &remoteProfileID,
		Revision:           14,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(owner) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-relay-caller", []L4RuleRow{{
		ID:                712,
		AgentID:           "wg-relay-caller",
		Name:              "caller-through-wg-relay",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[[711]]`,
		ListenMode:        "tcp",
		Enabled:           true,
		Revision:          15,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-relay-caller", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want caller default profile with generated peer", snapshot.WireGuardProfiles)
	}
	profile := snapshot.WireGuardProfiles[0]
	if profile.ID != localProfileID || profile.AgentID != "wg-relay-caller" || profile.PrivateKey != localPrivateKey {
		t.Fatalf("WireGuardProfiles[0] = %+v, want local caller profile without remote private key", profile)
	}
	if profile.PrivateKey == remotePrivateKey {
		t.Fatalf("leaked remote relay WireGuard private key in profile %+v", profile)
	}
	if len(profile.Peers) != 1 {
		t.Fatalf("generated peers = %+v, want one remote relay peer", profile.Peers)
	}
	peer := profile.Peers[0]
	if peer.PublicKey != testWireGuardPublicKeyFromPrivate(t, remotePrivateKey) {
		t.Fatalf("peer public_key = %q, want remote profile public key", peer.PublicKey)
	}
	if peer.Endpoint != "relay-owner.example.com:51820" {
		t.Fatalf("peer endpoint = %q, want relay public endpoint", peer.Endpoint)
	}
	if !stringSliceContains(peer.AllowedIPs, "10.71.0.1/32") {
		t.Fatalf("peer allowed_ips = %+v, want relay listener tunnel address", peer.AllowedIPs)
	}
	if peer.PersistentKeepaliveSeconds != 25 {
		t.Fatalf("peer keepalive = %d, want 25", peer.PersistentKeepaliveSeconds)
	}

	ownerSnapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-relay-owner", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(owner) error = %v", err)
	}
	if len(ownerSnapshot.WireGuardProfiles) != 1 {
		t.Fatalf("owner WireGuardProfiles = %+v, want owner profile with generated caller peer", ownerSnapshot.WireGuardProfiles)
	}
	ownerProfile := ownerSnapshot.WireGuardProfiles[0]
	if ownerProfile.ID != remoteProfileID || ownerProfile.AgentID != "wg-relay-owner" || ownerProfile.PrivateKey != remotePrivateKey {
		t.Fatalf("owner WireGuardProfiles[0] = %+v, want local owner profile", ownerProfile)
	}
	if len(ownerProfile.Peers) != 1 {
		t.Fatalf("owner generated peers = %+v, want one caller peer", ownerProfile.Peers)
	}
	ownerPeer := ownerProfile.Peers[0]
	if ownerPeer.PublicKey != testWireGuardPublicKeyFromPrivate(t, localPrivateKey) {
		t.Fatalf("owner peer public_key = %q, want caller profile public key", ownerPeer.PublicKey)
	}
	if ownerPeer.Endpoint != "" {
		t.Fatalf("owner peer endpoint = %q, want empty endpoint for behind-NAT caller", ownerPeer.Endpoint)
	}
	if len(ownerPeer.AllowedIPs) != 1 || ownerPeer.AllowedIPs[0] != "10.72.0.1/32" {
		t.Fatalf("owner peer allowed_ips = %+v, want caller WireGuard address", ownerPeer.AllowedIPs)
	}

	persisted, err := store.ListWireGuardProfiles(t.Context(), "wg-relay-caller")
	if err != nil {
		t.Fatalf("ListWireGuardProfiles(caller) error = %v", err)
	}
	if len(persisted) != 1 || len(parseWireGuardPeers(persisted[0].PeersJSON)) != 0 {
		t.Fatalf("persisted caller profiles = %+v, want generated peer only in snapshot", persisted)
	}
}

func TestStoreLoadAgentSnapshotBumpsCallerRevisionForRelayOwnerWireGuardProfileRevision(t *testing.T) {
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

	for _, agentID := range []string{"wg-rev-caller", "wg-rev-owner"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: `["wireguard","l4","relay"]`,
			DesiredRevision:  20,
			CurrentRevision:  20,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	ownerProfileID := 171
	ownerPrivateKey := "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-rev-owner", []WireGuardProfileRow{{
		ID:             ownerProfileID,
		AgentID:        "wg-rev-owner",
		Name:           "owner-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     ownerPrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "rotated-owner.example.com:51820",
		AddressesJSON:  `["10.171.0.254/32"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1280,
		Enabled:        true,
		TagsJSON:       `["system:default-wireguard"]`,
		Revision:       31,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(owner) error = %v", err)
	}

	callerProfileID := 172
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-rev-caller", []WireGuardProfileRow{{
		ID:            callerProfileID,
		AgentID:       "wg-rev-caller",
		Name:          "caller-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ListenPort:    51821,
		AddressesJSON: `["10.172.0.1/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		MTU:           1280,
		Enabled:       true,
		TagsJSON:      `["system:default-wireguard"]`,
		Revision:      20,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(caller) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "wg-rev-owner", []RelayListenerRow{{
		ID:                 1711,
		AgentID:            "wg-rev-owner",
		Name:               "owner-relay-wg",
		ListenHost:         "10.171.0.1",
		BindHostsJSON:      `["10.171.0.1"]`,
		ListenPort:         7443,
		PublicHost:         "owner-listener.example.com",
		PublicPort:         51820,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &ownerProfileID,
		Revision:           14,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(owner) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-rev-caller", []L4RuleRow{{
		ID:                1712,
		AgentID:           "wg-rev-caller",
		Name:              "caller-through-wg-relay",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[[1711]]`,
		ListenMode:        "tcp",
		Enabled:           true,
		Revision:          15,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-rev-caller", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 31 {
		t.Fatalf("snapshot revision = %d, want owner WireGuard profile revision 31", snapshot.Revision)
	}
	if len(snapshot.WireGuardProfiles) != 1 || len(snapshot.WireGuardProfiles[0].Peers) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want generated owner peer", snapshot.WireGuardProfiles)
	}
	peer := snapshot.WireGuardProfiles[0].Peers[0]
	if peer.PublicKey != testWireGuardPublicKeyFromPrivate(t, ownerPrivateKey) {
		t.Fatalf("peer public_key = %q, want owner public key", peer.PublicKey)
	}
	if peer.Endpoint != "rotated-owner.example.com:51820" {
		t.Fatalf("peer endpoint = %q, want owner profile public endpoint", peer.Endpoint)
	}
}

func TestStoreLoadAgentSnapshotBumpsOwnerRevisionForRelayCallerWireGuardProfileRevision(t *testing.T) {
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

	for _, agentID := range []string{"wg-owner-rev-caller", "wg-owner-rev-owner"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: `["wireguard","l4","relay"]`,
			DesiredRevision:  20,
			CurrentRevision:  20,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	ownerProfileID := 271
	ownerPrivateKey := "AwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-owner-rev-owner", []WireGuardProfileRow{{
		ID:             ownerProfileID,
		AgentID:        "wg-owner-rev-owner",
		Name:           "owner-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     ownerPrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "owner.example.com:51820",
		AddressesJSON:  `["10.27.1.254/32"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1280,
		Enabled:        true,
		TagsJSON:       `["system:default-wireguard"]`,
		Revision:       20,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(owner) error = %v", err)
	}

	callerProfileID := 272
	callerPrivateKey := "BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-owner-rev-caller", []WireGuardProfileRow{{
		ID:            callerProfileID,
		AgentID:       "wg-owner-rev-caller",
		Name:          "caller-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    callerPrivateKey,
		ListenPort:    51821,
		AddressesJSON: `["10.72.0.9/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		MTU:           1280,
		Enabled:       true,
		TagsJSON:      `["system:default-wireguard"]`,
		Revision:      34,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(caller) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "wg-owner-rev-owner", []RelayListenerRow{{
		ID:                 2711,
		AgentID:            "wg-owner-rev-owner",
		Name:               "owner-relay-wg",
		ListenHost:         "10.27.1.1",
		BindHostsJSON:      `["10.27.1.1"]`,
		ListenPort:         7443,
		PublicHost:         "owner.example.com",
		PublicPort:         51820,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &ownerProfileID,
		Revision:           14,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(owner) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-owner-rev-caller", []L4RuleRow{{
		ID:                2712,
		AgentID:           "wg-owner-rev-caller",
		Name:              "caller-through-wg-relay",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[[2711]]`,
		ListenMode:        "tcp",
		Enabled:           true,
		Revision:          15,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-owner-rev-owner", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 34 {
		t.Fatalf("snapshot revision = %d, want caller WireGuard profile revision 34", snapshot.Revision)
	}
	if len(snapshot.WireGuardProfiles) != 1 || len(snapshot.WireGuardProfiles[0].Peers) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want generated caller peer", snapshot.WireGuardProfiles)
	}
	peer := snapshot.WireGuardProfiles[0].Peers[0]
	if peer.PublicKey != testWireGuardPublicKeyFromPrivate(t, callerPrivateKey) {
		t.Fatalf("peer public_key = %q, want caller public key", peer.PublicKey)
	}
	if len(peer.AllowedIPs) != 1 || peer.AllowedIPs[0] != "10.72.0.9/32" {
		t.Fatalf("peer allowed_ips = %+v, want caller WireGuard address", peer.AllowedIPs)
	}
}

func TestStoreLoadAgentSnapshotGeneratesWireGuardPeerForTransitRelayHop(t *testing.T) {
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

	for _, agentID := range []string{"edge-a", "relay-a", "relay-b", "relay-c"} {
		if err := store.SaveAgent(t.Context(), AgentRow{
			ID:               agentID,
			Name:             agentID,
			AgentToken:       "token-" + agentID,
			CapabilitiesJSON: `["wireguard","l4","relay"]`,
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	relayAProfileID := 61
	relayAPrivateKey := "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "relay-a", []WireGuardProfileRow{{
		ID:             relayAProfileID,
		AgentID:        "relay-a",
		Name:           "relay-a-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     relayAPrivateKey,
		ListenPort:     51821,
		PublicEndpoint: "relay-a.example.com:51821",
		AddressesJSON:  `["10.72.0.1/32"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1280,
		Enabled:        true,
		TagsJSON:       `["system:default-wireguard"]`,
		Revision:       12,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(relay-a) error = %v", err)
	}

	relayBProfileID := 71
	relayBPrivateKey := "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := store.SaveWireGuardProfiles(t.Context(), "relay-b", []WireGuardProfileRow{{
		ID:             relayBProfileID,
		AgentID:        "relay-b",
		Name:           "relay-b-wg",
		Mode:           "generic_wireguard",
		PrivateKey:     relayBPrivateKey,
		ListenPort:     51820,
		PublicEndpoint: "relay-b.example.com:51820",
		AddressesJSON:  `["10.71.0.254/32"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1280,
		Enabled:        true,
		TagsJSON:       `["system:default-wireguard"]`,
		Revision:       13,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(relay-b) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "relay-a", []RelayListenerRow{{
		ID:            601,
		AgentID:       "relay-a",
		Name:          "relay-a-tls",
		ListenHost:    "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenPort:    19001,
		PublicHost:    "relay-a.example.com",
		PublicPort:    19001,
		Enabled:       true,
		TransportMode: "tls_tcp",
		Revision:      14,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-a) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-b", []RelayListenerRow{{
		ID:                 711,
		AgentID:            "relay-b",
		Name:               "relay-b-wg",
		ListenHost:         "10.71.0.1",
		BindHostsJSON:      `["10.71.0.1"]`,
		ListenPort:         19002,
		PublicHost:         "relay-b.example.com",
		PublicPort:         51820,
		Enabled:            true,
		TransportMode:      "wireguard",
		WireGuardProfileID: &relayBProfileID,
		Revision:           15,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-b) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-c", []RelayListenerRow{{
		ID:            801,
		AgentID:       "relay-c",
		Name:          "relay-c-quic",
		ListenHost:    "0.0.0.0",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenPort:    19003,
		PublicHost:    "relay-c.example.com",
		PublicPort:    19003,
		Enabled:       true,
		TransportMode: "quic",
		Revision:      16,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-c) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "edge-a", []L4RuleRow{{
		ID:                901,
		AgentID:           "edge-a",
		Name:              "edge-through-transit-wg-relay",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        9443,
		BackendsJSON:      `[{"host":"127.0.0.1","port":9444}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[[601],[711],[801]]`,
		ListenMode:        "tcp",
		Enabled:           true,
		Revision:          17,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(edge-a) error = %v", err)
	}

	relayASnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-a", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(relay-a) error = %v", err)
	}
	if len(relayASnapshot.WireGuardProfiles) != 1 {
		t.Fatalf("relay-a WireGuardProfiles = %+v, want transit caller profile", relayASnapshot.WireGuardProfiles)
	}
	relayAProfile := relayASnapshot.WireGuardProfiles[0]
	if relayAProfile.ID != relayAProfileID || relayAProfile.AgentID != "relay-a" || relayAProfile.PrivateKey != relayAPrivateKey {
		t.Fatalf("relay-a profile = %+v, want local caller profile without remote private key", relayAProfile)
	}
	if relayAProfile.PrivateKey == relayBPrivateKey {
		t.Fatalf("leaked relay-b private key in relay-a profile %+v", relayAProfile)
	}
	if len(relayAProfile.Peers) != 1 {
		t.Fatalf("relay-a generated peers = %+v, want relay-b peer", relayAProfile.Peers)
	}
	relayAPeer := relayAProfile.Peers[0]
	if relayAPeer.PublicKey != testWireGuardPublicKeyFromPrivate(t, relayBPrivateKey) {
		t.Fatalf("relay-a peer public_key = %q, want relay-b public key", relayAPeer.PublicKey)
	}
	if relayAPeer.Endpoint != "relay-b.example.com:51820" {
		t.Fatalf("relay-a peer endpoint = %q, want relay-b endpoint", relayAPeer.Endpoint)
	}
	if !stringSliceContains(relayAPeer.AllowedIPs, "10.71.0.1/32") {
		t.Fatalf("relay-a peer allowed_ips = %+v, want relay-b listener tunnel address", relayAPeer.AllowedIPs)
	}

	relayBSnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-b", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(relay-b) error = %v", err)
	}
	if len(relayBSnapshot.WireGuardProfiles) != 1 {
		t.Fatalf("relay-b WireGuardProfiles = %+v, want owner profile", relayBSnapshot.WireGuardProfiles)
	}
	relayBProfile := relayBSnapshot.WireGuardProfiles[0]
	if relayBProfile.ID != relayBProfileID || relayBProfile.AgentID != "relay-b" || relayBProfile.PrivateKey != relayBPrivateKey {
		t.Fatalf("relay-b profile = %+v, want local owner profile", relayBProfile)
	}
	if len(relayBProfile.Peers) != 1 {
		t.Fatalf("relay-b generated peers = %+v, want relay-a caller peer", relayBProfile.Peers)
	}
	relayBPeer := relayBProfile.Peers[0]
	if relayBPeer.PublicKey != testWireGuardPublicKeyFromPrivate(t, relayAPrivateKey) {
		t.Fatalf("relay-b peer public_key = %q, want relay-a public key", relayBPeer.PublicKey)
	}
	if relayBPeer.Endpoint != "relay-a.example.com:51821" {
		t.Fatalf("relay-b peer endpoint = %q, want relay-a endpoint", relayBPeer.Endpoint)
	}
	if len(relayBPeer.AllowedIPs) != 1 || relayBPeer.AllowedIPs[0] != "10.72.0.1/32" {
		t.Fatalf("relay-b peer allowed_ips = %+v, want relay-a WireGuard address", relayBPeer.AllowedIPs)
	}
}

func TestStoreLoadAgentSnapshotIgnoresListenersReferencedOnlyByLegacyRelayChain(t *testing.T) {
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
		ID:              "legacy-chain-agent",
		Name:            "legacy-chain-agent",
		AgentToken:      "token-legacy-chain-agent",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "legacy-chain-peer",
		Name:            "legacy-chain-peer",
		AgentToken:      "token-legacy-chain-peer",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(peer) error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "legacy-chain-agent", []HTTPRuleRow{{
		ID:                91,
		AgentID:           "legacy-chain-agent",
		FrontendURL:       "https://legacy-chain-http.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		RelayChainJSON:    `[401]`,
		RelayLayersJSON:   `[]`,
		Revision:          5,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "legacy-chain-agent", []L4RuleRow{{
		ID:                92,
		AgentID:           "legacy-chain-agent",
		Name:              "legacy-chain-l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        19090,
		BackendsJSON:      `[{"host":"127.0.0.1","port":19091}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		Enabled:           true,
		RelayChainJSON:    `[401]`,
		RelayLayersJSON:   `[]`,
		Revision:          6,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "legacy-chain-peer", []RelayListenerRow{{
		ID:         401,
		AgentID:    "legacy-chain-peer",
		Name:       "legacy-chain-listener",
		ListenHost: "127.0.0.1",
		ListenPort: 7444,
		PublicHost: "legacy-chain-peer.example.com",
		PublicPort: 7444,
		Enabled:    true,
		Revision:   7,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "legacy-chain-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.RelayListeners) != 0 {
		t.Fatalf("RelayListeners = %+v", snapshot.RelayListeners)
	}
}

func TestStoreLoadAgentSnapshotIncludesHTTPSCertificateReferencedByRemoteRuleWithoutTargetAssignment(t *testing.T) {
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
		ID:              "edge-cert-ref",
		Name:            "edge-cert-ref",
		AgentToken:      "token-edge-cert-ref",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "edge-cert-ref", []HTTPRuleRow{{
		ID:                21,
		AgentID:           "edge-cert-ref",
		FrontendURL:       "https://edge.managed.example.com",
		BackendURL:        "https://origin.example.net",
		BackendsJSON:      `[{"url":"https://origin.example.net"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		PassProxyHeaders:  true,
		Revision:          7,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{
		ID:              30,
		Domain:          "*.managed.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "master_cf_dns",
		TargetAgentIDs:  `["local"]`,
		Status:          "active",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"managed.example.com"}`,
		Usage:           "https",
		CertificateType: "acme",
		TagsJSON:        `["wildcard"]`,
		Revision:        9,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	writeManagedCertificateMaterial(t, dataRoot, "*.managed.example.com", "wildcard-cert", "wildcard-key")

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "edge-cert-ref", AgentSnapshotInput{
		DesiredVersion:  "1.2.3",
		DesiredRevision: 7,
		CurrentRevision: 1,
		Platform:        "linux-amd64",
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if !containsCertificateID(snapshot.Certificates, 30) {
		t.Fatalf("Certificates missing referenced HTTPS cert id 30: %+v", snapshot.Certificates)
	}
	if !containsPolicyID(snapshot.CertificatePolicies, 30) {
		t.Fatalf("CertificatePolicies missing referenced HTTPS cert id 30: %+v", snapshot.CertificatePolicies)
	}
}

func TestStoreLoadAgentSnapshotIncludesRelayListenerServerCertificate(t *testing.T) {
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
		ID:              "edge-relay",
		Name:            "edge-relay",
		AgentToken:      "token-edge-relay",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(edge-relay) error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "relay-host",
		Name:            "relay-host",
		AgentToken:      "token-relay-host",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent(relay-host) error = %v", err)
	}

	if err := store.SaveHTTPRules(t.Context(), "edge-relay", []HTTPRuleRow{{
		ID:                1,
		AgentID:           "edge-relay",
		FrontendURL:       "https://relay-chain.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		RelayChainJSON:    `[77]`,
		RelayLayersJSON:   `[[77]]`,
		PassProxyHeaders:  true,
		Revision:          9,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(edge-relay) error = %v", err)
	}

	if err := store.SaveRelayListeners(t.Context(), "relay-host", []RelayListenerRow{{
		ID:                      77,
		AgentID:                 "relay-host",
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
		t.Fatalf("SaveRelayListeners(relay-host) error = %v", err)
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
		TargetAgentIDs:  `["relay-host"]`,
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

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "edge-relay", AgentSnapshotInput{
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		Platform:        "linux-amd64",
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if !containsCertificateID(snapshot.Certificates, 30) || !containsCertificateID(snapshot.Certificates, 31) {
		t.Fatalf("Certificates missing relay dependency material: %+v", snapshot.Certificates)
	}
	if !containsPolicyID(snapshot.CertificatePolicies, 30) || !containsPolicyID(snapshot.CertificatePolicies, 31) {
		t.Fatalf("CertificatePolicies missing relay dependency policies: %+v", snapshot.CertificatePolicies)
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

func TestStoreLoadAgentSnapshotIncludesProxyEntryL4RuleWithoutBackend(t *testing.T) {
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
		ID:              "proxy-entry-agent",
		Name:            "proxy-entry-agent",
		AgentToken:      "token-proxy-entry-agent",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveL4Rules(t.Context(), "proxy-entry-agent", []L4RuleRow{{
		ID:                 71,
		AgentID:            "proxy-entry-agent",
		Name:               "proxy-entry",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         1080,
		BackendsJSON:       `[]`,
		LoadBalancingJSON:  `{}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "proxy",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"client","password":"secret"}`,
		ProxyEgressMode:    "proxy",
		ProxyEgressURL:     "socks://egress.example.test:1080",
		Enabled:            true,
		Revision:           17,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "proxy-entry-agent", AgentSnapshotInput{
		DesiredRevision: 0,
		CurrentRevision: 0,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if snapshot.Revision != 17 {
		t.Fatalf("Revision = %d", snapshot.Revision)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 71 || rule.ListenMode != "proxy" || rule.ProxyEgressMode != "proxy" || rule.ProxyEgressURL == "" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if len(rule.Backends) != 0 || rule.UpstreamHost != "" || rule.UpstreamPort != 0 {
		t.Fatalf("proxy entry targets = backends=%+v upstream=%s:%d", rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	}
}

func TestStoreLoadAgentSnapshotIncludesUDPProxyEntryL4RuleWithoutBackend(t *testing.T) {
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
		ID:              "udp-proxy-entry-agent",
		Name:            "udp-proxy-entry-agent",
		AgentToken:      "token-udp-proxy-entry-agent",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveL4Rules(t.Context(), "udp-proxy-entry-agent", []L4RuleRow{{
		ID:                 75,
		AgentID:            "udp-proxy-entry-agent",
		Name:               "udp-proxy-entry",
		Protocol:           "udp",
		ListenHost:         "0.0.0.0",
		ListenPort:         1082,
		BackendsJSON:       `[]`,
		LoadBalancingJSON:  `{}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "proxy",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"client","password":"secret"}`,
		ProxyEgressMode:    "proxy",
		ProxyEgressURL:     "socks5://egress.example.test:1080",
		Enabled:            true,
		Revision:           23,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "udp-proxy-entry-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}

	if snapshot.Revision != 23 {
		t.Fatalf("Revision = %d", snapshot.Revision)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 75 || rule.Protocol != "udp" || rule.ListenMode != "proxy" || rule.ProxyEgressMode != "proxy" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if len(rule.Backends) != 0 || rule.UpstreamHost != "" || rule.UpstreamPort != 0 {
		t.Fatalf("udp proxy entry targets = backends=%+v upstream=%s:%d", rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardProxyEntryL4RuleWithoutBackend(t *testing.T) {
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
		ID:               "wg-proxy-entry-agent",
		Name:             "wg-proxy-entry-agent",
		AgentToken:       "token-wg-proxy-entry-agent",
		CapabilitiesJSON: `["wireguard"]`,
		DesiredRevision:  0,
		CurrentRevision:  0,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 7
	if err := store.SaveL4Rules(t.Context(), "wg-proxy-entry-agent", []L4RuleRow{{
		ID:                 72,
		AgentID:            "wg-proxy-entry-agent",
		Name:               "wg-proxy-entry",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         1081,
		BackendsJSON:       `[]`,
		LoadBalancingJSON:  `{}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		ProxyEntryAuthJSON: `{"enabled":true,"username":"client","password":"secret"}`,
		ProxyEgressMode:    "wireguard",
		Enabled:            true,
		Revision:           18,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-proxy-entry-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 72 || rule.ListenMode != "wireguard" || rule.ProxyEgressMode != "wireguard" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != profileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, profileID)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %+v, want empty proxy entry target list", rule.Backends)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardProfilesReferencedByL4Rules(t *testing.T) {
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
		ID:               "wg-l4-agent",
		Name:             "wg-l4-agent",
		AgentToken:       "token-wg-l4-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 91
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-l4-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-l4-agent",
		Name:          "l4-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "l4-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.92.0.2/32"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      21,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-l4-agent", []L4RuleRow{{
		ID:                 73,
		AgentID:            "wg-l4-agent",
		Name:               "wg-l4-inbound",
		Protocol:           "tcp",
		ListenHost:         "0.0.0.0",
		ListenPort:         9443,
		BackendsJSON:       `[{"host":"10.92.0.9","port":9443}]`,
		LoadBalancingJSON:  `{"strategy":"round_robin"}`,
		TuningJSON:         `{}`,
		RelayChainJSON:     `[]`,
		RelayLayersJSON:    `[]`,
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		Enabled:            true,
		Revision:           19,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-l4-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want L4 referenced profile", snapshot.WireGuardProfiles)
	}
	if snapshot.WireGuardProfiles[0].ID != profileID || snapshot.WireGuardProfiles[0].AgentID != "wg-l4-agent" {
		t.Fatalf("WireGuardProfiles[0] = %+v", snapshot.WireGuardProfiles[0])
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardProfilesWithManualPeers(t *testing.T) {
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
		ID:               "wg-manual-agent",
		Name:             "wg-manual-agent",
		AgentToken:       "token-wg-manual-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 92
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-manual-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-manual-agent",
		Name:          "manual-peer-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "manual-profile-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.92.0.1/24"]`,
		PeersJSON: `[{
			"name":"manual-phone",
			"public_key":"manual-peer-public-key",
			"preshared_key":"manual-peer-psk",
			"endpoint":"198.51.100.10:51820",
			"allowed_ips":["10.92.0.2/32"],
			"persistent_keepalive_seconds":25
		}]`,
		DNSJSON:  `[]`,
		Enabled:  true,
		Revision: 21,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-manual-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want manual-peer profile", snapshot.WireGuardProfiles)
	}
	profile := snapshot.WireGuardProfiles[0]
	if profile.ID != profileID || profile.AgentID != "wg-manual-agent" || len(profile.Peers) != 1 {
		t.Fatalf("WireGuardProfiles[0] = %+v, want profile with manual peer", profile)
	}
	if peer := profile.Peers[0]; peer.Name != "manual-phone" || peer.PublicKey != "manual-peer-public-key" {
		t.Fatalf("WireGuardProfiles[0].Peers[0] = %+v, want manual peer", peer)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardTransparentL4RuleWithoutBackends(t *testing.T) {
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
		ID:               "wg-transparent-agent",
		Name:             "wg-transparent-agent",
		AgentToken:       "token-wg-transparent-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 93
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-transparent-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-transparent-agent",
		Name:          "transparent-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "transparent-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.93.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      21,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-transparent-agent", []L4RuleRow{{
		ID:                   74,
		AgentID:              "wg-transparent-agent",
		Name:                 "wg-transparent",
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           18080,
		BackendsJSON:         `[]`,
		LoadBalancingJSON:    `{}`,
		TuningJSON:           `{}`,
		RelayChainJSON:       `[]`,
		RelayLayersJSON:      `[[1],[2]]`,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
		Revision:             22,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-transparent-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 74 || rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != profileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, profileID)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %+v, want empty transparent target list", rule.Backends)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardTransparentUDPL4RuleWithoutBackends(t *testing.T) {
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
		ID:               "wg-transparent-udp-agent",
		Name:             "wg-transparent-udp-agent",
		AgentToken:       "token-wg-transparent-udp-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 94
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-transparent-udp-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-transparent-udp-agent",
		Name:          "transparent-udp-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "transparent-udp-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.94.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      24,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-transparent-udp-agent", []L4RuleRow{{
		ID:                   76,
		AgentID:              "wg-transparent-udp-agent",
		Name:                 "wg-transparent-udp",
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           18081,
		BackendsJSON:         `[]`,
		LoadBalancingJSON:    `{}`,
		TuningJSON:           `{}`,
		RelayChainJSON:       `[]`,
		RelayLayersJSON:      `[]`,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		Enabled:              true,
		Revision:             25,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-transparent-udp-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 76 || rule.Protocol != "udp" || rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID != profileID {
		t.Fatalf("WireGuardProfileID = %v, want %d", rule.WireGuardProfileID, profileID)
	}
	if len(rule.Backends) != 0 {
		t.Fatalf("Backends = %+v, want empty transparent target list", rule.Backends)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardTransparentWildcardTCPL4RuleWithRelayEgress(t *testing.T) {
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
		ID:               "wg-transparent-wildcard-tcp-agent",
		Name:             "wg-transparent-wildcard-tcp-agent",
		AgentToken:       "token-wg-transparent-wildcard-tcp-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 95
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-transparent-wildcard-tcp-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-transparent-wildcard-tcp-agent",
		Name:          "transparent-wildcard-tcp-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "transparent-wildcard-tcp-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.95.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      26,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-transparent-wildcard-tcp-agent", []L4RuleRow{{
		ID:                   77,
		AgentID:              "wg-transparent-wildcard-tcp-agent",
		Name:                 "wg-transparent-wildcard-tcp",
		Protocol:             "tcp",
		ListenHost:           "0.0.0.0",
		ListenPort:           0,
		BackendsJSON:         `[]`,
		LoadBalancingJSON:    `{"strategy":"adaptive"}`,
		TuningJSON:           `{}`,
		RelayChainJSON:       `[]`,
		RelayLayersJSON:      `[[7],[6]]`,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		ProxyEgressMode:      "relay",
		Enabled:              true,
		Revision:             27,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-transparent-wildcard-tcp-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 77 || rule.Protocol != "tcp" || rule.ListenPort != 0 || rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" || rule.ProxyEgressMode != "relay" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardTransparentWildcardUDPL4RuleWithRelayEgress(t *testing.T) {
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
		ID:               "wg-transparent-wildcard-udp-agent",
		Name:             "wg-transparent-wildcard-udp-agent",
		AgentToken:       "token-wg-transparent-wildcard-udp-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 96
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-transparent-wildcard-udp-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-transparent-wildcard-udp-agent",
		Name:          "transparent-wildcard-udp-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "transparent-wildcard-udp-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.96.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      28,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-transparent-wildcard-udp-agent", []L4RuleRow{{
		ID:                   78,
		AgentID:              "wg-transparent-wildcard-udp-agent",
		Name:                 "wg-transparent-wildcard-udp",
		Protocol:             "udp",
		ListenHost:           "0.0.0.0",
		ListenPort:           0,
		BackendsJSON:         `[]`,
		LoadBalancingJSON:    `{"strategy":"adaptive"}`,
		TuningJSON:           `{}`,
		RelayChainJSON:       `[]`,
		RelayLayersJSON:      `[[7],[6]]`,
		ListenMode:           "wireguard",
		WireGuardProfileID:   &profileID,
		WireGuardInboundMode: "transparent",
		ProxyEgressMode:      "relay",
		Enabled:              true,
		Revision:             29,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-transparent-wildcard-udp-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 1 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
	}
	rule := snapshot.L4Rules[0]
	if rule.ID != 78 || rule.Protocol != "udp" || rule.ListenPort != 0 || rule.ListenMode != "wireguard" || rule.WireGuardInboundMode != "transparent" || rule.ProxyEgressMode != "relay" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardProfilesReferencedByHTTPRules(t *testing.T) {
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
		ID:               "wg-http-agent",
		Name:             "wg-http-agent",
		AgentToken:       "token-wg-http-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	profileID := 81
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-http-agent", []WireGuardProfileRow{{
		ID:            profileID,
		AgentID:       "wg-http-agent",
		Name:          "http-local-wg",
		Mode:          "generic_wireguard",
		PrivateKey:    "http-local-private-key",
		ListenPort:    51820,
		AddressesJSON: `["10.81.0.1/24"]`,
		PeersJSON:     `[]`,
		DNSJSON:       `[]`,
		Enabled:       true,
		Revision:      21,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "wg-http-agent", []HTTPRuleRow{{
		ID:                       83,
		AgentID:                  "wg-http-agent",
		FrontendURL:              "http://app.internal",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		TagsJSON:                 `[]`,
		ProxyRedirect:            true,
		RelayChainJSON:           `[]`,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "10.81.0.1",
		WireGuardEntryListenPort: 8080,
		Revision:                 19,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-http-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || !snapshot.Rules[0].WireGuardEntryEnabled {
		t.Fatalf("Rules = %+v, want HTTP WireGuard entry", snapshot.Rules)
	}
	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("WireGuardProfiles = %+v, want HTTP referenced profile", snapshot.WireGuardProfiles)
	}
	if snapshot.WireGuardProfiles[0].ID != profileID || snapshot.WireGuardProfiles[0].AgentID != "wg-http-agent" {
		t.Fatalf("WireGuardProfiles[0] = %+v", snapshot.WireGuardProfiles[0])
	}
}

func TestStoreLoadAgentSnapshotExcludesIdleWireGuardProfiles(t *testing.T) {
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
		ID:               "wg-filter-agent",
		Name:             "wg-filter-agent",
		AgentToken:       "token-wg-filter-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	referencedProfileID := 111
	manualProfileID := 112
	idleProfileID := 113
	importedProfileID := 114
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-filter-agent", []WireGuardProfileRow{
		{
			ID:            referencedProfileID,
			AgentID:       "wg-filter-agent",
			Name:          "referenced-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "referenced-private-key",
			ListenPort:    51820,
			AddressesJSON: `["10.111.0.1/24"]`,
			PeersJSON:     `[{"name":"referenced-peer","public_key":"referenced-public-key","preshared_key":"referenced-psk","allowed_ips":["10.111.0.2/32"]}]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      21,
		},
		{
			ID:            manualProfileID,
			AgentID:       "wg-filter-agent",
			Name:          "manual-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "manual-private-key",
			ListenPort:    51821,
			AddressesJSON: `["10.112.0.1/24"]`,
			PeersJSON:     `[{"name":"manual-peer","public_key":"manual-public-key","preshared_key":"manual-psk","allowed_ips":["10.112.0.2/32"]}]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      22,
		},
		{
			ID:            idleProfileID,
			AgentID:       "wg-filter-agent",
			Name:          "idle-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "idle-private-key",
			ListenPort:    51822,
			AddressesJSON: `["10.113.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      23,
		},
		{
			ID:            importedProfileID,
			AgentID:       "wg-filter-agent",
			Name:          "imported-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "imported-private-key",
			ListenPort:    51823,
			AddressesJSON: `["10.114.0.1/24"]`,
			PeersJSON:     `[{"public_key":"imported-public-key","preshared_key":"imported-psk","endpoint":"peer.example.com:51820","allowed_ips":["10.114.0.2/32"]}]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      24,
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "wg-filter-agent", []HTTPRuleRow{{
		ID:                       113,
		AgentID:                  "wg-filter-agent",
		FrontendURL:              "http://wg-filter.internal",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		TagsJSON:                 `[]`,
		ProxyRedirect:            true,
		RelayChainJSON:           `[]`,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &referencedProfileID,
		WireGuardEntryListenHost: "10.111.0.1",
		WireGuardEntryListenPort: 8080,
		Revision:                 19,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-filter-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 3 {
		t.Fatalf("WireGuardProfiles = %+v, want referenced, manual-peer, and imported profiles", snapshot.WireGuardProfiles)
	}
	seen := map[int]WireGuardProfile{}
	for _, profile := range snapshot.WireGuardProfiles {
		seen[profile.ID] = profile
	}
	if profile := seen[referencedProfileID]; profile.PrivateKey != "referenced-private-key" {
		t.Fatalf("referenced profile = %+v", profile)
	}
	if profile := seen[manualProfileID]; profile.PrivateKey != "manual-private-key" || len(profile.Peers) != 1 || profile.Peers[0].PresharedKey != "manual-psk" {
		t.Fatalf("manual profile = %+v", profile)
	}
	if profile := seen[importedProfileID]; profile.PrivateKey != "imported-private-key" || len(profile.Peers) != 1 || profile.Peers[0].PublicKey != "imported-public-key" {
		t.Fatalf("imported profile = %+v", profile)
	}
	for _, profile := range snapshot.WireGuardProfiles {
		if profile.PrivateKey == "idle-private-key" || profile.ID == idleProfileID {
			t.Fatalf("snapshot leaked idle WireGuard profile: %+v", profile)
		}
		for _, peer := range profile.Peers {
			if peer.PresharedKey == "idle-psk" {
				t.Fatalf("snapshot leaked idle WireGuard peer secret: %+v", profile)
			}
		}
	}
}

func TestStoreLoadAgentSnapshotIncludesWireGuardProfilesReferencedByRelayAndL4ProxyEntry(t *testing.T) {
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
		ID:               "wg-graph-agent",
		Name:             "wg-graph-agent",
		AgentToken:       "token-wg-graph-agent",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	relayProfileID := 121
	l4ProfileID := 122
	l4EgressProfileID := 123
	staleProfileID := 126
	if err := store.SaveWireGuardProfiles(t.Context(), "wg-graph-agent", []WireGuardProfileRow{
		{
			ID:            relayProfileID,
			AgentID:       "wg-graph-agent",
			Name:          "relay-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "relay-private-key",
			ListenPort:    51820,
			AddressesJSON: `["10.121.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      31,
		},
		{
			ID:            l4ProfileID,
			AgentID:       "wg-graph-agent",
			Name:          "l4-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "l4-private-key",
			ListenPort:    51821,
			AddressesJSON: `["10.122.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      32,
		},
		{
			ID:            l4EgressProfileID,
			AgentID:       "wg-graph-agent",
			Name:          "l4-egress-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "l4-egress-private-key",
			ListenPort:    51822,
			AddressesJSON: `["10.123.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      33,
		},
		{
			ID:            staleProfileID,
			AgentID:       "wg-graph-agent",
			Name:          "stale-wg",
			Mode:          "generic_wireguard",
			PrivateKey:    "stale-private-key",
			ListenPort:    51823,
			AddressesJSON: `["10.126.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			Enabled:       true,
			Revision:      36,
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "wg-graph-agent", []RelayListenerRow{{
		ID:                 124,
		AgentID:            "wg-graph-agent",
		Name:               "relay-entry",
		ListenHost:         "0.0.0.0",
		BindHostsJSON:      `["0.0.0.0"]`,
		ListenPort:         9443,
		PublicHost:         "relay-entry.example.com",
		PublicPort:         9443,
		TransportMode:      "wireguard",
		WireGuardProfileID: &relayProfileID,
		Enabled:            true,
		TagsJSON:           `[]`,
		Revision:           24,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "wg-graph-agent", []L4RuleRow{
		{
			ID:                 125,
			AgentID:            "wg-graph-agent",
			Name:               "wg-l4-listen",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         1081,
			BackendsJSON:       `[{"host":"10.122.0.9","port":9443}]`,
			LoadBalancingJSON:  `{}`,
			TuningJSON:         `{}`,
			RelayChainJSON:     `[]`,
			RelayLayersJSON:    `[]`,
			ListenMode:         "wireguard",
			WireGuardProfileID: &l4ProfileID,
			Enabled:            true,
			Revision:           25,
		},
		{
			ID:                 127,
			AgentID:            "wg-graph-agent",
			Name:               "wg-l4-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         1082,
			BackendsJSON:       `[{"host":"10.123.0.9","port":9443}]`,
			LoadBalancingJSON:  `{}`,
			TuningJSON:         `{}`,
			RelayChainJSON:     `[]`,
			RelayLayersJSON:    `[]`,
			ListenMode:         "tcp",
			WireGuardProfileID: &l4EgressProfileID,
			ProxyEgressMode:    "wireguard",
			Enabled:            true,
			Revision:           27,
		},
	}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "wg-graph-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 3 {
		t.Fatalf("WireGuardProfiles = %+v, want relay and L4 referenced profiles", snapshot.WireGuardProfiles)
	}
	got := map[int]WireGuardProfile{}
	for _, profile := range snapshot.WireGuardProfiles {
		got[profile.ID] = profile
	}
	if got[relayProfileID].PrivateKey != "relay-private-key" {
		t.Fatalf("relay profile = %+v", got[relayProfileID])
	}
	if got[l4ProfileID].PrivateKey != "l4-private-key" {
		t.Fatalf("l4 profile = %+v", got[l4ProfileID])
	}
	if got[l4EgressProfileID].PrivateKey != "l4-egress-private-key" {
		t.Fatalf("l4 egress profile = %+v", got[l4EgressProfileID])
	}
	if _, ok := got[staleProfileID]; ok {
		t.Fatalf("snapshot leaked stale WireGuard profile: %+v", got[staleProfileID])
	}
}

func TestStoreLoadAgentSnapshotExcludesUpstreamOnlyL4RowsWithoutCanonicalBackends(t *testing.T) {
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
		ID:              "upstream-only-agent",
		Name:            "upstream-only-agent",
		AgentToken:      "token-upstream-only-agent",
		DesiredRevision: 0,
		CurrentRevision: 0,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveL4Rules(t.Context(), "upstream-only-agent", []L4RuleRow{{
		ID:                93,
		AgentID:           "upstream-only-agent",
		Name:              "upstream-only",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        19999,
		UpstreamHost:      "127.0.0.1",
		UpstreamPort:      20001,
		BackendsJSON:      `[]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[]`,
		Enabled:           true,
		Revision:          3,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "upstream-only-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.L4Rules) != 0 {
		t.Fatalf("L4Rules = %+v", snapshot.L4Rules)
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
		RelayLayersJSON:   `[[77]]`,
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
		RelayLayersJSON:   `[[77]]`,
		RelayObfs:         true,
		Enabled:           true,
		Revision:          32,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-obfs-agent", []RelayListenerRow{{
		ID:                      77,
		AgentID:                 "relay-obfs-agent",
		Name:                    "relay-obfs-listener",
		BindHostsJSON:           `["0.0.0.0"]`,
		ListenHost:              "0.0.0.0",
		ListenPort:              17443,
		PublicHost:              "relay-obfs-listener.example.com",
		PublicPort:              17443,
		Enabled:                 true,
		TLSMode:                 "pin_or_ca",
		TransportMode:           "quic",
		AllowTransportFallback:  true,
		ObfsMode:                "off",
		PinSetJSON:              `[]`,
		TrustedCACertificateIDs: `[]`,
		AllowSelfSigned:         true,
		Revision:                33,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
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
	if len(snapshot.RelayListeners) != 1 {
		t.Fatalf("expected one snapshot relay listener: %+v", snapshot.RelayListeners)
	}
	if snapshot.RelayListeners[0].TransportMode != "quic" {
		t.Fatalf("snapshot relay listener transport_mode = %q", snapshot.RelayListeners[0].TransportMode)
	}
	if !snapshot.RelayListeners[0].AllowTransportFallback {
		t.Fatalf("snapshot relay listener allow_transport_fallback = %v", snapshot.RelayListeners[0].AllowTransportFallback)
	}
	if snapshot.RelayListeners[0].ObfsMode != "off" {
		t.Fatalf("snapshot relay listener obfs_mode = %q", snapshot.RelayListeners[0].ObfsMode)
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

func TestStoreLoadAgentSnapshotUsesWireGuardProfileRevision(t *testing.T) {
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
		ID:               "remote-wg",
		Name:             "remote-wg",
		AgentToken:       "token-remote-wg",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(t.Context(), "remote-wg", []WireGuardProfileRow{{
		ID:             7,
		AgentID:        "remote-wg",
		Name:           "wg remote",
		Mode:           "generic_wireguard",
		PrivateKey:     "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		ListenPort:     51820,
		PublicEndpoint: "wg.example.com:51820",
		AddressesJSON:  `["10.10.0.1/24"]`,
		PeersJSON:      `[]`,
		DNSJSON:        `[]`,
		MTU:            1420,
		Enabled:        true,
		TagsJSON:       `[]`,
		Revision:       9,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	profileID := 7
	if err := store.SaveHTTPRules(t.Context(), "remote-wg", []HTTPRuleRow{{
		ID:                       89,
		AgentID:                  "remote-wg",
		FrontendURL:              "http://remote-wg-revision.internal",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		TagsJSON:                 `[]`,
		ProxyRedirect:            true,
		RelayChainJSON:           `[]`,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "10.10.0.1",
		WireGuardEntryListenPort: 8080,
		Revision:                 1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-wg", AgentSnapshotInput{
		DesiredRevision: 1,
		CurrentRevision: 2,
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 9 {
		t.Fatalf("snapshot revision = %d, want WireGuard profile revision 9", snapshot.Revision)
	}
	if len(snapshot.WireGuardProfiles) != 1 || snapshot.WireGuardProfiles[0].PublicEndpoint != "wg.example.com:51820" {
		t.Fatalf("snapshot WireGuardProfiles = %+v, want public endpoint", snapshot.WireGuardProfiles)
	}
}

func TestStoreBootstrapBumpsAgentRevisionsForWireGuardSnapshotCleanupOnce(t *testing.T) {
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:               "remote-cleanup",
		Name:             "remote-cleanup",
		AgentToken:       "token-remote-cleanup",
		DesiredVersion:   "3.0.0",
		DesiredRevision:  7,
		CurrentRevision:  7,
		LastApplyStatus:  "success",
		CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.db.WithContext(t.Context()).
		Where("key = ?", wireGuardAgentLocalSnapshotMarkerKey).
		Delete(&MetaRow{}).Error; err != nil {
		t.Fatalf("delete migration marker error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	upgraded, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(upgraded) error = %v", err)
	}
	t.Cleanup(func() {
		_ = upgraded.Close()
	})

	agents, err := upgraded.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	var remote AgentRow
	for _, row := range agents {
		if row.ID == "remote-cleanup" {
			remote = row
			break
		}
	}
	if remote.ID == "" {
		t.Fatal("remote-cleanup agent not found")
	}
	if remote.DesiredRevision <= remote.CurrentRevision {
		t.Fatalf("agent revisions = desired %d current %d, want desired bumped once", remote.DesiredRevision, remote.CurrentRevision)
	}
	if remote.DesiredRevision != 8 {
		t.Fatalf("agent desired revision = %d, want 8", remote.DesiredRevision)
	}

	snapshot, err := upgraded.LoadAgentSnapshot(t.Context(), "remote-cleanup", AgentSnapshotInput{
		DesiredVersion:  remote.DesiredVersion,
		DesiredRevision: remote.DesiredRevision,
		CurrentRevision: remote.CurrentRevision,
		Platform:        "linux-amd64",
	})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != int64(remote.DesiredRevision) {
		t.Fatalf("snapshot revision = %d, want bumped desired revision %d", snapshot.Revision, remote.DesiredRevision)
	}
	if snapshot.WireGuardProfiles == nil || len(snapshot.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want explicit empty slice", snapshot.WireGuardProfiles)
	}

	revisionAfterFirstBootstrap := remote.DesiredRevision
	if err := upgraded.Close(); err != nil {
		t.Fatalf("Close(upgraded) error = %v", err)
	}

	reopened, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopened) error = %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	agents, err = reopened.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents(reopened) error = %v", err)
	}
	for _, row := range agents {
		if row.ID == "remote-cleanup" {
			remote = row
			break
		}
	}
	if remote.DesiredRevision != revisionAfterFirstBootstrap {
		t.Fatalf("desired revision after second bootstrap = %d, want unchanged %d", remote.DesiredRevision, revisionAfterFirstBootstrap)
	}
}

func TestStoreBootstrapBumpsAgentRevisionAboveExistingDesiredForWireGuardSnapshotCleanup(t *testing.T) {
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:               "remote-cleanup",
		Name:             "remote-cleanup",
		AgentToken:       "token-remote-cleanup",
		DesiredVersion:   "3.0.0",
		DesiredRevision:  12,
		CurrentRevision:  7,
		LastApplyStatus:  "success",
		CapabilitiesJSON: `[]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.db.WithContext(t.Context()).
		Where("key = ?", wireGuardAgentLocalSnapshotMarkerKey).
		Delete(&MetaRow{}).Error; err != nil {
		t.Fatalf("delete migration marker error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	upgraded, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(upgraded) error = %v", err)
	}
	t.Cleanup(func() {
		_ = upgraded.Close()
	})

	agents, err := upgraded.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	var remote AgentRow
	for _, row := range agents {
		if row.ID == "remote-cleanup" {
			remote = row
			break
		}
	}
	if remote.ID == "" {
		t.Fatal("remote-cleanup agent not found")
	}
	if remote.DesiredRevision != 13 {
		t.Fatalf("agent desired revision = %d, want bumped above existing desired revision 13", remote.DesiredRevision)
	}
}

func TestMarkWireGuardSnapshotsAgentLocalBumpsOnceUnderConcurrentMarkerRace(t *testing.T) {
	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

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
	if err := db.Save(&AgentRow{
		ID:               "remote-race",
		Name:             "remote-race",
		AgentToken:       "token-remote-race",
		DesiredVersion:   "3.0.0",
		DesiredRevision:  5,
		CurrentRevision:  5,
		LastApplyStatus:  "success",
		CapabilitiesJSON: `[]`,
	}).Error; err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := db.WithContext(t.Context()).
		Where("key = ?", wireGuardAgentLocalSnapshotMarkerKey).
		Delete(&MetaRow{}).Error; err != nil {
		t.Fatalf("delete migration marker error = %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	sqlDB.SetMaxOpenConns(2)

	var countArrivals int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	const countBarrierName = "test:wireguard_marker_count_barrier"
	db.Callback().Query().After("gorm:query").Register(countBarrierName, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*int64); !ok {
			return
		}
		if tx.Statement.Table != "meta" {
			return
		}
		if atomic.AddInt32(&countArrivals, 1) == 2 {
			releaseOnce.Do(func() {
				close(release)
			})
		}
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			tx.AddError(errors.New("timed out waiting for concurrent marker count"))
		}
	})
	var agentUpdateCount int32
	const updateCountName = "test:wireguard_marker_update_count"
	db.Callback().Update().After("gorm:update").Register(updateCountName, func(tx *gorm.DB) {
		if tx.Statement.Table != "agents" {
			return
		}
		if !strings.Contains(strings.ToLower(tx.Statement.SQL.String()), "desired_revision") {
			return
		}
		atomic.AddInt32(&agentUpdateCount, 1)
	})

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errCh <- markWireGuardSnapshotsAgentLocal(t.Context(), db)
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("markWireGuardSnapshotsAgentLocal() error = %v", err)
		}
	}

	db.Callback().Query().Remove(countBarrierName)
	db.Callback().Update().Remove(updateCountName)

	if got := atomic.LoadInt32(&agentUpdateCount); got != 1 {
		t.Fatalf("agent desired_revision update count = %d, want 1", got)
	}

	var remote AgentRow
	if err := db.First(&remote, "id = ?", "remote-race").Error; err != nil {
		t.Fatalf("load remote agent error = %v", err)
	}
	if remote.DesiredRevision != 6 {
		t.Fatalf("remote desired revision = %d, want 6", remote.DesiredRevision)
	}
	var markerCount int64
	if err := db.Model(&MetaRow{}).Where("key = ?", wireGuardAgentLocalSnapshotMarkerKey).Count(&markerCount).Error; err != nil {
		t.Fatalf("count migration marker error = %v", err)
	}
	if markerCount != 1 {
		t.Fatalf("migration marker count = %d, want 1", markerCount)
	}
}

func TestStoreLoadAgentSnapshotOmitsWireGuardProfilesWhenAgentLacksCapability(t *testing.T) {
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
		ID:               "remote-no-wg",
		Name:             "remote-no-wg",
		AgentToken:       "token-remote-no-wg",
		CapabilitiesJSON: `["http_rules","l4"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(t.Context(), "remote-no-wg", []WireGuardProfileRow{
		{
			ID:            7,
			AgentID:       "remote-no-wg",
			Name:          "wg enabled",
			Mode:          "generic_wireguard",
			PrivateKey:    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			ListenPort:    51820,
			AddressesJSON: `["10.10.0.1/24"]`,
			PeersJSON:     `[{"name":"peer-a","public_key":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","preshared_key":"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=","endpoint":"peer.example.com:51820","allowed_ips":["10.10.0.2/32"],"persistent_keepalive_seconds":25}]`,
			DNSJSON:       `["1.1.1.1"]`,
			MTU:           1420,
			Enabled:       true,
			TagsJSON:      `["edge"]`,
			Revision:      9,
		},
		{
			ID:            8,
			AgentID:       "remote-no-wg",
			Name:          "wg disabled",
			Mode:          "generic_wireguard",
			PrivateKey:    "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=",
			ListenPort:    51821,
			AddressesJSON: `["10.11.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			MTU:           1420,
			Enabled:       false,
			TagsJSON:      `[]`,
			Revision:      10,
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "remote-no-wg", []HTTPRuleRow{
		{
			ID:                21,
			AgentID:           "remote-no-wg",
			FrontendURL:       "http://plain.example.test:8080",
			BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
			Enabled:           true,
			LoadBalancingJSON: `{"strategy":"round_robin"}`,
			Revision:          3,
		},
		{
			ID:                       22,
			AgentID:                  "remote-no-wg",
			FrontendURL:              "http://wg.example.test:8081",
			BackendsJSON:             `[{"url":"http://127.0.0.1:8097"}]`,
			Enabled:                  true,
			WireGuardEntryEnabled:    true,
			WireGuardProfileID:       intPointer(7),
			WireGuardEntryListenHost: "10.10.0.1",
			WireGuardEntryListenPort: 8081,
			LoadBalancingJSON:        `{"strategy":"round_robin"}`,
			Revision:                 4,
		},
		{
			ID:                23,
			AgentID:           "remote-no-wg",
			FrontendURL:       "http://wg-relay.example.test:8082",
			BackendsJSON:      `[{"url":"http://127.0.0.1:8098"}]`,
			Enabled:           true,
			RelayLayersJSON:   `[[42]]`,
			LoadBalancingJSON: `{"strategy":"round_robin"}`,
			Revision:          10,
		},
	}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "remote-no-wg", []L4RuleRow{
		{
			ID:           31,
			AgentID:      "remote-no-wg",
			Name:         "plain",
			Protocol:     "tcp",
			ListenHost:   "0.0.0.0",
			ListenPort:   9000,
			ListenMode:   "tcp",
			BackendsJSON: `[{"host":"127.0.0.1","port":9001}]`,
			Enabled:      true,
			Revision:     5,
		},
		{
			ID:                 32,
			AgentID:            "remote-no-wg",
			Name:               "wg-listen",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9002,
			ListenMode:         "wireguard",
			WireGuardProfileID: intPointer(7),
			BackendsJSON:       `[{"host":"127.0.0.1","port":9003}]`,
			Enabled:            true,
			Revision:           6,
		},
		{
			ID:                 33,
			AgentID:            "remote-no-wg",
			Name:               "wg-egress",
			Protocol:           "tcp",
			ListenHost:         "0.0.0.0",
			ListenPort:         9004,
			ListenMode:         "tcp",
			ProxyEgressMode:    "wireguard",
			WireGuardProfileID: intPointer(7),
			BackendsJSON:       `[{"host":"127.0.0.1","port":9005}]`,
			Enabled:            true,
			Revision:           7,
		},
		{
			ID:              34,
			AgentID:         "remote-no-wg",
			Name:            "wg-relay-egress",
			Protocol:        "tcp",
			ListenHost:      "0.0.0.0",
			ListenPort:      9006,
			ListenMode:      "tcp",
			RelayLayersJSON: `[[42]]`,
			BackendsJSON:    `[{"host":"127.0.0.1","port":9007}]`,
			Enabled:         true,
			Revision:        11,
		},
	}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "remote-no-wg", []RelayListenerRow{
		{
			ID:            41,
			AgentID:       "remote-no-wg",
			Name:          "plain-relay",
			ListenHost:    "0.0.0.0",
			ListenPort:    9443,
			PublicHost:    "relay.example.test",
			PublicPort:    9443,
			Enabled:       true,
			TransportMode: "tls_tcp",
			TLSMode:       "pin_only",
			Revision:      8,
		},
		{
			ID:                 42,
			AgentID:            "remote-no-wg",
			Name:               "wg-relay",
			ListenHost:         "10.10.0.1",
			ListenPort:         9444,
			PublicHost:         "10.10.0.1",
			PublicPort:         9444,
			Enabled:            true,
			TransportMode:      "wireguard",
			WireGuardProfileID: intPointer(7),
			TLSMode:            "pin_only",
			Revision:           9,
		},
	}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-no-wg", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 0 {
		t.Fatalf("WireGuardProfiles = %+v, want none for agent without wireguard capability", snapshot.WireGuardProfiles)
	}
	for _, profile := range snapshot.WireGuardProfiles {
		if profile.PrivateKey == "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" || profile.PrivateKey == "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=" {
			t.Fatalf("unsupported agent snapshot leaked wireguard private material: %+v", profile)
		}
	}
	if len(snapshot.Rules) != 1 || snapshot.Rules[0].ID != 21 {
		t.Fatalf("Rules = %+v, want only non-wireguard rule", snapshot.Rules)
	}
	if len(snapshot.L4Rules) != 1 || snapshot.L4Rules[0].ID != 31 {
		t.Fatalf("L4Rules = %+v, want only non-wireguard rule", snapshot.L4Rules)
	}
	if len(snapshot.RelayListeners) != 1 || snapshot.RelayListeners[0].ID != 41 {
		t.Fatalf("RelayListeners = %+v, want only non-wireguard listener", snapshot.RelayListeners)
	}
}

func TestStoreLoadAgentSnapshotIncludesEnabledWireGuardProfilesWithRawSecrets(t *testing.T) {
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
		ID:               "remote-wg",
		Name:             "remote-wg",
		AgentToken:       "token-remote-wg",
		CapabilitiesJSON: `["wireguard"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(t.Context(), "remote-wg", []WireGuardProfileRow{
		{
			ID:            7,
			AgentID:       "remote-wg",
			Name:          "wg enabled",
			Mode:          "generic_wireguard",
			PrivateKey:    "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			ListenPort:    51820,
			AddressesJSON: `["10.10.0.1/24"]`,
			PeersJSON:     `[{"name":"peer-a","public_key":"BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=","preshared_key":"CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=","endpoint":"peer.example.com:51820","allowed_ips":["10.10.0.2/32"],"persistent_keepalive_seconds":25}]`,
			DNSJSON:       `["1.1.1.1"]`,
			MTU:           1420,
			Enabled:       true,
			TagsJSON:      `["edge"]`,
			Revision:      9,
		},
		{
			ID:            8,
			AgentID:       "remote-wg",
			Name:          "wg disabled",
			Mode:          "generic_wireguard",
			PrivateKey:    "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=",
			ListenPort:    51821,
			AddressesJSON: `["10.11.0.1/24"]`,
			PeersJSON:     `[]`,
			DNSJSON:       `[]`,
			MTU:           1420,
			Enabled:       false,
			TagsJSON:      `[]`,
			Revision:      10,
		},
	}); err != nil {
		t.Fatalf("SaveWireGuardProfiles() error = %v", err)
	}
	profileID := 7
	if err := store.SaveHTTPRules(t.Context(), "remote-wg", []HTTPRuleRow{{
		ID:                       87,
		AgentID:                  "remote-wg",
		FrontendURL:              "http://remote-wg.internal",
		BackendsJSON:             `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON:        `{"strategy":"adaptive"}`,
		Enabled:                  true,
		TagsJSON:                 `[]`,
		ProxyRedirect:            true,
		RelayChainJSON:           `[]`,
		RelayLayersJSON:          `[]`,
		CustomHeadersJSON:        `[]`,
		WireGuardEntryEnabled:    true,
		WireGuardProfileID:       &profileID,
		WireGuardEntryListenHost: "10.10.0.1",
		WireGuardEntryListenPort: 8080,
		Revision:                 8,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-wg", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) != 1 {
		t.Fatalf("WireGuardProfiles length = %d, want 1: %+v", len(snapshot.WireGuardProfiles), snapshot.WireGuardProfiles)
	}
	profile := snapshot.WireGuardProfiles[0]
	if profile.PrivateKey != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" {
		t.Fatalf("private_key = %q, want raw private key", profile.PrivateKey)
	}
	if len(profile.Peers) != 1 || profile.Peers[0].PresharedKey != "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC=" {
		t.Fatalf("peer preshared_key not raw: %+v", profile.Peers)
	}
	if profile.ID != 7 || profile.AgentID != "remote-wg" || profile.Mode != "generic_wireguard" || !profile.Enabled || profile.Revision != 9 {
		t.Fatalf("unexpected WireGuard profile metadata: %+v", profile)
	}
}

func TestStoreLoadAgentSnapshotUsesStoredAgentDesiredRevisionForProxyOnlyConfig(t *testing.T) {
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
		ID:               "remote-proxy-only",
		Name:             "remote proxy only",
		AgentToken:       "token-remote-proxy-only",
		OutboundProxyURL: "socks://127.0.0.1:1080",
		DesiredRevision:  8,
		CurrentRevision:  7,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "remote-proxy-only", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 8 {
		t.Fatalf("snapshot revision = %d, want stored agent desired revision 8", snapshot.Revision)
	}
}

func testWireGuardPublicKeyFromPrivate(t *testing.T, privateKey string) string {
	t.Helper()
	privateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(privateBytes) != 32 {
		t.Fatalf("invalid test WireGuard private key %q", privateKey)
	}
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	publicBytes, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		t.Fatalf("derive test WireGuard public key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(publicBytes)
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestStoreLoadAgentSnapshotTreatsLocalAgentAsSpecialRuntimeState(t *testing.T) {
	dataRoot := t.TempDir()

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.SaveLocalRuntimeState(t.Context(), "local", RuntimeState{
		CurrentRevision: 5,
		Status:          "active",
		Metadata:        map[string]string{},
	}); err != nil {
		t.Fatalf("SaveLocalRuntimeState() error = %v", err)
	}

	var agentRevisionLookups int
	callbackName := "test:count_local_agent_revision_lookup"
	if err := store.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "agents" && tx.Statement.SQL.String() == "SELECT * FROM `agents` WHERE id = ? ORDER BY `agents`.`id` LIMIT 1" {
			agentRevisionLookups++
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		_ = store.db.Callback().Query().Remove(callbackName)
	})

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "local", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.Revision != 5 {
		t.Fatalf("snapshot revision = %d, want local runtime desired revision 5", snapshot.Revision)
	}
	if agentRevisionLookups != 0 {
		t.Fatalf("local snapshot queried agents table %d times", agentRevisionLookups)
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

	runtimeState, err := store.LoadLocalRuntimeState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalRuntimeState() error = %v", err)
	}
	if runtimeState.CurrentRevision != 9 || runtimeState.Status != "active" {
		t.Fatalf("LoadLocalRuntimeState() = %+v", runtimeState)
	}
}

func TestStorePersistsLocalRuntimeStateMetadata(t *testing.T) {
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
		Metadata: map[string]string{
			"stats": `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}`,
		},
	})
	if err != nil {
		t.Fatalf("SaveLocalRuntimeState() error = %v", err)
	}

	runtimeState, err := store.LoadLocalRuntimeState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalRuntimeState() error = %v", err)
	}
	if runtimeState.Metadata["stats"] != `{"traffic":{"total":{"rx_bytes":123,"tx_bytes":456}}}` {
		t.Fatalf("LoadLocalRuntimeState() metadata = %+v", runtimeState.Metadata)
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

type sqliteTableColumn struct {
	Name         string
	NotNull      int
	DefaultValue sql.NullString
}

func loadSQLiteTableInfo(t *testing.T, db *gorm.DB, tableName string) map[string]sqliteTableColumn {
	t.Helper()

	rows, err := db.Raw("PRAGMA table_info(" + tableName + ")").Rows()
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s) error = %v", tableName, err)
	}
	defer rows.Close()

	columns := make(map[string]sqliteTableColumn)
	for rows.Next() {
		var cid int
		var columnType string
		var column sqliteTableColumn
		var primaryKey int
		if err := rows.Scan(&cid, &column.Name, &columnType, &column.NotNull, &column.DefaultValue, &primaryKey); err != nil {
			t.Fatalf("scan PRAGMA table_info(%s) error = %v", tableName, err)
		}
		columns[column.Name] = column
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PRAGMA table_info(%s) error = %v", tableName, err)
	}
	return columns
}

func assertSQLiteColumnContract(t *testing.T, columns map[string]sqliteTableColumn, columnName string, wantNotNull int, wantDefault string) {
	t.Helper()

	column, ok := columns[columnName]
	if !ok {
		t.Fatalf("column %q not found", columnName)
	}
	if column.NotNull != wantNotNull {
		t.Fatalf("%s notnull = %d, want %d", columnName, column.NotNull, wantNotNull)
	}
	if wantDefault == "" {
		if column.DefaultValue.Valid {
			t.Fatalf("%s dflt_value = %q, want NULL", columnName, column.DefaultValue.String)
		}
		return
	}
	if !column.DefaultValue.Valid {
		t.Fatalf("%s dflt_value is NULL, want %q", columnName, wantDefault)
	}
	if column.DefaultValue.String != wantDefault {
		t.Fatalf("%s dflt_value = %q, want %q", columnName, column.DefaultValue.String, wantDefault)
	}
}

type schemaTraceLogger struct {
	duplicateRelayColumnStatements int
}

func (l *schemaTraceLogger) LogMode(level logger.LogLevel) logger.Interface {
	return l
}

func (l *schemaTraceLogger) Info(context.Context, string, ...interface{})  {}
func (l *schemaTraceLogger) Warn(context.Context, string, ...interface{})  {}
func (l *schemaTraceLogger) Error(context.Context, string, ...interface{}) {}

func (l *schemaTraceLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), err error) {
	if err == nil {
		return
	}
	sql, _ := fc()
	if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") &&
		strings.Contains(strings.ToLower(sql), "alter table relay_listeners add column") {
		l.duplicateRelayColumnStatements++
	}
}

func (l *schemaTraceLogger) Reset() {
	l.duplicateRelayColumnStatements = 0
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
