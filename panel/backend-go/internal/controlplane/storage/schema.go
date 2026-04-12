package storage

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func BootstrapSQLiteSchema(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)

	if tx.Migrator().HasTable(&LocalAgentStateRow{}) {
		if err := tx.Exec(`DELETE FROM local_agent_state WHERE id <> 1`).Error; err != nil {
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

	normalizationStatements := []string{
		`UPDATE rules SET pass_proxy_headers = 1 WHERE pass_proxy_headers IS NULL`,
		`UPDATE rules SET user_agent = '' WHERE user_agent IS NULL`,
		`UPDATE rules SET custom_headers = '[]' WHERE custom_headers IS NULL OR trim(custom_headers) = ''`,
		`UPDATE rules SET relay_chain = '[]' WHERE relay_chain IS NULL OR trim(relay_chain) = ''`,
		`UPDATE rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
		`UPDATE l4_rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
		`UPDATE agents SET desired_version = '' WHERE desired_version IS NULL`,
		`UPDATE agents SET platform = '' WHERE platform IS NULL`,
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
	}
	for _, stmt := range normalizationStatements {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}

	if err := tx.Where("id <> ?", 1).Delete(&LocalAgentStateRow{}).Error; err != nil {
		return err
	}

	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&LocalAgentStateRow{
			ID:              1,
			LastApplyStatus: "success",
		}).Error
}
