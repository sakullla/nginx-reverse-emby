package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PluginRuntimeInstanceRow struct {
	InstanceID               string    `gorm:"primaryKey;size:64" json:"instance_id"`
	PluginID                 string    `gorm:"index;size:190;not null" json:"plugin_id"`
	HostScope                string    `gorm:"index;size:32;not null" json:"host_scope"`
	ActiveGeneration         string    `gorm:"size:128;not null;default:''" json:"active_generation,omitempty"`
	ActivePackageDigest      string    `gorm:"size:64;not null;default:''" json:"active_package_digest,omitempty"`
	ActiveArtifactDigest     string    `gorm:"size:64;not null;default:''" json:"active_artifact_digest,omitempty"`
	ActiveOperationID        string    `gorm:"size:64;not null;default:''" json:"active_operation_id,omitempty"`
	ActiveResourceGroupID    string    `gorm:"size:64;not null;default:''" json:"active_resource_group_id,omitempty"`
	ActiveRevision           int64     `gorm:"not null;default:0" json:"active_revision,omitempty"`
	CandidateGeneration      string    `gorm:"index;size:128;not null;default:''" json:"candidate_generation,omitempty"`
	CandidatePackageDigest   string    `gorm:"size:64;not null;default:''" json:"candidate_package_digest,omitempty"`
	CandidateArtifactDigest  string    `gorm:"size:64;not null;default:''" json:"candidate_artifact_digest,omitempty"`
	CandidateOperationID     string    `gorm:"size:64;not null;default:''" json:"candidate_operation_id,omitempty"`
	CandidateResourceGroupID string    `gorm:"size:64;not null;default:''" json:"candidate_resource_group_id,omitempty"`
	CandidateRevision        int64     `gorm:"not null;default:0" json:"candidate_revision,omitempty"`
	CandidateState           string    `gorm:"index;size:32;not null;default:''" json:"candidate_state,omitempty"`
	CandidateLastError       string    `gorm:"type:text;not null;default:''" json:"candidate_last_error,omitempty"`
	State                    string    `gorm:"index;size:32;not null" json:"state"`
	PID                      int       `gorm:"column:pid;not null;default:0" json:"pid,omitempty"`
	SandboxProvider          string    `gorm:"size:64;not null;default:''" json:"sandbox_provider,omitempty"`
	RestartCount             int       `gorm:"not null;default:0" json:"restart_count"`
	CircuitOpen              bool      `gorm:"not null;default:false" json:"circuit_open"`
	LastError                string    `gorm:"type:text;not null" json:"last_error,omitempty"`
	UpdatedAt                time.Time `gorm:"not null" json:"updated_at"`
}

type PluginRuntimePromotion struct {
	InstanceID      string
	Generation      string
	PID             int
	SandboxProvider string
}

type PluginRuntimeCandidateFailure struct {
	InstanceID string
	Generation string
	Failure    error
}

type PluginRuntimeDirectoryReference struct {
	InstanceID string
	Generation string
}

func (PluginRuntimeInstanceRow) TableName() string { return "plugin_runtime_instances" }

func (s *GormStore) ensurePluginRuntimeSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return gorm.ErrInvalidDB
	}
	return s.db.WithContext(ctx).AutoMigrate(&PluginRuntimeInstanceRow{}, &PluginControlPlaneLogOutboxRow{}, &PluginRuntimeLogRow{})
}

func (s *GormStore) StagePluginRuntime(ctx context.Context, row PluginRuntimeInstanceRow) error {
	if strings.TrimSpace(row.InstanceID) == "" || strings.TrimSpace(row.CandidateGeneration) == "" {
		return errors.New("plugin runtime instance and candidate generation are required")
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	row.UpdatedAt = now
	row.CandidateState = "starting"
	row.CandidateLastError = ""
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current PluginRuntimeInstanceRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", row.InstanceID).Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if row.State == "" {
				row.State = "stopped"
			}
			return tx.Create(&row).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]any{"plugin_id": row.PluginID, "host_scope": row.HostScope, "candidate_generation": row.CandidateGeneration, "candidate_package_digest": row.CandidatePackageDigest, "candidate_artifact_digest": row.CandidateArtifactDigest, "candidate_operation_id": row.CandidateOperationID, "candidate_resource_group_id": row.CandidateResourceGroupID, "candidate_revision": row.CandidateRevision, "candidate_state": "starting", "candidate_last_error": "", "updated_at": now}).Error
	})
}

func (s *GormStore) StagePluginRuntimeBatch(ctx context.Context, rows []PluginRuntimeInstanceRow) error {
	if len(rows) == 0 {
		return errors.New("plugin runtime batch is empty")
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		seen := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			if strings.TrimSpace(row.InstanceID) == "" || strings.TrimSpace(row.CandidateGeneration) == "" {
				return errors.New("plugin runtime instance and candidate generation are required")
			}
			if _, duplicate := seen[row.InstanceID]; duplicate {
				return errors.New("plugin runtime batch contains a duplicate instance")
			}
			seen[row.InstanceID] = struct{}{}
			now := time.Now().UTC()
			var current PluginRuntimeInstanceRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", row.InstanceID).Take(&current).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row.State, row.CandidateState, row.CandidateLastError, row.UpdatedAt = "stopped", "starting", "", now
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			updates := map[string]any{"plugin_id": row.PluginID, "host_scope": row.HostScope, "candidate_generation": row.CandidateGeneration, "candidate_package_digest": row.CandidatePackageDigest, "candidate_artifact_digest": row.CandidateArtifactDigest, "candidate_operation_id": row.CandidateOperationID, "candidate_resource_group_id": row.CandidateResourceGroupID, "candidate_revision": row.CandidateRevision, "candidate_state": "starting", "candidate_last_error": "", "updated_at": now}
			if err := tx.Model(&current).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) PromotePluginRuntime(ctx context.Context, instanceID, generation string, pid int, sandbox string) error {
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var row PluginRuntimeInstanceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", instanceID).Take(&row).Error; err != nil {
			return err
		}
		if row.CandidateGeneration != generation {
			return errors.New("plugin runtime candidate generation changed before promotion")
		}
		updates := map[string]any{"active_generation": row.CandidateGeneration, "active_package_digest": row.CandidatePackageDigest, "active_artifact_digest": row.CandidateArtifactDigest, "active_operation_id": row.CandidateOperationID, "active_resource_group_id": row.CandidateResourceGroupID, "active_revision": row.CandidateRevision, "candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_operation_id": "", "candidate_resource_group_id": "", "candidate_revision": 0, "candidate_state": "", "candidate_last_error": "", "state": "healthy", "pid": pid, "sandbox_provider": sandbox, "last_error": "", "circuit_open": false, "updated_at": time.Now().UTC()}
		return tx.Model(&row).Updates(updates).Error
	})
}

func (s *GormStore) PromotePluginRuntimeBatch(ctx context.Context, promotions []PluginRuntimePromotion) error {
	if len(promotions) == 0 {
		return errors.New("plugin runtime promotion batch is empty")
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		seen := make(map[string]struct{}, len(promotions))
		for _, promotion := range promotions {
			if _, duplicate := seen[promotion.InstanceID]; duplicate {
				return errors.New("plugin runtime promotion batch contains a duplicate instance")
			}
			seen[promotion.InstanceID] = struct{}{}
			var row PluginRuntimeInstanceRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", promotion.InstanceID).Take(&row).Error; err != nil {
				return err
			}
			if row.CandidateGeneration != promotion.Generation {
				return errors.New("plugin runtime candidate generation changed before batch promotion")
			}
			updates := map[string]any{"active_generation": row.CandidateGeneration, "active_package_digest": row.CandidatePackageDigest, "active_artifact_digest": row.CandidateArtifactDigest, "active_operation_id": row.CandidateOperationID, "active_resource_group_id": row.CandidateResourceGroupID, "active_revision": row.CandidateRevision, "candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_operation_id": "", "candidate_resource_group_id": "", "candidate_revision": 0, "candidate_state": "", "candidate_last_error": "", "state": "healthy", "pid": promotion.PID, "sandbox_provider": promotion.SandboxProvider, "last_error": "", "circuit_open": false, "updated_at": time.Now().UTC()}
			if err := tx.Model(&row).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) FailPluginRuntimeCandidate(ctx context.Context, instanceID, generation string, failure error) error {
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	message := ""
	if failure != nil {
		message = failure.Error()
		if len(message) > 512 {
			message = message[:512]
		}
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var row PluginRuntimeInstanceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", instanceID).Take(&row).Error; err != nil {
			return err
		}
		if row.CandidateGeneration != generation {
			return errors.New("plugin runtime candidate generation changed before failure report")
		}
		return tx.Model(&row).Updates(map[string]any{"candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_operation_id": "", "candidate_resource_group_id": "", "candidate_revision": 0, "candidate_state": "failed", "candidate_last_error": message, "updated_at": time.Now().UTC()}).Error
	})
}

func (s *GormStore) FailPluginRuntimeCandidateBatch(ctx context.Context, failures []PluginRuntimeCandidateFailure) error {
	if len(failures) == 0 {
		return nil
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		for _, failure := range failures {
			message := ""
			if failure.Failure != nil {
				message = failure.Failure.Error()
				if len(message) > 512 {
					message = message[:512]
				}
			}
			var row PluginRuntimeInstanceRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", failure.InstanceID).Take(&row).Error; err != nil {
				return err
			}
			if row.CandidateGeneration != failure.Generation {
				return errors.New("plugin runtime candidate generation changed before batch failure report")
			}
			if err := tx.Model(&row).Updates(map[string]any{"candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_operation_id": "", "candidate_resource_group_id": "", "candidate_revision": 0, "candidate_state": "failed", "candidate_last_error": message, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) StopPluginRuntime(ctx context.Context, instanceID, generation string) error {
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(generation) == "" {
		return errors.New("plugin runtime instance and active generation are required")
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	result := s.db.WithContext(ctx).Model(&PluginRuntimeInstanceRow{}).
		Where("instance_id = ? AND active_generation = ?", instanceID, generation).
		Updates(map[string]any{"state": "stopped", "pid": 0, "circuit_open": false, "last_error": "", "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("plugin runtime active generation changed before stop")
	}
	return nil
}

func (s *GormStore) GetPluginRuntime(ctx context.Context, instanceID string) (PluginRuntimeInstanceRow, bool, error) {
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return PluginRuntimeInstanceRow{}, false, err
	}
	var row PluginRuntimeInstanceRow
	err := s.db.WithContext(ctx).Where("instance_id = ?", instanceID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginRuntimeInstanceRow{}, false, nil
	}
	return row, err == nil, err
}

func (s *GormStore) ListPluginRuntimeDirectoryReferences(ctx context.Context) ([]PluginRuntimeDirectoryReference, error) {
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return nil, err
	}
	var rows []PluginRuntimeInstanceRow
	if err := s.db.WithContext(ctx).
		Select("instance_id", "active_generation", "candidate_generation").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	references := make([]PluginRuntimeDirectoryReference, 0, len(rows)*2)
	for _, row := range rows {
		if generation := strings.TrimSpace(row.ActiveGeneration); generation != "" {
			references = append(references, PluginRuntimeDirectoryReference{InstanceID: row.InstanceID, Generation: generation})
		}
		if generation := strings.TrimSpace(row.CandidateGeneration); generation != "" {
			references = append(references, PluginRuntimeDirectoryReference{InstanceID: row.InstanceID, Generation: generation})
		}
	}
	return references, nil
}

func (s *GormStore) UpdatePluginRuntimeHealth(ctx context.Context, instanceID, generation, state string, pid, restartCount int, circuitOpen bool, lastError string) error {
	if strings.TrimSpace(instanceID) == "" || strings.TrimSpace(generation) == "" {
		return errors.New("plugin runtime instance and active generation are required")
	}
	if err := s.ensurePluginRuntimeSchema(ctx); err != nil {
		return err
	}
	if len(lastError) > 512 {
		lastError = lastError[:512]
	}
	result := s.db.WithContext(ctx).Model(&PluginRuntimeInstanceRow{}).
		Where("instance_id = ? AND active_generation = ?", instanceID, generation).
		Updates(map[string]any{"state": state, "pid": pid, "restart_count": restartCount, "circuit_open": circuitOpen, "last_error": lastError, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("plugin runtime active generation changed before health update")
	}
	instance, exists, err := s.GetPluginInstance(ctx, instanceID)
	if err != nil || !exists {
		return err
	}
	targets, err := pluginInstanceTargets(instance.TargetJSON, s.LocalAgentID())
	if err != nil || len(targets) == 0 {
		return err
	}
	message := state
	if strings.TrimSpace(lastError) != "" {
		message += ": " + lastError
	}
	level := "info"
	if state == "backoff" || circuitOpen {
		level = "warning"
	}
	if state == "failed" {
		level = "error"
	}
	_, err = s.AppendPluginRuntimeLog(ctx, PluginRuntimeLogRow{InstanceID: instance.ID, PluginID: instance.PluginID, AgentID: targets[0], ResourceGroupID: instance.ResourceGroupID, Level: level, Message: message, CreatedAt: time.Now().UTC()})
	return err
}
