package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CaptureConsistentPKISQLite produces a standalone transactionally consistent
// SQLite image with VACUUM INTO. It serializes against this store's writers;
// SQLite itself supplies snapshot isolation against writers in other
// processes. Sanitization remains the service layer's responsibility.
func (s *GormStore) CaptureConsistentPKISQLite(ctx context.Context) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("PKI SQLite snapshot store is unavailable")
	}
	if s.driver != "sqlite" {
		return nil, fmt.Errorf("protected PKI backup requires the SQLite storage driver")
	}
	directory, err := os.MkdirTemp("", "nre-pki-snapshot-")
	if err != nil {
		return nil, fmt.Errorf("create PKI SQLite snapshot directory: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("restrict PKI SQLite snapshot directory: %w", err)
	}
	path := filepath.Join(directory, "panel.db")

	s.sqliteWrite.Lock()
	result := s.db.WithContext(ctx).Exec("VACUUM INTO ?", path)
	s.sqliteWrite.Unlock()
	if result.Error != nil {
		return nil, fmt.Errorf("capture consistent PKI SQLite snapshot: %w", result.Error)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("restrict PKI SQLite snapshot: %w", err)
	}
	snapshot, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read consistent PKI SQLite snapshot: %w", err)
	}
	if len(snapshot) == 0 {
		return nil, fmt.Errorf("consistent PKI SQLite snapshot is empty")
	}
	return snapshot, nil
}
