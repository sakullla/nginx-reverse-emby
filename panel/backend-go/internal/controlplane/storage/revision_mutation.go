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
	DeleteIdempotencyRecords []IdempotencyRecordKey
}

type IdempotencyRecordKey struct {
	Scope string
	Key   string
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
			wireGuard:    s.wireGuard,
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
				"scope = ? AND key = ?",
				strings.TrimSpace(recordKey.Scope),
				strings.TrimSpace(recordKey.Key),
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
	var row IdempotencyRecordRow
	err := s.db.WithContext(ctx).
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

func (s *GormStore) LockAgentRevisionPointer(ctx context.Context, agentID string, now time.Time) (AgentRevisionPointerRow, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return AgentRevisionPointerRow{}, fmt.Errorf("agent revision pointer agent id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&AgentRevisionPointerRow{AgentID: agentID, UpdatedAt: now}).Error; err != nil {
		return AgentRevisionPointerRow{}, err
	}
	var row AgentRevisionPointerRow
	if err := s.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ?", agentID).
		First(&row).Error; err != nil {
		return AgentRevisionPointerRow{}, err
	}
	return row, nil
}
