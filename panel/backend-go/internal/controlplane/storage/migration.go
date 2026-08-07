package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CopyDefaultMigrationRows copies durable control-plane state while leaving
// high-volume traffic history tables behind.
func CopyDefaultMigrationRows(ctx context.Context, source, target *GormStore) error {
	if source == nil || target == nil {
		return fmt.Errorf("source and target stores are required")
	}

	tables := []any{
		&AgentRow{},
		&LocalAgentStateRow{},
		&VersionPolicyRow{},
		&MetaRow{},
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
	}
	for _, table := range tables {
		if err := copyRows(ctx, source, target, table); err != nil {
			return err
		}
	}
	if err := copySharedMigrationRows(ctx, source, target); err != nil {
		return err
	}
	if err := copyPKIMigrationRows(ctx, source, target); err != nil {
		return err
	}
	if err := copyTrafficPolicies(ctx, source, target); err != nil {
		return err
	}
	if err := copyTrafficBaselines(ctx, source, target); err != nil {
		return err
	}

	return copyManagedCertificateMaterials(ctx, source, target)
}

func copyRows(ctx context.Context, source, target *GormStore, model any) error {
	if !source.db.Migrator().HasTable(model) {
		return nil
	}
	if !target.db.Migrator().HasTable(model) {
		return nil
	}

	rows := newSliceForModel(model)
	if err := source.db.WithContext(ctx).Model(model).Find(rows).Error; err != nil {
		return err
	}
	if isEmptyMigrationSlice(rows) {
		return nil
	}
	conflict, err := migrationUpsertClause(ctx, target, model)
	if err != nil {
		return err
	}
	return target.db.WithContext(ctx).
		Clauses(conflict).
		Create(rows).Error
}

func migrationUpsertClause(ctx context.Context, target *GormStore, model any) (clause.OnConflict, error) {
	columns, err := target.db.WithContext(ctx).Migrator().ColumnTypes(model)
	if err != nil {
		return clause.OnConflict{}, err
	}
	primaryKeys := make([]clause.Column, 0)
	for _, column := range columns {
		primary, ok := column.PrimaryKey()
		if ok && primary {
			primaryKeys = append(primaryKeys, clause.Column{Name: column.Name()})
		}
	}
	return clause.OnConflict{Columns: primaryKeys, UpdateAll: true}, nil
}

func newSliceForModel(model any) any {
	switch model.(type) {
	case *AgentRow:
		return &[]AgentRow{}
	case *HTTPRuleRow:
		return &[]HTTPRuleRow{}
	case *L4RuleRow:
		return &[]L4RuleRow{}
	case *RelayListenerRow:
		return &[]RelayListenerRow{}
	case *ManagedCertificateRow:
		return &[]ManagedCertificateRow{}
	case *LocalAgentStateRow:
		return &[]LocalAgentStateRow{}
	case *VersionPolicyRow:
		return &[]VersionPolicyRow{}
	case *MetaRow:
		return &[]MetaRow{}
	case *PKISettingsRow:
		return &[]PKISettingsRow{}
	case *PKIAuthorityRow:
		return &[]PKIAuthorityRow{}
	case *PKIIdentityRow:
		return &[]PKIIdentityRow{}
	case *PKICertificateRow:
		return &[]PKICertificateRow{}
	case *PKIEnrollmentTokenRow:
		return &[]PKIEnrollmentTokenRow{}
	case *PKIEnrollmentReplayRow:
		return &[]PKIEnrollmentReplayRow{}
	case *PKIConfirmationNonceRow:
		return &[]PKIConfirmationNonceRow{}
	case *PKISecuritySnapshotRow:
		return &[]PKISecuritySnapshotRow{}
	case *PKILifecycleJobRow:
		return &[]PKILifecycleJobRow{}
	case *PKIEventRow:
		return &[]PKIEventRow{}
	case *UserRow:
		return &[]UserRow{}
	case *SessionRow:
		return &[]SessionRow{}
	case *RoleRow:
		return &[]RoleRow{}
	case *PermissionRow:
		return &[]PermissionRow{}
	case *RolePermissionRow:
		return &[]RolePermissionRow{}
	case *RoleBindingRow:
		return &[]RoleBindingRow{}
	case *ResourceGroupRow:
		return &[]ResourceGroupRow{}
	case *ResourceGroupGrantRow:
		return &[]ResourceGroupGrantRow{}
	case *ResourceBindingRow:
		return &[]ResourceBindingRow{}
	case *QuotaPolicyRow:
		return &[]QuotaPolicyRow{}
	case *QuotaUsageRow:
		return &[]QuotaUsageRow{}
	case *QuotaPolicyUsageRow:
		return &[]QuotaPolicyUsageRow{}
	case *QuotaAllocationRow:
		return &[]QuotaAllocationRow{}
	case *AuditEventRow:
		return &[]AuditEventRow{}
	case *SecretRow:
		return &[]SecretRow{}
	case *SecretVersionRow:
		return &[]SecretVersionRow{}
	default:
		panic(fmt.Sprintf("unsupported migration model %T", model))
	}
}

func isEmptyMigrationSlice(rows any) bool {
	switch typed := rows.(type) {
	case *[]AgentRow:
		return len(*typed) == 0
	case *[]HTTPRuleRow:
		return len(*typed) == 0
	case *[]L4RuleRow:
		return len(*typed) == 0
	case *[]RelayListenerRow:
		return len(*typed) == 0
	case *[]ManagedCertificateRow:
		return len(*typed) == 0
	case *[]LocalAgentStateRow:
		return len(*typed) == 0
	case *[]VersionPolicyRow:
		return len(*typed) == 0
	case *[]MetaRow:
		return len(*typed) == 0
	case *[]PKISettingsRow:
		return len(*typed) == 0
	case *[]PKIAuthorityRow:
		return len(*typed) == 0
	case *[]PKIIdentityRow:
		return len(*typed) == 0
	case *[]PKICertificateRow:
		return len(*typed) == 0
	case *[]PKIEnrollmentTokenRow:
		return len(*typed) == 0
	case *[]PKIEnrollmentReplayRow:
		return len(*typed) == 0
	case *[]PKIConfirmationNonceRow:
		return len(*typed) == 0
	case *[]PKISecuritySnapshotRow:
		return len(*typed) == 0
	case *[]PKILifecycleJobRow:
		return len(*typed) == 0
	case *[]PKIEventRow:
		return len(*typed) == 0
	case *[]UserRow:
		return len(*typed) == 0
	case *[]SessionRow:
		return len(*typed) == 0
	case *[]RoleRow:
		return len(*typed) == 0
	case *[]PermissionRow:
		return len(*typed) == 0
	case *[]RolePermissionRow:
		return len(*typed) == 0
	case *[]RoleBindingRow:
		return len(*typed) == 0
	case *[]ResourceGroupRow:
		return len(*typed) == 0
	case *[]ResourceGroupGrantRow:
		return len(*typed) == 0
	case *[]ResourceBindingRow:
		return len(*typed) == 0
	case *[]QuotaPolicyRow:
		return len(*typed) == 0
	case *[]QuotaUsageRow:
		return len(*typed) == 0
	case *[]QuotaPolicyUsageRow:
		return len(*typed) == 0
	case *[]QuotaAllocationRow:
		return len(*typed) == 0
	case *[]AuditEventRow:
		return len(*typed) == 0
	case *[]SecretRow:
		return len(*typed) == 0
	case *[]SecretVersionRow:
		return len(*typed) == 0
	default:
		panic(fmt.Sprintf("unsupported migration rows %T", rows))
	}
}

// copyPKIMigrationRows copies one validated canonical graph, not a collection
// of independently committed tables. The process-local lease is intentionally
// omitted and running jobs are returned to pending without their old owner so
// the target control-plane instance must acquire a fresh lease before work can
// resume.
func copyPKIMigrationRows(ctx context.Context, source, target *GormStore) error {
	if !source.db.Migrator().HasTable(&PKISettingsRow{}) || !target.db.Migrator().HasTable(&PKISettingsRow{}) {
		return nil
	}
	state, err := source.LoadPKICanonicalState(ctx)
	if err != nil {
		return fmt.Errorf("load source canonical PKI state: %w", err)
	}
	if state.Settings == nil {
		return nil
	}
	if _, err := ValidateCanonicalPKISecuritySnapshot(state); err != nil {
		return fmt.Errorf("validate source canonical PKI security snapshot: %w", err)
	}
	identities := append([]PKIIdentityRow(nil), state.Identities...)
	for index := range identities {
		ownerKey, err := pkiIdentityOwnerKey(
			identities[index].PKIDomainID,
			identities[index].Kind,
			identities[index].AgentID,
			identities[index].ListenerID,
		)
		if err != nil {
			return fmt.Errorf("derive migrated PKI identity owner slot: %w", err)
		}
		identities[index].ActiveOwnerKey = nil
		if identities[index].State != PKIIdentityStateRevoked {
			identities[index].ActiveOwnerKey = &ownerKey
		}
	}
	jobs := append([]PKILifecycleJobRow(nil), state.LifecycleJobs...)
	for index := range jobs {
		jobs[index].LeaseOwner = ""
		jobs[index].LeaseDeadline = nil
		if jobs[index].State == PKILifecycleJobStateRunning {
			jobs[index].State = PKILifecycleJobStatePending
			jobs[index].NextAttemptAt = nil
		}
	}
	return target.writeTransaction(ctx, func(tx *gorm.DB) error {
		var targetSettings int64
		if err := tx.Model(&PKISettingsRow{}).Count(&targetSettings).Error; err != nil {
			return err
		}
		if targetSettings != 0 {
			return errors.New("target canonical PKI state is already initialised")
		}
		// The target write transaction serializes competing migrations. Files
		// become durable before canonical rows can reference them; a database
		// rollback therefore leaves only retry-safe identical files.
		if err := copyPKIVaultForMigration(state, source, target); err != nil {
			return err
		}
		rows := []any{
			state.Settings,
			&state.Authorities,
			&identities,
			&state.Certificates,
			&state.EnrollmentTokens,
			&state.EnrollmentReplays,
			&state.ConfirmationNonces,
			state.SecuritySnapshot,
			&jobs,
			&state.Events,
		}
		for _, row := range rows {
			if row == nil || isEmptyPKIMigrationValue(row) {
				continue
			}
			if err := tx.WithContext(ctx).Create(row).Error; err != nil {
				return err
			}
		}
		return validatePKICanonicalRelationships(ctx, tx, target.LocalAgentID())
	})
}

func isEmptyPKIMigrationValue(value any) bool {
	switch typed := value.(type) {
	case *[]PKIAuthorityRow:
		return len(*typed) == 0
	case *[]PKIIdentityRow:
		return len(*typed) == 0
	case *[]PKICertificateRow:
		return len(*typed) == 0
	case *[]PKIEnrollmentTokenRow:
		return len(*typed) == 0
	case *[]PKIEnrollmentReplayRow:
		return len(*typed) == 0
	case *[]PKIConfirmationNonceRow:
		return len(*typed) == 0
	case *[]PKILifecycleJobRow:
		return len(*typed) == 0
	case *[]PKIEventRow:
		return len(*typed) == 0
	default:
		return false
	}
}

func copyPKIVaultForMigration(state PKICanonicalState, source, target *GormStore) error {
	if filepath.Clean(source.dataRoot) == filepath.Clean(target.dataRoot) {
		return nil
	}
	sourcePKIRoot := filepath.Join(source.dataRoot, "pki")
	targetPKIRoot := filepath.Join(target.dataRoot, "pki")
	targetVault := filepath.Join(targetPKIRoot, "vault")
	for _, directory := range []string{target.dataRoot, targetPKIRoot, targetVault} {
		if err := ensurePKIMigrationDirectory(directory); err != nil {
			return fmt.Errorf("secure target PKI directory %s: %w", directory, err)
		}
	}
	masterKey := filepath.Join(sourcePKIRoot, "master.key")
	if info, err := os.Lstat(masterKey); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("source PKI master key is not a regular file")
		}
		if err := copyPKIFileForMigration(masterKey, filepath.Join(targetPKIRoot, "master.key")); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, authority := range state.Authorities {
		if authority.EncryptedKeyRef == nil {
			continue
		}
		reference := strings.TrimSpace(*authority.EncryptedKeyRef)
		if reference == "" || filepath.Base(reference) != reference {
			return errors.New("canonical PKI authority has an invalid vault reference")
		}
		if err := copyPKIFileForMigration(
			filepath.Join(sourcePKIRoot, "vault", reference),
			filepath.Join(targetVault, reference),
		); err != nil {
			return fmt.Errorf("copy PKI vault record %s: %w", reference, err)
		}
	}
	return nil
}

func copyPKIFileForMigration(sourcePath, targetPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("source PKI vault record is not a regular file")
	}
	sourceValue, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	defer clear(sourceValue)
	if targetInfo, err := os.Lstat(targetPath); err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
			return errors.New("target PKI vault record is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if existing, err := os.ReadFile(targetPath); err == nil {
		if !bytes.Equal(existing, sourceValue) {
			// A killed pre-staging implementation may have left a strict prefix.
			// It is safe to repair only that identifiable truncated orphan while
			// the caller holds an empty-target migration transaction.
			if len(existing) >= len(sourceValue) || !bytes.HasPrefix(sourceValue, existing) {
				return errors.New("target PKI vault record already exists with different contents")
			}
			if err := os.Remove(targetPath); err != nil {
				return fmt.Errorf("remove truncated target PKI vault record: %w", err)
			}
		} else {
			return cleanupPKIMigrationStaging(filepath.Dir(targetPath), filepath.Base(targetPath))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)
	if err := cleanupPKIMigrationStaging(directory, base); err != nil {
		return err
	}
	staging, err := os.CreateTemp(directory, "."+base+".nre-migrate-")
	if err != nil {
		return err
	}
	stagingPath := staging.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(stagingPath)
		}
	}()
	if err := staging.Chmod(0o600); err != nil {
		_ = staging.Close()
		return err
	}
	_, writeErr := staging.Write(sourceValue)
	syncErr := staging.Sync()
	closeErr := staging.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Link(stagingPath, targetPath); err != nil {
		if existing, readErr := os.ReadFile(targetPath); readErr == nil && bytes.Equal(existing, sourceValue) {
			_ = os.Remove(stagingPath)
			return nil
		}
		return fmt.Errorf("atomically publish target PKI vault record: %w", err)
	}
	if err := syncPKIMigrationDirectory(directory); err != nil {
		return err
	}
	if err := os.Remove(stagingPath); err != nil {
		return err
	}
	published = true
	return syncPKIMigrationDirectory(directory)
}

func ensurePKIMigrationDirectory(path string) error {
	if err := rejectPKIMigrationSymlinkComponents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a real directory")
	}
	return os.Chmod(path, 0o700)
}

func rejectPKIMigrationSymlinkComponents(path string) error {
	current, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("target PKI path component %s is a symbolic link", current)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func cleanupPKIMigrationStaging(directory, base string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	prefix := "." + base + ".nre-migrate-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func syncPKIMigrationDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func copySharedMigrationRows(ctx context.Context, source, target *GormStore) error {
	relayRows := make([]RelayListenerRow, 0)
	if source.db.Migrator().HasTable(&RelayListenerRow{}) {
		if err := source.db.WithContext(ctx).Order("id").Find(&relayRows).Error; err != nil {
			return err
		}
	}
	supportedRelayRows, excludedRelayIDs := partitionSnapshotRelayRows(relayRows)

	egressRows := make([]EgressProfileRow, 0)
	if source.db.Migrator().HasTable(&EgressProfileRow{}) {
		if err := source.db.WithContext(ctx).Order("id").Find(&egressRows).Error; err != nil {
			return err
		}
	}
	supportedEgressRows, excludedEgressIDs := partitionSnapshotEgressRows(egressRows)

	httpRows := make([]HTTPRuleRow, 0)
	if source.db.Migrator().HasTable(&HTTPRuleRow{}) {
		if err := source.db.WithContext(ctx).Order("agent_id, id").Find(&httpRows).Error; err != nil {
			return err
		}
	}
	httpRows = filterHTTPRuleRowsForSnapshot(httpRows, excludedRelayIDs, excludedEgressIDs)

	l4Rows := make([]L4RuleRow, 0)
	if source.db.Migrator().HasTable(&L4RuleRow{}) {
		if err := source.db.WithContext(ctx).Order("agent_id, id").Find(&l4Rows).Error; err != nil {
			return err
		}
	}
	l4Rows = filterL4RuleRowsForMigration(l4Rows, excludedRelayIDs, excludedEgressIDs)

	for _, item := range []struct {
		model any
		rows  any
	}{
		{model: &RelayListenerRow{}, rows: &supportedRelayRows},
		{model: &HTTPRuleRow{}, rows: &httpRows},
		{model: &L4RuleRow{}, rows: &l4Rows},
	} {
		if !target.db.Migrator().HasTable(item.model) || isEmptyMigrationSlice(item.rows) {
			continue
		}
		conflict, err := migrationUpsertClause(ctx, target, item.model)
		if err != nil {
			return err
		}
		if err := target.db.WithContext(ctx).Clauses(conflict).Create(item.rows).Error; err != nil {
			return err
		}
	}

	if !target.db.Migrator().HasTable(&EgressProfileRow{}) || len(supportedEgressRows) == 0 {
		return nil
	}
	payload := make([]map[string]any, 0, len(supportedEgressRows))
	for _, row := range supportedEgressRows {
		payload = append(payload, egressProfileRowPayload(row))
	}
	conflict, err := migrationUpsertClause(ctx, target, &EgressProfileRow{})
	if err != nil {
		return err
	}
	return target.db.WithContext(ctx).
		Model(&EgressProfileRow{}).
		Clauses(conflict).
		Create(&payload).Error
}

func filterL4RuleRowsForMigration(rows []L4RuleRow, excludedRelayIDs, excludedEgressIDs map[int]struct{}) []L4RuleRow {
	filtered := make([]L4RuleRow, 0, len(rows))
	for _, row := range rows {
		switch strings.ToLower(strings.TrimSpace(row.ListenMode)) {
		case "", "tcp", "proxy":
		default:
			continue
		}
		if snapshotRuleReferencesExcludedResource(row.RelayLayersJSON, row.EgressProfileID, excludedRelayIDs, excludedEgressIDs) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func copyTrafficPolicies(ctx context.Context, source, target *GormStore) error {
	if !source.db.Migrator().HasTable(&AgentTrafficPolicyRow{}) || !target.db.Migrator().HasTable(&AgentTrafficPolicyRow{}) {
		return nil
	}
	rows, err := source.ListTrafficPolicies(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := target.SaveTrafficPolicy(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func copyTrafficBaselines(ctx context.Context, source, target *GormStore) error {
	if !source.db.Migrator().HasTable(&AgentTrafficBaselineRow{}) || !target.db.Migrator().HasTable(&AgentTrafficBaselineRow{}) {
		return nil
	}
	rows, err := source.ListTrafficBaselines(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := target.SaveTrafficBaseline(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

type managedCertificateMigrationGeneration struct {
	row      ManagedCertificateGenerationRow
	manifest managedCertificateGenerationManifest
	bundle   ManagedCertificateBundle
}

func copyManagedCertificateMaterials(ctx context.Context, source, target *GormStore) error {
	certs, err := source.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	certificateDomains := make(map[string]struct{}, len(certs))
	for _, cert := range certs {
		certificateDomains[strings.TrimSpace(cert.Domain)] = struct{}{}
	}
	rowsByDomain := make(map[string][]ManagedCertificateGenerationRow)
	if source.db.Migrator().HasTable(&ManagedCertificateGenerationRow{}) && target.db.Migrator().HasTable(&ManagedCertificateGenerationRow{}) {
		var rows []ManagedCertificateGenerationRow
		if err := source.db.WithContext(ctx).Order("domain, created_at, id").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if _, ok := certificateDomains[row.Domain]; !ok {
				continue
			}
			rowsByDomain[row.Domain] = append(rowsByDomain[row.Domain], row)
		}
	}
	for _, cert := range certs {
		domain := strings.TrimSpace(cert.Domain)
		unlock := target.lockManagedCertificateDomain(domain)
		err := copyManagedCertificateMaterialDomainLocked(ctx, source, target, cert, rowsByDomain[domain])
		unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func copyManagedCertificateMaterialDomainLocked(ctx context.Context, source, target *GormStore, cert ManagedCertificateRow, rows []ManagedCertificateGenerationRow) error {
	domain := strings.TrimSpace(cert.Domain)
	if err := target.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	graph, graphErr := loadManagedCertificateMigrationGraph(source, cert, rows)
	if graphErr == nil && len(graph) != 0 {
		generationRows := make([]ManagedCertificateGenerationRow, 0, len(graph))
		installedIDs := make([]string, 0, len(graph))
		cleanup := func() error {
			return cleanupManagedCertificateMigrationDirectories(target, domain, installedIDs)
		}
		for _, generation := range graph {
			installed, err := target.installManagedCertificateGeneration(generation.manifest, generation.bundle)
			if err != nil {
				return errors.Join(fmt.Errorf("install managed certificate generation %s: %w", generation.row.ID, err), cleanup())
			}
			if installed {
				installedIDs = append(installedIDs, generation.row.ID)
			}
			generationRows = append(generationRows, generation.row)
		}
		restore, err := commitManagedCertificateMigrationState(ctx, target, cert, generationRows)
		if err != nil {
			return errors.Join(err, cleanup())
		}
		return reconcileManagedCertificateMigrationCommitLocked(ctx, target, domain, restore, cleanup)
	}
	material, ok, err := source.readManagedCertificateMaterialSecure(domain)
	if err != nil {
		return err
	}
	if !ok {
		if graphErr != nil {
			return fmt.Errorf("managed certificate generation graph for %s is invalid and no legacy material is available: %w", domain, graphErr)
		}
		restore, err := commitManagedCertificateMigrationState(ctx, target, cert, nil)
		if err != nil {
			return err
		}
		return reconcileManagedCertificateMigrationCommitLocked(ctx, target, domain, restore, func() error { return nil })
	}
	bundle := ManagedCertificateBundle{Domain: domain, CertPEM: material.CertPEM, KeyPEM: material.KeyPEM}
	legacyGeneration, installed, err := installManagedCertificateMigrationLegacyGeneration(ctx, target, domain, bundle)
	if err != nil {
		return err
	}
	installedIDs := make([]string, 0, 1)
	if installed {
		installedIDs = append(installedIDs, legacyGeneration.ID)
	}
	cleanup := func() error {
		return cleanupManagedCertificateMigrationDirectories(target, domain, installedIDs)
	}
	cert.ActiveGenerationID = legacyGeneration.ID
	cert.PendingGenerationID = ""
	restore, err := commitManagedCertificateMigrationState(ctx, target, cert, []ManagedCertificateGenerationRow{legacyGeneration})
	if err != nil {
		return errors.Join(err, cleanup())
	}
	return reconcileManagedCertificateMigrationCommitLocked(ctx, target, domain, restore, cleanup)
}

func installManagedCertificateMigrationLegacyGeneration(ctx context.Context, target *GormStore, domain string, bundle ManagedCertificateBundle) (ManagedCertificateGenerationRow, bool, error) {
	bundle.Domain = domain
	materialHash := managedCertificateGenerationMaterialHash(bundle)
	generationID := managedCertificateLegacyGenerationID(domain, materialHash)
	var existingRow ManagedCertificateGenerationRow
	existingRowErr := target.db.WithContext(ctx).Where("id = ?", generationID).First(&existingRow).Error
	if existingRowErr != nil && !errors.Is(existingRowErr, gorm.ErrRecordNotFound) {
		return ManagedCertificateGenerationRow{}, false, existingRowErr
	}
	if existingRowErr == nil && (existingRow.Domain != domain || existingRow.MaterialHash != materialHash) {
		return ManagedCertificateGenerationRow{}, false, ErrManagedCertificateGenerationHashMismatch
	}

	installed := false
	manifest, existingBundle, readErr := target.readManagedCertificateGeneration(domain, generationID)
	if readErr == nil {
		if existingBundle.CertPEM != bundle.CertPEM || existingBundle.KeyPEM != bundle.KeyPEM || manifest.MaterialHash != materialHash {
			return ManagedCertificateGenerationRow{}, false, ErrManagedCertificateGenerationHashMismatch
		}
	} else {
		if _, err := os.Lstat(target.managedCertificateGenerationDirectory(domain, generationID)); err == nil {
			return ManagedCertificateGenerationRow{}, false, fmt.Errorf("legacy managed certificate generation destination is invalid: %w", readErr)
		} else if !errors.Is(err, os.ErrNotExist) {
			return ManagedCertificateGenerationRow{}, false, err
		}
		createdAt := time.Now().UTC().Format(time.RFC3339Nano)
		if existingRowErr == nil && strings.TrimSpace(existingRow.CreatedAt) != "" {
			createdAt = existingRow.CreatedAt
		}
		manifest = managedCertificateGenerationManifest{
			Version:      managedCertificateGenerationManifestVersion,
			ID:           generationID,
			Domain:       domain,
			MaterialHash: materialHash,
			CertSHA256:   managedCertificateGenerationValueHash(bundle.CertPEM),
			KeySHA256:    managedCertificateGenerationValueHash(bundle.KeyPEM),
			CreatedAt:    createdAt,
		}
		newlyInstalled, installErr := target.installManagedCertificateGeneration(manifest, bundle)
		if installErr != nil {
			return ManagedCertificateGenerationRow{}, false, installErr
		}
		installed = newlyInstalled
	}
	promotedAt := manifest.CreatedAt
	if existingRowErr == nil && strings.TrimSpace(existingRow.PromotedAt) != "" {
		promotedAt = existingRow.PromotedAt
	}
	row := ManagedCertificateGenerationRow{
		ID:           generationID,
		Domain:       domain,
		State:        ManagedCertificateGenerationStateActive,
		MaterialHash: materialHash,
		CreatedAt:    manifest.CreatedAt,
		PromotedAt:   promotedAt,
	}
	return row, installed, nil
}

func commitManagedCertificateMigrationState(ctx context.Context, target *GormStore, row ManagedCertificateRow, generationRows []ManagedCertificateGenerationRow) (func() error, error) {
	normalizeManagedCertificateRow(&row)
	var previous ManagedCertificateRow
	previousFound := false
	var previousDomainRows []ManagedCertificateGenerationRow
	err := target.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", row.ID).First(&previous).Error; err == nil {
			previousFound = true
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Where("domain = ?", row.Domain).Order("created_at, id").Find(&previousDomainRows).Error; err != nil {
			return err
		}
		incomingIDs := make(map[string]struct{}, len(generationRows))
		for _, generation := range generationRows {
			incomingIDs[generation.ID] = struct{}{}
			var existing ManagedCertificateGenerationRow
			if err := tx.Where("id = ?", generation.ID).First(&existing).Error; err == nil {
				if existing.Domain != generation.Domain || existing.MaterialHash != generation.MaterialHash {
					return ErrManagedCertificateGenerationHashMismatch
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		for _, existing := range previousDomainRows {
			if _, incoming := incomingIDs[existing.ID]; incoming {
				continue
			}
			nextState := existing.State
			if strings.TrimSpace(row.ActiveGenerationID) == "" {
				switch existing.State {
				case ManagedCertificateGenerationStateActive,
					ManagedCertificateGenerationStatePending,
					ManagedCertificateGenerationStateSuperseded:
					nextState = managedCertificateGenerationStateInvalid
				}
			} else {
				switch existing.State {
				case ManagedCertificateGenerationStateActive:
					nextState = ManagedCertificateGenerationStateSuperseded
				case ManagedCertificateGenerationStatePending:
					nextState = managedCertificateGenerationStateInvalid
				}
			}
			if nextState != existing.State {
				if err := tx.Model(&ManagedCertificateGenerationRow{}).
					Where("id = ? AND domain = ?", existing.ID, row.Domain).
					Update("state", nextState).Error; err != nil {
					return err
				}
			}
		}
		for _, generation := range generationRows {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&generation).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&row).Error
	})
	if err != nil {
		return nil, err
	}
	restore := func() error {
		return target.writeTransaction(ctx, func(tx *gorm.DB) error {
			if err := tx.Where("domain = ?", row.Domain).Delete(&ManagedCertificateGenerationRow{}).Error; err != nil {
				return err
			}
			if len(previousDomainRows) != 0 {
				if err := tx.Create(&previousDomainRows).Error; err != nil {
					return err
				}
			}
			if previousFound {
				return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&previous).Error
			}
			return tx.Delete(&ManagedCertificateRow{}, "id = ?", row.ID).Error
		})
	}
	return restore, nil
}

func reconcileManagedCertificateMigrationCommitLocked(ctx context.Context, target *GormStore, domain string, restore, cleanup func() error) error {
	if err := target.reconcileManagedCertificateGenerationsLocked(ctx, domain); err != nil {
		restoreErr := restore()
		var cleanupErr error
		if restoreErr == nil {
			cleanupErr = cleanup()
		}
		repairErr := target.reconcileManagedCertificateGenerationsLocked(ctx, domain)
		return errors.Join(err, restoreErr, cleanupErr, repairErr)
	}
	return nil
}

func cleanupManagedCertificateMigrationDirectories(target *GormStore, domain string, generationIDs []string) error {
	var cleanupErr error
	for i := len(generationIDs) - 1; i >= 0; i-- {
		cleanupErr = errors.Join(cleanupErr, target.removeManagedCertificateGenerationDirectory(domain, generationIDs[i]))
	}
	cleanupErr = errors.Join(cleanupErr, target.cleanManagedCertificateGenerationStagingDirectories(domain))
	return cleanupErr
}

func loadManagedCertificateMigrationGraph(source *GormStore, certificate ManagedCertificateRow, rows []ManagedCertificateGenerationRow) ([]managedCertificateMigrationGeneration, error) {
	if len(rows) == 0 {
		if strings.TrimSpace(certificate.ActiveGenerationID) != "" || strings.TrimSpace(certificate.PendingGenerationID) != "" {
			return nil, errors.New("managed certificate generation pointers have no generation rows")
		}
		return nil, nil
	}
	domain := strings.TrimSpace(certificate.Domain)
	graph := make([]managedCertificateMigrationGeneration, 0, len(rows))
	rowsByID := make(map[string]ManagedCertificateGenerationRow, len(rows))
	for _, row := range rows {
		manifest, bundle, err := source.readManagedCertificateGeneration(domain, row.ID)
		if err != nil {
			return nil, fmt.Errorf("read managed certificate generation %s: %w", row.ID, err)
		}
		if row.Domain != domain || manifest.MaterialHash != row.MaterialHash || manifest.CreatedAt != row.CreatedAt {
			return nil, fmt.Errorf("managed certificate generation %s metadata mismatch", row.ID)
		}
		switch row.State {
		case ManagedCertificateGenerationStateActive,
			ManagedCertificateGenerationStatePending,
			ManagedCertificateGenerationStateSuperseded,
			managedCertificateGenerationStateInvalid:
		default:
			return nil, fmt.Errorf("managed certificate generation %s has unsupported state %q", row.ID, row.State)
		}
		rowsByID[row.ID] = row
		graph = append(graph, managedCertificateMigrationGeneration{row: row, manifest: manifest, bundle: bundle})
	}
	activeID := strings.TrimSpace(certificate.ActiveGenerationID)
	pendingID := strings.TrimSpace(certificate.PendingGenerationID)
	if activeID != "" {
		row, ok := rowsByID[activeID]
		if !ok || row.State != ManagedCertificateGenerationStateActive {
			return nil, errors.New("managed certificate active generation pointer is incomplete")
		}
	}
	if pendingID != "" {
		row, ok := rowsByID[pendingID]
		if !ok || row.State != ManagedCertificateGenerationStatePending {
			return nil, errors.New("managed certificate pending generation pointer is incomplete")
		}
	}
	for _, row := range rows {
		if row.State == ManagedCertificateGenerationStateActive && row.ID != activeID {
			return nil, errors.New("managed certificate generation graph has an unpointed active row")
		}
		if row.State == ManagedCertificateGenerationStatePending && row.ID != pendingID {
			return nil, errors.New("managed certificate generation graph has an unpointed pending row")
		}
	}
	return graph, nil
}
