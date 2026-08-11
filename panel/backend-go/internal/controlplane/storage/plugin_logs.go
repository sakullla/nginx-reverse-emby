package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/sanitize"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	pluginRuntimeLogMessageMax = 4096
	pluginRuntimeLogPageMax    = 100
	pluginRuntimeLogRetention  = 1000
)

type PluginRuntimeLogQuery struct {
	InstanceID      string
	AgentID         string
	ResourceGroupID string
	Cursor          string
	Limit           int
}

type PluginRuntimeLogPage struct {
	Rows       []PluginRuntimeLogRow
	NextCursor string
}

func (s *GormStore) EnqueueControlPlanePluginRuntimeLog(ctx context.Context, row PluginControlPlaneLogOutboxRow) error {
	row.EventID = strings.TrimSpace(row.EventID)
	row.InstanceID = strings.TrimSpace(row.InstanceID)
	row.PluginID = strings.TrimSpace(row.PluginID)
	row.OperationID = strings.TrimSpace(row.OperationID)
	row.GenerationID = strings.TrimSpace(row.GenerationID)
	row.ResourceGroupID = strings.TrimSpace(row.ResourceGroupID)
	row.PackageDigest = strings.ToLower(strings.TrimSpace(row.PackageDigest))
	row.ArtifactDigest = strings.ToLower(strings.TrimSpace(row.ArtifactDigest))
	if row.EventID == "" || row.InstanceID == "" || row.PluginID == "" || row.OperationID == "" || row.GenerationID == "" || row.ResourceGroupID == "" || row.Revision <= 0 || len(row.PackageDigest) != 64 || len(row.ArtifactDigest) != 64 {
		return errors.New("control-plane plugin log authority is invalid")
	}
	row.Message, row.Truncated = sanitizePluginRuntimeLog(row.Message)
	if row.Level == "" {
		row.Level = "info"
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var runtime PluginRuntimeInstanceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ? AND plugin_id = ?", row.InstanceID, row.PluginID).First(&runtime).Error; err != nil {
			return err
		}
		candidate := runtime.CandidateGeneration == row.GenerationID && runtime.CandidateOperationID == row.OperationID && runtime.CandidateResourceGroupID == row.ResourceGroupID && runtime.CandidateRevision == row.Revision && runtime.CandidatePackageDigest == row.PackageDigest && runtime.CandidateArtifactDigest == row.ArtifactDigest
		active := runtime.ActiveGeneration == row.GenerationID && runtime.ActiveOperationID == row.OperationID && runtime.ActiveResourceGroupID == row.ResourceGroupID && runtime.ActiveRevision == row.Revision && runtime.ActivePackageDigest == row.PackageDigest && runtime.ActiveArtifactDigest == row.ArtifactDigest
		if !candidate && !active {
			return ErrPluginGenerationStale
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	})
}

func (s *GormStore) ListControlPlanePluginRuntimeLogOutbox(ctx context.Context, limit int) ([]PluginControlPlaneLogOutboxRow, error) {
	if limit <= 0 || limit > 128 {
		limit = 128
	}
	var rows []PluginControlPlaneLogOutboxRow
	err := s.db.WithContext(ctx).Order("created_at, event_id").Limit(limit).Find(&rows).Error
	return rows, err
}

func (s *GormStore) FlushControlPlanePluginRuntimeLog(ctx context.Context, eventID string) error {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return errors.New("control-plane plugin log event is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var outbox PluginControlPlaneLogOutboxRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ?", eventID).First(&outbox).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		ownedEventID := outbox.EventID
		row := PluginRuntimeLogRow{EventID: &ownedEventID, InstanceID: outbox.InstanceID, PluginID: outbox.PluginID, AgentID: "control-plane", ResourceGroupID: outbox.ResourceGroupID, OperationID: outbox.OperationID, GenerationID: outbox.GenerationID, Revision: outbox.Revision, PackageDigest: outbox.PackageDigest, ArtifactDigest: outbox.ArtifactDigest, Level: outbox.Level, Message: outbox.Message, Truncated: outbox.Truncated, CreatedAt: outbox.CreatedAt}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var stale []uint64
		if err := tx.Model(&PluginRuntimeLogRow{}).Where("instance_id = ?", outbox.InstanceID).Order("id DESC").Offset(pluginRuntimeLogRetention).Pluck("id", &stale).Error; err != nil {
			return err
		}
		if len(stale) > 0 {
			if err := tx.Where("id IN ?", stale).Delete(&PluginRuntimeLogRow{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("event_id = ?", eventID).Delete(&PluginControlPlaneLogOutboxRow{}).Error
	})
}

// AppendControlPlanePluginRuntimeLog derives ownership from durable runtime
// and instance rows; callers cannot stamp a resource group onto guest output.
func (s *GormStore) AppendControlPlanePluginRuntimeLog(ctx context.Context, instanceID, pluginID, generation, message string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var runtime PluginRuntimeInstanceRow
		if err := tx.Where("instance_id = ? AND plugin_id = ? AND (active_generation = ? OR candidate_generation = ?)", instanceID, pluginID, generation, generation).First(&runtime).Error; err != nil {
			return err
		}
		var instance PluginInstanceRow
		if err := tx.Where("id = ? AND plugin_id = ?", instanceID, pluginID).First(&instance).Error; err != nil {
			return err
		}
		row := PluginRuntimeLogRow{InstanceID: instanceID, PluginID: pluginID, AgentID: "control-plane", ResourceGroupID: instance.ResourceGroupID, Level: "info", Message: message, CreatedAt: time.Now().UTC()}
		row.Message, row.Truncated = sanitizePluginRuntimeLog(row.Message)
		return appendPluginRuntimeLogTx(tx, &row)
	})
}

type PluginRuntimeLogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	Truncated bool   `json:"truncated"`
}

type PluginRuntimeLogReport struct {
	Revision       int64                   `json:"revision"`
	GenerationID   string                  `json:"generation_id"`
	InstanceID     string                  `json:"instance_id"`
	PluginID       string                  `json:"plugin_id"`
	AgentID        string                  `json:"agent_id"`
	PackageDigest  string                  `json:"package_digest"`
	ArtifactDigest string                  `json:"artifact_digest"`
	Sequence       uint64                  `json:"sequence"`
	Entries        []PluginRuntimeLogEntry `json:"entries"`
}

// RecordPluginRuntimeLogReport validates immutable runtime ownership and a
// monotonic per-generation replay fence before persisting sanitized fragments.
func (s *GormStore) RecordPluginRuntimeLogReport(ctx context.Context, authenticatedAgentID string, report PluginRuntimeLogReport) (bool, error) {
	authenticatedAgentID = strings.TrimSpace(authenticatedAgentID)
	if authenticatedAgentID == "" || report.AgentID != authenticatedAgentID || report.Revision <= 0 || report.Sequence == 0 ||
		strings.TrimSpace(report.GenerationID) == "" || strings.TrimSpace(report.InstanceID) == "" || strings.TrimSpace(report.PluginID) == "" ||
		len(report.PackageDigest) != 64 || len(report.ArtifactDigest) != 64 || len(report.Entries) == 0 || len(report.Entries) > 32 {
		return false, errors.New("plugin runtime log report identity is invalid")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return false, err
	}
	digestBytes := sha256.Sum256(encoded)
	digest := hex.EncodeToString(digestBytes[:])
	accepted := false
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var status PluginAgentRuntimeStatusRow
		if err := tx.Where("agent_id = ? AND instance_id = ? AND generation_id = ? AND revision = ? AND plugin_id = ? AND package_digest = ? AND artifact_digest = ?",
			report.AgentID, report.InstanceID, report.GenerationID, report.Revision, report.PluginID, report.PackageDigest, report.ArtifactDigest).Order("updated_at DESC").First(&status).Error; err != nil {
			return fmt.Errorf("plugin runtime log generation is not authoritative: %w", err)
		}
		if err := pluginRuntimeLogStatusCurrentTx(tx, status); err != nil {
			return err
		}
		var fence PluginRuntimeLogReportRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND instance_id = ? AND generation_id = ?", report.AgentID, report.InstanceID, report.GenerationID).First(&fence).Error
		if err == nil {
			if report.Sequence < fence.Sequence {
				return errors.New("plugin runtime log report is stale")
			}
			if report.Sequence == fence.Sequence {
				if digest != fence.ReportDigest {
					return errors.New("plugin runtime log report replay digest differs")
				}
				return nil
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		createdAt := time.Now().UTC()
		for _, entry := range report.Entries {
			row := PluginRuntimeLogRow{InstanceID: report.InstanceID, PluginID: report.PluginID, AgentID: report.AgentID, ResourceGroupID: status.ResourceGroupID, OperationID: status.OperationID, GenerationID: status.GenerationID, Revision: status.Revision, PackageDigest: status.PackageDigest, ArtifactDigest: status.ArtifactDigest, Level: entry.Level, Message: entry.Message, Truncated: entry.Truncated, CreatedAt: createdAt}
			row.Message, row.Truncated = sanitizePluginRuntimeLog(row.Message)
			row.Truncated = row.Truncated || entry.Truncated
			if err := appendPluginRuntimeLogTx(tx, &row); err != nil {
				return err
			}
		}
		fence = PluginRuntimeLogReportRow{AgentID: report.AgentID, InstanceID: report.InstanceID, GenerationID: report.GenerationID, Revision: report.Revision, PluginID: report.PluginID, PackageDigest: report.PackageDigest, ArtifactDigest: report.ArtifactDigest, Sequence: report.Sequence, ReportDigest: digest, UpdatedAt: createdAt}
		if err := tx.Save(&fence).Error; err != nil {
			return err
		}
		accepted = true
		return nil
	})
	return accepted, err
}

func pluginRuntimeLogStatusCurrentTx(tx *gorm.DB, status PluginAgentRuntimeStatusRow) error {
	if status.ResourceGroupID == "" || status.TargetVersion == 0 || (status.AuthoritySlot != "active" && status.AuthoritySlot != "pending") || status.State == "failed" || status.State == "drained" || status.State == "draining" {
		return ErrPluginGenerationStale
	}
	var agent AgentRow
	if err := tx.Where("id = ?", status.AgentID).First(&agent).Error; err != nil {
		return ErrPluginGenerationStale
	}
	var instance PluginInstanceRow
	if err := tx.Where("id = ? AND plugin_id = ?", status.InstanceID, status.PluginID).First(&instance).Error; err != nil {
		return ErrPluginGenerationStale
	}
	var installed InstalledPluginRow
	if err := tx.Where("plugin_id = ?", status.PluginID).First(&installed).Error; err != nil || installed.DesiredLifecycle != "enabled" {
		return ErrPluginGenerationStale
	}
	pendingAuthority := status.AuthoritySlot == "pending" && installed.PendingOperationID == status.OperationID
	activeAuthority := status.AuthoritySlot == "active" && (installed.PendingOperationID != "" || installed.LastOperationID == status.OperationID)
	if !pendingAuthority && !activeAuthority {
		return ErrPluginGenerationStale
	}
	groupID, configVersion, targetsJSON, packageDigest := instance.ResourceGroupID, instance.ConfigVersion, instance.TargetJSON, installed.ActivePackageDigest
	if instance.PendingOperationID == status.OperationID && instance.PendingVersion > 0 {
		groupID, configVersion, targetsJSON = instance.PendingResourceGroupID, instance.PendingVersion, instance.PendingTargetJSON
		if groupID == "" {
			groupID = instance.ResourceGroupID
		}
		if installed.PendingOperationID == status.OperationID && installed.StagedPackageDigest != "" {
			packageDigest = installed.StagedPackageDigest
		}
	}
	var targets []string
	if err := json.Unmarshal([]byte(targetsJSON), &targets); err != nil {
		return ErrPluginGenerationStale
	}
	foundTarget := false
	for _, target := range targets {
		if target == status.AgentID {
			foundTarget = true
			break
		}
	}
	if !foundTarget || groupID != status.ResourceGroupID || configVersion != status.ConfigVersion || configVersion != status.TargetVersion || packageDigest != status.PackageDigest {
		return ErrPluginGenerationStale
	}
	return nil
}

func (s *GormStore) AppendPluginRuntimeLog(ctx context.Context, row PluginRuntimeLogRow) (PluginRuntimeLogRow, error) {
	row.InstanceID = strings.TrimSpace(row.InstanceID)
	row.PluginID = strings.TrimSpace(row.PluginID)
	row.AgentID = strings.TrimSpace(row.AgentID)
	row.ResourceGroupID = strings.TrimSpace(row.ResourceGroupID)
	row.Level = strings.ToLower(strings.TrimSpace(row.Level))
	if row.InstanceID == "" || row.PluginID == "" || row.AgentID == "" || row.ResourceGroupID == "" {
		return PluginRuntimeLogRow{}, errors.New("plugin runtime log ownership is required")
	}
	if row.Level != "info" && row.Level != "warning" && row.Level != "error" {
		row.Level = "info"
	}
	row.Message, row.Truncated = sanitizePluginRuntimeLog(row.Message)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	} else {
		row.CreatedAt = row.CreatedAt.UTC()
	}
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error { return appendPluginRuntimeLogTx(tx, &row) })
	return row, err
}

func appendPluginRuntimeLogTx(tx *gorm.DB, row *PluginRuntimeLogRow) error {
	if err := tx.Create(row).Error; err != nil {
		return err
	}
	var stale []uint64
	if err := tx.Model(&PluginRuntimeLogRow{}).Where("instance_id = ?", row.InstanceID).Order("id DESC").Offset(pluginRuntimeLogRetention).Pluck("id", &stale).Error; err != nil {
		return err
	}
	if len(stale) > 0 {
		return tx.Where("id IN ?", stale).Delete(&PluginRuntimeLogRow{}).Error
	}
	return nil
}

func (s *GormStore) ListPluginRuntimeLogs(ctx context.Context, query PluginRuntimeLogQuery) (PluginRuntimeLogPage, error) {
	query.InstanceID = strings.TrimSpace(query.InstanceID)
	query.AgentID = strings.TrimSpace(query.AgentID)
	if query.InstanceID == "" {
		return PluginRuntimeLogPage{}, errors.New("plugin runtime log instance is required")
	}
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > pluginRuntimeLogPageMax {
		query.Limit = pluginRuntimeLogPageMax
	}
	db := s.db.WithContext(ctx).Where("instance_id = ?", query.InstanceID)
	if query.AgentID != "" {
		db = db.Where("agent_id = ?", query.AgentID)
	}
	if query.ResourceGroupID != "" {
		db = db.Where("resource_group_id = ?", query.ResourceGroupID)
	}
	if query.Cursor != "" {
		cursor, err := decodePluginRuntimeLogCursor(query.Cursor)
		if err != nil {
			return PluginRuntimeLogPage{}, err
		}
		db = db.Where("id < ?", cursor)
	}
	var rows []PluginRuntimeLogRow
	if err := db.Order("id DESC").Limit(query.Limit + 1).Find(&rows).Error; err != nil {
		return PluginRuntimeLogPage{}, err
	}
	next := ""
	if len(rows) > query.Limit {
		rows = rows[:query.Limit]
		next = encodePluginRuntimeLogCursor(rows[len(rows)-1].ID)
	}
	return PluginRuntimeLogPage{Rows: rows, NextCursor: next}, nil
}

func sanitizePluginRuntimeLog(message string) (string, bool) {
	message = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return ' '
		}
		return r
	}, message)
	message = sanitize.Text(message, nil)
	truncated := len(message) > pluginRuntimeLogMessageMax
	if truncated {
		message = message[:pluginRuntimeLogMessageMax]
	}
	return message, truncated
}

func encodePluginRuntimeLogCursor(id uint64) string {
	value := make([]byte, 8)
	binary.BigEndian.PutUint64(value, id)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodePluginRuntimeLogCursor(value string) (uint64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 8 {
		return 0, errors.New("plugin runtime log cursor is invalid")
	}
	id := binary.BigEndian.Uint64(decoded)
	if id == 0 {
		return 0, errors.New("plugin runtime log cursor is invalid")
	}
	return id, nil
}
