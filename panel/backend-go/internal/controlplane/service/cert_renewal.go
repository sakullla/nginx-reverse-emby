package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type managedCertificateRenewalIssuer interface {
	Issue(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error)
	Renew(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error)
}

type managedCertificateRenewalResult struct {
	Changed      bool
	LastIssueAt  string
	MaterialHash string
	ACMEInfo     ManagedCertificateACMEInfo
	Material     storage.ManagedCertificateBundle
}

var newManagedCertificateRenewalIssuer = newMasterCFDNSManagedCertificateIssuer

func (s *certificateService) RunRenewalPass(ctx context.Context) error {
	issuer := s.renewalIssuer
	if issuer == nil && s.cfg.ManagedDNSCertificatesEnabled {
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
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if !s.isManagedCertificateRenewalCandidate(cert, s.now().UTC()) {
			continue
		}

		_, err := s.renewSingleCertificate(ctx, issuer, cert, rows, index, &maxRevision)
		if err != nil {
			return err
		}

		// Always reload rows after each renewal attempt, even when this
		// goroutine skipped (changed=false). Another goroutine may have
		// renewed a different certificate and saved its rows snapshot;
		// continuing with our stale snapshot would overwrite those changes.
		rows, err = s.store.ListManagedCertificates(ctx)
		if err != nil {
			return err
		}
		maxRevision = highestManagedCertificateRevisionForService(rows)
	}
	return nil
}

func (s *certificateService) renewSingleCertificate(
	ctx context.Context,
	issuer managedCertificateRenewalIssuer,
	cert ManagedCertificate,
	rows []storage.ManagedCertificateRow,
	index int,
	maxRevision *int,
) (bool, error) {
	unlock := issuanceLock(cert.ID)
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
	rows = freshRows
	cert = freshCert
	index = freshIndex
	if currentMax := highestManagedCertificateRevisionForService(rows); currentMax > *maxRevision {
		*maxRevision = currentMax
	}

	result, err := issuer.Renew(ctx, cert)
	if err != nil {
		if _, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index); saveErr != nil {
			return false, saveErr
		}
		return false, fmt.Errorf("renew certificate %d: %w", cert.ID, err)
	}

	var issuedMaterial storage.ManagedCertificateBundle
	var previousMaterial storage.ManagedCertificateBundle
	previousMaterialFound := false
	if result.Changed {
		issuedMaterial, err = resolveManagedCertificateIssueMaterial(cert, result)
		if err != nil {
			if _, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index); saveErr != nil {
				return false, saveErr
			}
			return false, fmt.Errorf("renew certificate %d: %w", cert.ID, err)
		}

		previousMaterial, previousMaterialFound, err = s.store.LoadManagedCertificateMaterial(ctx, cert.Domain)
		if err != nil {
			return false, err
		}
		if err := s.store.SaveManagedCertificateMaterial(ctx, cert.Domain, issuedMaterial); err != nil {
			next, saveErr := s.recordManagedCertificateRenewalFailure(ctx, cert, err, rows, index)
			if saveErr != nil {
				return false, saveErr
			}
			if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, next, previousMaterial, previousMaterialFound); restoreErr != nil {
				return false, fmt.Errorf("persist renewed certificate material for %s: %w (restore failed: %v)", cert.Domain, err, restoreErr)
			}
			return false, fmt.Errorf("persist renewed certificate material for %s: %w", cert.Domain, err)
		}
	}

	now := s.now().UTC()
	next := cert
	next.Status = "active"
	next.LastError = ""
	// Success clears accumulated failure backoff so the next renewal cycle treats this cert as healthy.
	next.BackoffClass = ""
	next.RetryCount = 0
	next.NextRetryAtUnix = 0
	if result.Changed {
		if result.LastIssueAt != "" {
			next.LastIssueAt = result.LastIssueAt
		} else {
			next.LastIssueAt = now.Format(time.RFC3339)
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
		*maxRevision++
		next.Revision = *maxRevision
	}
	if managedCertificateEqual(cert, next) {
		return false, nil
	}
	rows[index] = managedCertificateToRow(next)
	if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
		if result.Changed {
			if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, cert, previousMaterial, previousMaterialFound); restoreErr != nil {
				return false, fmt.Errorf("save renewed certificate metadata for %s: %w (restore failed: %v)", cert.Domain, err, restoreErr)
			}
		}
		return false, err
	}
	if result.Changed {
		if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
			return false, err
		}
	}
	return result.Changed, nil
}

func (s *certificateService) isManagedCertificateRenewalCandidate(cert ManagedCertificate, now time.Time) bool {
	if !cert.Enabled || cert.Scope != "domain" || cert.IssuerMode != "master_cf_dns" || cert.CertificateType != "acme" {
		return false
	}
	if localAgentID := s.cfg.LocalAgentID; localAgentID != "" {
		if len(cert.TargetAgentIDs) != 1 || cert.TargetAgentIDs[0] != localAgentID {
			return false
		}
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
func (s *certificateService) recordManagedCertificateRenewalFailure(ctx context.Context, cert ManagedCertificate, err error, rows []storage.ManagedCertificateRow, index int) (ManagedCertificate, error) {
	next := applyManagedCertificateRenewalFailureBackoff(cert, err, s.now().UTC())
	next.Status = "error"
	next.LastError = err.Error()
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
