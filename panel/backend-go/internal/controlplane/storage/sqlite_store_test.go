//go:build integration

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestIntegrationBootstrapFreshSchemaOmitsRetiredNetworkObjects(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	retiredPrefix := "wire" + "guard"

	if store.db.Migrator().HasTable(retiredPrefix + "_profiles") {
		t.Fatalf("fresh schema created retired profile table")
	}
	if store.db.Migrator().HasTable(retiredPrefix + "_clients") {
		t.Fatalf("fresh schema created retired client table")
	}
	for _, item := range []struct {
		model  any
		column string
	}{
		{model: &HTTPRuleRow{}, column: retiredPrefix + "_entry_enabled"},
		{model: &L4RuleRow{}, column: retiredPrefix + "_profile_id"},
		{model: &RelayListenerRow{}, column: retiredPrefix + "_profile_id"},
		{model: &EgressProfileRow{}, column: retiredPrefix + "_config_json"},
	} {
		if store.db.Migrator().HasColumn(item.model, item.column) {
			t.Fatalf("fresh schema created retired column %q", item.column)
		}
	}
}

func TestIntegrationStoreUpgradePreservesRetiredPhysicalObjectsWithoutSnapshotActivation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataRoot := t.TempDir()
	retiredPrefix := "wire" + "guard"
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(seed) error = %v", err)
	}
	if err := store.SaveAgent(ctx, AgentRow{
		ID:               "edge-old",
		Name:             "edge-old",
		CapabilitiesJSON: `["http_rules","l4","relay_quic","egress_profiles"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	retiredEgressID := 31
	if err := store.SaveHTTPRules(ctx, "edge-old", []HTTPRuleRow{
		{
			ID:                17,
			AgentID:           "edge-old",
			FrontendURL:       "https://media.example.com",
			BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			Enabled:           true,
			PassProxyHeaders:  true,
			Revision:          4,
		},
		{
			ID:                18,
			AgentID:           "edge-old",
			FrontendURL:       "https://retired-egress.example.com",
			BackendsJSON:      `[{"url":"http://127.0.0.1:8097"}]`,
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			EgressProfileID:   &retiredEgressID,
			Enabled:           true,
			Revision:          50,
		},
		{
			ID:                19,
			AgentID:           "edge-old",
			FrontendURL:       "https://retired-relay.example.com",
			BackendsJSON:      `[{"url":"http://127.0.0.1:8098"}]`,
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			RelayLayersJSON:   `[[41]]`,
			Enabled:           true,
			Revision:          60,
		},
	}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(ctx, "edge-old", []L4RuleRow{
		{
			ID:                27,
			AgentID:           "edge-old",
			Name:              "retired-listener",
			Protocol:          "tcp",
			ListenHost:        "0.0.0.0",
			ListenPort:        9000,
			BackendsJSON:      `[{"host":"127.0.0.1","port":9001}]`,
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			ListenMode:        retiredPrefix,
			Enabled:           true,
			Revision:          70,
		},
		{
			ID:                28,
			AgentID:           "edge-old",
			Name:              "ordinary-listener",
			Protocol:          "tcp",
			ListenHost:        "0.0.0.0",
			ListenPort:        9010,
			BackendsJSON:      `[{"host":"127.0.0.1","port":9011}]`,
			LoadBalancingJSON: `{"strategy":"adaptive"}`,
			ListenMode:        "tcp",
			Enabled:           true,
			Revision:          6,
		},
	}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	if err := store.SaveRelayListeners(ctx, "edge-old", []RelayListenerRow{{
		ID:            41,
		AgentID:       "edge-old",
		Name:          "retired-relay",
		BindHostsJSON: `["0.0.0.0"]`,
		ListenHost:    "0.0.0.0",
		ListenPort:    7443,
		PublicHost:    "relay.example.com",
		PublicPort:    7443,
		Enabled:       true,
		TransportMode: retiredPrefix,
		Revision:      80,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveEgressProfiles(ctx, []EgressProfileRow{{
		ID:       retiredEgressID,
		Name:     "retired-egress",
		Type:     retiredPrefix,
		Enabled:  true,
		Revision: 90,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(seed) error = %v", err)
	}

	db, err := openSQLiteForTest(filepath.Join(dataRoot, "panel.db"))
	if err != nil {
		t.Fatalf("openSQLiteForTest() error = %v", err)
	}
	rawDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	defer rawDB.Close()
	entryColumn := retiredPrefix + "_entry_enabled"
	profileIDColumn := retiredPrefix + "_profile_id"
	legacyEgressURIColumn := retiredPrefix + "_egress_uri"
	egressConfigColumn := retiredPrefix + "_config_json"
	if !db.Migrator().HasColumn(&HTTPRuleRow{}, entryColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE rules ADD COLUMN %q INTEGER NOT NULL DEFAULT 0`, entryColumn)).Error; err != nil {
			t.Fatalf("add retired entry column: %v", err)
		}
	}
	if !db.Migrator().HasColumn(&HTTPRuleRow{}, profileIDColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE rules ADD COLUMN %q INTEGER`, profileIDColumn)).Error; err != nil {
			t.Fatalf("add retired profile column: %v", err)
		}
	}
	if err := db.Exec(fmt.Sprintf(`UPDATE rules SET %q = 1, %q = 23 WHERE id = 17 AND agent_id = 'edge-old'`, entryColumn, profileIDColumn)).Error; err != nil {
		t.Fatalf("seed retired rule columns: %v", err)
	}
	if !db.Migrator().HasColumn(&L4RuleRow{}, profileIDColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE l4_rules ADD COLUMN %q INTEGER`, profileIDColumn)).Error; err != nil {
			t.Fatalf("add retired L4 profile column: %v", err)
		}
	}
	if !db.Migrator().HasColumn(&L4RuleRow{}, legacyEgressURIColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE l4_rules ADD COLUMN %q TEXT NOT NULL DEFAULT ''`, legacyEgressURIColumn)).Error; err != nil {
			t.Fatalf("add retired L4 egress URI column: %v", err)
		}
	}
	if err := db.Exec(fmt.Sprintf(`UPDATE l4_rules SET %q = 23 WHERE id = 27 AND agent_id = 'edge-old'`, profileIDColumn)).Error; err != nil {
		t.Fatalf("seed retired L4 profile column: %v", err)
	}
	legacyEgressURI := retiredPrefix + "://preserve-me"
	if err := db.Exec(fmt.Sprintf(`UPDATE l4_rules SET %q = ? WHERE id = 28 AND agent_id = 'edge-old'`, legacyEgressURIColumn), legacyEgressURI).Error; err != nil {
		t.Fatalf("seed retired L4 egress URI: %v", err)
	}
	if !db.Migrator().HasColumn(&RelayListenerRow{}, profileIDColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE relay_listeners ADD COLUMN %q INTEGER`, profileIDColumn)).Error; err != nil {
			t.Fatalf("add retired relay profile column: %v", err)
		}
	}
	if err := db.Exec(fmt.Sprintf(`UPDATE relay_listeners SET %q = 23 WHERE id = 41 AND agent_id = 'edge-old'`, profileIDColumn)).Error; err != nil {
		t.Fatalf("seed retired relay profile column: %v", err)
	}
	if !db.Migrator().HasColumn(&EgressProfileRow{}, egressConfigColumn) {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE egress_profiles ADD COLUMN %q TEXT NOT NULL DEFAULT ''`, egressConfigColumn)).Error; err != nil {
			t.Fatalf("add retired egress config column: %v", err)
		}
	}
	if err := db.Exec(fmt.Sprintf(`UPDATE egress_profiles SET %q = 'preserve-config' WHERE id = 31`, egressConfigColumn)).Error; err != nil {
		t.Fatalf("seed retired egress config column: %v", err)
	}
	profileTable := retiredPrefix + "_profiles"
	if err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (id INTEGER PRIMARY KEY, marker TEXT NOT NULL)`, profileTable)).Error; err != nil {
		t.Fatalf("create retired profile table: %v", err)
	}
	if !db.Migrator().HasColumn(profileTable, "marker") {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE %q ADD COLUMN marker TEXT NOT NULL DEFAULT ''`, profileTable)).Error; err != nil {
			t.Fatalf("add retired marker column: %v", err)
		}
	}
	if err := db.Exec(fmt.Sprintf(`INSERT OR REPLACE INTO %q (id, marker) VALUES (23, 'preserve-me')`, profileTable)).Error; err != nil {
		t.Fatalf("seed retired profile table: %v", err)
	}
	clientTable := retiredPrefix + "_clients"
	if err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (id INTEGER PRIMARY KEY, profile_id INTEGER NOT NULL, marker TEXT NOT NULL)`, clientTable)).Error; err != nil {
		t.Fatalf("create retired client table: %v", err)
	}
	if err := db.Exec(fmt.Sprintf(`INSERT OR REPLACE INTO %q (id, profile_id, marker) VALUES (24, 23, 'preserve-me')`, clientTable)).Error; err != nil {
		t.Fatalf("seed retired client table: %v", err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore(upgrade) error = %v", err)
	}
	defer store.Close()
	if !store.db.Migrator().HasTable(profileTable) || !store.db.Migrator().HasTable(clientTable) ||
		!store.db.Migrator().HasColumn(&HTTPRuleRow{}, entryColumn) ||
		!store.db.Migrator().HasColumn(&L4RuleRow{}, legacyEgressURIColumn) ||
		!store.db.Migrator().HasColumn(&RelayListenerRow{}, profileIDColumn) ||
		!store.db.Migrator().HasColumn(&EgressProfileRow{}, egressConfigColumn) {
		t.Fatalf("upgrade removed retired physical objects")
	}
	var retiredRows int64
	if err := store.db.Table(profileTable).Count(&retiredRows).Error; err != nil {
		t.Fatalf("count retired rows: %v", err)
	}
	if retiredRows != 1 {
		t.Fatalf("retired profile rows = %d, want 1", retiredRows)
	}
	if err := store.db.Table(clientTable).Count(&retiredRows).Error; err != nil {
		t.Fatalf("count retired client rows: %v", err)
	}
	if retiredRows != 1 {
		t.Fatalf("retired client rows = %d, want 1", retiredRows)
	}
	var preservedLegacyURI string
	if err := store.db.Table("l4_rules").Select(legacyEgressURIColumn).
		Where("id = ? AND agent_id = ?", 28, "edge-old").Scan(&preservedLegacyURI).Error; err != nil {
		t.Fatalf("read retired L4 egress URI: %v", err)
	}
	if preservedLegacyURI != legacyEgressURI {
		t.Fatalf("retired L4 egress URI = %q, want %q", preservedLegacyURI, legacyEgressURI)
	}
	if rows, err := store.ListL4Rules(ctx, "edge-old"); err != nil || len(rows) != 2 {
		t.Fatalf("physical L4 rows = %+v, %v; want two preserved rows", rows, err)
	}
	if rows, err := store.ListRelayListeners(ctx, "edge-old"); err != nil || len(rows) != 1 {
		t.Fatalf("physical relay rows = %+v, %v; want one preserved row", rows, err)
	}
	if rows, err := store.ListEgressProfiles(ctx); err != nil || len(rows) != 1 {
		t.Fatalf("physical egress rows = %+v, %v; want one preserved row", rows, err)
	}

	snapshot, err := store.LoadAgentSnapshot(ctx, "edge-old", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.Rules) != 1 || snapshot.Rules[0].ID != 17 {
		t.Fatalf("ordinary HTTP rules = %+v, want preserved rule 17", snapshot.Rules)
	}
	if snapshot.Revision != 6 {
		t.Fatalf("snapshot revision = %d, want ordinary-resource revision 6", snapshot.Revision)
	}
	if len(snapshot.L4Rules) != 1 || snapshot.L4Rules[0].ID != 28 {
		t.Fatalf("ordinary L4 rules = %+v, want preserved rule 28", snapshot.L4Rules)
	}
	if snapshot.L4Rules[0].EgressProfileID != nil {
		t.Fatalf("legacy URI activated an egress profile: %+v", snapshot.L4Rules[0])
	}
	if len(snapshot.RelayListeners) != 0 || len(snapshot.EgressProfiles) != 0 {
		t.Fatalf("retired resources entered snapshot: l4=%+v relay=%+v egress=%+v", snapshot.L4Rules, snapshot.RelayListeners, snapshot.EgressProfiles)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), retiredPrefix) {
		t.Fatalf("snapshot contains retired network content: %s", raw)
	}
}

func TestIntegrationStoreLoadAgentSnapshotUsesEgressProfileRevision(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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
	profileID := 41
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
		EgressProfileID:   &profileID,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
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

func TestIntegrationStoreLoadAgentSnapshotScopesEgressProfilesToExecutors(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	httpProfileID := 41
	l4ProfileID := 42
	relayProfileID := 43
	unusedProfileID := 44
	disabledRelayProfileID := 45
	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{
		{ID: httpProfileID, Name: "http exit", Type: "socks", ProxyURL: "socks5://http-secret@127.0.0.1:1080", Enabled: true, Revision: 7},
		{ID: l4ProfileID, Name: "l4 exit", Type: "http", ProxyURL: "http://l4-secret@127.0.0.1:8080", Enabled: true, Revision: 8},
		{ID: relayProfileID, Name: "relay exit", Type: "socks", ProxyURL: "socks5://relay-secret@127.0.0.1:1081", Enabled: true, Revision: 9},
		{ID: unusedProfileID, Name: "unused exit", Type: "socks", ProxyURL: "socks5://unused-secret@127.0.0.1:1082", Enabled: true, Revision: 10},
		{ID: disabledRelayProfileID, Name: "disabled final exit", Type: "socks", ProxyURL: "socks5://disabled-final-secret@127.0.0.1:1083", Enabled: true, Revision: 11},
	}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "http-owner", []HTTPRuleRow{{
		ID:                1001,
		AgentID:           "http-owner",
		FrontendURL:       "https://http.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[]`,
		RelayLayersJSON:   `[]`,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &httpProfileID,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(http-owner) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "l4-owner", []L4RuleRow{{
		ID:                 2001,
		AgentID:            "l4-owner",
		Name:               "tcp direct",
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
		EgressProfileID:    &l4ProfileID,
		Revision:           1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(l4-owner) error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "relay-entry", []HTTPRuleRow{{
		ID:                3001,
		AgentID:           "relay-entry",
		FrontendURL:       "https://relay.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[501,502]`,
		RelayLayersJSON:   `[[501],[502,503]]`,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &relayProfileID,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(relay-entry) error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "disabled-final-entry", []HTTPRuleRow{{
		ID:                3002,
		AgentID:           "disabled-final-entry",
		FrontendURL:       "https://disabled-final.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[504,505]`,
		RelayLayersJSON:   `[[504],[505]]`,
		CustomHeadersJSON: `[]`,
		EgressProfileID:   &disabledRelayProfileID,
		Revision:          1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(disabled-final-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-entry", []RelayListenerRow{{
		ID:         501,
		AgentID:    "relay-entry",
		Name:       "entry relay",
		ListenHost: "127.0.0.1",
		ListenPort: 7443,
		PublicHost: "entry.example.com",
		PublicPort: 7443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-final-a", []RelayListenerRow{{
		ID:         502,
		AgentID:    "relay-final-a",
		Name:       "final relay a",
		ListenHost: "127.0.0.1",
		ListenPort: 8443,
		PublicHost: "final-a.example.com",
		PublicPort: 8443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-final-a) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "relay-final-b", []RelayListenerRow{{
		ID:         503,
		AgentID:    "relay-final-b",
		Name:       "final relay b",
		ListenHost: "127.0.0.1",
		ListenPort: 9443,
		PublicHost: "final-b.example.com",
		PublicPort: 9443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(relay-final-b) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "disabled-final-entry", []RelayListenerRow{{
		ID:         504,
		AgentID:    "disabled-final-entry",
		Name:       "disabled final entry",
		ListenHost: "127.0.0.1",
		ListenPort: 10443,
		PublicHost: "disabled-entry.example.com",
		PublicPort: 10443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(disabled-final-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "disabled-final-agent", []RelayListenerRow{{
		ID:         505,
		AgentID:    "disabled-final-agent",
		Name:       "disabled final",
		ListenHost: "127.0.0.1",
		ListenPort: 11443,
		PublicHost: "disabled-final.example.com",
		PublicPort: 11443,
		Enabled:    false,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(disabled-final-agent) error = %v", err)
	}

	httpOwnerSnapshot, err := store.LoadAgentSnapshot(t.Context(), "http-owner", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(http-owner) error = %v", err)
	}
	assertSnapshotHasProfile(t, httpOwnerSnapshot, httpProfileID, "socks5://http-secret@127.0.0.1:1080")
	assertSnapshotLacksProfile(t, httpOwnerSnapshot, unusedProfileID)

	l4OwnerSnapshot, err := store.LoadAgentSnapshot(t.Context(), "l4-owner", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(l4-owner) error = %v", err)
	}
	assertSnapshotHasProfile(t, l4OwnerSnapshot, l4ProfileID, "http://l4-secret@127.0.0.1:8080")
	assertSnapshotLacksProfile(t, l4OwnerSnapshot, unusedProfileID)

	relayEntrySnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-entry", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(relay-entry) error = %v", err)
	}
	assertSnapshotLacksProfile(t, relayEntrySnapshot, relayProfileID)
	assertSnapshotLacksProfile(t, relayEntrySnapshot, unusedProfileID)
	assertSnapshotLacksProfile(t, relayEntrySnapshot, disabledRelayProfileID)

	relayFinalASnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-final-a", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(relay-final-a) error = %v", err)
	}
	assertSnapshotHasProfile(t, relayFinalASnapshot, relayProfileID, "socks5://relay-secret@127.0.0.1:1081")
	assertSnapshotLacksProfile(t, relayFinalASnapshot, unusedProfileID)

	relayFinalBSnapshot, err := store.LoadAgentSnapshot(t.Context(), "relay-final-b", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(relay-final-b) error = %v", err)
	}
	assertSnapshotHasProfile(t, relayFinalBSnapshot, relayProfileID, "socks5://relay-secret@127.0.0.1:1081")
	assertSnapshotLacksProfile(t, relayFinalBSnapshot, unusedProfileID)

	disabledFinalSnapshot, err := store.LoadAgentSnapshot(t.Context(), "disabled-final-agent", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(disabled-final-agent) error = %v", err)
	}
	assertSnapshotLacksProfile(t, disabledFinalSnapshot, disabledRelayProfileID)

	unrelatedSnapshot, err := store.LoadAgentSnapshot(t.Context(), "unrelated", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(unrelated) error = %v", err)
	}
	assertSnapshotLacksProfile(t, unrelatedSnapshot, httpProfileID)
	assertSnapshotLacksProfile(t, unrelatedSnapshot, l4ProfileID)
	assertSnapshotLacksProfile(t, unrelatedSnapshot, relayProfileID)
	assertSnapshotLacksProfile(t, unrelatedSnapshot, unusedProfileID)
	assertSnapshotLacksProfile(t, unrelatedSnapshot, disabledRelayProfileID)
}

func TestIntegrationStoreLoadLocalSnapshotIncludesRelayFinalHopEgressProfile(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	profileID := 41
	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{{
		ID:       profileID,
		Name:     "local relay exit",
		Type:     "socks",
		ProxyURL: "socks5://127.0.0.1:1080",
		Enabled:  true,
		Revision: 7,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "local", []RelayListenerRow{{
		ID:         501,
		AgentID:    "local",
		Name:       "local final relay",
		ListenHost: "127.0.0.1",
		ListenPort: 8443,
		PublicHost: "127.0.0.1",
		PublicPort: 8443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(local) error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "local", []L4RuleRow{{
		ID:                2001,
		AgentID:           "local",
		Name:              "local relay l4",
		Protocol:          "tcp",
		ListenHost:        "0.0.0.0",
		ListenPort:        19000,
		BackendsJSON:      `[{"host":"127.0.0.1","port":19101}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		TuningJSON:        `{}`,
		RelayLayersJSON:   `[[501]]`,
		ListenMode:        "tcp",
		Enabled:           true,
		EgressProfileID:   &profileID,
		Revision:          2,
	}}); err != nil {
		t.Fatalf("SaveL4Rules(local) error = %v", err)
	}

	snapshot, err := store.LoadLocalSnapshot(t.Context(), "local")
	if err != nil {
		t.Fatalf("LoadLocalSnapshot() error = %v", err)
	}
	assertSnapshotHasProfile(t, snapshot, profileID, "socks5://127.0.0.1:1080")
}

func TestIntegrationStoreLoadAgentSnapshotBumpsRelayExecutorWhenEgressProfileReferenceRemoved(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "reference-removed-final",
		Name:            "reference removed final",
		DesiredRevision: 7,
		CurrentRevision: 7,
	}); err != nil {
		t.Fatalf("SaveAgent(reference-removed-final) error = %v", err)
	}
	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{{
		ID:       102,
		Name:     "unreferenced exit",
		Type:     "socks",
		ProxyURL: "socks5://unreferenced-secret@127.0.0.1:1080",
		Enabled:  true,
		Revision: 8,
	}}); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "reference-removed-entry", []HTTPRuleRow{{
		ID:                5001,
		AgentID:           "reference-removed-entry",
		FrontendURL:       "https://reference-removed.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[601,602]`,
		RelayLayersJSON:   `[[601],[602]]`,
		CustomHeadersJSON: `[]`,
		Revision:          22,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(reference-removed-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "reference-removed-entry", []RelayListenerRow{{
		ID:         601,
		AgentID:    "reference-removed-entry",
		Name:       "entry",
		ListenHost: "127.0.0.1",
		ListenPort: 10443,
		PublicHost: "entry.example.com",
		PublicPort: 10443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(reference-removed-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "reference-removed-final", []RelayListenerRow{{
		ID:         602,
		AgentID:    "reference-removed-final",
		Name:       "final",
		ListenHost: "127.0.0.1",
		ListenPort: 11443,
		PublicHost: "final.example.com",
		PublicPort: 11443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(reference-removed-final) error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "reference-removed-final", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.EgressProfiles) != 0 {
		t.Fatalf("snapshot egress profiles = %+v, want none after reference removal", snapshot.EgressProfiles)
	}
	if snapshot.Revision != 22 {
		t.Fatalf("snapshot revision = %d, want relay rule revision 22 for cleanup", snapshot.Revision)
	}
}

func TestIntegrationStoreLoadAgentSnapshotBumpsRelayExecutorWhenLastEgressProfileRemoved(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveAgent(t.Context(), AgentRow{
		ID:              "last-profile-removed-final",
		Name:            "last profile removed final",
		DesiredRevision: 7,
		CurrentRevision: 7,
	}); err != nil {
		t.Fatalf("SaveAgent(last-profile-removed-final) error = %v", err)
	}
	if err := store.SaveEgressProfiles(t.Context(), nil); err != nil {
		t.Fatalf("SaveEgressProfiles() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "last-profile-removed-entry", []HTTPRuleRow{{
		ID:                5501,
		AgentID:           "last-profile-removed-entry",
		FrontendURL:       "https://last-profile-removed.example.com",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"adaptive"}`,
		Enabled:           true,
		TagsJSON:          `[]`,
		RelayChainJSON:    `[651,652]`,
		RelayLayersJSON:   `[[651],[652]]`,
		CustomHeadersJSON: `[]`,
		Revision:          24,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules(last-profile-removed-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "last-profile-removed-entry", []RelayListenerRow{{
		ID:         651,
		AgentID:    "last-profile-removed-entry",
		Name:       "entry",
		ListenHost: "127.0.0.1",
		ListenPort: 14443,
		PublicHost: "entry.example.com",
		PublicPort: 14443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(last-profile-removed-entry) error = %v", err)
	}
	if err := store.SaveRelayListeners(t.Context(), "last-profile-removed-final", []RelayListenerRow{{
		ID:         652,
		AgentID:    "last-profile-removed-final",
		Name:       "final",
		ListenHost: "127.0.0.1",
		ListenPort: 15443,
		PublicHost: "final.example.com",
		PublicPort: 15443,
		Enabled:    true,
		Revision:   1,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners(last-profile-removed-final) error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(t.Context(), "last-profile-removed-final", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if len(snapshot.EgressProfiles) != 0 {
		t.Fatalf("snapshot egress profiles = %+v, want none after last profile removal", snapshot.EgressProfiles)
	}
	if snapshot.Revision != 24 {
		t.Fatalf("snapshot revision = %d, want relay rule revision 24 for cleanup after last profile removal", snapshot.Revision)
	}
}

func TestIntegrationStoreLoadAgentSnapshotIncludesPersistedRuleEgressProfileIDs(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

func TestIntegrationStoreEgressProfileReferencesFindsRowsAcrossAllAgents(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	profileID := 51
	otherProfileID := 52
	if err := store.SaveHTTPRules(t.Context(), "orphan-http", []HTTPRuleRow{{
		ID:              7,
		AgentID:         "orphan-http",
		FrontendURL:     "http://orphan.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &profileID,
		Enabled:         false,
		Revision:        1,
	}, {
		ID:              8,
		AgentID:         "orphan-http",
		FrontendURL:     "http://other.example.com",
		BackendsJSON:    `[{"url":"http://127.0.0.1:8096"}]`,
		EgressProfileID: &otherProfileID,
		Enabled:         true,
		Revision:        1,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveL4Rules(t.Context(), "orphan-l4", []L4RuleRow{{
		ID:              9,
		AgentID:         "orphan-l4",
		Name:            "orphan l4",
		Protocol:        "tcp",
		ListenHost:      "0.0.0.0",
		ListenPort:      9443,
		BackendsJSON:    `[{"host":"127.0.0.1","port":443}]`,
		EgressProfileID: &profileID,
		Enabled:         false,
		Revision:        1,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}

	references, err := store.EgressProfileReferences(t.Context(), profileID)
	if err != nil {
		t.Fatalf("EgressProfileReferences() error = %v", err)
	}
	if len(references) != 2 {
		t.Fatalf("reference count = %d, want 2: %+v", len(references), references)
	}
	if references[0] != (EgressProfileReference{Kind: "http", AgentID: "orphan-http", ID: 7}) {
		t.Fatalf("references[0] = %+v", references[0])
	}
	if references[1] != (EgressProfileReference{Kind: "l4", AgentID: "orphan-l4", ID: 9}) {
		t.Fatalf("references[1] = %+v", references[1])
	}
}

func TestIntegrationBootstrapSQLiteSchemaUpgradesLegacySQLiteAndNormalizesBackfills(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
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

	store, err := openExistingStorageTestSQLiteStore(dataRoot, "legacy-agent", true)
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

func TestIntegrationBootstrapSchemaMigratesLegacyHTTPRuleFieldsToCanonical(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "legacy-http-agent")
	db := store.db

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

func TestIntegrationBootstrapSchemaMigratesLegacyL4RuleFieldsToCanonical(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "legacy-l4-agent")
	db := store.db

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

func TestIntegrationBootstrapSchemaMigratesLegacyRuleFieldsOutsideSQLiteLegacyBootstrap(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "general-bootstrap-agent")
	db := store.db

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

func TestIntegrationBootstrapSchemaPreservesCanonicalHTTPAndL4FieldsAcrossRepeatedRuns(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "canonical-agent")
	db := store.db

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

func TestIntegrationBootstrapSchemaDoesNotOverwriteMalformedCanonicalFields(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "malformed-canonical-agent")
	db := store.db

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

func TestIntegrationBootstrapSQLiteSchemaHandlesMalformedRelayBindHostsJSON(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "legacy-agent")
	db := store.db

	if err := db.WithContext(t.Context()).Exec(`INSERT INTO relay_listeners (
		id, agent_id, name, listen_host, listen_port, public_host, public_port, enabled, bind_hosts, tls_mode, pin_set, trusted_ca_certificate_ids, allow_self_signed, tags, revision
	) VALUES (21, 'legacy-agent', 'bad-json', '10.10.0.5', 7443, '', NULL, 1, 'not-json', 'pin_or_ca', '[]', '[]', 0, '[]', 1)`).Error; err != nil {
		t.Fatalf("seed malformed relay listener error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() with malformed bind_hosts error = %v", err)
	}

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

func TestIntegrationBootstrapSQLiteSchemaDoesNotRetryExistingRelayTransportColumns(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "local")
	traceLogger := &schemaTraceLogger{}
	db := store.db.Session(&gorm.Session{Logger: traceLogger})
	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("repeated BootstrapSQLiteSchema() error = %v", err)
	}

	if traceLogger.duplicateRelayColumnStatements != 0 {
		t.Fatalf("expected no duplicate relay column ALTER statements, got %d", traceLogger.duplicateRelayColumnStatements)
	}
}

func TestIntegrationNormalizeRelayListenerRowAppliesLegacyTransportDefaultsWithoutClobberingExplicitFalse(t *testing.T) {
	t.Parallel()
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

func TestIntegrationSnapshotHTTPRulesUsesCanonicalBackendsAndRelayLayersOnly(t *testing.T) {
	t.Parallel()
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

func TestIntegrationSnapshotL4RulesPreservesProxyEntryPasswordAndTrimsUsername(t *testing.T) {
	t.Parallel()
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

func TestIntegrationStorePersistsRelayListenersAndManagedCertificates(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreSaveManagedCertificatesRemovesMaterialForDeletedDomains(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadsLocalSnapshotWithHighestRelevantRevision(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadsAgentSnapshotWithReferencedRelayListenersAndCertificates(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadAgentSnapshotWithholdsMasterIssuedPolicyWithoutMaterial(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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
		ID:              "edge-withhold",
		Name:            "edge-withhold",
		AgentToken:      "token-edge-withhold",
		DesiredVersion:  "1.2.3",
		DesiredRevision: 2,
		CurrentRevision: 1,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	rows := []ManagedCertificateRow{
		{
			ID:              40,
			Domain:          "issuing.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["edge-withhold"]`,
			Status:          "issuing",
			AgentReports:    `{}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        5,
		},
		{
			ID:              41,
			Domain:          "agent-issued.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["edge-withhold"]`,
			Status:          "issuing",
			AgentReports:    `{}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        6,
		},
	}
	if err := store.SaveManagedCertificates(t.Context(), rows); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}

	load := func() Snapshot {
		t.Helper()
		snapshot, err := store.LoadAgentSnapshot(t.Context(), "edge-withhold", AgentSnapshotInput{
			DesiredVersion:  "1.2.3",
			DesiredRevision: 6,
			CurrentRevision: 1,
			Platform:        "linux-amd64",
		})
		if err != nil {
			t.Fatalf("LoadAgentSnapshot() error = %v", err)
		}
		return snapshot
	}

	snapshot := load()
	if containsPolicyID(snapshot.CertificatePolicies, 40) {
		t.Fatalf("master_cf_dns policy id 40 must be withheld while it has no material (agents cannot issue it): %+v", snapshot.CertificatePolicies)
	}
	if containsCertificateID(snapshot.Certificates, 40) {
		t.Fatalf("master_cf_dns bundle id 40 must be absent without material: %+v", snapshot.Certificates)
	}
	if !containsPolicyID(snapshot.CertificatePolicies, 41) {
		t.Fatalf("local_http01 policy id 41 must remain published without material (the agent issues it): %+v", snapshot.CertificatePolicies)
	}

	// Background signer completes: material is written and the row flips to active.
	writeManagedCertificateMaterial(t, dataRoot, "issuing.example.com", "issued-cert", "issued-key")
	rows[0].Status = "active"
	if err := store.SaveManagedCertificates(t.Context(), rows); err != nil {
		t.Fatalf("SaveManagedCertificates() reactivate error = %v", err)
	}

	snapshot = load()
	if !containsPolicyID(snapshot.CertificatePolicies, 40) || !containsCertificateID(snapshot.Certificates, 40) {
		t.Fatalf("master_cf_dns id 40 must be published once material exists: policies=%+v certs=%+v", snapshot.CertificatePolicies, snapshot.Certificates)
	}
}

func TestIntegrationStoreLoadAgentSnapshotIgnoresDisabledRelayDependencies(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadAgentSnapshotSkipsMalformedL4Rows(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadAgentSnapshotIncludesProxyEntryL4RuleWithoutBackend(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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
	if rule.ID != 71 || rule.ListenMode != "proxy" {
		t.Fatalf("L4Rules[0] = %+v", rule)
	}
	if len(rule.Backends) != 0 || rule.UpstreamHost != "" || rule.UpstreamPort != 0 {
		t.Fatalf("proxy entry targets = backends=%+v upstream=%s:%d", rule.Backends, rule.UpstreamHost, rule.UpstreamPort)
	}
}

func TestIntegrationStoreLoadAgentSnapshotExcludesUpstreamOnlyL4RowsWithoutCanonicalBackends(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationStoreLoadAgentSnapshotIncludesRelayObfsFlags(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationBootstrapSchemaSkipsRepeatedAgentDefaultNormalization(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "local")
	db := store.db

	var updateCount int
	callbackName := "test:count_agent_default_normalization_updates"
	if err := db.Callback().Raw().Before("gorm:raw").Register(callbackName, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.HasPrefix(sql, "UPDATE agents SET ") && strings.Contains(sql, " IS NULL") {
			updateCount++
		}
	}); err != nil {
		t.Fatalf("register raw callback: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Callback().Raw().Remove(callbackName)
	})

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if updateCount != 0 {
		t.Fatalf("repeated agent default normalization updates = %d, want 0", updateCount)
	}
}

func TestIntegrationStoreLoadAgentSnapshotUsesStoredAgentDesiredRevisionForProxyOnlyConfig(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestIntegrationStoreLoadAgentSnapshotTreatsLocalAgentAsSpecialRuntimeState(t *testing.T) {
	t.Parallel()
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

func TestIntegrationStoreSavesSuccessfulLocalRuntimeStateIntoLocalAgentState(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

func TestIntegrationSaveAgentHeartbeatUpdatesLivenessWithoutOverwritingConfig(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	if err := store.SaveAgent(ctx, AgentRow{
		ID:              "edge-heartbeat",
		Name:            "edge",
		AgentToken:      "token",
		DesiredRevision: 7,
		CurrentRevision: 6,
		LastApplyStatus: "success",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveAgentHeartbeat(ctx, AgentRow{
		ID:                    "edge-heartbeat",
		Name:                  "changed-name",
		AgentToken:            "changed-token",
		Version:               "1.2.3",
		Platform:              "linux-amd64",
		DesiredRevision:       99,
		CurrentRevision:       8,
		LastApplyRevision:     8,
		LastApplyStatus:       "error",
		LastApplyMessage:      "apply failed",
		LastReportedStatsJSON: `{"status":"running"}`,
		LastSeenAt:            "2026-06-18T16:00:00Z",
		LastSeenIP:            "10.0.0.1",
	}); err != nil {
		t.Fatal(err)
	}

	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got AgentRow
	for _, row := range agents {
		if row.ID == "edge-heartbeat" {
			got = row
			break
		}
	}
	if got.Name != "edge" || got.AgentToken != "token" || got.DesiredRevision != 7 {
		t.Fatalf("config fields overwritten: %+v", got)
	}
	if got.Version != "1.2.3" || got.CurrentRevision != 8 || got.LastApplyStatus != "error" || got.LastReportedStatsJSON == "" || got.LastSeenIP != "10.0.0.1" {
		t.Fatalf("heartbeat fields not updated: %+v", got)
	}
}

func TestIntegrationAuthenticatedRegistrationCannotRestoreConcurrentlyRevokedToken(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	row := AgentRow{ID: "edge-register", Name: "before", AgentToken: "control-token", TagsJSON: `[]`, CapabilitiesJSON: `[]`}
	if err := store.SaveAgent(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.Name = "authenticated-update"
	if err := store.SaveAuthenticatedAgentRegistration(ctx, "control-token", row); err != nil {
		t.Fatalf("SaveAuthenticatedAgentRegistration() = %v", err)
	}
	if err := store.db.WithContext(ctx).Model(&AgentRow{}).Where("id = ?", row.ID).Update("agent_token", "").Error; err != nil {
		t.Fatal(err)
	}
	row.Name = "stale-update"
	if err := store.SaveAuthenticatedAgentRegistration(ctx, "control-token", row); !errors.Is(err, ErrAgentControlTokenChanged) {
		t.Fatalf("stale SaveAuthenticatedAgentRegistration() = %v", err)
	}
	var persisted AgentRow
	if err := store.db.WithContext(ctx).Where("id = ?", row.ID).First(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.AgentToken != "" || persisted.Name != "authenticated-update" {
		t.Fatalf("stale registration restored revoked state: %+v", persisted)
	}
}

func TestIntegrationStoreSaveLocalRuntimeStateUsesExplicitApplyMetadata(t *testing.T) {
	t.Parallel()
	dataRoot := seedSQLiteFixtureFromGORM(t)

	store, err := takeSeededSQLiteFixture(dataRoot)
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

var seededSQLiteFixtureStores sync.Map

func takeSeededSQLiteFixture(dataRoot string) (*SQLiteStore, error) {
	value, ok := seededSQLiteFixtureStores.LoadAndDelete(dataRoot)
	if !ok {
		return nil, errors.New("seeded SQLite fixture not found")
	}
	store, ok := value.(*SQLiteStore)
	if !ok {
		return nil, errors.New("seeded SQLite fixture has unexpected type")
	}
	return store, nil
}

func seedSQLiteFixtureFromGORM(t *testing.T) string {
	t.Helper()

	dataRoot := t.TempDir()
	store, err := newStorageTestSQLiteStore(t, dataRoot, "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		seededSQLiteFixtureStores.Delete(dataRoot)
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

	seededSQLiteFixtureStores.Store(dataRoot, store)
	return dataRoot
}

func openSQLiteForTest(dbPath string) (*gorm.DB, error) {
	dsn := dbPath +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
	return gorm.Open(sqlite.Open(dsn), &gorm.Config{
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

func TestIntegrationManagedCertificateDirectoryUsesCollisionIsolatedComponent(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()
	got := managedCertificateDirectory(baseDir, `../../evil\leaf`)
	want := filepath.Join(baseDir, managedCertificateDomainStorageKey(`../../evil\leaf`))
	if got != want {
		t.Fatalf("managedCertificateDirectory() = %q, want %q", got, want)
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

func assertSnapshotHasProfile(t *testing.T, snapshot Snapshot, id int, proxyURL string) {
	t.Helper()
	for _, profile := range snapshot.EgressProfiles {
		if profile.ID == id {
			if profile.ProxyURL != proxyURL {
				t.Fatalf("profile %d proxy_url = %q, want %q", id, profile.ProxyURL, proxyURL)
			}
			return
		}
	}
	t.Fatalf("snapshot egress profiles = %+v, want profile %d", snapshot.EgressProfiles, id)
}

func assertSnapshotLacksProfile(t *testing.T, snapshot Snapshot, id int) {
	t.Helper()
	for _, profile := range snapshot.EgressProfiles {
		if profile.ID == id {
			t.Fatalf("snapshot unexpectedly included profile %d: %+v", id, snapshot.EgressProfiles)
		}
	}
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

// mustGetAgentByID returns the single normalized agent row with the given id,
// failing the test if it is missing. It backs the DDNS heartbeat assertions.
func mustGetAgentByID(t *testing.T, store *GormStore, agentID string) AgentRow {
	t.Helper()
	agents, err := store.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	for _, row := range agents {
		if row.ID == agentID {
			return row
		}
	}
	t.Fatalf("agent %q not found", agentID)
	return AgentRow{}
}

// TestSaveAgentHeartbeatOverridesIPv4IPv6OnlyWhenReported pins the contract that
// agent-reported IPv4/IPv6 overwrite the stored value only when non-empty, so a
// family that is transiently unavailable does not clobber the last known address.
// It also proves the heartbeat path never touches ddns_config / ddns_status,
// which are owned by the service/master DDNS writer.
func TestIntegrationSaveAgentHeartbeatOverridesIPv4IPv6OnlyWhenReported(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	if err := store.SaveAgent(ctx, AgentRow{
		ID:             "edge-ddns",
		Name:           "edge-ddns",
		AgentToken:     "token",
		LastSeenIPv4:   "203.0.113.4",
		LastSeenIPv6:   "2001:db8::4",
		DdnsConfigJSON: `{"domain":"edge.example.com","ipv4":{"enabled":true}}`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.SaveAgentHeartbeat(ctx, AgentRow{
		ID:           "edge-ddns",
		LastSeenIPv4: "203.0.113.9",
		LastSeenIPv6: "2001:db8::9",
	}); err != nil {
		t.Fatalf("SaveAgentHeartbeat(update) error = %v", err)
	}
	got := mustGetAgentByID(t, store, "edge-ddns")
	if got.LastSeenIPv4 != "203.0.113.9" || got.LastSeenIPv6 != "2001:db8::9" {
		t.Fatalf("heartbeat did not update reported IPs: %+v", got)
	}

	// Only IPv6 is reported this cycle; IPv4 must keep its last known value.
	if err := store.SaveAgentHeartbeat(ctx, AgentRow{
		ID:           "edge-ddns",
		LastSeenIPv4: "",
		LastSeenIPv6: "2001:db8::12",
	}); err != nil {
		t.Fatalf("SaveAgentHeartbeat(partial) error = %v", err)
	}
	got = mustGetAgentByID(t, store, "edge-ddns")
	if got.LastSeenIPv4 != "203.0.113.9" {
		t.Fatalf("empty v4 report clobbered last known v4: %+v", got)
	}
	if got.LastSeenIPv6 != "2001:db8::12" {
		t.Fatalf("reported v6 not updated: %+v", got)
	}

	// The heartbeat path Selects neither ddns_config nor ddns_status, so the
	// master-owned DDNS config must survive every heartbeat untouched.
	if got.DdnsConfigJSON != `{"domain":"edge.example.com","ipv4":{"enabled":true}}` {
		t.Fatalf("heartbeat altered ddns_config: %+v", got)
	}
	if got.DdnsStatusJSON != "" {
		t.Fatalf("heartbeat wrote ddns_status: %+v", got)
	}
}

// TestIntegrationUpdateDdnsStatusColumnWritesOnlyStatus guards the DDNS
// reconciler's narrow update against clobbering concurrent agent writes.
func TestIntegrationUpdateDdnsStatusColumnWritesOnlyStatus(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	if err := store.SaveAgent(ctx, AgentRow{
		ID:             "edge-ddns",
		Name:           "edge-ddns",
		AgentToken:     "rotated-token",
		LastSeenIPv4:   "203.0.113.4",
		LastSeenIPv6:   "2001:db8::4",
		DdnsConfigJSON: `{"domain":"edge.example.com","ipv4":{"enabled":true}}`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	if err := store.UpdateDdnsStatusColumn(ctx, "edge-ddns", `{"status":"ok"}`); err != nil {
		t.Fatalf("UpdateDdnsStatusColumn() error = %v", err)
	}
	got := mustGetAgentByID(t, store, "edge-ddns")
	if got.DdnsStatusJSON != `{"status":"ok"}` {
		t.Fatalf("ddns_status not written: %q", got.DdnsStatusJSON)
	}
	if got.DdnsConfigJSON != `{"domain":"edge.example.com","ipv4":{"enabled":true}}` {
		t.Fatalf("narrow update clobbered ddns_config: %q", got.DdnsConfigJSON)
	}
	if got.AgentToken != "rotated-token" {
		t.Fatalf("narrow update clobbered agent_token: %q", got.AgentToken)
	}
	if got.LastSeenIPv4 != "203.0.113.4" || got.LastSeenIPv6 != "2001:db8::4" {
		t.Fatalf("narrow update clobbered reported IPs: %+v", got)
	}
	if got.Name != "edge-ddns" {
		t.Fatalf("narrow update clobbered name: %q", got.Name)
	}

	if err := store.UpdateDdnsStatusColumn(ctx, "", `{"status":"ok"}`); err != nil {
		t.Fatalf("UpdateDdnsStatusColumn(empty id) error = %v", err)
	}
	if err := store.UpdateDdnsStatusColumn(ctx, "missing", `{"status":"ok"}`); err != nil {
		t.Fatalf("UpdateDdnsStatusColumn(unknown id) error = %v", err)
	}
}

// TestLoadAgentSnapshotExposesDDNSConfig verifies the snapshot wire contract
// surfaces the per-agent DDNS configuration (domain + per-family strategy) so
// the desired-state dispatch can carry it to the agent.
func TestIntegrationLoadAgentSnapshotExposesDDNSConfig(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := t.Context()

	if err := store.SaveAgent(ctx, AgentRow{
		ID:             "edge-ddns",
		Name:           "edge-ddns",
		DdnsConfigJSON: `{"domain":"edge.example.com","ipv4":{"enabled":true,"source":"public_api"},"ipv6":{"enabled":true,"source":"interface","interface":"eth0"}}`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	snapshot, err := store.LoadAgentSnapshot(ctx, "edge-ddns", AgentSnapshotInput{})
	if err != nil {
		t.Fatalf("LoadAgentSnapshot() error = %v", err)
	}
	if snapshot.DDNSConfig == nil {
		t.Fatalf("DDNSConfig = nil, want populated")
	}
	if snapshot.DDNSConfig.Domain != "edge.example.com" {
		t.Fatalf("Domain = %q", snapshot.DDNSConfig.Domain)
	}
	if !snapshot.DDNSConfig.IPv4.Enabled || snapshot.DDNSConfig.IPv4.Source != "public_api" {
		t.Fatalf("IPv4 = %+v", snapshot.DDNSConfig.IPv4)
	}
	if !snapshot.DDNSConfig.IPv6.Enabled || snapshot.DDNSConfig.IPv6.Source != "interface" || snapshot.DDNSConfig.IPv6.Interface != "eth0" {
		t.Fatalf("IPv6 = %+v", snapshot.DDNSConfig.IPv6)
	}
}

// TestLoadAgentSnapshotOmitsDDNSConfigWhenEmptyOrDisabled guards the empty-state
// contract: a missing, all-disabled, or malformed ddns_config yields a nil
// pointer so the wire payload omits the field entirely (omitempty).
func TestIntegrationLoadAgentSnapshotOmitsDDNSConfigWhenEmptyOrDisabled(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	ctx := t.Context()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "all_disabled", raw: `{"domain":"","ipv4":{"enabled":false},"ipv6":{"enabled":false}}`},
		{name: "malformed", raw: "{not-json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentID := "edge-ddns-" + tc.name
			if err := store.SaveAgent(ctx, AgentRow{ID: agentID, Name: agentID, DdnsConfigJSON: tc.raw}); err != nil {
				t.Fatalf("SaveAgent() error = %v", err)
			}
			snapshot, err := store.LoadAgentSnapshot(ctx, agentID, AgentSnapshotInput{})
			if err != nil {
				t.Fatalf("LoadAgentSnapshot() error = %v", err)
			}
			if snapshot.DDNSConfig != nil {
				t.Fatalf("DDNSConfig = %+v, want nil for %s", snapshot.DDNSConfig, tc.name)
			}
		})
	}
}

func TestIntegrationSQLiteColumnContractIncludesEgressProfiles(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()
	columns := loadSQLiteTableInfo(t, store.db, "egress_profiles")

	assertSQLiteColumnContract(t, columns, "id", 1, "")
	assertSQLiteColumnContract(t, columns, "name", 1, "")
	assertSQLiteColumnContract(t, columns, "type", 1, "")
	assertSQLiteColumnContract(t, columns, "proxy_url", 1, `""`)
	retiredColumn := "wire" + "guard_config_json"
	if _, found := columns[retiredColumn]; found {
		t.Fatalf("fresh egress schema contains retired column %q", retiredColumn)
	}
	assertSQLiteColumnContract(t, columns, "enabled", 1, "true")
	assertSQLiteColumnContract(t, columns, "description", 1, `""`)
	assertSQLiteColumnContract(t, columns, "revision", 1, "0")

	httpColumns := loadSQLiteTableInfo(t, store.db, "rules")
	assertSQLiteColumnContract(t, httpColumns, "egress_profile_id", 0, "")

	l4Columns := loadSQLiteTableInfo(t, store.db, "l4_rules")
	assertSQLiteColumnContract(t, l4Columns, "egress_profile_id", 0, "")
}

func TestIntegrationStoreSaveListEgressProfilesOrdersAndReplacesFullSet(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	if err := store.SaveEgressProfiles(t.Context(), []EgressProfileRow{
		{ID: 3, Name: "third", Type: "socks", Enabled: true},
		{ID: 1, Name: "first", Type: "http", Enabled: true},
		{ID: 2, Name: "second", Type: "direct", Enabled: true},
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
		{ID: 2, Name: "second only", Type: "direct", Enabled: true},
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

func TestIntegrationBootstrapSchemaMigratesLegacyL4ProxyEgressToProfile(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	store := newStorageMigrationTestStore(t, "legacy-egress-agent")
	db := store.db
	if err := db.WithContext(t.Context()).Exec(`ALTER TABLE l4_rules ADD COLUMN proxy_egress_mode TEXT NOT NULL DEFAULT ''`).Error; err != nil {
		t.Fatalf("add legacy proxy_egress_mode column error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`ALTER TABLE l4_rules ADD COLUMN proxy_egress_url TEXT NOT NULL DEFAULT ''`).Error; err != nil {
		t.Fatalf("add legacy proxy_egress_url column error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO agents (id, name, desired_revision, current_revision) VALUES ('legacy-egress-agent', 'legacy-egress-agent', 4, 4)`).Error; err != nil {
		t.Fatalf("seed legacy agent error = %v", err)
	}
	if err := db.WithContext(t.Context()).Exec(`INSERT INTO l4_rules (
		id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
		load_balancing, tuning, relay_chain, relay_layers, relay_obfs, listen_mode, proxy_egress_mode,
		proxy_egress_url, enabled, tags, revision
	) VALUES (72, 'legacy-egress-agent', 'legacy proxy egress', 'tcp', '0.0.0.0', 25565, '', 0, '[]',
		NULL, NULL, '[]', '[]', 0, 'proxy', 'proxy', 'socks5://127.0.0.1:1080', 1, '[]', 4)`).Error; err != nil {
		t.Fatalf("seed legacy l4 rule error = %v", err)
	}

	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("BootstrapSQLiteSchema() migration error = %v", err)
	}
	if err := BootstrapSQLiteSchema(t.Context(), db); err != nil {
		t.Fatalf("second BootstrapSQLiteSchema() migration error = %v", err)
	}

	rules, err := store.ListL4Rules(t.Context(), "legacy-egress-agent")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	profiles, err := store.ListEgressProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListEgressProfiles() error = %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("egress profiles = %+v, want one migrated profile", profiles)
	}
	if profiles[0].Name != "legacy proxy egress" || profiles[0].Type != "socks" || profiles[0].ProxyURL != "socks5://127.0.0.1:1080" || !profiles[0].Enabled || profiles[0].Revision <= 4 {
		t.Fatalf("migrated egress profile = %+v", profiles[0])
	}
	if len(rules) != 1 || rules[0].EgressProfileID == nil || *rules[0].EgressProfileID != profiles[0].ID {
		t.Fatalf("migrated l4 rule = %+v, profiles=%+v", rules, profiles)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].DesiredRevision <= 4 {
		t.Fatalf("agents after legacy egress migration = %+v, want desired revision above applied revision 4", agents)
	}
}

func TestIntegrationSnapshotL4RulesUsesCanonicalBackendsAndRelayLayersOnly(t *testing.T) {
	t.Parallel()
	rules := SnapshotL4Rules([]L4RuleRow{{
		ID:                1,
		AgentID:           "local",
		Name:              "canonical-l4",
		Protocol:          "tcp",
		ListenHost:        "127.0.0.1",
		ListenPort:        9443,
		UpstreamHost:      "legacy.example.com",
		UpstreamPort:      9444,
		BackendsJSON:      `[{"host":"canonical.example.com","port":9445}]`,
		LoadBalancingJSON: `{}`,
		TuningJSON:        `{}`,
		RelayChainJSON:    `[101]`,
		RelayLayersJSON:   `[[201,202]]`,
		ListenMode:        "proxy",
		Enabled:           true,
		Revision:          3,
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
	if rules[0].ListenMode != "proxy" {
		t.Fatalf("ListenMode = %q, want proxy", rules[0].ListenMode)
	}
}
