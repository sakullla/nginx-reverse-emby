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
	localAgentID := strings.TrimSpace(cfg.LocalAgentID)
	if cfg.EnableLocalAgent && localAgentID != "" {
		seen[localAgentID] = struct{}{}
		agentIDs = append(agentIDs, localAgentID)
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		agentID := strings.TrimSpace(row.ID)
		if agentID == "" || inactiveLocalAgentRow(cfg, row) {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	return agentIDs, nil
}

func inactiveLocalAgentRow(cfg config.Config, row storage.AgentRow) bool {
	if !row.IsLocal {
		return false
	}
	return !cfg.EnableLocalAgent || strings.TrimSpace(row.ID) != strings.TrimSpace(cfg.LocalAgentID)
}
