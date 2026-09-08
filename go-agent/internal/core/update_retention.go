package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const updatePackageGracePeriod = 24 * time.Hour

// CleanupPackages runs only at authoritative worker startup and before Stage.
// The updater mutex excludes this worker's staging, promotion and rollback.
// Other hot-restart processes retain their packages through /proc, while the
// current/previous pointers protect both sides of an authority handoff.
func (m *UpdateManager) CleanupPackages(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupPackagesLocked(ctx, "")
}

func (m *UpdateManager) cleanupPackagesLocked(ctx context.Context, targetSHA string) error {
	if err := m.validateStorePath(m.packageRoot()); err != nil {
		return err
	}
	entries, err := os.ReadDir(m.packageRoot())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	keep := map[string]bool{targetSHA: true, m.stagedPackageSHA: true}
	for _, name := range []string{currentPointerFile, previousPointerFile} {
		pointer, err := m.loadPointer(name)
		if os.IsNotExist(err) {
			// No baseline has been committed yet, or a referenced package has
			// disappeared. Leave recovery to the updater before reclaiming data.
			if name == currentPointerFile {
				return nil
			}
			if _, statErr := os.Lstat(filepath.Join(m.stateRoot(), name)); os.IsNotExist(statErr) {
				continue
			}
		}
		if err != nil {
			return fmt.Errorf("read retained package %s: %w", name, err)
		}
		keep[pointer.Manifest.SHA256] = true
	}
	probe := m.runningPackageDigests
	if probe == nil {
		probe = m.livePackageDigests
	}
	running, err := probe()
	if err != nil {
		return fmt.Errorf("inspect running update packages: %w", err)
	}
	for _, digest := range running {
		keep[digest] = true
	}
	cutoff := time.Now().Add(-updatePackageGracePeriod)
	removed := 0
	var cleanupErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(cleanupErr, err)
		}
		name := entry.Name()
		if keep[name] || !isPackageDigestDirectory(name) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		directory := filepath.Join(m.packageRoot(), name)
		if _, err := m.readPackage(filepath.Join(directory, packageBinaryFile)); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("validate obsolete package %s: %w", name, err))
			continue
		}
		// Never recursively delete unknown contents or follow a symlink. Only
		// the two files written by Stage belong to package retention.
		children, err := os.ReadDir(directory)
		if err != nil || len(children) != 2 {
			continue
		}
		known := true
		for _, child := range children {
			if (child.Name() != packageBinaryFile && child.Name() != packageManifestFile) || !child.Type().IsRegular() {
				known = false
			}
		}
		if !known {
			continue
		}
		for _, path := range []string{filepath.Join(directory, packageBinaryFile), filepath.Join(directory, packageManifestFile), directory} {
			if err := os.Remove(path); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove obsolete package %s: %w", name, err))
				known = false
				break
			}
		}
		if known {
			removed++
		}
	}
	if removed > 0 {
		cleanupErr = errors.Join(cleanupErr, m.syncDirectory(m.packageRoot()))
		log.Printf("[agent] removed %d obsolete update packages", removed)
	}
	return cleanupErr
}

func isPackageDigestDirectory(name string) bool {
	decoded, err := hex.DecodeString(name)
	return err == nil && len(decoded) == 32 && name == strings.ToLower(name)
}

func (m *UpdateManager) livePackageDigests() ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("running package retention requires Linux procfs")
	}
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var digests []string
	for _, process := range processes {
		if _, err := strconv.Atoi(process.Name()); err != nil {
			continue
		}
		image := filepath.Join("/proc", process.Name(), "exe")
		path, err := os.Readlink(image)
		if os.IsNotExist(err) {
			continue // Exited processes and kernel threads have no executable.
		}
		if err != nil {
			return nil, err // An incomplete process view cannot prove a package unused.
		}
		path = strings.TrimSuffix(path, " (deleted)")
		if pathWithin(m.packageRoot(), path) || sameFilesystemPath(path, m.executablePath) || sameFilesystemPath(path, m.runningExecutablePath) {
			digest, err := fileDigest(image)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			digests = append(digests, digest)
		}
	}
	return digests, nil
}
