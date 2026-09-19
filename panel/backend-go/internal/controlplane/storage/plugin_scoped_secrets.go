package storage

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Scoped-secret metadata is distinct from plugin state. These records contain
// references and a keyed Vault fingerprint only; never plaintext or wire bodies.
type PluginScopedSecretOperationRow struct {
	ID          string `gorm:"primaryKey;size:64"`
	SecretName  string `gorm:"index;size:190;not null"`
	Action      string `gorm:"size:16;not null"`
	OldVersion  string `gorm:"size:64"`
	Fingerprint string `gorm:"size:64"`
	State       string `gorm:"size:16;not null"`
	NewVersion  string `gorm:"size:64"`
	CreatedAt   time.Time
}

type PluginScopedSecretDeliveryRow struct {
	ID                   string `gorm:"primaryKey;size:64"`
	SecretName           string `gorm:"index;size:190;not null"`
	Version              string `gorm:"size:64;not null"`
	AgentID              string `gorm:"size:64"`
	InstanceID           string `gorm:"index;size:190;not null"`
	PluginID             string `gorm:"size:190;not null"`
	GenerationID         string `gorm:"index;size:190;not null"`
	ProviderGenerationID string `gorm:"size:190"`
	Revision             int64
	FenceID              string `gorm:"size:64"`
	Acknowledged         bool
}

func (s *GormStore) GetScopedSecretOperation(ctx context.Context, id string) (PluginScopedSecretOperationRow, bool, error) {
	var row PluginScopedSecretOperationRow
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) PutScopedSecretOperation(ctx context.Context, row PluginScopedSecretOperationRow) error {
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *GormStore) HasOtherPendingScopedSecretOperation(ctx context.Context, name, id string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&PluginScopedSecretOperationRow{}).Where("secret_name = ? AND state = ? AND id <> ?", name, "pending", id).Count(&count).Error
	return count != 0, err
}

func (s *GormStore) ScopedSecretReadBlocked(ctx context.Context, name, agentID, instanceID, generation string) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&PluginScopedSecretOperationRow{}).Where("secret_name = ? AND state = ?", name, "pending").Count(&count).Error; err != nil || count != 0 {
		return count != 0, err
	}
	return s.ScopedSecretGenerationFenced(ctx, agentID, instanceID, generation)
}

func (s *GormStore) ScopedSecretGenerationFenced(ctx context.Context, agentID, instanceID, generation string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&PluginScopedSecretDeliveryRow{}).
		Where("agent_id = ? AND instance_id = ? AND generation_id = ? AND fence_id <> ''", agentID, instanceID, generation).Count(&count).Error
	return count != 0, err
}

func (s *GormStore) RecordScopedSecretDelivery(ctx context.Context, row PluginScopedSecretDeliveryRow) error {
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (s *GormStore) FenceScopedSecretDeliveries(ctx context.Context, name, version, fenceID string) error {
	return s.db.WithContext(ctx).Model(&PluginScopedSecretDeliveryRow{}).
		Where("secret_name = ? AND version = ? AND fence_id = ''", name, version).Update("fence_id", fenceID).Error
}

func (s *GormStore) PendingScopedSecretDeliveries(ctx context.Context, name, version string) ([]PluginScopedSecretDeliveryRow, error) {
	var rows []PluginScopedSecretDeliveryRow
	err := s.db.WithContext(ctx).Where("secret_name = ? AND version = ? AND acknowledged = ?", name, version, false).Order("id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) AcknowledgeScopedSecretDelivery(ctx context.Context, id, fenceID string) error {
	result := s.db.WithContext(ctx).Model(&PluginScopedSecretDeliveryRow{}).Where("id = ? AND fence_id = ?", id, fenceID).Update("acknowledged", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("scoped secret delivery fence changed")
	}
	return nil
}

func ensureLocalScopedSecretGenerationUnfenced(tx *gorm.DB, instanceID, generation string) error {
	var count int64
	err := tx.Model(&PluginScopedSecretDeliveryRow{}).Where("agent_id = '' AND instance_id = ? AND generation_id = ? AND fence_id <> ''", instanceID, generation).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("plugin generation is fenced by secret revocation")
	}
	return nil
}

// GetSecretByName includes retired identities: create cannot revive a revoked
// reference. The unique name constraint also serializes concurrent creates.
func (s *GormStore) GetSecretByName(ctx context.Context, name string) (SecretRow, bool, error) {
	var row SecretRow
	query := s.db.WithContext(ctx)
	if s.transactionScoped {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where("name = ?", name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SecretRow{}, false, nil
	}
	return row, err == nil, err
}

// RetireScopedSecret compares the exact live version inside the caller's
// security transaction and destroys its encrypted versions. A stale revoke
// cannot retire a concurrently replaced secret.
func (s *GormStore) RetireScopedSecret(ctx context.Context, id string, version uint64, at time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&SecretRow{}).Where("id = ? AND active_version = ? AND retired_at IS NULL", id, version).Update("retired_at", at)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("scoped secret version is unknown or revoked")
		}
		return tx.Model(&SecretVersionRow{}).Where("secret_id = ? AND destroyed_at IS NULL", id).
			Updates(map[string]any{"destroyed_at": at, "ciphertext": []byte{}, "nonce": []byte{}}).Error
	})
}
