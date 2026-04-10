package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStoreLoadsAgentsAndRulesFromExistingSQLite(t *testing.T) {
	dataRoot := seedSQLiteFixtureFromCanonicalSchema(t)

	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.db.Close()
	})

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

func seedSQLiteFixtureFromCanonicalSchema(t *testing.T) string {
	t.Helper()

	dataRoot := t.TempDir()
	dbPath := filepath.Join(dataRoot, "panel.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	repoRoot := repositoryRoot(t)
	for _, stmt := range loadControlPlaneBaseSchemaStatements(t, repoRoot) {
		execSQLiteStatement(t, db, stmt, false)
	}
	for _, stmt := range loadPrismaMigrationStatements(t, repoRoot) {
		execSQLiteStatement(t, db, stmt, true)
	}

	statements := []string{
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
		execSQLiteStatement(t, db, stmt, false)
	}

	return dataRoot
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filePath), "..", "..", "..", "..", ".."))
}

func loadControlPlaneBaseSchemaStatements(t *testing.T, repoRoot string) []string {
	t.Helper()

	sourcePath := filepath.Join(repoRoot, "panel", "backend", "storage-prisma-core.js")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", sourcePath, err)
	}

	source := string(sourceBytes)
	const startMarker = "const SCHEMA_STATEMENTS = ["
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("SCHEMA_STATEMENTS not found in %s", sourcePath)
	}

	body := source[start+len(startMarker):]
	end := strings.Index(body, "];")
	if end < 0 {
		t.Fatalf("SCHEMA_STATEMENTS terminator not found in %s", sourcePath)
	}

	var statements []string
	for i := 0; i < end; i++ {
		delimiter := body[i]
		if delimiter != '`' && delimiter != '"' && delimiter != '\'' {
			continue
		}

		statement, nextIndex, ok := readJavaScriptStringLiteral(body, i)
		if !ok {
			t.Fatalf("failed to parse schema statement in %s", sourcePath)
		}
		trimmed := strings.TrimSpace(statement)
		if trimmed != "" {
			statements = append(statements, trimmed)
		}
		i = nextIndex - 1
	}

	if len(statements) == 0 {
		t.Fatalf("no schema statements parsed from %s", sourcePath)
	}
	return statements
}

func readJavaScriptStringLiteral(source string, start int) (string, int, bool) {
	delimiter := source[start]
	var builder strings.Builder
	escaped := false

	for i := start + 1; i < len(source); i++ {
		ch := source[i]
		if escaped {
			builder.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && delimiter != '`' {
			escaped = true
			continue
		}
		if ch == delimiter {
			return builder.String(), i + 1, true
		}
		builder.WriteByte(ch)
	}

	return "", 0, false
}

func loadPrismaMigrationStatements(t *testing.T, repoRoot string) []string {
	t.Helper()

	migrationsDir := filepath.Join(repoRoot, "panel", "backend", "prisma", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("os.ReadDir(%q) error = %v", migrationsDir, err)
	}

	var statements []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		sqlPath := filepath.Join(migrationsDir, entry.Name())
		sqlBytes, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%q) error = %v", sqlPath, err)
		}
		statements = append(statements, splitSQLStatements(string(sqlBytes))...)
	}

	if len(statements) == 0 {
		t.Fatalf("no Prisma migration statements found in %s", migrationsDir)
	}
	return statements
}

func splitSQLStatements(sqlText string) []string {
	rawStatements := strings.Split(sqlText, ";")
	statements := make([]string, 0, len(rawStatements))
	for _, raw := range rawStatements {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

func execSQLiteStatement(t *testing.T, db *sql.DB, stmt string, allowDuplicate bool) {
	t.Helper()

	if _, err := db.Exec(stmt); err != nil {
		if allowDuplicate && isIgnorableMigrationError(err) {
			return
		}
		t.Fatalf("db.Exec(%q) error = %v", stmt, err)
	}
}

func isIgnorableMigrationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "duplicate column name") || strings.Contains(message, "already exists")
}
