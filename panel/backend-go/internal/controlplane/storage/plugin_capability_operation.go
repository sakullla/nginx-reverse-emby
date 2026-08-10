package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPluginCapabilityOperationConflict = errors.New("plugin capability operation id was reused with a different request")

const PluginCapabilityOperationLease = 2 * time.Minute

type pluginCapabilityPendingClaim struct {
	Status         string    `json:"status"`
	ClaimToken     string    `json:"claim_token"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

func (s *GormStore) ClaimPluginCapabilityOperation(ctx context.Context, scope, key, fingerprint, operationID, claimToken string, now, expiresAt time.Time) (IdempotencyRecordRow, bool, error) {
	scope, key, fingerprint, operationID, claimToken = strings.TrimSpace(scope), strings.TrimSpace(key), strings.TrimSpace(fingerprint), strings.TrimSpace(operationID), strings.TrimSpace(claimToken)
	if scope == "" || key == "" || fingerprint == "" || operationID == "" || claimToken == "" || !expiresAt.After(now) {
		return IdempotencyRecordRow{}, false, errors.New("plugin capability operation claim is incomplete")
	}
	pendingJSON, err := json.Marshal(pluginCapabilityPendingClaim{Status: "pending", ClaimToken: claimToken, LeaseExpiresAt: now.Add(PluginCapabilityOperationLease).UTC()})
	if err != nil {
		return IdempotencyRecordRow{}, false, err
	}
	var result IdempotencyRecordRow
	claimed := false
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing IdempotencyRecordRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope = ? AND key = ?", scope, key).First(&existing).Error
		if err == nil && existing.ExpiresAt.After(now) {
			if existing.RequestFingerprint != fingerprint || existing.OperationID != operationID {
				return ErrPluginCapabilityOperationConflict
			}
			var pending pluginCapabilityPendingClaim
			if json.Unmarshal([]byte(existing.ResponseJSON), &pending) != nil || pending.Status != "pending" || pending.LeaseExpiresAt.After(now) {
				result = existing
				return nil
			}
			updated := tx.Model(&IdempotencyRecordRow{}).
				Where("scope = ? AND key = ? AND response_json = ?", scope, key, existing.ResponseJSON).
				Update("response_json", string(pendingJSON))
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return errors.New("plugin capability operation claim fence was lost")
			}
			existing.ResponseJSON = string(pendingJSON)
			result, claimed = existing, true
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if deleteErr := tx.Where("scope = ? AND key = ?", scope, key).Delete(&IdempotencyRecordRow{}).Error; deleteErr != nil {
				return deleteErr
			}
		}
		result = IdempotencyRecordRow{Scope: scope, Key: key, RequestFingerprint: fingerprint, OperationID: operationID, ResponseJSON: string(pendingJSON), CreatedAt: now.UTC(), ExpiresAt: expiresAt.UTC()}
		if createErr := tx.Create(&result).Error; createErr != nil {
			return fmt.Errorf("create plugin capability operation claim: %w", createErr)
		}
		claimed = true
		return nil
	})
	return result, claimed, err
}

func (s *GormStore) RenewPluginCapabilityOperation(ctx context.Context, scope, key, operationID, claimToken string, now time.Time) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing IdempotencyRecordRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope = ? AND key = ? AND operation_id = ?", scope, key, operationID).First(&existing).Error; err != nil {
			return err
		}
		var pending pluginCapabilityPendingClaim
		if err := json.Unmarshal([]byte(existing.ResponseJSON), &pending); err != nil || pending.Status != "pending" || pending.ClaimToken != claimToken {
			return errors.New("plugin capability operation ownership fence was lost")
		}
		pending.LeaseExpiresAt = now.Add(PluginCapabilityOperationLease).UTC()
		encoded, err := json.Marshal(pending)
		if err != nil {
			return err
		}
		updated := tx.Model(&IdempotencyRecordRow{}).
			Where("scope = ? AND key = ? AND operation_id = ? AND response_json = ?", scope, key, operationID, existing.ResponseJSON).
			Update("response_json", string(encoded))
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("plugin capability operation ownership fence was lost")
		}
		return nil
	})
}

func (s *GormStore) CompletePluginCapabilityOperation(ctx context.Context, scope, key, operationID, claimToken, responseJSON string) error {
	return s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var existing IdempotencyRecordRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("scope = ? AND key = ? AND operation_id = ?", scope, key, operationID).First(&existing).Error; err != nil {
			return err
		}
		var pending pluginCapabilityPendingClaim
		if err := json.Unmarshal([]byte(existing.ResponseJSON), &pending); err != nil || pending.Status != "pending" || pending.ClaimToken != claimToken {
			return errors.New("plugin capability operation ownership fence was lost")
		}
		updated := tx.Model(&IdempotencyRecordRow{}).
			Where("scope = ? AND key = ? AND operation_id = ? AND response_json = ?", scope, key, operationID, existing.ResponseJSON).
			Update("response_json", responseJSON)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("plugin capability operation ownership fence was lost")
		}
		return nil
	})
}
