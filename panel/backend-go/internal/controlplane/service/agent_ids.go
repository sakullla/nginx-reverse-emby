package service

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type agentLister interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
}

func allKnownAgentIDs(ctx context.Context, cfg config.Config, store agentLister) ([]string, error) {
	seen := map[string]struct{}{}
	agentIDs := make([]string, 0)
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		seen[cfg.LocalAgentID] = struct{}{}
		agentIDs = append(agentIDs, cfg.LocalAgentID)
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.TrimSpace(row.ID) == "" {
			continue
		}
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		agentIDs = append(agentIDs, row.ID)
	}
	return agentIDs, nil
}
