package certs

import (
	"errors"
	"testing"
)

func TestSyncACMERollbackDirectoriesSyncsCurrentAndMaterial(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("material directory sync failed")
	var synced []string
	err := syncACMERollbackDirectories("state/current", "certificates/17", func(path string) error {
		synced = append(synced, path)
		if path == "certificates/17" {
			return wantErr
		}
		return nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("syncACMERollbackDirectories() error = %v, want %v", err, wantErr)
	}
	if len(synced) != 2 || synced[0] != "state/current" || synced[1] != "certificates/17" {
		t.Fatalf("synced directories = %v", synced)
	}
}
