package storage

import (
	"context"
	"encoding/json"
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
	}

	if err := tx.AutoMigrate(
		&AgentRow{},
		&HTTPRuleRow{},
		&L4RuleRow{},
		&RelayListenerRow{},
		&WireGuardProfileRow{},
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
		{column: "wireguard_inbound_mode", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_inbound_mode TEXT NOT NULL DEFAULT 'address'`},
		{column: "wireguard_listen_host", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_listen_host TEXT NOT NULL DEFAULT ''`},
		{column: "proxy_entry_auth", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_entry_auth TEXT NOT NULL DEFAULT '{}'`},
		{column: "proxy_egress_mode", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_mode TEXT NOT NULL DEFAULT ''`},
		{column: "proxy_egress_url", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_url TEXT NOT NULL DEFAULT ''`},
		{column: "wireguard_egress_uri", sql: `ALTER TABLE l4_rules ADD COLUMN wireguard_egress_uri TEXT NOT NULL DEFAULT ''`},
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
		`UPDATE l4_rules SET proxy_egress_mode = '' WHERE proxy_egress_mode IS NULL`,
		`UPDATE l4_rules SET proxy_egress_url = '' WHERE proxy_egress_url IS NULL`,
		`UPDATE l4_rules SET wireguard_egress_uri = '' WHERE wireguard_egress_uri IS NULL`,
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
	ID              int    `gorm:"column:id"`
	AgentID         string `gorm:"column:agent_id"`
	UpstreamHost    string `gorm:"column:upstream_host"`
	UpstreamPort    int    `gorm:"column:upstream_port"`
	BackendsJSON    string `gorm:"column:backends"`
	RelayChainJSON  string `gorm:"column:relay_chain"`
	RelayLayersJSON string `gorm:"column:relay_layers"`
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

	var rows []legacyL4RuleMigrationRow
	if err := tx.Model(&L4RuleRow{}).
		Select("id", "agent_id", "upstream_host", "upstream_port", "backends", "relay_chain", "relay_layers").
		Find(&rows).Error; err != nil {
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
		if len(updates) == 0 {
			continue
		}
		if err := tx.Model(&L4RuleRow{}).
			Where("id = ? AND agent_id = ?", row.ID, row.AgentID).
			Updates(updates).Error; err != nil {
			return err
		}
	}

	return nil
}

func canonicalJSONIsEmptyArray(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", "[]":
		return true
	default:
		return false
	}
}
