package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestClientPackageServiceCRUDAndValidation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)
	fixedNow := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixedNow }

	created, err := svc.Create(ctx, ClientPackageInput{
		Version:     strPtr("1.0.0"),
		Platform:    strPtr(" Windows "),
		Arch:        strPtr("amd64"),
		Kind:        strPtr("flutter_gui"),
		DownloadURL: strPtr("https://example.com/nre-client-windows-amd64.zip"),
		SHA256:      strPtr("0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF"),
		Notes:       strPtr(" desktop gui "),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == "" {
		t.Fatalf("Create() ID is empty")
	}
	if created.Platform != "windows" {
		t.Fatalf("Create() Platform = %q, want windows", created.Platform)
	}
	if created.SHA256 != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("Create() SHA256 = %q, want normalized lower-case sha", created.SHA256)
	}
	if created.Notes != "desktop gui" {
		t.Fatalf("Create() Notes = %q, want trimmed notes", created.Notes)
	}
	if created.CreatedAt != "2026-04-26T00:00:00Z" {
		t.Fatalf("Create() CreatedAt = %q, want fixed timestamp", created.CreatedAt)
	}

	listed, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(listed, []ClientPackage{created}) {
		t.Fatalf("List() = %+v, want %+v", listed, []ClientPackage{created})
	}

	updated, err := svc.Update(ctx, created.ID, ClientPackageInput{
		Notes: strPtr(" refreshed notes "),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Notes != "refreshed notes" {
		t.Fatalf("Update() Notes = %q, want refreshed notes", updated.Notes)
	}
	if updated.DownloadURL != created.DownloadURL {
		t.Fatalf("Update() DownloadURL = %q, want preserved %q", updated.DownloadURL, created.DownloadURL)
	}
	if updated.ID != created.ID {
		t.Fatalf("Update() ID = %q, want preserved %q", updated.ID, created.ID)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatalf("Update() CreatedAt = %q, want preserved %q", updated.CreatedAt, created.CreatedAt)
	}

	if _, err := svc.Create(ctx, ClientPackageInput{
		Version:     strPtr("1.0.1"),
		Platform:    strPtr("windows"),
		Arch:        strPtr("amd64"),
		Kind:        strPtr("flutter_gui"),
		DownloadURL: strPtr("https://example.com/nre-client-windows-amd64-v1.0.1.zip"),
		SHA256:      strPtr("not-a-sha"),
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() invalid SHA error = %v, want ErrInvalidArgument", err)
	}

	deleted, err := svc.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !reflect.DeepEqual(deleted, updated) {
		t.Fatalf("Delete() = %+v, want %+v", deleted, updated)
	}
}

func TestClientPackageServiceLatestCompatible(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)
	svc.now = func() time.Time {
		return time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	}

	for _, input := range []ClientPackageInput{
		{
			Version:     strPtr("1.0.0"),
			Platform:    strPtr("windows"),
			Arch:        strPtr("amd64"),
			Kind:        strPtr("flutter_gui"),
			DownloadURL: strPtr("https://example.com/nre-client-windows-amd64-v1.0.0.zip"),
			SHA256:      strPtr("1111111111111111111111111111111111111111111111111111111111111111"),
		},
		{
			Version:     strPtr("1.2.0"),
			Platform:    strPtr("windows"),
			Arch:        strPtr("amd64"),
			Kind:        strPtr("flutter_gui"),
			DownloadURL: strPtr("https://example.com/nre-client-windows-amd64-v1.2.0.zip"),
			SHA256:      strPtr("2222222222222222222222222222222222222222222222222222222222222222"),
		},
		{
			Version:     strPtr("1.9.0"),
			Platform:    strPtr("macos"),
			Arch:        strPtr("amd64"),
			Kind:        strPtr("flutter_gui"),
			DownloadURL: strPtr("https://example.com/nre-client-macos-amd64-v1.9.0.zip"),
			SHA256:      strPtr("3333333333333333333333333333333333333333333333333333333333333333"),
		},
	} {
		if _, err := svc.Create(ctx, input); err != nil {
			t.Fatalf("Create(%s) error = %v", *input.Version, err)
		}
	}

	latest, err := svc.Latest(ctx, ClientPackageQuery{
		Platform: "windows",
		Arch:     "amd64",
		Kind:     "flutter_gui",
	})
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if latest.Version != "1.2.0" {
		t.Fatalf("Latest() Version = %q, want 1.2.0", latest.Version)
	}
}

func strPtr(value string) *string {
	return &value
}
