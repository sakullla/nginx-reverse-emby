package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type VerifiedCache struct {
	root       string
	validator  *plugins.Validator
	references PackageReferenceChecker
}

var cacheRootLocks [64]sync.Mutex

func NewVerifiedCache(root string, validator *plugins.Validator, references PackageReferenceChecker) (*VerifiedCache, error) {
	if validator == nil {
		return nil, errors.New("plugin validator is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := withCacheRootLock(root, func(root string) error {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		return sealCacheTree(root)
	}); err != nil {
		return nil, fmt.Errorf("seal verified cache root: %w", err)
	}
	return &VerifiedCache{root: root, validator: validator, references: references}, nil
}

func (c *VerifiedCache) Path(digest string) (string, error) {
	return CachePath(c.root, digest)
}

// SignerCachePath returns the package directory for one immutable signature
// identity. Package digests intentionally exclude package.sig, so a digest-only
// directory cannot safely hold envelopes from independent signers.
func SignerCachePath(root, digest, signerFingerprint string) (string, error) {
	container, err := CachePath(root, digest)
	if err != nil {
		return "", err
	}
	signerFingerprint = strings.ToLower(strings.TrimSpace(signerFingerprint))
	if !IsDigest(signerFingerprint) {
		return "", errors.New("invalid package signer fingerprint")
	}
	return filepath.Join(container, signerFingerprint), nil
}

// CachePathMatchesPackage accepts the current signer-aware layout and the
// legacy digest-only layout used by already-installed packages. New market
// acquisitions are always written with SignerCachePath.
func CachePathMatchesPackage(candidate, digest, signerFingerprint string) bool {
	digest = strings.ToLower(strings.TrimSpace(digest))
	cleaned := filepath.Clean(candidate)
	if !IsDigest(digest) {
		return false
	}
	if strings.EqualFold(filepath.Base(cleaned), digest) {
		return true
	}
	return IsDigest(signerFingerprint) &&
		strings.EqualFold(filepath.Base(cleaned), signerFingerprint) &&
		strings.EqualFold(filepath.Base(filepath.Dir(cleaned)), digest)
}

// CachePath is the single containment gate for all digest-addressed cache IO.
func CachePath(root, digest string) (string, error) {
	if !IsDigest(digest) {
		return "", errors.New("invalid package digest")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, strings.ToLower(digest))
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("package cache path escapes managed root")
	}
	return candidate, nil
}

func (c *VerifiedCache) Store(validated plugins.ValidatedPackage) (string, error) {
	return c.StoreWithValidator(validated, c.validator)
}

func (c *VerifiedCache) StoreWithValidator(validated plugins.ValidatedPackage, validator *plugins.Validator) (string, error) {
	fingerprint, err := signatureEnvelopeFingerprint(validated.Root)
	if err != nil {
		return "", err
	}
	return c.store(validated, validator, fingerprint)
}

// StoreWithTrust isolates the canonical package contents by the public-key
// fingerprint captured for the source acquisition. Equal content signed by
// equal key IDs but different keys or sources can therefore coexist without
// reusing another source's package.sig.
func (c *VerifiedCache) StoreWithTrust(validated plugins.ValidatedPackage, validator *plugins.Validator, trust SignatureTrust) (string, error) {
	if err := ValidateSignatureTrust(trust); err != nil {
		return "", err
	}
	if trust.KeyID != validated.Manifest.Signature.KeyID {
		return "", errors.New("verified package signer differs from cache trust binding")
	}
	return c.store(validated, validator, trust.Fingerprint)
}

func (c *VerifiedCache) store(validated plugins.ValidatedPackage, validator *plugins.Validator, signerFingerprint string) (string, error) {
	if validator == nil {
		return "", errors.New("plugin validator is required")
	}
	target, err := SignerCachePath(c.root, validated.Digest, signerFingerprint)
	if err != nil {
		return "", err
	}
	err = withCacheRootLock(c.root, func(root string) (resultErr error) {
		container, err := CachePath(root, validated.Digest)
		if err != nil {
			return err
		}
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			if err := validateCachedPackage(target, validated, validator); err != nil {
				return err
			}
			// Heal caches written by the earlier signer-aware layout, which
			// sealed the signer subtree but left this container replaceable.
			return errors.Join(sealCacheTree(container), sealCacheContainer(root))
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}

		var temporary string
		if err := withCacheRootMutationLocked(root, func() error {
			var createErr error
			temporary, createErr = os.MkdirTemp(root, ".package-")
			return createErr
		}); err != nil {
			return err
		}
		published := false
		containerExisted := false
		committed := false
		defer func() {
			if committed {
				return
			}
			cleanupErr := withCacheRootMutationLocked(root, func() error {
				var cleanupErr error
				if published {
					cleanupErr = errors.Join(cleanupErr, unsealCacheTree(target), os.RemoveAll(target))
					if containerExisted {
						cleanupErr = errors.Join(cleanupErr, sealCacheContainer(container))
					} else {
						cleanupErr = errors.Join(cleanupErr, os.Remove(container))
					}
				}
				if temporary != "" {
					unsealErr := unsealCacheTree(temporary)
					if errors.Is(unsealErr, os.ErrNotExist) {
						unsealErr = nil
					}
					cleanupErr = errors.Join(cleanupErr, unsealErr, os.RemoveAll(temporary))
				}
				return cleanupErr
			})
			resultErr = errors.Join(resultErr, cleanupErr)
		}()
		if err := copyRegularTree(validated.Root, temporary); err != nil {
			return err
		}
		revalidated, err := validator.ValidatePackage(temporary, plugins.PackageExpectation{ID: validated.Manifest.ID, Version: validated.Manifest.Version, SHA256: validated.Digest})
		if err != nil {
			return err
		}
		if revalidated.Digest != validated.Digest {
			return errors.New("package changed while entering verified cache")
		}
		if err := sealCacheTree(temporary); err != nil {
			return err
		}

		publishErr := withCacheRootMutationLocked(root, func() error {
			if info, statErr := os.Lstat(container); statErr == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return errors.New("verified cache digest container is not a directory")
				}
				containerExisted = true
				if err := unsealCacheContainer(container); err != nil {
					return fmt.Errorf("unseal verified cache digest container: %w", err)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			} else if err := os.MkdirAll(container, 0o755); err != nil {
				return err
			}

			publishErr := os.Rename(temporary, target)
			published = publishErr == nil
			if publishErr != nil {
				if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
					publishErr = validateCachedPackage(target, validated, validator)
					if publishErr != nil {
						publishErr = errors.New("concurrent verified cache entry is corrupt")
					}
				}
			}
			return errors.Join(publishErr, sealCacheContainer(container))
		})
		if publishErr != nil {
			return publishErr
		}
		committed = true
		return nil
	})
	if err != nil {
		return "", err
	}
	return target, nil
}

func validateCachedPackage(target string, validated plugins.ValidatedPackage, validator *plugins.Validator) error {
	cached, err := validator.ValidatePackage(target, plugins.PackageExpectation{ID: validated.Manifest.ID, Version: validated.Manifest.Version, SHA256: validated.Digest, SignatureKeyID: validated.Manifest.Signature.KeyID})
	if err != nil || !strings.EqualFold(cached.Digest, validated.Digest) {
		return errors.New("verified cache entry is corrupt")
	}
	return nil
}

func withCacheRootLock(root string, action func(root string) error) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	key := sha256.Sum256([]byte(root))
	lock := &cacheRootLocks[int(key[0])%len(cacheRootLocks)]
	lock.Lock()
	defer lock.Unlock()
	return action(root)
}

func withCacheRootMutationLocked(root string, action func() error) error {
	if err := unsealCacheContainer(root); err != nil {
		return fmt.Errorf("unseal verified cache root: %w", err)
	}
	actionErr := action()
	sealErr := sealCacheContainer(root)
	if sealErr != nil {
		sealErr = fmt.Errorf("reseal verified cache root: %w", sealErr)
	}
	return errors.Join(actionErr, sealErr)
}

func signatureEnvelopeFingerprint(root string) (string, error) {
	value, err := os.ReadFile(filepath.Join(root, plugins.PackageSignatureFile))
	if err != nil {
		return "", fmt.Errorf("read package signature envelope: %w", err)
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

// SealVerifiedPackage applies the platform-specific non-executable seal after
// a caller has completed digest and signature verification (for example during
// data-root migration).
func SealVerifiedPackage(path string) error {
	return sealCacheTree(path)
}

// DiscardSealedVerifiedPackage removes a not-yet-published sealed staging
// package. Production live-cache GC remains fenced by digest and references.
func DiscardSealedVerifiedPackage(path string) error {
	if err := unsealCacheTree(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(path)
}

// DiscardVerifiedCacheRoot explicitly tears down an entire managed cache root.
// Callers must first ensure that no live package users remain; normal runtime
// reclamation must use the reference-fenced per-digest GC paths instead.
func DiscardVerifiedCacheRoot(root string) error {
	return withCacheRootLock(root, func(root string) error {
		info, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("verified cache root teardown target is not a directory")
		}
		if err := unsealCacheTree(root); err != nil {
			return fmt.Errorf("unseal verified cache root for teardown: %w", err)
		}
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("remove unsealed verified cache root: %w", err)
		}
		return nil
	})
}

func (c *VerifiedCache) RemoveUnreferenced(ctx context.Context, digest string) (bool, error) {
	if c.references == nil {
		return false, errors.New("package reference checker is required for cache GC")
	}
	referenced, err := c.references.PackageReferenced(ctx, strings.ToLower(digest))
	if err != nil || referenced {
		return false, err
	}
	err = withCacheRootLock(c.root, func(root string) error {
		path, err := CachePath(root, digest)
		if err != nil {
			return err
		}
		return withCacheRootMutationLocked(root, func() error {
			if err := unsealCacheTree(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unseal verified cache for removal: %w", err)
			}
			if err := os.RemoveAll(path); err != nil {
				resealErr := sealCacheTree(path)
				return errors.Join(fmt.Errorf("remove unsealed verified cache: %w", err), resealErr)
			}
			return nil
		})
	})
	return err == nil, err
}

// QuarantineAndDeleteVerifiedPackage is the only production filesystem path
// for fenced package GC. It deliberately owns unsealing so Windows deny-delete
// ACLs and Unix read-only directory modes cannot strand a claimed cache entry.
func QuarantineAndDeleteVerifiedPackage(root, digest, quarantineRelative string) error {
	live, err := CachePath(root, digest)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	relative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(quarantineRelative)))
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || (relative != ".gc" && !strings.HasPrefix(relative, ".gc"+string(filepath.Separator))) {
		return errors.New("package GC quarantine path is outside the verified cache root")
	}
	quarantine := filepath.Join(root, relative)
	contained, err := filepath.Rel(root, quarantine)
	if err != nil || contained == "." || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return errors.New("package GC quarantine path escapes verified cache root")
	}
	return withCacheRootLock(root, func(root string) error {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		if err := sealCacheContainer(root); err != nil {
			return fmt.Errorf("seal verified cache root before GC: %w", err)
		}
		if err := withCacheRootMutationLocked(root, func() error {
			gcRoot := filepath.Join(root, ".gc")
			if err := unsealCacheTree(gcRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unseal verified cache quarantine root: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(quarantine), 0o755); err != nil {
				return err
			}
			if _, err := os.Stat(quarantine); errors.Is(err, os.ErrNotExist) {
				if err := unsealCacheTree(live); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("unseal verified cache for quarantine: %w", err)
				}
				if err := os.Rename(live, quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
					resealErr := sealCacheTree(live)
					return errors.Join(fmt.Errorf("quarantine verified cache: %w", err), resealErr)
				}
			} else if err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
		if filepath.Clean(quarantine) == filepath.Join(root, ".gc") {
			return withCacheRootMutationLocked(root, func() error {
				if err := unsealCacheTree(quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("unseal quarantined verified cache: %w", err)
				}
				if err := os.RemoveAll(quarantine); err != nil {
					return fmt.Errorf("remove quarantined verified cache: %w", err)
				}
				return nil
			})
		}
		if err := unsealCacheTree(quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unseal quarantined verified cache: %w", err)
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return fmt.Errorf("remove quarantined verified cache: %w", err)
		}
		return nil
	})
}

func copyRegularTree(source, destination string) error {
	return filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to copy symlink %s", rel)
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to copy non-regular file %s", rel)
		}
		input, err := os.Open(current)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o444)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputErr := input.Close()
		outputErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputErr != nil {
			return inputErr
		}
		return outputErr
	})
}

func IsDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range strings.ToLower(value) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
