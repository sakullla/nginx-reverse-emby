package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	revisionLedgerBaselineMarkerKey   = "migration.agent_revision_ledger_baseline.v1"
	revisionSnapshotArtifactRole      = "snapshot"
	revisionPolicyArtifactRolePrefix  = "plugin_policy_artifact:"
	revisionPolicyArtifactKind        = "plugin_policy_wasm"
	revisionRuntimeArtifactRolePrefix = "plugin_runtime_artifact:"
	revisionRuntimeArtifactKind       = "plugin_runtime_artifact"

	OperationStatusPending = "pending"
	OperationStatusApplied = "applied"

	AgentRevisionStatePending = "pending"
	AgentRevisionStateApplied = "applied"

	GenerationStateActive   = "active"
	GenerationStateDraining = "draining"
)

type RevisionLedgerWrite struct {
	Operation          OperationRow
	Revisions          []AgentRevisionRow
	Pointers           []AgentRevisionPointerRow
	Attempts           []AgentRevisionAttemptRow
	Generations        []AgentGenerationRow
	Events             []RevisionEventRow
	Artifacts          []GenerationArtifactRow
	ArtifactRefs       []AgentRevisionArtifactRow
	IdempotencyRecords []IdempotencyRecordRow
}

type revisionPolicyArtifactIdentity struct {
	Source            PolicyArtifactSource
	ArtifactDigest    string
	PackageDigest     string
	SignerFingerprint string
}

type revisionRuntimeArtifactIdentity struct {
	ArtifactID        string
	PackageIdentity   string
	PackageDigest     string
	RelativePath      string
	ArtifactDigest    string
	SizeBytes         int64
	SignerKeyID       string
	SignerFingerprint string
}

// BuildAgentRevisionPolicyArtifacts copies every policy WASM referenced by an
// issued snapshot into the immutable revision ledger. The copy makes retries
// independent of later package lifecycle and cache cleanup.
func (s *GormStore) BuildAgentRevisionPolicyArtifacts(ctx context.Context, agentID string, revision int64, snapshot Snapshot, now time.Time) ([]GenerationArtifactRow, []AgentRevisionArtifactRow, error) {
	return buildAgentRevisionPolicyArtifacts(ctx, s.db, agentID, revision, snapshot, now)
}

func buildAgentRevisionPolicyArtifacts(ctx context.Context, db *gorm.DB, agentID string, revision int64, snapshot Snapshot, now time.Time) ([]GenerationArtifactRow, []AgentRevisionArtifactRow, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || revision <= 0 || snapshot.Revision != revision {
		return nil, nil, fmt.Errorf("agent revision policy artifact identity is invalid")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	identities := make(map[string]revisionPolicyArtifactIdentity)
	stages := make(map[string]PolicyStage)
	blobs := make(map[string]GenerationArtifactRow)
	refs := make([]AgentRevisionArtifactRow, 0)
	for _, policy := range snapshot.PluginPolicies {
		for _, stage := range policy.Stages {
			source := stage.ArtifactSource
			artifactID := strings.TrimSpace(source.ArtifactID)
			identity := revisionPolicyArtifactIdentity{
				Source: source, ArtifactDigest: strings.ToLower(strings.TrimSpace(stage.ArtifactDigest)),
				PackageDigest:     strings.ToLower(strings.TrimSpace(stage.PackageDigest)),
				SignerFingerprint: strings.ToLower(strings.TrimSpace(stage.SignerFingerprint)),
			}
			if artifactID == "" || source.SizeBytes <= 0 || identity.ArtifactDigest == "" ||
				!strings.EqualFold(source.SHA256, identity.ArtifactDigest) ||
				!strings.EqualFold(source.PackageDigest, identity.PackageDigest) {
				return nil, nil, fmt.Errorf("policy artifact %q has incomplete snapshot identity", artifactID)
			}
			if previous, found := identities[artifactID]; found {
				if previous != identity {
					return nil, nil, fmt.Errorf("policy artifact %q has conflicting snapshot identities", artifactID)
				}
				continue
			}
			identities[artifactID] = identity
			stages[artifactID] = stage
		}
	}
	artifactIDs := make([]string, 0, len(stages))
	for artifactID := range stages {
		artifactIDs = append(artifactIDs, artifactID)
	}
	sort.Strings(artifactIDs)
	for _, artifactID := range artifactIDs {
		stage, identity := stages[artifactID], identities[artifactID]
		payload, err := loadIssuedPolicyArtifact(ctx, db, stage)
		if err != nil {
			return nil, nil, err
		}
		blobID := revisionPolicyArtifactBlobID(identity.ArtifactDigest)
		blob := GenerationArtifactRow{ID: blobID, Kind: revisionPolicyArtifactKind, SHA256: identity.ArtifactDigest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now}
		if existing, found := blobs[blobID]; found {
			if existing.SHA256 != blob.SHA256 || existing.SizeBytes != blob.SizeBytes || !bytes.Equal(existing.Payload, blob.Payload) {
				return nil, nil, fmt.Errorf("policy artifact blob %q has conflicting content", blobID)
			}
		} else {
			blobs[blobID] = blob
		}
		refs = append(refs, AgentRevisionArtifactRow{AgentID: agentID, Revision: revision, ArtifactID: blobID, Role: revisionPolicyArtifactRole(artifactID), CreatedAt: now})
	}
	runtimeIdentities := make(map[string]revisionRuntimeArtifactIdentity)
	runtimeGenerations := make(map[string]PluginGeneration)
	for _, generation := range snapshot.PluginGenerations {
		artifactID := strings.TrimSpace(generation.Artifact.ArtifactID)
		identity := revisionRuntimeArtifactIdentity{
			ArtifactID: artifactID, PackageIdentity: strings.TrimSpace(generation.Artifact.PackageIdentity),
			PackageDigest: strings.ToLower(strings.TrimSpace(generation.PackageDigest)), RelativePath: generation.Artifact.RelativePath,
			ArtifactDigest: strings.ToLower(strings.TrimSpace(generation.Artifact.SHA256)), SizeBytes: generation.Artifact.SizeBytes,
			SignerKeyID: generation.Artifact.SignerKeyID, SignerFingerprint: strings.ToLower(strings.TrimSpace(generation.Artifact.SignerFingerprint)),
		}
		if artifactID == "" || identity.PackageIdentity == "" || identity.RelativePath == "" || identity.SizeBytes <= 0 ||
			!validSHA256(identity.PackageDigest) || !validSHA256(identity.ArtifactDigest) || !validSHA256(identity.SignerFingerprint) {
			return nil, nil, fmt.Errorf("plugin runtime artifact %q has incomplete snapshot identity", artifactID)
		}
		if previous, found := runtimeIdentities[artifactID]; found {
			if previous != identity {
				return nil, nil, fmt.Errorf("plugin runtime artifact %q has conflicting snapshot identities", artifactID)
			}
			continue
		}
		runtimeIdentities[artifactID], runtimeGenerations[artifactID] = identity, generation
	}
	runtimeArtifactIDs := make([]string, 0, len(runtimeGenerations))
	for artifactID := range runtimeGenerations {
		runtimeArtifactIDs = append(runtimeArtifactIDs, artifactID)
	}
	sort.Strings(runtimeArtifactIDs)
	for _, artifactID := range runtimeArtifactIDs {
		generation, identity := runtimeGenerations[artifactID], runtimeIdentities[artifactID]
		payload, err := loadIssuedRuntimeArtifact(ctx, db, generation)
		if err != nil {
			return nil, nil, err
		}
		blobID := revisionRuntimeArtifactBlobID(identity.ArtifactDigest)
		blob := GenerationArtifactRow{ID: blobID, Kind: revisionRuntimeArtifactKind, SHA256: identity.ArtifactDigest, Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now}
		if existing, found := blobs[blobID]; found {
			if existing.Kind != blob.Kind || existing.SHA256 != blob.SHA256 || existing.SizeBytes != blob.SizeBytes || !bytes.Equal(existing.Payload, blob.Payload) {
				return nil, nil, fmt.Errorf("plugin runtime artifact blob %q has conflicting content", blobID)
			}
		} else {
			blobs[blobID] = blob
		}
		refs = append(refs, AgentRevisionArtifactRow{AgentID: agentID, Revision: revision, ArtifactID: blobID, Role: revisionRuntimeArtifactRole(artifactID), CreatedAt: now})
	}
	artifacts := make([]GenerationArtifactRow, 0, len(blobs))
	for _, blob := range blobs {
		artifacts = append(artifacts, blob)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ID < artifacts[j].ID })
	sort.Slice(refs, func(i, j int) bool { return refs[i].Role < refs[j].Role })
	return artifacts, refs, nil
}

func loadIssuedPolicyArtifact(ctx context.Context, db *gorm.DB, stage PolicyStage) ([]byte, error) {
	source := stage.ArtifactSource
	var packageRow PluginPackageRow
	if err := db.WithContext(ctx).Where("identity = ?", source.PackageIdentity).First(&packageRow).Error; err != nil {
		return nil, fmt.Errorf("policy artifact %q package identity: %w", source.ArtifactID, err)
	}
	if !strings.EqualFold(packageRow.Digest, source.PackageDigest) ||
		!strings.EqualFold(packageRow.SignatureFingerprint, stage.SignerFingerprint) {
		return nil, fmt.Errorf("policy artifact %q package evidence differs from snapshot", source.ArtifactID)
	}
	var artifact PluginArtifactRow
	if err := db.WithContext(ctx).Where("id = ? AND package_identity = ?", source.ArtifactID, source.PackageIdentity).First(&artifact).Error; err != nil {
		return nil, fmt.Errorf("policy artifact %q durable identity: %w", source.ArtifactID, err)
	}
	if artifact.Path != source.RelativePath || !strings.EqualFold(artifact.PackageDigest, source.PackageDigest) ||
		!strings.EqualFold(artifact.SHA256, source.SHA256) || artifact.SizeBytes != source.SizeBytes {
		return nil, fmt.Errorf("policy artifact %q durable evidence differs from snapshot", source.ArtifactID)
	}
	path := filepath.Join(packageRow.CachePath, filepath.FromSlash(artifact.Path))
	relative, err := filepath.Rel(packageRow.CachePath, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("policy artifact %q path escapes verified package cache", source.ArtifactID)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open policy artifact %q: %w", source.ArtifactID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != source.SizeBytes {
		return nil, fmt.Errorf("policy artifact %q size differs from snapshot", source.ArtifactID)
	}
	hash := sha256.New()
	var payload bytes.Buffer
	written, err := io.Copy(io.MultiWriter(hash, &payload), io.LimitReader(file, source.SizeBytes+1))
	if err != nil || written != source.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), source.SHA256) {
		return nil, fmt.Errorf("policy artifact %q digest differs from snapshot", source.ArtifactID)
	}
	return payload.Bytes(), nil
}

func loadIssuedRuntimeArtifact(ctx context.Context, db *gorm.DB, generation PluginGeneration) ([]byte, error) {
	descriptor := generation.Artifact
	var packageRow PluginPackageRow
	if err := db.WithContext(ctx).Where("identity = ?", descriptor.PackageIdentity).First(&packageRow).Error; err != nil {
		return nil, fmt.Errorf("plugin runtime artifact %q package identity: %w", descriptor.ArtifactID, err)
	}
	if !strings.EqualFold(packageRow.Digest, generation.PackageDigest) || packageRow.SignatureVerdict != "verified" ||
		packageRow.SignatureKeyID != descriptor.SignerKeyID || !strings.EqualFold(packageRow.SignatureFingerprint, descriptor.SignerFingerprint) {
		return nil, fmt.Errorf("plugin runtime artifact %q package evidence differs from snapshot", descriptor.ArtifactID)
	}
	var artifact PluginArtifactRow
	if err := db.WithContext(ctx).Where("id = ? AND package_identity = ?", descriptor.ArtifactID, descriptor.PackageIdentity).First(&artifact).Error; err != nil {
		return nil, fmt.Errorf("plugin runtime artifact %q durable identity: %w", descriptor.ArtifactID, err)
	}
	if artifact.Path != descriptor.RelativePath || artifact.RuntimeKind != generation.Runtime.Kind || artifact.RuntimeABI != generation.Runtime.ABI ||
		!strings.EqualFold(artifact.PackageDigest, generation.PackageDigest) ||
		!strings.EqualFold(artifact.SHA256, descriptor.SHA256) || artifact.SizeBytes != descriptor.SizeBytes {
		return nil, fmt.Errorf("plugin runtime artifact %q durable evidence differs from snapshot", descriptor.ArtifactID)
	}
	var manifest pluginsdk.Manifest
	if err := json.Unmarshal([]byte(packageRow.ManifestJSON), &manifest); err != nil {
		return nil, fmt.Errorf("plugin runtime artifact %q package manifest: %w", descriptor.ArtifactID, err)
	}
	if !pluginsdk.RuntimeDurableArtifactHostScopeMatches(manifest.Runtime, generation.Runtime.HostScope, artifact.HostScope) {
		return nil, fmt.Errorf("plugin runtime artifact %q durable evidence differs from snapshot", descriptor.ArtifactID)
	}
	artifactPath := filepath.Join(packageRow.CachePath, filepath.FromSlash(artifact.Path))
	relative, err := filepath.Rel(packageRow.CachePath, artifactPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("plugin runtime artifact %q path escapes verified package cache", descriptor.ArtifactID)
	}
	file, err := os.Open(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("open plugin runtime artifact %q: %w", descriptor.ArtifactID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != descriptor.SizeBytes {
		return nil, fmt.Errorf("plugin runtime artifact %q size differs from snapshot", descriptor.ArtifactID)
	}
	hash := sha256.New()
	var payload bytes.Buffer
	written, err := io.Copy(io.MultiWriter(hash, &payload), io.LimitReader(file, descriptor.SizeBytes+1))
	if err != nil || written != descriptor.SizeBytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), descriptor.SHA256) {
		return nil, fmt.Errorf("plugin runtime artifact %q digest differs from snapshot", descriptor.ArtifactID)
	}
	return payload.Bytes(), nil
}

// ResolveLocalPluginGenerationArtifact returns the already-verified immutable
// package path used by the embedded Agent. Remote Agents still receive the
// same bytes through the revision artifact endpoint.
func (s *GormStore) ResolveLocalPluginGenerationArtifact(ctx context.Context, generation PluginGeneration) (string, error) {
	resolve := func(scoped *GormStore) (string, error) {
		if _, err := loadIssuedRuntimeArtifact(ctx, scoped.db, generation); err != nil {
			return "", err
		}
		var packageRow PluginPackageRow
		if err := scoped.db.WithContext(ctx).Where("identity = ?", generation.Artifact.PackageIdentity).First(&packageRow).Error; err != nil {
			return "", err
		}
		artifactPath := filepath.Join(packageRow.CachePath, filepath.FromSlash(generation.Artifact.RelativePath))
		relative, err := filepath.Rel(packageRow.CachePath, artifactPath)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("plugin runtime artifact %q path escapes verified package cache", generation.Artifact.ArtifactID)
		}
		return artifactPath, nil
	}
	if s.transactionScoped {
		return resolve(s)
	}
	var artifactPath string
	err := s.readSnapshotTransaction(ctx, func(scoped *GormStore) error {
		var err error
		artifactPath, err = resolve(scoped)
		return err
	})
	return artifactPath, err
}

// EnsureAgentHeartbeatRevision atomically issues the exact positive snapshot
// used by legacy heartbeat sync when an earlier mutation did not pass through
// revision.Executor. Concurrent identical heartbeats converge on one immutable
// row; the same agent/revision with different content is rejected.
func (s *GormStore) EnsureAgentHeartbeatRevision(ctx context.Context, agentID string, snapshot Snapshot, snapshotPayload []byte, snapshotDigest string, now time.Time) (AgentRevisionRow, error) {
	agentID = strings.TrimSpace(agentID)
	snapshotDigest = strings.ToLower(strings.TrimSpace(snapshotDigest))
	if agentID == "" || snapshot.Revision <= 0 || len(snapshotDigest) != sha256.Size*2 {
		return AgentRevisionRow{}, fmt.Errorf("heartbeat revision identity is invalid")
	}
	digest := sha256.Sum256(snapshotPayload)
	if !strings.EqualFold(snapshotDigest, hex.EncodeToString(digest[:])) {
		return AgentRevisionRow{}, fmt.Errorf("heartbeat snapshot digest does not match payload")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var issued AgentRevisionRow
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing AgentRevisionRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND revision = ?", agentID, snapshot.Revision).First(&existing).Error
		if err == nil {
			if !strings.EqualFold(existing.SnapshotDigest, snapshotDigest) {
				return fmt.Errorf("agent revision %q/%d already has a different snapshot digest", agentID, snapshot.Revision)
			}
			if err := verifyRevisionSnapshotArtifact(tx, existing); err != nil {
				return err
			}
			complete, err := verifyRevisionPolicyArtifactRefs(tx, existing, snapshot)
			if err != nil {
				return err
			}
			if !complete {
				policyArtifacts, policyRefs, err := buildAgentRevisionPolicyArtifacts(ctx, tx, agentID, snapshot.Revision, snapshot, now)
				if err != nil {
					return err
				}
				for _, artifact := range policyArtifacts {
					if err := createImmutableArtifact(tx, artifact); err != nil {
						return err
					}
				}
				if err := createRevisionArtifactRefs(tx, policyRefs); err != nil {
					return err
				}
			}
			if err := rebaseInheritedPluginAgentRuntimeStatusesTx(ctx, tx, agentID, snapshot.Revision, snapshot.PluginGenerations, now); err != nil {
				return err
			}
			if err := ensureHeartbeatRevisionPointer(tx, agentID, snapshot.Revision, now); err != nil {
				return err
			}
			issued = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		policyArtifacts, policyRefs, err := buildAgentRevisionPolicyArtifacts(ctx, tx, agentID, snapshot.Revision, snapshot, now)
		if err != nil {
			return err
		}
		snapshotArtifact := GenerationArtifactRow{ID: "snapshot-" + snapshotDigest, Kind: "agent_snapshot", SHA256: snapshotDigest, Payload: append([]byte(nil), snapshotPayload...), SizeBytes: int64(len(snapshotPayload)), CreatedAt: now}
		if err := createImmutableArtifact(tx, snapshotArtifact); err != nil {
			return err
		}
		for _, artifact := range policyArtifacts {
			if err := createImmutableArtifact(tx, artifact); err != nil {
				return err
			}
		}
		operationHash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", agentID, snapshot.Revision, snapshotDigest)))
		operationID := "heartbeat-revision-" + hex.EncodeToString(operationHash[:])
		operation := OperationRow{ID: operationID, Kind: "agent_snapshot.heartbeat_issue", Status: OperationStatusPending, PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation).Error; err != nil {
			return err
		}
		issued = AgentRevisionRow{AgentID: agentID, Revision: snapshot.Revision, OperationID: operationID, State: AgentRevisionStatePending, SnapshotArtifactID: snapshotArtifact.ID, SnapshotDigest: snapshotDigest, DesiredVersion: snapshot.DesiredVersion, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, CreatedAt: now, UpdatedAt: now}
		inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&issued)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected == 0 {
			if err := tx.Where("agent_id = ? AND revision = ?", agentID, snapshot.Revision).First(&existing).Error; err != nil {
				return err
			}
			if !strings.EqualFold(existing.SnapshotDigest, snapshotDigest) {
				return fmt.Errorf("agent revision %q/%d concurrently received a different snapshot digest", agentID, snapshot.Revision)
			}
			issued = existing
		}
		if err := ensureRevisionSnapshotArtifacts(tx, []AgentRevisionRow{issued}); err != nil {
			return err
		}
		if err := createRevisionArtifactRefs(tx, policyRefs); err != nil {
			return err
		}
		if err := rebaseInheritedPluginAgentRuntimeStatusesTx(ctx, tx, agentID, snapshot.Revision, snapshot.PluginGenerations, now); err != nil {
			return err
		}
		return ensureHeartbeatRevisionPointer(tx, agentID, snapshot.Revision, now)
	})
	return issued, err
}

func ensureHeartbeatRevisionPointer(tx *gorm.DB, agentID string, revision int64, now time.Time) error {
	var pointer AgentRevisionPointerRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ?", agentID).First(&pointer).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if pointer.DesiredRevision > revision {
		return fmt.Errorf("heartbeat snapshot revision %d is behind durable desired revision %d", revision, pointer.DesiredRevision)
	}
	pointer.AgentID, pointer.DesiredRevision, pointer.UpdatedAt = agentID, revision, now
	return upsertMonotonicRevisionPointer(tx, pointer)
}

func verifyRevisionSnapshotArtifact(tx *gorm.DB, revision AgentRevisionRow) error {
	if strings.TrimSpace(revision.SnapshotArtifactID) == "" || strings.TrimSpace(revision.SnapshotDigest) == "" {
		return fmt.Errorf("agent revision %q/%d has no immutable snapshot", revision.AgentID, revision.Revision)
	}
	var artifact GenerationArtifactRow
	if err := tx.Where("id = ?", revision.SnapshotArtifactID).First(&artifact).Error; err != nil {
		return err
	}
	if err := validateGenerationArtifact(artifact); err != nil {
		return err
	}
	if !strings.EqualFold(artifact.SHA256, revision.SnapshotDigest) {
		return fmt.Errorf("agent revision %q/%d snapshot artifact digest differs", revision.AgentID, revision.Revision)
	}
	return nil
}

func verifyRevisionPolicyArtifactRefs(tx *gorm.DB, revision AgentRevisionRow, snapshot Snapshot) (bool, error) {
	identities := make(map[string]revisionPolicyArtifactIdentity)
	for _, policy := range snapshot.PluginPolicies {
		for _, stage := range policy.Stages {
			artifactID := strings.TrimSpace(stage.ArtifactSource.ArtifactID)
			identity := revisionPolicyArtifactIdentity{Source: stage.ArtifactSource, ArtifactDigest: strings.ToLower(strings.TrimSpace(stage.ArtifactDigest)), PackageDigest: strings.ToLower(strings.TrimSpace(stage.PackageDigest)), SignerFingerprint: strings.ToLower(strings.TrimSpace(stage.SignerFingerprint))}
			if previous, found := identities[artifactID]; found {
				if previous != identity {
					return false, fmt.Errorf("policy artifact %q has conflicting revision identities", artifactID)
				}
				continue
			}
			identities[artifactID] = identity
			var ref AgentRevisionArtifactRow
			if err := tx.Where("agent_id = ? AND revision = ? AND role = ?", revision.AgentID, revision.Revision, revisionPolicyArtifactRole(artifactID)).First(&ref).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, nil
				}
				return false, err
			}
			var blob GenerationArtifactRow
			if err := tx.Where("id = ?", ref.ArtifactID).First(&blob).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return false, nil
				}
				return false, err
			}
			if blob.Kind != revisionPolicyArtifactKind || !strings.EqualFold(blob.SHA256, identity.ArtifactDigest) || blob.SizeBytes != identity.Source.SizeBytes {
				return false, fmt.Errorf("revision policy artifact %q identity is inconsistent", artifactID)
			}
			if err := validateGenerationArtifact(blob); err != nil {
				return false, err
			}
		}
	}
	for _, generation := range snapshot.PluginGenerations {
		identity := runtimeArtifactIdentity(generation)
		var ref AgentRevisionArtifactRow
		if err := tx.Where("agent_id = ? AND revision = ? AND role = ?", revision.AgentID, revision.Revision, revisionRuntimeArtifactRole(identity.ArtifactID)).First(&ref).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		var blob GenerationArtifactRow
		if err := tx.Where("id = ?", ref.ArtifactID).First(&blob).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, err
		}
		if blob.Kind != revisionRuntimeArtifactKind || !strings.EqualFold(blob.SHA256, identity.ArtifactDigest) || blob.SizeBytes != identity.SizeBytes {
			return false, fmt.Errorf("revision plugin runtime artifact %q identity is inconsistent", identity.ArtifactID)
		}
		if err := validateGenerationArtifact(blob); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ResolveAgentRevisionPolicyArtifact authorizes an artifact solely from an
// immutable issued revision and its snapshot digest, never from the live
// plugin catalog.
func (s *GormStore) ResolveAgentRevisionPolicyArtifact(ctx context.Context, agentID string, revision int64, snapshotDigest, artifactID string) (GenerationArtifactRow, bool, error) {
	agentID, snapshotDigest, artifactID = strings.TrimSpace(agentID), strings.ToLower(strings.TrimSpace(snapshotDigest)), strings.TrimSpace(artifactID)
	if agentID == "" || revision <= 0 || len(snapshotDigest) != sha256.Size*2 || artifactID == "" {
		return GenerationArtifactRow{}, false, nil
	}
	var revisionRow AgentRevisionRow
	if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ?", agentID, revision).First(&revisionRow).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GenerationArtifactRow{}, false, nil
		}
		return GenerationArtifactRow{}, false, err
	}
	if !strings.EqualFold(revisionRow.SnapshotDigest, snapshotDigest) {
		return GenerationArtifactRow{}, false, nil
	}
	var snapshotArtifact GenerationArtifactRow
	if err := s.db.WithContext(ctx).Where("id = ?", revisionRow.SnapshotArtifactID).First(&snapshotArtifact).Error; err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if err := validateGenerationArtifact(snapshotArtifact); err != nil || !strings.EqualFold(snapshotArtifact.SHA256, snapshotDigest) {
		if err != nil {
			return GenerationArtifactRow{}, false, err
		}
		return GenerationArtifactRow{}, false, fmt.Errorf("agent revision snapshot digest is inconsistent")
	}
	var snapshot Snapshot
	if err := json.Unmarshal(snapshotArtifact.Payload, &snapshot); err != nil {
		return GenerationArtifactRow{}, false, fmt.Errorf("decode agent revision snapshot: %w", err)
	}
	policyIdentity, policyFound, err := policyArtifactIdentityFromSnapshot(snapshot, artifactID)
	if err != nil {
		return GenerationArtifactRow{}, false, err
	}
	runtimeIdentity, runtimeFound, err := runtimeArtifactIdentityFromSnapshot(snapshot, artifactID)
	if err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if policyFound == runtimeFound {
		if policyFound {
			return GenerationArtifactRow{}, false, fmt.Errorf("plugin artifact %q has ambiguous revision identity", artifactID)
		}
		return GenerationArtifactRow{}, false, nil
	}
	role, kind, digest, sizeBytes := "", "", "", int64(0)
	if policyFound {
		role, kind, digest, sizeBytes = revisionPolicyArtifactRole(artifactID), revisionPolicyArtifactKind, policyIdentity.ArtifactDigest, policyIdentity.Source.SizeBytes
	} else {
		role, kind, digest, sizeBytes = revisionRuntimeArtifactRole(artifactID), revisionRuntimeArtifactKind, runtimeIdentity.ArtifactDigest, runtimeIdentity.SizeBytes
	}
	var ref AgentRevisionArtifactRow
	if err := s.db.WithContext(ctx).Where("agent_id = ? AND revision = ? AND role = ?", agentID, revision, role).First(&ref).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GenerationArtifactRow{}, false, nil
		}
		return GenerationArtifactRow{}, false, err
	}
	var blob GenerationArtifactRow
	if err := s.db.WithContext(ctx).Where("id = ?", ref.ArtifactID).First(&blob).Error; err != nil {
		return GenerationArtifactRow{}, false, err
	}
	if blob.Kind != kind || !strings.EqualFold(blob.SHA256, digest) || blob.SizeBytes != sizeBytes {
		return GenerationArtifactRow{}, false, fmt.Errorf("revision plugin artifact identity is inconsistent")
	}
	if err := validateGenerationArtifact(blob); err != nil {
		return GenerationArtifactRow{}, false, err
	}
	return blob, true, nil
}

func policyArtifactIdentityFromSnapshot(snapshot Snapshot, artifactID string) (revisionPolicyArtifactIdentity, bool, error) {
	var matched revisionPolicyArtifactIdentity
	found := false
	for _, policy := range snapshot.PluginPolicies {
		for _, stage := range policy.Stages {
			if stage.ArtifactSource.ArtifactID != artifactID {
				continue
			}
			identity := revisionPolicyArtifactIdentity{Source: stage.ArtifactSource, ArtifactDigest: strings.ToLower(strings.TrimSpace(stage.ArtifactDigest)), PackageDigest: strings.ToLower(strings.TrimSpace(stage.PackageDigest)), SignerFingerprint: strings.ToLower(strings.TrimSpace(stage.SignerFingerprint))}
			if found && matched != identity {
				return revisionPolicyArtifactIdentity{}, false, fmt.Errorf("policy artifact %q has conflicting revision identities", artifactID)
			}
			matched, found = identity, true
		}
	}
	return matched, found, nil
}

func runtimeArtifactIdentity(generation PluginGeneration) revisionRuntimeArtifactIdentity {
	return revisionRuntimeArtifactIdentity{
		ArtifactID: strings.TrimSpace(generation.Artifact.ArtifactID), PackageIdentity: strings.TrimSpace(generation.Artifact.PackageIdentity),
		PackageDigest: strings.ToLower(strings.TrimSpace(generation.PackageDigest)), RelativePath: generation.Artifact.RelativePath,
		ArtifactDigest: strings.ToLower(strings.TrimSpace(generation.Artifact.SHA256)), SizeBytes: generation.Artifact.SizeBytes,
		SignerKeyID: generation.Artifact.SignerKeyID, SignerFingerprint: strings.ToLower(strings.TrimSpace(generation.Artifact.SignerFingerprint)),
	}
}

func runtimeArtifactIdentityFromSnapshot(snapshot Snapshot, artifactID string) (revisionRuntimeArtifactIdentity, bool, error) {
	var matched revisionRuntimeArtifactIdentity
	found := false
	for _, generation := range snapshot.PluginGenerations {
		if generation.Artifact.ArtifactID != artifactID {
			continue
		}
		identity := runtimeArtifactIdentity(generation)
		if found && matched != identity {
			return revisionRuntimeArtifactIdentity{}, false, fmt.Errorf("plugin runtime artifact %q has conflicting revision identities", artifactID)
		}
		matched, found = identity, true
	}
	return matched, found, nil
}

func revisionPolicyArtifactBlobID(digest string) string {
	return "plugin-policy-wasm-" + strings.ToLower(strings.TrimSpace(digest))
}
func revisionPolicyArtifactRole(artifactID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(artifactID)))
	return revisionPolicyArtifactRolePrefix + hex.EncodeToString(digest[:])
}

func revisionRuntimeArtifactBlobID(digest string) string {
	return "plugin-runtime-" + strings.ToLower(strings.TrimSpace(digest))
}

func revisionRuntimeArtifactRole(artifactID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(artifactID)))
	return revisionRuntimeArtifactRolePrefix + hex.EncodeToString(digest[:])
}

type RevisionRetentionPolicy struct {
	Now             time.Time
	MaxAge          time.Duration
	OperationMaxAge time.Duration
	AuditMaxAge     time.Duration
	MaxPerAgent     int
}

type RevisionPruneResult struct {
	RevisionsDeleted               int64
	OperationsDeleted              int64
	ArtifactsDeleted               int64
	IdempotencyRecordsDeleted      int64
	SessionsDeleted                int64
	MarketplaceOperationsDeleted   int64
	PluginOperationsDeleted        int64
	PluginRuntimeStatusesDeleted   int64
	PluginRuntimeLogsDeleted       int64
	PluginRuntimeLogReportsDeleted int64
	PluginLogOutboxDeleted         int64
	SecretVersionsDeleted          int64
	SecretsDeleted                 int64
	PluginDigestFencesDeleted      int64
	AuditEventsDeleted             int64
}

type RevisionEventQuery struct {
	AfterID     uint64
	Limit       int
	OperationID string
	AgentID     string
}

type CoordinatorRuntimeSnapshot struct {
	Revision            AgentRevisionRow
	Artifact            GenerationArtifactRow
	Snapshot            Snapshot
	Normalized          bool
	RequiresNewRevision bool
}

func (s *GormStore) CreateRevisionLedger(ctx context.Context, input RevisionLedgerWrite) error {
	if strings.TrimSpace(input.Operation.ID) == "" {
		return fmt.Errorf("operation id is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&input.Operation).Error; err != nil {
			return err
		}
		for i := range input.Artifacts {
			if err := validateGenerationArtifact(input.Artifacts[i]); err != nil {
				return err
			}
			if err := createImmutableArtifact(tx, input.Artifacts[i]); err != nil {
				return err
			}
		}
		for i := range input.Revisions {
			row := input.Revisions[i]
			if strings.TrimSpace(row.AgentID) == "" || row.Revision < 0 {
				return fmt.Errorf("agent revision identity is invalid")
			}
			if strings.TrimSpace(row.OperationID) == "" {
				row.OperationID = input.Operation.ID
			}
			if row.OperationID != input.Operation.ID {
				return fmt.Errorf("agent revision operation %q does not match %q", row.OperationID, input.Operation.ID)
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		for i := range input.Pointers {
			if err := upsertMonotonicRevisionPointer(tx, input.Pointers[i]); err != nil {
				return err
			}
		}
		if len(input.Attempts) > 0 {
			if err := tx.Create(&input.Attempts).Error; err != nil {
				return err
			}
		}
		if len(input.Generations) > 0 {
			if err := tx.Create(&input.Generations).Error; err != nil {
				return err
			}
		}
		if len(input.Events) > 0 {
			if err := tx.Create(&input.Events).Error; err != nil {
				return err
			}
		}
		if err := ensureRevisionSnapshotArtifacts(tx, input.Revisions); err != nil {
			return err
		}
		if err := createRevisionArtifactRefs(tx, input.ArtifactRefs); err != nil {
			return err
		}
		if len(input.IdempotencyRecords) > 0 {
			if err := tx.Create(&input.IdempotencyRecords).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) GetOperation(ctx context.Context, operationID string) (OperationRow, bool, error) {
	var row OperationRow
	err := s.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(operationID)).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return OperationRow{}, false, nil
	}
	return OperationRow{}, false, err
}

func (s *GormStore) DismissOperation(ctx context.Context, operationID string, now time.Time) (OperationRow, bool, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return OperationRow{}, false, fmt.Errorf("operation id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var operation OperationRow
	found := false
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", operationID).First(&operation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		if operation.CompletedAt != nil {
			return nil
		}
		if err := tx.Model(&OperationRow{}).Where("id = ?", operationID).Updates(map[string]any{
			"completed_at": now,
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}
		operation.CompletedAt = &now
		operation.UpdatedAt = now
		return nil
	})
	return operation, found, err
}

func (s *GormStore) GetAgentRevisionPointer(ctx context.Context, agentID string) (AgentRevisionPointerRow, bool, error) {
	var row AgentRevisionPointerRow
	err := s.db.WithContext(ctx).Where("agent_id = ?", strings.TrimSpace(agentID)).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return AgentRevisionPointerRow{}, false, nil
	}
	return AgentRevisionPointerRow{}, false, err
}

func (s *GormStore) GetAgentReportedRevision(ctx context.Context, agentID string) (int64, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID != "" && agentID == strings.TrimSpace(s.localAgentID) {
		// Embedded runtime state is stored in local_agent_state, not agents.
		// Treat it as unreported so remote runtime-loss repair never targets it.
		return 0, false, nil
	}
	var row AgentRow
	err := s.db.WithContext(ctx).
		Select("id", "current_revision", "is_local").
		Where("id = ?", agentID).
		First(&row).Error
	if err == nil {
		// The embedded worker may pull again immediately after reporting applied,
		// before its next heartbeat updates AgentRow. Runtime-loss repair is a
		// remote heartbeat contract; applying it here would create a repair loop.
		if row.IsLocal {
			return 0, false, nil
		}
		return maxRevisionInt64(int64(row.CurrentRevision), 0), true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	return 0, false, err
}

func (s *GormStore) ListAgentRevisions(ctx context.Context, agentID string) ([]AgentRevisionRow, error) {
	var rows []AgentRevisionRow
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", strings.TrimSpace(agentID)).
		Order("revision").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) ListCoordinatorGenerations(ctx context.Context, agentID string) ([]AgentGenerationRow, error) {
	var rows []AgentGenerationRow
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", strings.TrimSpace(agentID)).
		Order("created_at, generation_id").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) ListRevisionEvents(ctx context.Context, query RevisionEventQuery) ([]RevisionEventRow, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	db := s.db.WithContext(ctx).Where("id > ?", query.AfterID)
	if operationID := strings.TrimSpace(query.OperationID); operationID != "" {
		db = db.Where("operation_id = ?", operationID)
	}
	if agentID := strings.TrimSpace(query.AgentID); agentID != "" {
		db = db.Where("agent_id = ?", agentID)
	}
	var rows []RevisionEventRow
	if err := db.Order("id").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *GormStore) UpdateIdempotencyResponseJSON(ctx context.Context, scope, key, operationID, responseJSON string) (bool, error) {
	scope = strings.TrimSpace(scope)
	key = strings.TrimSpace(key)
	operationID = strings.TrimSpace(operationID)
	if scope == "" || key == "" || operationID == "" || strings.TrimSpace(responseJSON) == "" {
		return false, fmt.Errorf("idempotency response identity and payload are required")
	}
	result := s.db.WithContext(ctx).Model(&IdempotencyRecordRow{}).
		Where("scope = ? AND key = ? AND operation_id = ?", scope, key, operationID).
		Update("response_json", responseJSON)
	return result.RowsAffected == 1, result.Error
}

func (s *GormStore) GetGenerationArtifact(ctx context.Context, artifactID string) (GenerationArtifactRow, bool, error) {
	var row GenerationArtifactRow
	err := s.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(artifactID)).First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return GenerationArtifactRow{}, false, nil
	}
	return GenerationArtifactRow{}, false, err
}

func (s *GormStore) LoadCoordinatorRuntimeSnapshot(ctx context.Context, agentID string, revision int64) (CoordinatorRuntimeSnapshot, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || revision < 0 {
		return CoordinatorRuntimeSnapshot{}, false, fmt.Errorf("agent revision identity is invalid")
	}

	var result CoordinatorRuntimeSnapshot
	found := false
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var target AgentRevisionRow
		err := tx.
			Where("agent_id = ? AND revision = ?", agentID, revision).
			First(&target).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true

		var revisionRows []AgentRevisionRow
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		if operationID := strings.TrimSpace(target.OperationID); operationID != "" {
			query = query.Where("operation_id = ?", operationID)
		} else {
			query = query.Where("agent_id = ? AND revision = ?", agentID, revision)
		}
		if err := query.Order("agent_id, revision").Find(&revisionRows).Error; err != nil {
			return err
		}

		states := make([]coordinatorRuntimeSnapshotState, 0, len(revisionRows))
		snapshots := make([]Snapshot, 0, len(revisionRows))
		targetIndex := -1
		for _, revisionRow := range revisionRows {
			isTarget := revisionRow.AgentID == agentID && revisionRow.Revision == revision
			// Migration baselines retain the applied revision even when its historical payload was unavailable.
			if !isTarget && revisionRow.LegacyBaseline &&
				strings.TrimSpace(revisionRow.SnapshotArtifactID) == "" &&
				strings.TrimSpace(revisionRow.SnapshotDigest) == "" {
				continue
			}
			state, err := loadCoordinatorRuntimeSnapshotState(tx, revisionRow)
			if err != nil {
				return err
			}
			if isTarget {
				targetIndex = len(states)
			}
			states = append(states, state)
			snapshots = append(snapshots, state.snapshot)
		}
		if targetIndex < 0 {
			return fmt.Errorf("agent revision %q/%d changed concurrently", agentID, revision)
		}

		filteredSnapshots, operationNormalized := FilterSupportedSnapshotResourceGraph(snapshots)
		requiresNewRevision := false
		for i := range states {
			payload, err := json.Marshal(filteredSnapshots[i])
			if err != nil {
				return fmt.Errorf("encode supported revision snapshot: %w", err)
			}
			states[i].snapshot = filteredSnapshots[i]
			if bytes.Equal(payload, states[i].artifact.Payload) {
				continue
			}
			operationNormalized = true
			mutable, err := coordinatorRuntimeSnapshotIdentityIsMutable(tx, states[i].revision)
			if err != nil {
				return err
			}
			if !mutable {
				requiresNewRevision = true
				continue
			}
			digestBytes := sha256.Sum256(payload)
			digest := hex.EncodeToString(digestBytes[:])
			replacement, err := loadOrCreateNormalizedSnapshotArtifact(tx, states[i].artifact.Kind, payload, digest)
			if err != nil {
				return err
			}
			if err := replaceCoordinatorRuntimeSnapshot(tx, &states[i], replacement); err != nil {
				return err
			}
		}

		targetState := states[targetIndex]
		result = CoordinatorRuntimeSnapshot{
			Revision: targetState.revision, Artifact: targetState.artifact,
			Snapshot: targetState.snapshot, Normalized: operationNormalized,
			RequiresNewRevision: requiresNewRevision,
		}
		return nil
	})
	return result, found, err
}

func coordinatorRuntimeSnapshotIdentityIsMutable(tx *gorm.DB, revision AgentRevisionRow) (bool, error) {
	if revision.State != AgentRevisionStatePending || revision.AttemptCount != 0 || strings.TrimSpace(revision.GenerationID) != "" {
		return false, nil
	}
	var attemptCount int64
	if err := tx.Model(&AgentRevisionAttemptRow{}).
		Where("agent_id = ? AND revision = ?", revision.AgentID, revision.Revision).
		Count(&attemptCount).Error; err != nil {
		return false, err
	}
	if attemptCount != 0 {
		return false, nil
	}
	var generationCount int64
	if err := tx.Model(&AgentGenerationRow{}).
		Where("agent_id = ? AND revision = ?", revision.AgentID, revision.Revision).
		Count(&generationCount).Error; err != nil {
		return false, err
	}
	return generationCount == 0, nil
}

type coordinatorRuntimeSnapshotState struct {
	revision AgentRevisionRow
	artifact GenerationArtifactRow
	snapshot Snapshot
}

func loadCoordinatorRuntimeSnapshotState(tx *gorm.DB, revisionRow AgentRevisionRow) (coordinatorRuntimeSnapshotState, error) {
	agentID := strings.TrimSpace(revisionRow.AgentID)
	artifactID := strings.TrimSpace(revisionRow.SnapshotArtifactID)
	if artifactID == "" || strings.TrimSpace(revisionRow.SnapshotDigest) == "" {
		return coordinatorRuntimeSnapshotState{}, fmt.Errorf("agent revision %q/%d has no complete snapshot identity", agentID, revisionRow.Revision)
	}
	var artifact GenerationArtifactRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", artifactID).First(&artifact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return coordinatorRuntimeSnapshotState{}, fmt.Errorf("agent revision %q/%d snapshot artifact %q does not exist", agentID, revisionRow.Revision, artifactID)
		}
		return coordinatorRuntimeSnapshotState{}, err
	}
	if err := validateGenerationArtifact(artifact); err != nil {
		return coordinatorRuntimeSnapshotState{}, err
	}
	if !strings.EqualFold(revisionRow.SnapshotDigest, artifact.SHA256) {
		return coordinatorRuntimeSnapshotState{}, fmt.Errorf("agent revision %q/%d snapshot digest does not match artifact %q", agentID, revisionRow.Revision, artifactID)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
		return coordinatorRuntimeSnapshotState{}, fmt.Errorf("decode revision snapshot: %w", err)
	}
	if snapshot.Revision != revisionRow.Revision {
		return coordinatorRuntimeSnapshotState{}, fmt.Errorf("snapshot revision %d does not match desired revision %d", snapshot.Revision, revisionRow.Revision)
	}
	return coordinatorRuntimeSnapshotState{revision: revisionRow, artifact: artifact, snapshot: snapshot}, nil
}

func replaceCoordinatorRuntimeSnapshot(tx *gorm.DB, state *coordinatorRuntimeSnapshotState, replacement GenerationArtifactRow) error {
	now := time.Now().UTC()
	updated := tx.Model(&AgentRevisionRow{}).
		Where(
			"agent_id = ? AND revision = ? AND snapshot_artifact_id = ? AND snapshot_digest = ?",
			state.revision.AgentID,
			state.revision.Revision,
			state.revision.SnapshotArtifactID,
			state.revision.SnapshotDigest,
		).
		Updates(map[string]any{
			"snapshot_artifact_id": replacement.ID,
			"snapshot_digest":      replacement.SHA256,
			"updated_at":           now,
		})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return fmt.Errorf("agent revision %q/%d snapshot changed concurrently", state.revision.AgentID, state.revision.Revision)
	}
	if err := tx.Where(
		"agent_id = ? AND revision = ? AND role = ?",
		state.revision.AgentID,
		state.revision.Revision,
		revisionSnapshotArtifactRole,
	).Delete(&AgentRevisionArtifactRow{}).Error; err != nil {
		return err
	}
	createdAt := state.revision.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	if err := tx.Create(&AgentRevisionArtifactRow{
		AgentID: state.revision.AgentID, Revision: state.revision.Revision,
		ArtifactID: replacement.ID, Role: revisionSnapshotArtifactRole, CreatedAt: createdAt,
	}).Error; err != nil {
		return err
	}

	state.revision.SnapshotArtifactID = replacement.ID
	state.revision.SnapshotDigest = replacement.SHA256
	state.revision.UpdatedAt = now
	state.artifact = replacement
	return nil
}

func loadOrCreateNormalizedSnapshotArtifact(tx *gorm.DB, kind string, payload []byte, digest string) (GenerationArtifactRow, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "agent_snapshot"
	}
	var existing GenerationArtifactRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("sha256 = ?", digest).First(&existing).Error
	if err == nil {
		if err := validateGenerationArtifact(existing); err != nil {
			return GenerationArtifactRow{}, err
		}
		if !bytes.Equal(existing.Payload, payload) {
			return GenerationArtifactRow{}, fmt.Errorf("generation artifact digest collision for %q", digest)
		}
		if strings.TrimSpace(existing.Kind) != kind {
			return GenerationArtifactRow{}, fmt.Errorf("generation artifact %q has kind %q, want %q", existing.ID, existing.Kind, kind)
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return GenerationArtifactRow{}, err
	}

	replacement := GenerationArtifactRow{
		ID:        "snapshot-" + digest,
		Kind:      kind,
		SHA256:    digest,
		Payload:   payload,
		SizeBytes: int64(len(payload)),
		CreatedAt: time.Now().UTC(),
	}
	if err := validateGenerationArtifact(replacement); err != nil {
		return GenerationArtifactRow{}, err
	}
	if err := createImmutableArtifact(tx, replacement); err != nil {
		return GenerationArtifactRow{}, err
	}
	return replacement, nil
}

func (s *GormStore) BootstrapRevisionLedger(ctx context.Context) error {
	var marker MetaRow
	err := s.db.WithContext(ctx).Where("key = ?", revisionLedgerBaselineMarkerKey).First(&marker).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	seeds, err := s.buildRevisionBaselineSeeds(ctx)
	if err != nil {
		return err
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&MetaRow{
			Key:   revisionLedgerBaselineMarkerKey,
			Value: "applied",
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		for _, seed := range seeds {
			if err := createRevisionLedgerInTransaction(tx, seed); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) PruneRevisionHistory(ctx context.Context, policy RevisionRetentionPolicy) (RevisionPruneResult, error) {
	now := policy.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	maxAge := policy.MaxAge
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	maxPerAgent := policy.MaxPerAgent
	if maxPerAgent <= 0 {
		maxPerAgent = 500
	}
	operationMaxAge := policy.OperationMaxAge
	if operationMaxAge <= 0 {
		operationMaxAge = 3 * maxAge
	}
	if operationMaxAge < maxAge {
		operationMaxAge = maxAge
	}
	auditMaxAge := policy.AuditMaxAge
	if auditMaxAge <= 0 {
		auditMaxAge = 365 * 24 * time.Hour
	}
	if auditMaxAge < operationMaxAge {
		auditMaxAge = operationMaxAge
	}
	cutoff := now.Add(-maxAge)
	operationCutoff := now.Add(-operationMaxAge)
	auditCutoff := now.Add(-auditMaxAge)
	result := RevisionPruneResult{}

	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var agentIDs []string
		if err := tx.Model(&AgentRevisionRow{}).Distinct("agent_id").Pluck("agent_id", &agentIDs).Error; err != nil {
			return err
		}
		for _, agentID := range agentIDs {
			protected, err := protectedAgentRevisions(tx, agentID)
			if err != nil {
				return err
			}
			var rows []AgentRevisionRow
			if err := tx.Where("agent_id = ?", agentID).Order("revision DESC").Find(&rows).Error; err != nil {
				return err
			}
			deleteRevisions := make([]int64, 0)
			for index, row := range rows {
				_, pinned := protected[row.Revision]
				withinCount := index < maxPerAgent
				withinAge := !row.CreatedAt.Before(cutoff)
				if !pinned && (!withinCount || !withinAge) {
					deleteRevisions = append(deleteRevisions, row.Revision)
				}
			}
			if len(deleteRevisions) == 0 {
				continue
			}
			if err := tx.Where("agent_id = ? AND revision IN ?", agentID, deleteRevisions).Delete(&AgentRevisionAttemptRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("agent_id = ? AND revision IN ?", agentID, deleteRevisions).Delete(&RevisionEventRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("agent_id = ? AND revision IN ?", agentID, deleteRevisions).Delete(&AgentRevisionArtifactRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("agent_id = ? AND revision IN ?", agentID, deleteRevisions).Delete(&AgentGenerationRow{}).Error; err != nil {
				return err
			}
			deleted := tx.Where("agent_id = ? AND revision IN ?", agentID, deleteRevisions).Delete(&AgentRevisionRow{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.RevisionsDeleted += deleted.RowsAffected
		}

		expired := tx.Where("expires_at <= ?", now).Delete(&IdempotencyRecordRow{})
		if expired.Error != nil {
			return expired.Error
		}
		result.IdempotencyRecordsDeleted = expired.RowsAffected

		deletedSessions := tx.Where("expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?)", now, cutoff).Delete(&SessionRow{})
		if deletedSessions.Error != nil {
			return deletedSessions.Error
		}
		result.SessionsDeleted = deletedSessions.RowsAffected

		deletedAuditEvents := tx.Where("created_at <= ?", auditCutoff).Delete(&AuditEventRow{})
		if deletedAuditEvents.Error != nil {
			return deletedAuditEvents.Error
		}
		result.AuditEventsDeleted = deletedAuditEvents.RowsAffected

		var expiredRefreshOperationIDs []string
		if err := tx.Model(&MarketplaceRefreshOperationRow{}).
			Where("finished_at IS NOT NULL AND finished_at <= ?", operationCutoff).
			Pluck("id", &expiredRefreshOperationIDs).Error; err != nil {
			return err
		}
		if len(expiredRefreshOperationIDs) > 0 {
			if err := tx.Where("operation_id IN ?", expiredRefreshOperationIDs).Delete(&PluginPackageStagingRow{}).Error; err != nil {
				return err
			}
			deleted := tx.Where("id IN ?", expiredRefreshOperationIDs).Delete(&MarketplaceRefreshOperationRow{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.MarketplaceOperationsDeleted = deleted.RowsAffected
		}

		deletedRuntimeStatuses := tx.
			Where("updated_at <= ? AND (authority_slot = ? OR (authority_slot = ? AND state = ?))", operationCutoff, "retired", "pending", "draining").
			Delete(&PluginAgentRuntimeStatusRow{})
		if deletedRuntimeStatuses.Error != nil {
			return deletedRuntimeStatuses.Error
		}
		result.PluginRuntimeStatusesDeleted = deletedRuntimeStatuses.RowsAffected

		deletedLogReports := tx.Where("updated_at <= ?", operationCutoff).
			Where("NOT EXISTS (SELECT 1 FROM plugin_agent_runtime_statuses WHERE plugin_agent_runtime_statuses.agent_id = plugin_runtime_log_reports.agent_id AND plugin_agent_runtime_statuses.instance_id = plugin_runtime_log_reports.instance_id AND plugin_agent_runtime_statuses.generation_id = plugin_runtime_log_reports.generation_id AND plugin_agent_runtime_statuses.authority_slot IN ?)", []string{"pending", "active"}).
			Delete(&PluginRuntimeLogReportRow{})
		if deletedLogReports.Error != nil {
			return deletedLogReports.Error
		}
		result.PluginRuntimeLogReportsDeleted = deletedLogReports.RowsAffected

		deletedRuntimeLogs := tx.Where("created_at <= ?", operationCutoff).Delete(&PluginRuntimeLogRow{})
		if deletedRuntimeLogs.Error != nil {
			return deletedRuntimeLogs.Error
		}
		result.PluginRuntimeLogsDeleted = deletedRuntimeLogs.RowsAffected

		deletedOutbox := tx.Where("created_at <= ?", operationCutoff).Delete(&PluginControlPlaneLogOutboxRow{})
		if deletedOutbox.Error != nil {
			return deletedOutbox.Error
		}
		result.PluginLogOutboxDeleted = deletedOutbox.RowsAffected

		protectedPluginOperationIDs := make([]string, 0)
		for _, query := range []struct {
			db     *gorm.DB
			column string
		}{
			{tx.Model(&InstalledPluginRow{}).Where("last_operation_id <> ?", ""), "last_operation_id"},
			{tx.Model(&InstalledPluginRow{}).Where("pending_operation_id <> ?", ""), "pending_operation_id"},
			{tx.Model(&PluginInstanceRow{}).Where("pending_operation_id <> ?", ""), "pending_operation_id"},
			{tx.Model(&PluginAgentRuntimeStatusRow{}).Where("authority_slot IN ?", []string{"pending", "active"}), "operation_id"},
		} {
			var ids []string
			if err := query.db.Pluck(query.column, &ids).Error; err != nil {
				return err
			}
			protectedPluginOperationIDs = append(protectedPluginOperationIDs, ids...)
		}
		protectedPluginOperationIDs = uniqueNonEmptyStrings(protectedPluginOperationIDs)
		pluginOperations := tx.Model(&PluginOperationRow{}).
			Where("completed_at IS NOT NULL AND completed_at <= ?", operationCutoff)
		if len(protectedPluginOperationIDs) > 0 {
			pluginOperations = pluginOperations.Where("id NOT IN ?", protectedPluginOperationIDs)
		}
		var expiredPluginOperationIDs []string
		if err := pluginOperations.Pluck("id", &expiredPluginOperationIDs).Error; err != nil {
			return err
		}
		if len(expiredPluginOperationIDs) > 0 {
			for _, model := range []any{&PluginOperationScopeRow{}, &PluginOperationSecretRow{}} {
				if err := tx.Where("operation_id IN ?", expiredPluginOperationIDs).Delete(model).Error; err != nil {
					return err
				}
			}
			deleted := tx.Where("id IN ?", expiredPluginOperationIDs).Delete(&PluginOperationRow{})
			if deleted.Error != nil {
				return deleted.Error
			}
			result.PluginOperationsDeleted = deleted.RowsAffected
		}

		deletedSecretVersions := tx.Where("destroyed_at IS NOT NULL AND destroyed_at <= ?", operationCutoff).Delete(&SecretVersionRow{})
		if deletedSecretVersions.Error != nil {
			return deletedSecretVersions.Error
		}
		result.SecretVersionsDeleted = deletedSecretVersions.RowsAffected
		deletedSecrets := tx.Where("retired_at IS NOT NULL AND retired_at <= ?", operationCutoff).
			Where("id NOT IN (?)", tx.Model(&SecretVersionRow{}).Select("secret_id")).Delete(&SecretRow{})
		if deletedSecrets.Error != nil {
			return deletedSecrets.Error
		}
		result.SecretsDeleted = deletedSecrets.RowsAffected

		deletedDigestFences := tx.Where("claim_token = ? AND updated_at <= ?", "", operationCutoff).
			Where("digest NOT IN (?)", tx.Model(&PluginPackageRow{}).Select("digest")).
			Where("digest NOT IN (?)", tx.Model(&PluginPackageAcquisitionRow{}).Select("digest")).
			Where("digest NOT IN (?)", tx.Model(&PluginPackageStagingRow{}).Select("digest")).
			Where("digest NOT IN (?)", tx.Model(&PluginCacheGCIntentRow{}).Select("digest")).
			Delete(&PluginDigestFenceRow{})
		if deletedDigestFences.Error != nil {
			return deletedDigestFences.Error
		}
		result.PluginDigestFencesDeleted = deletedDigestFences.RowsAffected

		deletedOperations := tx.
			Where("completed_at IS NOT NULL AND completed_at <= ?", operationCutoff).
			Where("NOT EXISTS (SELECT 1 FROM agent_revisions WHERE agent_revisions.operation_id = operations.id)").
			Where("NOT EXISTS (SELECT 1 FROM revision_events WHERE revision_events.operation_id = operations.id)").
			Where("NOT EXISTS (SELECT 1 FROM idempotency_records WHERE idempotency_records.operation_id = operations.id)").
			Delete(&OperationRow{})
		if deletedOperations.Error != nil {
			return deletedOperations.Error
		}
		result.OperationsDeleted = deletedOperations.RowsAffected

		var explicitArtifactIDs []string
		if err := tx.Model(&AgentRevisionArtifactRow{}).Distinct("artifact_id").Pluck("artifact_id", &explicitArtifactIDs).Error; err != nil {
			return err
		}
		var snapshotArtifactIDs []string
		if err := tx.Model(&AgentRevisionRow{}).
			Where("snapshot_artifact_id <> ?", "").
			Distinct("snapshot_artifact_id").
			Pluck("snapshot_artifact_id", &snapshotArtifactIDs).Error; err != nil {
			return err
		}
		referencedArtifactIDs := uniqueNonEmptyStrings(explicitArtifactIDs, snapshotArtifactIDs)
		artifacts := tx.Model(&GenerationArtifactRow{})
		if len(referencedArtifactIDs) > 0 {
			artifacts = artifacts.Where("id NOT IN ?", referencedArtifactIDs)
		} else {
			artifacts = artifacts.Where("1 = 1")
		}
		deletedArtifacts := artifacts.Delete(&GenerationArtifactRow{})
		if deletedArtifacts.Error != nil {
			return deletedArtifacts.Error
		}
		result.ArtifactsDeleted = deletedArtifacts.RowsAffected
		return nil
	})
	return result, err
}

func validateGenerationArtifact(row GenerationArtifactRow) error {
	if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.SHA256) == "" {
		return fmt.Errorf("generation artifact identity is required")
	}
	digest := sha256.Sum256(row.Payload)
	if !strings.EqualFold(row.SHA256, hex.EncodeToString(digest[:])) {
		return fmt.Errorf("generation artifact %q sha256 does not match payload", row.ID)
	}
	if row.SizeBytes != int64(len(row.Payload)) {
		return fmt.Errorf("generation artifact %q size does not match payload", row.ID)
	}
	return nil
}

func createImmutableArtifact(tx *gorm.DB, row GenerationArtifactRow) error {
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var existing GenerationArtifactRow
	if err := tx.Where("id = ?", row.ID).First(&existing).Error; err != nil {
		return err
	}
	if existing.SHA256 != row.SHA256 || existing.SizeBytes != row.SizeBytes {
		return fmt.Errorf("generation artifact %q is immutable", row.ID)
	}
	return nil
}

func upsertMonotonicRevisionPointer(tx *gorm.DB, row AgentRevisionPointerRow) error {
	row.AgentID = strings.TrimSpace(row.AgentID)
	if row.AgentID == "" {
		return fmt.Errorf("agent revision pointer agent id is required")
	}
	if row.DesiredRevision < 0 || row.AppliedRevision < 0 || row.LastKnownGoodRevision < 0 {
		return fmt.Errorf("agent revision pointer for %q has a negative revision", row.AgentID)
	}

	inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if inserted.Error != nil {
		return inserted.Error
	}

	for attempt := 0; attempt < 2; attempt++ {
		updated := tx.Model(&AgentRevisionPointerRow{}).
			Where(
				"agent_id = ? AND desired_revision <= ? AND applied_revision <= ? AND last_known_good_revision <= ?",
				row.AgentID,
				row.DesiredRevision,
				row.AppliedRevision,
				row.LastKnownGoodRevision,
			).
			Updates(map[string]any{
				"desired_revision":         row.DesiredRevision,
				"applied_revision":         row.AppliedRevision,
				"last_known_good_revision": row.LastKnownGoodRevision,
				"updated_at": gorm.Expr(
					"CASE WHEN updated_at < ? THEN ? ELSE updated_at END",
					row.UpdatedAt,
					row.UpdatedAt,
				),
			})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected > 0 {
			return nil
		}

		var current AgentRevisionPointerRow
		if err := tx.Where("agent_id = ?", row.AgentID).First(&current).Error; err != nil {
			return err
		}
		if current.DesiredRevision > row.DesiredRevision ||
			current.AppliedRevision > row.AppliedRevision ||
			current.LastKnownGoodRevision > row.LastKnownGoodRevision {
			return fmt.Errorf(
				"agent revision pointer for %q is stale: current=(%d,%d,%d) incoming=(%d,%d,%d)",
				row.AgentID,
				current.DesiredRevision,
				current.AppliedRevision,
				current.LastKnownGoodRevision,
				row.DesiredRevision,
				row.AppliedRevision,
				row.LastKnownGoodRevision,
			)
		}
		if current.DesiredRevision == row.DesiredRevision &&
			current.AppliedRevision == row.AppliedRevision &&
			current.LastKnownGoodRevision == row.LastKnownGoodRevision {
			return nil
		}
	}

	return fmt.Errorf("agent revision pointer for %q could not be advanced atomically", row.AgentID)
}

func ensureRevisionSnapshotArtifacts(tx *gorm.DB, revisions []AgentRevisionRow) error {
	for _, revision := range revisions {
		artifactID := strings.TrimSpace(revision.SnapshotArtifactID)
		digest := strings.TrimSpace(revision.SnapshotDigest)
		if artifactID == "" {
			if digest != "" {
				return fmt.Errorf("agent revision %q/%d has a snapshot digest without an artifact", revision.AgentID, revision.Revision)
			}
			continue
		}
		if digest == "" {
			return fmt.Errorf("agent revision %q/%d snapshot digest is required", revision.AgentID, revision.Revision)
		}

		var artifact GenerationArtifactRow
		if err := tx.Where("id = ?", artifactID).First(&artifact).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("agent revision %q/%d snapshot artifact %q does not exist", revision.AgentID, revision.Revision, artifactID)
			}
			return err
		}
		if err := validateGenerationArtifact(artifact); err != nil {
			return err
		}
		if !strings.EqualFold(digest, artifact.SHA256) {
			return fmt.Errorf("agent revision %q/%d snapshot digest does not match artifact %q", revision.AgentID, revision.Revision, artifactID)
		}

		createdAt := revision.CreatedAt
		if createdAt.IsZero() {
			createdAt = artifact.CreatedAt
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentRevisionArtifactRow{
			AgentID:    strings.TrimSpace(revision.AgentID),
			Revision:   revision.Revision,
			ArtifactID: artifactID,
			Role:       revisionSnapshotArtifactRole,
			CreatedAt:  createdAt,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func createRevisionArtifactRefs(tx *gorm.DB, refs []AgentRevisionArtifactRow) error {
	for _, ref := range refs {
		ref.AgentID = strings.TrimSpace(ref.AgentID)
		ref.ArtifactID = strings.TrimSpace(ref.ArtifactID)
		ref.Role = strings.TrimSpace(ref.Role)
		if ref.AgentID == "" || ref.Revision < 0 || ref.ArtifactID == "" || ref.Role == "" {
			return fmt.Errorf("agent revision artifact reference identity is invalid")
		}
		var revision AgentRevisionRow
		if err := tx.
			Where("agent_id = ? AND revision = ?", ref.AgentID, ref.Revision).
			First(&revision).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("agent revision artifact reference %q/%d has no revision", ref.AgentID, ref.Revision)
			}
			return err
		}
		if ref.Role == revisionSnapshotArtifactRole && strings.TrimSpace(revision.SnapshotArtifactID) != ref.ArtifactID {
			return fmt.Errorf(
				"agent revision artifact reference %q/%d snapshot %q does not match canonical artifact %q",
				ref.AgentID,
				ref.Revision,
				ref.ArtifactID,
				revision.SnapshotArtifactID,
			)
		}
		var artifactCount int64
		if err := tx.Model(&GenerationArtifactRow{}).Where("id = ?", ref.ArtifactID).Count(&artifactCount).Error; err != nil {
			return err
		}
		if artifactCount == 0 {
			return fmt.Errorf("agent revision artifact reference %q/%d has no artifact %q", ref.AgentID, ref.Revision, ref.ArtifactID)
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ref).Error; err != nil {
			return err
		}
	}
	return nil
}

func uniqueNonEmptyStrings(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func (s *GormStore) buildRevisionBaselineSeeds(ctx context.Context) ([]RevisionLedgerWrite, error) {
	type target struct {
		agentID        string
		desiredVersion string
		desired        int64
		current        int64
		platform       string
		local          bool
	}
	localAgentID := strings.TrimSpace(s.localAgentID)
	if localAgentID == "" {
		localAgentID = "local"
	}
	localState, err := s.LoadLocalAgentState(ctx)
	if err != nil {
		return nil, err
	}
	targets := []target{{
		agentID:        localAgentID,
		desiredVersion: localState.DesiredVersion,
		desired:        int64(localState.DesiredRevision),
		current:        int64(localState.CurrentRevision),
		platform:       "",
		local:          true,
	}}
	agents, err := s.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{localAgentID: {}}
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.ID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		targets = append(targets, target{
			agentID:        agentID,
			desiredVersion: agent.DesiredVersion,
			desired:        int64(agent.DesiredRevision),
			current:        int64(agent.CurrentRevision),
			platform:       agent.Platform,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].agentID < targets[j].agentID })

	now := time.Now().UTC()
	seeds := make([]RevisionLedgerWrite, 0, len(targets))
	for _, current := range targets {
		var snapshot Snapshot
		if current.local {
			snapshot, err = s.LoadLocalSnapshot(ctx, current.agentID)
		} else {
			snapshot, err = s.LoadAgentSnapshot(ctx, current.agentID, AgentSnapshotInput{
				DesiredVersion:  current.desiredVersion,
				DesiredRevision: int(current.desired),
				CurrentRevision: int(current.current),
				Platform:        current.platform,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("build revision baseline for agent %q: %w", current.agentID, err)
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return nil, fmt.Errorf("marshal revision baseline for agent %q: %w", current.agentID, err)
		}
		digest := sha256.Sum256(payload)
		digestText := hex.EncodeToString(digest[:])
		artifactID := "snapshot-" + digestText
		desired := current.desired
		if snapshot.Revision > desired {
			desired = snapshot.Revision
		}
		if current.current > desired {
			desired = current.current
		}
		operationID := revisionBaselineOperationID(current.agentID)
		operationStatus := OperationStatusApplied
		var completedAt *time.Time
		if desired > current.current {
			operationStatus = OperationStatusPending
		} else {
			completed := now
			completedAt = &completed
		}
		seed := RevisionLedgerWrite{
			Operation: OperationRow{
				ID:                 operationID,
				Kind:               "migration_baseline",
				Status:             operationStatus,
				PrimaryAgentID:     current.agentID,
				RequestFingerprint: digestText,
				CreatedAt:          now,
				UpdatedAt:          now,
				CompletedAt:        completedAt,
			},
			Pointers: []AgentRevisionPointerRow{{
				AgentID:               current.agentID,
				DesiredRevision:       desired,
				AppliedRevision:       current.current,
				LastKnownGoodRevision: current.current,
				UpdatedAt:             now,
			}},
			Artifacts: []GenerationArtifactRow{{
				ID:        artifactID,
				Kind:      "agent_snapshot",
				SHA256:    digestText,
				Payload:   payload,
				SizeBytes: int64(len(payload)),
				CreatedAt: now,
			}},
		}
		applied := AgentRevisionRow{
			AgentID:             current.agentID,
			Revision:            current.current,
			OperationID:         operationID,
			State:               AgentRevisionStateApplied,
			DesiredVersion:      current.desiredVersion,
			ApplyTimeoutSeconds: 60,
			DrainTimeoutSeconds: 600,
			LegacyBaseline:      true,
			CreatedAt:           now,
			UpdatedAt:           now,
			AppliedAt:           timePointer(now),
		}
		if desired == current.current {
			applied.SnapshotArtifactID = artifactID
			applied.SnapshotDigest = digestText
			seed.ArtifactRefs = append(seed.ArtifactRefs, AgentRevisionArtifactRow{
				AgentID:    current.agentID,
				Revision:   current.current,
				ArtifactID: artifactID,
				Role:       "snapshot",
				CreatedAt:  now,
			})
		}
		seed.Revisions = append(seed.Revisions, applied)
		if desired > current.current {
			seed.Revisions = append(seed.Revisions, AgentRevisionRow{
				AgentID:             current.agentID,
				Revision:            desired,
				OperationID:         operationID,
				State:               AgentRevisionStatePending,
				SnapshotArtifactID:  artifactID,
				SnapshotDigest:      digestText,
				DesiredVersion:      current.desiredVersion,
				ApplyTimeoutSeconds: 60,
				DrainTimeoutSeconds: 600,
				CreatedAt:           now,
				UpdatedAt:           now,
			})
			seed.ArtifactRefs = append(seed.ArtifactRefs, AgentRevisionArtifactRow{
				AgentID:    current.agentID,
				Revision:   desired,
				ArtifactID: artifactID,
				Role:       "snapshot",
				CreatedAt:  now,
			})
		}
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

func createRevisionLedgerInTransaction(tx *gorm.DB, input RevisionLedgerWrite) error {
	if err := tx.Create(&input.Operation).Error; err != nil {
		return err
	}
	for _, artifact := range input.Artifacts {
		if err := validateGenerationArtifact(artifact); err != nil {
			return err
		}
		if err := createImmutableArtifact(tx, artifact); err != nil {
			return err
		}
	}
	if len(input.Revisions) > 0 {
		if err := tx.Create(&input.Revisions).Error; err != nil {
			return err
		}
	}
	for _, pointer := range input.Pointers {
		if err := upsertMonotonicRevisionPointer(tx, pointer); err != nil {
			return err
		}
	}
	if err := ensureRevisionSnapshotArtifacts(tx, input.Revisions); err != nil {
		return err
	}
	if err := createRevisionArtifactRefs(tx, input.ArtifactRefs); err != nil {
		return err
	}
	return nil
}

func revisionBaselineOperationID(agentID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(agentID)))
	return "migration-baseline-" + hex.EncodeToString(digest[:8])
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func protectedAgentRevisions(tx *gorm.DB, agentID string) (map[int64]struct{}, error) {
	protected := map[int64]struct{}{}
	var pointer AgentRevisionPointerRow
	err := tx.Where("agent_id = ?", agentID).First(&pointer).Error
	if err == nil {
		protected[pointer.DesiredRevision] = struct{}{}
		protected[pointer.AppliedRevision] = struct{}{}
		protected[pointer.LastKnownGoodRevision] = struct{}{}
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	var generationRevisions []int64
	if err := tx.Model(&AgentGenerationRow{}).
		Where("agent_id = ? AND state IN ?", agentID, []string{GenerationStateActive, GenerationStateDraining}).
		Pluck("revision", &generationRevisions).Error; err != nil {
		return nil, err
	}
	for _, revision := range generationRevisions {
		protected[revision] = struct{}{}
	}
	return protected, nil
}
