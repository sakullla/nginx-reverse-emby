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

// Rotations can finish stopping recipients after a restart, but replacing the
// value requires the caller to replay its original material.
func (s *GormStore) InterruptedScopedSecretOperations(ctx context.Context) ([]PluginScopedSecretOperationRow, error) {
	var rows []PluginScopedSecretOperationRow
	err := s.db.WithContext(ctx).Where("state = ?", "pending").
		Where("action = ? OR (action = ? AND EXISTS (SELECT 1 FROM plugin_scoped_secret_delivery_rows d WHERE d.fence_id = plugin_scoped_secret_operation_rows.id AND d.acknowledged = ?))", "revoke", "rotate", false).
		Order("created_at, id").Find(&rows).Error
	return rows, err
}

// Only a desired generation that was actually fenced needs a successor. The
// exact local generation and remote provider generation stay in the ledger
// across a host restart, including a crash after the last ACK and retirement.
func (s *GormStore) ScopedSecretSuccessorInstances(ctx context.Context, instanceID string) ([]string, error) {
	query := s.db.WithContext(ctx).Table("plugin_scoped_secret_delivery_rows AS d").
		Joins("JOIN plugin_scoped_secret_operation_rows o ON o.id = d.fence_id").
		Joins("JOIN plugin_instances i ON i.id = d.instance_id AND i.plugin_id = d.plugin_id").
		Joins("JOIN installed_plugins p ON p.plugin_id = i.plugin_id").
		Where("p.desired_lifecycle = ? AND i.desired_enabled = ? AND COALESCE(p.pending_operation_id, '') = '' AND COALESCE(i.pending_operation_id, '') = ''", "enabled", true).
		Where("d.acknowledged = ? AND (o.state = ? OR (o.state = ? AND o.action = ?))", true, "completed", "pending", "rotate").
		Where(`NOT EXISTS (SELECT 1 FROM plugin_scoped_secret_delivery_rows affected
			JOIN plugin_scoped_secret_operation_rows pending ON pending.id = affected.fence_id
			WHERE affected.instance_id = d.instance_id AND pending.state = 'pending'
			AND (pending.action = 'revoke' OR EXISTS (SELECT 1 FROM plugin_scoped_secret_delivery_rows waiting WHERE waiting.fence_id = pending.id AND waiting.acknowledged = ?)))`, false).
		Where(`(d.agent_id = '' AND EXISTS (SELECT 1 FROM plugin_runtime_instances r WHERE r.instance_id = d.instance_id AND r.active_generation = d.generation_id))
			OR (d.agent_id <> '' AND EXISTS (SELECT 1 FROM plugin_agent_runtime_statuses a WHERE a.agent_id = d.agent_id AND a.instance_id = d.instance_id AND a.authority_slot = 'active' AND a.generation_id = d.provider_generation_id))`)
	if instanceID != "" {
		query = query.Where("d.instance_id = ?", instanceID)
	}
	var ids []string
	err := query.Distinct("d.instance_id").Order("d.instance_id").Pluck("d.instance_id", &ids).Error
	return ids, err
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
