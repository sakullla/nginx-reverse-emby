package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrDatasetNotFound = errors.New("dataset resource not found")
var ErrDatasetInUse = errors.New("dataset version is referenced or retained")

func (s *GormStore) PutDatasetSource(ctx context.Context, row DatasetSourceRow) error {
	var source pluginsdk.DatasetSource
	if json.Unmarshal([]byte(row.SourceJSON), &source) != nil || source.ID != row.ID || source.Validate() != nil || !json.Valid([]byte(row.RetrievalJSON)) {
		return errors.New("dataset source record is invalid")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var current DatasetSourceRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.ID).First(&current).Error
		if err == nil {
			var old pluginsdk.DatasetSource
			if json.Unmarshal([]byte(current.SourceJSON), &old) != nil || old.Format != source.Format {
				return errors.New("dataset source format is immutable")
			}
			return tx.Model(&current).Updates(map[string]any{"source_json": row.SourceJSON, "retrieval_json": row.RetrievalJSON, "next_refresh_at": row.NextRefreshAt, "updated_at": time.Now().UTC()}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&row).Error
	})
}

func (s *GormStore) GetDatasetSource(ctx context.Context, id string) (DatasetSourceRow, error) {
	var row DatasetSourceRow
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrDatasetNotFound
	}
	return row, err
}
func (s *GormStore) ListDatasetSources(ctx context.Context) ([]DatasetSourceRow, error) {
	var rows []DatasetSourceRow
	err := s.db.WithContext(ctx).Order("id").Find(&rows).Error
	return rows, err
}
func (s *GormStore) RecordDatasetRefresh(ctx context.Context, id string, failure pluginsdk.DatasetFailureCode, next *time.Time) error {
	if failure != "" && failure.Validate() != nil {
		return errors.New("invalid dataset failure code")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&DatasetSourceRow{}).Where("id = ?", id).Updates(map[string]any{"last_failure": string(failure), "last_refresh_at": time.Now().UTC(), "next_refresh_at": next}).Error
	})
}

func (s *GormStore) RecordDatasetFailure(ctx context.Context, id string, failure pluginsdk.DatasetFailureCode) error {
	if failure.Validate() != nil {
		return errors.New("invalid dataset failure code")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&DatasetSourceRow{}).Where("id = ?", id).Update("last_failure", string(failure)).Error
	})
}

// StoreDatasetVersion stores a prepared index separately from its metadata.
// It never changes the active pointer. Files are content addressed and the
// existing revision artifact owner verifies and preserves their immutability.
func (s *GormStore) StoreDatasetVersion(ctx context.Context, version pluginsdk.DatasetVersion, encodedIndex []byte, verification ...DatasetVersionVerification) (DatasetVersionRow, error) {
	if version.Validate() != nil || len(encodedIndex) == 0 || int64(len(encodedIndex)) > pluginsdk.DatasetDefaultIndexBudgetBytes {
		return DatasetVersionRow{}, errors.New("prepared dataset index is invalid or oversized")
	}
	digest := sha256.Sum256(encodedIndex)
	sha := hex.EncodeToString(digest[:])
	if version.IndexDigest != "sha256:"+sha || version.IndexBytes != int64(len(encodedIndex)) {
		return DatasetVersionRow{}, errors.New("prepared dataset index differs from its manifest")
	}
	path, err := writeGenerationArtifactFile(s.dataRoot, sha, encodedIndex)
	if err != nil {
		return DatasetVersionRow{}, err
	}
	versionJSON, err := json.Marshal(version)
	if err != nil {
		return DatasetVersionRow{}, err
	}
	now := time.Now().UTC()
	row := DatasetVersionRow{SourceID: version.SourceID, Digest: version.Digest, VersionJSON: string(versionJSON), ArtifactID: "dataset-" + sha, ArtifactSHA256: sha, ArtifactSizeBytes: int64(len(encodedIndex)), ArtifactPath: path, CreatedAt: now}
	if len(verification) > 1 {
		return DatasetVersionRow{}, errors.New("ambiguous dataset verification evidence")
	}
	if len(verification) == 1 {
		evidence := verification[0]
		if evidence.Mode != "rolling-sha256" || evidence.ExpectedDigest != version.RawDigest || evidence.ChecksumURL == "" || !strings.HasPrefix(evidence.ChecksumDigest, "sha256:") || !validSHA256(strings.TrimPrefix(evidence.ChecksumDigest, "sha256:")) {
			return DatasetVersionRow{}, errors.New("dataset verification evidence is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, evidence.ResolvedAt); err != nil {
			return DatasetVersionRow{}, err
		}
		encoded, _ := json.Marshal(evidence)
		row.VerificationJSON = string(encoded)
	}
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", version.SourceID).First(&source).Error; err != nil {
			return err
		}
		var descriptor pluginsdk.DatasetSource
		if json.Unmarshal([]byte(source.SourceJSON), &descriptor) != nil || descriptor.Format != version.Format {
			return errors.New("prepared dataset source identity differs")
		}
		artifact := datasetArtifactRow(row)
		if err := validateGenerationArtifact(artifact); err != nil {
			return err
		}
		if err := createImmutableArtifact(tx, artifact); err != nil {
			return err
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil || result.RowsAffected > 0 {
			return result.Error
		}
		var previous DatasetVersionRow
		if err := tx.Where("source_id = ? AND digest = ?", row.SourceID, row.Digest).First(&previous).Error; err != nil {
			return err
		}
		if previous.VersionJSON != row.VersionJSON || previous.ArtifactSHA256 != row.ArtifactSHA256 {
			return errors.New("dataset version is immutable")
		}
		row = previous
		return nil
	})
	return row, err
}

func datasetArtifactRow(row DatasetVersionRow) GenerationArtifactRow {
	return GenerationArtifactRow{ID: row.ArtifactID, Kind: DatasetArtifactKind, SHA256: row.ArtifactSHA256, SizeBytes: row.ArtifactSizeBytes, ExternalPath: row.ArtifactPath, Payload: []byte{}, CreatedAt: row.CreatedAt}
}
func (s *GormStore) GetDatasetVersion(ctx context.Context, sourceID, digest string) (DatasetVersionRow, error) {
	var row DatasetVersionRow
	err := s.db.WithContext(ctx).Where("source_id = ? AND digest = ?", sourceID, digest).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrDatasetNotFound
	}
	return row, err
}
func (s *GormStore) ListDatasetVersions(ctx context.Context, sourceID string) ([]DatasetVersionRow, error) {
	var rows []DatasetVersionRow
	err := s.db.WithContext(ctx).Where("source_id = ?", sourceID).Order("created_at DESC, digest").Find(&rows).Error
	return rows, err
}
func (s *GormStore) ReadDatasetIndex(ctx context.Context, sourceID, digest string) (DatasetVersionRow, []byte, error) {
	row, err := s.GetDatasetVersion(ctx, sourceID, digest)
	if err != nil {
		return row, nil, err
	}
	artifact, err := s.materializeGenerationArtifact(datasetArtifactRow(row))
	return row, artifact.Payload, err
}

func (s *GormStore) ResolveLocalDatasetArtifact(ctx context.Context, entry DatasetSnapshot) (string, error) {
	row, err := s.GetDatasetVersion(ctx, entry.Version.SourceID, entry.Version.Digest)
	if err != nil {
		return "", err
	}
	var version pluginsdk.DatasetVersion
	if json.Unmarshal([]byte(row.VersionJSON), &version) != nil || version != entry.Version || entry.Artifact.Kind != DatasetArtifactKind || entry.Artifact.ID != row.ArtifactID || entry.Artifact.SHA256 != row.ArtifactSHA256 || entry.Artifact.SizeBytes != row.ArtifactSizeBytes {
		return "", errors.New("embedded dataset differs from prepared version")
	}
	if _, err := s.materializeGenerationArtifact(datasetArtifactRow(row)); err != nil {
		return "", err
	}
	return generationArtifactFilePath(s.dataRoot, row.ArtifactPath, row.ArtifactSHA256)
}
func (s *GormStore) DatasetBindings(ctx context.Context, sourceID string) ([]DatasetBindingRow, error) {
	var rows []DatasetBindingRow
	query := s.db.WithContext(ctx)
	if s.transactionScoped {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where("source_id = ?", sourceID).Order("agent_id, instance_id").Find(&rows).Error
	return rows, err
}

func (s *GormStore) LockDatasetSource(ctx context.Context, sourceID string) error {
	var row DatasetSourceRow
	return s.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&row).Error
}

// PutDatasetBinding is called inside a revision mutation after source category
// and instance authorization validation. Binding and plugin configuration can
// be changed in the same transaction by the config owner.
func (s *GormStore) PutDatasetBinding(ctx context.Context, row DatasetBindingRow) error {
	for _, id := range []string{row.AgentID, row.InstanceID, row.SourceID} {
		if pluginsdk.ValidatePolicyIdentity(id) != nil {
			return errors.New("dataset binding identity is invalid")
		}
	}
	if row.Revision <= 0 {
		return errors.New("dataset binding requires an issued revision")
	}
	var classifications []pluginsdk.DatasetClassification
	if json.Unmarshal([]byte(row.ClassificationsJSON), &classifications) != nil || len(classifications) == 0 || len(classifications) > pluginsdk.DatasetMaxQueryClassifications {
		return errors.New("dataset binding classifications are invalid")
	}
	for _, class := range classifications {
		if err := class.Validate(); err != nil {
			return err
		}
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", row.SourceID).First(&source).Error; err != nil {
			return err
		}
		var version DatasetVersionRow
		if err := tx.Where("source_id = ? AND digest = ?", row.SourceID, row.VersionDigest).First(&version).Error; err != nil {
			return err
		}
		return tx.Save(&row).Error
	})
}
func (s *GormStore) RemoveDatasetBinding(ctx context.Context, sourceID, agentID, instanceID string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		return tx.Where("source_id = ? AND agent_id = ? AND instance_id = ?", sourceID, agentID, instanceID).Delete(&DatasetBindingRow{}).Error
	})
}
func (s *GormStore) ActivateDatasetVersion(ctx context.Context, sourceID, digest string, revisions map[string]int64) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		var row DatasetVersionRow
		if err := tx.Where("source_id = ? AND digest = ?", sourceID, digest).First(&row).Error; err != nil {
			return err
		}
		var bindings []DatasetBindingRow
		if err := tx.Where("source_id = ?", sourceID).Find(&bindings).Error; err != nil {
			return err
		}
		for _, binding := range bindings {
			revision, ok := revisions[binding.AgentID]
			if !ok || revision <= 0 {
				return errors.New("dataset activation is missing a consumer revision")
			}
			if err := tx.Model(&DatasetBindingRow{}).Where("source_id = ? AND agent_id = ? AND instance_id = ?", sourceID, binding.AgentID, binding.InstanceID).Updates(map[string]any{"version_digest": digest, "revision": revision}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&DatasetSourceRow{}).Where("id = ?", sourceID).Updates(map[string]any{"current_digest": digest, "last_failure": ""}).Error
	})
}

func (s *GormStore) DeleteDatasetVersion(ctx context.Context, sourceID, digest string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		if source.CurrentDigest == digest {
			return ErrDatasetInUse
		}
		var retained []DatasetVersionRow
		if err := tx.Where("source_id = ?", sourceID).Order("created_at DESC, digest").Limit(3).Find(&retained).Error; err != nil {
			return err
		}
		for _, row := range retained {
			if row.Digest == digest {
				return ErrDatasetInUse
			}
		}
		var version DatasetVersionRow
		if err := tx.Where("source_id = ? AND digest = ?", sourceID, digest).First(&version).Error; err != nil {
			return err
		}
		if err := datasetVersionUnreferenced(tx, version); err != nil {
			return err
		}
		return tx.Delete(&version).Error
	})
}
func (s *GormStore) DeleteDatasetSource(ctx context.Context, sourceID string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		var versions []DatasetVersionRow
		if err := tx.Where("source_id = ?", sourceID).Find(&versions).Error; err != nil {
			return err
		}
		for _, version := range versions {
			if err := datasetVersionUnreferenced(tx, version); err != nil {
				return err
			}
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&DatasetVersionRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", sourceID).Delete(&DatasetUploadRow{}).Error; err != nil {
			return err
		}
		return tx.Delete(&source).Error
	})
}
func datasetVersionUnreferenced(tx *gorm.DB, version DatasetVersionRow) error {
	var count int64
	if err := tx.Model(&DatasetBindingRow{}).Where("source_id = ? AND version_digest = ?", version.SourceID, version.Digest).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrDatasetInUse
	}
	if err := tx.Model(&AgentRevisionArtifactRow{}).Where("artifact_id = ?", version.ArtifactID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrDatasetInUse
	}
	return nil
}

func (s *GormStore) loadAgentDatasetSnapshots(ctx context.Context, agentID string) ([]DatasetSnapshot, error) {
	result := make([]DatasetSnapshot, 0)
	if !s.db.Migrator().HasTable(&DatasetBindingRow{}) {
		return result, nil
	}
	var rows []DatasetBindingRow
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Order("source_id, instance_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	positions := make(map[string]int)
	for _, binding := range rows {
		index, exists := positions[binding.SourceID]
		if !exists {
			row, err := s.GetDatasetVersion(ctx, binding.SourceID, binding.VersionDigest)
			if err != nil {
				return nil, err
			}
			var version pluginsdk.DatasetVersion
			if json.Unmarshal([]byte(row.VersionJSON), &version) != nil || version.Validate() != nil {
				return nil, errors.New("stored dataset version is invalid")
			}
			index = len(result)
			positions[binding.SourceID] = index
			result = append(result, DatasetSnapshot{Version: version, Artifact: DatasetArtifact{ID: row.ArtifactID, Kind: DatasetArtifactKind, SHA256: row.ArtifactSHA256, SizeBytes: row.ArtifactSizeBytes}})
		} else if result[index].Version.Digest != binding.VersionDigest {
			return nil, errors.New("one Agent snapshot cannot mix versions of a dataset source")
		}
		var classes []pluginsdk.DatasetClassification
		if json.Unmarshal([]byte(binding.ClassificationsJSON), &classes) != nil {
			return nil, errors.New("stored dataset binding is invalid")
		}
		result[index].Bindings = append(result[index].Bindings, DatasetInstanceBinding{InstanceID: binding.InstanceID, Classifications: classes})
	}
	return result, nil
}

// Disabled or removed instances keep their configured references for a future
// explicit re-enable, but cannot make an absent runtime consume a data index.
func activeDatasetSnapshots(values []DatasetSnapshot, policies []PluginPolicy, generations []PluginGeneration) []DatasetSnapshot {
	active := map[string]bool{}
	for _, generation := range generations {
		active[generation.InstanceID] = true
	}
	for _, policy := range policies {
		for _, stage := range policy.Stages {
			active[stage.InstanceID] = true
		}
	}
	result := make([]DatasetSnapshot, 0, len(values))
	for _, value := range values {
		bindings := make([]DatasetInstanceBinding, 0, len(value.Bindings))
		for _, binding := range value.Bindings {
			if active[binding.InstanceID] {
				bindings = append(bindings, binding)
			}
		}
		if len(bindings) > 0 {
			value.Bindings = bindings
			result = append(result, value)
		}
	}
	return result
}

func buildAgentRevisionDatasetArtifacts(ctx context.Context, db *gorm.DB, agentID string, revision int64, snapshot Snapshot, now time.Time) ([]GenerationArtifactRow, []AgentRevisionArtifactRow, error) {
	var artifacts []GenerationArtifactRow
	var refs []AgentRevisionArtifactRow
	seen := map[string]bool{}
	for _, dataset := range snapshot.Datasets {
		if dataset.Version.Validate() != nil || dataset.Artifact.Kind != DatasetArtifactKind || seen[dataset.Version.SourceID] {
			return nil, nil, errors.New("dataset snapshot is invalid or duplicated")
		}
		seen[dataset.Version.SourceID] = true
		var row DatasetVersionRow
		if err := db.WithContext(ctx).Where("source_id = ? AND digest = ?", dataset.Version.SourceID, dataset.Version.Digest).First(&row).Error; err != nil {
			return nil, nil, err
		}
		var version pluginsdk.DatasetVersion
		if json.Unmarshal([]byte(row.VersionJSON), &version) != nil || version != dataset.Version || row.ArtifactID != dataset.Artifact.ID || row.ArtifactSHA256 != dataset.Artifact.SHA256 || row.ArtifactSizeBytes != dataset.Artifact.SizeBytes {
			return nil, nil, errors.New("dataset snapshot differs from prepared version")
		}
		artifacts = append(artifacts, datasetArtifactRow(row))
		refs = append(refs, AgentRevisionArtifactRow{AgentID: agentID, Revision: revision, ArtifactID: row.ArtifactID, Role: revisionDatasetArtifactRolePrefix + row.ArtifactID, CreatedAt: now})
	}
	return artifacts, refs, nil
}

func (s *GormStore) ResolveAgentRevisionDatasetArtifact(ctx context.Context, agentID string, revision int64, snapshotDigest, artifactID string) (GenerationArtifactRow, error) {
	var issued AgentRevisionRow
	if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ? AND snapshot_digest = ?", agentID, revision, snapshotDigest).First(&issued).Error; err != nil {
		return GenerationArtifactRow{}, ErrDatasetNotFound
	}
	var snapshotRow GenerationArtifactRow
	if err := s.db.WithContext(ctx).Where("id = ?", issued.SnapshotArtifactID).First(&snapshotRow).Error; err != nil {
		return GenerationArtifactRow{}, err
	}
	if validateGenerationArtifact(snapshotRow) != nil || snapshotRow.SHA256 != snapshotDigest {
		return GenerationArtifactRow{}, errors.New("dataset revision snapshot failed integrity validation")
	}
	var snapshot Snapshot
	if json.Unmarshal(snapshotRow.Payload, &snapshot) != nil {
		return GenerationArtifactRow{}, errors.New("dataset revision snapshot is invalid")
	}
	var expected *DatasetArtifact
	for _, dataset := range snapshot.Datasets {
		if dataset.Artifact.ID == artifactID {
			if expected != nil {
				return GenerationArtifactRow{}, errors.New("dataset artifact is duplicated")
			}
			value := dataset.Artifact
			expected = &value
		}
	}
	if expected == nil || expected.Kind != DatasetArtifactKind {
		return GenerationArtifactRow{}, ErrDatasetNotFound
	}
	var ref AgentRevisionArtifactRow
	if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ? AND artifact_id = ? AND role = ?", agentID, revision, artifactID, revisionDatasetArtifactRolePrefix+artifactID).First(&ref).Error; err != nil {
		return GenerationArtifactRow{}, ErrDatasetNotFound
	}
	var blob GenerationArtifactRow
	if err := s.db.WithContext(ctx).Where("id = ?", artifactID).First(&blob).Error; err != nil {
		return GenerationArtifactRow{}, err
	}
	if blob.Kind != DatasetArtifactKind || blob.SHA256 != expected.SHA256 || blob.SizeBytes != expected.SizeBytes {
		return GenerationArtifactRow{}, errors.New("dataset artifact identity differs from revision")
	}
	return s.materializeGenerationArtifact(blob)
}

// DatasetNodeStatus derives actual application exclusively from the revision
// ledger's acknowledged snapshot. A desired source pointer never implies that
// an offline node has prepared or switched to it.
func (s *GormStore) DatasetNodeStatus(ctx context.Context, sourceID, agentID string, now time.Time) (pluginsdk.DatasetStatusResponse, error) {
	status := pluginsdk.DatasetStatusResponse{SourceID: sourceID, NodeID: agentID, Phase: pluginsdk.DatasetNodeUnavailable}
	var bindings []DatasetBindingRow
	if err := s.db.WithContext(ctx).Where("source_id = ? AND agent_id = ?", sourceID, agentID).Find(&bindings).Error; err != nil {
		return status, err
	}
	for _, binding := range bindings {
		if status.Desired != "" && status.Desired != binding.VersionDigest {
			return status, errors.New("inconsistent desired dataset versions")
		}
		status.Desired = binding.VersionDigest
	}
	pointer, found, err := s.GetAgentRevisionPointer(ctx, agentID)
	if err != nil {
		return status, err
	}
	if found && pointer.AppliedRevision > 0 {
		var applied AgentRevisionRow
		if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ?", agentID, pointer.AppliedRevision).First(&applied).Error; err != nil {
			return status, err
		}
		var blob GenerationArtifactRow
		if err := s.db.WithContext(ctx).Where("id = ?", applied.SnapshotArtifactID).First(&blob).Error; err != nil {
			return status, err
		}
		var snapshot Snapshot
		if validateGenerationArtifact(blob) != nil || json.Unmarshal(blob.Payload, &snapshot) != nil {
			return status, errors.New("applied dataset snapshot is invalid")
		}
		for _, dataset := range snapshot.Datasets {
			if dataset.Version.SourceID == sourceID {
				status.Applied = dataset.Version.Digest
				status.LastGood = status.Applied
				status.Generation = applied.GenerationID
			}
		}
	}
	if status.Desired != "" {
		status.Phase = pluginsdk.DatasetNodePreparing
	}
	if status.Applied != "" && status.Applied == status.Desired {
		status.Phase = pluginsdk.DatasetNodeApplied
	}
	if source, err := s.GetDatasetSource(ctx, sourceID); err == nil && source.LastFailure != "" {
		status.Phase = pluginsdk.DatasetNodeFailed
		status.Failure = pluginsdk.DatasetFailureCode(source.LastFailure)
	}
	if found && pointer.DesiredRevision > 0 {
		var desired AgentRevisionRow
		if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ?", agentID, pointer.DesiredRevision).First(&desired).Error; err == nil && desired.FailedAt != nil {
			status.Phase = pluginsdk.DatasetNodeFailed
			status.Failure = pluginsdk.DatasetFailureInvalidData
		}
	}
	if agentID != s.localAgentID {
		var agent AgentRow
		if err := s.db.WithContext(ctx).Where("id = ?", agentID).First(&agent).Error; err != nil {
			return status, ErrDatasetNotFound
		}
		seen, err := time.Parse(time.RFC3339Nano, agent.LastSeenAt)
		if err != nil || now.Sub(seen) > 2*time.Minute {
			status.Phase = pluginsdk.DatasetNodeOffline
			status.Failure = ""
		}
	}
	return status, status.Validate()
}

func (s *GormStore) DatasetHistory(ctx context.Context, request pluginsdk.DatasetCatalogRequest) (pluginsdk.DatasetCatalogResponse, error) {
	if request.Validate() != nil || request.VersionDigest != "" {
		return pluginsdk.DatasetCatalogResponse{}, errors.New("dataset history request is invalid")
	}
	rows, err := s.ListDatasetVersions(ctx, request.SourceID)
	if err != nil {
		return pluginsdk.DatasetCatalogResponse{}, err
	}
	// A cursor is a stable version digest without its sha256 prefix, not a row offset.
	start := 0
	if request.Cursor != "" {
		found := false
		for i, row := range rows {
			if row.Digest == "sha256:"+request.Cursor {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return pluginsdk.DatasetCatalogResponse{}, errors.New("dataset history cursor is stale")
		}
	}
	response := pluginsdk.DatasetCatalogResponse{SourceID: request.SourceID}
	end := min(start+request.Limit, len(rows))
	for _, row := range rows[start:end] {
		var version pluginsdk.DatasetVersion
		if json.Unmarshal([]byte(row.VersionJSON), &version) != nil {
			return response, fmt.Errorf("dataset version metadata is corrupt")
		}
		response.Versions = append(response.Versions, version)
	}
	if end < len(rows) {
		response.NextCursor = rows[end-1].Digest[len("sha256:"):]
	}
	return response, response.Validate()
}

func DatasetTargetIDs(bindings []DatasetBindingRow) []string {
	seen := map[string]bool{}
	for _, binding := range bindings {
		seen[binding.AgentID] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *GormStore) StoreDatasetUpload(ctx context.Context, sourceID string, payload []byte) (string, error) {
	if len(payload) == 0 || int64(len(payload)) > pluginsdk.DatasetMaxDownloadBytes {
		return "", errors.New("dataset upload exceeds byte budget")
	}
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])
	digest := "sha256:" + sha
	path, err := writeGenerationArtifactFile(s.dataRoot, sha, payload)
	if err != nil {
		return "", err
	}
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var source DatasetSourceRow
		if err := tx.Where("id = ?", sourceID).First(&source).Error; err != nil {
			return err
		}
		artifact := GenerationArtifactRow{ID: "dataset-source-" + sha, Kind: "dataset-source-v1", SHA256: sha, ExternalPath: path, SizeBytes: int64(len(payload)), Payload: []byte{}, CreatedAt: time.Now().UTC()}
		if err := createImmutableArtifact(tx, artifact); err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&DatasetUploadRow{SourceID: sourceID, Digest: digest, ArtifactID: artifact.ID, CreatedAt: time.Now().UTC()}).Error
	})
	return digest, err
}
func (s *GormStore) ReadDatasetUpload(ctx context.Context, sourceID, digest string) ([]byte, error) {
	var upload DatasetUploadRow
	if err := s.db.WithContext(ctx).Where("source_id = ? AND digest = ?", sourceID, digest).First(&upload).Error; err != nil {
		return nil, ErrDatasetNotFound
	}
	var artifact GenerationArtifactRow
	if err := s.db.WithContext(ctx).Where("id = ? AND kind = ?", upload.ArtifactID, "dataset-source-v1").First(&artifact).Error; err != nil {
		return nil, err
	}
	if "sha256:"+artifact.SHA256 != digest || artifact.SizeBytes > pluginsdk.DatasetMaxDownloadBytes {
		return nil, errors.New("dataset upload digest is invalid")
	}
	materialized, err := s.materializeGenerationArtifact(artifact)
	return materialized.Payload, err
}
func (s *GormStore) PruneDatasetUploads(ctx context.Context, cutoff time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error { return tx.Where("created_at < ?", cutoff).Delete(&DatasetUploadRow{}).Error })
}
