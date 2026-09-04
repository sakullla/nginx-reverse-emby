package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrPKILifecycleInvalid     = errors.New("invalid PKI lifecycle operation")
	ErrPKILifecycleConflict    = errors.New("PKI lifecycle state changed concurrently")
	ErrPKIOperationNotFound    = errors.New("PKI operation not found")
	ErrPKIEndpointFailedClosed = errors.New("PKI endpoint certificate is expired")
)

type PKIEndpointRotationCandidate struct {
	CertificateID                string
	CertificateFingerprintSHA256 string
	PublicKeyFingerprintSHA256   string
	Generation                   int64
	NotBefore                    time.Time
	NotAfter                     time.Time
	Verified                     bool
}

type PKIEndpointRotationFailure struct {
	IdentityID                  string
	ExpectedActiveCertificateID string
	FailureCount                int
	NextAttemptAt               time.Time
	FailedClosed                bool
	Reason                      string
	Lease                       PKILeaseGrant
	Event                       PKIAuditEvent
}

type PKIEndpointActivation struct {
	IdentityID                  string
	ExpectedActiveCertificateID string
	Candidate                   PKIEndpointRotationCandidate
	Forced                      bool
	Lease                       PKILeaseGrant
	Event                       PKIAuditEvent
}

// PKIEndpointActivationCommitValidator is invoked by the repository with its
// authoritative transaction commit time while the activation CAS and lease
// fence are still held. Returning an error must abort the transaction.
type PKIEndpointActivationCommitValidator func(time.Time) error

// PKIEndpointRotationRepository must persist a failure without changing the
// active generation, and must activate a verified candidate with a CAS on
// ExpectedActiveCertificateID. In the same transaction it must compare Lease
// against the canonical live domain/epoch/instance/term/deadline; checking the
// lease before entering this method is insufficient.
type PKIEndpointRotationRepository interface {
	LoadPKIEndpointCertificate(context.Context, string) (PKIEndpointCertificateState, error)
	RecordPKIEndpointRotationFailure(context.Context, PKIEndpointRotationFailure) error
	ActivatePKIEndpointCandidate(context.Context, PKIEndpointActivation, PKIEndpointActivationCommitValidator) error
}

// PKIEndpointRotator stages the new private key at the owning endpoint and
// returns only public metadata after local key/certificate/trust validation.
type PKIEndpointRotator interface {
	StageAndVerifyPKIEndpoint(context.Context, PKIEndpointCertificateState, bool) (PKIEndpointRotationCandidate, error)
}

type PKILifecycleServiceOptions struct {
	Policy     InternalPKIPolicy
	Repository PKIEndpointRotationRepository
	Rotator    PKIEndpointRotator
	Lease      PKILeaseGate
	Clock      func() time.Time
}

type PKILifecycleService struct {
	policy     InternalPKIPolicy
	repository PKIEndpointRotationRepository
	rotator    PKIEndpointRotator
	lease      PKILeaseGate
	clock      func() time.Time
}

type PKIEndpointRotationResult struct {
	IdentityID          string
	PreviousCertificate string
	ActiveCertificate   string
	Started             bool
	Activated           bool
	Forced              bool
	FailedClosed        bool
	FailureCount        int
	NextAttemptAt       time.Time
	AlertLevel          PKIAlertLevel
}

func NewPKILifecycleService(options PKILifecycleServiceOptions) (*PKILifecycleService, error) {
	if err := ValidateInternalPKIPolicy(options.Policy); err != nil {
		return nil, err
	}
	if options.Repository == nil || options.Rotator == nil || options.Lease == nil {
		return nil, fmt.Errorf("%w: repository, rotator, and lease gate are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &PKILifecycleService{
		policy: options.Policy, repository: options.Repository, rotator: options.Rotator,
		lease: options.Lease, clock: options.Clock,
	}, nil
}

func (s *PKILifecycleService) RunEndpointRotation(ctx context.Context, identityID string, forced bool) (PKIEndpointRotationResult, error) {
	identityID = strings.TrimSpace(identityID)
	if identityID == "" {
		return PKIEndpointRotationResult{}, fmt.Errorf("%w: identity ID is required", ErrPKILifecycleInvalid)
	}
	now := s.clock().UTC()
	if now.IsZero() {
		return PKIEndpointRotationResult{}, fmt.Errorf("%w: clock returned zero", ErrPKILifecycleInvalid)
	}
	active, err := s.repository.LoadPKIEndpointCertificate(ctx, identityID)
	if err != nil {
		return PKIEndpointRotationResult{}, err
	}
	if active.IdentityID != identityID {
		return PKIEndpointRotationResult{}, fmt.Errorf("%w: repository returned another identity", ErrPKILifecycleInvalid)
	}
	decision, err := EvaluatePKIEndpointSchedule(s.policy, active, now, forced)
	if err != nil {
		return PKIEndpointRotationResult{}, err
	}
	result := PKIEndpointRotationResult{
		IdentityID: identityID, PreviousCertificate: active.CertificateID, ActiveCertificate: active.CertificateID,
		Forced: forced, FailedClosed: decision.FailedClosed, FailureCount: active.FailureCount,
		NextAttemptAt: active.NextAttemptAt, AlertLevel: decision.AlertLevel,
	}
	if !decision.Due {
		return result, nil
	}
	result.Started = true
	before, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return result, err
	}
	candidate, stageErr := s.rotator.StageAndVerifyPKIEndpoint(ctx, active, forced)
	after, err := s.lease.RequirePKILease(ctx)
	if err != nil || !samePKILeaseAuthority(before, after) {
		if err == nil {
			err = ErrPKILeaseNotHeld
		}
		return result, err
	}
	completedAt := s.clock().UTC()
	if completedAt.IsZero() {
		return result, fmt.Errorf("%w: clock returned zero", ErrPKILifecycleInvalid)
	}
	if stageErr == nil {
		stageErr = validatePKIEndpointCandidate(active, candidate, completedAt)
	}
	if err := validatePKIMutationLeaseFence(after); err != nil {
		return result, err
	}
	if stageErr != nil {
		return s.recordEndpointFailure(ctx, active, result, completedAt, after, stageErr)
	}
	activation := PKIEndpointActivation{
		IdentityID: identityID, ExpectedActiveCertificateID: active.CertificateID,
		Candidate: candidate, Forced: forced, Lease: after,
		Event: NewPKIAuditEvent("endpoint_rotated", "scheduler", identityID, "succeeded", "", completedAt),
	}
	if err := s.repository.ActivatePKIEndpointCandidate(ctx, activation, func(committedAt time.Time) error {
		return validatePKIEndpointActivationCommit(candidate, committedAt)
	}); err != nil {
		failedAt := s.clock().UTC()
		if failedAt.IsZero() {
			return result, errors.Join(err, fmt.Errorf("%w: clock returned zero", ErrPKILifecycleInvalid))
		}
		return s.recordEndpointFailure(ctx, active, result, failedAt, after, err)
	}
	result.Activated = true
	result.ActiveCertificate = candidate.CertificateID
	result.FailureCount = 0
	result.NextAttemptAt = time.Time{}
	result.AlertLevel = PKIAlertNone
	result.FailedClosed = false
	return result, nil
}

func validatePKIEndpointActivationCommit(candidate PKIEndpointRotationCandidate, committedAt time.Time) error {
	committedAt = committedAt.UTC()
	if committedAt.IsZero() {
		return fmt.Errorf("%w: endpoint activation commit time is zero", ErrPKILifecycleInvalid)
	}
	if !committedAt.Before(candidate.NotAfter) {
		return fmt.Errorf("%w: endpoint candidate expired before activation commit", ErrPKILifecycleInvalid)
	}
	return nil
}

func (s *PKILifecycleService) ForceRotateEndpoint(ctx context.Context, identityID string) (PKIEndpointRotationResult, error) {
	return s.RunEndpointRotation(ctx, identityID, true)
}

func (s *PKILifecycleService) recordEndpointFailure(
	ctx context.Context,
	active PKIEndpointCertificateState,
	result PKIEndpointRotationResult,
	now time.Time,
	lease PKILeaseGrant,
	cause error,
) (PKIEndpointRotationResult, error) {
	updated, retryErr := NextPKIEndpointRetry(s.policy, active, now)
	if retryErr != nil {
		return result, errors.Join(cause, retryErr)
	}
	alert, alertErr := PKICertificateAlertLevel(
		s.policy, updated.FailureCount, updated.NotAfter.Sub(updated.NotBefore), updated.NotAfter.Sub(now),
	)
	if alertErr != nil {
		return result, errors.Join(cause, alertErr)
	}
	failure := PKIEndpointRotationFailure{
		IdentityID: active.IdentityID, ExpectedActiveCertificateID: active.CertificateID,
		FailureCount: updated.FailureCount, NextAttemptAt: updated.NextAttemptAt,
		FailedClosed: !now.Before(active.NotAfter), Reason: cause.Error(), Lease: lease,
		Event: NewPKIAuditEvent("endpoint_rotation_failed", "scheduler", active.IdentityID, "failed", cause.Error(), now),
	}
	if err := s.repository.RecordPKIEndpointRotationFailure(ctx, failure); err != nil {
		return result, errors.Join(cause, err)
	}
	result.FailureCount = updated.FailureCount
	result.NextAttemptAt = updated.NextAttemptAt
	result.AlertLevel = alert
	result.FailedClosed = failure.FailedClosed
	if failure.FailedClosed {
		return result, errors.Join(ErrPKIEndpointFailedClosed, cause)
	}
	return result, cause
}

func validatePKIEndpointCandidate(active PKIEndpointCertificateState, candidate PKIEndpointRotationCandidate, now time.Time) error {
	if strings.TrimSpace(candidate.CertificateID) == "" || strings.TrimSpace(candidate.CertificateFingerprintSHA256) == "" ||
		strings.TrimSpace(candidate.PublicKeyFingerprintSHA256) == "" || !candidate.Verified ||
		candidate.Generation <= active.Generation || candidate.CertificateID == active.CertificateID ||
		!validPKILifecycleSHA256(candidate.CertificateFingerprintSHA256) || !validPKILifecycleSHA256(candidate.PublicKeyFingerprintSHA256) ||
		strings.EqualFold(candidate.CertificateFingerprintSHA256, active.CertificateFingerprintSHA256) ||
		strings.EqualFold(candidate.PublicKeyFingerprintSHA256, active.PublicKeyFingerprintSHA256) ||
		candidate.NotBefore.IsZero() || candidate.NotBefore.After(now) ||
		!candidate.NotAfter.After(candidate.NotBefore) || !candidate.NotAfter.After(now) {
		return fmt.Errorf("%w: staged endpoint generation is not a verified key-changing replacement", ErrPKILifecycleInvalid)
	}
	return nil
}

func validatePKIMutationLeaseFence(lease PKILeaseGrant) error {
	if strings.TrimSpace(lease.PKIDomainID) == "" || lease.PKIEpoch < 0 || strings.TrimSpace(lease.InstanceID) == "" ||
		!validPKILeaseTerm(lease.LeaseTerm) || lease.LeaseDeadline.IsZero() {
		return fmt.Errorf("%w: mutation lease fence is invalid", ErrPKILifecycleInvalid)
	}
	return nil
}

func validPKILifecycleSHA256(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}
