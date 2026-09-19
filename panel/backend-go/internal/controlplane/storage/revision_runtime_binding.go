package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CoordinatorRuntimeBindingRequest struct {
	Lease               CoordinatorLease
	GenerationID        string
	RuntimeGenerationID string
	RuntimeSnapshotHash string
	Now                 time.Time
}

func validateCoordinatorRuntimeIdentity(revision int64, generation, hash string, optional bool) error {
	if optional && generation == "" && hash == "" {
		return nil
	}
	if !validSHA256(hash) || hash != strings.ToLower(hash) || generation != fmt.Sprintf("generation-%d-%s", revision, hash[:16]) {
		return coordinatorStateConflict("actual runtime generation identity is invalid")
	}
	return nil
}

func bindCoordinatorRuntimeIdentityTx(tx *gorm.DB, revision *AgentRevisionRow, generation, hash string) error {
	if revision.RuntimeGenerationID != "" || revision.RuntimeSnapshotHash != "" {
		if revision.RuntimeGenerationID != generation || revision.RuntimeSnapshotHash != hash {
			return coordinatorStateConflict("actual runtime generation is already bound")
		}
		return nil
	}
	result := tx.Model(&AgentRevisionRow{}).Where("agent_id = ? AND revision = ? AND runtime_generation_id = '' AND runtime_snapshot_hash = ''", revision.AgentID, revision.Revision).
		Updates(map[string]any{"runtime_generation_id": generation, "runtime_snapshot_hash": hash})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return coordinatorStateConflict("actual runtime generation binding changed")
	}
	revision.RuntimeGenerationID, revision.RuntimeSnapshotHash = generation, hash
	return nil
}

// BindAgentRevisionRuntime is invoked by the trusted embedded candidate
// boundary after final snapshot projection and before preparing any plugin.
// It never accepts a naked plugin claim: the exact current started lease and
// its independent attempt identity must still hold in this transaction.
func (s *GormStore) BindAgentRevisionRuntime(ctx context.Context, request CoordinatorRuntimeBindingRequest) error {
	if err := validateCoordinatorLease(request.Lease); err != nil {
		return err
	}
	if err := validateCoordinatorRuntimeIdentity(request.Lease.Revision, request.RuntimeGenerationID, request.RuntimeSnapshotHash, false); err != nil {
		return err
	}
	request.Now = coordinatorTime(request.Now)
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.Lease.AgentID)
		if err != nil {
			return err
		}
		attempt, err := lockCoordinatorAttempt(tx, request.Lease)
		if err != nil {
			return err
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("agent_id = ? AND revision = ?", request.Lease.AgentID, request.Lease.Revision).First(&revision).Error; err != nil {
			return err
		}
		if attempt.LeaseID != request.Lease.LeaseID || attempt.State != AgentRevisionAttemptStateStarted || !request.Now.Before(attempt.DeadlineAt) ||
			revision.State != AgentRevisionStateApplying || revision.RetryCycle != request.Lease.RetryCycle || revision.AttemptCount != request.Lease.Attempt ||
			revision.GenerationID != request.GenerationID || pointer.DesiredRevision > revision.Revision {
			return coordinatorLeaseConflict("actual runtime binding requires the current unexpired started lease")
		}
		return bindCoordinatorRuntimeIdentityTx(tx, &revision, request.RuntimeGenerationID, request.RuntimeSnapshotHash)
	})
}
