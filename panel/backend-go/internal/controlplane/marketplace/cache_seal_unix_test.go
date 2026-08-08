//go:build !windows

package marketplace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestUnixSignerAwareCacheRootRejectsDigestReplacement(t *testing.T) {
	marketRoot := marketplaceFixture(t, false)
	validator := marketTestValidator(plugins.ValidatorOptions{})
	validated, err := validator.ValidateMarket(marketRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	cache, err := NewVerifiedCache(cacheRoot, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := cache.Store(validated.Packages[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unsealCacheTree(cacheRoot) })
	rootInfo, err := os.Stat(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm()&0o222 != 0 {
		t.Fatalf("cache root retained write permission: %v", rootInfo.Mode())
	}

	container := filepath.Dir(stored)
	displaced := filepath.Join(filepath.Dir(cacheRoot), ".displaced-digest")
	renameErr := os.Rename(container, displaced)
	if renameErr == nil {
		// UID 0 can bypass mode bits. Restore the fixture before asserting the
		// portable boundary; unprivileged production identities must be denied.
		if err := unsealCacheContainer(cacheRoot); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(displaced, container); err != nil {
			t.Fatalf("restore root-bypassed digest rename: %v", err)
		}
		if err := sealCacheContainer(cacheRoot); err != nil {
			t.Fatal(err)
		}
		if os.Geteuid() != 0 {
			t.Fatal("sealed cache root allowed digest replacement")
		}
	} else if !errors.Is(renameErr, fs.ErrPermission) {
		t.Fatalf("digest directory rename failed for an unexpected reason: %v", renameErr)
	}
}
