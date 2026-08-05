package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureConsistentPKISQLiteIncludesCommittedWALState(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("real SQLite backup snapshots run in the full test tier")
	}
	root := t.TempDir()
	dsn := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatalf("NewStore(snapshot) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	writer, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(writer) error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if err := store.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		t.Fatalf("truncate WAL before independent write: %v", err)
	}
	if err := writer.ensureSQLiteWriteDB(); err != nil {
		t.Fatalf("open independent SQLite writer: %v", err)
	}
	var journalMode string
	if err := writer.writeDB.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("read writer journal mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("writer journal mode = %q, want WAL", journalMode)
	}
	if err := writer.writeDB.Exec("PRAGMA wal_autocheckpoint=0").Error; err != nil {
		t.Fatalf("disable writer WAL autocheckpoint: %v", err)
	}
	now := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	if err := writer.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), PKISettingsRow{
			PKIDomainID: "domain-backup", CALifetimeSeconds: int64(365 * 24 * time.Hour / time.Second),
			EndpointLifetimeSeconds: int64(90 * 24 * time.Hour / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 2, SecurityRevision: 0, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.CreatePKIEnrollmentToken(t.Context(), PKIEnrollmentTokenRow{
			ID: "token-backup", TokenDigestSHA256: digest, Scope: "new_agent",
			ExpiresAt: now.Add(time.Hour), CreatedBy: "operator", CreatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed PKI state: %v", err)
	}
	walInfo, err := os.Stat(dsn + "-wal")
	if err != nil {
		t.Fatalf("stat uncheckpointed WAL: %v", err)
	}
	if walInfo.Size() <= 32 {
		t.Fatalf("WAL size = %d, want committed frames beyond the header", walInfo.Size())
	}

	snapshot, err := store.CaptureConsistentPKISQLite(t.Context())
	if err != nil {
		t.Fatalf("CaptureConsistentPKISQLite() error = %v", err)
	}
	if !bytes.HasPrefix(snapshot, []byte("SQLite format 3\x00")) || !bytes.Contains(snapshot, []byte(digest)) {
		t.Fatal("captured snapshot is not a standalone image of the committed PKI state")
	}
	targetRoot := t.TempDir()
	targetPath := filepath.Join(targetRoot, "panel.db")
	if err := os.WriteFile(targetPath, snapshot, 0o600); err != nil {
		t.Fatalf("write captured snapshot: %v", err)
	}
	target, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: targetPath, DataRoot: targetRoot, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("open captured snapshot: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })
	state, err := target.LoadPKICanonicalState(t.Context())
	if err != nil || state.Settings == nil || state.Settings.PKIDomainID != "domain-backup" || len(state.EnrollmentTokens) != 1 {
		t.Fatalf("captured canonical state = %+v, error = %v", state, err)
	}
}

func TestReadBoundedPKISQLiteSnapshotRejectsOversizedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(path, []byte("123456"), 0o600); err != nil {
		t.Fatalf("write oversized fixture: %v", err)
	}
	if _, err := readBoundedPKISQLiteSnapshot(path, 5); err == nil || !strings.Contains(err.Error(), "exceeds 5 bytes") {
		t.Fatalf("readBoundedPKISQLiteSnapshot() error = %v, want bounded-size rejection", err)
	}
}
