package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store interface {
	ListAgents(context.Context) ([]AgentRow, error)
	ListHTTPRules(context.Context, string) ([]HTTPRuleRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
	SaveAgent(context.Context, AgentRow) error
}

type SQLiteStore struct {
	db           *sql.DB
	localAgentID string
}

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.Join(dataRoot, "panel.db")))
	if err != nil {
		return nil, err
	}

	store := &SQLiteStore{db: db, localAgentID: localAgentID}
	if err := store.initializeSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
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
			COALESCE(tags, '[]'),
			COALESCE(capabilities, '[]'),
			COALESCE(mode, 'pull'),
			desired_revision,
			current_revision,
			last_apply_revision,
			COALESCE(last_apply_status, ''),
			COALESCE(last_apply_message, ''),
			COALESCE(last_seen_at, ''),
			COALESCE(last_seen_ip, ''),
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
			&row.TagsJSON,
			&row.CapabilitiesJSON,
			&row.Mode,
			&row.DesiredRevision,
			&row.CurrentRevision,
			&row.LastApplyRevision,
			&row.LastApplyStatus,
			&row.LastApplyMessage,
			&row.LastSeenAt,
			&row.LastSeenIP,
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
			COALESCE(backends, '[]'),
			COALESCE(load_balancing, '{}'),
			enabled,
			COALESCE(tags, '[]'),
			proxy_redirect,
			COALESCE(relay_chain, '[]'),
			pass_proxy_headers,
			COALESCE(user_agent, ''),
			COALESCE(custom_headers, '[]'),
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
			&row.BackendsJSON,
			&row.LoadBalancingJSON,
			&row.Enabled,
			&row.TagsJSON,
			&row.ProxyRedirect,
			&row.RelayChainJSON,
			&row.PassProxyHeaders,
			&row.UserAgent,
			&row.CustomHeadersJSON,
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

func (s *SQLiteStore) SaveAgent(ctx context.Context, row AgentRow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (
			id,
			name,
			agent_url,
			agent_token,
			version,
			platform,
			desired_version,
			tags,
			capabilities,
			mode,
			desired_revision,
			current_revision,
			last_apply_revision,
			last_apply_status,
			last_apply_message,
			last_seen_at,
			last_seen_ip,
			is_local
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			agent_url = excluded.agent_url,
			agent_token = excluded.agent_token,
			version = excluded.version,
			platform = excluded.platform,
			desired_version = excluded.desired_version,
			tags = excluded.tags,
			capabilities = excluded.capabilities,
			mode = excluded.mode,
			desired_revision = excluded.desired_revision,
			current_revision = excluded.current_revision,
			last_apply_revision = excluded.last_apply_revision,
			last_apply_status = excluded.last_apply_status,
			last_apply_message = excluded.last_apply_message,
			last_seen_at = excluded.last_seen_at,
			last_seen_ip = excluded.last_seen_ip,
			is_local = excluded.is_local
	`,
		row.ID,
		row.Name,
		row.AgentURL,
		row.AgentToken,
		row.Version,
		row.Platform,
		row.DesiredVersion,
		row.TagsJSON,
		row.CapabilitiesJSON,
		row.Mode,
		row.DesiredRevision,
		row.CurrentRevision,
		row.LastApplyRevision,
		row.LastApplyStatus,
		row.LastApplyMessage,
		row.LastSeenAt,
		row.LastSeenIP,
		row.IsLocal,
	)
	return err
}

func (s *SQLiteStore) initializeSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			agent_url TEXT DEFAULT '',
			agent_token TEXT DEFAULT '',
			version TEXT DEFAULT '',
			platform TEXT DEFAULT '',
			desired_version TEXT DEFAULT '',
			tags TEXT DEFAULT '[]',
			capabilities TEXT DEFAULT '[]',
			mode TEXT DEFAULT 'pull',
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT,
			last_apply_message TEXT DEFAULT '',
			last_reported_stats TEXT,
			last_seen_at TEXT,
			last_seen_ip TEXT,
			created_at TEXT,
			updated_at TEXT,
			error TEXT,
			is_local INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			frontend_url TEXT NOT NULL,
			backend_url TEXT NOT NULL,
			backends TEXT DEFAULT '[]',
			load_balancing TEXT DEFAULT '{}',
			enabled INTEGER DEFAULT 1,
			tags TEXT DEFAULT '[]',
			proxy_redirect INTEGER DEFAULT 1,
			relay_chain TEXT DEFAULT '[]',
			pass_proxy_headers INTEGER DEFAULT 1,
			user_agent TEXT DEFAULT '',
			custom_headers TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0,
			PRIMARY KEY (agent_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rules_agent ON rules(agent_id)`,
		`CREATE TABLE IF NOT EXISTS l4_rules (
			id INTEGER NOT NULL,
			agent_id TEXT NOT NULL,
			name TEXT DEFAULT '',
			protocol TEXT DEFAULT 'tcp',
			listen_host TEXT DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL,
			upstream_host TEXT DEFAULT '',
			upstream_port INTEGER DEFAULT 0,
			backends TEXT DEFAULT '[]',
			load_balancing TEXT DEFAULT '{}',
			tuning TEXT DEFAULT '{}',
			relay_chain TEXT DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0,
			PRIMARY KEY (agent_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_l4_rules_agent ON l4_rules(agent_id)`,
		`CREATE TABLE IF NOT EXISTS relay_listeners (
			id INTEGER PRIMARY KEY,
			agent_id TEXT NOT NULL,
			name TEXT DEFAULT '',
			bind_hosts TEXT DEFAULT '[]',
			listen_host TEXT DEFAULT '0.0.0.0',
			listen_port INTEGER NOT NULL,
			public_host TEXT,
			public_port INTEGER,
			enabled INTEGER DEFAULT 1,
			certificate_id INTEGER,
			tls_mode TEXT DEFAULT 'pin_or_ca',
			pin_set TEXT DEFAULT '[]',
			trusted_ca_certificate_ids TEXT DEFAULT '[]',
			allow_self_signed INTEGER DEFAULT 0,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_relay_listeners_agent ON relay_listeners(agent_id)`,
		`CREATE TABLE IF NOT EXISTS managed_certificates (
			id INTEGER PRIMARY KEY,
			domain TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			scope TEXT DEFAULT 'domain',
			issuer_mode TEXT DEFAULT 'master_cf_dns',
			target_agent_ids TEXT DEFAULT '[]',
			status TEXT DEFAULT 'pending',
			last_issue_at TEXT,
			last_error TEXT DEFAULT '',
			material_hash TEXT DEFAULT '',
			agent_reports TEXT DEFAULT '{}',
			acme_info TEXT DEFAULT '{}',
			usage TEXT DEFAULT 'https',
			certificate_type TEXT DEFAULT 'acme',
			self_signed INTEGER DEFAULT 0,
			tags TEXT DEFAULT '[]',
			revision INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS local_agent_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT DEFAULT 'success',
			last_apply_message TEXT DEFAULT '',
			desired_version TEXT DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS version_policy (
			id TEXT PRIMARY KEY,
			channel TEXT DEFAULT 'stable',
			desired_version TEXT DEFAULT '',
			packages TEXT DEFAULT '[]',
			tags TEXT DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
		`INSERT OR IGNORE INTO local_agent_state (
			id, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, desired_version
		) VALUES (1, 0, 0, 0, 'success', '', '')`,
	}

	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
