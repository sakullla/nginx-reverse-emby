package process

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Artifact describes the exact executable selected from a verified plugin
// package. CachePath must remain non-executable; Install copies it into an
// instance-owned runtime directory before enabling execution.
type Artifact struct {
	CachePath string
	SHA256    string
	GOOS      string
	GOARCH    string
}

type Installer struct {
	RuntimeRoot string
}

func (i Installer) Install(instanceID string, artifact Artifact) (string, error) {
	if strings.TrimSpace(instanceID) == "" || strings.ContainsAny(instanceID, `/\\`) || instanceID == "." || instanceID == ".." {
		return "", errors.New("plugin process instance id is invalid")
	}
	if artifact.GOOS != runtime.GOOS || artifact.GOARCH != runtime.GOARCH {
		return "", fmt.Errorf("plugin process artifact platform %s/%s does not match host %s/%s", artifact.GOOS, artifact.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	want, err := parseDigest(artifact.SHA256)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(i.RuntimeRoot)
	if err != nil || strings.TrimSpace(i.RuntimeRoot) == "" {
		return "", errors.New("plugin process runtime root is required")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("plugin process runtime root must resolve to an absolute path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create plugin process runtime root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve plugin process runtime root: %w", err)
	}
	source, err := filepath.Abs(artifact.CachePath)
	if err != nil {
		return "", fmt.Errorf("resolve plugin process cache artifact: %w", err)
	}
	if rel, relErr := filepath.Rel(root, source); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("plugin process cache artifact must be outside the managed runtime root")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("stat plugin process cache artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("plugin process cache artifact must be a regular non-link file")
	}
	if info.Mode().Perm()&0o111 != 0 {
		return "", errors.New("plugin process cache artifact must not be executable")
	}
	instanceRoot := filepath.Join(root, instanceID)
	if rel, relErr := filepath.Rel(root, instanceRoot); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("plugin process runtime path escapes the managed root")
	}
	if err := os.MkdirAll(instanceRoot, 0o700); err != nil {
		return "", fmt.Errorf("create plugin process runtime directory: %w", err)
	}
	temporary, err := os.CreateTemp(instanceRoot, ".artifact-*")
	if err != nil {
		return "", fmt.Errorf("create plugin process staged artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	sourceFile, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open plugin process cache artifact: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temporary, hash), sourceFile)
	closeSourceErr := sourceFile.Close()
	if copyErr != nil || closeSourceErr != nil {
		return "", errors.Join(copyErr, closeSourceErr)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), want) {
		return "", errors.New("plugin process artifact digest changed while copying from cache")
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync plugin process staged artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close plugin process staged artifact: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o500); err != nil {
		return "", fmt.Errorf("enable plugin process staged artifact: %w", err)
	}
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	target := filepath.Join(instanceRoot, name)
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", fmt.Errorf("activate plugin process runtime artifact: %w", err)
	}
	committed = true
	actual, err := fileDigest(target)
	if err != nil || !strings.EqualFold(actual, want) {
		_ = os.Remove(target)
		if err != nil {
			return "", err
		}
		return "", errors.New("plugin process runtime artifact digest mismatch")
	}
	return target, nil
}

func parseDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "sha256:")))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("plugin process artifact sha256 is invalid")
	}
	return value, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open plugin process runtime artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("digest plugin process runtime artifact: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
