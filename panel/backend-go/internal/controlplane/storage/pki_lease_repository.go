package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PKIInstanceLeaseStateHeld         = "held"
	PKIInstanceLeaseStateRelinquished = "relinquished"
)

// PKIInstanceLeaseSnapshot is a transactionally consistent view of the PKI
// settings singleton and its cooperative single-active lease. LeaseTerm is a
// unique fencing token for one acquisition; it deliberately survives renewal
// and changes on every reacquisition, including by the same InstanceID.
type PKIInstanceLeaseSnapshot struct {
	Exists        bool
	PKIDomainID   string
	PKIEpoch      int64
	InstanceID    string
	LeaseTerm     string
	LeaseDeadline time.Time
	State         string
}

func (s *GormStore) ReadPKIInstanceLease(ctx context.Context) (PKIInstanceLeaseSnapshot, error) {
	if s == nil || s.db == nil {
		return PKIInstanceLeaseSnapshot{}, fmt.Errorf("PKI lease store is unavailable")
	}
	if s.transactionScoped {
		return loadPKIInstanceLeaseSnapshot(ctx, s.db)
	}
	var snapshot PKIInstanceLeaseSnapshot
	options := &sql.TxOptions{ReadOnly: true}
	if s.driver == "postgres" || s.driver == "mysql" {
		options.Isolation = sql.LevelRepeatableRead
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		snapshot, err = loadPKIInstanceLeaseSnapshot(ctx, tx)
		return err
	}, options)
	if err != nil {
		return PKIInstanceLeaseSnapshot{}, err
	}
	return snapshot, nil
}

func (s *GormStore) TryAcquirePKIInstanceLease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	now time.Time,
	deadline time.Time,
) (PKIInstanceLeaseSnapshot, bool, error) {
	instanceID = strings.TrimSpace(instanceID)
	leaseTerm = strings.TrimSpace(leaseTerm)
	if instanceID == "" || leaseTerm == "" || now.IsZero() || !deadline.After(now) {
		return PKIInstanceLeaseSnapshot{}, false, pkiInvariant("PKI lease acquisition fields are incomplete")
	}
	var snapshot PKIInstanceLeaseSnapshot
	var acquired bool
	err := s.withPKILeaseWrite(ctx, func(tx *gorm.DB) error {
		settings, err := loadPKILeaseSettingsForUpdate(ctx, tx)
		if err != nil {
			return err
		}
		updates := map[string]any{
			"pki_domain_id":  settings.PKIDomainID,
			"instance_id":    instanceID,
			"lease_term":     leaseTerm,
			"lease_deadline": deadline,
			"pki_epoch":      settings.PKIEpoch,
			"state":          PKIInstanceLeaseStateHeld,
			"updated_at":     now,
		}
		result := tx.WithContext(ctx).
			Model(&PKIInstanceLeaseRow{}).
			Where(
				"id = ? AND (pki_domain_id <> ? OR pki_epoch <> ? OR state <> ? OR lease_deadline <= ? OR instance_id = ?)",
				PKILeaseSingletonID,
				settings.PKIDomainID,
				settings.PKIEpoch,
				PKIInstanceLeaseStateHeld,
				now,
				instanceID,
			).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		acquired = result.RowsAffected == 1
		if !acquired {
			row := PKIInstanceLeaseRow{
				ID: PKILeaseSingletonID, PKIDomainID: settings.PKIDomainID,
				InstanceID: instanceID, LeaseTerm: leaseTerm, LeaseDeadline: deadline,
				PKIEpoch: settings.PKIEpoch, State: PKIInstanceLeaseStateHeld, UpdatedAt: now,
			}
			created := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if created.Error != nil {
				return created.Error
			}
			acquired = created.RowsAffected == 1
		}
		snapshot, err = loadPKIInstanceLeaseSnapshotWithSettings(ctx, tx, settings)
		return err
	})
	if err != nil {
		return PKIInstanceLeaseSnapshot{}, false, err
	}
	return snapshot, acquired, nil
}

func (s *GormStore) RenewPKIInstanceLease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	epoch int64,
	now time.Time,
	deadline time.Time,
) (PKIInstanceLeaseSnapshot, bool, error) {
	instanceID = strings.TrimSpace(instanceID)
	leaseTerm = strings.TrimSpace(leaseTerm)
	if instanceID == "" || leaseTerm == "" || epoch < 0 || now.IsZero() || !deadline.After(now) {
		return PKIInstanceLeaseSnapshot{}, false, pkiInvariant("PKI lease renewal fields are incomplete")
	}
	var snapshot PKIInstanceLeaseSnapshot
	var renewed bool
	err := s.withPKILeaseWrite(ctx, func(tx *gorm.DB) error {
		settings, err := loadPKILeaseSettingsForUpdate(ctx, tx)
		if err != nil {
			return err
		}
		if epoch != settings.PKIEpoch {
			snapshot = PKIInstanceLeaseSnapshot{PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch}
			return nil
		}
		result := tx.WithContext(ctx).
			Model(&PKIInstanceLeaseRow{}).
			Where(
				"id = ? AND pki_domain_id = ? AND pki_epoch = ? AND instance_id = ? AND lease_term = ? AND state = ? AND lease_deadline > ?",
				PKILeaseSingletonID,
				settings.PKIDomainID,
				epoch,
				instanceID,
				leaseTerm,
				PKIInstanceLeaseStateHeld,
				now,
			).
			Updates(map[string]any{"lease_deadline": deadline, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		renewed = result.RowsAffected == 1
		snapshot, err = loadPKIInstanceLeaseSnapshotWithSettings(ctx, tx, settings)
		return err
	})
	if err != nil {
		return PKIInstanceLeaseSnapshot{}, false, err
	}
	return snapshot, renewed, nil
}

func (s *GormStore) RelinquishPKIInstanceLease(
	ctx context.Context,
	instanceID string,
	leaseTerm string,
	epoch int64,
	now time.Time,
) (bool, error) {
	instanceID = strings.TrimSpace(instanceID)
	leaseTerm = strings.TrimSpace(leaseTerm)
	if instanceID == "" || leaseTerm == "" || epoch < 0 || now.IsZero() {
		return false, pkiInvariant("PKI lease relinquish fields are incomplete")
	}
	var relinquished bool
	err := s.withPKILeaseWrite(ctx, func(tx *gorm.DB) error {
		settings, err := loadPKILeaseSettingsForUpdate(ctx, tx)
		if err != nil {
			return err
		}
		if epoch != settings.PKIEpoch {
			return nil
		}
		result := tx.WithContext(ctx).
			Model(&PKIInstanceLeaseRow{}).
			Where(
				"id = ? AND pki_domain_id = ? AND pki_epoch = ? AND instance_id = ? AND lease_term = ? AND state = ?",
				PKILeaseSingletonID,
				settings.PKIDomainID,
				epoch,
				instanceID,
				leaseTerm,
				PKIInstanceLeaseStateHeld,
			).
			Updates(map[string]any{
				"state": PKIInstanceLeaseStateRelinquished, "lease_deadline": now, "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		relinquished = result.RowsAffected == 1
		return nil
	})
	return relinquished, err
}

func (s *GormStore) withPKILeaseWrite(ctx context.Context, mutate func(*gorm.DB) error) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("PKI lease store is unavailable")
	}
	if s.transactionScoped {
		return mutate(s.db.WithContext(ctx))
	}
	return s.writeTransaction(ctx, mutate)
}

func loadPKILeaseSettingsForUpdate(ctx context.Context, db *gorm.DB) (PKISettingsRow, error) {
	var settings PKISettingsRow
	err := db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&settings, PKISettingsSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PKISettingsRow{}, pkiInvariant("PKI settings must be initialized before lease acquisition")
	}
	if err != nil {
		return PKISettingsRow{}, err
	}
	if strings.TrimSpace(settings.PKIDomainID) == "" || settings.PKIEpoch < 0 {
		return PKISettingsRow{}, pkiInvariant("PKI lease settings are invalid")
	}
	return settings, nil
}

func loadPKIInstanceLeaseSnapshot(ctx context.Context, db *gorm.DB) (PKIInstanceLeaseSnapshot, error) {
	var settings PKISettingsRow
	if err := db.WithContext(ctx).First(&settings, PKISettingsSingletonID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return PKIInstanceLeaseSnapshot{}, nil
	} else if err != nil {
		return PKIInstanceLeaseSnapshot{}, err
	}
	return loadPKIInstanceLeaseSnapshotWithSettings(ctx, db, settings)
}

func loadPKIInstanceLeaseSnapshotWithSettings(ctx context.Context, db *gorm.DB, settings PKISettingsRow) (PKIInstanceLeaseSnapshot, error) {
	snapshot := PKIInstanceLeaseSnapshot{PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch}
	var row PKIInstanceLeaseRow
	err := db.WithContext(ctx).First(&row, PKILeaseSingletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return snapshot, nil
	}
	if err != nil {
		return PKIInstanceLeaseSnapshot{}, err
	}
	snapshot.Exists = true
	snapshot.InstanceID = row.InstanceID
	snapshot.LeaseTerm = row.LeaseTerm
	snapshot.LeaseDeadline = row.LeaseDeadline
	snapshot.State = row.State
	if row.PKIDomainID != settings.PKIDomainID || row.PKIEpoch != settings.PKIEpoch {
		return PKIInstanceLeaseSnapshot{}, pkiInvariant("PKI lease domain or epoch does not match settings")
	}
	return snapshot, nil
}
