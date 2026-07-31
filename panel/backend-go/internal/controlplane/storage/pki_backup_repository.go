package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxProtectedPKISnapshotBytes is shared with the backup envelope service so
// the repository rejects oversized SQLite images before allocating them.
const MaxProtectedPKISnapshotBytes = 512 << 20

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
	snapshot, err := readBoundedPKISQLiteSnapshot(path, MaxProtectedPKISnapshotBytes)
	if err != nil {
		return nil, fmt.Errorf("read consistent PKI SQLite snapshot: %w", err)
	}
	return snapshot, nil
}

func readBoundedPKISQLiteSnapshot(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("snapshot size limit must be positive")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 {
		return nil, fmt.Errorf("consistent PKI SQLite snapshot is empty")
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("consistent PKI SQLite snapshot exceeds %d bytes", maxBytes)
	}
	snapshot, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(snapshot) == 0 {
		return nil, fmt.Errorf("consistent PKI SQLite snapshot is empty")
	}
	if int64(len(snapshot)) > maxBytes {
		return nil, fmt.Errorf("consistent PKI SQLite snapshot exceeds %d bytes", maxBytes)
	}
	return snapshot, nil
}
