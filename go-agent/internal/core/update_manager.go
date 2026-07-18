package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	updateDirectory       = "updates"
	updatePackageDir      = "packages"
	updateStateDir        = "state"
	packageBinaryFile     = "nre-agent"
	packageManifestFile   = "manifest.json"
	currentPointerFile    = "current.json"
	previousPointerFile   = "previous.json"
	packagePointerVersion = 1
	// installExecutableEnv is an internal handoff value. A child executes from
	// the immutable package store but must keep promoting the service's fixed
	// entrypoint for subsequent systemd/launchd starts.
	installExecutableEnv = "NRE_AGENT_INSTALL_EXECUTABLE"
	// Legacy version policies did not carry a package size. Keep those rollouts
	// compatible while bounding a length-less download before its SHA-256 is
	// authenticated and its exact size is committed to the local manifest.
	maxDerivedPackageSize = int64(512 << 20)
)

type ExecFunc func(binary string, argv []string, env []string) error

type PackagePointer struct {
	SchemaVersion  int                   `json:"schema_version"`
	Path           string                `json:"path"`
	DesiredVersion string                `json:"desired_version,omitempty"`
	Manifest       model.PackageManifest `json:"manifest"`
}

type UpdateManager struct {
	root           string
	executablePath string
	argv           []string
	env            []string
	execFn         ExecFunc
	httpClient     *http.Client
	platform       string
	syncDirectory  func(string) error
	savePointer    func(string, PackagePointer) error
	mu             sync.Mutex
}

func NewUpdateManager(root, executablePath string, argv, env []string, execFn ExecFunc, httpClient *http.Client) *UpdateManager {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	executablePath = resolveInstallExecutable(root, executablePath, env)
	managerEnv := append([]string(nil), env...)
	if strings.TrimSpace(executablePath) != "" {
		managerEnv = withEnv(managerEnv, installExecutableEnv, executablePath)
	}
	return &UpdateManager{
		root:           root,
		executablePath: executablePath,
		argv:           append([]string(nil), argv...),
		env:            managerEnv,
		execFn:         execFn,
		httpClient:     httpClient,
		platform:       runtime.GOOS + "-" + runtime.GOARCH,
		syncDirectory:  syncFilesystemDirectory,
	}
}

func SupportsPackageManifest(goos, goarch string) bool {
	return strings.EqualFold(strings.TrimSpace(goos), "linux") &&
		(strings.EqualFold(strings.TrimSpace(goarch), "amd64") || strings.EqualFold(strings.TrimSpace(goarch), "arm64"))
}

func (m *UpdateManager) Preflight(pkg model.VersionPackage) error {
	_, err := versionPackageManifest(pkg, m.platform)
	return err
}

func (m *UpdateManager) Stage(ctx context.Context, pkg model.VersionPackage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifest, err := versionPackageManifest(pkg, m.platform)
	if err != nil {
		return "", err
	}
	packageRoot := m.packageRoot()
	targetDir := filepath.Join(packageRoot, manifest.SHA256)
	targetPath := filepath.Join(targetDir, packageBinaryFile)
	if err := m.validateStorePath(targetDir); err != nil {
		return "", err
	}
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("immutable package digest path must be a directory")
		}
		if manifest.Size == 0 {
			if pointer, readErr := m.readPackage(targetPath); readErr == nil {
				if !packageManifestMatchesRequest(pointer.Manifest, manifest) {
					return "", errors.New("existing immutable package manifest conflicts with requested package")
				}
				return m.pointerPath(pointer), nil
			}
			// A crash may leave the verified binary in place before manifest.json.
			// Its on-disk length is sufficient to finish the normal hash recovery.
			if binaryInfo, statErr := os.Lstat(targetPath); statErr == nil && binaryInfo.Mode().IsRegular() {
				manifest.Size = binaryInfo.Size()
			}
		}
		pointer, recovered, validateErr := m.recoverPackage(targetPath, manifest, true)
		if validateErr != nil {
			return "", fmt.Errorf("existing immutable package is invalid: %w", validateErr)
		}
		if recovered {
			return m.pointerPath(pointer), nil
		}
		if removeErr := os.Remove(targetDir); removeErr != nil {
			return "", fmt.Errorf("remove incomplete package directory: %w", removeErr)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	reader, err := m.openPackage(ctx, pkg.URL)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	if err := m.ensureStoreDirectory(packageRoot, 0o755); err != nil {
		return "", err
	}
	if err := m.ensureStoreDirectory(targetDir, 0o755); err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp(packageRoot, ".stage-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	copyLimit := manifest.Size
	if copyLimit < math.MaxInt64 {
		copyLimit++
	}
	if manifest.Size == 0 {
		copyLimit = maxDerivedPackageSize + 1
	}
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), io.LimitReader(reader, copyLimit))
	if err != nil {
		cleanup()
		return "", err
	}
	if manifest.Size == 0 {
		if written <= 0 {
			cleanup()
			return "", errors.New("version package is empty")
		}
		if written > maxDerivedPackageSize {
			cleanup()
			return "", fmt.Errorf("version package without declared size exceeds %d bytes", maxDerivedPackageSize)
		}
		manifest.Size = written
	} else if written != manifest.Size {
		cleanup()
		return "", fmt.Errorf("package size mismatch: expected %d got %d", manifest.Size, written)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != manifest.SHA256 {
		cleanup()
		return "", fmt.Errorf("sha256 mismatch: expected %s got %s", manifest.SHA256, actual)
	}
	if err := tmpFile.Chmod(0o555); err != nil {
		cleanup()
		return "", err
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := m.syncDirectory(targetDir); err != nil {
		return "", &filesystemCommitUncertainError{err: err}
	}
	if err := m.writeAtomicJSON(filepath.Join(targetDir, packageManifestFile), manifest, 0o444); err != nil {
		return "", err
	}
	return targetPath, nil
}

func (m *UpdateManager) Activate(stagedPath string, desiredVersion string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.execFn == nil {
		return errors.New("exec function is required")
	}
	next, err := m.readPackage(stagedPath)
	if err != nil {
		return fmt.Errorf("validate staged package: %w", err)
	}
	next.DesiredVersion = strings.TrimSpace(desiredVersion)
	current, err := m.ensureCurrentPackage()
	if err != nil {
		return err
	}
	packageChanged := current.Manifest.SHA256 != next.Manifest.SHA256
	if packageChanged {
		if err := m.writePointerConvergent(previousPointerFile, current); err != nil {
			return fmt.Errorf("save previous package pointer: %w", err)
		}
		if err := m.writePointerConvergent(currentPointerFile, next); err != nil {
			return fmt.Errorf("save current package pointer: %w", err)
		}
	} else if current.DesiredVersion != next.DesiredVersion {
		if err := m.writePointerConvergent(currentPointerFile, next); err != nil {
			return fmt.Errorf("refresh current package pointer: %w", err)
		}
	}
	if err := m.promoteInstalledPackage(next); err != nil {
		var restoreErr error
		if packageChanged {
			restoreErr = m.restorePreviousLocked()
		} else {
			restoreErr = m.writePointerConvergent(currentPointerFile, current)
		}
		return errors.Join(fmt.Errorf("promote installed executable: %w", err), restoreErr)
	}

	binaryPath := m.pointerPath(next)
	env := append([]string(nil), m.env...)
	if next.DesiredVersion != "" {
		env = withEnv(env, "NRE_AGENT_VERSION", next.DesiredVersion)
	}
	err = m.execFn(binaryPath, m.resolveArgv(binaryPath), env)
	if err == nil || errors.Is(err, ErrRestartRequested) {
		return err
	}
	var restoreErr error
	if packageChanged {
		restoreErr = m.restorePreviousLocked()
	} else {
		restoreErr = errors.Join(
			m.promoteInstalledPackage(current),
			m.writePointerConvergent(currentPointerFile, current),
		)
	}
	return errors.Join(err, restoreErr)
}

func (m *UpdateManager) CurrentPackage() (PackagePointer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadPointer(currentPointerFile)
}

func (m *UpdateManager) PreviousPackage() (PackagePointer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadPointer(previousPointerFile)
}

func (m *UpdateManager) RestorePrevious() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restorePreviousLocked()
}

func (m *UpdateManager) restorePreviousLocked() error {
	current, err := m.loadPointer(currentPointerFile)
	if err != nil {
		return fmt.Errorf("load current package pointer: %w", err)
	}
	previous, err := m.loadPointer(previousPointerFile)
	if err != nil {
		return fmt.Errorf("load previous package pointer: %w", err)
	}
	if err := m.promoteInstalledPackage(previous); err != nil {
		return fmt.Errorf("restore installed executable: %w", err)
	}
	if err := m.writePointerConvergent(currentPointerFile, previous); err != nil {
		return fmt.Errorf("restore current package pointer: %w", err)
	}
	if err := m.writePointerConvergent(previousPointerFile, current); err != nil {
		return fmt.Errorf("retain failed package pointer: %w", err)
	}
	return nil
}

func (m *UpdateManager) promoteInstalledPackage(pointer PackagePointer) error {
	targetPath := strings.TrimSpace(m.executablePath)
	if targetPath == "" {
		return errors.New("installed executable path is required")
	}
	sourcePath := m.pointerPath(pointer)
	stored, err := m.readPackage(sourcePath)
	if err != nil {
		return err
	}
	if stored.Manifest != pointer.Manifest {
		return errors.New("installed package pointer does not match immutable package")
	}
	if sameFilesystemPath(sourcePath, targetPath) {
		return nil
	}
	if pathWithin(m.packageRoot(), targetPath) {
		return errors.New("installed executable target must be outside the immutable package store")
	}
	if matches, matchErr := installedPackageMatches(targetPath, pointer.Manifest); matchErr != nil {
		return matchErr
	} else if matches {
		return nil
	}

	mode := os.FileMode(0o755)
	if info, statErr := os.Lstat(targetPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("installed executable target must be a regular file")
		}
		if info.Mode().Perm() != 0 {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	parent := filepath.Dir(targetPath)
	if info, statErr := os.Stat(parent); statErr != nil || !info.IsDir() {
		if statErr != nil {
			return statErr
		}
		return errors.New("installed executable directory is not a directory")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	tmp, err := os.CreateTemp(parent, ".nre-agent-install-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := io.Copy(tmp, source); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := m.syncDirectory(parent); err != nil {
		if matches, _ := installedPackageMatches(targetPath, pointer.Manifest); matches {
			return nil
		}
		return &filesystemCommitUncertainError{err: err}
	}
	return nil
}

func installedPackageMatches(path string, manifest model.PackageManifest) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("installed executable target must be a regular file")
	}
	if info.Size() != manifest.Size {
		return false, nil
	}
	digest, err := fileDigest(path)
	return digest == manifest.SHA256, err
}

func resolveInstallExecutable(root, executablePath string, env []string) string {
	executablePath = strings.TrimSpace(executablePath)
	inherited := strings.TrimSpace(environmentValue(env, installExecutableEnv))
	packageRoot := filepath.Join(root, updateDirectory, updatePackageDir)
	if inherited != "" && pathWithin(packageRoot, executablePath) && !pathWithin(packageRoot, inherited) {
		return filepath.Clean(inherited)
	}
	return executablePath
}

func environmentValue(env []string, key string) string {
	prefix := key + "="
	for index := len(env) - 1; index >= 0; index-- {
		if strings.HasPrefix(env[index], prefix) {
			return strings.TrimPrefix(env[index], prefix)
		}
	}
	return ""
}

func pathWithin(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	absRoot, rootErr := filepath.Abs(root)
	absPath, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return false
	}
	relative, err := filepath.Rel(absRoot, absPath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sameFilesystemPath(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftPath = filepath.Clean(leftPath)
	rightPath = filepath.Clean(rightPath)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftPath, rightPath)
	}
	return leftPath == rightPath
}

func (m *UpdateManager) ensureCurrentPackage() (PackagePointer, error) {
	current, err := m.loadPointer(currentPointerFile)
	if err == nil {
		return current, nil
	}
	if !os.IsNotExist(err) {
		return PackagePointer{}, fmt.Errorf("load current package pointer: %w", err)
	}
	previous, previousErr := m.loadPointer(previousPointerFile)
	if previousErr == nil {
		if err := m.writePointerConvergent(currentPointerFile, previous); err != nil {
			return PackagePointer{}, fmt.Errorf("recover current package pointer: %w", err)
		}
		return previous, nil
	}
	if !os.IsNotExist(previousErr) {
		return PackagePointer{}, fmt.Errorf("load previous package pointer: %w", previousErr)
	}
	return m.importExecutable(m.executablePath)
}

func (m *UpdateManager) importExecutable(sourcePath string) (PackagePointer, error) {
	if strings.TrimSpace(sourcePath) == "" {
		return PackagePointer{}, errors.New("target executable path is required")
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return PackagePointer{}, fmt.Errorf("open current executable: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return PackagePointer{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return PackagePointer{}, errors.New("current executable is not a non-empty regular file")
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return PackagePointer{}, err
	}
	manifest := model.PackageManifest{
		SchemaVersion: model.PackageManifestVersion,
		Filename:      filepath.Base(sourcePath),
		Platform:      m.platform,
		SHA256:        hex.EncodeToString(hasher.Sum(nil)),
		Size:          info.Size(),
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return PackagePointer{}, err
	}
	targetDir := filepath.Join(m.packageRoot(), manifest.SHA256)
	targetPath := filepath.Join(targetDir, packageBinaryFile)
	if err := m.validateStorePath(targetDir); err != nil {
		return PackagePointer{}, err
	}
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return PackagePointer{}, errors.New("current package digest path must be a directory")
		}
		pointer, recovered, validateErr := m.recoverPackage(targetPath, manifest, false)
		if validateErr != nil {
			return PackagePointer{}, fmt.Errorf("existing current package is invalid: %w", validateErr)
		}
		if recovered {
			return pointer, nil
		}
		if removeErr := os.Remove(targetDir); removeErr != nil {
			return PackagePointer{}, removeErr
		}
	} else if !os.IsNotExist(err) {
		return PackagePointer{}, err
	}
	if err := m.ensureStoreDirectory(targetDir, 0o755); err != nil {
		return PackagePointer{}, err
	}
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		tmpFile, err := os.CreateTemp(m.packageRoot(), ".import-*")
		if err != nil {
			return PackagePointer{}, err
		}
		tmpPath := tmpFile.Name()
		if _, err := io.Copy(tmpFile, file); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return PackagePointer{}, err
		}
		if err := tmpFile.Chmod(0o555); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return PackagePointer{}, err
		}
		if err := tmpFile.Sync(); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return PackagePointer{}, err
		}
		if err := tmpFile.Close(); err != nil {
			os.Remove(tmpPath)
			return PackagePointer{}, err
		}
		if err := os.Rename(tmpPath, targetPath); err != nil {
			os.Remove(tmpPath)
			return PackagePointer{}, err
		}
	}
	if err := m.writeAtomicJSON(filepath.Join(targetDir, packageManifestFile), manifest, 0o444); err != nil {
		return PackagePointer{}, err
	}
	return m.readPackage(targetPath)
}

func (m *UpdateManager) recoverPackage(binaryPath string, expected model.PackageManifest, requireExactManifest bool) (PackagePointer, bool, error) {
	manifestPath := filepath.Join(filepath.Dir(binaryPath), packageManifestFile)
	if _, err := os.Lstat(manifestPath); err == nil {
		pointer, validateErr := m.readPackage(binaryPath)
		if validateErr != nil {
			return PackagePointer{}, false, validateErr
		}
		if requireExactManifest && pointer.Manifest != expected {
			return PackagePointer{}, false, errors.New("content-addressed package manifest conflict")
		}
		if !samePackageContent(pointer.Manifest, expected) {
			return PackagePointer{}, false, errors.New("content-addressed package identity conflict")
		}
		return pointer, true, nil
	} else if !os.IsNotExist(err) {
		return PackagePointer{}, false, err
	}
	entries, err := os.ReadDir(filepath.Dir(binaryPath))
	if err != nil {
		return PackagePointer{}, false, err
	}
	if len(entries) == 0 {
		return PackagePointer{}, false, nil
	}
	if len(entries) != 1 || entries[0].Name() != packageBinaryFile || entries[0].Type()&os.ModeSymlink != 0 {
		return PackagePointer{}, false, errors.New("incomplete package directory contains unexpected files")
	}
	info, err := os.Lstat(binaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
		return PackagePointer{}, false, errors.New("incomplete package binary does not match manifest size")
	}
	digest, err := fileDigest(binaryPath)
	if err != nil || digest != expected.SHA256 {
		return PackagePointer{}, false, errors.New("incomplete package binary does not match manifest digest")
	}
	if err := m.writeAtomicJSON(manifestPath, expected, 0o444); err != nil {
		return PackagePointer{}, false, err
	}
	pointer, err := m.readPackage(binaryPath)
	return pointer, err == nil, err
}

func samePackageContent(left, right model.PackageManifest) bool {
	return left.SchemaVersion == right.SchemaVersion && left.Platform == right.Platform &&
		left.SHA256 == right.SHA256 && left.Size == right.Size
}

func packageManifestMatchesRequest(stored, requested model.PackageManifest) bool {
	return stored.SchemaVersion == requested.SchemaVersion && stored.Platform == requested.Platform &&
		stored.SHA256 == requested.SHA256 &&
		(requested.Size == 0 || stored.Size == requested.Size)
}

func versionPackageManifest(pkg model.VersionPackage, platform string) (model.PackageManifest, error) {
	parsed, err := url.Parse(strings.TrimSpace(pkg.URL))
	if err != nil || parsed == nil || (parsed.Scheme != "file" && parsed.Scheme != "http" && parsed.Scheme != "https") {
		return model.PackageManifest{}, errors.New("version package url must use file, http, or https")
	}
	digest := strings.ToLower(strings.TrimSpace(pkg.SHA256))
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return model.PackageManifest{}, errors.New("version package sha256 must be a 64-character hex digest")
	}
	if pkg.Size < 0 {
		return model.PackageManifest{}, errors.New("version package size must not be negative")
	}
	filename := strings.TrimSpace(pkg.Filename)
	if filename == "" {
		filename = pathpkg.Base(parsed.EscapedPath())
		if decodedFilename, decodeErr := url.PathUnescape(filename); decodeErr == nil {
			filename = decodedFilename
		}
		if filename == "" || filename == "." || filename == "/" {
			filename = packageBinaryFile
		}
	}
	if filename == "" || filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\\`) || filename == "." || filename == ".." {
		return model.PackageManifest{}, errors.New("version package filename must be a safe base name")
	}
	packagePlatform := strings.ToLower(strings.TrimSpace(pkg.Platform))
	if packagePlatform == "" || packagePlatform != strings.ToLower(strings.TrimSpace(platform)) {
		return model.PackageManifest{}, fmt.Errorf("version package platform %q does not match runtime %q", packagePlatform, platform)
	}
	parts := strings.Split(packagePlatform, "-")
	if len(parts) != 2 || !SupportsPackageManifest(parts[0], parts[1]) {
		return model.PackageManifest{}, fmt.Errorf("hot upgrade is unsupported on platform %q", packagePlatform)
	}
	return model.PackageManifest{
		SchemaVersion: model.PackageManifestVersion,
		Filename:      filename,
		Platform:      packagePlatform,
		SHA256:        digest,
		Size:          pkg.Size,
	}, nil
}

func (m *UpdateManager) readPackage(binaryPath string) (PackagePointer, error) {
	absRoot, err := filepath.Abs(m.packageRoot())
	if err != nil {
		return PackagePointer{}, err
	}
	absPath, err := filepath.Abs(binaryPath)
	if err != nil {
		return PackagePointer{}, err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return PackagePointer{}, errors.New("package path is outside the immutable package store")
	}
	if err := m.validateStorePath(absPath); err != nil {
		return PackagePointer{}, err
	}
	if filepath.Base(absPath) != packageBinaryFile {
		return PackagePointer{}, errors.New("package path does not name the immutable binary")
	}
	packageDirInfo, err := os.Lstat(filepath.Dir(absPath))
	if err != nil {
		return PackagePointer{}, err
	}
	if !packageDirInfo.IsDir() || packageDirInfo.Mode()&os.ModeSymlink != 0 {
		return PackagePointer{}, errors.New("package digest path must be a directory")
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return PackagePointer{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PackagePointer{}, errors.New("package binary must be a regular file")
	}
	manifestPath := filepath.Join(filepath.Dir(absPath), packageManifestFile)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return PackagePointer{}, err
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return PackagePointer{}, errors.New("package manifest must be a regular file")
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return PackagePointer{}, err
	}
	var manifest model.PackageManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return PackagePointer{}, err
	}
	if manifest.SchemaVersion != model.PackageManifestVersion || manifest.Size <= 0 ||
		manifest.Platform != strings.ToLower(m.platform) || filepath.Base(filepath.Dir(absPath)) != manifest.SHA256 {
		return PackagePointer{}, errors.New("package manifest identity is invalid")
	}
	if _, err := versionPackageManifest(model.VersionPackage{
		URL: "file:///immutable-package", SHA256: manifest.SHA256, Platform: manifest.Platform,
		Filename: manifest.Filename, Size: manifest.Size,
	}, m.platform); err != nil {
		return PackagePointer{}, err
	}
	if info.Size() != manifest.Size {
		return PackagePointer{}, errors.New("stored package size does not match manifest")
	}
	digest, err := fileDigest(absPath)
	if err != nil {
		return PackagePointer{}, err
	}
	if digest != manifest.SHA256 {
		return PackagePointer{}, errors.New("stored package digest does not match manifest")
	}
	rootRelative, err := filepath.Rel(m.root, absPath)
	if err != nil {
		return PackagePointer{}, err
	}
	return PackagePointer{
		SchemaVersion: packagePointerVersion,
		Path:          filepath.ToSlash(rootRelative),
		Manifest:      manifest,
	}, nil
}

func (m *UpdateManager) packageRoot() string {
	return filepath.Join(m.root, updateDirectory, updatePackageDir)
}

func (m *UpdateManager) stateRoot() string {
	return filepath.Join(m.root, updateDirectory, updateStateDir)
}

func (m *UpdateManager) pointerPath(pointer PackagePointer) string {
	return filepath.Join(m.root, filepath.FromSlash(pointer.Path))
}

func (m *UpdateManager) writePointer(name string, pointer PackagePointer) error {
	if m.savePointer != nil {
		return m.savePointer(name, pointer)
	}
	if pointer.SchemaVersion != packagePointerVersion {
		return errors.New("package pointer schema version is invalid")
	}
	stored, err := m.readPackage(m.pointerPath(pointer))
	if err != nil {
		return err
	}
	if stored.Manifest != pointer.Manifest {
		return errors.New("package pointer manifest does not match immutable package")
	}
	if err := m.ensureStoreDirectory(m.stateRoot(), 0o700); err != nil {
		return err
	}
	return m.writeAtomicJSON(filepath.Join(m.stateRoot(), name), pointer, 0o600)
}

func (m *UpdateManager) writePointerConvergent(name string, pointer PackagePointer) error {
	err := m.writePointer(name, pointer)
	if err == nil {
		return nil
	}
	if !isFilesystemCommitUncertain(err) {
		return err
	}
	stored, loadErr := m.loadPointer(name)
	if loadErr == nil && stored == pointer {
		return nil
	}
	if loadErr == nil {
		loadErr = errors.New("persisted package pointer has a different identity")
	}
	return errors.Join(err, loadErr)
}

func (m *UpdateManager) loadPointer(name string) (PackagePointer, error) {
	pointerPath := filepath.Join(m.stateRoot(), name)
	if err := m.validateStorePath(pointerPath); err != nil {
		return PackagePointer{}, err
	}
	info, err := os.Lstat(pointerPath)
	if err != nil {
		return PackagePointer{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return PackagePointer{}, errors.New("package pointer must be a regular file")
	}
	payload, err := os.ReadFile(pointerPath)
	if err != nil {
		return PackagePointer{}, err
	}
	var pointer PackagePointer
	if err := json.Unmarshal(payload, &pointer); err != nil {
		return PackagePointer{}, err
	}
	if pointer.SchemaVersion != packagePointerVersion {
		return PackagePointer{}, errors.New("unsupported package pointer version")
	}
	stored, err := m.readPackage(m.pointerPath(pointer))
	if err != nil {
		return PackagePointer{}, err
	}
	if stored.Manifest != pointer.Manifest {
		return PackagePointer{}, errors.New("package pointer manifest does not match immutable package")
	}
	return pointer, nil
}

func (m *UpdateManager) writeAtomicJSON(path string, value any, mode os.FileMode) error {
	if err := m.ensureStoreDirectory(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := m.validateStorePath(path); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmpFile.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmpFile.Write(payload); err != nil {
		cleanup()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := m.syncDirectory(filepath.Dir(path)); err != nil {
		return &filesystemCommitUncertainError{err: err}
	}
	return nil
}

func (m *UpdateManager) validateStorePath(path string) error {
	root, rel, err := m.storeRelativePath(path)
	if err != nil {
		return err
	}
	current := root
	for _, component := range splitRelativePath(rel) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("update store path contains symlink: %s", current)
		}
	}
	return nil
}

func (m *UpdateManager) ensureStoreDirectory(path string, mode os.FileMode) error {
	root, rel, err := m.storeRelativePath(path)
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
	} else if !rootInfo.IsDir() {
		return errors.New("update store root is not a directory")
	}
	components := splitRelativePath(rel)
	current := root
	for index, component := range components {
		parent := current
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("update store directory is not a real directory: %s", current)
			}
			continue
		}
		if !os.IsNotExist(statErr) {
			return statErr
		}
		createMode := os.FileMode(0o755)
		if index == len(components)-1 {
			createMode = mode
		}
		if err := os.Mkdir(current, createMode); err != nil {
			return err
		}
		if err := m.syncDirectory(parent); err != nil {
			return &filesystemCommitUncertainError{err: err}
		}
	}
	return nil
}

func (m *UpdateManager) storeRelativePath(path string) (string, string, error) {
	root := m.root
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("update store path escapes the data root")
	}
	return absRoot, rel, nil
}

func splitRelativePath(path string) []string {
	if path == "." || path == "" {
		return nil
	}
	return strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
}

func (m *UpdateManager) resolveArgv(binaryPath string) []string {
	if len(m.argv) == 0 {
		return []string{binaryPath}
	}
	argv := append([]string(nil), m.argv...)
	argv[0] = binaryPath
	return argv
}

func (m *UpdateManager) openPackage(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "file":
		path := filepath.FromSlash(parsed.Path)
		if parsed.Host != "" {
			path = filepath.FromSlash(parsed.Host + parsed.Path)
		}
		if len(path) >= 3 && path[0] == filepath.Separator && path[2] == ':' {
			path = path[1:]
		}
		return os.Open(path)
	case "http", "https":
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := m.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("download failed: %s", resp.Status)
		}
		return resp.Body, nil
	default:
		return nil, fmt.Errorf("unsupported package url scheme: %s", parsed.Scheme)
	}
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	nextEnv := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			nextEnv = append(nextEnv, prefix+value)
			replaced = true
			continue
		}
		nextEnv = append(nextEnv, item)
	}
	if !replaced {
		nextEnv = append(nextEnv, prefix+value)
	}
	return nextEnv
}
