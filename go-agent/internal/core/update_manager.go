package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	return &UpdateManager{
		root:           root,
		executablePath: executablePath,
		argv:           append([]string(nil), argv...),
		env:            append([]string(nil), env...),
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
	if info, err := os.Lstat(targetDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("immutable package digest path must be a directory")
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
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
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
	written, err := io.Copy(io.MultiWriter(tmpFile, hasher), io.LimitReader(reader, manifest.Size+1))
	if err != nil {
		cleanup()
		return "", err
	}
	if written != manifest.Size {
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
	if current.Manifest.SHA256 != next.Manifest.SHA256 {
		if err := m.writePointer(previousPointerFile, current); err != nil {
			return fmt.Errorf("save previous package pointer: %w", err)
		}
		if err := m.writePointer(currentPointerFile, next); err != nil {
			return fmt.Errorf("save current package pointer: %w", err)
		}
	} else if current.DesiredVersion != next.DesiredVersion {
		if err := m.writePointer(currentPointerFile, next); err != nil {
			return fmt.Errorf("refresh current package pointer: %w", err)
		}
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
	restoreErr := m.restorePreviousLocked()
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
	if err := m.writePointer(currentPointerFile, previous); err != nil {
		return fmt.Errorf("restore current package pointer: %w", err)
	}
	if err := m.writePointer(previousPointerFile, current); err != nil {
		return fmt.Errorf("retain failed package pointer: %w", err)
	}
	return nil
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
		if err := m.writePointer(currentPointerFile, previous); err != nil {
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
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
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
	if pkg.Size <= 0 {
		return model.PackageManifest{}, errors.New("version package size must be positive")
	}
	filename := strings.TrimSpace(pkg.Filename)
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
	if err := os.MkdirAll(m.stateRoot(), 0o700); err != nil {
		return err
	}
	return m.writeAtomicJSON(filepath.Join(m.stateRoot(), name), pointer, 0o600)
}

func (m *UpdateManager) loadPointer(name string) (PackagePointer, error) {
	pointerPath := filepath.Join(m.stateRoot(), name)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
