package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	revisionLedgerBaselineMarkerKey = "migration.agent_revision_ledger_baseline.v1"
	revisionSnapshotArtifactRole    = "snapshot"

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

type RevisionRetentionPolicy struct {
	Now             time.Time
	MaxAge          time.Duration
	OperationMaxAge time.Duration
	MaxPerAgent     int
}

type RevisionPruneResult struct {
	RevisionsDeleted          int64
	OperationsDeleted         int64
	ArtifactsDeleted          int64
	IdempotencyRecordsDeleted int64
}

type RevisionEventQuery struct {
	AfterID     uint64
	Limit       int
	OperationID string
	AgentID     string
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
	cutoff := now.Add(-maxAge)
	operationCutoff := now.Add(-operationMaxAge)
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
