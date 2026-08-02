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
type PKIActiveCredential = modulepki.ActiveCredential
type PKISecurityState = modulepki.SecurityState

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

func (s *CredentialStore) Root() string {
	if s == nil || s.delegate == nil {
		return ""
	}
	return s.delegate.Root()
}

func (s *CredentialStore) PrepareEnrollment(ctx context.Context, spec PKIEnrollmentSpec) (PKIPendingEnrollment, error) {
	return s.delegate.PrepareEnrollment(ctx, spec)
}

func (s *CredentialStore) LoadPending(storageIdentity string) (PKIPendingEnrollment, error) {
	return s.delegate.LoadPending(storageIdentity)
}

func (s *CredentialStore) ActivateCredential(ctx context.Context, request PKIActivateRequest) (PKIActiveCredential, error) {
	return s.delegate.ActivateCredential(ctx, request)
}

func (s *CredentialStore) ActivateStagedRegistration(ctx context.Context, storageIdentity string) (PKIActiveCredential, error) {
	return s.delegate.ActivateStagedRegistration(ctx, storageIdentity)
}

func (s *CredentialStore) LoadActiveCredential(storageIdentity string) (PKIActiveCredential, error) {
	return s.delegate.LoadActiveCredential(storageIdentity)
}

func (s *CredentialStore) ApplySecuritySnapshot(snapshot PKISecuritySnapshot) (PKISecurityState, error) {
	return s.delegate.ApplySecuritySnapshot(snapshot)
}

func (s *CredentialStore) LoadSecuritySnapshot() (PKISecurityState, error) {
	return s.delegate.LoadSecuritySnapshot()
}

func (s *CredentialStore) SecurityAcknowledgement(certificateID string) (PKISecurityAcknowledgement, error) {
	return s.delegate.SecurityAcknowledgement(certificateID)
}
