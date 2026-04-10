package storage

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store interface {
	ListAgents(context.Context) ([]AgentRow, error)
	ListHTTPRules(context.Context, string) ([]HTTPRuleRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
}

type SQLiteStore struct {
	db           *sql.DB
	localAgentID string
}

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.Join(dataRoot, "panel.db")))
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db, localAgentID: localAgentID}, nil
}

func (s *SQLiteStore) ListAgents(ctx context.Context) ([]AgentRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id,
			name,
			agent_url,
			agent_token,
			version,
			platform,
			desired_version,
			desired_revision,
			current_revision,
			last_apply_revision,
			COALESCE(last_apply_status, ''),
			COALESCE(last_apply_message, ''),
			is_local
		FROM agents
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentRow
	for rows.Next() {
		var row AgentRow
		if err := rows.Scan(
			&row.ID,
			&row.Name,
			&row.AgentURL,
			&row.AgentToken,
			&row.Version,
			&row.Platform,
			&row.DesiredVersion,
			&row.DesiredRevision,
			&row.CurrentRevision,
			&row.LastApplyRevision,
			&row.LastApplyStatus,
			&row.LastApplyMessage,
			&row.IsLocal,
		); err != nil {
			return nil, err
		}
		agents = append(agents, row)
	}

	return agents, rows.Err()
}

func (s *SQLiteStore) ListHTTPRules(ctx context.Context, agentID string) ([]HTTPRuleRow, error) {
	if agentID == "" {
		agentID = s.localAgentID
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id,
			agent_id,
			frontend_url,
			backend_url,
			enabled,
			revision
		FROM rules
		WHERE agent_id = ?
		ORDER BY id
	`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []HTTPRuleRow
	for rows.Next() {
		var row HTTPRuleRow
		if err := rows.Scan(
			&row.ID,
			&row.AgentID,
			&row.FrontendURL,
			&row.BackendURL,
			&row.Enabled,
			&row.Revision,
		); err != nil {
			return nil, err
		}
		rules = append(rules, row)
	}

	return rules, rows.Err()
}

func (s *SQLiteStore) LoadLocalAgentState(ctx context.Context) (LocalAgentStateRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			desired_revision,
			current_revision,
			last_apply_revision,
			last_apply_status,
			last_apply_message,
			desired_version
		FROM local_agent_state
		ORDER BY id
		LIMIT 1
	`)

	var state LocalAgentStateRow
	err := row.Scan(
		&state.DesiredRevision,
		&state.CurrentRevision,
		&state.LastApplyRevision,
		&state.LastApplyStatus,
		&state.LastApplyMessage,
		&state.DesiredVersion,
	)
	if err == sql.ErrNoRows {
		return LocalAgentStateRow{
			LastApplyStatus: "success",
		}, nil
	}
	if err != nil {
		return LocalAgentStateRow{}, err
	}
	return state, nil
}
