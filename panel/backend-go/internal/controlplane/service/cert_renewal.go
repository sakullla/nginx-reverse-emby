package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type managedCertificateRenewalIssuer interface {
	Issue(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error)
	Renew(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error)
}

const defaultManagedCertificateIssuanceLockWait = 5 * time.Second
const managedCertificateRenewalBusyRetryDelay = time.Minute

var errManagedCertificateRenewalBusy = errors.New("managed certificate renewal is already in progress")

type managedCertificateRenewalResult struct {
	Changed      bool
	LastIssueAt  string
	MaterialHash string
	NotAfter     string
	ACMEInfo     ManagedCertificateACMEInfo
	Material     storage.ManagedCertificateBundle
}

var newManagedCertificateRenewalIssuer = newMasterCFDNSManagedCertificateIssuer

func (s *certificateService) RunRenewalPass(ctx context.Context) error {
	issuer := s.renewalIssuer
	if issuer == nil && s.cfg.ManagedCloudflareDNSReady() {
		issuer = newManagedCertificateRenewalIssuer()
	}
	if issuer == nil {
		return nil
	}

	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}

	maxRevision := highestManagedCertificateRevisionForService(rows)
	var renewalErrors []error
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if !s.isManagedCertificateRenewalCandidate(cert, s.now().UTC()) {
			continue
		}

		_, renewErr := s.renewSingleCertificate(ctx, issuer, cert, rows, index, &maxRevision)
		if renewErr != nil {
			if ctx.Err() != nil {
				return errors.Join(append(renewalErrors, renewErr)...)
			}
			// One broken ACME order must not prevent the remaining due
			// certificates from being scheduled in this pass.
			renewalErrors = append(renewalErrors, renewErr)
		}

		// Always reload rows after each renewal attempt, even when this
		// goroutine skipped (changed=false). Another goroutine may have
		// renewed a different certificate and saved its rows snapshot;
		// continuing with our stale snapshot would overwrite those changes.
		rows, err = s.store.ListManagedCertificates(ctx)
		if err != nil {
			return errors.Join(append(renewalErrors, err)...)
		}
		maxRevision = highestManagedCertificateRevisionForService(rows)
	}
	return errors.Join(renewalErrors...)
}

func (s *certificateService) renewSingleCertificate(
	ctx context.Context,
	issuer managedCertificateRenewalIssuer,
	cert ManagedCertificate,
	rows []storage.ManagedCertificateRow,
	index int,
	maxRevision *int,
) (bool, error) {
	lockTimeout := s.managedCertificateIssuanceLockWaitTimeout()
	lockCtx, cancelLock := context.WithTimeout(ctx, lockTimeout)
	unlock, lockErr := issuanceLockContext(lockCtx, cert.ID)
	cancelLock()
	if lockErr != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Another issue/renew operation owns this certificate. It has its own
		// ACME deadline; skip this attempt instead of recording a false failure
		// against an operation that is still in flight. Persist a short retry
		// window so a lock collision cannot silently defer renewal to the next
		// daily interval.
		if retryErr := s.scheduleManagedCertificateRenewalRetry(ctx, cert); retryErr != nil {
			return false, errors.Join(
				fmt.Errorf("renew certificate %d skipped: %w", cert.ID, errManagedCertificateRenewalBusy),
				retryErr,
			)
		}
		return false, fmt.Errorf("renew certificate %d skipped: %w", cert.ID, errManagedCertificateRenewalBusy)
	}
	defer unlock()

	// Re-read candidate state from storage after acquiring the
	// per-certificate lock; another goroutine (e.g. manual Issue
	// API or another renewal pass) may have already renewed it.
	freshRows, refreshErr := s.store.ListManagedCertificates(ctx)
	if refreshErr != nil {
		return false, refreshErr
	}
	freshCert, freshIndex, freshFound := findManagedCertificateByID(freshRows, cert.ID)
	if !freshFound {
		return false, nil
	}
	if !s.isManagedCertificateRenewalCandidate(freshCert, s.now().UTC()) {
		return false, nil
	}
	if pending, err := s.hasPendingManagedCertificateGeneration(ctx, freshCert.Domain); err != nil {
		return false, err
	} else if pending {
		return false, nil
	}
	rows = freshRows
	cert = freshCert
	index = freshIndex
	if currentMax := highestManagedCertificateRevisionForService(rows); currentMax > *maxRevision {
		*maxRevision = currentMax
	}

	result, err := s.runManagedCertificateACMEOperation(ctx, cert.Domain, func(operationCtx context.Context) (managedCertificateRenewalResult, error) {
		return issuer.Renew(operationCtx, cert)
	})
	if err != nil {
		if _, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index); saveErr != nil {
			return false, saveErr
		}
		return false, fmt.Errorf("renew certificate %d: %w", cert.ID, err)
	}

	var issuedMaterial storage.ManagedCertificateBundle
	if result.Changed {
		issuedMaterial, err = resolveManagedCertificateIssueMaterial(cert, result)
		if err != nil {
			if _, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index); saveErr != nil {
				return false, saveErr
			}
			return false, fmt.Errorf("renew certificate %d: %w", cert.ID, err)
		}
	}
	next, persisted, err := s.persistManagedCertificateRenewalResult(ctx, rows, index, cert, result, issuedMaterial)
	if err != nil {
		if managedCertificateMaterialRestoreFailed(err) {
			return false, err
		}
		if _, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index); saveErr != nil {
			return false, saveErr
		}
		return false, fmt.Errorf("persist renewed certificate %s: %w", cert.Domain, err)
	}
	if next.Revision > *maxRevision {
		*maxRevision = next.Revision
	}
	return result.Changed && persisted, nil
}

func (s *certificateService) persistManagedCertificateRenewalResult(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	result managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, bool, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, false, err
	}
	expectedGeneration := managedCertificateGenerationFor(current)
	freshRows, fresh, freshIndex, matched, err := s.loadManagedCertificateGeneration(ctx, current.ID, expectedGeneration)
	if err != nil {
		return ManagedCertificate{}, false, err
	}
	if !matched {
		return fresh, false, nil
	}
	rows, current, targetIndex = freshRows, fresh, freshIndex
	if s.mutationExecutor == nil || s.revisionMutation {
		next, persistErr := s.persistManagedCertificateRenewalResultLegacy(ctx, rows, targetIndex, current, result, issuedMaterial)
		return next, !managedCertificateEqual(current, next), persistErr
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, current)
	if err != nil {
		return ManagedCertificate{}, false, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	next := current
	var persisted bool
	var mutationResult revision.MutationResult
	request := revision.MutationRequest{
		Kind:             "certificate.renew.complete",
		DependencyAction: revision.DependencyActionApply,
		Request: map[string]any{
			"id": current.ID, "domain": current.Domain,
			"changed": result.Changed, "material_hash": result.MaterialHash,
		},
		Targets:       configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: managedCertificateMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			freshRows, loadErr := tx.ListManagedCertificates(ctx)
			if loadErr != nil {
				return loadErr
			}
			fresh, freshIndex, found := findManagedCertificateByID(freshRows, current.ID)
			if !found || !expectedGeneration.Matches(fresh) || !s.isManagedCertificateRenewalCandidate(fresh, s.now().UTC()) {
				next = fresh
				return nil
			}
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			persisted = true
			next, loadErr = txService.persistManagedCertificateRenewalResultLegacy(
				ctx, freshRows, freshIndex, fresh, result, issuedMaterial,
			)
			return loadErr
		},
	}
	err = s.withManagedCertificateDomainLock(ctx, current.Domain, func(lockedCtx context.Context) error {
		var mutationErr error
		mutationResult, mutationErr = s.mutationExecutor.Execute(lockedCtx, request)
		if mutationErr != nil {
			return certificateMutationRollbackError(mutationErr, rollbackActions)
		}
		return nil
	})
	if err != nil {
		return ManagedCertificate{}, false, err
	}
	runConfigPostCommitActions(postCommitActions)
	if !persisted {
		return next, false, nil
	}
	if mutationResult.NoOp {
		current, loadErr := s.certificateByID(ctx, "", current.ID)
		return current, false, loadErr
	}
	return next, true, nil
}

func (s *certificateService) persistManagedCertificateRenewalResultLegacy(
	ctx context.Context,
	rows []storage.ManagedCertificateRow,
	targetIndex int,
	current ManagedCertificate,
	result managedCertificateRenewalResult,
	issuedMaterial storage.ManagedCertificateBundle,
) (ManagedCertificate, error) {
	if targetIndex < 0 || targetIndex >= len(rows) {
		return current, nil
	}
	fresh := managedCertificateFromRow(rows[targetIndex])
	if !managedCertificateGenerationFor(current).Matches(fresh) {
		return fresh, nil
	}
	current = fresh
	if result.Changed && s.generationStore != nil {
		return s.persistManagedCertificateRenewalResultGeneration(ctx, rows, targetIndex, current, result, issuedMaterial)
	}
	var restore func() error
	var commitMaterial func()
	materialRegistered := false
	if result.Changed {
		var err error
		commitMaterial, restore, err = stageManagedCertificateMaterialWithRollback(ctx, s.store, current.Domain, issuedMaterial)
		if err != nil {
			return ManagedCertificate{}, err
		}
		materialRegistered = s.revisionMutation && s.postCommitActions != nil && s.rollbackActions != nil
		if materialRegistered {
			*s.postCommitActions = append(*s.postCommitActions, commitMaterial)
			*s.rollbackActions = append(*s.rollbackActions, restore)
		}
	}

	now := s.now().UTC()
	next := current
	next.Status = "active"
	next.LastError = ""
	next.BackoffClass = ""
	next.RetryCount = 0
	next.NextRetryAtUnix = 0
	if result.Changed {
		if result.LastIssueAt != "" {
			next.LastIssueAt = result.LastIssueAt
		} else {
			next.LastIssueAt = now.Format(time.RFC3339)
		}
		next.NotAfter = result.NotAfter
		if next.NotAfter == "" {
			next.NotAfter = managedCertificateNotAfterFromPEM(issuedMaterial.CertPEM, current.NotAfter)
		}
	}
	if result.MaterialHash != "" {
		next.MaterialHash = result.MaterialHash
	} else if result.Changed {
		next.MaterialHash = hashManagedCertificateMaterial(issuedMaterial.CertPEM, issuedMaterial.KeyPEM)
	}
	if !isZeroManagedCertificateACMEInfo(result.ACMEInfo) {
		next.ACMEInfo = result.ACMEInfo
	}
	if result.Changed {
		next.Revision = highestManagedCertificateRevisionForService(rows) + 1
	}
	next.Revision = s.certificateMutationRevision(next.Revision)
	if managedCertificateEqual(current, next) {
		if commitMaterial != nil && !materialRegistered {
			commitMaterial()
		}
		return current, nil
	}
	originalRows := append([]storage.ManagedCertificateRow(nil), rows...)
	rows[targetIndex] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if restore != nil && !materialRegistered {
			if restoreErr := restore(); restoreErr != nil {
				return ManagedCertificate{}, &managedCertificateMaterialRestoreError{
					writeErr:   fmt.Errorf("save renewed certificate metadata for %s: %w", current.Domain, err),
					restoreErr: restoreErr,
				}
			}
		}
		return ManagedCertificate{}, err
	}
	if commitMaterial != nil && !materialRegistered {
		commitMaterial()
	}
	s.cleanupManagedCertificateMaterialAfterMutation(ctx, originalRows, rows)
	if result.Changed {
		if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
			return ManagedCertificate{}, err
		}
	}
	return next, nil
}

func (s *certificateService) isManagedCertificateRenewalCandidate(cert ManagedCertificate, now time.Time) bool {
	if !s.isManagedCertificateRenewalTarget(cert) {
		return false
	}
	// Honor failure backoff recorded by the issue/renew failure paths: a cert whose next retry is
	// still in the future is skipped until NextRetryAtUnix elapses. This replaces the old behavior
	// of blindly retrying certs with no Renew date on every pass (R5①). A zero/cleared
	// NextRetryAtUnix (fresh cert, successful issue, or legacy row) is not skipped. A cert left in
	// "issuing" by the background signer remains a candidate as a crash/restart fallback: the
	// per-cert issuanceLock serializes it, and the fresh re-check below re-evaluates candidacy
	// after the lock so a finalized cert is never double-processed.
	if cert.NextRetryAtUnix > 0 && now.Unix() < cert.NextRetryAtUnix {
		return false
	}
	renewAt, ok := parseManagedCertificateRenewAt(cert.ACMEInfo.Renew)
	if !ok {
		return true
	}
	return !renewAt.After(now)
}

func (s *certificateService) isManagedCertificateRenewalTarget(cert ManagedCertificate) bool {
	if !cert.Enabled || cert.Scope != "domain" || cert.IssuerMode != "master_cf_dns" || cert.CertificateType != "acme" {
		return false
	}
	if localAgentID := strings.TrimSpace(s.cfg.LocalAgentID); localAgentID != "" {
		return containsString(cert.TargetAgentIDs, localAgentID)
	}
	return true
}

// NextManagedCertificateRenewalRetryAt returns the earliest persisted failure
// retry time. A zero time means that the periodic renewal interval is enough;
// a time at or before now means the retry is due immediately.
func (s *certificateService) NextManagedCertificateRenewalRetryAt(ctx context.Context, now time.Time) (time.Time, error) {
	if now.IsZero() {
		now = s.now().UTC()
	} else {
		now = now.UTC()
	}
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return time.Time{}, err
	}
	var next time.Time
	for _, row := range rows {
		cert := managedCertificateFromRow(row)
		if !s.isManagedCertificateRenewalTarget(cert) || cert.NextRetryAtUnix <= 0 {
			continue
		}
		retryAt := time.Unix(cert.NextRetryAtUnix, 0).UTC()
		if !retryAt.After(now) {
			return now, nil
		}
		if next.IsZero() || retryAt.Before(next) {
			next = retryAt
		}
	}
	return next, nil
}

// scheduleManagedCertificateRenewalRetry records a short-lived retry for a
// transient issuance-lock collision. It deliberately preserves the
// certificate's status and error fields: the other operation still owns the
// real outcome, while the scheduler needs a durable wake-up if that outcome
// does not update the row as expected.
func (s *certificateService) scheduleManagedCertificateRenewalRetry(ctx context.Context, cert ManagedCertificate) error {
	retryAt := s.now().UTC().Add(managedCertificateRenewalBusyRetryDelay).Unix()
	expectedGeneration := managedCertificateGenerationFor(cert)
	update := func(rows []storage.ManagedCertificateRow) ([]storage.ManagedCertificateRow, bool, error) {
		current, index, found := findManagedCertificateByID(rows, cert.ID)
		if !found || !expectedGeneration.Matches(current) {
			return rows, false, nil
		}
		if current.NextRetryAtUnix >= retryAt {
			return rows, false, nil
		}
		current.NextRetryAtUnix = retryAt
		rows[index] = managedCertificateToRow(current)
		return rows, true, nil
	}

	if atomicStore, ok := s.store.(storage.ManagedCertificateUpdateStore); ok {
		return atomicStore.UpdateManagedCertificates(ctx, update)
	}
	rows, err := s.store.ListManagedCertificates(ctx)
	if err != nil {
		return err
	}
	nextRows, changed, err := update(rows)
	if err != nil || !changed {
		return err
	}
	return s.store.SaveManagedCertificates(ctx, nextRows)
}

func (s *certificateService) managedCertificateACMETimeout() time.Duration {
	if s.cfg.ManagedCertificateACMETimeout > 0 {
		return s.cfg.ManagedCertificateACMETimeout
	}
	return config.Default().ManagedCertificateACMETimeout
}

func (s *certificateService) managedCertificateIssuanceLockWaitTimeout() time.Duration {
	lockTimeout := defaultManagedCertificateIssuanceLockWait
	if acmeTimeout := s.managedCertificateACMETimeout(); acmeTimeout < lockTimeout {
		lockTimeout = acmeTimeout
	}
	return lockTimeout
}

// runManagedCertificateACMEOperation gives every ACME order a finite lifetime.
// The parent context remains available to persist the failure after a timeout,
// so a timed-out order is recorded as an error and the scheduler can continue.
func (s *certificateService) runManagedCertificateACMEOperation(
	ctx context.Context,
	domain string,
	operation func(context.Context) (managedCertificateRenewalResult, error),
) (managedCertificateRenewalResult, error) {
	timeout := s.managedCertificateACMETimeout()
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	result, err := operation(operationCtx)
	operationErr := operationCtx.Err()
	parentErr := ctx.Err()
	cancel()
	if parentErr != nil {
		if err == nil {
			err = parentErr
		}
		return result, err
	}
	if operationErr == context.DeadlineExceeded {
		return result, fmt.Errorf("managed certificate ACME operation for %s timed out after %s: %w", domain, timeout, context.DeadlineExceeded)
	}
	return result, err
}

// applyManagedCertificateRenewalFailureBackoff records the failure backoff fields on a renewal
// attempt failure, mirroring failManagedCertificateIssue in certs.go so the async issue path and
// the renewal loop share one backoff contract (and isManagedCertificateRenewalCandidate reads the
// same NextRetryAtUnix). It only touches the backoff fields; callers still own Status/LastError.
func applyManagedCertificateRenewalFailureBackoff(cert ManagedCertificate, err error, now time.Time) ManagedCertificate {
	failed := cert
	class := classifyManagedCertificateIssueError(err)
	retryAfter := extractManagedCertificateRetryAfter(err)
	failed.BackoffClass = class
	failed.RetryCount = cert.RetryCount + 1
	failed.NextRetryAtUnix = now.Add(managedCertificateBackoffDelay(class, retryAfter, failed.RetryCount)).Unix()
	return failed
}

// recordManagedCertificateRenewalFailure writes the failure backoff + error state for a
// renewal attempt and persists it, centralizing the pattern that was previously repeated
// across the three failure branches of renewSingleCertificate (issuer.Renew /
// resolveManagedCertificateIssueMaterial / SaveManagedCertificateMaterial). It mirrors
// failManagedCertificateIssue (certs.go) so the async issue path and the renewal loop
// share one backoff contract. It returns the updated certificate (with backoff and error
// fields set) so the caller can drive any extra cleanup — e.g. restoring the previous
// material after a SaveMaterial failure — using the same row that was just persisted.
func (s *certificateService) recordManagedCertificateRenewalFailure(ctx context.Context, cert ManagedCertificate, failureErr error, rows []storage.ManagedCertificateRow, index int) (ManagedCertificate, error) {
	if err := requireConfigMutationStore(s.store, s.mutationExecutor, s.revisionMutation); err != nil {
		return ManagedCertificate{}, err
	}
	expectedGeneration := managedCertificateGenerationFor(cert)
	freshRows, fresh, freshIndex, matched, err := s.loadManagedCertificateGeneration(ctx, cert.ID, expectedGeneration)
	if err != nil {
		return ManagedCertificate{}, err
	}
	if !matched {
		return fresh, nil
	}
	rows, cert, index = freshRows, fresh, freshIndex
	if s.mutationExecutor == nil || s.revisionMutation {
		return s.recordManagedCertificateRenewalFailureLegacy(ctx, cert, failureErr, rows, index)
	}
	targetAgentIDs, err := s.certificateMutationTargetAgentIDs(ctx, cert)
	if err != nil {
		return ManagedCertificate{}, err
	}
	postCommitActions := make([]func(), 0)
	rollbackActions := make([]func() error, 0)
	next := cert
	persisted := false
	_, err = s.mutationExecutor.Execute(ctx, revision.MutationRequest{
		Kind:             "certificate.renew.failure",
		DependencyAction: revision.DependencyActionApply,
		Request: map[string]any{
			"id": cert.ID, "domain": cert.Domain,
			"error_class": classifyManagedCertificateIssueError(failureErr),
		},
		Targets:       configMutationTargets(s.cfg, targetAgentIDs, nil),
		ResourceState: managedCertificateMutationResourceState,
		Mutate: func(ctx context.Context, tx *storage.GormStore, revisions map[string]int64) error {
			freshRows, loadErr := tx.ListManagedCertificates(ctx)
			if loadErr != nil {
				return loadErr
			}
			fresh, freshIndex, found := findManagedCertificateByID(freshRows, cert.ID)
			if !found || !expectedGeneration.Matches(fresh) {
				next = fresh
				return nil
			}
			txService := s.certificateRevisionTransactionService(tx, revisions, &postCommitActions, &rollbackActions)
			persisted = true
			next, loadErr = txService.recordManagedCertificateRenewalFailureLegacy(ctx, fresh, failureErr, freshRows, freshIndex)
			return loadErr
		},
	})
	if err != nil {
		return ManagedCertificate{}, certificateMutationRollbackError(err, rollbackActions)
	}
	if !persisted {
		return next, nil
	}
	runConfigPostCommitActions(postCommitActions)
	return next, nil
}

func (s *certificateService) recordManagedCertificateRenewalFailureLegacy(ctx context.Context, cert ManagedCertificate, failureErr error, rows []storage.ManagedCertificateRow, index int) (ManagedCertificate, error) {
	if index < 0 || index >= len(rows) {
		return cert, nil
	}
	fresh := managedCertificateFromRow(rows[index])
	if !managedCertificateGenerationFor(cert).Matches(fresh) {
		return fresh, nil
	}
	cert = fresh
	next := applyManagedCertificateRenewalFailureBackoff(cert, failureErr, s.now().UTC())
	next.Status = "error"
	next.LastError = failureErr.Error()
	next.Revision = s.certificateMutationRevision(cert.Revision)
	rows[index] = managedCertificateToRow(next)
	if saveErr := s.store.SaveManagedCertificates(ctx, rows); saveErr != nil {
		return next, saveErr
	}
	return next, nil
}

func parseManagedCertificateRenewAt(raw string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func isZeroManagedCertificateACMEInfo(info ManagedCertificateACMEInfo) bool {
	return info == (ManagedCertificateACMEInfo{})
}

func highestManagedCertificateRevisionForService(rows []storage.ManagedCertificateRow) int {
	maxRevision := 0
	for _, row := range rows {
		if row.Revision > maxRevision {
			maxRevision = row.Revision
		}
	}
	return maxRevision
}
