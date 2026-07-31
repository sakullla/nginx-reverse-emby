package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PKIEnrollmentServiceOptions struct {
	Store           PKITransactionStore
	AuthoritySigner PKIEnrollmentAuthoritySigner
	LocalAgentID    string
	Clock           func() time.Time
	Random          io.Reader
	NewID           PKIIDGenerator
}

type PKIEnrollmentService struct {
	store           PKITransactionStore
	authoritySigner PKIEnrollmentAuthoritySigner
	localAgentID    string
	clock           func() time.Time
	random          io.Reader
	newID           PKIIDGenerator
}

type PKIEnrollRequest struct {
	Token       string
	AgentID     string
	Kind        string
	ListenerID  string
	Purpose     string
	CSRPEM      string
	DNSNames    []string
	IPAddresses []string
}

type PKILocalEnrollRequest struct {
	Kind        string
	ListenerID  string
	Purpose     string
	CSRPEM      string
	DNSNames    []string
	IPAddresses []string
}

type PKIEnrollmentResult struct {
	AgentID              string
	IdentityID           string
	CertificateID        string
	Purpose              string
	CertificatePEM       string
	PublicKeyFingerprint string
	AuthorityID          string
	CAGeneration         int64
	NotBefore            time.Time
	NotAfter             time.Time
}

type pkiEnrollmentCredential struct {
	tokenDigest string
	local       bool
}

func NewPKIEnrollmentService(options PKIEnrollmentServiceOptions) (*PKIEnrollmentService, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrPKIEnrollmentRequest)
	}
	if options.AuthoritySigner == nil {
		return nil, fmt.Errorf("%w: authority signer is required", ErrPKIEnrollmentAuthorityUnavailable)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	service := &PKIEnrollmentService{
		store:           options.Store,
		authoritySigner: options.AuthoritySigner,
		localAgentID:    strings.TrimSpace(options.LocalAgentID),
		clock:           options.Clock,
		random:          options.Random,
		newID:           options.NewID,
	}
	if service.newID == nil {
		service.newID = service.randomID
	}
	return service, nil
}

func (s *PKIEnrollmentService) Enroll(ctx context.Context, request PKIEnrollRequest) (PKIEnrollmentResult, error) {
	digest, err := digestPKIEnrollmentToken(request.Token)
	if err != nil {
		return s.finishPKIEnrollment(ctx, request, false, PKIEnrollmentResult{}, ErrPKIEnrollmentTokenRejected)
	}
	result, err := s.enroll(ctx, request, pkiEnrollmentCredential{tokenDigest: digest})
	return s.finishPKIEnrollment(ctx, request, false, result, err)
}

// EnrollLocal is intentionally tokenless and only available as a direct
// service call. Its owner is always the configured LocalAgentID, so the
// embedded agent never needs a bootstrap secret that could escape via config,
// API models, or logs.
func (s *PKIEnrollmentService) EnrollLocal(ctx context.Context, request PKILocalEnrollRequest) (PKIEnrollmentResult, error) {
	enrollRequest := PKIEnrollRequest{
		AgentID: s.localAgentID, Kind: request.Kind, ListenerID: request.ListenerID, Purpose: request.Purpose,
		CSRPEM: request.CSRPEM, DNSNames: request.DNSNames, IPAddresses: request.IPAddresses,
	}
	if s.localAgentID == "" {
		return s.finishPKIEnrollment(ctx, enrollRequest, true, PKIEnrollmentResult{}, fmt.Errorf("%w: local agent identity is not configured", ErrPKIEnrollmentRequest))
	}
	result, err := s.enroll(ctx, enrollRequest, pkiEnrollmentCredential{local: true})
	return s.finishPKIEnrollment(ctx, enrollRequest, true, result, err)
}

func (s *PKIEnrollmentService) enroll(ctx context.Context, request PKIEnrollRequest, credential pkiEnrollmentCredential) (PKIEnrollmentResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Kind = strings.TrimSpace(request.Kind)
	request.ListenerID = strings.TrimSpace(request.ListenerID)
	request.Purpose = strings.TrimSpace(request.Purpose)
	if err := validatePKIEnrollRequestShape(request); err != nil {
		return PKIEnrollmentResult{}, err
	}
	csr, err := parsePKIEnrollmentCSR(request.CSRPEM)
	if err != nil {
		return PKIEnrollmentResult{}, err
	}
	now := s.clock().UTC()
	if now.IsZero() {
		return PKIEnrollmentResult{}, fmt.Errorf("%w: clock returned a zero timestamp", ErrPKIEnrollmentRequest)
	}
	var result PKIEnrollmentResult
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		settings, found, err := tx.GetPKISettings(ctx)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: PKI settings are not initialized", ErrPKIEnrollmentAuthorityUnavailable)
		}
		ownerAgentID := request.AgentID
		tokenScope := "local"
		operatorID := ""
		anonymousNewAgent := false
		if credential.local {
			if ownerAgentID != s.localAgentID {
				return ErrPKIEnrollmentOwnerMismatch
			}
		} else {
			token, consumed, err := tx.ConsumePKIEnrollmentToken(ctx, credential.tokenDigest, now)
			if err != nil {
				return err
			}
			if !consumed {
				return ErrPKIEnrollmentTokenRejected
			}
			tokenScope = token.Scope
			operatorID = token.CreatedBy
			switch token.Scope {
			case PKIEnrollmentTokenScopeNewAgent:
				if token.BoundAgentID != "" || request.AgentID != "" || request.Kind != storage.PKIIdentityKindAgent {
					return ErrPKIEnrollmentTokenRejected
				}
				ownerAgentID, err = s.nextID("agent")
				if err != nil {
					return err
				}
				anonymousNewAgent = true
			case PKIEnrollmentTokenScopeBoundReenrollment:
				ownerAgentID = strings.TrimSpace(token.BoundAgentID)
				if ownerAgentID == "" {
					return ErrPKIEnrollmentTokenRejected
				}
				if s.localAgentID != "" && ownerAgentID == s.localAgentID {
					return ErrPKIEnrollmentTokenRejected
				}
				if request.AgentID != "" && request.AgentID != ownerAgentID {
					return ErrPKIEnrollmentOwnerMismatch
				}
				exists, err := tx.PKIStableAgentExistsForUpdate(ctx, ownerAgentID)
				if err != nil {
					return err
				}
				if !exists {
					return fmt.Errorf("%w: bound agent owner does not exist", ErrPKIEnrollmentOwnerMismatch)
				}
			default:
				return ErrPKIEnrollmentTokenRejected
			}
		}

		binding, err := newPKIIdentityBinding(settings.PKIDomainID, request.Kind, ownerAgentID, request.ListenerID, request.Purpose, request.DNSNames, request.IPAddresses)
		if err != nil {
			return err
		}
		if err := validatePKIEnrollmentCSRBinding(csr, binding, anonymousNewAgent); err != nil {
			return err
		}
		identity, identityFound, err := tx.FindPKIIdentityForUpdate(ctx, binding.DomainID, binding.Kind, binding.AgentID, binding.ListenerID)
		if err != nil {
			return err
		}
		if anonymousNewAgent && identityFound {
			return fmt.Errorf("%w: generated agent owner is already allocated", ErrPKIEnrollmentOwnerMismatch)
		}

		var currentCertificate storage.PKICertificateRow
		currentCertificateFound := false
		if identityFound && identity.CurrentCertificateID != nil {
			currentCertificate, currentCertificateFound, err = tx.GetPKICertificateForUpdate(ctx, *identity.CurrentCertificateID)
			if err != nil {
				return err
			}
			if !currentCertificateFound || currentCertificate.IdentityID != identity.ID || currentCertificate.Purpose != binding.Purpose {
				return fmt.Errorf("%w: identity current certificate is inconsistent", ErrPKIEnrollmentRequest)
			}
			if strings.EqualFold(currentCertificate.PublicKeyFingerprint, csr.publicKeyFingerprint) {
				return ErrPKIEnrollmentPublicKeyReuse
			}
		}
		if identityFound && identity.State == storage.PKIIdentityStateActive && !currentCertificateFound {
			return fmt.Errorf("%w: active identity has no current certificate", ErrPKIEnrollmentRequest)
		}

		authority, found, err := tx.GetActivePKIAuthorityForUpdate(ctx, settings.PKIDomainID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPKIEnrollmentAuthorityUnavailable
		}
		signer, err := s.authoritySigner.LoadSigner(ctx, authority)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("%w: %v", ErrPKIEnrollmentAuthorityUnavailable, err)
		}
		endpointLifetime, err := validatedPKIEndpointLifetime(settings.EndpointLifetimeSeconds)
		if err != nil {
			return err
		}
		certificate, err := issuePKIIdentityCertificate(s.random, now, endpointLifetime, authority, signer, csr, binding)
		if err != nil {
			return err
		}
		certificateID, err := s.nextID("certificate")
		if err != nil {
			return err
		}
		identityID := identity.ID
		if !identityFound {
			identityID, err = s.nextID("identity")
			if err != nil {
				return err
			}
		}
		recordedAt := now
		if currentCertificateFound && !recordedAt.After(currentCertificate.CreatedAt) {
			recordedAt = currentCertificate.CreatedAt.Add(time.Nanosecond)
		}
		certificate.ID = certificateID
		certificate.IdentityID = identityID
		certificate.CreatedAt = recordedAt
		certificate.UpdatedAt = recordedAt

		if !identityFound {
			currentID := certificateID
			if err := tx.CreatePKIIdentity(ctx, storage.PKIIdentityRow{
				ID: identityID, PKIDomainID: binding.DomainID, Kind: binding.Kind, AgentID: binding.AgentID, ListenerID: binding.ListenerID,
				State: storage.PKIIdentityStateActive, CurrentCertificateID: &currentID, CreatedAt: recordedAt, UpdatedAt: recordedAt,
			}); err != nil {
				return err
			}
		} else {
			if currentCertificateFound && currentCertificate.Status == storage.PKICertificateStatusActive {
				superseded, err := tx.SupersedePKICertificate(ctx, currentCertificate.ID, certificateID, recordedAt)
				if err != nil {
					return err
				}
				if !superseded {
					return fmt.Errorf("%w: current certificate changed concurrently", ErrPKIEnrollmentRequest)
				}
			}
			if err := tx.SetPKIIdentityCurrentCertificate(ctx, identity.ID, certificateID, recordedAt); err != nil {
				return err
			}
		}
		if err := tx.CreatePKICertificate(ctx, certificate); err != nil {
			return err
		}
		eventID, err := s.nextID("event")
		if err != nil {
			return err
		}
		eventType := "pki.identity.enrolled"
		if identityFound {
			eventType = "pki.identity.reenrolled"
		}
		details, err := json.Marshal(struct {
			IdentityKind string `json:"identity_kind"`
			TokenScope   string `json:"token_scope"`
			Local        bool   `json:"local"`
		}{IdentityKind: binding.Kind, TokenScope: tokenScope, Local: credential.local})
		if err != nil {
			return err
		}
		generation := authority.Generation
		eventSource := "control_plane"
		if credential.local {
			eventSource = "embedded"
		}
		if err := tx.AppendPKIEvent(ctx, storage.PKIEventRow{
			ID: eventID, PKIDomainID: settings.PKIDomainID, Type: eventType, OccurredAt: recordedAt,
			Source: eventSource, OperatorID: operatorID, ObjectType: "identity", ObjectID: identityID, CertificateID: &certificateID,
			CAGeneration: &generation, Result: "success", SecurityRevision: settings.SecurityRevision, DetailsJSON: string(details),
		}); err != nil {
			return err
		}
		result = PKIEnrollmentResult{
			AgentID: binding.AgentID, IdentityID: identityID, CertificateID: certificateID, Purpose: binding.Purpose,
			CertificatePEM: certificate.CertificatePEM, PublicKeyFingerprint: certificate.PublicKeyFingerprint,
			AuthorityID: certificate.AuthorityID, CAGeneration: certificate.CAGeneration,
			NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		}
		return nil
	})
	if err != nil {
		return PKIEnrollmentResult{}, err
	}
	return result, nil
}

func (s *PKIEnrollmentService) finishPKIEnrollment(ctx context.Context, request PKIEnrollRequest, local bool, result PKIEnrollmentResult, enrollmentErr error) (PKIEnrollmentResult, error) {
	if enrollmentErr == nil {
		return result, nil
	}
	if auditErr := s.appendPKIEnrollmentFailure(ctx, request, local, enrollmentErr); auditErr != nil {
		return PKIEnrollmentResult{}, errors.Join(enrollmentErr, fmt.Errorf("append sanitized PKI enrollment failure audit: %w", auditErr))
	}
	return PKIEnrollmentResult{}, enrollmentErr
}

func (s *PKIEnrollmentService) appendPKIEnrollmentFailure(ctx context.Context, request PKIEnrollRequest, local bool, enrollmentErr error) error {
	now := s.clock().UTC()
	if now.IsZero() {
		return errors.New("PKI enrollment audit clock returned a zero timestamp")
	}
	eventID, err := s.nextID("event")
	if err != nil {
		return err
	}
	identityKind := "unknown"
	if request.Kind == storage.PKIIdentityKindAgent || request.Kind == storage.PKIIdentityKindListener {
		identityKind = request.Kind
	}
	rejectionClass := classifyPKIEnrollmentFailure(enrollmentErr)
	details, err := json.Marshal(struct {
		RejectionClass string `json:"rejection_class"`
		IdentityKind   string `json:"identity_kind"`
		Local          bool   `json:"local"`
	}{RejectionClass: rejectionClass, IdentityKind: identityKind, Local: local})
	if err != nil {
		return err
	}
	source := "control_plane"
	objectID := "remote"
	if local {
		source = "embedded"
		objectID = "local"
	}
	return s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		settings, found, err := tx.GetPKISettings(ctx)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("PKI settings are not initialized")
		}
		return tx.AppendPKIEvent(ctx, storage.PKIEventRow{
			ID: eventID, PKIDomainID: settings.PKIDomainID, Type: "pki.enrollment.rejected", OccurredAt: now,
			Source: source, ObjectType: "enrollment", ObjectID: objectID, Result: "failure", Reason: rejectionClass,
			SecurityRevision: settings.SecurityRevision, DetailsJSON: string(details),
		})
	})
}

func classifyPKIEnrollmentFailure(err error) string {
	switch {
	case errors.Is(err, ErrPKIEnrollmentTokenRejected):
		return "token_rejected"
	case errors.Is(err, ErrPKIEnrollmentCSR):
		return "csr_rejected"
	case errors.Is(err, ErrPKIEnrollmentOwnerMismatch):
		return "owner_mismatch"
	case errors.Is(err, ErrPKIEnrollmentPublicKeyReuse):
		return "public_key_reuse"
	case errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable):
		return "signing_failure"
	case errors.Is(err, ErrPKIEnrollmentRequest):
		return "business_rejected"
	default:
		return "business_failure"
	}
}

func validatePKIEnrollRequestShape(request PKIEnrollRequest) error {
	if strings.TrimSpace(request.CSRPEM) == "" {
		return fmt.Errorf("%w: CSR is required", ErrPKIEnrollmentRequest)
	}
	switch request.Kind {
	case storage.PKIIdentityKindAgent:
		if request.ListenerID != "" || request.Purpose != storage.PKICertificatePurposeClient || len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 {
			return fmt.Errorf("%w: agent enrollment requires client purpose", ErrPKIEnrollmentRequest)
		}
	case storage.PKIIdentityKindListener:
		if request.ListenerID == "" || request.Purpose != storage.PKICertificatePurposeServer {
			return fmt.Errorf("%w: listener enrollment requires listener ID and server purpose", ErrPKIEnrollmentRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported identity kind", ErrPKIEnrollmentRequest)
	}
	return nil
}

func validatedPKIEndpointLifetime(seconds int64) (time.Duration, error) {
	minimum := int64((24 * time.Hour) / time.Second)
	maximum := int64((397 * 24 * time.Hour) / time.Second)
	if seconds < minimum || seconds > maximum {
		return 0, fmt.Errorf("%w: endpoint lifetime is outside the internal PKI policy", ErrPKIEnrollmentAuthorityUnavailable)
	}
	return time.Duration(seconds) * time.Second, nil
}

func (s *PKIEnrollmentService) nextID(label string) (string, error) {
	value, err := s.newID()
	if err != nil {
		return "", fmt.Errorf("generate PKI %s identifier: %w", label, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("generate PKI %s identifier: empty identifier", label)
	}
	return value, nil
}

func (s *PKIEnrollmentService) randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(s.random, value); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", value), nil
}
