package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	}
	for _, table := range tables {
		if err := copyRows(ctx, source, target, table); err != nil {
			return err
		}
	}
	if err := copySharedMigrationRows(ctx, source, target); err != nil {
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
	default:
		panic(fmt.Sprintf("unsupported migration rows %T", rows))
	}
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
