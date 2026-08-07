package marketplace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
)

type VerifiedCache struct {
	root       string
	validator  *plugins.Validator
	references PackageReferenceChecker
}

func NewVerifiedCache(root string, validator *plugins.Validator, references PackageReferenceChecker) (*VerifiedCache, error) {
	if validator == nil {
		return nil, errors.New("plugin validator is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &VerifiedCache{root: root, validator: validator, references: references}, nil
}

func (c *VerifiedCache) Path(digest string) (string, error) {
	return CachePath(c.root, digest)
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
	target, err := c.Path(validated.Digest)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
		cachedDigest, digestErr := plugins.ComputePackageDigest(target)
		if digestErr != nil || !strings.EqualFold(cachedDigest, validated.Digest) {
			return "", errors.New("verified cache entry is corrupt")
		}
		return target, nil
	}
	temporary, err := os.MkdirTemp(c.root, ".package-")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := copyRegularTree(validated.Root, temporary); err != nil {
		return "", err
	}
	revalidated, err := c.validator.ValidatePackage(temporary, plugins.PackageExpectation{ID: validated.Manifest.ID, Version: validated.Manifest.Version, SHA256: validated.Digest})
	if err != nil {
		return "", err
	}
	if revalidated.Digest != validated.Digest {
		return "", errors.New("package changed while entering verified cache")
	}
	if err := os.Rename(temporary, target); err != nil {
		if info, statErr := os.Stat(target); statErr == nil && info.IsDir() {
			cachedDigest, digestErr := plugins.ComputePackageDigest(target)
			if digestErr == nil && strings.EqualFold(cachedDigest, validated.Digest) {
				return target, nil
			}
			return "", errors.New("concurrent verified cache entry is corrupt")
		}
		return "", err
	}
	keep = true
	return target, nil
}

func (c *VerifiedCache) RemoveUnreferenced(ctx context.Context, digest string) (bool, error) {
	if c.references == nil {
		return false, errors.New("package reference checker is required for cache GC")
	}
	referenced, err := c.references.PackageReferenced(ctx, strings.ToLower(digest))
	if err != nil || referenced {
		return false, err
	}
	path, err := c.Path(digest)
	if err != nil {
		return false, err
	}
	if err := os.RemoveAll(path); err != nil {
		return false, err
	}
	return true, nil
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
