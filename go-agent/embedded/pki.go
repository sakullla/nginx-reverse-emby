package embedded

import (
	"context"

	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

// These aliases keep the embedded bridge importable by the control-plane
// module without exposing go-agent/internal package paths to its callers.
type PKIEnrollmentSpec = modulepki.EnrollmentSpec
type PKIPendingEnrollment = modulepki.PendingEnrollment
type PKICredentialExpectation = modulepki.CredentialExpectation
type PKIActivateRequest = modulepki.ActivateRequest
type PKIStagedRegistration = modulepki.StagedRegistration
type PKICredentialManifest = modulepki.CredentialManifest
type PKICredentialMetadata = modulepki.CredentialMetadata
type PKISecurityState = modulepki.SecurityState

var (
	ErrPKIPendingNotFound            = modulepki.ErrPendingNotFound
	ErrPKIStagedRegistrationNotFound = modulepki.ErrStagedRegistrationNotFound
	ErrPKICredentialInvalid          = modulepki.ErrCredentialInvalid
	ErrPKISecurityInvalid            = modulepki.ErrSecurityInvalid
	ErrPKISecurityDowngrade          = modulepki.ErrSecurityDowngrade
	ErrPKIActiveCredential           = modulepki.ErrActiveCredential
	ErrPKIActivationCommitted        = modulepki.ErrActivationCommitted
)

// CredentialStore is the embedded agent's tunnel identity owner. New creates
// it below DataDir/embedded-agent-state/pki, so it cannot share an active
// pointer with a remote agent rooted directly at DataDir/pki.
type CredentialStore struct {
	delegate *modulepki.Store
}

func (r *Runtime) TunnelCredentialStore() *CredentialStore {
	if r == nil {
		return nil
	}
	return r.credentials
}

func (s *CredentialStore) PrepareEnrollment(ctx context.Context, spec PKIEnrollmentSpec) (PKIPendingEnrollment, error) {
	return s.delegate.PrepareEnrollment(ctx, spec)
}

func (s *CredentialStore) LoadPending(storageIdentity string) (PKIPendingEnrollment, error) {
	return s.delegate.LoadPending(storageIdentity)
}

func (s *CredentialStore) PendingEnrollments() ([]PKIPendingEnrollment, error) {
	return s.delegate.PendingEnrollments()
}

// RejectPendingEnrollment durably quarantines a terminally rejected request
// while keeping its key and CSR available for local diagnosis. It allows the
// embedded bridge to create a fresh request after an emergency invalidates a
// response-loss replay.
func (s *CredentialStore) RejectPendingEnrollment(storageIdentity, requestID, code string) error {
	return s.delegate.RejectPendingEnrollment(storageIdentity, requestID, code)
}

func (s *CredentialStore) ActivateCredential(ctx context.Context, request PKIActivateRequest) (PKICredentialMetadata, error) {
	return s.delegate.ActivateCredential(ctx, request)
}

// ActivateRegistrationCredential is the explicit trusted in-process boundary
// for a tokenless local enrollment response. Unlike ordinary continuity
// updates, it may consume the module's constrained emergency trust-reset path;
// key-bearing state still never crosses this facade.
func (s *CredentialStore) ActivateRegistrationCredential(ctx context.Context, request PKIActivateRequest) (PKICredentialMetadata, error) {
	return s.delegate.ActivateRegistrationCredential(ctx, request)
}

func (s *CredentialStore) ActivateStagedRegistration(ctx context.Context, storageIdentity string) (PKICredentialMetadata, error) {
	return s.delegate.ActivateStagedRegistration(ctx, storageIdentity)
}

func (s *CredentialStore) LoadActiveCredential(storageIdentity string) (PKICredentialMetadata, error) {
	return s.delegate.LoadActiveCredential(storageIdentity)
}

func (s *CredentialStore) ApplySecuritySnapshot(snapshot PKISecuritySnapshot) (PKISecurityState, error) {
	return s.delegate.ApplySecuritySnapshot(snapshot)
}

func (s *CredentialStore) LoadSecuritySnapshot() (PKISecurityState, error) {
	return s.delegate.LoadSecuritySnapshot()
}

func (s *CredentialStore) SecurityAcknowledgement(storageIdentity string) (PKISecurityAcknowledgement, error) {
	return s.delegate.SecurityAcknowledgement(storageIdentity)
}
