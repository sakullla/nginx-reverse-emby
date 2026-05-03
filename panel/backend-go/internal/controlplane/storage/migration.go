package storage

import (
	"context"
	"fmt"

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
		&HTTPRuleRow{},
		&L4RuleRow{},
		&RelayListenerRow{},
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
	return target.db.WithContext(ctx).
		Clauses(clause.OnConflict{UpdateAll: true}).
		Create(rows).Error
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
