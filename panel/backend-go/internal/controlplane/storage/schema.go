package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const wireGuardAgentLocalSnapshotMarkerKey = "migration.wireguard_snapshots_agent_local.v1"

type SchemaOptions struct {
	TrafficStatsEnabled    bool
	SQLiteLegacyMigrations bool
}

func SchemaOptionsForDriver(driver string, trafficStatsEnabled bool) SchemaOptions {
	driver = strings.ToLower(strings.TrimSpace(driver))
	return SchemaOptions{
		TrafficStatsEnabled:    trafficStatsEnabled,
		SQLiteLegacyMigrations: driver == "" || driver == "sqlite",
	}
}

func BootstrapSchema(ctx context.Context, db *gorm.DB, options SchemaOptions) error {
	tx := db.WithContext(ctx)

	if options.SQLiteLegacyMigrations {
		if err := cleanupSQLiteLegacyLocalAgentState(ctx, db); err != nil {
			return err
		}
		if err := createSQLiteEgressProfilesTable(ctx, db); err != nil {
			return err
		}
	}

	if err := tx.AutoMigrate(
		&AgentRow{},
		&HTTPRuleRow{},
		&L4RuleRow{},
		&RelayListenerRow{},
		&WireGuardProfileRow{},
		&EgressProfileRow{},
		&WireGuardClientRow{},
		&ManagedCertificateRow{},
		&LocalAgentStateRow{},
		&VersionPolicyRow{},
		&MetaRow{},
	); err != nil {
		return err
	}

	if options.TrafficStatsEnabled {
		if err := tx.AutoMigrate(
			&AgentTrafficPolicyRow{},
			&AgentTrafficBaselineRow{},
			&AgentTrafficRawCursorRow{},
			&AgentTrafficHourlyBucketRow{},
			&AgentTrafficDailySummaryRow{},
			&AgentTrafficMonthlySummaryRow{},
			&AgentTrafficEventRow{},
		); err != nil {
			return err
		}
	}

	if options.SQLiteLegacyMigrations {
		if err := bootstrapSQLiteLegacySchema(ctx, db); err != nil {
			return err
		}
	}

	if err := migrateLegacyRuleCanonicalFields(ctx, db); err != nil {
		return err
	}

	if err := markWireGuardSnapshotsAgentLocal(ctx, db); err != nil {
		return err
	}

	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&LocalAgentStateRow{
			ID:              1,
			LastApplyStatus: "success",
		}).Error
}

func markWireGuardSnapshotsAgentLocal(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if !tx.Migrator().HasTable(&MetaRow{}) || !tx.Migrator().HasTable(&AgentRow{}) {
		return nil
	}

	return tx.Transaction(func(tx *gorm.DB) error {
		marker := MetaRow{
			Key:   wireGuardAgentLocalSnapshotMarkerKey,
			Value: "applied",
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		var agents []AgentRow
		if err := tx.Find(&agents).Error; err != nil {
			return err
		}
		for _, row := range agents {
			normalizeAgentRow(&row)
			if strings.TrimSpace(row.ID) == "" {
				continue
			}
			nextRevision := maxInt(row.DesiredRevision, row.CurrentRevision) + 1
			if row.DesiredRevision >= nextRevision {
				continue
			}
			if err := tx.Model(&AgentRow{}).
				Where("id = ?", row.ID).
				Update("desired_revision", nextRevision).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func cleanupSQLiteLegacyLocalAgentState(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if tx.Migrator().HasTable(&LocalAgentStateRow{}) {
		if err := tx.Exec(`DELETE FROM local_agent_state WHERE id <> 1`).Error; err != nil {
			return err
		}
	}
	return nil
}

func createSQLiteEgressProfilesTable(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if tx.Migrator().HasTable(&EgressProfileRow{}) {
		return nil
	}
	return tx.Exec(`CREATE TABLE egress_profiles (
		id integer NOT NULL,
		name text NOT NULL,
		type text NOT NULL,
		proxy_url text NOT NULL DEFAULT "",
		wireguard_config_json text NOT NULL DEFAULT "",
		enabled integer NOT NULL DEFAULT 1,
		description text NOT NULL DEFAULT "",
		revision integer NOT NULL DEFAULT 0,
		PRIMARY KEY (id)
	)`).Error
}

func bootstrapSQLiteLegacySchema(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)

	requiredIndexes := []struct {
		model any
		name  string
	}{
		{model: &HTTPRuleRow{}, name: "idx_rules_agent"},
		{model: &L4RuleRow{}, name: "idx_l4_rules_agent"},
		{model: &RelayListenerRow{}, name: "idx_relay_listeners_agent"},
		{model: &WireGuardProfileRow{}, name: "idx_wireguard_profiles_agent"},
		{model: &WireGuardClientRow{}, name: "idx_wireguard_clients_agent_profile"},
	}
	for _, index := range requiredIndexes {
		if tx.Migrator().HasIndex(index.model, index.name) {
			continue
		}
		if err := tx.Migrator().CreateIndex(index.model, index.name); err != nil {
			return err
		}
	}

	relayListenerColumnMigrations := []struct {
		column string
		sql    string
	}{
		{column: "transport_mode", sql: `ALTER TABLE relay_listeners ADD COLUMN transport_mode TEXT NOT NULL DEFAULT 'tls_tcp'`},
		{column: "wireguard_profile_id", sql: `ALTER TABLE relay_listeners ADD COLUMN wireguard_profile_id INTEGER`},
		{column: "allow_transport_fallback", sql: `ALTER TABLE relay_listeners ADD COLUMN allow_transport_fallback INTEGER NOT NULL DEFAULT 1`},
		{column: "obfs_mode", sql: `ALTER TABLE relay_listeners ADD COLUMN obfs_mode TEXT NOT NULL DEFAULT 'off'`},
	}
	for _, migration := range relayListenerColumnMigrations {
		if tx.Migrator().HasColumn(&RelayListenerRow{}, migration.column) {
			continue
		}
		if err := tx.Exec(migration.sql).Error; err != nil {
			return err
		}
	}

	agentColumnMigrations := []struct {
		column string
		sql    string
	}{
		{column: "outbound_proxy_url", sql: `ALTER TABLE agents ADD COLUMN outbound_proxy_url TEXT NOT NULL DEFAULT ''`},
		{column: "traffic_stats_interval", sql: `ALTER TABLE agents ADD COLUMN traffic_stats_interval TEXT NOT NULL DEFAULT ''`},
		{column: "traffic_blocked", sql: `ALTER TABLE agents ADD COLUMN traffic_blocked INTEGER NOT NULL DEFAULT 0`},
		{column: "traffic_block_reason", sql: `ALTER TABLE agents ADD COLUMN traffic_block_reason TEXT NOT NULL DEFAULT ''`},
	}
	for _, migration := range agentColumnMigrations {
		if tx.Migrator().HasColumn(&AgentRow{}, migration.column) {
			continue
		}
		if err := tx.Exec(migration.sql).Error; err != nil {
			return err
		}
	}

	l4ColumnMigrations := []struct {
		column string
		sql    string
	}{
		{column: "listen_mode", sql: `ALTER TABLE l4_rules ADD COLUMN listen_mode TEXT NOT NULL DEFAULT 'tcp'`},
		{column: "wireguard_profile_id", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_profile_id INTEGER`},
		{column: "egress_profile_id", sql: `ALTER TABLE l4_rules ADD COLUMN egress_profile_id INTEGER`},
		{column: "wireguard_inbound_mode", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_inbound_mode TEXT NOT NULL DEFAULT 'address'`},
		{column: "wireguard_listen_host", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_listen_host TEXT NOT NULL DEFAULT ''`},
		{column: "proxy_entry_auth", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_entry_auth TEXT NOT NULL DEFAULT '{}'`},
	}
	for _, migration := range l4ColumnMigrations {
		if tx.Migrator().HasColumn(&L4RuleRow{}, migration.column) {
			continue
		}
		if err := tx.Exec(migration.sql).Error; err != nil {
			return err
		}
	}

	wireGuardProfileColumnMigrations := []struct {
		column string
		sql    string
	}{
		{column: "public_endpoint", sql: `ALTER TABLE wireguard_profiles ADD COLUMN public_endpoint TEXT NOT NULL DEFAULT ''`},
		{column: "bind_addresses", sql: `ALTER TABLE wireguard_profiles ADD COLUMN bind_addresses TEXT NOT NULL DEFAULT '[]'`},
	}
	for _, migration := range wireGuardProfileColumnMigrations {
		if tx.Migrator().HasColumn(&WireGuardProfileRow{}, migration.column) {
			continue
		}
		if err := tx.Exec(migration.sql).Error; err != nil {
			return err
		}
	}

	ruleColumnMigrations := []struct {
		model  any
		column string
		sql    string
	}{
		{model: &HTTPRuleRow{}, column: "relay_layers", sql: `ALTER TABLE rules ADD COLUMN relay_layers TEXT NOT NULL DEFAULT '[]'`},
		{model: &HTTPRuleRow{}, column: "wireguard_entry_enabled", sql: `ALTER TABLE rules ADD COLUMN wireguard_entry_enabled INTEGER NOT NULL DEFAULT 0`},
		{model: &HTTPRuleRow{}, column: "wireguard_profile_id", sql: `ALTER TABLE rules ADD COLUMN wireguard_profile_id INTEGER`},
		{model: &HTTPRuleRow{}, column: "egress_profile_id", sql: `ALTER TABLE rules ADD COLUMN egress_profile_id INTEGER`},
		{model: &HTTPRuleRow{}, column: "wireguard_entry_listen_host", sql: `ALTER TABLE rules ADD COLUMN wireguard_entry_listen_host TEXT NOT NULL DEFAULT ''`},
		{model: &HTTPRuleRow{}, column: "wireguard_entry_listen_port", sql: `ALTER TABLE rules ADD COLUMN wireguard_entry_listen_port INTEGER NOT NULL DEFAULT 0`},
		{model: &L4RuleRow{}, column: "relay_layers", sql: `ALTER TABLE l4_rules ADD COLUMN relay_layers TEXT NOT NULL DEFAULT '[]'`},
	}
	for _, migration := range ruleColumnMigrations {
		if tx.Migrator().HasColumn(migration.model, migration.column) {
			continue
		}
		if err := tx.Exec(migration.sql).Error; err != nil {
			return err
		}
	}

	normalizationStatements := []string{
		`UPDATE rules SET pass_proxy_headers = 1 WHERE pass_proxy_headers IS NULL`,
		`UPDATE rules SET user_agent = '' WHERE user_agent IS NULL`,
		`UPDATE rules SET custom_headers = '[]' WHERE custom_headers IS NULL OR trim(custom_headers) = ''`,
		`UPDATE rules SET relay_chain = '[]' WHERE relay_chain IS NULL OR trim(relay_chain) = ''`,
		`UPDATE rules SET relay_layers = '[]' WHERE relay_layers IS NULL OR trim(relay_layers) = ''`,
		`UPDATE rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
		`UPDATE rules SET wireguard_entry_enabled = 0 WHERE wireguard_entry_enabled IS NULL`,
		`UPDATE rules SET wireguard_entry_listen_host = '' WHERE wireguard_entry_listen_host IS NULL`,
		`UPDATE rules SET wireguard_entry_listen_port = 0 WHERE wireguard_entry_listen_port IS NULL`,
		`UPDATE l4_rules SET relay_layers = '[]' WHERE relay_layers IS NULL OR trim(relay_layers) = ''`,
		`UPDATE l4_rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
		`UPDATE l4_rules SET listen_mode = 'tcp' WHERE listen_mode IS NULL OR trim(listen_mode) = ''`,
		`UPDATE l4_rules SET wireguard_inbound_mode = 'address' WHERE wireguard_inbound_mode IS NULL OR trim(wireguard_inbound_mode) = ''`,
		`UPDATE l4_rules SET wireguard_listen_host = '' WHERE wireguard_listen_host IS NULL`,
		`UPDATE l4_rules SET proxy_entry_auth = '{}' WHERE proxy_entry_auth IS NULL OR trim(proxy_entry_auth) = ''`,
		`UPDATE agents SET desired_version = '' WHERE desired_version IS NULL`,
		`UPDATE agents SET platform = '' WHERE platform IS NULL`,
		`UPDATE agents SET runtime_package_version = '' WHERE runtime_package_version IS NULL`,
		`UPDATE agents SET runtime_package_platform = '' WHERE runtime_package_platform IS NULL`,
		`UPDATE agents SET runtime_package_arch = '' WHERE runtime_package_arch IS NULL`,
		`UPDATE agents SET runtime_package_sha256 = '' WHERE runtime_package_sha256 IS NULL`,
		`UPDATE agents SET outbound_proxy_url = '' WHERE outbound_proxy_url IS NULL`,
		`UPDATE agents SET traffic_stats_interval = '' WHERE traffic_stats_interval IS NULL`,
		`UPDATE agents SET traffic_blocked = 0 WHERE traffic_blocked IS NULL`,
		`UPDATE agents SET traffic_block_reason = '' WHERE traffic_block_reason IS NULL`,
		`UPDATE local_agent_state SET desired_version = '' WHERE desired_version IS NULL`,
		`UPDATE local_agent_state SET last_apply_status = 'success' WHERE last_apply_status IS NULL OR trim(last_apply_status) = ''`,
		`UPDATE local_agent_state SET last_apply_message = '' WHERE last_apply_message IS NULL`,
		`UPDATE managed_certificates SET usage = 'https' WHERE usage IS NULL OR trim(usage) = ''`,
		`UPDATE managed_certificates SET certificate_type = 'acme' WHERE certificate_type IS NULL OR trim(certificate_type) = ''`,
		`UPDATE managed_certificates SET self_signed = 0 WHERE self_signed IS NULL`,
		`UPDATE relay_listeners
			SET bind_hosts = json_array(COALESCE(NULLIF(trim(listen_host), ''), '0.0.0.0'))
			WHERE bind_hosts IS NULL OR trim(bind_hosts) = '' OR trim(bind_hosts) = '[]'`,
		`UPDATE relay_listeners
			SET bind_hosts = json_array(COALESCE(NULLIF(trim(listen_host), ''), '0.0.0.0'))
			WHERE bind_hosts IS NOT NULL AND trim(bind_hosts) <> '' AND json_valid(bind_hosts) = 0`,
		`UPDATE relay_listeners
			SET public_host = COALESCE(
				NULLIF(trim(public_host), ''),
				CASE
					WHEN json_valid(bind_hosts) = 1 AND json_type(bind_hosts) = 'array' THEN NULLIF(trim(json_extract(bind_hosts, '$[0]')), '')
					ELSE NULL
				END,
				COALESCE(NULLIF(trim(listen_host), ''), '0.0.0.0')
			)
			WHERE public_host IS NULL OR trim(public_host) = ''`,
		`UPDATE relay_listeners
			SET public_port = COALESCE(public_port, listen_port)
			WHERE public_port IS NULL OR public_port <= 0`,
		`UPDATE relay_listeners SET transport_mode = 'tls_tcp' WHERE transport_mode IS NULL OR trim(transport_mode) = ''`,
		`UPDATE relay_listeners SET allow_transport_fallback = 1 WHERE allow_transport_fallback IS NULL`,
		`UPDATE relay_listeners SET obfs_mode = 'off' WHERE obfs_mode IS NULL OR trim(obfs_mode) = ''`,
		`UPDATE wireguard_profiles SET public_endpoint = '' WHERE public_endpoint IS NULL`,
		`UPDATE wireguard_profiles SET bind_addresses = '[]' WHERE bind_addresses IS NULL OR trim(bind_addresses) = ''`,
	}
	for _, stmt := range normalizationStatements {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}

	if err := tx.Where("id <> ?", 1).Delete(&LocalAgentStateRow{}).Error; err != nil {
		return err
	}

	return nil
}

func BootstrapSQLiteSchema(ctx context.Context, db *gorm.DB) error {
	return BootstrapSchema(ctx, db, SchemaOptionsForDriver("sqlite", true))
}

type legacyHTTPRuleMigrationRow struct {
	ID              int    `gorm:"column:id"`
	AgentID         string `gorm:"column:agent_id"`
	BackendURL      string `gorm:"column:backend_url"`
	BackendsJSON    string `gorm:"column:backends"`
	RelayChainJSON  string `gorm:"column:relay_chain"`
	RelayLayersJSON string `gorm:"column:relay_layers"`
}

type legacyL4RuleMigrationRow struct {
	ID                 int    `gorm:"column:id"`
	AgentID            string `gorm:"column:agent_id"`
	Name               string `gorm:"column:name"`
	UpstreamHost       string `gorm:"column:upstream_host"`
	UpstreamPort       int    `gorm:"column:upstream_port"`
	BackendsJSON       string `gorm:"column:backends"`
	RelayChainJSON     string `gorm:"column:relay_chain"`
	RelayLayersJSON    string `gorm:"column:relay_layers"`
	ProxyEgressMode    string `gorm:"column:proxy_egress_mode"`
	ProxyEgressURL     string `gorm:"column:proxy_egress_url"`
	WireGuardEgressURI string `gorm:"column:wireguard_egress_uri"`
	EgressProfileID    *int   `gorm:"column:egress_profile_id"`
	Revision           int    `gorm:"column:revision"`
}

func migrateLegacyRuleCanonicalFields(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if err := migrateLegacyHTTPRuleCanonicalFields(tx); err != nil {
		return err
	}
	if err := migrateLegacyL4RuleCanonicalFields(tx); err != nil {
		return err
	}
	return nil
}

func migrateLegacyHTTPRuleCanonicalFields(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn(&HTTPRuleRow{}, "backend_url") || !tx.Migrator().HasColumn(&HTTPRuleRow{}, "relay_chain") {
		return nil
	}

	var rows []legacyHTTPRuleMigrationRow
	if err := tx.Model(&HTTPRuleRow{}).
		Select("id", "agent_id", "backend_url", "backends", "relay_chain", "relay_layers").
		Find(&rows).Error; err != nil {
		return err
	}

	for _, row := range rows {
		updates := map[string]any{}
		if canonicalJSONIsEmptyArray(row.BackendsJSON) {
			if backendURL := strings.TrimSpace(row.BackendURL); backendURL != "" {
				backendsJSON, err := json.Marshal([]HTTPBackend{{URL: backendURL}})
				if err != nil {
					return err
				}
				updates["backends"] = string(backendsJSON)
			}
		}
		if canonicalJSONIsEmptyArray(row.RelayLayersJSON) {
			relayChain := parseIntSlice(row.RelayChainJSON)
			if len(relayChain) > 0 {
				relayLayers := make([][]int, 0, len(relayChain))
				for _, id := range relayChain {
					relayLayers = append(relayLayers, []int{id})
				}
				relayLayersJSON, err := json.Marshal(relayLayers)
				if err != nil {
					return err
				}
				updates["relay_layers"] = string(relayLayersJSON)
			}
		}
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&HTTPRuleRow{}).
			Where("id = ? AND agent_id = ?", row.ID, row.AgentID).
			Updates(updates).Error; err != nil {
			return err
		}
	}

	return nil
}

func migrateLegacyL4RuleCanonicalFields(tx *gorm.DB) error {
	if !tx.Migrator().HasColumn(&L4RuleRow{}, "upstream_host") || !tx.Migrator().HasColumn(&L4RuleRow{}, "upstream_port") || !tx.Migrator().HasColumn(&L4RuleRow{}, "relay_chain") {
		return nil
	}

	legacyEgressColumns := tx.Migrator().HasColumn(&L4RuleRow{}, "proxy_egress_mode") &&
		tx.Migrator().HasColumn(&L4RuleRow{}, "proxy_egress_url") &&
		tx.Migrator().HasColumn(&L4RuleRow{}, "wireguard_egress_uri") &&
		tx.Migrator().HasColumn(&L4RuleRow{}, "egress_profile_id")
	selectColumns := "id, agent_id, name, upstream_host, upstream_port, backends, relay_chain, relay_layers, revision"
	if legacyEgressColumns {
		selectColumns += ", proxy_egress_mode, proxy_egress_url, wireguard_egress_uri, egress_profile_id"
	}
	var rows []legacyL4RuleMigrationRow
	if err := tx.Model(&L4RuleRow{}).
		Select(selectColumns).
		Find(&rows).Error; err != nil {
		return err
	}
	nextEgressProfileID, err := nextLegacyEgressProfileID(tx)
	if err != nil {
		return err
	}

	for _, row := range rows {
		updates := map[string]any{}
		if canonicalJSONIsEmptyArray(row.BackendsJSON) {
			host := strings.TrimSpace(row.UpstreamHost)
			if host != "" && row.UpstreamPort >= 1 && row.UpstreamPort <= 65535 {
				backendsJSON, err := json.Marshal([]L4Backend{{Host: host, Port: row.UpstreamPort}})
				if err != nil {
					return err
				}
				updates["backends"] = string(backendsJSON)
			}
		}
		if canonicalJSONIsEmptyArray(row.RelayLayersJSON) {
			relayChain := parseIntSlice(row.RelayChainJSON)
			if len(relayChain) > 0 {
				relayLayers := make([][]int, 0, len(relayChain))
				for _, id := range relayChain {
					relayLayers = append(relayLayers, []int{id})
				}
				relayLayersJSON, err := json.Marshal(relayLayers)
				if err != nil {
					return err
				}
				updates["relay_layers"] = string(relayLayersJSON)
			}
		}
		if legacyEgressColumns && row.EgressProfileID == nil {
			profile, ok, err := legacyL4EgressProfileFromRow(row, nextEgressProfileID)
			if err != nil {
				return err
			}
			if ok {
				if err := tx.Create(&profile).Error; err != nil {
					return err
				}
				updates["egress_profile_id"] = profile.ID
				updates["revision"] = int(profile.Revision)
				nextEgressProfileID++
			}
		}
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&L4RuleRow{}).
			Where("id = ? AND agent_id = ?", row.ID, row.AgentID).
			Updates(updates).Error; err != nil {
			return err
		}
		if revision, ok := updates["revision"].(int); ok {
			if err := bumpLegacyMigrationAgentDesiredRevision(tx, row.AgentID, revision); err != nil {
				return err
			}
		}
	}

	return nil
}

func bumpLegacyMigrationAgentDesiredRevision(tx *gorm.DB, agentID string, revision int) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || revision <= 0 {
		return nil
	}
	return tx.Model(&AgentRow{}).
		Where("id = ? AND desired_revision < ?", agentID, revision).
		Update("desired_revision", revision).Error
}

func nextLegacyEgressProfileID(tx *gorm.DB) (int, error) {
	if !tx.Migrator().HasTable(&EgressProfileRow{}) {
		return 1, nil
	}
	var maxID int
	if err := tx.Model(&EgressProfileRow{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return 0, err
	}
	return maxID + 1, nil
}

func legacyL4EgressProfileFromRow(row legacyL4RuleMigrationRow, id int) (EgressProfileRow, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(row.ProxyEgressMode))
	switch mode {
	case "proxy":
		proxyURL := strings.TrimSpace(row.ProxyEgressURL)
		if proxyURL == "" {
			return EgressProfileRow{}, false, nil
		}
		profileType, err := legacyEgressProfileProxyType(proxyURL)
		if err != nil {
			return EgressProfileRow{}, false, err
		}
		return EgressProfileRow{
			ID:          id,
			Name:        legacyEgressProfileName(row),
			Type:        profileType,
			ProxyURL:    proxyURL,
			Enabled:     true,
			Description: fmt.Sprintf("Migrated from legacy L4 rule %d", row.ID),
			Revision:    int64(legacyEgressProfileRevision(row)),
		}, true, nil
	case "wireguard":
		configJSON, err := legacyWireGuardEgressConfigJSON(row.WireGuardEgressURI)
		if err != nil {
			return EgressProfileRow{}, false, err
		}
		if configJSON == "" {
			return EgressProfileRow{}, false, nil
		}
		return EgressProfileRow{
			ID:                  id,
			Name:                legacyEgressProfileName(row),
			Type:                "wireguard",
			WireGuardConfigJSON: configJSON,
			Enabled:             true,
			Description:         fmt.Sprintf("Migrated from legacy L4 rule %d", row.ID),
			Revision:            int64(legacyEgressProfileRevision(row)),
		}, true, nil
	default:
		return EgressProfileRow{}, false, nil
	}
}

func legacyEgressProfileRevision(row legacyL4RuleMigrationRow) int {
	if row.Revision > 0 {
		return row.Revision + 1
	}
	return 1
}

func legacyEgressProfileName(row legacyL4RuleMigrationRow) string {
	name := strings.TrimSpace(row.Name)
	if name == "" {
		name = fmt.Sprintf("L4 rule %d", row.ID)
	}
	if strings.HasSuffix(strings.ToLower(name), " egress") {
		return name
	}
	return name + " egress"
}

func legacyEgressProfileProxyType(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http":
		return "http", nil
	case "socks", "socks5", "socks5h":
		return "socks", nil
	default:
		return "", fmt.Errorf("unsupported legacy proxy egress URL scheme %q", parsed.Scheme)
	}
}

type legacyWireGuardURI struct {
	Name         string
	PrivateKey   string
	Endpoint     string
	PublicKey    string
	PresharedKey string
	Addresses    []string
	AllowedIPs   []string
	DNS          []string
	MTU          int
}

func legacyWireGuardEgressConfigJSON(raw string) (string, error) {
	parsed, err := parseLegacyWireGuardURI(raw)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.PrivateKey) == "" {
		return "", nil
	}
	if strings.TrimSpace(parsed.Endpoint) == "" {
		return "", fmt.Errorf("legacy wireguard egress URI endpoint host and port are required")
	}
	if strings.TrimSpace(parsed.PublicKey) == "" {
		return "", fmt.Errorf("legacy wireguard egress URI publickey is required")
	}
	if len(parsed.Addresses) == 0 {
		return "", fmt.Errorf("legacy wireguard egress URI address is required")
	}
	type peer struct {
		Name         string   `json:"name,omitempty"`
		PublicKey    string   `json:"public_key"`
		PresharedKey string   `json:"preshared_key,omitempty"`
		Endpoint     string   `json:"endpoint"`
		AllowedIPs   []string `json:"allowed_ips"`
	}
	config := struct {
		PrivateKey string   `json:"private_key"`
		Addresses  []string `json:"addresses"`
		Peers      []peer   `json:"peers"`
		DNS        []string `json:"dns,omitempty"`
		MTU        int      `json:"mtu,omitempty"`
	}{
		PrivateKey: parsed.PrivateKey,
		Addresses:  parsed.Addresses,
		Peers: []peer{{
			Name:         parsed.Name,
			PublicKey:    parsed.PublicKey,
			PresharedKey: parsed.PresharedKey,
			Endpoint:     parsed.Endpoint,
			AllowedIPs:   parsed.AllowedIPs,
		}},
		DNS: parsed.DNS,
		MTU: parsed.MTU,
	}
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseLegacyWireGuardURI(raw string) (legacyWireGuardURI, error) {
	uri := strings.TrimSpace(raw)
	if uri == "" {
		return legacyWireGuardURI{}, nil
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return legacyWireGuardURI{}, err
	}
	if !strings.EqualFold(parsed.Scheme, "wireguard") {
		return legacyWireGuardURI{}, fmt.Errorf("legacy wireguard egress URI scheme must be wireguard")
	}
	if parsed.User == nil {
		return legacyWireGuardURI{}, nil
	}
	query := parseLegacyWireGuardURIQuery(parsed.RawQuery)
	endpoint := ""
	if host, port := strings.TrimSpace(parsed.Hostname()), strings.TrimSpace(parsed.Port()); host != "" && port != "" {
		endpoint = net.JoinHostPort(host, port)
	}
	allowedIPs := splitLegacyWireGuardURIList(firstLegacyWireGuardURIValue(query["allowedips"], query["allowed-ips"]))
	if len(allowedIPs) == 0 {
		allowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	mtu := 0
	if rawMTU := strings.TrimSpace(query["mtu"]); rawMTU != "" {
		if value, err := strconv.Atoi(rawMTU); err == nil && value >= 0 {
			mtu = value
		}
	}
	return legacyWireGuardURI{
		Name:         strings.TrimSpace(parsed.Fragment),
		PrivateKey:   strings.TrimSpace(parsed.User.Username()),
		Endpoint:     endpoint,
		PublicKey:    strings.TrimSpace(query["publickey"]),
		PresharedKey: firstLegacyWireGuardURIValue(query["preshared-key"], query["psk"]),
		Addresses:    splitLegacyWireGuardURIList(query["address"]),
		AllowedIPs:   allowedIPs,
		DNS:          splitLegacyWireGuardURIList(query["dns"]),
		MTU:          mtu,
	}, nil
}

func parseLegacyWireGuardURIQuery(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			continue
		}
		decodedValue, err := url.PathUnescape(value)
		if err != nil {
			decodedValue = value
		}
		out[strings.ToLower(strings.TrimSpace(decodedKey))] = decodedValue
	}
	return out
}

func firstLegacyWireGuardURIValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func splitLegacyWireGuardURIList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func canonicalJSONIsEmptyArray(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", "[]":
		return true
	default:
		return false
	}
}
