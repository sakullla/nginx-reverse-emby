package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	trafficAgentIndexBackfillMarkerKey = "migration.agent_traffic_agents_backfill.v1"
	agentDefaultNormalizationMarkerKey = "migration.agent_default_normalization.v1"
	crossGroupCertificateGroupID       = "system:cross-group-certificate"
	legacyPKIIdentityOwnerIndex        = "idx_pki_identity_owner"
	pkiIdentityActiveOwnerIndex        = "idx_pki_identity_active_owner"
	pkiIdentityOwnerLookupIndex        = "idx_pki_identity_owner_lookup"
)

type SchemaOptions struct {
	Driver                 string
	TrafficStatsEnabled    bool
	SQLiteLegacyMigrations bool
	LocalAgentID           string
}

func SchemaOptionsForDriver(driver string, trafficStatsEnabled bool) SchemaOptions {
	driver = strings.ToLower(strings.TrimSpace(driver))
	return SchemaOptions{
		Driver:                 driver,
		TrafficStatsEnabled:    trafficStatsEnabled,
		SQLiteLegacyMigrations: driver == "" || driver == "sqlite",
		LocalAgentID:           "local",
	}
}

func BootstrapSchema(ctx context.Context, db *gorm.DB, options SchemaOptions) error {
	tx := db.WithContext(ctx)
	defaultPluginTargetID := strings.TrimSpace(options.LocalAgentID)
	if defaultPluginTargetID == "" {
		defaultPluginTargetID = "local"
	}

	if options.SQLiteLegacyMigrations {
		if err := cleanupSQLiteLegacyLocalAgentState(ctx, db); err != nil {
			return err
		}
		if err := createSQLiteEgressProfilesTable(ctx, db); err != nil {
			return err
		}
		if err := prepareSQLiteLegacyPluginPackageColumns(db); err != nil {
			return err
		}
		if err := prepareSQLiteLegacyPluginTrustColumns(db); err != nil {
			return err
		}
		if err := migrateSQLitePluginPackageVariantIdentity(ctx, db); err != nil {
			return err
		}
		if err := migrateSQLitePluginGCVariantIdentity(ctx, db); err != nil {
			return err
		}
	} else if strings.EqualFold(options.Driver, "postgres") {
		if err := migratePostgresPluginVariantIdentity(ctx, db); err != nil {
			return err
		}
	}
	if err := prepareLegacyPluginRollbackResourceGroupColumn(db); err != nil {
		return err
	}
	if err := preparePluginSafeIndexes(ctx, db); err != nil {
		return err
	}
	if err := prepareMarketplaceRepositorySourceColumns(ctx, db); err != nil {
		return err
	}
	if err := preparePostgresPluginRuntimeStateBinaryColumn(ctx, db, options.Driver); err != nil {
		return err
	}

	if err := tx.AutoMigrate(
		&AgentRow{},
		&HTTPRuleRow{},
		&L4RuleRow{},
		&RelayListenerRow{},
		&EgressProfileRow{},
		&ManagedCertificateRow{},
		&ManagedCertificateGenerationRow{},
		&LocalAgentStateRow{},
		&VersionPolicyRow{},
		&MetaRow{},
		&OperationRow{},
		&AgentRevisionRow{},
		&AgentRevisionPointerRow{},
		&AgentRevisionAttemptRow{},
		&AgentGenerationRow{},
		&RevisionEventRow{},
		&IdempotencyRecordRow{},
		&GenerationArtifactRow{},
		&AgentRevisionArtifactRow{},
		&PKISettingsRow{},
		&PKIAuthorityRow{},
		&PKIIdentityRow{},
		&PKICertificateRow{},
		&PKIEnrollmentTokenRow{},
		&PKIEnrollmentReplayRow{},
		&PKIConfirmationNonceRow{},
		&PKISecuritySnapshotRow{},
		&PKILifecycleJobRow{},
		&PKIEventRow{},
		&PKIInstanceLeaseRow{},
		&UserRow{},
		&SessionRow{},
		&RoleRow{},
		&PermissionRow{},
		&RolePermissionRow{},
		&RoleBindingRow{},
		&ResourceGroupRow{},
		&ResourceGroupGrantRow{},
		&ResourceBindingRow{},
		&QuotaPolicyRow{},
		&QuotaUsageRow{},
		&QuotaPolicyUsageRow{},
		&QuotaAllocationRow{},
		&AuditEventRow{},
		&SecretRow{},
		&SecretVersionRow{},
		&MarketplaceSourceRow{},
		&MarketSnapshotRow{},
		&MarketEntryRow{},
		&MarketplaceRefreshOperationRow{},
		&PluginPackageRow{},
		&PluginArtifactRow{},
		&PluginPackageAcquisitionRow{},
		&PluginPackageStagingRow{},
		&PluginCacheGCIntentRow{},
		&PluginDigestFenceRow{},
		&MarketplaceSourceDeletionRow{},
		&MarketplaceDirectoryCleanupRow{},
		&InstalledPluginRow{},
		&PluginInstanceRow{},
		&PluginPolicyAgentRevisionRow{},
		&PluginGrantRow{},
		&PluginOperationRow{},
		&PluginOperationScopeRow{},
		&PluginOperationSecretRow{},
		&PluginAgentRuntimeStatusRow{},
		&PluginRuntimeLogRow{},
		&PluginRuntimeStateRow{},
		&PluginControlPlaneLogOutboxRow{},
		&PluginRuntimeLogReportRow{},
	); err != nil {
		return err
	}
	if err := cleanupLegacyPluginIndexes(ctx, db); err != nil {
		return err
	}
	if err := backfillPluginVariantReferences(ctx, db); err != nil {
		return err
	}
	if err := migratePluginRuntimeProjection(ctx, db); err != nil {
		return err
	}
	if err := backfillPluginRollbackResourceGroups(ctx, db); err != nil {
		return err
	}
	if err := backfillPluginOperationScopes(ctx, db); err != nil {
		return err
	}
	if err := backfillPluginAgentRuntimeAuthority(ctx, db); err != nil {
		return err
	}
	if err := normalizeLegacyControlPlanePluginTargets(ctx, db, defaultPluginTargetID); err != nil {
		return err
	}
	if err := backfillPluginOwnershipAndAcquisitions(ctx, db, defaultPluginTargetID); err != nil {
		return err
	}
	if err := backfillMarketplaceSignatureTrust(ctx, db); err != nil {
		return err
	}
	if err := backfillMarketplaceRepositorySources(ctx, db); err != nil {
		return err
	}
	if err := reconcileTerminalMarketplaceRefreshStaging(ctx, db); err != nil {
		return err
	}
	if err := backfillMarketplaceDirectoryCleanup(ctx, db); err != nil {
		return err
	}
	if err := migrateQuotaUsageScopes(ctx, db); err != nil {
		return err
	}
	if err := migratePKIIdentityOwnerSlots(ctx, db); err != nil {
		return err
	}

	if options.TrafficStatsEnabled {
		if err := tx.AutoMigrate(
			&AgentTrafficPolicyRow{},
			&AgentTrafficBaselineRow{},
			&AgentTrafficAgentRow{},
			&AgentTrafficRawCursorRow{},
			&AgentTrafficHourlyBucketRow{},
			&AgentTrafficDailySummaryRow{},
			&AgentTrafficMonthlySummaryRow{},
			&AgentTrafficEventRow{},
		); err != nil {
			return err
		}
		if err := backfillTrafficAgentIndex(ctx, db); err != nil {
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

	if err := normalizeAgentDefaultsOnce(ctx, db); err != nil {
		return err
	}
	if err := backfillSecurityResourceOwnershipAndQuota(ctx, db); err != nil {
		return err
	}

	return tx.
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&LocalAgentStateRow{
			ID:              1,
			LastApplyStatus: "success",
		}).Error
}

func preparePostgresPluginRuntimeStateBinaryColumn(ctx context.Context, db *gorm.DB, driver string) error {
	if !strings.EqualFold(strings.TrimSpace(driver), "postgres") || !db.Migrator().HasTable(&PluginRuntimeStateRow{}) {
		return nil
	}
	var column struct {
		DataType string `gorm:"column:data_type"`
		UDTName  string `gorm:"column:udt_name"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT data_type, udt_name
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'plugin_runtime_state'
		  AND column_name = 'value'
	`).Scan(&column).Error; err != nil {
		return err
	}
	if strings.EqualFold(column.UDTName, "bytea") {
		return nil
	}
	if !strings.EqualFold(column.UDTName, "blob") {
		return fmt.Errorf("plugin_runtime_state.value has unsupported PostgreSQL type %q (%q)", column.DataType, column.UDTName)
	}
	return db.WithContext(ctx).Exec(`
		ALTER TABLE plugin_runtime_state
		ALTER COLUMN value TYPE bytea USING value::bytea
	`).Error
}

func prepareLegacyPluginRollbackResourceGroupColumn(db *gorm.DB) error {
	if !db.Migrator().HasTable(&PluginInstanceRow{}) || db.Migrator().HasColumn(&PluginInstanceRow{}, "RollbackResourceGroupID") {
		return nil
	}
	if db.Dialector.Name() == "sqlite" {
		if err := db.Exec(`ALTER TABLE plugin_instances ADD COLUMN rollback_resource_group_id text NOT NULL DEFAULT ""`).Error; err != nil {
			return fmt.Errorf("add legacy plugin rollback resource-group column: %w", err)
		}
		return nil
	}
	if err := db.Migrator().AddColumn(&PluginInstanceRow{}, "RollbackResourceGroupID"); err != nil {
		return fmt.Errorf("add legacy plugin rollback resource-group column: %w", err)
	}
	return nil
}

// backfillPluginRollbackResourceGroups is the pre-service boundary for the
// A5 rollback ownership column. A legacy rollback slot is usable only when
// its immutable owner can be reconstructed from exact Vault or consumer
// binding evidence. Ambiguous plugin-wide rollback state is retired instead
// of preventing the control plane from starting.
func backfillPluginRollbackResourceGroups(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var installedRows []InstalledPluginRow
		if err := tx.Order("plugin_id").Find(&installedRows).Error; err != nil {
			return err
		}
		for _, installed := range installedRows {
			var instances []PluginInstanceRow
			if err := tx.Where("plugin_id = ?", installed.PluginID).Order("id").Find(&instances).Error; err != nil {
				return err
			}
			retire := false
			resolved := make(map[string]string, len(instances))
			for _, instance := range instances {
				if !pluginRollbackSnapshotPresent(instance) {
					if installed.RollbackPackageDigest != "" {
						retire = true
					}
					continue
				}
				if installed.RollbackPackageDigest == "" || instance.RollbackVersion == 0 {
					retire = true
					continue
				}
				groupID, exact, err := pluginRollbackResourceGroupEvidenceTx(ctx, tx, instance)
				if err != nil {
					return err
				}
				if !exact {
					retire = true
					continue
				}
				resolved[instance.ID] = groupID
			}
			if retire {
				if err := retirePluginRollbackSnapshotsTx(tx, installed, instances); err != nil {
					return err
				}
				continue
			}
			for instanceID, groupID := range resolved {
				if err := tx.Model(&PluginInstanceRow{}).
					Where("id = ? AND plugin_id = ? AND rollback_resource_group_id = ?", instanceID, installed.PluginID, "").
					Updates(map[string]any{"rollback_resource_group_id": groupID, "state_version": gorm.Expr("state_version + 1")}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func pluginRollbackSnapshotPresent(instance PluginInstanceRow) bool {
	return instance.RollbackVersion != 0 || strings.TrimSpace(instance.RollbackConfigJSON) != "" ||
		!canonicalJSONIsEmptyArray(instance.RollbackSecretHandlesJSON) ||
		!canonicalJSONIsEmptyArray(instance.RollbackBindingsJSON) ||
		!canonicalJSONIsEmptyArray(instance.RollbackPolicyChainsJSON)
}

func pluginRollbackResourceGroupEvidenceTx(ctx context.Context, tx *gorm.DB, instance PluginInstanceRow) (string, bool, error) {
	groups := make(map[string]struct{})
	addGroup := func(groupID string) bool {
		groupID = strings.TrimSpace(groupID)
		if groupID == "" {
			return false
		}
		groups[groupID] = struct{}{}
		return len(groups) == 1
	}

	var handles []PluginInstanceSecretHandle
	if err := json.Unmarshal([]byte(pluginDefaultArrayJSON(instance.RollbackSecretHandlesJSON)), &handles); err != nil {
		return "", false, nil
	}
	for _, handle := range handles {
		var secret SecretRow
		err := tx.WithContext(ctx).Where("id = ?", strings.TrimSpace(handle.ID)).First(&secret).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		var version SecretVersionRow
		err = tx.WithContext(ctx).Where("secret_id = ? AND version = ?", secret.ID, handle.Version).First(&version).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if handle.ID == "" || handle.Version == 0 || handle.Purpose == "" || handle.Purpose != secret.Purpose ||
			pluginSecretInstanceID(secret.Purpose) != instance.ID || secret.RetiredAt != nil || version.DestroyedAt != nil || !addGroup(secret.ResourceGroupID) {
			return "", false, nil
		}
	}

	bindings, err := CanonicalPluginInstanceBindings(instance.RollbackBindingsJSON)
	if err != nil {
		return "", false, nil
	}
	for _, binding := range bindings {
		consumerID, err := strconv.Atoi(binding.Consumer.ID)
		if err != nil || consumerID <= 0 {
			return "", false, nil
		}
		resourceID := binding.TargetAgentID + ":" + binding.Consumer.ID
		var owner ResourceBindingRow
		err = tx.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", binding.Consumer.Kind, resourceID).First(&owner).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if owner.ResourceGroupID != binding.Consumer.ResourceGroupID || pluginDependencyConsumerOwnershipVersion(owner) != binding.Consumer.Version || !addGroup(owner.ResourceGroupID) {
			return "", false, nil
		}
		var count int64
		switch binding.Consumer.Kind {
		case PluginDependencyConsumerHTTPRule:
			err = tx.WithContext(ctx).Model(&HTTPRuleRow{}).Where("agent_id = ? AND id = ?", binding.TargetAgentID, consumerID).Count(&count).Error
		case PluginDependencyConsumerL4Rule:
			err = tx.WithContext(ctx).Model(&L4RuleRow{}).Where("agent_id = ? AND id = ?", binding.TargetAgentID, consumerID).Count(&count).Error
		default:
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if count != 1 {
			return "", false, nil
		}
		var agentOwner ResourceBindingRow
		err = tx.WithContext(ctx).Where("resource_kind = ? AND resource_id = ?", "agent", binding.TargetAgentID).First(&agentOwner).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if agentOwner.ResourceGroupID != owner.ResourceGroupID {
			return "", false, nil
		}
	}

	current := strings.TrimSpace(instance.RollbackResourceGroupID)
	if current != "" {
		if len(groups) == 0 {
			return current, true, nil
		}
		if _, ok := groups[current]; ok && len(groups) == 1 {
			return current, true, nil
		}
		return "", false, nil
	}
	if len(groups) != 1 {
		return "", false, nil
	}
	for groupID := range groups {
		return groupID, true, nil
	}
	return "", false, nil
}

func retirePluginRollbackSnapshotsTx(tx *gorm.DB, installed InstalledPluginRow, instances []PluginInstanceRow) error {
	candidates := make(map[string]string)
	for _, instance := range instances {
		var handles []PluginInstanceSecretHandle
		if json.Unmarshal([]byte(pluginDefaultArrayJSON(instance.RollbackSecretHandlesJSON)), &handles) == nil {
			for _, handle := range handles {
				if handle.ID != "" {
					candidates[handle.ID] = instance.ID
				}
			}
		}
	}
	now := time.Now().UTC()
	if err := tx.Model(&PluginInstanceRow{}).Where("plugin_id = ?", installed.PluginID).Updates(map[string]any{
		"rollback_config_json": "", "rollback_version": 0, "rollback_resource_group_id": "",
		"rollback_policy_chains_json": "[]", "rollback_bindings_json": "[]", "rollback_secret_handles_json": "[]",
		"state_version": gorm.Expr("state_version + 1"), "updated_at": now,
	}).Error; err != nil {
		return err
	}
	if err := tx.Model(&InstalledPluginRow{}).Where("plugin_id = ?", installed.PluginID).Updates(map[string]any{
		"rollback_package_digest": "", "rollback_package_identity": "", "rollback_source_id": "", "rollback_source_kind": "", "rollback_source_risk_label": "",
		"rollback_source_revision": 0, "rollback_source_ref_kind": "", "rollback_source_ref_name": "", "rollback_source_resolved_oid": "",
		"rollback_signature_key_id": "", "rollback_signature_public_key": "", "rollback_signature_fingerprint": "",
		"state_version": gorm.Expr("state_version + 1"), "updated_at": now,
	}).Error; err != nil {
		return err
	}
	current, err := pluginSecretIDsForPluginTx(tx, installed.PluginID)
	if err != nil {
		return err
	}
	for secretID, instanceID := range candidates {
		if _, referenced := current[secretID]; referenced {
			continue
		}
		var secret SecretRow
		err := tx.Where("id = ?", secretID).First(&secret).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if pluginSecretInstanceID(secret.Purpose) != instanceID {
			continue
		}
		if err := tx.Model(&SecretVersionRow{}).Where("secret_id = ? AND destroyed_at IS NULL", secretID).Updates(map[string]any{"destroyed_at": now, "nonce": []byte{}, "ciphertext": []byte{}}).Error; err != nil {
			return err
		}
		if err := tx.Model(&SecretRow{}).Where("id = ? AND retired_at IS NULL", secretID).Update("retired_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&PluginOperationSecretRow{}).Where("secret_id = ? AND retired_at IS NULL", secretID).Updates(map[string]any{"role": "retired", "state": "retired", "retired_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func backfillPluginOperationScopes(ctx context.Context, db *gorm.DB) error {
	var operations []PluginOperationRow
	if err := db.WithContext(ctx).Where("instance_id <> ? AND resource_group_id <> ?", "", "").Find(&operations).Error; err != nil {
		return err
	}
	rows := make([]PluginOperationScopeRow, 0, len(operations))
	for _, operation := range operations {
		rows = append(rows, PluginOperationScopeRow{OperationID: operation.ID, InstanceID: operation.InstanceID, PluginID: operation.PluginID, ResourceGroupID: operation.ResourceGroupID, CreatedAt: operation.CreatedAt})
	}
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
}

func backfillPluginAgentRuntimeAuthority(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&PluginAgentRuntimeStatusRow{}).Where("authority_slot = ?", "").Update("authority_slot", "retired").Error; err != nil {
			return err
		}
		var instances []PluginInstanceRow
		if err := tx.Find(&instances).Error; err != nil {
			return err
		}
		for _, instance := range instances {
			var installed InstalledPluginRow
			if err := tx.Where("plugin_id = ?", instance.PluginID).First(&installed).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return err
			}
			if installed.PendingOperationID != "" {
				if err := tx.Model(&PluginAgentRuntimeStatusRow{}).Where("plugin_id = ? AND instance_id = ? AND operation_id = ?", instance.PluginID, instance.ID, installed.PendingOperationID).Update("authority_slot", "pending").Error; err != nil {
					return err
				}
			}
			if installed.CurrentLifecycle != "active" && installed.CurrentLifecycle != "degraded" && installed.PendingOperationID == "" {
				continue
			}
			var targets []string
			if err := json.Unmarshal([]byte(instance.TargetJSON), &targets); err != nil {
				return fmt.Errorf("plugin instance %s active targets are invalid: %w", instance.ID, err)
			}
			for _, agentID := range targets {
				var active PluginAgentRuntimeStatusRow
				err := tx.Where("plugin_id = ? AND instance_id = ? AND agent_id = ? AND operation_id <> ? AND resource_group_id = ? AND target_version = ? AND package_digest = ? AND state IN ?", instance.PluginID, instance.ID, agentID, installed.PendingOperationID, instance.ResourceGroupID, instance.ConfigVersion, installed.ActivePackageDigest, []string{"active", "degraded"}).Order("updated_at DESC").First(&active).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				if err := tx.Model(&PluginAgentRuntimeStatusRow{}).Where("operation_id = ? AND agent_id = ? AND instance_id = ?", active.OperationID, active.AgentID, active.InstanceID).Update("authority_slot", "active").Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// prepareMarketplaceRepositorySourceColumns is the one-time legacy boundary:
// the old untyped reference column becomes ref_name before the current model is
// migrated. Runtime code never reads or writes the legacy column.
func prepareMarketplaceRepositorySourceColumns(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable(&MarketplaceSourceRow{}) || !db.Migrator().HasColumn("marketplace_sources", "reference") {
		return nil
	}
	if db.Migrator().HasColumn("marketplace_sources", "ref_name") {
		return errors.New("marketplace source has both legacy reference and ref_name columns")
	}
	if err := db.WithContext(ctx).Migrator().RenameColumn("marketplace_sources", "reference", "ref_name"); err != nil {
		return fmt.Errorf("migrate marketplace source reference: %w", err)
	}
	return nil
}

func backfillMarketplaceRepositorySources(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&MarketplaceSourceRow{}).Where("purpose = ?", "").Update("purpose", "market").Error; err != nil {
			return err
		}
		if err := tx.Model(&MarketplaceSourceRow{}).Where("ref_kind = ?", "").Update("ref_kind", "branch").Error; err != nil {
			return err
		}
		if err := tx.Model(&MarketplaceSourceRow{}).Where("config_revision = ?", 0).Update("config_revision", 1).Error; err != nil {
			return err
		}
		var rows []MarketplaceSourceRow
		if err := tx.Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			key := strings.ToLower(strings.TrimSpace(row.Name))
			if key == "" {
				return errors.New("marketplace source canonical name is empty")
			}
			if err := tx.Model(&MarketplaceSourceRow{}).Where("id = ?", row.ID).Update("name_key", key).Error; err != nil {
				return err
			}
		}
		var duplicates int64
		if err := tx.Raw("SELECT COUNT(*) FROM (SELECT purpose, name_key FROM marketplace_sources GROUP BY purpose, name_key HAVING COUNT(*) > 1) AS duplicate_names").Scan(&duplicates).Error; err != nil {
			return err
		}
		if duplicates != 0 {
			return errors.New("duplicate marketplace source purpose/name prevents repository-source migration")
		}
		if tx.Migrator().HasIndex(&marketplaceSourcePurposeNameIndex{}, "idx_marketplace_source_purpose_name") {
			return nil
		}
		return tx.Migrator().CreateIndex(&marketplaceSourcePurposeNameIndex{}, "idx_marketplace_source_purpose_name")
	})
}

type marketplaceSourcePurposeNameIndex struct {
	Purpose string `gorm:"uniqueIndex:idx_marketplace_source_purpose_name"`
	NameKey string `gorm:"uniqueIndex:idx_marketplace_source_purpose_name"`
}

func (marketplaceSourcePurposeNameIndex) TableName() string { return "marketplace_sources" }

func backfillPluginVariantReferences(ctx context.Context, db *gorm.DB) error {
	return reconcilePluginVariantReferences(ctx, db)
}

func prepareSQLiteLegacyPluginPackageColumns(db *gorm.DB) error {
	if !db.Migrator().HasTable("plugin_packages") || db.Migrator().HasColumn("plugin_packages", "identity") {
		return nil
	}
	for _, field := range []string{"RuntimeKind", "RuntimeABI", "HostScope", "EntryPath", "SignatureKeyID", "SignaturePublicKey", "SignatureFingerprint", "SourceID", "SourceKind", "SourceRiskLabel", "SignatureVerdict", "ResourceBudgetJSON", "FailurePolicyJSON"} {
		if !db.Migrator().HasColumn(&PluginPackageRow{}, field) {
			if err := db.Migrator().AddColumn(&PluginPackageRow{}, field); err != nil {
				return fmt.Errorf("add legacy plugin package column %s: %w", field, err)
			}
		}
	}
	return nil
}

func prepareSQLiteLegacyPluginTrustColumns(db *gorm.DB) error {
	for _, field := range []string{"SourceKind", "SignatureKeyID", "SignaturePublicKey", "SignatureFingerprint"} {
		if db.Migrator().HasTable(&PluginPackageAcquisitionRow{}) && !db.Migrator().HasColumn(&PluginPackageAcquisitionRow{}, field) {
			if err := db.Migrator().AddColumn(&PluginPackageAcquisitionRow{}, field); err != nil {
				return fmt.Errorf("add legacy plugin acquisition column %s: %w", field, err)
			}
		}
	}
	if db.Migrator().HasTable(&PluginPackageStagingRow{}) && !db.Migrator().HasColumn(&PluginPackageStagingRow{}, "SignerFingerprint") {
		if err := db.Migrator().AddColumn(&PluginPackageStagingRow{}, "SignerFingerprint"); err != nil {
			return fmt.Errorf("add legacy plugin staging signer fingerprint: %w", err)
		}
	}
	for _, field := range []string{"SignerKeyID", "SignerSecretRef", "SignerPublicKey", "SignerFingerprint"} {
		if db.Migrator().HasTable(&MarketplaceSourceRow{}) && !db.Migrator().HasColumn(&MarketplaceSourceRow{}, field) {
			if err := db.Migrator().AddColumn(&MarketplaceSourceRow{}, field); err != nil {
				return fmt.Errorf("add legacy marketplace source column %s: %w", field, err)
			}
		}
	}
	return nil
}

func migrateSQLitePluginGCVariantIdentity(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable("plugin_cache_gc_intents") {
		return nil
	}
	if !db.Migrator().HasColumn("plugin_cache_gc_intents", "signer_fingerprint") {
		if err := db.WithContext(ctx).Exec("ALTER TABLE plugin_cache_gc_intents ADD COLUMN signer_fingerprint text NOT NULL DEFAULT ''").Error; err != nil {
			return err
		}
	}
	var columns []struct {
		Name string `gorm:"column:name"`
		PK   int    `gorm:"column:pk"`
	}
	if err := db.WithContext(ctx).Raw("PRAGMA table_info('plugin_cache_gc_intents')").Scan(&columns).Error; err != nil {
		return err
	}
	fingerprintPrimary := false
	for _, column := range columns {
		fingerprintPrimary = fingerprintPrimary || (column.Name == "signer_fingerprint" && column.PK > 0)
	}
	if fingerprintPrimary {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE plugin_cache_gc_intents SET signer_fingerprint = COALESCE(
(SELECT signature_fingerprint FROM plugin_packages WHERE plugin_packages.source_id = plugin_cache_gc_intents.source_id AND plugin_packages.digest = plugin_cache_gc_intents.digest AND signature_fingerprint <> '' ORDER BY identity LIMIT 1),
(SELECT signature_fingerprint FROM plugin_package_acquisitions WHERE plugin_package_acquisitions.source_id = plugin_cache_gc_intents.source_id AND plugin_package_acquisitions.digest = plugin_cache_gc_intents.digest AND signature_fingerprint <> '' LIMIT 1),
(SELECT signer_fingerprint FROM marketplace_sources WHERE marketplace_sources.id = plugin_cache_gc_intents.source_id AND signer_fingerprint <> '' LIMIT 1), '') WHERE signer_fingerprint = ''`).Error; err != nil {
			return err
		}
		if err := tx.Exec("ALTER TABLE plugin_cache_gc_intents RENAME TO plugin_cache_gc_intents_legacy_pk").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE plugin_cache_gc_intents (
source_id text NOT NULL, digest text NOT NULL, signer_fingerprint text NOT NULL DEFAULT '', status text NOT NULL,
deferred numeric NOT NULL DEFAULT false, claim_token text NOT NULL DEFAULT '', claim_expires_at datetime,
quarantine_path text NOT NULL DEFAULT '', last_error text NOT NULL, updated_at datetime NOT NULL,
PRIMARY KEY (source_id,digest,signer_fingerprint))`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO plugin_cache_gc_intents (source_id,digest,signer_fingerprint,status,deferred,claim_token,claim_expires_at,quarantine_path,last_error,updated_at)
SELECT source_id,digest,signer_fingerprint,status,deferred,claim_token,claim_expires_at,quarantine_path,last_error,updated_at FROM plugin_cache_gc_intents_legacy_pk`).Error; err != nil {
			return err
		}
		return tx.Exec("DROP TABLE plugin_cache_gc_intents_legacy_pk").Error
	})
}

func postgresPluginVariantMigrationStatements() []string {
	return []string{
		`ALTER TABLE plugin_packages ADD COLUMN IF NOT EXISTS identity varchar(64), ADD COLUMN IF NOT EXISTS runtime_kind varchar(32) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS runtime_abi varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS host_scope varchar(32) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS entry_path varchar(2048) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_key_id varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_public_key varchar(64) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_fingerprint varchar(64) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS source_id varchar(64) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS source_kind varchar(32) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS source_risk_label varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_verdict varchar(32) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS resource_budget_json text NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS failure_policy_json text NOT NULL DEFAULT ''`,
		`UPDATE plugin_packages SET identity = digest WHERE identity IS NULL OR identity = ''`,
		`ALTER TABLE plugin_packages ALTER COLUMN identity SET NOT NULL`,
		`DO $$ DECLARE constraint_name text; BEGIN SELECT conname INTO constraint_name FROM pg_constraint WHERE conrelid = 'plugin_packages'::regclass AND contype = 'p'; IF constraint_name IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint c JOIN unnest(c.conkey) key(attnum) ON true JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.attnum WHERE c.conrelid='plugin_packages'::regclass AND c.contype='p' GROUP BY c.oid HAVING array_agg(a.attname::text ORDER BY a.attname)=ARRAY['identity']) THEN EXECUTE format('ALTER TABLE plugin_packages DROP CONSTRAINT %I', constraint_name); END IF; END $$`,
		`DO $$ DECLARE constraint_name text; BEGIN FOR constraint_name IN SELECT c.conname FROM pg_constraint c JOIN unnest(c.conkey) key(attnum) ON true JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.attnum WHERE c.conrelid='plugin_packages'::regclass AND c.contype='u' GROUP BY c.oid,c.conname HAVING array_agg(a.attname::text ORDER BY a.attname)=ARRAY['digest'] LOOP EXECUTE format('ALTER TABLE plugin_packages DROP CONSTRAINT %I', constraint_name); END LOOP; END $$`,
		`DO $$ DECLARE index_name text; BEGIN FOR index_name IN SELECT ic.relname FROM pg_index i JOIN pg_class tc ON tc.oid=i.indrelid JOIN pg_class ic ON ic.oid=i.indexrelid JOIN LATERAL unnest(i.indkey) WITH ORDINALITY key(attnum,position) ON key.position=1 JOIN pg_attribute a ON a.attrelid=tc.oid AND a.attnum=key.attnum WHERE tc.oid='plugin_packages'::regclass AND i.indisunique AND NOT i.indisprimary AND i.indnkeyatts=1 AND a.attname='digest' LOOP EXECUTE format('DROP INDEX %I', index_name); END LOOP; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_constraint c JOIN unnest(c.conkey) key(attnum) ON true JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.attnum WHERE c.conrelid='plugin_packages'::regclass AND c.contype='p' GROUP BY c.oid HAVING array_agg(a.attname::text ORDER BY a.attname)=ARRAY['identity']) THEN ALTER TABLE plugin_packages ADD PRIMARY KEY (identity); END IF; END $$`,
		`CREATE INDEX IF NOT EXISTS idx_plugin_packages_digest ON plugin_packages(digest)`,
		`ALTER TABLE IF EXISTS plugin_package_acquisitions ADD COLUMN IF NOT EXISTS source_kind varchar(32) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_key_id varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_public_key varchar(64) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signature_fingerprint varchar(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE IF EXISTS plugin_package_staging ADD COLUMN IF NOT EXISTS signer_fingerprint varchar(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE IF EXISTS marketplace_sources ADD COLUMN IF NOT EXISTS signer_key_id varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signer_secret_ref varchar(190) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signer_public_key varchar(64) NOT NULL DEFAULT '', ADD COLUMN IF NOT EXISTS signer_fingerprint varchar(64) NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_cache_gc_intents ADD COLUMN IF NOT EXISTS signer_fingerprint varchar(64) NOT NULL DEFAULT ''`,
		`UPDATE plugin_cache_gc_intents i SET signer_fingerprint = COALESCE((SELECT p.signature_fingerprint FROM plugin_packages p WHERE p.source_id=i.source_id AND p.digest=i.digest AND p.signature_fingerprint<>'' ORDER BY p.identity LIMIT 1),(SELECT a.signature_fingerprint FROM plugin_package_acquisitions a WHERE a.source_id=i.source_id AND a.digest=i.digest AND a.signature_fingerprint<>'' LIMIT 1),(SELECT s.signer_fingerprint FROM marketplace_sources s WHERE s.id=i.source_id AND s.signer_fingerprint<>'' LIMIT 1),'') WHERE signer_fingerprint=''`,
		`DO $$ DECLARE constraint_name text; BEGIN SELECT conname INTO constraint_name FROM pg_constraint WHERE conrelid='plugin_cache_gc_intents'::regclass AND contype='p'; IF constraint_name IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint c JOIN unnest(c.conkey) key(attnum) ON true JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.attnum WHERE c.conrelid='plugin_cache_gc_intents'::regclass AND c.contype='p' GROUP BY c.oid HAVING array_agg(a.attname::text ORDER BY a.attname)=ARRAY['digest','signer_fingerprint','source_id']) THEN EXECUTE format('ALTER TABLE plugin_cache_gc_intents DROP CONSTRAINT %I', constraint_name); END IF; IF NOT EXISTS (SELECT 1 FROM pg_constraint c JOIN unnest(c.conkey) key(attnum) ON true JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum=key.attnum WHERE c.conrelid='plugin_cache_gc_intents'::regclass AND c.contype='p' GROUP BY c.oid HAVING array_agg(a.attname::text ORDER BY a.attname)=ARRAY['digest','signer_fingerprint','source_id']) THEN ALTER TABLE plugin_cache_gc_intents ADD PRIMARY KEY (source_id,digest,signer_fingerprint); END IF; END $$`,
	}
}

func migratePostgresPluginVariantIdentity(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		statements := postgresPluginVariantMigrationStatements()
		for index, statement := range statements {
			if index < 8 && !tx.Migrator().HasTable("plugin_packages") {
				continue
			}
			if index >= 8 && !tx.Migrator().HasTable("plugin_cache_gc_intents") {
				continue
			}
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func migrateSQLitePluginPackageVariantIdentity(ctx context.Context, db *gorm.DB) error {
	if !db.Migrator().HasTable("plugin_packages") || db.Migrator().HasColumn("plugin_packages", "identity") {
		return nil
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER TABLE plugin_packages RENAME TO plugin_packages_digest_pk").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE plugin_packages (
identity text PRIMARY KEY, digest text NOT NULL, plugin_id text NOT NULL, version text NOT NULL,
runtime_kind text NOT NULL DEFAULT '', runtime_abi text NOT NULL DEFAULT '', host_scope text NOT NULL DEFAULT '', entry_path text NOT NULL DEFAULT '',
signature_key_id text NOT NULL DEFAULT '', signature_public_key text NOT NULL DEFAULT '', signature_fingerprint text NOT NULL DEFAULT '',
source_id text NOT NULL DEFAULT '', source_kind text NOT NULL DEFAULT '', source_risk_label text NOT NULL DEFAULT '', signature_verdict text NOT NULL DEFAULT '',
resource_budget_json text NOT NULL DEFAULT '', failure_policy_json text NOT NULL DEFAULT '', cache_path text NOT NULL,
manifest_json text NOT NULL, config_schema_json text NOT NULL, verified_at datetime NOT NULL)`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO plugin_packages (
identity,digest,plugin_id,version,runtime_kind,runtime_abi,host_scope,entry_path,signature_key_id,signature_public_key,signature_fingerprint,
source_id,source_kind,source_risk_label,signature_verdict,resource_budget_json,failure_policy_json,cache_path,manifest_json,config_schema_json,verified_at)
SELECT digest,digest,plugin_id,version,runtime_kind,runtime_abi,host_scope,entry_path,signature_key_id,signature_public_key,signature_fingerprint,
source_id,source_kind,source_risk_label,signature_verdict,resource_budget_json,failure_policy_json,cache_path,manifest_json,config_schema_json,verified_at
FROM plugin_packages_digest_pk`).Error; err != nil {
			return err
		}
		return tx.Exec("DROP TABLE plugin_packages_digest_pk").Error
	})
}

func preparePluginSafeIndexes(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if db.Migrator().HasTable(&MarketplaceDirectoryCleanupRow{}) {
		if !db.Migrator().HasColumn(&MarketplaceDirectoryCleanupRow{}, "PathDigest") {
			if err := db.Migrator().AddColumn(&MarketplaceDirectoryCleanupRow{}, "PathDigest"); err != nil {
				return err
			}
		}
		var rows []MarketplaceDirectoryCleanupRow
		if err := tx.Select("id", "path", "path_digest").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if row.PathDigest == "" {
				if err := tx.Model(&MarketplaceDirectoryCleanupRow{}).Where("id = ?", row.ID).Update("path_digest", pluginStorageDigest(row.Path)).Error; err != nil {
					return err
				}
			}
		}
	}
	if db.Migrator().HasTable(&PluginGrantRow{}) {
		if !db.Migrator().HasColumn(&PluginGrantRow{}, "GrantKey") {
			if err := db.Migrator().AddColumn(&PluginGrantRow{}, "GrantKey"); err != nil {
				return err
			}
		}
		var grants []PluginGrantRow
		if err := tx.Find(&grants).Error; err != nil {
			return err
		}
		for _, grant := range grants {
			if grant.GrantKey == "" {
				if err := tx.Model(&PluginGrantRow{}).Where("id = ?", grant.ID).Update("grant_key", pluginGrantKey(grant)).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func cleanupLegacyPluginIndexes(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if tx.Migrator().HasTable(&MarketplaceDirectoryCleanupRow{}) {
		if !tx.Migrator().HasIndex(&MarketplaceDirectoryCleanupRow{}, "PathDigest") {
			return errors.New("marketplace cleanup path digest index is unavailable")
		}
		if tx.Migrator().HasIndex(&MarketplaceDirectoryCleanupRow{}, "idx_marketplace_directory_cleanup_path") {
			if err := tx.Migrator().DropIndex(&MarketplaceDirectoryCleanupRow{}, "idx_marketplace_directory_cleanup_path"); err != nil {
				return err
			}
		}
	}
	if tx.Migrator().HasTable(&PluginGrantRow{}) {
		if !tx.Migrator().HasIndex(&PluginGrantRow{}, "GrantKey") {
			return errors.New("plugin grant key index is unavailable")
		}
		if tx.Migrator().HasIndex(&PluginGrantRow{}, "idx_plugin_grant") {
			if err := tx.Migrator().DropIndex(&PluginGrantRow{}, "idx_plugin_grant"); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateQuotaUsageScopes(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if !tx.Migrator().HasTable(&QuotaUsageRow{}) {
		return nil
	}
	if tx.Migrator().HasIndex(&QuotaUsageRow{}, "idx_quota_usage") {
		if err := tx.Migrator().DropIndex(&QuotaUsageRow{}, "idx_quota_usage"); err != nil {
			return err
		}
	}
	return tx.Where("subject_kind = '' OR subject_id = ''").Delete(&QuotaUsageRow{}).Error
}

// backfillSecurityResourceOwnershipAndQuota gives pre-security resources a
// canonical owner group and count allocation. Legacy rows have no durable
// creating-user fact, so their attribution is intentionally resource-group
// only; new mutations continue to add user and role allocations as well.
func backfillSecurityResourceOwnershipAndQuota(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		var agentBindings []ResourceBindingRow
		if err := tx.Where("resource_kind = ?", "agent").Find(&agentBindings).Error; err != nil {
			return err
		}
		agentGroups := make(map[string]string, len(agentBindings))
		for _, binding := range agentBindings {
			agentGroups[binding.ResourceID] = binding.ResourceGroupID
		}

		type legacyResource struct {
			kind, id, agentID, metric string
		}
		resources := make([]legacyResource, 0)
		var httpRules []HTTPRuleRow
		if err := tx.Find(&httpRules).Error; err != nil {
			return err
		}
		for _, row := range httpRules {
			resources = append(resources, legacyResource{"http_rule", fmt.Sprintf("%s:%d", row.AgentID, row.ID), row.AgentID, "rule_count"})
			resources = append(resources, legacyResource{"http_rule", fmt.Sprintf("%s:%d", row.AgentID, row.ID), row.AgentID, "application_count"})
		}
		var l4Rules []L4RuleRow
		if err := tx.Find(&l4Rules).Error; err != nil {
			return err
		}
		for _, row := range l4Rules {
			resources = append(resources, legacyResource{"l4_rule", fmt.Sprintf("%s:%d", row.AgentID, row.ID), row.AgentID, "public_port_count"})
		}
		var listeners []RelayListenerRow
		if err := tx.Find(&listeners).Error; err != nil {
			return err
		}
		for _, row := range listeners {
			resources = append(resources, legacyResource{"relay_listener", fmt.Sprintf("%s:%d", row.AgentID, row.ID), row.AgentID, "public_port_count"})
		}
		var agents []AgentRow
		if err := tx.Find(&agents).Error; err != nil {
			return err
		}
		for _, agent := range agents {
			if strings.TrimSpace(agent.ID) != "" {
				agentGroups[agent.ID] = defaultString(agentGroups[agent.ID], "default")
			}
		}
		for _, resource := range resources {
			if strings.TrimSpace(resource.agentID) != "" {
				agentGroups[resource.agentID] = defaultString(agentGroups[resource.agentID], "default")
			}
		}
		for agentID, groupID := range agentGroups {
			binding := ResourceBindingRow{ID: securityID("res"), ResourceKind: "agent", ResourceID: agentID, ResourceGroupID: groupID, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoNothing: true}).Create(&binding).Error; err != nil {
				return err
			}
		}
		var certificates []ManagedCertificateRow
		if err := tx.Find(&certificates).Error; err != nil {
			return err
		}
		for _, certificate := range certificates {
			var targetAgentIDs []string
			if err := json.Unmarshal([]byte(defaultJSON(certificate.TargetAgentIDs, "[]")), &targetAgentIDs); err != nil {
				return fmt.Errorf("backfill certificate %d ownership: %w", certificate.ID, err)
			}
			groupID := "default"
			for index, agentID := range targetAgentIDs {
				targetGroupID := defaultString(agentGroups[strings.TrimSpace(agentID)], "default")
				if index == 0 {
					groupID = targetGroupID
					continue
				}
				if groupID != targetGroupID {
					groupID = crossGroupCertificateGroupID
					break
				}
			}
			binding := ResourceBindingRow{ID: securityID("res"), ResourceKind: "certificate", ResourceID: fmt.Sprintf("%d", certificate.ID), ResourceGroupID: groupID, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoNothing: true}).Create(&binding).Error; err != nil {
				return err
			}
		}
		var childBindings []ResourceBindingRow
		if err := tx.Where("resource_kind IN ?", []string{"http_rule", "l4_rule", "relay_listener"}).Find(&childBindings).Error; err != nil {
			return err
		}
		childGroups := make(map[string]string, len(childBindings))
		for _, binding := range childBindings {
			childGroups[binding.ResourceKind+"\x00"+binding.ResourceID] = binding.ResourceGroupID
		}

		for _, resource := range resources {
			groupID := childGroups[resource.kind+"\x00"+resource.id]
			if groupID == "" {
				groupID = agentGroups[resource.agentID]
			}
			if groupID == "" {
				groupID = "default"
			}
			binding := ResourceBindingRow{
				ID: securityID("res"), ResourceKind: resource.kind, ResourceID: resource.id, ResourceGroupID: groupID,
				ParentResourceKind: "agent", ParentResourceID: resource.agentID, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "resource_kind"}, {Name: "resource_id"}}, DoNothing: true}).Create(&binding).Error; err != nil {
				return err
			}
			scope := quotaScope{SubjectKind: "resource_group", SubjectID: groupID, ResourceGroupID: groupID}
			if err := tx.Where("resource_kind = ? AND resource_id = ? AND metric = ? AND subject_kind = ? AND (subject_id <> ? OR resource_group_id <> ?)", resource.kind, resource.id, resource.metric, "resource_group", groupID, groupID).Delete(&QuotaAllocationRow{}).Error; err != nil {
				return err
			}
			allocation := QuotaAllocationRow{
				ID: quotaAllocationID(resource.kind, resource.id, resource.metric, scope), ResourceKind: resource.kind,
				ResourceID: resource.id, Metric: resource.metric, SubjectKind: scope.SubjectKind, SubjectID: scope.SubjectID,
				ResourceGroupID: groupID, Amount: 1, CreatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&allocation).Error; err != nil {
				return err
			}
		}

		countMetrics := []string{"rule_count", "application_count", "public_port_count"}
		if err := tx.Model(&QuotaPolicyRow{}).Where("metric IN ? AND reset_at IS NOT NULL", countMetrics).Updates(map[string]any{"reset_at": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&QuotaUsageRow{}).Where("metric IN ?", countMetrics).Updates(map[string]any{"current": 0, "reset_at": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		type allocationTotal struct {
			SubjectKind, SubjectID, ResourceGroupID, Metric string
			Current                                         int64
		}
		var totals []allocationTotal
		if err := tx.Model(&QuotaAllocationRow{}).
			Select("subject_kind, subject_id, resource_group_id, metric, SUM(amount) AS current").
			Where("metric IN ?", countMetrics).
			Group("subject_kind, subject_id, resource_group_id, metric").Scan(&totals).Error; err != nil {
			return err
		}
		for _, total := range totals {
			scope := quotaScope{SubjectKind: total.SubjectKind, SubjectID: total.SubjectID, ResourceGroupID: total.ResourceGroupID}
			usage := QuotaUsageRow{ID: quotaUsageID(scope, total.Metric), SubjectKind: total.SubjectKind, SubjectID: total.SubjectID, ResourceGroupID: total.ResourceGroupID, Metric: total.Metric, Current: total.Current, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subject_kind"}, {Name: "subject_id"}, {Name: "resource_group_id"}, {Name: "metric"}}, DoUpdates: clause.AssignmentColumns([]string{"current", "reset_at", "updated_at"})}).Create(&usage).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// migratePKIIdentityOwnerSlots replaces the legacy permanent owner tuple
// uniqueness with a nullable active-owner slot. Revoked identities keep their
// original owner facts and certificate history, but no longer prevent a bound
// agent from receiving a fresh, non-revoked identity ID.
func migratePKIIdentityOwnerSlots(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if !tx.Migrator().HasTable(&PKIIdentityRow{}) || !tx.Migrator().HasColumn(&PKIIdentityRow{}, "active_owner_key") {
		return nil
	}
	return tx.Transaction(func(tx *gorm.DB) error {
		var identities []PKIIdentityRow
		if err := tx.Order("id ASC").Find(&identities).Error; err != nil {
			return err
		}
		activeOwners := make(map[string]string, len(identities))
		for _, identity := range identities {
			ownerKey, err := pkiIdentityOwnerKey(identity.PKIDomainID, identity.Kind, identity.AgentID, identity.ListenerID)
			if err != nil {
				return fmt.Errorf("migrate PKI identity %q owner slot: %w", identity.ID, err)
			}
			var nextOwnerKey *string
			if identity.State != PKIIdentityStateRevoked {
				if previous, found := activeOwners[ownerKey]; found {
					return pkiInvariant(fmt.Sprintf("identities %q and %q share one active owner during migration", previous, identity.ID))
				}
				activeOwners[ownerKey] = identity.ID
				nextOwnerKey = &ownerKey
			}
			if identity.ActiveOwnerKey == nil && nextOwnerKey == nil ||
				identity.ActiveOwnerKey != nil && nextOwnerKey != nil && *identity.ActiveOwnerKey == *nextOwnerKey {
				continue
			}
			var nextOwnerValue any
			if nextOwnerKey != nil {
				nextOwnerValue = *nextOwnerKey
			}
			if err := tx.Model(&PKIIdentityRow{}).
				Where("id = ?", identity.ID).
				UpdateColumn("active_owner_key", nextOwnerValue).Error; err != nil {
				return err
			}
		}
		if !tx.Migrator().HasIndex(&PKIIdentityRow{}, pkiIdentityActiveOwnerIndex) {
			if err := tx.Migrator().CreateIndex(&PKIIdentityRow{}, pkiIdentityActiveOwnerIndex); err != nil {
				return err
			}
		}
		if !tx.Migrator().HasIndex(&PKIIdentityRow{}, pkiIdentityOwnerLookupIndex) {
			if err := tx.Migrator().CreateIndex(&PKIIdentityRow{}, pkiIdentityOwnerLookupIndex); err != nil {
				return err
			}
		}
		if tx.Migrator().HasIndex(&PKIIdentityRow{}, legacyPKIIdentityOwnerIndex) {
			if err := tx.Migrator().DropIndex(&PKIIdentityRow{}, legacyPKIIdentityOwnerIndex); err != nil {
				return err
			}
		}
		return nil
	})
}

func backfillTrafficAgentIndex(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if !tx.Migrator().HasTable(&MetaRow{}) || !tx.Migrator().HasTable(&AgentTrafficAgentRow{}) {
		return nil
	}

	return tx.Transaction(func(tx *gorm.DB) error {
		marker := MetaRow{
			Key:   trafficAgentIndexBackfillMarkerKey,
			Value: "applied",
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		sourceTables := []string{
			(&AgentTrafficRawCursorRow{}).TableName(),
			(&AgentTrafficHourlyBucketRow{}).TableName(),
			(&AgentTrafficDailySummaryRow{}).TableName(),
			(&AgentTrafficMonthlySummaryRow{}).TableName(),
		}
		seen := map[string]struct{}{}
		if tx.Migrator().HasTable(&AgentRow{}) {
			var rows []string
			if err := tx.Model(&AgentRow{}).Pluck("id", &rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				agentID := strings.TrimSpace(row)
				if agentID != "" {
					seen[agentID] = struct{}{}
				}
			}
		}
		for _, table := range sourceTables {
			if !tx.Migrator().HasTable(table) {
				continue
			}
			var rows []string
			if err := tx.Table(table).Distinct("agent_id").Pluck("agent_id", &rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				agentID := strings.TrimSpace(row)
				if agentID == "" {
					continue
				}
				seen[agentID] = struct{}{}
			}
		}

		now := nowTrafficTimestamp()
		for agentID := range seen {
			if err := ensureTrafficAgentTx(tx, agentID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func normalizeAgentDefaultsOnce(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if !tx.Migrator().HasTable(&MetaRow{}) || !tx.Migrator().HasTable(&AgentRow{}) {
		return nil
	}

	return tx.Transaction(func(tx *gorm.DB) error {
		marker := MetaRow{
			Key:   agentDefaultNormalizationMarkerKey,
			Value: "applied",
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		for _, stmt := range []string{
			`UPDATE agents SET desired_version = '' WHERE desired_version IS NULL`,
			`UPDATE agents SET platform = '' WHERE platform IS NULL`,
			`UPDATE agents SET runtime_package_version = '' WHERE runtime_package_version IS NULL`,
			`UPDATE agents SET runtime_package_platform = '' WHERE runtime_package_platform IS NULL`,
			`UPDATE agents SET runtime_package_arch = '' WHERE runtime_package_arch IS NULL`,
			`UPDATE agents SET runtime_package_sha256 = '' WHERE runtime_package_sha256 IS NULL`,
			`UPDATE agents SET outbound_proxy_url = '' WHERE outbound_proxy_url IS NULL`,
			`UPDATE agents SET traffic_stats_interval = '' WHERE traffic_stats_interval IS NULL`,
			`UPDATE agents SET traffic_block_reason = '' WHERE traffic_block_reason IS NULL`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&AgentRow{}).Where("traffic_blocked IS NULL").Update("traffic_blocked", false).Error; err != nil {
			return err
		}
		return nil
	})
}

func cleanupSQLiteLegacyLocalAgentState(ctx context.Context, db *gorm.DB) error {
	tx := db.WithContext(ctx)
	if tx.Migrator().HasTable(&LocalAgentStateRow{}) {
		var legacyRows int64
		if err := tx.Model(&LocalAgentStateRow{}).Where("id <> ?", 1).Count(&legacyRows).Error; err != nil {
			return err
		}
		if legacyRows == 0 {
			return nil
		}
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
		{column: "traffic_blocked", sql: `ALTER TABLE agents ADD COLUMN traffic_blocked INTEGER NOT NULL DEFAULT 0`},
		{column: "traffic_block_reason", sql: `ALTER TABLE agents ADD COLUMN traffic_block_reason TEXT NOT NULL DEFAULT ''`},
		{column: "last_seen_ipv4", sql: `ALTER TABLE agents ADD COLUMN last_seen_ipv4 TEXT NOT NULL DEFAULT ''`},
		{column: "last_seen_ipv6", sql: `ALTER TABLE agents ADD COLUMN last_seen_ipv6 TEXT NOT NULL DEFAULT ''`},
		{column: "ddns_config", sql: `ALTER TABLE agents ADD COLUMN ddns_config TEXT NOT NULL DEFAULT ''`},
		{column: "ddns_status", sql: `ALTER TABLE agents ADD COLUMN ddns_status TEXT NOT NULL DEFAULT ''`},
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
		{column: "egress_profile_id", sql: `ALTER TABLE l4_rules ADD COLUMN egress_profile_id INTEGER`},
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

	ruleColumnMigrations := []struct {
		model  any
		column string
		sql    string
	}{
		{model: &HTTPRuleRow{}, column: "relay_layers", sql: `ALTER TABLE rules ADD COLUMN relay_layers TEXT NOT NULL DEFAULT '[]'`},
		{model: &HTTPRuleRow{}, column: "egress_profile_id", sql: `ALTER TABLE rules ADD COLUMN egress_profile_id INTEGER`},
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
		`UPDATE local_agent_state SET desired_version = '' WHERE desired_version IS NULL`,
		`UPDATE local_agent_state SET last_apply_status = 'success' WHERE last_apply_status IS NULL OR trim(last_apply_status) = ''`,
		`UPDATE local_agent_state SET last_apply_message = '' WHERE last_apply_message IS NULL`,
		`UPDATE managed_certificates SET usage = 'https' WHERE usage IS NULL OR trim(usage) = ''`,
		`UPDATE managed_certificates SET certificate_type = 'acme' WHERE certificate_type IS NULL OR trim(certificate_type) = ''`,
		`UPDATE managed_certificates SET self_signed = 0 WHERE self_signed IS NULL`,
		`UPDATE managed_certificates SET self_signed = 0 WHERE certificate_type = 'acme' AND self_signed <> 0`,
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
	Name            string `gorm:"column:name"`
	UpstreamHost    string `gorm:"column:upstream_host"`
	UpstreamPort    int    `gorm:"column:upstream_port"`
	BackendsJSON    string `gorm:"column:backends"`
	RelayChainJSON  string `gorm:"column:relay_chain"`
	RelayLayersJSON string `gorm:"column:relay_layers"`
	ProxyEgressMode string `gorm:"column:proxy_egress_mode"`
	ProxyEgressURL  string `gorm:"column:proxy_egress_url"`
	EgressProfileID *int   `gorm:"column:egress_profile_id"`
	Revision        int    `gorm:"column:revision"`
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
		tx.Migrator().HasColumn(&L4RuleRow{}, "egress_profile_id")
	selectColumns := "id, agent_id, name, upstream_host, upstream_port, backends, relay_chain, relay_layers, revision"
	if legacyEgressColumns {
		selectColumns += ", proxy_egress_mode, proxy_egress_url, egress_profile_id"
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

func canonicalJSONIsEmptyArray(raw string) bool {
	switch strings.TrimSpace(raw) {
	case "", "[]":
		return true
	default:
		return false
	}
}
