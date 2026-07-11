package service

import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	DefaultListPageSize = 20
	MaxListPageSize     = 100
)

// ListQuery is the shared list filter/pagination contract for resource list APIs.
// Empty AgentID means all agents; non-empty filters to that agent.
type ListQuery struct {
	AgentID  string
	Page     int
	PageSize int
	Q        string
}

// PageMeta is returned with every paginated list response.
type PageMeta struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// NormalizeListQuery clamps page/page_size and trims filter fields.
// Invalid or missing page defaults to 1; page_size defaults to 20 and is capped at 100.
func NormalizeListQuery(query ListQuery) ListQuery {
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.Q = strings.TrimSpace(query.Q)
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = DefaultListPageSize
	}
	if query.PageSize > MaxListPageSize {
		query.PageSize = MaxListPageSize
	}
	return query
}

// ApplyPage slices items for the given normalized page and returns the page slice plus meta.
// total is the pre-pagination filtered count.
func ApplyPage[T any](items []T, query ListQuery) ([]T, PageMeta) {
	query = NormalizeListQuery(query)
	total := len(items)
	meta := PageMeta{
		Total:    total,
		Page:     query.Page,
		PageSize: query.PageSize,
	}
	if total == 0 {
		return []T{}, meta
	}
	start := (query.Page - 1) * query.PageSize
	if start >= total {
		return []T{}, meta
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	page := make([]T, end-start)
	copy(page, items[start:end])
	return page, meta
}

func matchesListQuery(q string, parts ...string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return true
	}
	needle := strings.ToLower(q)
	for _, part := range parts {
		if strings.Contains(strings.ToLower(part), needle) {
			return true
		}
	}
	return false
}

func agentDisplayNameMap(ctx context.Context, cfg config.Config, store agentLister) (map[string]string, error) {
	names := map[string]string{}
	if cfg.EnableLocalAgent && strings.TrimSpace(cfg.LocalAgentID) != "" {
		name := strings.TrimSpace(cfg.LocalAgentName)
		if name == "" {
			name = cfg.LocalAgentID
		}
		names[cfg.LocalAgentID] = name
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = id
		}
		names[id] = name
	}
	return names, nil
}

func resolveAgentDisplayName(names map[string]string, agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if name := strings.TrimSpace(names[agentID]); name != "" {
		return name
	}
	return agentID
}

func ensureKnownAgentID(ctx context.Context, cfg config.Config, store agentLister, agentID string) (string, error) {
	resolvedID := strings.TrimSpace(agentID)
	if resolvedID == "" {
		return "", nil
	}
	if cfg.EnableLocalAgent && resolvedID == cfg.LocalAgentID {
		return resolvedID, nil
	}
	rows, err := store.ListAgents(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.ID == resolvedID {
			return resolvedID, nil
		}
	}
	return "", ErrAgentNotFound
}

// compile-time guard that storage.AgentRow stays available to this helper package surface.
var _ = storage.AgentRow{}
