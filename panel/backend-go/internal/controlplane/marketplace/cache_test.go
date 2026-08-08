package marketplace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

func TestCachePathRequiresCanonicalHexDigestAndManagedContainment(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	path, err := CachePath(root, digest)
	if err != nil || path != filepath.Join(root, digest) {
		t.Fatalf("CachePath() = %q, %v", path, err)
	}
	for _, invalid := range []string{"../outside", `..\outside`, strings.Repeat("g", 64), strings.Repeat("a", 63), digest + "/child"} {
		if _, err := CachePath(root, invalid); err == nil {
			t.Fatalf("invalid digest %q was accepted", invalid)
		}
	}
}

func TestRuntimeArtifactCacheIsReadOnlyAndNonExecutable(t *testing.T) {
	marketRoot := marketplaceFixture(t, false)
	validator := marketTestValidator(plugins.ValidatorOptions{})
	validated, err := validator.ValidateMarket(marketRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	repository := &memoryRepository{current: map[string]Snapshot{}, referenced: map[string]bool{}}
	cache, err := NewVerifiedCache(cacheRoot, validator, repository)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := cache.Store(validated.Packages[0])
	if err != nil {
		t.Fatal(err)
	}
	defer makeCacheTreeRemovable(stored)
	artifact := filepath.Join(stored, "artifacts", "policy.wasm")
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("cached artifact is executable: %v", info.Mode())
	}
	if file, openErr := os.OpenFile(artifact, os.O_WRONLY, 0); openErr == nil {
		_ = file.Close()
		t.Fatal("cached artifact remained writable")
	}
	if removed, err := cache.RemoveUnreferenced(context.Background(), validated.Packages[0].Digest); err != nil || !removed {
		t.Fatalf("remove frozen cache: removed=%v err=%v", removed, err)
	}
}
