package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStoreLoadsAgentsAndRulesFromExistingSQLite(t *testing.T) {
	seedSQLiteFixture(t)

	store, err := NewSQLiteStore(filepath.Join("testdata", "panel-data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}

	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents() = %v, %v", agents, err)
	}

	rules, err := store.ListHTTPRules(t.Context(), "local")
	if err != nil || len(rules) == 0 {
		t.Fatalf("ListHTTPRules() = %v, %v", rules, err)
	}

	localState, err := store.LoadLocalAgentState(t.Context())
	if err != nil {
		t.Fatalf("LoadLocalAgentState() error = %v", err)
	}
	if localState.DesiredRevision == 0 {
		t.Fatalf("LoadLocalAgentState() returned empty state: %+v", localState)
	}
}

func seedSQLiteFixture(t *testing.T) {
	t.Helper()

	dataRoot := filepath.Join("testdata", "panel-data")
	dbPath := filepath.Join(dataRoot, "panel.db")
	if err := os.RemoveAll(dataRoot); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dataRoot)
	})

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	statements := []string{
		`CREATE TABLE agents (
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
			desired_revision INTEGER NOT NULL DEFAULT 0,
			current_revision INTEGER NOT NULL DEFAULT 0,
			last_apply_revision INTEGER NOT NULL DEFAULT 0,
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
		`CREATE TABLE rules (
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
		`CREATE INDEX idx_rules_agent ON rules(agent_id)`,
		`CREATE TABLE local_agent_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			desired_revision INTEGER DEFAULT 0,
			current_revision INTEGER DEFAULT 0,
			last_apply_revision INTEGER DEFAULT 0,
			last_apply_status TEXT DEFAULT 'success',
			last_apply_message TEXT DEFAULT '',
			desired_version TEXT DEFAULT ''
		)`,
		`INSERT INTO agents (
			id, name, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, is_local, mode, desired_version, platform, tags, capabilities
		) VALUES ('local', 'Local Agent', 3, 2, 2, 'success', '', 1, 'pull', 'v1.2.3', 'linux-amd64', '[]', '[]')`,
		`INSERT INTO rules (
			id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect, relay_chain, pass_proxy_headers, user_agent, custom_headers, revision
		) VALUES (1, 'local', 'https://emby.example.com', 'http://emby:8096', '[{"url":"http://emby:8096"}]', '{"strategy":"round_robin"}', 1, '[]', 1, '[]', 1, '', '[]', 3)`,
		`INSERT INTO local_agent_state (
			id, desired_revision, current_revision, last_apply_revision, last_apply_status, last_apply_message, desired_version
		) VALUES (1, 3, 2, 2, 'success', '', 'v1.2.3')`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("db.Exec() error = %v", err)
		}
	}
}
