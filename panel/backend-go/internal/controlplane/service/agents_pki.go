package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type internalPKIControlStore interface {
	PKITransactionStore
	pkiCanonicalStateSource
}

type PKIActivationFinalizer interface {
	FinalizeTunnelMTLSUpgrade(context.Context) error
}

type InternalPKIServiceOptions struct {
	Store          internalPKIControlStore
	Lease          PKILeaseGate
	Tokens         *PKITokenService
	Enrollment     *PKIEnrollmentService
	Revocation     *PKIRevocationService
	SnapshotSigner PKISecuritySnapshotSigner
	Tasks          *TaskService
	Activation     PKIActivationFinalizer
	Clock          func() time.Time
	Random         io.Reader
}

type InternalPKIService struct {
	store          internalPKIControlStore
	lease          PKILeaseGate
	tokens         *PKITokenService
	enrollment     *PKIEnrollmentService
	revocation     *PKIRevocationService
	snapshotSigner PKISecuritySnapshotSigner
	tasks          *TaskService
	activation     PKIActivationFinalizer
	clock          func() time.Time
	random         io.Reader
}

func NewInternalPKIService(options InternalPKIServiceOptions) (*InternalPKIService, error) {
	if options.Store == nil || options.Lease == nil || options.Tokens == nil || options.Enrollment == nil ||
		options.Revocation == nil || options.SnapshotSigner == nil || options.Tasks == nil || options.Activation == nil {
		return nil, fmt.Errorf("%w: internal PKI service dependencies are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &InternalPKIService{
		store: options.Store, lease: options.Lease, tokens: options.Tokens, enrollment: options.Enrollment,
		revocation: options.Revocation, snapshotSigner: options.SnapshotSigner, tasks: options.Tasks,
		activation: options.Activation,
		clock:      options.Clock, random: options.Random,
	}, nil
}

// RegisterAgent is invoked by the existing /agents/register handler. The
// control token remains the HTTP credential; the one-time token authorizes only
// certificate enrollment and is consumed in the same transaction as AgentRow.
func (s *InternalPKIService) RegisterAgent(ctx context.Context, request RegisterRequest, agent storage.AgentRow) (PKIRegistrationReply, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKIRegistrationReply{}, err
	}
	if state.Settings == nil {
		return PKIRegistrationReply{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	if err := preflightPKIRegistrationToken(state, request, s.clock().UTC()); err != nil {
		return PKIRegistrationReply{}, err
	}
	if request.PKISecurityAck != nil {
		if err := validatePKISecurityAcknowledgement(*state.Settings, *request.PKISecurityAck); err != nil {
			return PKIRegistrationReply{}, err
		}
	}
	// Produce trust/safety material before consuming the one-time token. If the
	// lease or CA key is unavailable, registration fails without committing an
	// agent/certificate that the caller never received.
	snapshot, err := s.fullSecuritySnapshot(ctx, state)
	if err != nil {
		return PKIRegistrationReply{}, err
	}
	result, err := s.enrollment.EnrollAndBindAgent(ctx, PKIEnrollRequest{
		Token: request.RegisterToken, AgentID: request.AgentID, Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient, CSRPEM: request.TunnelCSRPEM,
		SecurityAcknowledgement: request.PKISecurityAck,
	}, agent)
	if err != nil {
		return PKIRegistrationReply{}, err
	}
	return PKIRegistrationReply{
		AgentID: result.AgentID, AgentToken: result.AgentControlToken,
		TunnelCredential: storage.PKITunnelCredential{
			IdentityID: result.IdentityID, CertificateID: result.CertificateID, Purpose: result.Purpose,
			CertificatePEM: result.CertificatePEM, PublicKeyFingerprint: result.PublicKeyFingerprint,
			AuthorityID: result.AuthorityID, CAGeneration: result.CAGeneration,
			NotBefore: result.NotBefore, NotAfter: result.NotAfter,
		},
		SecuritySnapshot: snapshot,
	}, nil
}

func preflightPKIRegistrationToken(state storage.PKICanonicalState, request RegisterRequest, now time.Time) error {
	digest, err := digestPKIEnrollmentToken(request.RegisterToken)
	if err != nil || now.IsZero() {
		return ErrPKIEnrollmentTokenRejected
	}
	requestAgentID := strings.TrimSpace(request.AgentID)
	for _, token := range state.EnrollmentTokens {
		if len(token.TokenDigestSHA256) != len(digest) ||
			subtle.ConstantTimeCompare([]byte(token.TokenDigestSHA256), []byte(digest)) != 1 {
			continue
		}
		if token.ConsumedAt != nil || !token.ExpiresAt.After(now) {
			return ErrPKIEnrollmentTokenRejected
		}
		switch token.Scope {
		case PKIEnrollmentTokenScopeNewAgent:
			if strings.TrimSpace(token.BoundAgentID) != "" || requestAgentID != "" {
				return ErrPKIEnrollmentTokenRejected
			}
		case PKIEnrollmentTokenScopeBoundReenrollment:
			boundAgentID := strings.TrimSpace(token.BoundAgentID)
			if boundAgentID == "" || requestAgentID != "" && requestAgentID != boundAgentID {
				return ErrPKIEnrollmentTokenRejected
			}
		default:
			return ErrPKIEnrollmentTokenRejected
		}
		return nil
	}
	return ErrPKIEnrollmentTokenRejected
}

func (s *InternalPKIService) ControlSync(
	ctx context.Context,
	agentID string,
	acknowledgement *storage.PKISecurityAcknowledgement,
	requests []PKIControlEnrollmentRequest,
) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	snapshot, err := s.SecuritySnapshot(ctx, agentID, acknowledgement)
	if err != nil {
		// A plain token-authenticated heartbeat remains usable while a lease or
		// encrypted CA key is unavailable. Relay listeners were already stripped
		// of legacy authentication by PrepareRelayListeners; enrollment requests
		// still fail before issuing any unreachable credential.
		if len(requests) == 0 && temporaryPKISnapshotUnavailable(err) {
			return storage.PKISecuritySnapshot{}, nil, nil
		}
		return storage.PKISecuritySnapshot{}, nil, err
	}
	credentials := make([]PKIControlCredential, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		requestID := strings.TrimSpace(request.RequestID)
		if requestID == "" {
			return storage.PKISecuritySnapshot{}, nil, fmt.Errorf("%w: PKI enrollment request_id is required", ErrPKIEnrollmentRequest)
		}
		if _, duplicate := seen[requestID]; duplicate {
			return storage.PKISecuritySnapshot{}, nil, fmt.Errorf("%w: duplicate PKI enrollment request_id", ErrPKIEnrollmentRequest)
		}
		seen[requestID] = struct{}{}
		result, err := s.enrollment.EnrollAuthenticated(ctx, agentID, PKIEnrollRequest{
			Kind: request.Kind, ListenerID: request.ListenerID, Purpose: request.Purpose,
			CSRPEM: request.CSRPEM, DNSNames: request.DNSNames, IPAddresses: request.IPAddresses,
		})
		if err != nil {
			return storage.PKISecuritySnapshot{}, nil, err
		}
		credentials = append(credentials, PKIControlCredential{
			RequestID: requestID,
			Credential: storage.PKITunnelCredential{
				IdentityID: result.IdentityID, CertificateID: result.CertificateID, Purpose: result.Purpose,
				CertificatePEM: result.CertificatePEM, PublicKeyFingerprint: result.PublicKeyFingerprint,
				AuthorityID: result.AuthorityID, CAGeneration: result.CAGeneration,
				NotBefore: result.NotBefore, NotAfter: result.NotAfter,
			},
		})
	}
	return snapshot, credentials, nil
}

func temporaryPKISnapshotUnavailable(err error) bool {
	return errors.Is(err, ErrPKILeaseNotHeld) || errors.Is(err, ErrPKIVaultInvalid) ||
		errors.Is(err, ErrPKIEnrollmentAuthorityUnavailable)
}

// PrepareRelayListeners removes the legacy managed-certificate authentication
// fields from the control snapshot and attaches only canonical PKI references.
func (s *InternalPKIService) PrepareRelayListeners(ctx context.Context, agentID string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	if state.Settings == nil {
		return nil, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	identities := make(map[string]storage.PKIIdentityRow)
	for _, identity := range state.Identities {
		if identity.Kind == storage.PKIIdentityKindListener && identity.AgentID == agentID {
			identities[identity.ListenerID] = identity
		}
	}
	prepared := make([]storage.RelayListener, len(listeners))
	for index, listener := range listeners {
		listener.CertificateID = nil
		listener.TLSMode = "pki_mtls"
		listener.PinSet = nil
		listener.TrustedCACertificateIDs = nil
		listener.AllowSelfSigned = false
		listener.PKIIdentityState = storage.PKIIdentityStateEnrollmentRequired
		if identity, ok := identities[strconv.Itoa(listener.ID)]; ok {
			listener.PKIIdentityID = identity.ID
			listener.PKIIdentityState = identity.State
			if identity.CurrentCertificateID != nil {
				listener.PKICertificateID = *identity.CurrentCertificateID
			}
		}
		prepared[index] = listener
	}
	return prepared, nil
}

func (s *InternalPKIService) SecuritySnapshot(ctx context.Context, agentID string, acknowledgement *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	if state.Settings == nil {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	if acknowledgement != nil {
		if err := validatePKISecurityAcknowledgement(*state.Settings, *acknowledgement); err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
		encoded, err := json.Marshal(acknowledgement)
		if err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
		if err := s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
			return tx.SavePKISecurityAcknowledgement(ctx, agentID, string(encoded), s.clock().UTC())
		}); err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
		state, err = s.store.LoadPKICanonicalState(ctx)
		if err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
	}
	return s.fullSecuritySnapshot(ctx, state)
}

func validatePKISecurityAcknowledgement(settings storage.PKISettingsRow, acknowledgement storage.PKISecurityAcknowledgement) error {
	if strings.TrimSpace(acknowledgement.PKIDomainID) != settings.PKIDomainID || acknowledgement.PKIEpoch < 0 || acknowledgement.SecurityRevision < 0 {
		return fmt.Errorf("%w: PKI security acknowledgement domain/version is invalid", ErrPKILifecycleInvalid)
	}
	incoming := PKISecurityVersion{PKIEpoch: acknowledgement.PKIEpoch, SecurityRevision: acknowledgement.SecurityRevision}
	current := PKISecurityVersion{PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision}
	if ComparePKISecurityVersion(incoming, current) > 0 || (incoming.PKIEpoch > 0 && incoming.PKIEpoch > settings.PKIEpoch) {
		return fmt.Errorf("%w: PKI security acknowledgement is ahead of canonical state", ErrPKIEpochStale)
	}
	if acknowledgement.PKIEpoch == settings.PKIEpoch && acknowledgement.SecurityRevision == 0 && settings.SecurityRevision == 0 && !acknowledgement.Full {
		return fmt.Errorf("%w: first acknowledgement for an epoch must be full", ErrPKIEpochStale)
	}
	return nil
}

func (s *InternalPKIService) fullSecuritySnapshot(ctx context.Context, state storage.PKICanonicalState) (storage.PKISecuritySnapshot, error) {
	settings := state.Settings
	if settings == nil {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	trust := make([]int64, 0, len(state.Authorities))
	for _, authority := range state.Authorities {
		if authority.Status == "active" || authority.Status == "prepared" || authority.Status == "retiring" {
			trust = append(trust, authority.Generation)
		}
	}
	revokedIdentities := make([]string, 0)
	for _, identity := range state.Identities {
		if identity.State == storage.PKIIdentityStateRevoked {
			revokedIdentities = append(revokedIdentities, identity.ID)
		}
	}
	revokedSerials := make([]string, 0)
	for _, certificate := range state.Certificates {
		if certificate.Status == storage.PKICertificateStatusRevoked {
			revokedSerials = append(revokedSerials, certificate.SerialHex)
		}
	}
	slices.Sort(trust)
	slices.Sort(revokedIdentities)
	slices.Sort(revokedSerials)
	signed, err := s.snapshotSigner.SignPKISecuritySnapshot(ctx, PKIUnsignedSecuritySnapshot{
		PKIDomainID: settings.PKIDomainID,
		Version: PKISecuritySnapshotVersion{
			Version: PKISecurityVersion{PKIEpoch: settings.PKIEpoch, SecurityRevision: settings.SecurityRevision}, Full: true,
		},
		IssuedAt: s.clock().UTC(), TrustGenerations: trust,
		RevokedIdentityIDs: revokedIdentities, RevokedSerials: revokedSerials,
	})
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	return storagePKISecuritySnapshot(state, signed)
}

func (s *InternalPKIService) Overview(ctx context.Context) (PKIOverview, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKIOverview{}, err
	}
	if state.Settings == nil {
		return PKIOverview{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	return PKIOverview{
		PKIDomainID: state.Settings.PKIDomainID, PKIEpoch: state.Settings.PKIEpoch,
		SecurityRevision: state.Settings.SecurityRevision, UpgradeState: state.Settings.UpgradeState,
		AuthorityCount: len(state.Authorities), IdentityCount: len(state.Identities), CertificateCount: len(state.Certificates),
	}, nil
}

func (s *InternalPKIService) Authorities(ctx context.Context) ([]PKIAuthorityView, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PKIAuthorityView, 0, len(state.Authorities))
	for _, row := range state.Authorities {
		result = append(result, PKIAuthorityView{
			ID: row.ID, Generation: row.Generation, Status: row.Status, CertificatePEM: row.CertificatePEM,
			FingerprintSHA256: row.FingerprintSHA256, NotBefore: row.NotBefore, NotAfter: row.NotAfter, RetireDeadline: row.RetireDeadline,
		})
	}
	return result, nil
}

func (s *InternalPKIService) Identities(ctx context.Context) ([]PKIIdentityView, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PKIIdentityView, 0, len(state.Identities))
	for _, row := range state.Identities {
		result = append(result, PKIIdentityView{
			ID: row.ID, Kind: row.Kind, AgentID: row.AgentID, ListenerID: row.ListenerID, State: row.State,
			CurrentCertificateID: row.CurrentCertificateID, RevokedAt: row.RevokedAt, RevokedReason: row.RevokedReason,
		})
	}
	return result, nil
}

func (s *InternalPKIService) Certificates(ctx context.Context) ([]PKICertificateView, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PKICertificateView, 0, len(state.Certificates))
	for _, row := range state.Certificates {
		result = append(result, PKICertificateView{
			ID: row.ID, SerialHex: row.SerialHex, IdentityID: row.IdentityID, Purpose: row.Purpose,
			AuthorityID: row.AuthorityID, CAGeneration: row.CAGeneration, CertificatePEM: row.CertificatePEM,
			PublicKeyFingerprint: row.PublicKeyFingerprint, NotBefore: row.NotBefore, NotAfter: row.NotAfter,
			Status: row.Status, RevokedAt: row.RevokedAt, RevokedReason: row.RevokedReason,
		})
	}
	return result, nil
}

func (s *InternalPKIService) Events(ctx context.Context, query PKIEventQuery) ([]PKIAuditEvent, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]PKIAuditEvent, 0, len(state.Events))
	for _, row := range state.Events {
		if !pkiEventMatchesQuery(row, query) {
			continue
		}
		event := PKIAuditEvent{
			ID: row.ID, Type: row.Type, OccurredAt: row.OccurredAt, Source: row.Source, OperatorID: row.OperatorID,
			ObjectType: row.ObjectType, ObjectID: row.ObjectID, Result: row.Result, Reason: row.Reason,
			SecurityRevision: row.SecurityRevision, Details: map[string]string{"raw_json": row.DetailsJSON},
		}
		if row.CertificateID != nil {
			event.CertificateID = *row.CertificateID
		}
		if row.CAGeneration != nil {
			event.CAGeneration = *row.CAGeneration
		}
		result = append(result, event)
	}
	return result, nil
}

func pkiEventMatchesQuery(row storage.PKIEventRow, query PKIEventQuery) bool {
	if query.Type != "" && row.Type != query.Type || query.IdentityID != "" && row.ObjectID != query.IdentityID ||
		query.OperatorID != "" && row.OperatorID != query.OperatorID || query.Source != "" && row.Source != query.Source ||
		query.Result != "" && row.Result != query.Result {
		return false
	}
	if query.SerialHex != "" && !strings.Contains(row.DetailsJSON, query.SerialHex) {
		return false
	}
	if query.CAGeneration != nil && (row.CAGeneration == nil || *row.CAGeneration != *query.CAGeneration) {
		return false
	}
	if query.From != nil && row.OccurredAt.Before(*query.From) || query.To != nil && row.OccurredAt.After(*query.To) {
		return false
	}
	return true
}

func (s *InternalPKIService) Alerts(ctx context.Context) ([]PKIDerivedAlert, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	facts := make([]PKIAlertFact, 0)
	policy := DefaultInternalPKIPolicy()
	for _, certificate := range state.Certificates {
		if certificate.Status != storage.PKICertificateStatusActive {
			continue
		}
		parsedCertificate, err := parsePKIAuthorityCertificate(certificate.CertificatePEM)
		if err != nil {
			return nil, err
		}
		certificateFingerprint := sha256.Sum256(parsedCertificate.Raw)
		certificateFingerprintHex := fmt.Sprintf("%x", certificateFingerprint)
		decision, err := EvaluatePKIEndpointSchedule(policy, PKIEndpointCertificateState{
			IdentityID: certificate.IdentityID, CertificateID: certificate.ID,
			CertificateFingerprintSHA256: certificateFingerprintHex,
			PublicKeyFingerprintSHA256:   certificate.PublicKeyFingerprint,
			Generation:                   certificate.CAGeneration, NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		}, now, false)
		if err != nil {
			return nil, err
		}
		if fact, ok := PKIEndpointAlertFact(PKIEndpointCertificateState{
			IdentityID: certificate.IdentityID, CertificateID: certificate.ID,
			CertificateFingerprintSHA256: certificateFingerprintHex,
			PublicKeyFingerprintSHA256:   certificate.PublicKeyFingerprint,
			Generation:                   certificate.CAGeneration, NotBefore: certificate.NotBefore, NotAfter: certificate.NotAfter,
		}, decision, now, ""); ok {
			facts = append(facts, fact)
		}
	}
	for _, job := range state.LifecycleJobs {
		if job.State != storage.PKILifecycleJobStateFailed && job.Phase != "blocked" {
			continue
		}
		level := PKIAlertWarning
		if job.Phase == "blocked" {
			level = PKIAlertCritical
		}
		facts = append(facts, PKIAlertFact{
			Kind: PKIAlertKindRotationBlocked, ObjectType: job.TargetType, ObjectID: job.TargetID,
			Level: level, FirstSeen: job.UpdatedAt, LastSeen: now, Reason: job.LastError,
		})
	}
	return DerivePKIAlerts(facts)
}

func (s *InternalPKIService) CreateEnrollmentToken(ctx context.Context, request PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error) {
	if _, err := s.lease.RequirePKILease(ctx); err != nil {
		return PKIEnrollmentToken{}, err
	}
	return s.tokens.Create(ctx, request)
}

func (s *InternalPKIService) Revoke(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, true); err != nil {
		return PKIOperation{}, err
	}
	commit, err := s.revocation.Revoke(ctx, PKIRevocationRequest{
		IdentityID: request.TargetID, Reason: request.Reason, Source: "panel", OperatorID: "panel",
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return s.Operation(ctx, fmt.Sprintf("revoke-%s-r%d", commit.Facts.IdentityID, commit.Facts.SecurityRevision))
}

func (s *InternalPKIService) ForceRotate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, false, true); err != nil {
		return PKIOperation{}, err
	}
	operation, err := s.queueOperation(ctx, "force_rotate", "identity", request.TargetID)
	if err != nil {
		return PKIOperation{}, err
	}
	state, loadErr := s.store.LoadPKICanonicalState(ctx)
	if loadErr == nil {
		for _, identity := range state.Identities {
			if identity.ID == request.TargetID {
				_, _ = s.tasks.CreateAndDispatch(TaskCreateRequest{
					AgentID: identity.AgentID, Type: TaskTypePKIForceRotation,
					Payload: map[string]any{"operation_id": operation.ID, "identity_id": identity.ID},
				})
				break
			}
		}
	}
	return operation, nil
}

func (s *InternalPKIService) RotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, false, false); err != nil {
		return PKIOperation{}, err
	}
	return s.queueOperation(ctx, "normal_ca_rotate", "authority", "domain")
}

func (s *InternalPKIService) EmergencyRotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, false); err != nil {
		return PKIOperation{}, err
	}
	return s.queueOperation(ctx, "emergency_ca_rotate", "authority", "domain")
}

func (s *InternalPKIService) ExportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if strings.TrimSpace(request.Passphrase) == "" {
		return PKIOperation{}, fmt.Errorf("%w: backup passphrase is required", ErrInvalidArgument)
	}
	return s.queueOperation(ctx, "protected_export", "backup", "pki")
}

func (s *InternalPKIService) ImportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if strings.TrimSpace(request.Passphrase) == "" {
		return PKIOperation{}, fmt.Errorf("%w: backup passphrase is required", ErrInvalidArgument)
	}
	return s.queueOperation(ctx, "protected_import", "backup", "pki")
}

func (s *InternalPKIService) Activate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, false); err != nil {
		return PKIOperation{}, err
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	var row storage.PKILifecycleJobRow
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
			PKIDomainID: grant.PKIDomainID, PKIEpoch: grant.PKIEpoch, InstanceID: grant.InstanceID,
			LeaseTerm: grant.LeaseTerm, LeaseDeadline: grant.LeaseDeadline,
		}); err != nil {
			if errors.Is(err, storage.ErrPKILeaseFence) {
				return ErrPKILeaseNotHeld
			}
			return err
		}
		idempotencyKey := fmt.Sprintf("activate:%s:%d", grant.PKIDomainID, grant.PKIEpoch)
		if existing, found, err := tx.FindPKILifecycleJobByIdempotencyForUpdate(ctx, idempotencyKey); err != nil {
			return err
		} else if found {
			row = existing
			return nil
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || settings.PKIDomainID != grant.PKIDomainID || settings.PKIEpoch != grant.PKIEpoch {
			return ErrPKILeaseNotHeld
		}
		now := s.clock().UTC()
		if settings.UpgradeState != PKIUpgradeStateTunnelMTLSOnly {
			if err := tx.SetPKIUpgradeState(ctx, settings.UpgradeState, PKIUpgradeStateTunnelMTLSOnly, now); err != nil {
				return err
			}
		}
		operationID := fmt.Sprintf("activate-%d", grant.PKIEpoch)
		deadline := grant.LeaseDeadline
		row = storage.PKILifecycleJobRow{
			ID: operationID, PKIDomainID: grant.PKIDomainID, TargetType: "pki_domain", TargetID: grant.PKIDomainID,
			Kind: "activate", Phase: "completed", State: storage.PKILifecycleJobStateSucceeded,
			OperationID: operationID, IdempotencyKey: idempotencyKey, LeaseOwner: grant.InstanceID,
			LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.CreatePKILifecycleJob(ctx, row); err != nil {
			return err
		}
		eventID, err := randomPKIIdentifier(s.random)
		if err != nil {
			return err
		}
		return tx.AppendPKIEvent(ctx, storage.PKIEventRow{
			ID: eventID, PKIDomainID: grant.PKIDomainID, Type: "pki.tunnel_mtls.activated", OccurredAt: now,
			Source: "panel", OperatorID: "panel", ObjectType: "pki_domain", ObjectID: grant.PKIDomainID,
			Result: "success", Reason: strings.TrimSpace(request.Reason), SecurityRevision: settings.SecurityRevision,
			DetailsJSON: `{"legacy_relay_authentication":"disabled","control_protocol":"token"}`,
		})
	})
	if err != nil {
		return PKIOperation{}, err
	}
	if err := s.activation.FinalizeTunnelMTLSUpgrade(ctx); err != nil {
		return PKIOperation{}, err
	}
	return pkiOperationFromRow(row), nil
}

func requirePKIAction(request PKIActionRequest, confirmed, target bool) error {
	if strings.TrimSpace(request.Reason) == "" {
		return fmt.Errorf("%w: reason is required", ErrInvalidArgument)
	}
	if target && strings.TrimSpace(request.TargetID) == "" {
		return fmt.Errorf("%w: target is required", ErrInvalidArgument)
	}
	if confirmed && strings.TrimSpace(request.ConfirmationNonce) == "" {
		return fmt.Errorf("%w: confirmation nonce is required", ErrInvalidArgument)
	}
	return nil
}

func (s *InternalPKIService) queueOperation(ctx context.Context, kind, targetType, targetID string) (PKIOperation, error) {
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	var row storage.PKILifecycleJobRow
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := tx.RequirePKILeaseFence(ctx, storage.PKILeaseFence{
			PKIDomainID: grant.PKIDomainID, PKIEpoch: grant.PKIEpoch, InstanceID: grant.InstanceID,
			LeaseTerm: grant.LeaseTerm, LeaseDeadline: grant.LeaseDeadline,
		}); err != nil {
			if errors.Is(err, storage.ErrPKILeaseFence) {
				return ErrPKILeaseNotHeld
			}
			return err
		}
		if existing, found, err := tx.FindActivePKILifecycleJobForTargetForUpdate(ctx, grant.PKIDomainID, targetType, targetID, kind); err != nil {
			return err
		} else if found {
			row = existing
			return nil
		}
		operationID, err := randomPKIIdentifier(s.random)
		if err != nil {
			return err
		}
		now := s.clock().UTC()
		deadline := grant.LeaseDeadline
		row = storage.PKILifecycleJobRow{
			ID: "pki-op-" + operationID, PKIDomainID: grant.PKIDomainID,
			TargetType: targetType, TargetID: targetID, Kind: kind, Phase: "queued",
			State: storage.PKILifecycleJobStatePending, OperationID: "pki-op-" + operationID,
			IdempotencyKey: kind + ":" + targetType + ":" + targetID + ":" + operationID,
			LeaseOwner:     grant.InstanceID, LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
		}
		return tx.CreatePKILifecycleJob(ctx, row)
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return pkiOperationFromRow(row), nil
}

func (s *InternalPKIService) Operation(ctx context.Context, operationID string) (PKIOperation, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	for _, row := range state.LifecycleJobs {
		if row.ID == operationID || row.OperationID == operationID {
			return pkiOperationFromRow(row), nil
		}
	}
	return PKIOperation{}, fmt.Errorf("%w: PKI operation not found", ErrPKILifecycleInvalid)
}

func pkiOperationFromRow(row storage.PKILifecycleJobRow) PKIOperation {
	return PKIOperation{
		ID: row.OperationID, Kind: row.Kind, TargetType: row.TargetType, TargetID: row.TargetID,
		State: row.State, Phase: row.Phase, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastError: row.LastError,
	}
}

func sortedPKITrustGenerations(roots []storage.PKITrustRoot) []int64 {
	result := make([]int64, 0, len(roots))
	for _, root := range roots {
		result = append(result, root.Generation)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func parsePKISecurityRevision(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}
