package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type managedCertificatePendingSnapshotStore interface {
	storage.ManagedCertificateGenerationStore
	ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error)
}

func newManagedCertificateMutationExecutor(cfg config.Config, store storage.Store) *revision.Executor {
	revisionStore, ok := store.(revision.Store)
	if !ok {
		return nil
	}
	return NewMutationExecutor(
		revisionStore,
		revision.WithSnapshotBuilder(revision.SnapshotBuilderFunc(func(ctx context.Context, tx *storage.GormStore, target revision.Target) (storage.Snapshot, error) {
			snapshot, err := loadManagedCertificateMutationSnapshot(ctx, tx, target)
			if err != nil {
				return storage.Snapshot{}, err
			}
			return overlayPendingManagedCertificateGenerationsForConfig(ctx, cfg, tx, target.AgentID, snapshot)
		})),
	)
}

func loadManagedCertificateMutationSnapshot(ctx context.Context, store *storage.GormStore, target revision.Target) (storage.Snapshot, error) {
	if target.Local {
		return store.LoadLocalSnapshot(ctx, target.AgentID)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}
	for _, agent := range agents {
		if agent.ID != target.AgentID {
			continue
		}
		desiredVersion := strings.TrimSpace(target.DesiredVersion)
		if desiredVersion == "" {
			desiredVersion = agent.DesiredVersion
		}
		platform := strings.TrimSpace(target.Platform)
		if platform == "" {
			platform = agent.Platform
		}
		return store.LoadAgentSnapshot(ctx, target.AgentID, storage.AgentSnapshotInput{
			DesiredVersion:  desiredVersion,
			DesiredRevision: agent.DesiredRevision,
			CurrentRevision: agent.CurrentRevision,
			Platform:        platform,
		})
	}
	return storage.Snapshot{}, fmt.Errorf("agent %q was not found", target.AgentID)
}

// OverlayPendingManagedCertificateGenerations publishes pending material only in
// the target agent's desired snapshot. The storage active projection remains
// untouched until every target reports the exact installed material hash.
func OverlayPendingManagedCertificateGenerations(ctx context.Context, store any, agentID string, snapshot storage.Snapshot) (storage.Snapshot, error) {
	pendingStore, ok := store.(managedCertificatePendingSnapshotStore)
	if !ok {
		return snapshot, nil
	}
	return overlayPendingManagedCertificateGenerations(ctx, pendingStore, agentID, snapshot)
}

// OverlayPendingManagedCertificateGenerationsForConfig applies the same
// fallback target resolution used by revision mutations.
func OverlayPendingManagedCertificateGenerationsForConfig(ctx context.Context, cfg config.Config, store any, agentID string, snapshot storage.Snapshot) (storage.Snapshot, error) {
	return overlayPendingManagedCertificateGenerationsForConfig(ctx, cfg, store, agentID, snapshot)
}

func overlayPendingManagedCertificateGenerationsForConfig(ctx context.Context, cfg config.Config, store any, agentID string, snapshot storage.Snapshot) (storage.Snapshot, error) {
	pendingStore, ok := store.(managedCertificatePendingSnapshotStore)
	if !ok {
		return snapshot, nil
	}
	fullStore, ok := store.(storage.Store)
	if !ok {
		return overlayPendingManagedCertificateGenerations(ctx, pendingStore, agentID, snapshot)
	}
	resolver := &certificateService{cfg: cfg, store: fullStore}
	return overlayPendingManagedCertificateGenerationsWithResolver(ctx, pendingStore, agentID, snapshot, func(cert ManagedCertificate) (bool, error) {
		targetAgentIDs, err := resolver.certificateMutationTargetAgentIDs(ctx, cert)
		return containsString(targetAgentIDs, agentID), err
	})
}

func overlayPendingManagedCertificateGenerations(ctx context.Context, store managedCertificatePendingSnapshotStore, agentID string, snapshot storage.Snapshot) (storage.Snapshot, error) {
	return overlayPendingManagedCertificateGenerationsWithResolver(ctx, store, agentID, snapshot, nil)
}

func overlayPendingManagedCertificateGenerationsWithResolver(
	ctx context.Context,
	store managedCertificatePendingSnapshotStore,
	agentID string,
	snapshot storage.Snapshot,
	resolve func(ManagedCertificate) (bool, error),
) (storage.Snapshot, error) {
	rows, err := store.ListManagedCertificates(ctx)
	if err != nil {
		return storage.Snapshot{}, err
	}

	next := snapshot
	next.Certificates = append([]storage.ManagedCertificateBundle(nil), snapshot.Certificates...)
	next.CertificatePolicies = append([]storage.ManagedCertificatePolicy(nil), snapshot.CertificatePolicies...)
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if !cert.Enabled || cert.Scope != "domain" || cert.IssuerMode != "master_cf_dns" {
			continue
		}
		pending, found, err := store.LoadPendingManagedCertificateGeneration(ctx, cert.Domain)
		if err != nil {
			return storage.Snapshot{}, err
		}
		if !found {
			continue
		}
		relevant := pendingManagedCertificateRelevantToSnapshot(cert, agentID, next)
		if !relevant && resolve != nil {
			relevant, err = resolve(cert)
			if err != nil {
				return storage.Snapshot{}, err
			}
		}
		if !relevant {
			continue
		}

		bundle := pending.Material
		bundle.ID = cert.ID
		bundle.Domain = cert.Domain
		bundle.Revision = int64(cert.Revision)
		next.Certificates = upsertManagedCertificateBundle(next.Certificates, bundle)
		next.CertificatePolicies = upsertManagedCertificatePolicy(next.CertificatePolicies, managedCertificatePendingPolicy(cert))
	}
	sort.Slice(next.Certificates, func(i, j int) bool {
		if next.Certificates[i].ID == next.Certificates[j].ID {
			return next.Certificates[i].Domain < next.Certificates[j].Domain
		}
		return next.Certificates[i].ID < next.Certificates[j].ID
	})
	sort.Slice(next.CertificatePolicies, func(i, j int) bool {
		if next.CertificatePolicies[i].ID == next.CertificatePolicies[j].ID {
			return next.CertificatePolicies[i].Domain < next.CertificatePolicies[j].Domain
		}
		return next.CertificatePolicies[i].ID < next.CertificatePolicies[j].ID
	})
	return next, nil
}

func pendingManagedCertificateRelevantToSnapshot(cert ManagedCertificate, agentID string, snapshot storage.Snapshot) bool {
	if containsString(cert.TargetAgentIDs, strings.TrimSpace(agentID)) {
		return true
	}
	for _, bundle := range snapshot.Certificates {
		if bundle.ID == cert.ID || strings.EqualFold(strings.TrimSpace(bundle.Domain), strings.TrimSpace(cert.Domain)) {
			return true
		}
	}
	for _, policy := range snapshot.CertificatePolicies {
		if policy.ID == cert.ID || strings.EqualFold(strings.TrimSpace(policy.Domain), strings.TrimSpace(cert.Domain)) {
			return true
		}
	}
	for _, rule := range snapshot.Rules {
		parsed, err := url.Parse(strings.TrimSpace(rule.FrontendURL))
		if err == nil && parsed != nil && strings.EqualFold(parsed.Scheme, "https") && doesManagedCertificateMatchHost(cert, parsed.Hostname()) {
			return true
		}
	}
	for _, listener := range snapshot.RelayListeners {
		if listener.CertificateID != nil && *listener.CertificateID == cert.ID {
			return true
		}
		for _, trustedID := range listener.TrustedCACertificateIDs {
			if trustedID == cert.ID {
				return true
			}
		}
	}
	return false
}

func upsertManagedCertificateBundle(bundles []storage.ManagedCertificateBundle, pending storage.ManagedCertificateBundle) []storage.ManagedCertificateBundle {
	for index := range bundles {
		if bundles[index].ID == pending.ID || strings.EqualFold(strings.TrimSpace(bundles[index].Domain), strings.TrimSpace(pending.Domain)) {
			bundles[index] = pending
			return bundles
		}
	}
	return append(bundles, pending)
}

func upsertManagedCertificatePolicy(policies []storage.ManagedCertificatePolicy, pending storage.ManagedCertificatePolicy) []storage.ManagedCertificatePolicy {
	for index := range policies {
		if policies[index].ID == pending.ID || strings.EqualFold(strings.TrimSpace(policies[index].Domain), strings.TrimSpace(pending.Domain)) {
			policies[index] = pending
			return policies
		}
	}
	return append(policies, pending)
}

func managedCertificatePendingPolicy(cert ManagedCertificate) storage.ManagedCertificatePolicy {
	status := strings.TrimSpace(cert.Status)
	if status == "" {
		status = storage.ManagedCertificateGenerationStatePending
	}
	return storage.ManagedCertificatePolicy{
		ID: cert.ID, Domain: cert.Domain, Enabled: cert.Enabled, Scope: cert.Scope,
		IssuerMode: cert.IssuerMode, Status: status, LastIssueAt: cert.LastIssueAt,
		LastError: cert.LastError, Tags: append([]string(nil), cert.Tags...), Revision: int64(cert.Revision),
		Usage: cert.Usage, CertificateType: cert.CertificateType, SelfSigned: cert.SelfSigned,
		ACMEInfo: storage.ManagedCertificateACMEInfo{
			MainDomain: cert.ACMEInfo.MainDomain, KeyLength: cert.ACMEInfo.KeyLength,
			SANDomains: cert.ACMEInfo.SANDomains, Profile: cert.ACMEInfo.Profile,
			CA: cert.ACMEInfo.CA, Created: cert.ACMEInfo.Created, Renew: cert.ACMEInfo.Renew,
		},
	}
}

func (s *certificateService) persistManagedCertificateIssueSuccessGeneration(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	issueResult managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, error) {
	active, activeFound, err := s.generationStore.LoadActiveManagedCertificateGeneration(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	next := current
	setManagedCertificateGenerationSuccessMetadata(&next, issueResult, issuedMaterial, s.now().UTC())
	if activeFound {
		next.Status = "active"
		next.MaterialHash = active.MaterialHash
	} else {
		next.Status = storage.ManagedCertificateGenerationStatePending
		next.MaterialHash = ""
	}
	next.AgentReports = map[string]ManagedCertificateAgentReport{}
	next.Revision = s.certificateMutationRevision(highestManagedCertificateRevisionForService(rows) + 1)

	issuedMaterial.ID = next.ID
	issuedMaterial.Domain = next.Domain
	issuedMaterial.Revision = int64(next.Revision)
	_, rollback, rollbackRegistered, err := s.stageManagedCertificateGeneration(ctx, next.Domain, issuedMaterial)
	if err != nil {
		return ManagedCertificate{}, err
	}
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if !rollbackRegistered {
			if rollbackErr := rollback(); rollbackErr != nil {
				return ManagedCertificate{}, errors.Join(err, rollbackErr)
			}
		}
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func (s *certificateService) persistManagedCertificateRenewalResultGeneration(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	result managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, error) {
	active, activeFound, err := s.generationStore.LoadActiveManagedCertificateGeneration(ctx, current.Domain)
	if err != nil {
		return ManagedCertificate{}, err
	}
	next := current
	setManagedCertificateGenerationSuccessMetadata(&next, result, issuedMaterial, s.now().UTC())
	if activeFound {
		next.Status = "active"
		next.MaterialHash = active.MaterialHash
	} else {
		next.Status = storage.ManagedCertificateGenerationStatePending
		next.MaterialHash = ""
	}
	next.AgentReports = map[string]ManagedCertificateAgentReport{}
	next.Revision = s.certificateMutationRevision(highestManagedCertificateRevisionForService(rows) + 1)

	issuedMaterial.ID = next.ID
	issuedMaterial.Domain = next.Domain
	issuedMaterial.Revision = int64(next.Revision)
	_, rollback, rollbackRegistered, err := s.stageManagedCertificateGeneration(ctx, next.Domain, issuedMaterial)
	if err != nil {
		return ManagedCertificate{}, err
	}
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if !rollbackRegistered {
			if rollbackErr := rollback(); rollbackErr != nil {
				return ManagedCertificate{}, errors.Join(err, rollbackErr)
			}
		}
		return ManagedCertificate{}, err
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
		return ManagedCertificate{}, err
	}
	return next, nil
}

func setManagedCertificateGenerationSuccessMetadata(next *ManagedCertificate, result managedCertificateRenewalResult, material storage.ManagedCertificateBundle, now time.Time) {
	next.LastError = ""
	next.BackoffClass = ""
	next.RetryCount = 0
	next.NextRetryAtUnix = 0
	if strings.TrimSpace(result.LastIssueAt) != "" {
		next.LastIssueAt = result.LastIssueAt
	} else {
		next.LastIssueAt = now.Format(time.RFC3339)
	}
	next.NotAfter = result.NotAfter
	if strings.TrimSpace(next.NotAfter) == "" {
		next.NotAfter = managedCertificateNotAfterFromPEM(material.CertPEM, next.NotAfter)
	}
	if !isZeroManagedCertificateACMEInfo(result.ACMEInfo) {
		next.ACMEInfo = result.ACMEInfo
	}
}

func (s *certificateService) stageManagedCertificateGeneration(ctx context.Context, domain string, bundle storage.ManagedCertificateBundle) (storage.ManagedCertificateGeneration, func() error, bool, error) {
	generation, err := s.generationStore.StageManagedCertificateGeneration(ctx, domain, bundle)
	if err != nil {
		return storage.ManagedCertificateGeneration{}, nil, false, err
	}
	recoveryStore := s.generationRecoveryStore
	if recoveryStore == nil {
		recoveryStore = s.generationStore
	}
	var once sync.Once
	var rollbackErr error
	rollback := func() error {
		once.Do(func() {
			cleanupCtx, cancel := managedCertificateMaterialCleanupContext(ctx)
			defer cancel()
			abortErr := recoveryStore.AbortManagedCertificateGeneration(cleanupCtx, domain, generation.ID)
			if errors.Is(abortErr, storage.ErrManagedCertificateGenerationNotFound) {
				abortErr = nil
			}
			reconcileErr := recoveryStore.ReconcileManagedCertificateGenerations(cleanupCtx, domain)
			rollbackErr = errors.Join(abortErr, reconcileErr)
		})
		return rollbackErr
	}
	registered := s.revisionMutation && s.rollbackActions != nil
	if registered {
		*s.rollbackActions = append(*s.rollbackActions, rollback)
	}
	return generation, rollback, registered, nil
}

func (s *certificateService) hasPendingManagedCertificateGeneration(ctx context.Context, domain string) (bool, error) {
	if s.generationStore == nil {
		return false, nil
	}
	_, found, err := s.generationStore.LoadPendingManagedCertificateGeneration(ctx, domain)
	return found, err
}

func (s *certificateService) reconcileManagedCertificateGenerationPromotions(ctx context.Context) (int, error) {
	if s.generationStore == nil {
		return 0, nil
	}
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return 0, err
	}
	promoted := 0
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if !cert.Enabled || cert.Scope != "domain" || cert.IssuerMode != "master_cf_dns" {
			continue
		}
		pending, found, err := s.generationStore.LoadPendingManagedCertificateGeneration(ctx, cert.Domain)
		if err != nil {
			return promoted, err
		}
		if !found {
			continue
		}
		targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, cert)
		if err != nil {
			return promoted, err
		}
		if !managedCertificateGenerationAcknowledged(cert, targetAgentIDs, pending) {
			continue
		}
		if err := s.promoteManagedCertificateGeneration(ctx, cert, pending, targetAgentIDs); err != nil {
			if errors.Is(err, storage.ErrManagedCertificateGenerationNotFound) {
				continue
			}
			return promoted, err
		}
		promoted++
	}
	return promoted, nil
}

func managedCertificateGenerationAcknowledged(cert ManagedCertificate, targetAgentIDs []string, pending storage.ManagedCertificateGeneration) bool {
	createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(pending.CreatedAt))
	if err != nil || strings.TrimSpace(pending.MaterialHash) == "" || len(targetAgentIDs) == 0 {
		return false
	}
	for _, agentID := range targetAgentIDs {
		report, ok := cert.AgentReports[strings.TrimSpace(agentID)]
		if !ok || report.Status != "active" || report.MaterialHash != pending.MaterialHash {
			return false
		}
		updatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(report.UpdatedAt))
		if err != nil || updatedAt.Before(createdAt) {
			return false
		}
	}
	return true
}

func (s *certificateService) promoteManagedCertificateGeneration(ctx context.Context, current ManagedCertificate, pending storage.ManagedCertificateGeneration, targetAgentIDs []string) error {
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.promoteManagedCertificateGenerationLegacy(ctx, current, pending)
	}

	postCommitActions := make([]func(), 0, 1)
	rollbackActions := make([]func() error, 0, 1)
	persisted := false
	_, err := s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "certificate.generation.promote",
		DependencyAction: revision.DependencyActionApply,
		Request: map[string]any{
			"id": current.ID, "domain": current.Domain,
			"generation_id": pending.ID, "material_hash": pending.MaterialHash,
		},
		Targets:       configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: managedCertificateMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			txGenerationStore := storage.ManagedCertificateGenerationStore(tx)
			freshPending, found, loadErr := txGenerationStore.LoadPendingManagedCertificateGeneration(ctx, current.Domain)
			if loadErr != nil {
				return loadErr
			}
			if !found || freshPending.ID != pending.ID || freshPending.MaterialHash != pending.MaterialHash {
				return storage.ErrManagedCertificateGenerationNotFound
			}
			rows, loadErr := tx.ListManagedCertificates(ctx)
			if loadErr != nil {
				return loadErr
			}
			fresh, index, found := findManagedCertificateByID(rows, current.ID)
			if !found || !managedCertificateGenerationAcknowledged(fresh, targetAgentIDs, freshPending) {
				return storage.ErrManagedCertificateGenerationNotFound
			}
			rollbackActions = append(rollbackActions, s.reconcileManagedCertificateGenerationRollback(current.Domain))
			if promoteErr := txGenerationStore.PromoteManagedCertificateGeneration(ctx, current.Domain, freshPending.ID, freshPending.MaterialHash); promoteErr != nil {
				return promoteErr
			}
			fresh.Status = "active"
			fresh.MaterialHash = freshPending.MaterialHash
			fresh.LastError = ""
			fresh.Revision = maxConfigMutationRevision(revisions, highestManagedCertificateRevisionForService(rows)+1)
			rows[index] = managedCertificateToRow(fresh)
			if saveErr := tx.SaveManagedCertificates(ctx, rows); saveErr != nil {
				return saveErr
			}
			persisted = true
			return nil
		},
	})
	if err != nil {
		return certificateMutationRollbackError(err, rollbackActions)
	}
	if !persisted {
		return nil
	}
	cleanupStore := s.generationRecoveryStore
	if cleanupStore == nil {
		cleanupStore = s.generationStore
	}
	postCommitActions = append(postCommitActions, func() {
		cleanupCtx, cancel := managedCertificateMaterialCleanupContext(ctx)
		defer cancel()
		if cleanupStore != nil {
			_ = cleanupStore.GarbageCollectManagedCertificateGenerations(cleanupCtx, current.Domain)
		}
	})
	runConfigPostCommitActions(postCommitActions)
	return nil
}

func (s *certificateService) promoteManagedCertificateGenerationLegacy(ctx context.Context, current ManagedCertificate, pending storage.ManagedCertificateGeneration) error {
	if err := s.generationStore.PromoteManagedCertificateGeneration(ctx, current.Domain, pending.ID, pending.MaterialHash); err != nil {
		return err
	}
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	fresh, index, found := findManagedCertificateByID(rows, current.ID)
	if !found {
		return storage.ErrManagedCertificateGenerationNotFound
	}
	fresh.Status = "active"
	fresh.MaterialHash = pending.MaterialHash
	fresh.LastError = ""
	fresh.Revision = highestManagedCertificateRevisionForService(rows) + 1
	rows[index] = managedCertificateToRow(fresh)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		return err
	}
	_ = s.generationStore.GarbageCollectManagedCertificateGenerations(ctx, current.Domain)
	return nil
}

func (s *certificateService) reconcileManagedCertificateGenerationRollback(domain string) func() error {
	recoveryStore := s.generationRecoveryStore
	if recoveryStore == nil {
		recoveryStore = s.generationStore
	}
	return func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), managedCertificateMaterialCleanupTimeout)
		defer cancel()
		return recoveryStore.ReconcileManagedCertificateGenerations(cleanupCtx, domain)
	}
}
