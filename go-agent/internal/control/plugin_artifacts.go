package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const maxPluginArtifactIdentityBytes = 256

var pluginArtifactMaterializeLocks [64]sync.Mutex

func (c *SyncClient) preparePluginArtifacts(ctx context.Context, snapshot *model.Snapshot) error {
	if snapshot == nil || len(snapshot.PluginPolicies) == 0 {
		return nil
	}
	cacheDir := strings.TrimSpace(c.cfg.PluginCacheDir)
	if cacheDir == "" {
		return errors.New("plugin policy snapshot requires an Agent artifact cache")
	}
	for policyIndex := range snapshot.PluginPolicies {
		for stageIndex := range snapshot.PluginPolicies[policyIndex].Stages {
			stage := &snapshot.PluginPolicies[policyIndex].Stages[stageIndex]
			localPath, err := c.materializePluginArtifact(ctx, cacheDir, *stage)
			if err != nil {
				return fmt.Errorf("prepare plugin policy %q stage %q: %w", snapshot.PluginPolicies[policyIndex].ID, stage.InstanceID, err)
			}
			stage.ArtifactPath = localPath
		}
	}
	return nil
}

func (c *SyncClient) materializePluginArtifact(ctx context.Context, cacheDir string, stage model.PolicyStage) (string, error) {
	source := stage.ArtifactSource
	artifactID := strings.TrimSpace(source.ArtifactID)
	digest := strings.ToLower(strings.TrimSpace(source.SHA256))
	if artifactID == "" || len(artifactID) > maxPluginArtifactIdentityBytes || strings.ContainsAny(artifactID, "/\\\r\n\x00") {
		return "", errors.New("artifact identity is invalid")
	}
	if !validSHA256Hex(digest) || !strings.EqualFold(strings.TrimSpace(stage.ArtifactDigest), digest) {
		return "", errors.New("artifact digest identity is invalid")
	}
	if source.SizeBytes <= 0 {
		return "", errors.New("artifact size identity is invalid")
	}
	if strings.TrimSpace(source.PackageIdentity) == "" ||
		!strings.EqualFold(strings.TrimSpace(source.PackageDigest), strings.TrimSpace(stage.PackageDigest)) ||
		strings.TrimSpace(source.RelativePath) == "" {
		return "", errors.New("artifact package identity is incomplete")
	}

	targetDir := filepath.Join(cacheDir, "sha256", digest[:2])
	lockIndex := int(digest[0]) % len(pluginArtifactMaterializeLocks)
	pluginArtifactMaterializeLocks[lockIndex].Lock()
	defer pluginArtifactMaterializeLocks[lockIndex].Unlock()
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", fmt.Errorf("create artifact cache: %w", err)
	}
	target := filepath.Join(targetDir, digest+".wasm")
	if matches, err := pluginArtifactFileMatches(target, digest, source.SizeBytes); err != nil {
		return "", err
	} else if matches {
		return target, nil
	}

	endpoint := c.cfg.MasterURL + "/api/agent-plugin-artifacts/" + url.PathEscape(artifactID)
	temporary, err := os.CreateTemp(targetDir, ".artifact-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create artifact temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := c.downloadPluginArtifact(ctx, endpoint, temporary, digest, source.SizeBytes); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync artifact temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close artifact temporary file: %w", err)
	}
	if err := publishPluginArtifact(temporaryPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func (c *SyncClient) downloadPluginArtifact(ctx context.Context, endpoint string, target io.Writer, digest string, size int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build artifact request: %w", err)
	}
	req.Header.Set("X-Agent-Token", c.cfg.AgentToken)
	req.Header.Set("Accept", "application/wasm")
	client := *c.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download artifact failed: %s", resp.Status)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != size {
		return errors.New("downloaded artifact size differs from snapshot identity")
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(resp.Body, size+1))
	if err != nil {
		return fmt.Errorf("download artifact body: %w", err)
	}
	if written != size {
		return errors.New("downloaded artifact size differs from snapshot identity")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest) {
		return errors.New("downloaded artifact digest differs from snapshot identity")
	}
	return nil
}

func pluginArtifactFileMatches(path, digest string, size int64) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open cached artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != size {
		return false, nil
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), digest), nil
}

func publishPluginArtifact(temporaryPath, target string) error {
	if err := os.Rename(temporaryPath, target); err == nil {
		return nil
	}
	// Windows cannot atomically replace an existing destination. Move the bad
	// destination aside first and restore it if publishing the verified file
	// fails; artifacts for previous digests are never touched.
	stale := target + ".stale"
	_ = os.Remove(stale)
	if err := os.Rename(target, stale); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("quarantine stale artifact: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		_ = os.Rename(stale, target)
		return fmt.Errorf("publish verified artifact: %w", err)
	}
	_ = os.Remove(stale)
	return nil
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
