package http

import (
	"os"
	"path/filepath"
	"testing"

	marketplacepkg "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
)

func TestMarketplaceInitializationResolvesOfficialLockFromBackendWorkingDirectory(t *testing.T) {
	backendRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(backendRoot, "go.mod")); err != nil {
		t.Fatalf("resolve backend working directory fixture: %v", err)
	}
	t.Chdir(backendRoot)
	t.Setenv(marketplacepkg.OfficialMarketLockPathEnv, "")

	resolved, err := marketplacepkg.ResolveOfficialMarketLockPath(os.Getenv(marketplacepkg.OfficialMarketLockPathEnv))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(filepath.Join(backendRoot, "..", "..", marketplacepkg.OfficialMarketLockFile))
	if resolved != want {
		t.Fatalf("official lock path = %q, want %q", resolved, want)
	}
}
