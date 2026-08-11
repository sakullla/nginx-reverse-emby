package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PluginRuntimeLogEntry struct {
	InstanceID string    `json:"instance_id"`
	AgentID    string    `json:"agent_id"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	Truncated  bool      `json:"truncated"`
	CreatedAt  time.Time `json:"created_at"`
}

type PluginRuntimeLogPage struct {
	Entries    []PluginRuntimeLogEntry `json:"entries"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}

type pluginRuntimeLogStore interface {
	ListPluginRuntimeLogs(context.Context, storage.PluginRuntimeLogQuery) (storage.PluginRuntimeLogPage, error)
}

func (s *PluginService) LogsForActor(ctx context.Context, pluginID, instanceID, agentID, cursor string, limit int, actor authz.Actor) (PluginRuntimeLogPage, error) {
	if !actor.Has(authz.PermissionResourceRead) && !actor.Has(authz.PermissionSystemAdmin) {
		return PluginRuntimeLogPage{}, authz.ErrForbidden
	}
	instance, found, err := s.store.GetPluginInstance(ctx, strings.TrimSpace(instanceID))
	if err != nil {
		return PluginRuntimeLogPage{}, err
	}
	if !found || instance.PluginID != strings.TrimSpace(pluginID) {
		return PluginRuntimeLogPage{}, ErrPluginNotInstalled
	}
	if !actor.Bootstrap && !actor.Has(authz.PermissionSystemAdmin) && !actor.CanAccessGroup(instance.ResourceGroupID) {
		return PluginRuntimeLogPage{}, authz.ErrForbidden
	}
	store, ok := s.store.(pluginRuntimeLogStore)
	if !ok {
		return PluginRuntimeLogPage{}, errors.New("plugin runtime log store is unavailable")
	}
	page, err := store.ListPluginRuntimeLogs(ctx, storage.PluginRuntimeLogQuery{InstanceID: instance.ID, AgentID: agentID, Cursor: cursor, Limit: limit})
	if err != nil {
		return PluginRuntimeLogPage{}, err
	}
	result := PluginRuntimeLogPage{Entries: make([]PluginRuntimeLogEntry, 0, len(page.Rows)), NextCursor: page.NextCursor}
	for _, row := range page.Rows {
		if row.PluginID != instance.PluginID || row.ResourceGroupID != instance.ResourceGroupID {
			return PluginRuntimeLogPage{}, ErrPluginReadProjection
		}
		result.Entries = append(result.Entries, PluginRuntimeLogEntry{InstanceID: row.InstanceID, AgentID: row.AgentID, Level: row.Level, Message: row.Message, Truncated: row.Truncated, CreatedAt: row.CreatedAt})
	}
	return result, nil
}
