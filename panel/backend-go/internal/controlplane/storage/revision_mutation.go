package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RevisionMutationDecision struct {
	Ledger                   *RevisionLedgerWrite
	RollbackResources        bool
	DeleteIdempotencyRecords []IdempotencyRecordMatch
}

type IdempotencyRecordMatch struct {
	Scope              string
	Key                string
	RequestFingerprint string
	OperationID        string
	ExpiresAt          time.Time
}

type RevisionMutationFunc func(*GormStore) (RevisionMutationDecision, error)

func (s *GormStore) WithRevisionMutation(ctx context.Context, mutate RevisionMutationFunc) error {
	if mutate == nil {
		return fmt.Errorf("revision mutation callback is required")
	}
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		const resourceSavepoint = "revision_mutation_resources"
		if err := tx.SavePoint(resourceSavepoint).Error; err != nil {
			return err
		}

		scoped := GormStore{
			db:           tx,
			dataRoot:     s.dataRoot,
			localAgentID: s.localAgentID,
		}

		decision, err := mutate(&scoped)
		if err != nil {
			return err
		}
		if decision.RollbackResources {
			if err := tx.RollbackTo(resourceSavepoint).Error; err != nil {
				return err
			}
		}
		for _, recordKey := range decision.DeleteIdempotencyRecords {
			if err := tx.Where(
				"scope = ? AND key = ? AND request_fingerprint = ? AND operation_id = ? AND expires_at = ?",
				strings.TrimSpace(recordKey.Scope),
				strings.TrimSpace(recordKey.Key),
				recordKey.RequestFingerprint,
				recordKey.OperationID,
				recordKey.ExpiresAt,
			).Delete(&IdempotencyRecordRow{}).Error; err != nil {
				return err
			}
		}
		if decision.Ledger == nil {
			return nil
		}
		if strings.TrimSpace(decision.Ledger.Operation.ID) == "" {
			return fmt.Errorf("operation id is required")
		}
		if err := createRevisionLedgerInTransaction(tx, *decision.Ledger); err != nil {
			return err
		}
		if len(decision.Ledger.Attempts) > 0 {
			if err := tx.Create(&decision.Ledger.Attempts).Error; err != nil {
				return err
			}
		}
		if len(decision.Ledger.Generations) > 0 {
			if err := tx.Create(&decision.Ledger.Generations).Error; err != nil {
				return err
			}
		}
		if len(decision.Ledger.Events) > 0 {
			if err := tx.Create(&decision.Ledger.Events).Error; err != nil {
				return err
			}
		}
		if len(decision.Ledger.IdempotencyRecords) > 0 {
			if err := tx.Create(&decision.Ledger.IdempotencyRecords).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *GormStore) GetIdempotencyRecord(ctx context.Context, scope, key string) (IdempotencyRecordRow, bool, error) {
	return s.getIdempotencyRecord(ctx, scope, key, false)
}

func (s *GormStore) LockIdempotencyRecord(ctx context.Context, scope, key string) (IdempotencyRecordRow, bool, error) {
	return s.getIdempotencyRecord(ctx, scope, key, true)
}

func (s *GormStore) getIdempotencyRecord(ctx context.Context, scope, key string, lock bool) (IdempotencyRecordRow, bool, error) {
	var row IdempotencyRecordRow
	query := s.db.WithContext(ctx)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.
		Where("scope = ? AND key = ?", strings.TrimSpace(scope), strings.TrimSpace(key)).
		First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if err == gorm.ErrRecordNotFound {
		return IdempotencyRecordRow{}, false, nil
	}
	return IdempotencyRecordRow{}, false, err
}

func (s *GormStore) LockAgentRevisionPointer(ctx context.Context, agentID string, now time.Time) (AgentRevisionPointerRow, int64, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentRevisionPointerRow{}, 0, fmt.Errorf("agent revision pointer agent id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var historicalRevision int64
	if err := s.db.WithContext(ctx).
		Model(&AgentRevisionRow{}).
		Select("COALESCE(MAX(revision), 0)").
		Where("agent_id = ?", agentID).
		Scan(&historicalRevision).Error; err != nil {
		return AgentRevisionPointerRow{}, 0, err
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&AgentRevisionPointerRow{AgentID: agentID, UpdatedAt: now}).Error; err != nil {
		return AgentRevisionPointerRow{}, 0, err
	}
	var row AgentRevisionPointerRow
	if err := s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ?", agentID).
		First(&row).Error; err != nil {
		return AgentRevisionPointerRow{}, 0, err
	}
	return row, historicalRevision, nil
}
