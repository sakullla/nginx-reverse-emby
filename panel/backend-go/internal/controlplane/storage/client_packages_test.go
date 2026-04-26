package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteStoreClientPackagesRoundTrip(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	rows := []ClientPackageRow{
		{
			ID:          "pkg-windows-amd64",
			Version:     "1.1.0",
			Platform:    "windows",
			Arch:        "amd64",
			Kind:        "flutter_gui",
			DownloadURL: "https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip",
			SHA256:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Notes:       "desktop gui",
			CreatedAt:   "2026-04-26T00:00:00Z",
		},
		{
			ID:          "pkg-worker-script",
			Version:     "1.1.0",
			Platform:    "cloudflare_worker",
			Arch:        "script",
			Kind:        "worker_script",
			DownloadURL: "https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js",
			SHA256:      "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd",
			Notes:       "worker script",
			CreatedAt:   "2026-04-26T00:01:00Z",
		},
	}

	if err := store.SaveClientPackages(ctx, rows); err != nil {
		t.Fatalf("SaveClientPackages() error = %v", err)
	}
	got, err := store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
	}
	if got[0].ID != "pkg-worker-script" && got[1].ID != "pkg-worker-script" {
		t.Fatalf("worker package missing: %+v", got)
	}

	if err := store.SaveClientPackages(ctx, rows[:1]); err != nil {
		t.Fatalf("SaveClientPackages(replace) error = %v", err)
	}
	got, err = store.ListClientPackages(ctx)
	if err != nil {
		t.Fatalf("ListClientPackages() after replace error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "pkg-windows-amd64" {
		t.Fatalf("replace got %+v", got)
	}
}
