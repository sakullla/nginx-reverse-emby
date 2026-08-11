package storage

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	pluginRuntimeLogMessageMax = 4096
	pluginRuntimeLogPageMax    = 100
	pluginRuntimeLogRetention  = 1000
)

var pluginRuntimeCredentialPattern = regexp.MustCompile(`(?i)((?:authorization|cookie|credential|password|private[_-]?key|secret|token|api[_-]?key)\s*[:=]\s*)[^\s,;]+|Bearer\s+[^\s,;]+`)
var pluginRuntimeURLCredentialPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^\s/:@]+:)[^\s/@]+@`)
var pluginRuntimePrivateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)

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
	message = pluginRuntimeCredentialPattern.ReplaceAllStringFunc(message, func(match string) string {
		if strings.HasPrefix(strings.ToLower(match), "bearer ") {
			return "Bearer [REDACTED]"
		}
		if index := strings.IndexAny(match, ":="); index >= 0 {
			return match[:index+1] + "[REDACTED]"
		}
		return "[REDACTED]"
	})
	message = pluginRuntimeURLCredentialPattern.ReplaceAllString(message, `${1}[REDACTED]@`)
	message = pluginRuntimePrivateKeyPattern.ReplaceAllString(message, `[REDACTED PRIVATE KEY]`)
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
