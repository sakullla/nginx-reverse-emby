package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const pkiEnrollmentReplayTTL = 10 * time.Minute

type PKIEnrollmentServiceOptions struct {
	Store           PKITransactionStore
	Lease           PKILeaseGate
	AuthoritySigner PKIEnrollmentAuthoritySigner
	LocalAgentID    string
	Clock           func() time.Time
	Random          io.Reader
	NewID           PKIIDGenerator
}

type PKIEnrollmentService struct {
	store           PKITransactionStore
	lease           PKILeaseGate
	authoritySigner PKIEnrollmentAuthoritySigner
	localAgentID    string
	clock           func() time.Time
	random          io.Reader
	newID           PKIIDGenerator
}

type PKIEnrollRequest struct {
	RequestID               string
	Token                   string
	AgentID                 string
	Kind                    string
	ListenerID              string
	Purpose                 string
	CSRPEM                  string
	DNSNames                []string
	IPAddresses             []string
	SecurityAcknowledgement *storage.PKISecurityAcknowledgement
}

type PKILocalEnrollRequest struct {
	RequestID   string
	Kind        string
	ListenerID  string
	Purpose     string
	CSRPEM      string
	DNSNames    []string
	IPAddresses []string
}

type PKIEnrollmentResult struct {
	AgentID              string
	AgentControlToken    string
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
	tokenDigest        string
	controlToken       string
	local              bool
	authenticated      bool
	stableAgent        *storage.AgentRow
	requireStableAgent bool
}

func NewPKIEnrollmentService(options PKIEnrollmentServiceOptions) (*PKIEnrollmentService, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrPKIEnrollmentRequest)
	}
	if options.Lease == nil {
		return nil, fmt.Errorf("%w: lease gate is required", ErrPKIEnrollmentRequest)
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
		lease:           options.Lease,
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
	// Keep the lower-level entrypoint relation-safe as well: every remote PKI
	// identity has a stable AgentRow and a cryptographically random control
	// credential in the same transaction. Production registration normally uses
	// EnrollAndBindAgent to provide the caller's metadata.
	controlToken := make([]byte, 32)
	if _, err := io.ReadFull(s.random, controlToken); err != nil {
		return s.finishPKIEnrollment(ctx, request, false, PKIEnrollmentResult{}, fmt.Errorf("generate enrollment control token: %w", err))
	}
	result, err := s.enroll(ctx, request, pkiEnrollmentCredential{
		tokenDigest:        digest,
		stableAgent:        &storage.AgentRow{Name: "enrolled agent", AgentToken: hex.EncodeToString(controlToken), TagsJSON: "[]", CapabilitiesJSON: "[]"},
		requireStableAgent: true,
	})
	return s.finishPKIEnrollment(ctx, request, false, result, err)
}

// EnrollAndBindAgent is the production registration entrypoint. It creates or
// updates the stable control-plane agent row inside the same transaction that
// consumes the one-time token and issues the tunnel certificate.
func (s *PKIEnrollmentService) EnrollAndBindAgent(ctx context.Context, request PKIEnrollRequest, agent storage.AgentRow) (PKIEnrollmentResult, error) {
	digest, err := digestPKIEnrollmentToken(request.Token)
	if err != nil {
		return s.finishPKIEnrollment(ctx, request, false, PKIEnrollmentResult{}, ErrPKIEnrollmentTokenRejected)
	}
	result, err := s.enroll(ctx, request, pkiEnrollmentCredential{
		tokenDigest: digest, stableAgent: &agent, requireStableAgent: true,
	})
	return s.finishPKIEnrollment(ctx, request, false, result, err)
}

// EnrollAuthenticated handles renewals and listener enrollment after the
// existing control token has already resolved the stable agent owner.
func (s *PKIEnrollmentService) EnrollAuthenticated(ctx context.Context, agentID, controlToken string, request PKIEnrollRequest) (PKIEnrollmentResult, error) {
	request.AgentID = strings.TrimSpace(agentID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	controlToken = strings.TrimSpace(controlToken)
	if request.AgentID == "" || request.RequestID == "" || controlToken == "" {
		return s.finishPKIEnrollment(ctx, request, false, PKIEnrollmentResult{}, ErrPKIEnrollmentOwnerMismatch)
	}
	result, err := s.enroll(ctx, request, pkiEnrollmentCredential{authenticated: true, controlToken: controlToken})
	return s.finishPKIEnrollment(ctx, request, false, result, err)
}

// EnrollLocal is intentionally tokenless and only available as a direct
// service call. Its owner is always the configured LocalAgentID, so the
// embedded agent never needs a bootstrap secret that could escape via config,
// API models, or logs.
func (s *PKIEnrollmentService) EnrollLocal(ctx context.Context, request PKILocalEnrollRequest) (PKIEnrollmentResult, error) {
	enrollRequest := PKIEnrollRequest{
		RequestID: request.RequestID, AgentID: s.localAgentID,
		Kind: request.Kind, ListenerID: request.ListenerID, Purpose: request.Purpose,
		CSRPEM: request.CSRPEM, DNSNames: request.DNSNames, IPAddresses: request.IPAddresses,
	}
	if s.localAgentID == "" {
		return s.finishPKIEnrollment(ctx, enrollRequest, true, PKIEnrollmentResult{}, fmt.Errorf("%w: local agent identity is not configured", ErrPKIEnrollmentRequest))
	}
	result, err := s.enroll(ctx, enrollRequest, pkiEnrollmentCredential{local: true})
	return s.finishPKIEnrollment(ctx, enrollRequest, true, result, err)
}

func (s *PKIEnrollmentService) enroll(ctx context.Context, request PKIEnrollRequest, credential pkiEnrollmentCredential) (PKIEnrollmentResult, error) {
	request.RequestID = strings.TrimSpace(request.RequestID)
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
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIEnrollmentResult{}, err
	}
	replayKey, requestFingerprint, err := pkiEnrollmentReplayIdentity(request, credential)
	if err != nil {
		return PKIEnrollmentResult{}, err
	}
	var result PKIEnrollmentResult
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIEnrollmentLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("%w: PKI settings are not initialized", ErrPKIEnrollmentAuthorityUnavailable)
		}
		if settings.PKIDomainID != grant.PKIDomainID || settings.PKIEpoch != grant.PKIEpoch {
			return ErrPKILeaseNotHeld
		}
		if err := requirePKIEnrollmentIssuanceWindow(ctx, tx, settings, credential); err != nil {
			return err
		}
		if credential.authenticated {
			agent, found, err := tx.GetPKIStableAgentForUpdate(ctx, request.AgentID)
			if err != nil {
				return err
			}
			if !found || agent.IsLocal || !constantTimePKITokenMatch(agent.AgentToken, credential.controlToken) {
				return ErrPKIEnrollmentOwnerMismatch
			}
		}
		if replayKey != "" {
			replay, replayFound, err := tx.FindPKIEnrollmentReplayForUpdate(ctx, replayKey)
			if err != nil {
				return err
			}
			if replayFound {
				if replay.PKIDomainID != settings.PKIDomainID || !strings.EqualFold(replay.RequestFingerprint, requestFingerprint) {
					return fmt.Errorf("%w: enrollment replay key was reused with different input", errPKIEnrollmentClientRequest)
				}
				if !replay.ExpiresAt.After(now) {
					if credential.authenticated || credential.local {
						return fmt.Errorf("%w: enrollment replay expired", errPKIEnrollmentClientRequest)
					}
					return ErrPKIEnrollmentTokenRejected
				}
				if err := json.Unmarshal([]byte(replay.ResultJSON), &result); err != nil {
					return fmt.Errorf("%w: persisted enrollment replay is invalid", ErrPKIEnrollmentRequest)
				}
				if err := validatePKIEnrollmentReplay(ctx, tx, settings.PKIDomainID, request, credential, result, now); err != nil {
					return err
				}
				return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
			}
		}
		ownerAgentID := request.AgentID
		tokenScope := "local"
		operatorID := ""
		anonymousNewAgent := false
		replayExpiresAt := now.Add(pkiEnrollmentReplayTTL)
		if credential.local {
			if ownerAgentID != s.localAgentID {
				return ErrPKIEnrollmentOwnerMismatch
			}
		} else if credential.authenticated {
			if ownerAgentID == "" || (s.localAgentID != "" && ownerAgentID == s.localAgentID) {
				return ErrPKIEnrollmentOwnerMismatch
			}
			exists, err := tx.PKIStableAgentExistsForUpdate(ctx, ownerAgentID)
			if err != nil {
				return err
			}
			if !exists {
				return ErrPKIEnrollmentOwnerMismatch
			}
			tokenScope = "authenticated_control"
			operatorID = ownerAgentID
		} else {
			token, consumed, err := tx.ConsumePKIEnrollmentToken(ctx, credential.tokenDigest, now)
			if err != nil {
				return err
			}
			if !consumed {
				return ErrPKIEnrollmentTokenRejected
			}
			if token.ExpiresAt.Before(replayExpiresAt) {
				replayExpiresAt = token.ExpiresAt
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
		if credential.requireStableAgent {
			if credential.stableAgent == nil || request.Kind != storage.PKIIdentityKindAgent {
				return fmt.Errorf("%w: stable agent binding is required for remote registration", ErrPKIEnrollmentRequest)
			}
			agent := *credential.stableAgent
			agent.ID = ownerAgentID
			if request.AgentID != "" && request.AgentID != ownerAgentID {
				return ErrPKIEnrollmentOwnerMismatch
			}
			stableAgent, err := tx.UpsertPKIStableAgent(ctx, agent, anonymousNewAgent)
			if err != nil {
				return err
			}
			result.AgentControlToken = stableAgent.AgentToken
			if request.SecurityAcknowledgement != nil {
				state, err := tx.LoadPKICanonicalState(ctx)
				if err != nil {
					return err
				}
				if err := validatePKISecurityAcknowledgementForState(state, *request.SecurityAcknowledgement); err != nil {
					return err
				}
				encoded, err := json.Marshal(request.SecurityAcknowledgement)
				if err != nil {
					return err
				}
				if err := tx.SavePKISecurityAcknowledgement(ctx, ownerAgentID, string(encoded), now); err != nil {
					return err
				}
			}
		}
		if request.Kind == storage.PKIIdentityKindListener {
			if !credential.authenticated && !credential.local {
				return fmt.Errorf("%w: listener enrollment requires authenticated control", ErrPKIEnrollmentOwnerMismatch)
			}
			listenerID, err := strconv.Atoi(request.ListenerID)
			if err != nil || listenerID <= 0 {
				return fmt.Errorf("%w: listener ID is invalid", ErrPKIEnrollmentOwnerMismatch)
			}
			listener, listenerFound, err := tx.GetRelayListenerForUpdate(ctx, ownerAgentID, listenerID)
			if err != nil {
				return err
			}
			if !listenerFound {
				return fmt.Errorf("%w: listener is not owned by the authenticated agent", ErrPKIEnrollmentOwnerMismatch)
			}
			dnsNames, ipAddresses, err := canonicalPKIListenerSANs(listener)
			if err != nil {
				return err
			}
			requestedDNS, err := normalizePKIDNSNames(request.DNSNames)
			if err != nil {
				return fmt.Errorf("%w: %v", errPKIEnrollmentClientRequest, err)
			}
			requestedIPs, err := normalizePKIIPAddresses(request.IPAddresses)
			if err != nil {
				return fmt.Errorf("%w: %v", errPKIEnrollmentClientRequest, err)
			}
			if !equalPKIStrings(requestedDNS, dnsNames) || !equalPKIIPs(requestedIPs, ipAddresses) {
				return fmt.Errorf("%w: listener SANs do not match canonical listener endpoints", ErrPKIEnrollmentOwnerMismatch)
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
		if credential.authenticated && request.Kind == storage.PKIIdentityKindAgent &&
			(!identityFound || identity.State != storage.PKIIdentityStateActive && identity.State != storage.PKIIdentityStateEnrollmentRequired) {
			return fmt.Errorf("%w: authenticated agent enrollment requires an active or enrollment-required identity", ErrPKIEnrollmentOwnerMismatch)
		}
		if request.Kind == storage.PKIIdentityKindListener && (!identityFound || identity.State == storage.PKIIdentityStateRevoked) {
			return fmt.Errorf("%w: listener PKI identity is not enrollment-ready", ErrPKIEnrollmentOwnerMismatch)
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

		authority, preparedAuthority, err := selectPKIEnrollmentAuthorityForUpdate(ctx, tx, settings)
		if err != nil {
			return err
		}
		signer, err := s.authoritySigner.LoadSigner(ctx, authority)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("%w: %w", ErrPKIEnrollmentAuthorityUnavailable, err)
		}
		endpointLifetime, err := validatedPKIEndpointLifetime(settings.EndpointLifetimeSeconds)
		if err != nil {
			return err
		}
		certificate, err := issuePKIIdentityCertificate(s.random, now, endpointLifetime, authority, preparedAuthority, signer, csr, binding)
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
		if identityFound || tokenScope == PKIEnrollmentTokenScopeBoundReenrollment {
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
			AgentID: binding.AgentID, AgentControlToken: result.AgentControlToken,
			IdentityID: identityID, CertificateID: certificateID, Purpose: binding.Purpose,
			CertificatePEM: certificate.CertificatePEM, PublicKeyFingerprint: certificate.PublicKeyFingerprint,
			AuthorityID: certificate.AuthorityID, CAGeneration: certificate.CAGeneration,
			NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		}
		if replayKey != "" {
			encodedResult, err := json.Marshal(result)
			if err != nil {
				return err
			}
			replayDigest := sha256.Sum256([]byte(replayKey))
			if err := tx.CreatePKIEnrollmentReplay(ctx, storage.PKIEnrollmentReplayRow{
				ID: "enrollment-replay-" + hex.EncodeToString(replayDigest[:]), PKIDomainID: settings.PKIDomainID,
				RequestKey: replayKey, RequestFingerprint: requestFingerprint, ResultJSON: string(encodedResult),
				ExpiresAt: replayExpiresAt, CreatedAt: recordedAt,
			}); err != nil {
				return err
			}
		}
		return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		return PKIEnrollmentResult{}, err
	}
	return result, nil
}

func requirePKIEnrollmentIssuanceWindow(
	ctx context.Context,
	tx *storage.PKITransaction,
	settings storage.PKISettingsRow,
	credential pkiEnrollmentCredential,
) error {
	if !settings.RelayFailClosed {
		return nil
	}
	job, found, err := tx.GetActivePKILifecycleJobByKindForUpdate(ctx, "emergency_ca_rotate")
	if err != nil {
		return err
	}
	// Before replacement, every issuance path remains stopped so the old CA
	// cannot mint a late credential. After replacement only explicit one-time
	// re-enrollment and the direct embedded-agent bootstrap may use the new CA;
	// ordinary authenticated renewal stays rejected.
	if !found || job.Phase != "relay_enable_pending" || credential.authenticated {
		return fmt.Errorf("%w: certificate issuance is stopped by emergency CA rotation", ErrPKIEnrollmentAuthorityUnavailable)
	}
	return nil
}

// selectPKIEnrollmentAuthorityForUpdate keeps ordinary issuance on the active
// CA, but directs the explicitly gated reissue/cutover phases to the prepared
// replacement. Selecting a prepared signer from authority status alone would
// allow natural renewals to outrun dual-trust acknowledgement, so the durable
// lifecycle job and its exact authority fingerprints are part of the fence.
func selectPKIEnrollmentAuthorityForUpdate(
	ctx context.Context,
	tx *storage.PKITransaction,
	settings storage.PKISettingsRow,
) (storage.PKIAuthorityRow, bool, error) {
	job, found, err := tx.GetActivePKILifecycleJobByKindForUpdate(ctx, "ca_rotate")
	if err != nil {
		return storage.PKIAuthorityRow{}, false, err
	}
	if found && (job.Phase == PKICARotationPhaseReissue || job.Phase == PKICARotationPhaseCutover) {
		payload, err := decodePKIAuthorityRuntime(job)
		if err != nil || payload.Rotation.Phase != job.Phase ||
			payload.Rotation.NewGeneration <= payload.Rotation.CurrentGeneration {
			return storage.PKIAuthorityRow{}, false, fmt.Errorf(
				"%w: normal CA rotation reissue state is invalid",
				ErrPKIEnrollmentAuthorityUnavailable,
			)
		}
		authorities, err := tx.ListTrustedPKIAuthoritiesForUpdate(ctx, settings.PKIDomainID)
		if err != nil {
			return storage.PKIAuthorityRow{}, false, err
		}
		for _, authority := range authorities {
			if authority.Generation != payload.Rotation.NewGeneration {
				continue
			}
			keyFingerprint, fingerprintErr := pkiAuthorityPublicKeyFingerprint(authority.CertificatePEM)
			if fingerprintErr != nil || authority.Status != "prepared" ||
				!strings.EqualFold(authority.FingerprintSHA256, payload.Rotation.NewCertFingerprint) ||
				!strings.EqualFold(keyFingerprint, payload.Rotation.NewKeyFingerprint) {
				return storage.PKIAuthorityRow{}, false, fmt.Errorf(
					"%w: prepared CA does not match the normal rotation job",
					ErrPKIEnrollmentAuthorityUnavailable,
				)
			}
			return authority, true, nil
		}
		return storage.PKIAuthorityRow{}, false, fmt.Errorf(
			"%w: prepared CA for generation %d is unavailable",
			ErrPKIEnrollmentAuthorityUnavailable,
			payload.Rotation.NewGeneration,
		)
	}

	authority, found, err := tx.GetActivePKIAuthorityForUpdate(ctx, settings.PKIDomainID)
	if err != nil {
		return storage.PKIAuthorityRow{}, false, err
	}
	if !found {
		return storage.PKIAuthorityRow{}, false, ErrPKIEnrollmentAuthorityUnavailable
	}
	return authority, false, nil
}

func requirePKIEnrollmentLeaseFence(ctx context.Context, tx *storage.PKITransaction, grant PKILeaseGrant) error {
	err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
		PKIDomainID: grant.PKIDomainID, PKIEpoch: grant.PKIEpoch, InstanceID: grant.InstanceID,
		LeaseTerm: grant.LeaseTerm, LeaseDeadline: grant.LeaseDeadline,
	})
	if errors.Is(err, storage.ErrPKILeaseFence) {
		return ErrPKILeaseNotHeld
	}
	return err
}

func constantTimePKITokenMatch(stored, presented string) bool {
	stored = strings.TrimSpace(stored)
	presented = strings.TrimSpace(presented)
	return stored != "" && len(stored) == len(presented) && subtle.ConstantTimeCompare([]byte(stored), []byte(presented)) == 1
}

func validatePKIEnrollmentReplay(
	ctx context.Context,
	tx *storage.PKITransaction,
	domainID string,
	request PKIEnrollRequest,
	credential pkiEnrollmentCredential,
	result PKIEnrollmentResult,
	now time.Time,
) error {
	if result.AgentID == "" || result.IdentityID == "" || result.CertificateID == "" || !result.NotAfter.After(now) {
		return fmt.Errorf("%w: persisted enrollment replay is no longer active", ErrPKIEnrollmentRequest)
	}
	if result.AgentControlToken != "" {
		agent, found, err := tx.GetPKIStableAgentForUpdate(ctx, result.AgentID)
		if err != nil {
			return err
		}
		if !found || !constantTimePKITokenMatch(agent.AgentToken, result.AgentControlToken) {
			if credential.authenticated || credential.local {
				return ErrPKIEnrollmentOwnerMismatch
			}
			return ErrPKIEnrollmentTokenRejected
		}
	}
	identity, found, err := tx.FindPKIIdentityForUpdate(ctx, domainID, request.Kind, result.AgentID, request.ListenerID)
	if err != nil {
		return err
	}
	if !found || identity.ID != result.IdentityID || identity.State != storage.PKIIdentityStateActive ||
		identity.CurrentCertificateID == nil || *identity.CurrentCertificateID != result.CertificateID {
		return fmt.Errorf("%w: replayed identity is no longer active", ErrPKIEnrollmentOwnerMismatch)
	}
	certificate, found, err := tx.GetPKICertificateForUpdate(ctx, result.CertificateID)
	if err != nil {
		return err
	}
	if !found || certificate.IdentityID != identity.ID || certificate.Status != storage.PKICertificateStatusActive ||
		!certificate.NotAfter.After(now) {
		return fmt.Errorf("%w: replayed certificate is no longer active", ErrPKIEnrollmentOwnerMismatch)
	}
	return nil
}

func pkiEnrollmentReplayIdentity(request PKIEnrollRequest, credential pkiEnrollmentCredential) (string, string, error) {
	requestKey := ""
	switch {
	case credential.local:
		if request.RequestID == "" {
			return "", "", fmt.Errorf("%w: local enrollment request ID is required", errPKIEnrollmentClientRequest)
		}
		requestKey = "local:" + request.AgentID + ":" + request.RequestID
	case credential.authenticated:
		if request.RequestID == "" {
			return "", "", fmt.Errorf("%w: authenticated enrollment request ID is required", errPKIEnrollmentClientRequest)
		}
		requestKey = "control:" + request.AgentID + ":" + request.RequestID
	default:
		if request.RequestID == "" {
			return "", "", fmt.Errorf("%w: registration enrollment request ID is required", errPKIEnrollmentClientRequest)
		}
		if !validPKIEnrollmentDigest(credential.tokenDigest) {
			return "", "", ErrPKIEnrollmentTokenRejected
		}
		requestKey = "registration:" + credential.tokenDigest + ":" + request.RequestID
	}
	type stableAgentFingerprint struct {
		Name             string `json:"name"`
		AgentURL         string `json:"agent_url"`
		Version          string `json:"version"`
		Platform         string `json:"platform"`
		TagsJSON         string `json:"tags"`
		CapabilitiesJSON string `json:"capabilities"`
		Mode             string `json:"mode"`
	}
	payload := struct {
		RequestID               string                              `json:"request_id"`
		AgentID                 string                              `json:"agent_id"`
		Kind                    string                              `json:"kind"`
		ListenerID              string                              `json:"listener_id"`
		Purpose                 string                              `json:"purpose"`
		CSRPEM                  string                              `json:"csr_pem"`
		DNSNames                []string                            `json:"dns_names"`
		IPAddresses             []string                            `json:"ip_addresses"`
		SecurityAcknowledgement *storage.PKISecurityAcknowledgement `json:"security_ack"`
		StableAgent             *stableAgentFingerprint             `json:"stable_agent,omitempty"`
	}{
		RequestID: request.RequestID, AgentID: request.AgentID, Kind: request.Kind, ListenerID: request.ListenerID,
		Purpose: request.Purpose, CSRPEM: strings.TrimSpace(request.CSRPEM), DNSNames: request.DNSNames,
		IPAddresses: request.IPAddresses, SecurityAcknowledgement: request.SecurityAcknowledgement,
	}
	if credential.stableAgent != nil {
		agent := credential.stableAgent
		payload.StableAgent = &stableAgentFingerprint{
			Name: agent.Name, AgentURL: agent.AgentURL, Version: agent.Version, Platform: agent.Platform,
			TagsJSON: agent.TagsJSON, CapabilitiesJSON: agent.CapabilitiesJSON, Mode: agent.Mode,
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	fingerprint := sha256.Sum256(encoded)
	return requestKey, hex.EncodeToString(fingerprint[:]), nil
}

func validPKIEnrollmentDigest(value string) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == sha256.Size
}

func canonicalPKIListenerSANs(listener storage.RelayListenerRow) ([]string, []net.IP, error) {
	hosts := []string{listener.PublicHost, listener.ListenHost}
	var bindHosts []string
	if value := strings.TrimSpace(listener.BindHostsJSON); value != "" {
		if err := json.Unmarshal([]byte(value), &bindHosts); err != nil {
			return nil, nil, fmt.Errorf("%w: canonical listener bind hosts are invalid", ErrPKIEnrollmentRequest)
		}
	}
	hosts = append(hosts, bindHosts...)
	dnsInput := make([]string, 0, len(hosts))
	ipInput := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if !ip.IsUnspecified() {
				ipInput = append(ipInput, ip.String())
			}
			continue
		}
		dnsInput = append(dnsInput, host)
	}
	dnsNames, err := normalizePKIDNSNames(dnsInput)
	if err != nil {
		return nil, nil, err
	}
	ipAddresses, err := normalizePKIIPAddresses(ipInput)
	if err != nil {
		return nil, nil, err
	}
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return nil, nil, fmt.Errorf("%w: listener has no certificate endpoint", ErrPKIEnrollmentRequest)
	}
	return dnsNames, ipAddresses, nil
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
		return fmt.Errorf("%w: CSR is required", errPKIEnrollmentClientRequest)
	}
	switch request.Kind {
	case storage.PKIIdentityKindAgent:
		if request.ListenerID != "" || request.Purpose != storage.PKICertificatePurposeClient || len(request.DNSNames) != 0 || len(request.IPAddresses) != 0 {
			return fmt.Errorf("%w: agent enrollment requires client purpose", errPKIEnrollmentClientRequest)
		}
	case storage.PKIIdentityKindListener:
		if request.ListenerID == "" || request.Purpose != storage.PKICertificatePurposeServer {
			return fmt.Errorf("%w: listener enrollment requires listener ID and server purpose", errPKIEnrollmentClientRequest)
		}
		if err := validatePKIURISegment("listener", request.ListenerID); err != nil {
			return fmt.Errorf("%w: listener ID is invalid", errPKIEnrollmentClientRequest)
		}
	default:
		return fmt.Errorf("%w: unsupported identity kind", errPKIEnrollmentClientRequest)
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
