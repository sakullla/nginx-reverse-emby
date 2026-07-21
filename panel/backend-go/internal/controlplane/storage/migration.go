package storage

import (
	"context"
	"fmt"
	"strings"

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
		&ManagedCertificateRow{},
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

func copyManagedCertificateMaterials(ctx context.Context, source, target *GormStore) error {
	certs, err := source.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	for _, cert := range certs {
		material, ok, err := source.LoadManagedCertificateMaterial(ctx, cert.Domain)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := target.SaveManagedCertificateMaterial(ctx, cert.Domain, material); err != nil {
			return err
		}
	}
	return nil
}
