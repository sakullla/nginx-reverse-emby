package marketplace

import (
	"context"
	"errors"
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

func TestFencedPackageGCUnsealsQuarantinesAndDeletesSealedCache(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	live, err := CachePath(root, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "artifact.bin"), []byte("sealed"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unsealCacheTree(live)
		_ = os.RemoveAll(live)
	})
	if err := sealCacheTree(live); err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join(".gc", digest+"-fence"))
	if err := QuarantineAndDeleteVerifiedPackage(root, digest, relative); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed live cache remains after fenced GC: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed quarantine remains after fenced GC: %v", err)
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
	defer unsealCacheTree(stored)
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
