package marketplace

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
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

func TestVerifiedCacheSeparatesSameDigestSameKeyIDDifferentSigners(t *testing.T) {
	marketRoot := marketplaceFixture(t, false)
	firstKey := marketTestSigningKey()
	firstValidator := plugins.NewValidator(plugins.ValidatorOptions{TrustedSignerPolicy: plugins.TrustedSignerPolicyExact, TrustedSigners: map[string]ed25519.PublicKey{"test-market": firstKey.Public().(ed25519.PublicKey)}})
	validated, err := firstValidator.ValidateMarket(marketRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	firstSource, err := NewSignedCustomSource("first", "First", "https://example.com/first.git", "main", "", 0, SourceSigner{KeyID: "test-market", SecretRef: "vault-first", PublicKey: base64.StdEncoding.EncodeToString(firstKey.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	firstTrust, _ := firstSource.SignatureTrust()

	secondRoot := filepath.Join(t.TempDir(), "package")
	if err := os.MkdirAll(secondRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyRegularTree(validated.Packages[0].Root, secondRoot); err != nil {
		t.Fatal(err)
	}
	secondKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))
	secondSignature := filepath.Join(secondRoot, plugins.PackageSignatureFile)
	if err := os.Chmod(secondSignature, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondSignature, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(secondKey, []byte(validated.Packages[0].Digest)))), 0o600); err != nil {
		t.Fatal(err)
	}
	secondValidator := plugins.NewValidator(plugins.ValidatorOptions{TrustedSignerPolicy: plugins.TrustedSignerPolicyExact, TrustedSigners: map[string]ed25519.PublicKey{"test-market": secondKey.Public().(ed25519.PublicKey)}})
	secondPackage, err := secondValidator.ValidatePackage(secondRoot, plugins.PackageExpectation{SHA256: validated.Packages[0].Digest, SignatureKeyID: "test-market"})
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := NewSignedCustomSource("second", "Second", "https://example.com/second.git", "main", "", 0, SourceSigner{KeyID: "test-market", SecretRef: "vault-second", PublicKey: base64.StdEncoding.EncodeToString(secondKey.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	secondTrust, _ := secondSource.SignatureTrust()

	cache, err := NewVerifiedCache(t.TempDir(), firstValidator, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstPath, err := cache.StoreWithTrust(validated.Packages[0], firstValidator, firstTrust)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := cache.StoreWithTrust(secondPackage, secondValidator, secondTrust)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = unsealCacheTree(filepath.Dir(firstPath))
	})
	if firstPath == secondPath || filepath.Dir(firstPath) != filepath.Dir(secondPath) {
		t.Fatalf("signer-aware cache paths = %q, %q", firstPath, secondPath)
	}
	if _, err := firstValidator.ValidatePackage(firstPath, plugins.PackageExpectation{SHA256: validated.Packages[0].Digest, SignatureKeyID: "test-market"}); err != nil {
		t.Fatalf("second signer polluted first cache envelope: %v", err)
	}
	if _, err := secondValidator.ValidatePackage(secondPath, plugins.PackageExpectation{SHA256: validated.Packages[0].Digest, SignatureKeyID: "test-market"}); err != nil {
		t.Fatalf("first signer polluted second cache envelope: %v", err)
	}
}

func TestCustomSourceRejectsNonCanonicalSignerKeyIDs(t *testing.T) {
	publicKey := base64.StdEncoding.EncodeToString(marketTestSigningKey().Public().(ed25519.PublicKey))
	for _, keyID := range []string{"Uppercase", "under_score", "1starts-with-digit", " test-market "} {
		if _, err := NewSignedCustomSource("community", "Community", "https://example.com/community.git", "main", "", 0, SourceSigner{KeyID: keyID, SecretRef: "vault-key", PublicKey: publicKey}); err == nil {
			t.Fatalf("non-canonical signer key ID %q was accepted", keyID)
		}
	}
}
