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
		return ensureCacheBoundaryLocked(root)
	}); err != nil {
		return nil, fmt.Errorf("seal verified cache root: %w", err)
	}
	return &VerifiedCache{root: root, validator: validator, references: references}, nil
}

// ImportVerifiedPackage is the canonical migration/import entry point for a
// source package that has already passed its source-bound validator. It stages,
// revalidates, publishes, and reseals under the same managed cache-root lock as
// live marketplace acquisition.
func ImportVerifiedPackage(root string, validated plugins.ValidatedPackage, validator *plugins.Validator, trust SignatureTrust) (string, error) {
	cache, err := NewVerifiedCache(root, validator, nil)
	if err != nil {
		return "", err
	}
	return cache.StoreWithTrust(validated, validator, trust)
}

func (c *VerifiedCache) Path(digest string) (string, error) {
	return CachePath(c.root, digest)
}

// SignerCachePath returns the package directory for one immutable signature
// identity. Package digests intentionally exclude package.sig, so a digest-only
// directory cannot safely hold envelopes from independent signers.
func SignerCachePath(root, digest, signerFingerprint string) (string, error) {
	if !IsDigest(digest) {
		return "", errors.New("invalid package digest")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	signerFingerprint = strings.ToLower(strings.TrimSpace(signerFingerprint))
	if !IsDigest(signerFingerprint) {
		return "", errors.New("invalid package signer fingerprint")
	}
	candidate := filepath.Join(root, signerFingerprint, strings.ToLower(digest))
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("signer-aware package cache path escapes managed root")
	}
	return candidate, nil
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
		return true // canonical signer/digest or legacy digest-only layout
	}
	// Compatibility with the earlier digest/signer layout.
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
		container := filepath.Dir(target)
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			if err := validateCachedPackage(target, validated, validator); err != nil {
				return err
			}
			// Heal a signer container restored by migration or an interrupted
			// older cache implementation.
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
	if filepath.Base(root) != "packages" || filepath.Base(filepath.Dir(root)) != "plugins" {
		return errors.New("verified cache root must use a dedicated plugins/packages boundary")
	}
	key := sha256.Sum256([]byte(root))
	lock := &cacheRootLocks[int(key[0])%len(cacheRootLocks)]
	lock.Lock()
	defer lock.Unlock()
	return action(root)
}

func ensureCacheBoundaryLocked(root string) error {
	anchor := filepath.Dir(root)
	if anchor == root {
		return errors.New("verified cache root requires a dedicated parent anchor")
	}
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		return err
	}
	anchorInfo, err := os.Lstat(anchor)
	if err != nil {
		return err
	}
	if !anchorInfo.IsDir() || anchorInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("verified cache parent anchor is not a directory")
	}
	rootInfo, rootErr := os.Lstat(root)
	if errors.Is(rootErr, os.ErrNotExist) {
		if err := unsealCacheContainer(anchor); err != nil {
			return fmt.Errorf("unseal verified cache parent anchor: %w", err)
		}
		createErr := os.Mkdir(root, 0o755)
		sealErr := sealCacheContainer(anchor)
		if createErr != nil || sealErr != nil {
			return errors.Join(createErr, sealErr)
		}
		rootInfo, rootErr = os.Lstat(root)
	}
	if rootErr != nil {
		return rootErr
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("verified cache root is not a directory")
	}
	return errors.Join(sealCacheTree(root), sealCacheContainer(anchor))
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

// SealVerifiedPackage applies a leaf-only seal. Deprecated: managed cache
// migration/import must use ImportVerifiedPackage so publication is protected
// by the cache root and its dedicated parent anchor.
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
		anchor := filepath.Dir(root)
		info, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			anchorInfo, anchorErr := os.Lstat(anchor)
			if errors.Is(anchorErr, os.ErrNotExist) {
				return nil
			}
			if anchorErr != nil || !anchorInfo.IsDir() || anchorInfo.Mode()&os.ModeSymlink != 0 {
				return errors.New("verified cache parent anchor teardown target is not a directory")
			}
			if err := unsealCacheContainer(anchor); err != nil {
				return fmt.Errorf("unseal empty verified cache parent anchor for teardown: %w", err)
			}
			if err := os.Remove(anchor); err != nil {
				return errors.Join(fmt.Errorf("remove empty verified cache parent anchor: %w", err), sealCacheContainer(anchor))
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("verified cache root teardown target is not a directory")
		}
		anchorInfo, err := os.Lstat(anchor)
		if err != nil || !anchorInfo.IsDir() || anchorInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("verified cache parent anchor teardown target is not a directory")
		}
		if err := unsealCacheContainer(anchor); err != nil {
			return fmt.Errorf("unseal verified cache parent anchor for teardown: %w", err)
		}
		if err := unsealCacheTree(root); err != nil {
			return errors.Join(fmt.Errorf("unseal verified cache root for teardown: %w", err), sealCacheTree(root), sealCacheContainer(anchor))
		}
		if err := os.RemoveAll(root); err != nil {
			return errors.Join(fmt.Errorf("remove unsealed verified cache root: %w", err), sealCacheTree(root), sealCacheContainer(anchor))
		}
		if err := os.Remove(anchor); err != nil {
			return errors.Join(fmt.Errorf("remove dedicated verified cache parent anchor: %w", err), sealCacheContainer(anchor))
		}
		return nil
	})
}

func (c *VerifiedCache) RemoveUnreferenced(ctx context.Context, digest string) (bool, error) {
	if c.references == nil {
		return false, errors.New("package reference checker is required for cache GC")
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !IsDigest(digest) {
		return false, errors.New("invalid package digest")
	}
	removed := false
	err := withCacheRootLock(c.root, func(root string) error {
		if err := ensureCacheBoundaryLocked(root); err != nil {
			return fmt.Errorf("seal verified cache boundary before unreferenced removal: %w", err)
		}
		referenced, err := c.references.PackageReferenced(ctx, digest)
		if err != nil || referenced {
			return err
		}
		targets, err := signerVariantPathsForDigest(root, digest)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return nil
		}
		return withCacheRootMutationLocked(root, func() error {
			for _, target := range targets {
				container := filepath.Dir(target)
				if err := unsealCacheContainer(container); err != nil {
					return fmt.Errorf("unseal verified cache signer container: %w", err)
				}
				if err := unsealCacheTree(target); err != nil {
					return errors.Join(fmt.Errorf("unseal verified cache signer variant: %w", err), sealCacheContainer(container))
				}
				if err := os.RemoveAll(target); err != nil {
					return errors.Join(fmt.Errorf("remove unreferenced verified cache signer variant: %w", err), sealCacheTree(target), sealCacheContainer(container))
				}
				if err := sealOrRemoveEmptyCacheContainer(container); err != nil {
					return fmt.Errorf("finalize verified cache signer container: %w", err)
				}
			}
			removed = true
			return nil
		})
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

func signerVariantPathsForDigest(root, digest string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !IsDigest(entry.Name()) {
			continue
		}
		container := filepath.Join(root, entry.Name())
		info, err := os.Lstat(container)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("verified cache signer container is not a directory")
		}
		target := filepath.Join(container, digest)
		info, err = os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("verified cache signer variant is not a directory")
		}
		targets = append(targets, target)
	}
	return targets, nil
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
		if err := ensureCacheBoundaryLocked(root); err != nil {
			return fmt.Errorf("seal verified cache boundary before GC: %w", err)
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

// PackageGCQuarantinePath returns the only accepted quarantine identity for a
// signer-variant GC claim. Binding the source claim token, signer fingerprint,
// and package digest prevents a resumed or stale claim from selecting another
// immutable cache variant.
func PackageGCQuarantinePath(claim PackageGCClaim) (string, error) {
	digest := strings.ToLower(strings.TrimSpace(claim.Digest))
	fingerprint := strings.ToLower(strings.TrimSpace(claim.SignerFingerprint))
	token := strings.TrimSpace(claim.Token)
	if strings.TrimSpace(claim.SourceID) == "" || !IsDigest(digest) || !IsDigest(fingerprint) || !isCacheGCClaimToken(token) {
		return "", errors.New("valid signer-aware package GC claim is required")
	}
	relative := filepath.Join(".gc", fingerprint, digest+"-"+token)
	if persisted := strings.TrimSpace(claim.QuarantinePath); persisted != "" && filepath.Clean(filepath.FromSlash(persisted)) != relative {
		return "", errors.New("package GC quarantine path does not match its signer-aware claim")
	}
	return filepath.ToSlash(relative), nil
}

// QuarantineAndDeleteVerifiedPackageVariant is the canonical production
// filesystem operation for a storage-fenced signer-variant GC claim. It is
// restart-safe: a persisted claim whose live directory was already renamed to
// its quarantine identity resumes by deleting that same quarantine only.
func QuarantineAndDeleteVerifiedPackageVariant(root string, claim PackageGCClaim) error {
	relative, err := PackageGCQuarantinePath(claim)
	if err != nil {
		return err
	}
	live, err := SignerCachePath(root, claim.Digest, claim.SignerFingerprint)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	quarantine := filepath.Join(root, filepath.FromSlash(relative))
	return withCacheRootLock(root, func(root string) error {
		if err := ensureCacheBoundaryLocked(root); err != nil {
			return fmt.Errorf("seal verified cache boundary before signer-aware GC: %w", err)
		}
		gcRoot := filepath.Join(root, ".gc")
		if err := withCacheRootMutationLocked(root, func() error {
			if err := unsealCacheTree(gcRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unseal verified cache quarantine root: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(quarantine), 0o755); err != nil {
				return err
			}
			if _, err := os.Lstat(quarantine); err == nil {
				if _, liveErr := os.Lstat(live); liveErr == nil {
					return errors.New("verified cache signer variant exists both live and quarantined")
				} else if !errors.Is(liveErr, os.ErrNotExist) {
					return liveErr
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}

			container := filepath.Dir(live)
			if info, err := os.Lstat(live); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return err
			} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("verified cache signer variant is not a directory")
			}
			if err := unsealCacheContainer(container); err != nil {
				return fmt.Errorf("unseal verified cache signer container: %w", err)
			}
			if err := unsealCacheTree(live); err != nil {
				return errors.Join(fmt.Errorf("unseal verified cache signer variant: %w", err), sealCacheContainer(container))
			}
			if err := os.Rename(live, quarantine); err != nil {
				return errors.Join(fmt.Errorf("quarantine verified cache signer variant: %w", err), sealCacheTree(live), sealCacheContainer(container))
			}
			return sealOrRemoveEmptyCacheContainer(container)
		}); err != nil {
			return errors.Join(err, sealCacheTree(gcRoot))
		}

		if err := unsealCacheTree(quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(fmt.Errorf("unseal quarantined verified cache signer variant: %w", err), sealCacheTree(gcRoot))
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return errors.Join(fmt.Errorf("remove quarantined verified cache signer variant: %w", err), sealCacheTree(gcRoot))
		}
		return withCacheRootMutationLocked(root, func() error {
			if err := removeEmptyCacheDirectory(filepath.Dir(quarantine)); err != nil {
				return err
			}
			if err := removeEmptyCacheDirectory(gcRoot); err != nil {
				return err
			}
			if _, err := os.Lstat(gcRoot); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return err
			}
			return sealCacheTree(gcRoot)
		})
	})
}

// QuarantineAndDeleteLegacyVerifiedPackage removes the digest-only cache
// layout used before signer identities became part of the cache path. Callers
// must hold the durable digest GC claim and must have established that the
// digest has no remaining lifecycle references. The package is revalidated
// against the claim's exact signer before it is moved into the signer-bound
// quarantine identity.
func QuarantineAndDeleteLegacyVerifiedPackage(root string, claim PackageGCClaim, validator *plugins.Validator, expectation plugins.PackageExpectation) error {
	if validator == nil {
		return errors.New("plugin validator is required for legacy package GC")
	}
	relative, err := PackageGCQuarantinePath(claim)
	if err != nil {
		return err
	}
	live, err := CachePath(root, claim.Digest)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	quarantine := filepath.Join(root, filepath.FromSlash(relative))
	return withCacheRootLock(root, func(root string) error {
		if err := ensureCacheBoundaryLocked(root); err != nil {
			return fmt.Errorf("seal verified cache boundary before legacy GC: %w", err)
		}
		gcRoot := filepath.Join(root, ".gc")
		if err := withCacheRootMutationLocked(root, func() error {
			if err := unsealCacheTree(gcRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unseal verified cache quarantine root: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(quarantine), 0o755); err != nil {
				return err
			}
			if _, err := os.Lstat(quarantine); err == nil {
				if _, liveErr := os.Lstat(live); liveErr == nil {
					return errors.New("legacy verified cache exists both live and quarantined")
				} else if !errors.Is(liveErr, os.ErrNotExist) {
					return liveErr
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}

			info, err := os.Lstat(live)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("legacy verified cache is not a directory")
			}
			if _, err := validator.ValidatePackageIntegrity(live, expectation); err != nil {
				return fmt.Errorf("revalidate legacy verified cache trust: %w", err)
			}
			if err := unsealCacheTree(live); err != nil {
				return fmt.Errorf("unseal legacy verified cache: %w", err)
			}
			if err := os.Rename(live, quarantine); err != nil {
				return errors.Join(fmt.Errorf("quarantine legacy verified cache: %w", err), sealCacheTree(live))
			}
			return nil
		}); err != nil {
			return errors.Join(err, sealCacheTree(gcRoot))
		}

		if err := unsealCacheTree(quarantine); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(fmt.Errorf("unseal quarantined legacy verified cache: %w", err), sealCacheTree(gcRoot))
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return errors.Join(fmt.Errorf("remove quarantined legacy verified cache: %w", err), sealCacheTree(gcRoot))
		}
		if err := withCacheRootMutationLocked(root, func() error {
			if err := removeEmptyCacheDirectory(filepath.Dir(quarantine)); err != nil {
				return err
			}
			if err := removeEmptyCacheDirectory(gcRoot); err != nil {
				return err
			}
			if _, err := os.Lstat(gcRoot); errors.Is(err, os.ErrNotExist) {
				return nil
			} else if err != nil {
				return err
			}
			return sealCacheTree(gcRoot)
		}); err != nil {
			return err
		}
		for _, removedPath := range []string{live, quarantine} {
			if _, err := os.Lstat(removedPath); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					return fmt.Errorf("legacy verified cache GC target still exists: %s", removedPath)
				}
				return err
			}
		}
		return nil
	})
}

func isCacheGCClaimToken(token string) bool {
	if token == "" || len(token) > 190 || token == "." || token == ".." {
		return false
	}
	for _, r := range token {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func sealOrRemoveEmptyCacheContainer(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Join(err, sealCacheContainer(path))
	}
	if len(entries) == 0 {
		return os.Remove(path)
	}
	return sealCacheContainer(path)
}

func removeEmptyCacheDirectory(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || len(entries) != 0 {
		return err
	}
	return os.Remove(path)
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
