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
	InstanceID              string    `gorm:"primaryKey;size:64" json:"instance_id"`
	PluginID                string    `gorm:"index;size:190;not null" json:"plugin_id"`
	HostScope               string    `gorm:"index;size:32;not null" json:"host_scope"`
	ActiveGeneration        string    `gorm:"size:128;not null;default:''" json:"active_generation,omitempty"`
	ActivePackageDigest     string    `gorm:"size:64;not null;default:''" json:"active_package_digest,omitempty"`
	ActiveArtifactDigest    string    `gorm:"size:64;not null;default:''" json:"active_artifact_digest,omitempty"`
	CandidateGeneration     string    `gorm:"index;size:128;not null;default:''" json:"candidate_generation,omitempty"`
	CandidatePackageDigest  string    `gorm:"size:64;not null;default:''" json:"candidate_package_digest,omitempty"`
	CandidateArtifactDigest string    `gorm:"size:64;not null;default:''" json:"candidate_artifact_digest,omitempty"`
	CandidateState          string    `gorm:"index;size:32;not null;default:''" json:"candidate_state,omitempty"`
	CandidateLastError      string    `gorm:"type:text;not null;default:''" json:"candidate_last_error,omitempty"`
	State                   string    `gorm:"index;size:32;not null" json:"state"`
	PID                     int       `gorm:"column:pid;not null;default:0" json:"pid,omitempty"`
	SandboxProvider         string    `gorm:"size:64;not null;default:''" json:"sandbox_provider,omitempty"`
	RestartCount            int       `gorm:"not null;default:0" json:"restart_count"`
	CircuitOpen             bool      `gorm:"not null;default:false" json:"circuit_open"`
	LastError               string    `gorm:"type:text;not null" json:"last_error,omitempty"`
	UpdatedAt               time.Time `gorm:"not null" json:"updated_at"`
}

func (PluginRuntimeInstanceRow) TableName() string { return "plugin_runtime_instances" }

func (s *GormStore) ensurePluginRuntimeSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return gorm.ErrInvalidDB
	}
	return s.db.WithContext(ctx).AutoMigrate(&PluginRuntimeInstanceRow{})
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
		return tx.Model(&current).Updates(map[string]any{"plugin_id": row.PluginID, "host_scope": row.HostScope, "candidate_generation": row.CandidateGeneration, "candidate_package_digest": row.CandidatePackageDigest, "candidate_artifact_digest": row.CandidateArtifactDigest, "candidate_state": "starting", "candidate_last_error": "", "updated_at": now}).Error
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
		updates := map[string]any{"active_generation": row.CandidateGeneration, "active_package_digest": row.CandidatePackageDigest, "active_artifact_digest": row.CandidateArtifactDigest, "candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_state": "", "candidate_last_error": "", "state": "healthy", "pid": pid, "sandbox_provider": sandbox, "last_error": "", "circuit_open": false, "updated_at": time.Now().UTC()}
		return tx.Model(&row).Updates(updates).Error
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
		return tx.Model(&row).Updates(map[string]any{"candidate_generation": "", "candidate_package_digest": "", "candidate_artifact_digest": "", "candidate_state": "failed", "candidate_last_error": message, "updated_at": time.Now().UTC()}).Error
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
	return nil
}
