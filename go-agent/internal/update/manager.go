package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type ExecFunc func(binary string, argv []string, env []string) error

type Manager struct {
	root           string
	executablePath string
	argv           []string
	env            []string
	execFn         ExecFunc
	httpClient     *http.Client
}

func NewManager(root, executablePath string, argv, env []string, execFn ExecFunc, httpClient *http.Client) *Manager {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Manager{
		root:           root,
		executablePath: executablePath,
		argv:           append([]string(nil), argv...),
		env:            append([]string(nil), env...),
		execFn:         execFn,
		httpClient:     httpClient,
	}
}

func (m *Manager) Stage(ctx context.Context, pkg model.VersionPackage) (string, error) {
	if strings.TrimSpace(pkg.URL) == "" {
		return "", fmt.Errorf("version package url is required")
	}
	if strings.TrimSpace(pkg.SHA256) == "" {
		return "", fmt.Errorf("version package sha256 is required")
	}

	updateDir := filepath.Join(m.root, "updates")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		return "", err
	}

	reader, err := m.openPackage(ctx, pkg.URL)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	tmpFile, err := os.CreateTemp(updateDir, "stage-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), reader); err != nil {
		cleanup()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", err
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(pkg.SHA256)) {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sha256 mismatch: expected %s got %s", strings.TrimSpace(pkg.SHA256), actual)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	filename := stagedFilename(pkg)
	stagedPath := filepath.Join(updateDir, filename)
	if err := os.Rename(tmpPath, stagedPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return stagedPath, nil
}

func (m *Manager) Activate(stagedPath string, desiredVersion string) error {
	if m.execFn == nil {
		return fmt.Errorf("exec function is required")
	}
	if strings.TrimSpace(stagedPath) == "" {
		return fmt.Errorf("staged path is required")
	}
	if strings.TrimSpace(m.executablePath) == "" {
		return fmt.Errorf("target executable path is required")
	}

	if err := promoteStagedBinary(stagedPath, m.executablePath); err != nil {
		return err
	}
	env := append([]string(nil), m.env...)
	if strings.TrimSpace(desiredVersion) != "" {
		env = withEnv(env, "NRE_AGENT_VERSION", desiredVersion)
	}
	return m.execFn(m.executablePath, m.resolveArgv(), env)
}

func (m *Manager) resolveArgv() []string {
	if len(m.argv) == 0 {
		return []string{m.executablePath}
	}
	argv := append([]string(nil), m.argv...)
	argv[0] = m.executablePath
	return argv
}

func (m *Manager) openPackage(ctx context.Context, rawURL string) (io.ReadCloser, error) {
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

func stagedFilename(pkg model.VersionPackage) string {
	name := strings.TrimSpace(pkg.Filename)
	if name == "" {
		if parsed, err := url.Parse(pkg.URL); err == nil {
			name = filepath.Base(parsed.Path)
		}
	}
	name = sanitizeFilename(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "nre-agent"
	}
	return name
}

func sanitizeFilename(value string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "..", "-")
	return replacer.Replace(strings.TrimSpace(value))
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

func promoteStagedBinary(stagedPath, targetPath string) error {
	err := os.Rename(stagedPath, targetPath)
	if err == nil {
		return nil
	}
	if removeErr := os.Remove(targetPath); removeErr != nil && !os.IsNotExist(removeErr) {
		return err
	}
	return os.Rename(stagedPath, targetPath)
}
