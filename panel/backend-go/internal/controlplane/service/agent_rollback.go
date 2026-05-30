package service

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type agentRollbackStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	SaveAgent(context.Context, storage.AgentRow) error
}

func snapshotAgentRowsForRollback(ctx context.Context, store agentRollbackStore, agentIDs []string) ([]storage.AgentRow, error) {
	if len(agentIDs) == 0 {
		return nil, nil
	}
	wanted := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		agentID = strings.TrimSpace(agentID)
		if agentID == "" {
			continue
		}
		wanted[agentID] = struct{}{}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	rollbackRows := make([]storage.AgentRow, 0, len(wanted))
	for _, row := range rows {
		if _, ok := wanted[strings.TrimSpace(row.ID)]; ok {
			rollbackRows = append(rollbackRows, row)
		}
	}
	return rollbackRows, nil
}

func restoreAgentRowsBestEffort(ctx context.Context, store agentRollbackStore, rows []storage.AgentRow) {
	for _, row := range rows {
		_ = store.SaveAgent(ctx, row)
	}
}
