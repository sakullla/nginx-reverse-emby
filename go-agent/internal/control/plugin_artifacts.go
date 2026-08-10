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
	"strconv"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

const maxPluginArtifactIdentityBytes = 256

var pluginArtifactMaterializeLocks [64]sync.Mutex

func (c *SyncClient) preparePluginArtifacts(ctx context.Context, snapshot *model.Snapshot, revision int64, snapshotDigest string) error {
	if snapshot == nil {
		return nil
	}
	if err := model.ValidatePluginDependencies(*snapshot); err != nil {
		return err
	}
	if len(snapshot.PluginGenerations) == 0 && len(snapshot.PluginPolicies) == 0 {
		return nil
	}
	snapshotDigest = strings.ToLower(strings.TrimSpace(snapshotDigest))
	if revision <= 0 || snapshot.Revision != revision || !validSHA256Hex(snapshotDigest) {
		return errors.New("plugin policy snapshot has an invalid immutable revision identity")
	}
	required := make(map[string]struct{})
	for _, id := range policy.RequiredPolicyIDs(*snapshot) {
		required[id] = struct{}{}
	}
	cacheDir := strings.TrimSpace(c.cfg.PluginCacheDir)
	if len(snapshot.PluginGenerations) > 0 {
		if err := model.ValidatePluginGenerations(*snapshot, false); err != nil {
			return fmt.Errorf("validate plugin generations: %w", err)
		}
		generations := make([]model.PluginGeneration, 0, len(snapshot.PluginGenerations))
		for index := range snapshot.PluginGenerations {
			generation := clonePluginGeneration(snapshot.PluginGenerations[index])
			if cacheDir == "" {
				return errors.New("plugin generation snapshot requires an Agent artifact cache")
			}
			localPath, err := c.materializePluginGenerationArtifact(ctx, cacheDir, revision, snapshotDigest, generation)
			if err != nil {
				return fmt.Errorf("prepare required plugin generation %q: %w", generation.InstanceID, err)
			}
			generation.Artifact.LocalPath = localPath
			if err := generation.Validate(revision, true); err != nil {
				return fmt.Errorf("validate materialized plugin generation %q: %w", generation.InstanceID, err)
			}
			generations = append(generations, generation)
		}
		snapshot.PluginGenerations = generations
		if len(snapshot.PluginPolicies) == 0 {
			return nil
		}
	}
	materialized := make([]model.PluginPolicy, 0, len(snapshot.PluginPolicies))
	for policyIndex := range snapshot.PluginPolicies {
		definition := snapshot.PluginPolicies[policyIndex]
		definition.Stages = append([]model.PolicyStage(nil), definition.Stages...)
		var policyErr error
		for stageIndex := range definition.Stages {
			stage := &definition.Stages[stageIndex]
			stage.ExtensionPoints = append([]string(nil), stage.ExtensionPoints...)
			stage.DeclaredScopes = append([]string(nil), stage.DeclaredScopes...)
			stage.GrantedScopes = append([]string(nil), stage.GrantedScopes...)
			if cacheDir == "" {
				policyErr = errors.New("plugin policy snapshot requires an Agent artifact cache")
				break
			}
			localPath, err := c.materializePluginArtifact(ctx, cacheDir, revision, snapshotDigest, *stage)
			if err != nil {
				policyErr = fmt.Errorf("stage %q: %w", stage.InstanceID, err)
				break
			}
			stage.ArtifactPath = localPath
		}
		if policyErr != nil {
			if _, directlyRequired := required[strings.TrimSpace(definition.ID)]; directlyRequired {
				return fmt.Errorf("prepare required plugin policy %q: %w", definition.ID, policyErr)
			}
			continue
		}
		materialized = append(materialized, definition)
	}
	snapshot.PluginPolicies = materialized
	return nil
}

func (c *SyncClient) materializePluginGenerationArtifact(ctx context.Context, cacheDir string, revision int64, snapshotDigest string, generation model.PluginGeneration) (string, error) {
	artifact := generation.Artifact
	extension := ".wasm"
	if generation.Runtime.Kind == model.PluginRuntimeRPCService {
		extension = ".bin"
	}
	return c.materializePluginArtifactSource(ctx, cacheDir, revision, snapshotDigest, artifact.ArtifactID, generation.PackageDigest,
		artifact.PackageIdentity, artifact.RelativePath, artifact.SHA256, artifact.SizeBytes, extension)
}

func (c *SyncClient) materializePluginArtifact(ctx context.Context, cacheDir string, revision int64, snapshotDigest string, stage model.PolicyStage) (string, error) {
	source := stage.ArtifactSource
	return c.materializePluginArtifactSource(ctx, cacheDir, revision, snapshotDigest, source.ArtifactID, stage.PackageDigest,
		source.PackageIdentity, source.RelativePath, source.SHA256, source.SizeBytes, ".wasm")
}

func (c *SyncClient) materializePluginArtifactSource(ctx context.Context, cacheDir string, revision int64, snapshotDigest, artifactID, packageDigest, packageIdentity, relativePath, artifactDigest string, sizeBytes int64, extension string) (string, error) {
	artifactID = strings.TrimSpace(artifactID)
	digest := strings.ToLower(strings.TrimSpace(artifactDigest))
	if artifactID == "" || len(artifactID) > maxPluginArtifactIdentityBytes || strings.ContainsAny(artifactID, "/\\\r\n\x00") {
		return "", errors.New("artifact identity is invalid")
	}
	if !validSHA256Hex(digest) {
		return "", errors.New("artifact digest identity is invalid")
	}
	if sizeBytes <= 0 {
		return "", errors.New("artifact size identity is invalid")
	}
	if !validPluginArtifactPackageIdentity(packageIdentity) ||
		!validSHA256Hex(strings.ToLower(strings.TrimSpace(packageDigest))) || strings.TrimSpace(relativePath) == "" {
		return "", errors.New("artifact package identity is incomplete")
	}
	if extension != ".wasm" && extension != ".bin" {
		return "", errors.New("artifact cache mode is invalid")
	}
	accept := "application/wasm"
	if extension == ".bin" {
		accept = "application/octet-stream"
	}

	targetDir := filepath.Join(cacheDir, "sha256", digest[:2])
	lockIndex := int(digest[0]) % len(pluginArtifactMaterializeLocks)
	pluginArtifactMaterializeLocks[lockIndex].Lock()
	defer pluginArtifactMaterializeLocks[lockIndex].Unlock()
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", fmt.Errorf("create artifact cache: %w", err)
	}
	target := filepath.Join(targetDir, digest+extension)
	if matches, err := pluginArtifactFileMatches(target, digest, sizeBytes); err != nil {
		return "", err
	} else if matches {
		return target, nil
	}

	query := url.Values{}
	query.Set("revision", strconv.FormatInt(revision, 10))
	query.Set("snapshot_digest", snapshotDigest)
	endpoint := c.cfg.MasterURL + "/api/agent-plugin-artifacts/" + url.PathEscape(artifactID) + "?" + query.Encode()
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
	if err := c.downloadPluginArtifact(ctx, endpoint, temporary, digest, sizeBytes, accept); err != nil {
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

func validPluginArtifactPackageIdentity(value string) bool {
	if value == "" || len(value) > maxPluginArtifactIdentityBytes || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._:@+-/", char) {
			continue
		}
		return false
	}
	return true
}

func clonePluginGeneration(generation model.PluginGeneration) model.PluginGeneration {
	generation.Config = append([]byte(nil), generation.Config...)
	generation.ExtensionPoints = append([]string(nil), generation.ExtensionPoints...)
	generation.Grants = append([]model.PluginGrantProjection(nil), generation.Grants...)
	generation.SecretHandles = append([]model.PluginSecretHandle(nil), generation.SecretHandles...)
	return generation
}

func (c *SyncClient) downloadPluginArtifact(ctx context.Context, endpoint string, target io.Writer, digest string, size int64, accept string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build artifact request: %w", err)
	}
	req.Header.Set("X-Agent-Token", c.cfg.AgentToken)
	req.Header.Set("Accept", accept)
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
