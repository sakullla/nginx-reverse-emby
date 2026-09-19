package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/datasets"
)

// Report a rejected data candidate using the existing authenticated lease
// protocol. No runtime generation is published and no applied pointer changes.
// If the network is unavailable, the lease/retry coordinator retains the old
// applied revision and retries; a failed report is never represented as success.
func (c *SyncClient) reportDatasetPreparationFailure(ctx context.Context, lease model.RevisionLease) error {
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	digest := sha256.Sum256([]byte(lease.LeaseID))
	generation := "dataset-prepare-" + hex.EncodeToString(digest[:])
	if err := c.StartRevision(reportCtx, model.RevisionStart{AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generation}); err != nil {
		return err
	}
	return c.ReportRevision(reportCtx, model.RevisionReport{AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle, Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generation, Status: "failed", ErrorCode: "dataset-prepare-failed", ErrorMessage: "dataset artifact preparation failed"})
}

// Dataset artifacts have their own endpoint and cache extension. They are never
// executable and never pass through plugin package/manifest authorization.
func (c *SyncClient) prepareDatasetArtifacts(ctx context.Context, snapshot *model.Snapshot, revision int64, snapshotDigest string) error {
	if len(snapshot.Datasets) == 0 {
		return nil
	}
	if revision <= 0 || snapshot.Revision != revision || !validSHA256Hex(snapshotDigest) || c.cfg.PluginCacheDir == "" {
		return errors.New("dataset snapshot requires an immutable revision and Agent cache")
	}
	prepared := model.CloneDatasetSnapshots(snapshot.Datasets)
	for i := range prepared {
		prepared[i].Artifact.LocalPath = ""
		if err := prepared[i].Validate(); err != nil {
			return err
		}
		path, err := c.materializeDatasetArtifact(ctx, prepared[i], revision, snapshotDigest)
		if err != nil {
			return err
		}
		prepared[i].Artifact.LocalPath = path
	}
	snapshot.Datasets = prepared
	return nil
}

func (c *SyncClient) materializeDatasetArtifact(ctx context.Context, entry model.DatasetSnapshot, revision int64, snapshotDigest string) (string, error) {
	artifact := entry.Artifact
	directory := filepath.Join(c.cfg.PluginCacheDir, "datasets", "sha256", artifact.SHA256[:2])
	lock := &pluginArtifactMaterializeLocks[int(artifact.SHA256[0])%len(pluginArtifactMaterializeLocks)]
	lock.Lock()
	defer lock.Unlock()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, artifact.SHA256+".nredataset")
	if matches, err := pluginArtifactFileMatches(target, artifact.SHA256, artifact.SizeBytes); err != nil {
		return "", err
	} else if matches {
		return target, validatePreparedDatasetIndex(ctx, target, entry)
	}
	query := url.Values{"revision": []string{strconv.FormatInt(revision, 10)}, "snapshot_digest": []string{snapshotDigest}}
	endpoint := c.cfg.MasterURL + "/api/agent-dataset-artifacts/" + url.PathEscape(artifact.ID) + "?" + query.Encode()
	file, err := os.CreateTemp(directory, ".dataset-*.tmp")
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	if err := c.downloadPluginArtifact(ctx, endpoint, file, artifact.SHA256, artifact.SizeBytes, "application/vnd.nre.dataset-index"); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := validatePreparedDatasetIndex(ctx, file.Name(), entry); err != nil {
		return "", err
	}
	if err := publishPluginArtifact(file.Name(), target); err != nil {
		return "", err
	}
	return target, nil
}

func validatePreparedDatasetIndex(ctx context.Context, path string, entry model.DatasetSnapshot) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, entry.Artifact.SizeBytes+1))
	if err != nil {
		return err
	}
	if int64(len(encoded)) != entry.Artifact.SizeBytes {
		return errors.New("prepared dataset index size changed")
	}
	index, err := datasets.LoadIndex(ctx, encoded, datasets.DefaultLimits())
	if err != nil {
		return err
	}
	if index.Version() != entry.Version {
		return errors.New("prepared dataset index digest or version differs from desired manifest")
	}
	return nil
}
