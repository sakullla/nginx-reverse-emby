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

	now := s.now().UTC()
	maxRevision := highestManagedCertificateRevisionForService(rows)
	for index, row := range rows {
		cert := managedCertificateFromRow(row)
		if !s.isManagedCertificateRenewalCandidate(cert, now) {
			continue
		}

		result, err := issuer.Renew(ctx, cert)
		if err != nil {
			next := cert
			next.Status = "error"
			next.LastError = err.Error()
			rows[index] = managedCertificateToRow(next)
			if saveErr := s.store.SaveManagedCertificates(ctx, rows); saveErr != nil {
				return saveErr
			}
			return fmt.Errorf("renew certificate %d: %w", cert.ID, err)
		}

		var issuedMaterial storage.ManagedCertificateBundle
		var previousMaterial storage.ManagedCertificateBundle
		previousMaterialFound := false
		if result.Changed {
			issuedMaterial, err = resolveManagedCertificateIssueMaterial(cert, result)
			if err != nil {
				next := cert
				next.Status = "error"
				next.LastError = err.Error()
				rows[index] = managedCertificateToRow(next)
				if saveErr := s.store.SaveManagedCertificates(ctx, rows); saveErr != nil {
					return saveErr
				}
				return fmt.Errorf("renew certificate %d: %w", cert.ID, err)
			}

			previousMaterial, previousMaterialFound, err = s.store.LoadManagedCertificateMaterial(ctx, cert.Domain)
			if err != nil {
				return err
			}
			if err := s.store.SaveManagedCertificateMaterial(ctx, cert.Domain, issuedMaterial); err != nil {
				if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, cert, previousMaterial, previousMaterialFound); restoreErr != nil {
					return fmt.Errorf("persist renewed certificate material for %s: %w (restore failed: %v)", cert.Domain, err, restoreErr)
				}
				return fmt.Errorf("persist renewed certificate material for %s: %w", cert.Domain, err)
			}
		}

		next := cert
		next.Status = "active"
		next.LastError = ""
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
			maxRevision++
			next.Revision = maxRevision
		}
		if managedCertificateEqual(cert, next) {
			continue
		}
		rows[index] = managedCertificateToRow(next)
		if err := s.store.SaveManagedCertificates(ctx, rows); err != nil {
			if result.Changed {
				if restoreErr := s.restoreManagedCertificateMaterialAfterIssueFailure(ctx, cert, previousMaterial, previousMaterialFound); restoreErr != nil {
					return fmt.Errorf("save renewed certificate metadata for %s: %w (restore failed: %v)", cert.Domain, err, restoreErr)
				}
			}
			return err
		}
		if result.Changed {
			if err := s.syncManagedCertificateAgentIDs(ctx, next.TargetAgentIDs, next.Revision); err != nil {
				return err
			}
		}
	}
	return nil
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
	renewAt, ok := parseManagedCertificateRenewAt(cert.ACMEInfo.Renew)
	if !ok {
		return true
	}
	return !renewAt.After(now)
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
