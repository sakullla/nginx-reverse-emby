package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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

func TestClientPackageServiceLatestSemverPrecedence(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)

	for _, input := range []ClientPackageInput{
		clientPackageInput("1.9.0", "windows", "amd64", "flutter_gui", "1"),
		clientPackageInput("1.10.0", "windows", "amd64", "flutter_gui", "2"),
		clientPackageInput("1.2.0-rc.1", "macos", "arm64", "flutter_gui", "3"),
		clientPackageInput("1.2.0", "macos", "arm64", "flutter_gui", "4"),
		clientPackageInput("2.0.0+build.2", "android", "arm64", "flutter_gui", "5"),
		clientPackageInput("2.0.0+build.1", "android", "arm64", "flutter_gui", "6"),
	} {
		if _, err := svc.Create(ctx, input); err != nil {
			t.Fatalf("Create(%s) error = %v", *input.Version, err)
		}
	}

	latestWindows, err := svc.Latest(ctx, ClientPackageQuery{
		Platform: "windows",
		Arch:     "amd64",
		Kind:     "flutter_gui",
	})
	if err != nil {
		t.Fatalf("Latest(windows) error = %v", err)
	}
	if latestWindows.Version != "1.10.0" {
		t.Fatalf("Latest(windows) Version = %q, want 1.10.0", latestWindows.Version)
	}

	latestMacOS, err := svc.Latest(ctx, ClientPackageQuery{
		Platform: "macos",
		Arch:     "arm64",
		Kind:     "flutter_gui",
	})
	if err != nil {
		t.Fatalf("Latest(macos) error = %v", err)
	}
	if latestMacOS.Version != "1.2.0" {
		t.Fatalf("Latest(macos) Version = %q, want 1.2.0", latestMacOS.Version)
	}

	latestAndroid, err := svc.Latest(ctx, ClientPackageQuery{
		Platform: "android",
		Arch:     "arm64",
		Kind:     "flutter_gui",
	})
	if err != nil {
		t.Fatalf("Latest(android) error = %v", err)
	}
	if latestAndroid.Version != "2.0.0+build.1" {
		t.Fatalf("Latest(android) Version = %q, want deterministic equivalent version without build metadata precedence", latestAndroid.Version)
	}
}

func TestClientPackageServiceLatestNoMatch(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)
	if _, err := svc.Create(ctx, clientPackageInput("1.0.0", "windows", "amd64", "flutter_gui", "1")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.Latest(ctx, ClientPackageQuery{
		Platform: "windows",
		Arch:     "arm64",
		Kind:     "flutter_gui",
	}); !errors.Is(err, ErrClientPackageNotFound) {
		t.Fatalf("Latest() no match error = %v, want ErrClientPackageNotFound", err)
	}
}

func TestClientPackageServiceValidationErrors(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)

	input := clientPackageInput("1.0.0", "windows", "amd64", "flutter_gui", "1")
	input.ID = strPtr("duplicate")
	if _, err := svc.Create(ctx, input); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Create(ctx, input); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() duplicate ID error = %v, want ErrInvalidArgument", err)
	}

	nonHTTPS := clientPackageInput("1.0.1", "windows", "amd64", "flutter_gui", "2")
	nonHTTPS.DownloadURL = strPtr("http://example.com/client.zip")
	if _, err := svc.Create(ctx, nonHTTPS); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() non-HTTPS error = %v, want ErrInvalidArgument", err)
	}

	badWorkerArch := clientPackageInput("1.0.2", "cloudflare_worker", "amd64", "worker_script", "3")
	if _, err := svc.Create(ctx, badWorkerArch); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() bad worker arch error = %v, want ErrInvalidArgument", err)
	}

	badWorkerKind := clientPackageInput("1.0.3", "cloudflare_worker", "script", "flutter_gui", "4")
	if _, err := svc.Create(ctx, badWorkerKind); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Create() bad worker kind error = %v, want ErrInvalidArgument", err)
	}
}

func TestClientPackageServiceMissingIDErrors(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	svc := NewClientPackageService(store)

	if _, err := svc.Update(ctx, "missing", ClientPackageInput{Notes: strPtr("notes")}); !errors.Is(err, ErrClientPackageNotFound) {
		t.Fatalf("Update() missing ID error = %v, want ErrClientPackageNotFound", err)
	}
	if _, err := svc.Update(ctx, "", ClientPackageInput{Notes: strPtr("notes")}); !errors.Is(err, ErrClientPackageNotFound) {
		t.Fatalf("Update() empty ID error = %v, want ErrClientPackageNotFound", err)
	}
	if _, err := svc.Delete(ctx, "missing"); !errors.Is(err, ErrClientPackageNotFound) {
		t.Fatalf("Delete() missing ID error = %v, want ErrClientPackageNotFound", err)
	}
	if _, err := svc.Delete(ctx, " "); !errors.Is(err, ErrClientPackageNotFound) {
		t.Fatalf("Delete() empty ID error = %v, want ErrClientPackageNotFound", err)
	}
}

func clientPackageInput(version, platform, arch, kind, shaSeed string) ClientPackageInput {
	return ClientPackageInput{
		Version:     strPtr(version),
		Platform:    strPtr(platform),
		Arch:        strPtr(arch),
		Kind:        strPtr(kind),
		DownloadURL: strPtr("https://example.com/" + version + "/" + platform + "-" + arch + ".zip"),
		SHA256:      strPtr(strings.Repeat(shaSeed, 64)),
	}
}

func strPtr(value string) *string {
	return &value
}
