package storage

import "testing"

func requireStorageIntegration(t *testing.T) {
	t.Helper()
	if !storageIntegrationTestsEnabled {
		t.Skip("full SQLite migration coverage runs with -tags=integration")
	}
}
