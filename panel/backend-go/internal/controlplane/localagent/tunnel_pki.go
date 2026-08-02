package localagent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const localTunnelPKIStorageIdentity = "agent"

// TunnelPKIService is the public-only in-process boundary used by the
// embedded agent. It deliberately carries no bootstrap token, control token,
// endpoint private key, or vault key reference.
type TunnelPKIService interface {
	SecuritySnapshot(context.Context, string, *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error)
	EnrollLocal(context.Context, service.PKILocalEnrollRequest) (service.PKILocalEnrollmentReply, error)
}

type tunnelCredentialStore interface {
	PrepareEnrollment(context.Context, goagentembedded.PKIEnrollmentSpec) (goagentembedded.PKIPendingEnrollment, error)
	RejectPendingEnrollment(string, string, string) error
	ActivateRegistrationCredential(context.Context, goagentembedded.PKIActivateRequest) (goagentembedded.PKICredentialMetadata, error)
	LoadActiveCredential(string) (goagentembedded.PKICredentialMetadata, error)
	ApplySecuritySnapshot(goagentembedded.PKISecuritySnapshot) (goagentembedded.PKISecurityState, error)
	SecurityAcknowledgement(string) (goagentembedded.PKISecurityAcknowledgement, error)
}

var (
	_ TunnelPKIService      = (*service.DegradedPKIService)(nil)
	_ tunnelCredentialStore = (*goagentembedded.CredentialStore)(nil)
)

// ConfigureTunnelPKI keeps the stable degraded proxy rather than a concrete
// runtime instance, so supervisor promotion is observed without rebuilding
// the embedded agent. Configuration fails if an adapter erased the embedded
// credential facade.
func (r *Runtime) ConfigureTunnelPKI(pki TunnelPKIService) error {
	if r == nil || pki == nil {
		return errors.New("embedded tunnel PKI service is required")
	}
	if r.credentials == nil {
		return errors.New("embedded tunnel credential store is unavailable")
	}
	if strings.TrimSpace(r.agentID) == "" {
		return errors.New("embedded local agent identity is unavailable")
	}
	r.pkiMu.Lock()
	r.tunnelPKI = pki
	r.pkiMu.Unlock()
	return nil
}

func (r *Runtime) tunnelPKIConfigured() bool {
	if r == nil {
		return false
	}
	r.pkiMu.RLock()
	defer r.pkiMu.RUnlock()
	return r.tunnelPKI != nil && r.credentials != nil
}

func (r *Runtime) runTunnelPKIReconciler(ctx context.Context) {
	interval := r.heartbeatInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.ReconcileTunnelPKI(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[local-agent] tunnel PKI reconciliation failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileTunnelPKI advances public trust independently from ordinary local
// revision application, reuses a durable key/CSR after response loss, and
// consumes the constrained registration trust-reset path after an emergency
// CA replacement. PKI degradation does not stop the ordinary embedded runtime.
func (r *Runtime) ReconcileTunnelPKI(ctx context.Context) error {
	if r == nil {
		return errors.New("embedded runtime is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.pkiReconcileMu.Lock()
	defer r.pkiReconcileMu.Unlock()

	r.pkiMu.RLock()
	pki := r.tunnelPKI
	credentials := r.credentials
	r.pkiMu.RUnlock()
	if pki == nil || credentials == nil {
		return errors.New("embedded tunnel PKI is not configured")
	}

	var acknowledgement *storage.PKISecurityAcknowledgement
	activeForAck, activeForAckErr := credentials.LoadActiveCredential(localTunnelPKIStorageIdentity)
	if activeForAckErr == nil && activeForAck.Manifest.Expectation.AgentID == r.agentID &&
		activeForAck.Manifest.Expectation.Kind == storage.PKIIdentityKindAgent &&
		activeForAck.Manifest.Expectation.Purpose == storage.PKICertificatePurposeClient {
		if activeAck, err := credentials.SecurityAcknowledgement(localTunnelPKIStorageIdentity); err == nil {
			converted := toStoragePKIAcknowledgement(activeAck)
			acknowledgement = &converted
		} else if !errors.Is(err, goagentembedded.ErrPKIActiveCredential) &&
			!errors.Is(err, goagentembedded.ErrPKICredentialInvalid) &&
			!errors.Is(err, goagentembedded.ErrPKISecurityInvalid) {
			return fmt.Errorf("load embedded PKI acknowledgement: %w", err)
		}
	} else if activeForAckErr != nil && !errors.Is(activeForAckErr, goagentembedded.ErrPKIActiveCredential) &&
		!errors.Is(activeForAckErr, goagentembedded.ErrPKICredentialInvalid) {
		return fmt.Errorf("load embedded tunnel credential for acknowledgement: %w", activeForAckErr)
	}

	snapshot, err := pki.SecuritySnapshot(ctx, r.agentID, acknowledgement)
	if err != nil {
		return fmt.Errorf("load canonical embedded PKI snapshot: %w", err)
	}
	if strings.TrimSpace(snapshot.PKIDomainID) == "" {
		return errors.New("canonical embedded PKI snapshot has no domain")
	}
	embeddedSnapshot := toEmbeddedPKISnapshot(snapshot)
	registrationTrustReset := false
	if _, err := credentials.ApplySecuritySnapshot(embeddedSnapshot); err != nil {
		switch {
		case errors.Is(err, goagentembedded.ErrPKIActivationCommitted):
			// The durable pointer already references this public snapshot.
		case errors.Is(err, goagentembedded.ErrPKISecurityInvalid):
			// Only registration activation may authorize the narrowly bounded
			// same-epoch emergency trust reset. Continue with a fresh CSR.
			registrationTrustReset = true
		default:
			return fmt.Errorf("apply embedded PKI snapshot: %w", err)
		}
	}

	if !registrationTrustReset {
		active, activeErr := credentials.LoadActiveCredential(localTunnelPKIStorageIdentity)
		if activeErr == nil && !localTunnelCredentialNeedsEnrollment(active, embeddedSnapshot, r.agentID, r.currentTime()) {
			return nil
		}
		if activeErr != nil && !errors.Is(activeErr, goagentembedded.ErrPKIActiveCredential) &&
			!errors.Is(activeErr, goagentembedded.ErrPKICredentialInvalid) {
			return fmt.Errorf("load embedded tunnel credential: %w", activeErr)
		}
	}

	pending, err := credentials.PrepareEnrollment(ctx, goagentembedded.PKIEnrollmentSpec{
		StorageIdentity: localTunnelPKIStorageIdentity,
		DomainID:        snapshot.PKIDomainID,
		AgentID:         r.agentID,
		Kind:            storage.PKIIdentityKindAgent,
		Purpose:         storage.PKICertificatePurposeClient,
	})
	if err != nil {
		return fmt.Errorf("prepare embedded tunnel enrollment: %w", err)
	}
	if pending.DomainID != snapshot.PKIDomainID || pending.AgentID != r.agentID ||
		pending.Request.Kind != storage.PKIIdentityKindAgent ||
		pending.Request.Purpose != storage.PKICertificatePurposeClient {
		return errors.New("embedded tunnel enrollment owner changed")
	}

	reply, err := pki.EnrollLocal(ctx, service.PKILocalEnrollRequest{
		RequestID: pending.Request.RequestID,
		Kind:      pending.Request.Kind, ListenerID: pending.Request.ListenerID,
		Purpose: pending.Request.Purpose, CSRPEM: pending.Request.CSRPEM,
		DNSNames: pending.Request.DNSNames, IPAddresses: pending.Request.IPAddresses,
	})
	if err != nil {
		if code := localTunnelEnrollmentRejectionCode(err); code != "" {
			rejectErr := credentials.RejectPendingEnrollment(localTunnelPKIStorageIdentity, pending.Request.RequestID, code)
			return errors.Join(fmt.Errorf("enroll embedded tunnel credential: %w", err), rejectErr)
		}
		return fmt.Errorf("enroll embedded tunnel credential: %w", err)
	}

	replySnapshot := toEmbeddedPKISnapshot(reply.SecuritySnapshot)
	_, err = credentials.ActivateRegistrationCredential(ctx, goagentembedded.PKIActivateRequest{
		StorageIdentity: localTunnelPKIStorageIdentity,
		RequestID:       pending.Request.RequestID,
		Credential:      toEmbeddedPKICredential(reply.TunnelCredential),
		Security:        replySnapshot,
		Expectation: goagentembedded.PKICredentialExpectation{
			DomainID: reply.SecuritySnapshot.PKIDomainID, AgentID: r.agentID,
			Kind: pending.Request.Kind, ListenerID: pending.Request.ListenerID,
			Purpose: pending.Request.Purpose, DNSNames: pending.Request.DNSNames,
			IPAddresses: pending.Request.IPAddresses,
		},
	})
	if err != nil {
		if errors.Is(err, goagentembedded.ErrPKIActivationCommitted) {
			return fmt.Errorf("activate embedded tunnel credential: %w", err)
		}
		if errors.Is(err, goagentembedded.ErrPKICredentialInvalid) || errors.Is(err, goagentembedded.ErrPKISecurityInvalid) {
			rejectErr := credentials.RejectPendingEnrollment(localTunnelPKIStorageIdentity, pending.Request.RequestID, "activation_invalid")
			return errors.Join(fmt.Errorf("activate embedded tunnel credential: %w", err), rejectErr)
		}
		return fmt.Errorf("activate embedded tunnel credential: %w", err)
	}

	activeAck, err := credentials.SecurityAcknowledgement(localTunnelPKIStorageIdentity)
	if err != nil {
		return fmt.Errorf("load activated embedded PKI acknowledgement: %w", err)
	}
	convertedAck := toStoragePKIAcknowledgement(activeAck)
	if _, err := pki.SecuritySnapshot(ctx, r.agentID, &convertedAck); err != nil {
		return fmt.Errorf("acknowledge activated embedded PKI snapshot: %w", err)
	}
	return nil
}

func (r *Runtime) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

func localTunnelCredentialNeedsEnrollment(active goagentembedded.PKICredentialMetadata, snapshot goagentembedded.PKISecuritySnapshot, agentID string, now time.Time) bool {
	manifest := active.Manifest
	credential := manifest.Credential
	if manifest.Expectation.Kind != storage.PKIIdentityKindAgent ||
		manifest.Expectation.Purpose != storage.PKICertificatePurposeClient ||
		manifest.Expectation.AgentID != agentID ||
		manifest.PKIDomainID != snapshot.PKIDomainID || credential.NotBefore.IsZero() ||
		credential.NotAfter.IsZero() || !credential.NotAfter.After(credential.NotBefore) {
		return true
	}
	lifetime := credential.NotAfter.Sub(credential.NotBefore)
	if !now.Before(credential.NotBefore.Add(lifetime * 2 / 3)) {
		return true
	}
	for _, root := range snapshot.TrustRoots {
		if root.Generation == credential.CAGeneration {
			return root.Status != "active" || root.AuthorityID != credential.AuthorityID
		}
	}
	return true
}

func localTunnelEnrollmentRejectionCode(err error) string {
	switch {
	case errors.Is(err, service.ErrPKIEnrollmentOwnerMismatch):
		return "owner_mismatch"
	case errors.Is(err, service.ErrPKIEnrollmentPublicKeyReuse):
		return "public_key_reuse"
	case errors.Is(err, service.ErrPKIEnrollmentCSR):
		return "invalid_csr"
	case errors.Is(err, service.ErrPKIEnrollmentRequest):
		return "invalid_request"
	default:
		return ""
	}
}

func toStoragePKIAcknowledgement(value goagentembedded.PKISecurityAcknowledgement) storage.PKISecurityAcknowledgement {
	return storage.PKISecurityAcknowledgement{
		PKIDomainID: value.PKIDomainID, PKIEpoch: value.PKIEpoch,
		SecurityRevision: value.SecurityRevision, Full: value.Full,
		CertificateID:    value.CertificateID,
		TrustGenerations: slices.Clone(value.TrustGenerations),
	}
}

func toEmbeddedPKISnapshot(value storage.PKISecuritySnapshot) goagentembedded.PKISecuritySnapshot {
	roots := make([]goagentembedded.PKITrustRoot, 0, len(value.TrustRoots))
	for _, root := range value.TrustRoots {
		roots = append(roots, goagentembedded.PKITrustRoot{
			AuthorityID: root.AuthorityID, Generation: root.Generation, Status: root.Status,
			CertificatePEM: root.CertificatePEM, FingerprintSHA256: root.FingerprintSHA256,
			NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		})
	}
	return goagentembedded.PKISecuritySnapshot{
		PKIDomainID: value.PKIDomainID, PKIEpoch: value.PKIEpoch,
		SecurityRevision: value.SecurityRevision, Full: value.Full, IssuedAt: value.IssuedAt,
		TrustRoots:         roots,
		RevokedIdentityIDs: slices.Clone(value.RevokedIdentityIDs),
		RevokedSerials:     slices.Clone(value.RevokedSerials),
		SignerGeneration:   value.SignerGeneration,
		Signature:          append([]byte(nil), value.Signature...),
	}
}

func toEmbeddedPKICredential(value storage.PKITunnelCredential) goagentembedded.PKITunnelCredential {
	return goagentembedded.PKITunnelCredential{
		IdentityID: value.IdentityID, CertificateID: value.CertificateID, Purpose: value.Purpose,
		CertificatePEM: value.CertificatePEM, PublicKeyFingerprint: value.PublicKeyFingerprint,
		AuthorityID: value.AuthorityID, CAGeneration: value.CAGeneration,
		NotBefore: value.NotBefore, NotAfter: value.NotAfter,
	}
}
