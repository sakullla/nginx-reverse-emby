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
	Now         time.Time
	MaxAge      time.Duration
	MaxPerAgent int
}

type RevisionPruneResult struct {
	RevisionsDeleted          int64
	ArtifactsDeleted          int64
	IdempotencyRecordsDeleted int64
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
			row := input.Pointers[i]
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "agent_id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"desired_revision",
					"applied_revision",
					"last_known_good_revision",
					"updated_at",
				}),
			}).Create(&row).Error; err != nil {
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
		if len(input.ArtifactRefs) > 0 {
			if err := tx.Create(&input.ArtifactRefs).Error; err != nil {
				return err
			}
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
	cutoff := now.Add(-maxAge)
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

		var referencedArtifactIDs []string
		if err := tx.Model(&AgentRevisionArtifactRow{}).Distinct("artifact_id").Pluck("artifact_id", &referencedArtifactIDs).Error; err != nil {
			return err
		}
		artifacts := tx.Model(&GenerationArtifactRow{})
		if len(referencedArtifactIDs) > 0 {
			artifacts = artifacts.Where("id NOT IN ?", referencedArtifactIDs)
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
	if len(input.Pointers) > 0 {
		if err := tx.Create(&input.Pointers).Error; err != nil {
			return err
		}
	}
	if len(input.ArtifactRefs) > 0 {
		if err := tx.Create(&input.ArtifactRefs).Error; err != nil {
			return err
		}
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
