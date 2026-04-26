package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSQLiteStoreClientPackagesRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	windowsPackage := ClientPackageRow{
		ID:          "pkg-windows-amd64",
		Version:     "1.1.0",
		Platform:    "windows",
		Arch:        "amd64",
		Kind:        "flutter_gui",
		DownloadURL: "https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip",
		SHA256:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Notes:       "desktop gui",
		CreatedAt:   "2026-04-26T00:00:00Z",
	}
	workerPackage := ClientPackageRow{
		ID:          "pkg-worker-script",
		Version:     "1.1.0",
		Platform:    "cloudflare_worker",
		Arch:        "script",
		Kind:        "worker_script",
		DownloadURL: "https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js",
		SHA256:      "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
		Notes:       "worker script",
		CreatedAt:   "2026-04-26T00:01:00Z",
	}
	rows := []ClientPackageRow{
		workerPackage,
		windowsPackage,
	}

	if err := store.SaveClientPackages(ctx, rows); err != nil {
		t.Fatalf("SaveClientPackages() error = %v", err)
	}
	got, err := store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() error = %v", err)
	}
	want := []ClientPackageRow{windowsPackage, workerPackage}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListClientPackages() = %+v, want %+v", got, want)
	}

	if err := store.SaveClientPackages(ctx, []ClientPackageRow{windowsPackage}); err != nil {
		t.Fatalf("SaveClientPackages(replace) error = %v", err)
	}
	got, err = store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() after replace error = %v", err)
	}
	want = []ClientPackageRow{windowsPackage}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListClientPackages() after replace = %+v, want %+v", got, want)
	}
}
