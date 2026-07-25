package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ManagedCertificateGenerationStatePending    = "pending"
	ManagedCertificateGenerationStateActive     = "active"
	ManagedCertificateGenerationStateSuperseded = "superseded"
	managedCertificateGenerationStateInvalid    = "invalid"

	managedCertificateGenerationManifestVersion = 1
	managedCertificateDomainMarkerName          = ".domain"
)

var (
	ErrManagedCertificateGenerationNotFound      = errors.New("managed certificate generation not found")
	ErrManagedCertificateGenerationHashMismatch  = errors.New("managed certificate generation material hash mismatch")
	ErrManagedCertificateGenerationPending       = errors.New("managed certificate generation is already pending")
	ErrManagedCertificateGenerationActive        = errors.New("managed certificate generation is active")
	ErrManagedCertificateGenerationStateMismatch = errors.New("managed certificate generation state does not match its pointer")
	ErrManagedCertificateDomainPathCollision     = errors.New("managed certificate domain path collides with another domain")
)

type managedCertificateDomainLockEntry struct {
	mu   sync.Mutex
	refs int
}

var managedCertificateDomainLocks = struct {
	sync.Mutex
	entries map[string]*managedCertificateDomainLockEntry
}{entries: make(map[string]*managedCertificateDomainLockEntry)}

type ManagedCertificateGeneration struct {
	ID           string
	Domain       string
	State        string
	MaterialHash string
	CreatedAt    string
	PromotedAt   string
	Material     ManagedCertificateBundle
}

type ManagedCertificateGenerationStore interface {
	StageManagedCertificateGeneration(context.Context, string, ManagedCertificateBundle) (ManagedCertificateGeneration, error)
	LoadPendingManagedCertificateGeneration(context.Context, string) (ManagedCertificateGeneration, bool, error)
	LoadActiveManagedCertificateGeneration(context.Context, string) (ManagedCertificateGeneration, bool, error)
	PromoteManagedCertificateGeneration(context.Context, string, string, string) error
	AbortManagedCertificateGeneration(context.Context, string, string) error
	GarbageCollectManagedCertificateGenerations(context.Context, string) error
	ReconcileManagedCertificateGenerations(context.Context, string) error
}

var _ ManagedCertificateGenerationStore = (*GormStore)(nil)

type managedCertificateGenerationManifest struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	Domain       string `json:"domain"`
	MaterialHash string `json:"material_hash"`
	CertSHA256   string `json:"cert_sha256"`
	KeySHA256    string `json:"key_sha256"`
	CreatedAt    string `json:"created_at"`
}

func (s *GormStore) StageManagedCertificateGeneration(ctx context.Context, domain string, bundle ManagedCertificateBundle) (ManagedCertificateGeneration, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.stageManagedCertificateGenerationLocked(ctx, domain, bundle)
}

func (s *GormStore) stageManagedCertificateGenerationLocked(ctx context.Context, domain string, bundle ManagedCertificateBundle) (ManagedCertificateGeneration, error) {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return ManagedCertificateGeneration{}, err
	}
	bundle.Domain = domain
	generationID, err := newManagedCertificateGenerationID()
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	materialHash := managedCertificateGenerationMaterialHash(bundle)
	manifest := managedCertificateGenerationManifest{
		Version:      managedCertificateGenerationManifestVersion,
		ID:           generationID,
		Domain:       domain,
		MaterialHash: materialHash,
		CertSHA256:   managedCertificateGenerationValueHash(bundle.CertPEM),
		KeySHA256:    managedCertificateGenerationValueHash(bundle.KeyPEM),
		CreatedAt:    createdAt,
	}
	if err := s.writeManagedCertificateGeneration(manifest, bundle); err != nil {
		return ManagedCertificateGeneration{}, err
	}

	row := ManagedCertificateGenerationRow{
		ID:           generationID,
		Domain:       domain,
		State:        ManagedCertificateGenerationStatePending,
		MaterialHash: materialHash,
		CreatedAt:    createdAt,
	}
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if strings.TrimSpace(certificate.PendingGenerationID) != "" {
			return ErrManagedCertificateGenerationPending
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Model(&ManagedCertificateRow{}).
			Where("id = ?", certificate.ID).
			Update("pending_generation_id", generationID).Error
	})
	if err != nil {
		_ = s.removeManagedCertificateGenerationDirectory(domain, generationID)
		return ManagedCertificateGeneration{}, err
	}
	return managedCertificateGenerationFromRow(row, bundle), nil
}

func (s *GormStore) LoadPendingManagedCertificateGeneration(ctx context.Context, domain string) (ManagedCertificateGeneration, bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	return s.loadManagedCertificateGenerationByPointer(ctx, domain, "pending_generation_id", ManagedCertificateGenerationStatePending)
}

func (s *GormStore) LoadActiveManagedCertificateGeneration(ctx context.Context, domain string) (ManagedCertificateGeneration, bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.loadActiveManagedCertificateGenerationLocked(ctx, domain)
}

func (s *GormStore) loadActiveManagedCertificateGenerationLocked(ctx context.Context, domain string) (ManagedCertificateGeneration, bool, error) {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	generation, ok, err := s.loadManagedCertificateGenerationByPointer(ctx, domain, "active_generation_id", ManagedCertificateGenerationStateActive)
	if err == nil && ok {
		return generation, ok, nil
	}
	if reconcileErr := s.reconcileManagedCertificateGenerationsLocked(ctx, domain); reconcileErr != nil {
		return ManagedCertificateGeneration{}, false, errors.Join(err, reconcileErr)
	}
	return s.loadManagedCertificateGenerationByPointer(ctx, domain, "active_generation_id", ManagedCertificateGenerationStateActive)
}

func (s *GormStore) PromoteManagedCertificateGeneration(ctx context.Context, domain, generationID, expectedMaterialHash string) error {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.promoteManagedCertificateGenerationLocked(ctx, domain, generationID, expectedMaterialHash)
}

func (s *GormStore) promoteManagedCertificateGenerationLocked(ctx context.Context, domain, generationID, expectedMaterialHash string) error {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	generationID = strings.TrimSpace(generationID)
	if !isSafeSinglePathComponent(generationID) {
		return ErrManagedCertificateGenerationNotFound
	}
	generation, err := s.loadManagedCertificateGeneration(ctx, domain, generationID)
	if err != nil {
		return err
	}
	if generation.MaterialHash != strings.TrimSpace(expectedMaterialHash) {
		return ErrManagedCertificateGenerationHashMismatch
	}

	promotedAt := time.Now().UTC().Format(time.RFC3339Nano)
	previousActiveID := ""
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if strings.TrimSpace(certificate.PendingGenerationID) != generationID {
			return ErrManagedCertificateGenerationNotFound
		}
		var pending ManagedCertificateGenerationRow
		if err := tx.Where("id = ? AND domain = ? AND state = ?", generationID, domain, ManagedCertificateGenerationStatePending).First(&pending).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if pending.MaterialHash != generation.MaterialHash || pending.MaterialHash != strings.TrimSpace(expectedMaterialHash) {
			return ErrManagedCertificateGenerationHashMismatch
		}
		if activeID := strings.TrimSpace(certificate.ActiveGenerationID); activeID != "" && activeID != generationID {
			previousActiveID = activeID
			if err := tx.Model(&ManagedCertificateGenerationRow{}).
				Where("id = ? AND domain = ? AND state = ?", activeID, domain, ManagedCertificateGenerationStateActive).
				Update("state", ManagedCertificateGenerationStateSuperseded).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&ManagedCertificateGenerationRow{}).
			Where("id = ? AND domain = ?", generationID, domain).
			Updates(map[string]any{"state": ManagedCertificateGenerationStateActive, "promoted_at": promotedAt}).Error; err != nil {
			return err
		}
		return tx.Model(&ManagedCertificateRow{}).
			Where("id = ?", certificate.ID).
			Updates(map[string]any{"active_generation_id": generationID, "pending_generation_id": ""}).Error
	})
	if err != nil {
		return err
	}
	if err := s.writeManagedCertificateLegacyProjection(domain, generation.Material); err != nil {
		projectionErr := err
		compensationErr := s.writeTransaction(ctx, func(tx *gorm.DB) error {
			var certificate ManagedCertificateRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
				return err
			}
			if strings.TrimSpace(certificate.ActiveGenerationID) != generationID {
				return errors.New("managed certificate generation changed before projection compensation")
			}
			if err := tx.Model(&ManagedCertificateGenerationRow{}).
				Where("id = ? AND domain = ?", generationID, domain).
				Updates(map[string]any{"state": ManagedCertificateGenerationStatePending, "promoted_at": ""}).Error; err != nil {
				return err
			}
			if previousActiveID != "" {
				if err := tx.Model(&ManagedCertificateGenerationRow{}).
					Where("id = ? AND domain = ?", previousActiveID, domain).
					Update("state", ManagedCertificateGenerationStateActive).Error; err != nil {
					return err
				}
			}
			return tx.Model(&ManagedCertificateRow{}).
				Where("id = ?", certificate.ID).
				Updates(map[string]any{"active_generation_id": previousActiveID, "pending_generation_id": generationID}).Error
		})
		var restoreProjectionErr error
		if compensationErr == nil && previousActiveID != "" {
			if previous, loadErr := s.loadManagedCertificateGeneration(ctx, domain, previousActiveID); loadErr == nil {
				restoreProjectionErr = s.writeManagedCertificateLegacyProjection(domain, previous.Material)
			} else {
				restoreProjectionErr = loadErr
			}
		}
		return errors.Join(projectionErr, compensationErr, restoreProjectionErr)
	}
	return nil
}

func (s *GormStore) AbortManagedCertificateGeneration(ctx context.Context, domain, generationID string) error {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	generationID = strings.TrimSpace(generationID)
	if !isSafeSinglePathComponent(generationID) {
		return ErrManagedCertificateGenerationNotFound
	}
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if strings.TrimSpace(certificate.ActiveGenerationID) == generationID {
			return ErrManagedCertificateGenerationActive
		}
		if strings.TrimSpace(certificate.PendingGenerationID) != generationID {
			return ErrManagedCertificateGenerationNotFound
		}
		var pending ManagedCertificateGenerationRow
		if err := tx.Where("id = ? AND domain = ? AND state = ?", generationID, domain, ManagedCertificateGenerationStatePending).First(&pending).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if err := tx.Model(&ManagedCertificateRow{}).
			Where("id = ?", certificate.ID).
			Update("pending_generation_id", "").Error; err != nil {
			return err
		}
		return tx.Delete(&ManagedCertificateGenerationRow{}, "id = ? AND domain = ?", generationID, domain).Error
	})
	if err != nil {
		return err
	}
	return s.removeManagedCertificateGenerationDirectory(domain, generationID)
}

func (s *GormStore) GarbageCollectManagedCertificateGenerations(ctx context.Context, domain string) error {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	removeIDs := make([]string, 0)
	err = s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var rows []ManagedCertificateGenerationRow
		if err := tx.Where("domain = ?", domain).
			Order("promoted_at DESC, created_at DESC, id DESC").
			Find(&rows).Error; err != nil {
			return err
		}
		keep := map[string]struct{}{}
		if id := strings.TrimSpace(certificate.ActiveGenerationID); id != "" {
			keep[id] = struct{}{}
		}
		if id := strings.TrimSpace(certificate.PendingGenerationID); id != "" {
			keep[id] = struct{}{}
		}
		for _, row := range rows {
			if row.State != ManagedCertificateGenerationStateSuperseded {
				continue
			}
			generation, loadErr := s.loadManagedCertificateGenerationFromDB(tx, domain, row.ID)
			if loadErr != nil || generation.State != ManagedCertificateGenerationStateSuperseded {
				continue
			}
			keep[row.ID] = struct{}{}
			break
		}
		for _, row := range rows {
			if _, ok := keep[row.ID]; !ok {
				removeIDs = append(removeIDs, row.ID)
			}
		}
		if len(removeIDs) == 0 {
			return nil
		}
		return tx.Where("domain = ? AND id IN ?", domain, removeIDs).Delete(&ManagedCertificateGenerationRow{}).Error
	})
	if err != nil {
		return err
	}
	for _, id := range removeIDs {
		if err := s.removeManagedCertificateGenerationDirectory(domain, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *GormStore) ReconcileManagedCertificateGenerations(ctx context.Context, domain string) error {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.reconcileManagedCertificateGenerationsLocked(ctx, domain)
}

func (s *GormStore) reconcileManagedCertificateGenerationsLocked(ctx context.Context, domain string) error {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return err
	}
	if err := s.cleanManagedCertificateGenerationStagingDirectories(domain); err != nil {
		return err
	}

	var active ManagedCertificateGeneration
	haveActive := false
	knownGenerationIDs := make(map[string]struct{})
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return tx.Where("domain = ?", domain).Delete(&ManagedCertificateGenerationRow{}).Error
			}
			return err
		}

		var rows []ManagedCertificateGenerationRow
		if err := tx.Where("domain = ?", domain).
			Order("promoted_at DESC, created_at DESC, id DESC").Find(&rows).Error; err != nil {
			return err
		}
		rowsByID := make(map[string]ManagedCertificateGenerationRow, len(rows))
		validGenerations := make(map[string]ManagedCertificateGeneration, len(rows))
		invalidGenerationIDs := make(map[string]struct{})
		for _, row := range rows {
			knownGenerationIDs[row.ID] = struct{}{}
			rowsByID[row.ID] = row
			switch row.State {
			case ManagedCertificateGenerationStateActive,
				ManagedCertificateGenerationStatePending,
				ManagedCertificateGenerationStateSuperseded:
				generation, loadErr := s.loadManagedCertificateGenerationFromDB(tx, domain, row.ID)
				if loadErr != nil {
					invalidGenerationIDs[row.ID] = struct{}{}
					continue
				}
				validGenerations[row.ID] = generation
			case managedCertificateGenerationStateInvalid:
			default:
				invalidGenerationIDs[row.ID] = struct{}{}
			}
		}

		selectedActiveID := ""
		activeID := strings.TrimSpace(certificate.ActiveGenerationID)
		if row, ok := rowsByID[activeID]; ok && row.State == ManagedCertificateGenerationStateActive {
			if candidate, valid := validGenerations[activeID]; valid {
				selectedActiveID = activeID
				active = candidate
			}
		}
		if selectedActiveID == "" {
			for _, row := range rows {
				if row.State != ManagedCertificateGenerationStateActive {
					continue
				}
				if candidate, valid := validGenerations[row.ID]; valid {
					selectedActiveID = row.ID
					active = candidate
					break
				}
			}
		}
		if selectedActiveID == "" {
			for _, row := range rows {
				if row.State != ManagedCertificateGenerationStateSuperseded {
					continue
				}
				if candidate, valid := validGenerations[row.ID]; valid {
					selectedActiveID = row.ID
					active = candidate
					break
				}
			}
		}
		if selectedActiveID != "" {
			active.State = ManagedCertificateGenerationStateActive
			haveActive = true
		}

		selectedPendingID := ""
		pendingID := strings.TrimSpace(certificate.PendingGenerationID)
		if row, ok := rowsByID[pendingID]; ok && row.State == ManagedCertificateGenerationStatePending {
			if _, valid := validGenerations[pendingID]; valid {
				selectedPendingID = pendingID
			}
		}

		for _, row := range rows {
			nextState := row.State
			if _, invalid := invalidGenerationIDs[row.ID]; invalid {
				nextState = managedCertificateGenerationStateInvalid
			} else {
				switch row.State {
				case ManagedCertificateGenerationStateActive:
					if row.ID != selectedActiveID {
						nextState = ManagedCertificateGenerationStateSuperseded
					}
				case ManagedCertificateGenerationStatePending:
					if row.ID != selectedPendingID {
						nextState = managedCertificateGenerationStateInvalid
					}
				case ManagedCertificateGenerationStateSuperseded:
					if row.ID == selectedActiveID {
						nextState = ManagedCertificateGenerationStateActive
					}
				}
			}
			if row.ID == selectedActiveID {
				nextState = ManagedCertificateGenerationStateActive
			}
			if nextState != row.State {
				if err := tx.Model(&ManagedCertificateGenerationRow{}).
					Where("id = ? AND domain = ?", row.ID, domain).
					Update("state", nextState).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Model(&ManagedCertificateRow{}).Where("id = ?", certificate.ID).Updates(map[string]any{
			"active_generation_id":  selectedActiveID,
			"pending_generation_id": selectedPendingID,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.cleanManagedCertificateFinalizedOrphanDirectories(domain, knownGenerationIDs); err != nil {
		return err
	}
	if haveActive {
		return s.writeManagedCertificateLegacyProjection(domain, active.Material)
	}
	return nil
}

func (s *GormStore) importLegacyManagedCertificateGeneration(ctx context.Context, domain string, bundle ManagedCertificateBundle) (ManagedCertificateGeneration, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	unlock := s.lockManagedCertificateDomain(domain)
	defer unlock()
	return s.importLegacyManagedCertificateGenerationLocked(ctx, domain, bundle)
}

func (s *GormStore) importLegacyManagedCertificateGenerationLocked(ctx context.Context, domain string, bundle ManagedCertificateBundle) (ManagedCertificateGeneration, error) {
	if err := s.migrateManagedCertificateLegacyDirectoryLocked(domain); err != nil {
		return ManagedCertificateGeneration{}, err
	}
	bundle.Domain = domain
	materialHash := managedCertificateGenerationMaterialHash(bundle)
	generationID := managedCertificateLegacyGenerationID(domain, materialHash)
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	manifest := managedCertificateGenerationManifest{
		Version:      managedCertificateGenerationManifestVersion,
		ID:           generationID,
		Domain:       domain,
		MaterialHash: materialHash,
		CertSHA256:   managedCertificateGenerationValueHash(bundle.CertPEM),
		KeySHA256:    managedCertificateGenerationValueHash(bundle.KeyPEM),
		CreatedAt:    createdAt,
	}
	if err := s.writeManagedCertificateGeneration(manifest, bundle); err != nil {
		existingManifest, existingBundle, readErr := s.readManagedCertificateGeneration(domain, generationID)
		if readErr != nil || existingManifest.MaterialHash != materialHash || existingBundle.CertPEM != bundle.CertPEM || existingBundle.KeyPEM != bundle.KeyPEM {
			return ManagedCertificateGeneration{}, errors.Join(err, readErr)
		}
		createdAt = existingManifest.CreatedAt
	}
	promotedAt := time.Now().UTC().Format(time.RFC3339Nano)
	selectedID := generationID
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		var certificate ManagedCertificateRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("domain = ?", domain).First(&certificate).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrManagedCertificateGenerationNotFound
			}
			return err
		}
		if activeID := strings.TrimSpace(certificate.ActiveGenerationID); activeID != "" {
			selectedID = activeID
			return nil
		}
		if err := tx.Model(&ManagedCertificateGenerationRow{}).
			Where("domain = ? AND state = ? AND id <> ?", domain, ManagedCertificateGenerationStateActive, generationID).
			Update("state", ManagedCertificateGenerationStateSuperseded).Error; err != nil {
			return err
		}
		row := ManagedCertificateGenerationRow{
			ID:           generationID,
			Domain:       domain,
			State:        ManagedCertificateGenerationStateActive,
			MaterialHash: materialHash,
			CreatedAt:    createdAt,
			PromotedAt:   promotedAt,
		}
		var existing ManagedCertificateGenerationRow
		if err := tx.Where("id = ?", generationID).First(&existing).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			if existing.Domain != domain || existing.MaterialHash != materialHash {
				return ErrManagedCertificateGenerationHashMismatch
			}
			if err := tx.Model(&ManagedCertificateGenerationRow{}).
				Where("id = ?", generationID).
				Updates(map[string]any{"state": ManagedCertificateGenerationStateActive, "promoted_at": promotedAt}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&ManagedCertificateRow{}).
			Where("id = ?", certificate.ID).
			Update("active_generation_id", generationID).Error
	})
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	generation, err := s.loadManagedCertificateGeneration(ctx, domain, selectedID)
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	if err := s.writeManagedCertificateLegacyProjection(domain, generation.Material); err != nil {
		return ManagedCertificateGeneration{}, err
	}
	return generation, nil
}

func (s *GormStore) loadManagedCertificateGenerationByPointer(ctx context.Context, domain, pointerColumn, expectedState string) (ManagedCertificateGeneration, bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	var certificate ManagedCertificateRow
	if err := s.db.WithContext(ctx).Where("domain = ?", domain).First(&certificate).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ManagedCertificateGeneration{}, false, nil
		}
		return ManagedCertificateGeneration{}, false, err
	}
	var generationID string
	switch pointerColumn {
	case "pending_generation_id":
		generationID = certificate.PendingGenerationID
	case "active_generation_id":
		generationID = certificate.ActiveGenerationID
	default:
		return ManagedCertificateGeneration{}, false, errors.New("unsupported managed certificate generation pointer")
	}
	generationID = strings.TrimSpace(generationID)
	if generationID == "" {
		return ManagedCertificateGeneration{}, false, nil
	}
	generation, err := s.loadManagedCertificateGeneration(ctx, domain, generationID)
	if err != nil {
		return ManagedCertificateGeneration{}, false, err
	}
	if generation.State != expectedState {
		return ManagedCertificateGeneration{}, false, ErrManagedCertificateGenerationStateMismatch
	}
	return generation, true, nil
}

func (s *GormStore) loadManagedCertificateGeneration(ctx context.Context, domain, generationID string) (ManagedCertificateGeneration, error) {
	return s.loadManagedCertificateGenerationFromDB(s.db.WithContext(ctx), domain, generationID)
}

func (s *GormStore) loadManagedCertificateGenerationFromDB(db *gorm.DB, domain, generationID string) (ManagedCertificateGeneration, error) {
	normalizedDomain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil || normalizedDomain != domain || !isSafeSinglePathComponent(generationID) {
		return ManagedCertificateGeneration{}, ErrManagedCertificateGenerationNotFound
	}
	var row ManagedCertificateGenerationRow
	if err := db.Where("id = ? AND domain = ?", generationID, domain).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ManagedCertificateGeneration{}, ErrManagedCertificateGenerationNotFound
		}
		return ManagedCertificateGeneration{}, err
	}
	manifest, bundle, err := s.readManagedCertificateGeneration(domain, generationID)
	if err != nil {
		return ManagedCertificateGeneration{}, err
	}
	if manifest.MaterialHash != row.MaterialHash || manifest.CreatedAt != row.CreatedAt {
		return ManagedCertificateGeneration{}, ErrManagedCertificateGenerationHashMismatch
	}
	return managedCertificateGenerationFromRow(row, bundle), nil
}

func managedCertificateGenerationFromRow(row ManagedCertificateGenerationRow, bundle ManagedCertificateBundle) ManagedCertificateGeneration {
	return ManagedCertificateGeneration{
		ID:           row.ID,
		Domain:       row.Domain,
		State:        row.State,
		MaterialHash: row.MaterialHash,
		CreatedAt:    row.CreatedAt,
		PromotedAt:   row.PromotedAt,
		Material:     bundle,
	}
}

func (s *GormStore) writeManagedCertificateGeneration(manifest managedCertificateGenerationManifest, bundle ManagedCertificateBundle) (returnErr error) {
	domain, err := normalizeManagedCertificateGenerationDomain(manifest.Domain)
	if err != nil || domain != manifest.Domain || !isSafeSinglePathComponent(manifest.ID) {
		return errors.New("managed certificate generation manifest path is invalid")
	}
	generationsDir, err := s.ensureManagedCertificateGenerationsDirectory(domain)
	if err != nil {
		return err
	}
	temporaryDir, err := os.MkdirTemp(generationsDir, ".stage-")
	if err != nil {
		return err
	}
	defer func() {
		if returnErr != nil {
			_ = os.RemoveAll(temporaryDir)
		}
	}()
	if err := os.Chmod(temporaryDir, 0o700); err != nil {
		return err
	}
	if err := writeManagedCertificateDurableFile(filepath.Join(temporaryDir, "cert"), []byte(bundle.CertPEM), 0o600); err != nil {
		return err
	}
	if err := writeManagedCertificateDurableFile(filepath.Join(temporaryDir, "key"), []byte(bundle.KeyPEM), 0o600); err != nil {
		return err
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := writeManagedCertificateDurableFile(filepath.Join(temporaryDir, "manifest.json"), manifestJSON, 0o600); err != nil {
		return err
	}
	if err := syncManagedCertificateDirectory(temporaryDir); err != nil {
		return err
	}
	finalDir := s.managedCertificateGenerationDirectory(manifest.Domain, manifest.ID)
	if _, err := os.Lstat(finalDir); err == nil {
		return errors.New("managed certificate generation destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryDir, finalDir); err != nil {
		return err
	}
	if err := os.Chmod(finalDir, 0o700); err != nil {
		_ = os.RemoveAll(finalDir)
		return err
	}
	return syncManagedCertificateDirectory(generationsDir)
}

func (s *GormStore) installManagedCertificateGeneration(manifest managedCertificateGenerationManifest, bundle ManagedCertificateBundle) (bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(manifest.Domain)
	if err != nil || domain != manifest.Domain || !isSafeSinglePathComponent(manifest.ID) {
		return false, errors.New("managed certificate generation manifest path is invalid")
	}
	if existingManifest, existingBundle, readErr := s.readManagedCertificateGeneration(manifest.Domain, manifest.ID); readErr == nil {
		if existingManifest != manifest ||
			existingBundle.CertPEM != bundle.CertPEM || existingBundle.KeyPEM != bundle.KeyPEM {
			return false, ErrManagedCertificateGenerationHashMismatch
		}
		return false, nil
	}
	if _, err := os.Lstat(s.managedCertificateGenerationDirectory(manifest.Domain, manifest.ID)); err == nil {
		return false, errors.New("managed certificate generation destination already exists but is invalid")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	bundle.Domain = manifest.Domain
	if err := s.writeManagedCertificateGeneration(manifest, bundle); err != nil {
		return false, err
	}
	return true, nil
}

func (s *GormStore) readManagedCertificateGeneration(domain, generationID string) (managedCertificateGenerationManifest, ManagedCertificateBundle, error) {
	normalizedDomain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil || normalizedDomain != domain || !isSafeSinglePathComponent(generationID) {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, ErrManagedCertificateGenerationNotFound
	}
	directory, err := s.validateManagedCertificateGenerationDirectory(domain, generationID)
	if errors.Is(err, os.ErrNotExist) {
		directory, err = s.validateManagedCertificateLegacyGenerationDirectory(domain, generationID)
	}
	if err != nil {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, err
	}
	manifestJSON, err := readManagedCertificateRegularFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, err
	}
	var manifest managedCertificateGenerationManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, err
	}
	if manifest.Version != managedCertificateGenerationManifestVersion || manifest.ID != generationID || manifest.Domain != domain {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, ErrManagedCertificateGenerationHashMismatch
	}
	certPEM, err := readManagedCertificateRegularFile(filepath.Join(directory, "cert"))
	if err != nil {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, err
	}
	keyPEM, err := readManagedCertificateRegularFile(filepath.Join(directory, "key"))
	if err != nil {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, err
	}
	bundle := ManagedCertificateBundle{Domain: domain, CertPEM: string(certPEM), KeyPEM: string(keyPEM)}
	if manifest.CertSHA256 != managedCertificateGenerationValueHash(bundle.CertPEM) ||
		manifest.KeySHA256 != managedCertificateGenerationValueHash(bundle.KeyPEM) ||
		manifest.MaterialHash != managedCertificateGenerationMaterialHash(bundle) {
		return managedCertificateGenerationManifest{}, ManagedCertificateBundle{}, ErrManagedCertificateGenerationHashMismatch
	}
	return manifest, bundle, nil
}

func (s *GormStore) writeManagedCertificateLegacyProjection(domain string, bundle ManagedCertificateBundle) error {
	directory, err := s.ensureManagedCertificateDirectory(domain)
	if err != nil {
		return err
	}
	legacyDirectory, writeLegacy, err := s.ensureManagedCertificateLegacyDirectory(domain)
	if err != nil {
		return err
	}
	if !writeLegacy {
		if err := s.retireManagedCertificateLegacyProjection(domain); err != nil {
			return err
		}
	}
	canonicalSnapshot, err := captureManagedCertificateProjection(directory)
	if err != nil {
		return err
	}
	if writeLegacy {
		if _, err := captureManagedCertificateProjection(legacyDirectory); err != nil {
			return err
		}
	}
	if err := writeManagedCertificateProjection(directory, bundle); err != nil {
		return err
	}
	if writeLegacy {
		if err := writeManagedCertificateProjection(legacyDirectory, bundle); err != nil {
			return errors.Join(err, rollbackManagedCertificateProjection(directory, canonicalSnapshot))
		}
	}
	return nil
}

func captureManagedCertificateProjection(directory string) ([]managedCertificateProjectionFile, error) {
	files := []managedCertificateProjectionFile{{name: "cert"}, {name: "key"}}
	for i := range files {
		file := &files[i]
		file.destination = filepath.Join(directory, file.name)
		if err := validateManagedCertificateProjectionDestination(file.destination); err != nil {
			return nil, err
		}
		previous, err := readManagedCertificateRegularFile(file.destination)
		if errors.Is(err, os.ErrNotExist) {
			file.previousExists = false
		} else if err != nil {
			return nil, err
		} else {
			file.previous = previous
			file.previousExists = true
		}
		file.applied = true
	}
	return files, nil
}

func writeManagedCertificateProjection(directory string, bundle ManagedCertificateBundle) error {
	files := []managedCertificateProjectionFile{
		{name: "cert", data: []byte(bundle.CertPEM)},
		{name: "key", data: []byte(bundle.KeyPEM)},
	}
	defer removeManagedCertificateProjectionTemporaryFiles(files)
	for i := range files {
		file := &files[i]
		file.destination = filepath.Join(directory, file.name)
		if err := validateManagedCertificateProjectionDestination(file.destination); err != nil {
			return err
		}
		previous, err := readManagedCertificateRegularFile(file.destination)
		if errors.Is(err, os.ErrNotExist) {
			file.previousExists = false
		} else if err != nil {
			return err
		} else {
			file.previous = previous
			file.previousExists = true
		}
		file.temporary, err = writeManagedCertificateProjectionTemporaryFile(directory, file.data)
		if err != nil {
			return err
		}
	}

	for i := range files {
		file := &files[i]
		if err := os.Rename(file.temporary, file.destination); err != nil {
			return errors.Join(err, rollbackManagedCertificateProjection(directory, files))
		}
		file.temporary = ""
		file.applied = true
		if err := syncManagedCertificateDirectory(directory); err != nil {
			return errors.Join(err, rollbackManagedCertificateProjection(directory, files))
		}
	}
	return nil
}

type managedCertificateProjectionFile struct {
	name           string
	data           []byte
	destination    string
	temporary      string
	previous       []byte
	previousExists bool
	applied        bool
}

func writeManagedCertificateProjectionTemporaryFile(directory string, data []byte) (path string, returnErr error) {
	temporary, err := os.CreateTemp(directory, ".projection-")
	if err != nil {
		return "", err
	}
	path = temporary.Name()
	defer func() {
		if closeErr := temporary.Close(); returnErr == nil {
			returnErr = closeErr
		}
		if returnErr != nil {
			_ = os.Remove(path)
			path = ""
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return path, err
	}
	if _, err := temporary.Write(data); err != nil {
		return path, err
	}
	if err := temporary.Sync(); err != nil {
		return path, err
	}
	return path, nil
}

func removeManagedCertificateProjectionTemporaryFiles(files []managedCertificateProjectionFile) {
	for _, file := range files {
		if file.temporary != "" {
			_ = os.Remove(file.temporary)
		}
	}
}

func rollbackManagedCertificateProjection(directory string, files []managedCertificateProjectionFile) error {
	var rollbackErr error
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if !file.applied {
			continue
		}
		if file.previousExists {
			temporary, err := writeManagedCertificateProjectionTemporaryFile(directory, file.previous)
			if err == nil {
				err = os.Rename(temporary, file.destination)
				if err != nil {
					_ = os.Remove(temporary)
				}
			}
			rollbackErr = errors.Join(rollbackErr, err)
		} else if err := os.Remove(file.destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, err)
		}
	}
	return errors.Join(rollbackErr, syncManagedCertificateDirectory(directory))
}

func writeManagedCertificateDurableFile(path string, data []byte, mode os.FileMode) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); returnErr == nil {
			returnErr = closeErr
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func syncManagedCertificateDirectory(path string) error {
	// Windows does not expose directory handles that os.File.Sync can flush.
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func syncManagedCertificateDirectoryIfPresent(path string) error {
	if err := validateManagedCertificateRegularDirectory(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return syncManagedCertificateDirectory(path)
}

func (s *GormStore) managedCertificateGenerationsDirectory(domain string) string {
	return filepath.Join(s.managedCertificateDirectory(domain), "generations")
}

func (s *GormStore) managedCertificateGenerationDirectory(domain, generationID string) string {
	return filepath.Join(s.managedCertificateGenerationsDirectory(domain), generationID)
}

func (s *GormStore) ensureManagedCertificateDirectory(domain string) (string, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return "", err
	}
	root := filepath.Join(s.dataRoot, "managed_certificates")
	if err := ensureManagedCertificateRegularDirectory(root, 0o700, true); err != nil {
		return "", err
	}
	directory := s.managedCertificateDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(root) {
		return "", errors.New("managed certificate directory escapes its root")
	}
	if err := ensureManagedCertificateRegularDirectory(directory, 0o700, false); err != nil {
		return "", err
	}
	if err := ensureManagedCertificateDomainMarker(directory, domain); err != nil {
		return "", err
	}
	return directory, nil
}

func (s *GormStore) ensureManagedCertificateLegacyDirectory(domain string) (string, bool, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return "", false, err
	}
	unambiguous, err := s.managedCertificateLegacyPathIsUnambiguous(domain)
	if err != nil || !unambiguous {
		return "", false, err
	}
	root := filepath.Join(s.dataRoot, "managed_certificates")
	if err := ensureManagedCertificateRegularDirectory(root, 0o700, true); err != nil {
		return "", false, err
	}
	directory := s.legacyManagedCertificateDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(root) {
		return "", false, errors.New("legacy managed certificate directory escapes its root")
	}
	if err := ensureManagedCertificateRegularDirectory(directory, 0o700, false); err != nil {
		return "", false, err
	}
	owned, err := managedCertificateLegacyDirectoryOwnedBy(directory, domain)
	if err != nil {
		return "", false, err
	}
	if !owned {
		return "", false, ErrManagedCertificateDomainPathCollision
	}
	if err := ensureManagedCertificateDomainMarker(directory, domain); err != nil {
		return "", false, err
	}
	return directory, true, nil
}

func (s *GormStore) migrateManagedCertificateLegacyDirectoryLocked(domain string) error {
	return s.migrateManagedCertificateLegacyDirectoryLockedWithSync(domain, syncManagedCertificateDirectory)
}

func (s *GormStore) migrateManagedCertificateLegacyDirectoryLockedWithSync(
	domain string,
	syncDirectory func(string) error,
) error {
	canonicalDirectory := s.managedCertificateDirectory(domain)
	if _, err := os.Lstat(canonicalDirectory); err == nil {
		_, err = s.ensureManagedCertificateDirectory(domain)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	legacyDirectory := s.legacyManagedCertificateDirectory(domain)
	if _, err := os.Lstat(legacyDirectory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	unambiguous, err := s.managedCertificateLegacyPathIsUnambiguous(domain)
	if err != nil {
		return err
	}
	if !unambiguous {
		return s.retireManagedCertificateLegacyProjection(domain)
	}
	validatedLegacyDirectory, err := s.validateManagedCertificateLegacyDirectoryUnambiguous(domain)
	if err != nil {
		return err
	}
	if err := validateManagedCertificateLegacyTreeOwnership(validatedLegacyDirectory, domain); err != nil {
		return err
	}
	markerPath := filepath.Join(validatedLegacyDirectory, managedCertificateDomainMarkerName)
	_, markerErr := readManagedCertificateRegularFile(markerPath)
	markerExisted := markerErr == nil
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}
	if err := os.Rename(validatedLegacyDirectory, canonicalDirectory); err != nil {
		return err
	}
	root := filepath.Join(s.dataRoot, "managed_certificates")
	rollback := func(cause error) error {
		var rollbackErr error
		if err := removeManagedCertificateDirectoryPath(validatedLegacyDirectory); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
			return errors.Join(cause, rollbackErr)
		}
		if !markerExisted {
			if err := os.Remove(filepath.Join(canonicalDirectory, managedCertificateDomainMarkerName)); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		if err := os.Rename(canonicalDirectory, validatedLegacyDirectory); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		}
		rollbackErr = errors.Join(rollbackErr, syncDirectory(root))
		return errors.Join(cause, rollbackErr)
	}
	if err := ensureManagedCertificateDomainMarker(canonicalDirectory, domain); err != nil {
		return rollback(err)
	}
	if err := syncDirectory(root); err != nil {
		return rollback(err)
	}

	certPEM, certErr := readManagedCertificateRegularFile(filepath.Join(canonicalDirectory, "cert"))
	keyPEM, keyErr := readManagedCertificateRegularFile(filepath.Join(canonicalDirectory, "key"))
	if certErr != nil && !errors.Is(certErr, os.ErrNotExist) {
		return rollback(certErr)
	}
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		return rollback(keyErr)
	}
	if certErr == nil && keyErr == nil {
		legacyProjection, ok, legacyErr := s.ensureManagedCertificateLegacyDirectory(domain)
		if legacyErr != nil {
			return rollback(legacyErr)
		}
		if !ok {
			return rollback(ErrManagedCertificateDomainPathCollision)
		}
		if err := writeManagedCertificateProjection(legacyProjection, ManagedCertificateBundle{
			Domain:  domain,
			CertPEM: string(certPEM),
			KeyPEM:  string(keyPEM),
		}); err != nil {
			return rollback(err)
		}
		if err := syncDirectory(root); err != nil {
			return rollback(err)
		}
	}
	return nil
}

func (s *GormStore) retireManagedCertificateLegacyProjection(domain string) error {
	directory := s.legacyManagedCertificateDirectory(domain)
	root := filepath.Join(s.dataRoot, "managed_certificates")
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(root) {
		return errors.New("legacy managed certificate directory escapes its root")
	}
	if err := validateManagedCertificateRegularDirectory(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	marker, err := readManagedCertificateRegularFile(filepath.Join(directory, managedCertificateDomainMarkerName))
	if errors.Is(err, os.ErrNotExist) {
		return ErrManagedCertificateDomainPathCollision
	}
	if err != nil {
		return err
	}
	owner, err := normalizeManagedCertificateGenerationDomain(string(marker))
	if err != nil || managedCertificateLegacyDomainKey(owner) != managedCertificateLegacyDomainKey(domain) {
		return ErrManagedCertificateDomainPathCollision
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != managedCertificateDomainMarkerName && name != "cert" && name != "key" && !strings.HasPrefix(name, ".projection-") {
			return ErrManagedCertificateDomainPathCollision
		}
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return ErrManagedCertificateDomainPathCollision
		}
	}
	if err := removeManagedCertificateDirectoryPath(directory); err != nil {
		return err
	}
	return syncManagedCertificateDirectoryIfPresent(root)
}

func validateManagedCertificateLegacyTreeOwnership(directory, domain string) error {
	for _, name := range []string{"cert", "key"} {
		if _, err := readManagedCertificateRegularFile(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	generationsDirectory := filepath.Join(directory, "generations")
	if err := validateManagedCertificateRegularDirectory(generationsDirectory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(generationsDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		generationID := entry.Name()
		if !strings.HasPrefix(generationID, "gen-") && !strings.HasPrefix(generationID, "legacy-") {
			continue
		}
		if !isSafeSinglePathComponent(generationID) {
			return errors.New("legacy managed certificate generation id is invalid")
		}
		generationDirectory := filepath.Join(generationsDirectory, generationID)
		if err := validateManagedCertificateRegularDirectory(generationDirectory); err != nil {
			return err
		}
		manifestJSON, err := readManagedCertificateRegularFile(filepath.Join(generationDirectory, "manifest.json"))
		if err != nil {
			return err
		}
		var manifest managedCertificateGenerationManifest
		if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
			return err
		}
		if manifest.Version != managedCertificateGenerationManifestVersion || manifest.ID != generationID || manifest.Domain != domain {
			return ErrManagedCertificateDomainPathCollision
		}
	}
	return nil
}

func (s *GormStore) ensureManagedCertificateGenerationsDirectory(domain string) (string, error) {
	certificateDirectory, err := s.ensureManagedCertificateDirectory(domain)
	if err != nil {
		return "", err
	}
	directory := s.managedCertificateGenerationsDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(certificateDirectory) {
		return "", errors.New("managed certificate generations directory escapes its certificate directory")
	}
	if err := ensureManagedCertificateRegularDirectory(directory, 0o700, false); err != nil {
		return "", err
	}
	return directory, nil
}

func ensureManagedCertificateRegularDirectory(path string, mode os.FileMode, recursive bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if recursive {
			err = os.MkdirAll(path, mode)
		} else {
			err = os.Mkdir(path, mode)
		}
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed certificate path is not a regular directory")
	}
	return os.Chmod(path, mode)
}

func (s *GormStore) validateManagedCertificateDirectory(domain string) (string, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return "", err
	}
	root := filepath.Join(s.dataRoot, "managed_certificates")
	if err := validateManagedCertificateRegularDirectory(root); err != nil {
		return "", err
	}
	directory := s.managedCertificateDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(root) {
		return "", errors.New("managed certificate directory escapes its root")
	}
	if err := validateManagedCertificateRegularDirectory(directory); err != nil {
		return "", err
	}
	if err := validateManagedCertificateDomainMarker(directory, domain, true); err != nil {
		return "", err
	}
	return directory, nil
}

func (s *GormStore) validateManagedCertificateLegacyDirectory(domain string) (string, error) {
	domain, err := normalizeManagedCertificateGenerationDomain(domain)
	if err != nil {
		return "", err
	}
	unambiguous, err := s.managedCertificateLegacyPathIsUnambiguous(domain)
	if err != nil {
		return "", err
	}
	if !unambiguous {
		return "", ErrManagedCertificateDomainPathCollision
	}
	return s.validateManagedCertificateLegacyDirectoryUnambiguous(domain)
}

func (s *GormStore) validateManagedCertificateLegacyDirectoryUnambiguous(domain string) (string, error) {
	root := filepath.Join(s.dataRoot, "managed_certificates")
	if err := validateManagedCertificateRegularDirectory(root); err != nil {
		return "", err
	}
	directory := s.legacyManagedCertificateDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(root) {
		return "", errors.New("legacy managed certificate directory escapes its root")
	}
	if err := validateManagedCertificateRegularDirectory(directory); err != nil {
		return "", err
	}
	if err := validateManagedCertificateDomainMarker(directory, domain, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return directory, nil
}

func (s *GormStore) validateManagedCertificateGenerationsDirectory(domain string) (string, error) {
	certificateDirectory, err := s.validateManagedCertificateDirectory(domain)
	if err != nil {
		return "", err
	}
	directory := s.managedCertificateGenerationsDirectory(domain)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(certificateDirectory) {
		return "", errors.New("managed certificate generations directory escapes its certificate directory")
	}
	if err := validateManagedCertificateRegularDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}

func (s *GormStore) validateManagedCertificateGenerationDirectory(domain, generationID string) (string, error) {
	if !isSafeSinglePathComponent(generationID) {
		return "", ErrManagedCertificateGenerationNotFound
	}
	generationsDirectory, err := s.validateManagedCertificateGenerationsDirectory(domain)
	if err != nil {
		return "", err
	}
	directory := s.managedCertificateGenerationDirectory(domain, generationID)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(generationsDirectory) {
		return "", errors.New("managed certificate generation directory escapes its generations directory")
	}
	if err := validateManagedCertificateRegularDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}

func (s *GormStore) validateManagedCertificateLegacyGenerationDirectory(domain, generationID string) (string, error) {
	if !isSafeSinglePathComponent(generationID) {
		return "", ErrManagedCertificateGenerationNotFound
	}
	certificateDirectory, err := s.validateManagedCertificateLegacyDirectory(domain)
	if err != nil {
		return "", err
	}
	generationsDirectory := filepath.Join(certificateDirectory, "generations")
	if filepath.Clean(filepath.Dir(generationsDirectory)) != filepath.Clean(certificateDirectory) {
		return "", errors.New("legacy managed certificate generations directory escapes its certificate directory")
	}
	if err := validateManagedCertificateRegularDirectory(generationsDirectory); err != nil {
		return "", err
	}
	directory := filepath.Join(generationsDirectory, generationID)
	if filepath.Clean(filepath.Dir(directory)) != filepath.Clean(generationsDirectory) {
		return "", errors.New("legacy managed certificate generation directory escapes its generations directory")
	}
	if err := validateManagedCertificateRegularDirectory(directory); err != nil {
		return "", err
	}
	return directory, nil
}

func validateManagedCertificateRegularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed certificate path is not a regular directory")
	}
	return nil
}

func readManagedCertificateRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("managed certificate path is not a regular file")
	}
	return os.ReadFile(path)
}

func validateManagedCertificateProjectionDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("managed certificate projection destination is not a regular file")
	}
	return nil
}

func (s *GormStore) cleanManagedCertificateGenerationStagingDirectories(domain string) error {
	directory, err := s.validateManagedCertificateGenerationsDirectory(domain)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".stage-") || !isSafeSinglePathComponent(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			err = os.Remove(path)
		} else {
			err = os.RemoveAll(path)
		}
		if err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncManagedCertificateDirectory(directory)
	}
	return nil
}

func (s *GormStore) cleanManagedCertificateFinalizedOrphanDirectories(domain string, knownGenerationIDs map[string]struct{}) error {
	directory, err := s.validateManagedCertificateGenerationsDirectory(domain)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	removed := false
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := knownGenerationIDs[name]; ok {
			continue
		}
		if !isSafeSinglePathComponent(name) || (!strings.HasPrefix(name, "gen-") && !strings.HasPrefix(name, "legacy-")) {
			continue
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			err = os.Remove(path)
		} else {
			err = os.RemoveAll(path)
		}
		if err != nil {
			return err
		}
		removed = true
	}
	if removed {
		return syncManagedCertificateDirectory(directory)
	}
	return nil
}

func (s *GormStore) removeManagedCertificateGenerationDirectory(domain, generationID string) error {
	if !isSafeSinglePathComponent(generationID) {
		return ErrManagedCertificateGenerationNotFound
	}
	directory, err := s.validateManagedCertificateGenerationDirectory(domain, generationID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.RemoveAll(directory); err != nil {
		return err
	}
	return syncManagedCertificateDirectory(s.managedCertificateGenerationsDirectory(domain))
}

func (s *GormStore) lockManagedCertificateDomain(domain string) func() {
	key := managedCertificateDomainLockKey(s.dataRoot, domain)
	managedCertificateDomainLocks.Lock()
	entry := managedCertificateDomainLocks.entries[key]
	if entry == nil {
		entry = &managedCertificateDomainLockEntry{}
		managedCertificateDomainLocks.entries[key] = entry
	}
	entry.refs++
	managedCertificateDomainLocks.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		managedCertificateDomainLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(managedCertificateDomainLocks.entries, key)
		}
		managedCertificateDomainLocks.Unlock()
	}
}

func managedCertificateDomainLockKey(dataRoot, domain string) string {
	keyRoot := filepath.Clean(dataRoot)
	if absoluteRoot, err := filepath.Abs(keyRoot); err == nil {
		keyRoot = absoluteRoot
	}
	if runtime.GOOS == "windows" {
		keyRoot = strings.ToLower(keyRoot)
	}
	return keyRoot + "\x00legacy-" + managedCertificateDomainStorageKey(managedCertificateLegacyDomainKey(domain))
}

func managedCertificateDomainStorageKey(domain string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(domain)))
	return "v1-" + hex.EncodeToString(sum[:])
}

func managedCertificateLegacyDomainKey(domain string) string {
	return strings.ToLower(normalizeManagedCertificateHost(strings.TrimSpace(domain)))
}

func (s *GormStore) managedCertificateLegacyPathIsUnambiguous(domain string) (bool, error) {
	var rows []ManagedCertificateRow
	if err := s.db.Select("domain").Find(&rows).Error; err != nil {
		return false, err
	}
	key := managedCertificateLegacyDomainKey(domain)
	for _, row := range rows {
		other := strings.TrimSpace(row.Domain)
		if other != domain && managedCertificateLegacyDomainKey(other) == key {
			return false, nil
		}
	}
	return true, nil
}

func ensureManagedCertificateDomainMarker(directory, domain string) error {
	marker := filepath.Join(directory, managedCertificateDomainMarkerName)
	if err := validateManagedCertificateDomainMarker(directory, domain, false); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeManagedCertificateDurableFile(marker, []byte(domain), 0o600); err != nil {
		if validateErr := validateManagedCertificateDomainMarker(directory, domain, true); validateErr != nil {
			return errors.Join(err, validateErr)
		}
	}
	return syncManagedCertificateDirectory(directory)
}

func validateManagedCertificateDomainMarker(directory, domain string, required bool) error {
	marker, err := readManagedCertificateRegularFile(filepath.Join(directory, managedCertificateDomainMarkerName))
	if errors.Is(err, os.ErrNotExist) && !required {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if string(marker) != domain {
		return ErrManagedCertificateDomainPathCollision
	}
	return nil
}

func managedCertificateLegacyDirectoryOwnedBy(directory, domain string) (bool, error) {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	if !info.IsDir() {
		return false, errors.New("managed certificate path is not a regular directory")
	}
	err = validateManagedCertificateDomainMarker(directory, domain, false)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if errors.Is(err, ErrManagedCertificateDomainPathCollision) {
		return false, nil
	}
	return err == nil, err
}

func removeManagedCertificateDirectoryPath(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(directory)
	}
	return os.RemoveAll(directory)
}

func normalizeManagedCertificateGenerationDomain(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" || strings.ContainsRune(domain, '\x00') || strings.Contains(domain, "/") || strings.Contains(domain, "\\") || strings.Contains(domain, "..") {
		return "", errors.New("managed certificate generation domain is invalid")
	}
	if !isSafeSinglePathComponent(normalizeManagedCertificateHost(domain)) {
		return "", errors.New("managed certificate generation domain is invalid")
	}
	return domain, nil
}

func newManagedCertificateGenerationID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "gen-" + hex.EncodeToString(random), nil
}

func managedCertificateGenerationMaterialHash(bundle ManagedCertificateBundle) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n---\n%s", bundle.CertPEM, bundle.KeyPEM)))
	return hex.EncodeToString(sum[:])
}

func managedCertificateLegacyGenerationID(domain, materialHash string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n---\n%s", domain, materialHash)))
	return "legacy-" + hex.EncodeToString(sum[:])
}

func managedCertificateGenerationValueHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
