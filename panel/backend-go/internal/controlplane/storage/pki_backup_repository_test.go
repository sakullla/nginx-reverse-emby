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
	if testing.Short() {
		t.Skip("real SQLite backup snapshots run in the full test tier")
	}
	root := t.TempDir()
	store, err := NewStore(StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	if err := store.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		if err := tx.CreatePKISettings(t.Context(), PKISettingsRow{
			PKIDomainID: "domain-backup", CALifetimeSeconds: int64(365 * 24 * time.Hour / time.Second),
			EndpointLifetimeSeconds: int64(90 * 24 * time.Hour / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 2, SecurityRevision: 3, CreatedAt: now, UpdatedAt: now,
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
