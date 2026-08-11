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
	InstanceID string
	AgentID    string
	Cursor     string
	Limit      int
}

type PluginRuntimeLogPage struct {
	Rows       []PluginRuntimeLogRow
	NextCursor string
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
		var instance PluginInstanceRow
		if err := tx.Where("id = ? AND plugin_id = ?", report.InstanceID, report.PluginID).First(&instance).Error; err != nil {
			return fmt.Errorf("plugin runtime log instance is unavailable: %w", err)
		}
		groupID := instance.ResourceGroupID
		if instance.PendingOperationID == status.OperationID && strings.TrimSpace(instance.PendingResourceGroupID) != "" {
			groupID = instance.PendingResourceGroupID
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
			row := PluginRuntimeLogRow{InstanceID: report.InstanceID, PluginID: report.PluginID, AgentID: report.AgentID, ResourceGroupID: groupID, Level: entry.Level, Message: entry.Message, Truncated: entry.Truncated, CreatedAt: createdAt}
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
