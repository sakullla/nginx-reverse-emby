package storage

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginMigrationRejectsDigestNamedPackageOutsideSourceCacheRoot(t *testing.T) {
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })

	item := addMigrationRollbackPackage(t, source, "outside.cache.migration", ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!")))
	outside := filepath.Join(t.TempDir(), item.row.SignatureFingerprint, item.row.Digest)
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(item.row.CachePath, outside); err != nil {
		t.Fatal(err)
	}
	if err := source.db.Model(&PluginPackageRow{}).Where("identity = ?", item.row.Identity).Update("cache_path", outside).Error; err != nil {
		t.Fatal(err)
	}

	migrationErr := CopyDefaultMigrationRows(t.Context(), source, target)
	if migrationErr == nil || !strings.Contains(migrationErr.Error(), "outside the managed root") {
		t.Fatalf("outside-root package migration error = %v", migrationErr)
	}
	var targetPackages int64
	if err := target.db.Model(&PluginPackageRow{}).Count(&targetPackages).Error; err != nil || targetPackages != 0 {
		t.Fatalf("target package count after rejected migration = %d, %v", targetPackages, err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("rejected migration mutated outside source package: %v", err)
	}
}
