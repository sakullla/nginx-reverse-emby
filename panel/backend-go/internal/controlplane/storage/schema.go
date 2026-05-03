package storage

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
		&ManagedCertificateRow{},
		&LocalAgentStateRow{},
		&VersionPolicyRow{},
		&MetaRow{},
	); err != nil {
		return err
	}

	if options.SQLiteLegacyMigrations {
		if err := bootstrapSQLiteLegacySchema(ctx, db); err != nil {
			return err
		}
	}

	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&LocalAgentStateRow{
			ID:              1,
			LastApplyStatus: "success",
		}).Error
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
		{column: "proxy_entry_auth", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_entry_auth TEXT NOT NULL DEFAULT '{}'`},
		{column: "proxy_egress_mode", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_mode TEXT NOT NULL DEFAULT ''`},
		{column: "proxy_egress_url", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_url TEXT NOT NULL DEFAULT ''`},
	}
	for _, migration := range l4ColumnMigrations {
		if tx.Migrator().HasColumn(&L4RuleRow{}, migration.column) {
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
		`UPDATE l4_rules SET relay_layers = '[]' WHERE relay_layers IS NULL OR trim(relay_layers) = ''`,
		`UPDATE l4_rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
		`UPDATE l4_rules SET listen_mode = 'tcp' WHERE listen_mode IS NULL OR trim(listen_mode) = ''`,
		`UPDATE l4_rules SET proxy_entry_auth = '{}' WHERE proxy_entry_auth IS NULL OR trim(proxy_entry_auth) = ''`,
		`UPDATE l4_rules SET proxy_egress_mode = '' WHERE proxy_egress_mode IS NULL`,
		`UPDATE l4_rules SET proxy_egress_url = '' WHERE proxy_egress_url IS NULL`,
		`UPDATE agents SET desired_version = '' WHERE desired_version IS NULL`,
		`UPDATE agents SET platform = '' WHERE platform IS NULL`,
		`UPDATE agents SET runtime_package_version = '' WHERE runtime_package_version IS NULL`,
		`UPDATE agents SET runtime_package_platform = '' WHERE runtime_package_platform IS NULL`,
		`UPDATE agents SET runtime_package_arch = '' WHERE runtime_package_arch IS NULL`,
		`UPDATE agents SET runtime_package_sha256 = '' WHERE runtime_package_sha256 IS NULL`,
		`UPDATE agents SET outbound_proxy_url = '' WHERE outbound_proxy_url IS NULL`,
		`UPDATE agents SET traffic_stats_interval = '' WHERE traffic_stats_interval IS NULL`,
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
