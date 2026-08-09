package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	pkiConfirmationNonceTTL              = 5 * time.Minute
	defaultPKIForceRotationConvergence   = 45 * time.Second
	pkiForceRotationPollInterval         = 100 * time.Millisecond
	defaultPKIInvalidDataCleanupInterval = time.Hour
	defaultPKIConsumedNonceRetention     = 24 * time.Hour
	defaultPKITerminalJobRetention       = 30 * 24 * time.Hour
)

type internalPKIControlStore interface {
	PKITransactionStore
	pkiCanonicalStateSource
}

type pkiForceRotationAcknowledgementStore interface {
	ListAgents(context.Context) ([]storage.AgentRow, error)
	LoadLocalAgentState(context.Context) (storage.LocalAgentStateRow, error)
	LocalAgentID() string
}

type PKIActivationFinalizer interface {
	FinalizeTunnelMTLSUpgrade(
		context.Context,
		string,
		func(context.Context, *storage.GormStore) error,
		func(context.Context, *storage.GormStore) error,
	) error
}

type InternalPKIServiceOptions struct {
	Store                      internalPKIControlStore
	Lease                      PKILeaseGate
	Tokens                     *PKITokenService
	Enrollment                 *PKIEnrollmentService
	Revocation                 *PKIRevocationService
	SnapshotSigner             PKISecuritySnapshotSigner
	Tasks                      *TaskService
	Backup                     *PKIBackupService
	Activation                 PKIActivationFinalizer
	Authority                  *PKIAuthorityRuntime
	Clock                      func() time.Time
	Random                     io.Reader
	InvalidDataCleanupInterval time.Duration
}

type InternalPKIService struct {
	store                      internalPKIControlStore
	lease                      PKILeaseGate
	tokens                     *PKITokenService
	enrollment                 *PKIEnrollmentService
	revocation                 *PKIRevocationService
	snapshotSigner             PKISecuritySnapshotSigner
	tasks                      *TaskService
	backup                     *PKIBackupService
	activation                 PKIActivationFinalizer
	authority                  *PKIAuthorityRuntime
	clock                      func() time.Time
	random                     io.Reader
	forceRotationConvergence   time.Duration
	invalidDataCleanupInterval time.Duration
	invalidDataCleanupMu       sync.Mutex
	lastInvalidDataCleanup     time.Time
}

var _ AgentPKIController = (*InternalPKIService)(nil)

func NewInternalPKIService(options InternalPKIServiceOptions) (*InternalPKIService, error) {
	if options.Store == nil || options.Lease == nil || options.Tokens == nil || options.Enrollment == nil ||
		options.Revocation == nil || options.SnapshotSigner == nil || options.Tasks == nil || options.Backup == nil || options.Activation == nil {
		return nil, fmt.Errorf("%w: internal PKI service dependencies are required", ErrPKILifecycleInvalid)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.InvalidDataCleanupInterval == 0 {
		options.InvalidDataCleanupInterval = defaultPKIInvalidDataCleanupInterval
	}
	if options.InvalidDataCleanupInterval < 0 {
		return nil, fmt.Errorf("%w: invalid-data cleanup interval must be positive", ErrPKILifecycleInvalid)
	}
	return &InternalPKIService{
		store: options.Store, lease: options.Lease, tokens: options.Tokens, enrollment: options.Enrollment,
		revocation: options.Revocation, snapshotSigner: options.SnapshotSigner, tasks: options.Tasks,
		backup:     options.Backup,
		activation: options.Activation,
		authority:  options.Authority,
		clock:      options.Clock, random: options.Random,
		forceRotationConvergence:   defaultPKIForceRotationConvergence,
		invalidDataCleanupInterval: options.InvalidDataCleanupInterval,
	}, nil
}

// PKILocalEnrollmentReply is the public-only credential response consumed by
// the embedded agent. The matching private key remains in the embedded data
// root and never crosses this in-process boundary.
type PKILocalEnrollmentReply struct {
	TunnelCredential storage.PKITunnelCredential
	SecuritySnapshot storage.PKISecuritySnapshot
}

// EnrollLocal binds the embedded agent's durable request ID and CSR to the
// configured local identity, then returns the same canonical signed snapshot
// used by remote registration. A replay after response loss returns the
// original certificate instead of issuing another generation.
func (s *InternalPKIService) EnrollLocal(ctx context.Context, request PKILocalEnrollRequest) (PKILocalEnrollmentReply, error) {
	if s == nil || s.enrollment == nil || s.store == nil || s.snapshotSigner == nil {
		return PKILocalEnrollmentReply{}, ErrPKIRuntimeUnavailable
	}
	result, err := s.enrollment.EnrollLocal(ctx, request)
	if err != nil {
		return PKILocalEnrollmentReply{}, err
	}
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKILocalEnrollmentReply{}, err
	}
	snapshot, err := s.fullSecuritySnapshot(ctx, state)
	if err != nil {
		return PKILocalEnrollmentReply{}, err
	}
	return PKILocalEnrollmentReply{
		TunnelCredential: storage.PKITunnelCredential{
			IdentityID: result.IdentityID, CertificateID: result.CertificateID, Purpose: result.Purpose,
			CertificatePEM: result.CertificatePEM, PublicKeyFingerprint: result.PublicKeyFingerprint,
			AuthorityID: result.AuthorityID, CAGeneration: result.CAGeneration,
			NotBefore: result.NotBefore, NotAfter: result.NotAfter,
		},
		SecuritySnapshot: snapshot,
	}, nil
}

// RegisterAgent is invoked by the existing /agents/register handler. The
// control token remains the HTTP credential; the one-time token authorizes only
// certificate enrollment and is consumed in the same transaction as AgentRow.
func (s *InternalPKIService) RegisterAgent(ctx context.Context, request RegisterRequest, agent storage.AgentRow) (PKIRegistrationReply, error) {
	result, err := s.enrollment.EnrollAndBindAgent(ctx, PKIEnrollRequest{
		RequestID: request.PKIEnrollmentRequestID,
		Token:     request.RegisterToken, AgentID: request.AgentID, Kind: storage.PKIIdentityKindAgent,
		Purpose: storage.PKICertificatePurposeClient, CSRPEM: request.TunnelCSRPEM,
		SecurityAcknowledgement: request.PKISecurityAck,
	}, agent)
	if err != nil {
		return PKIRegistrationReply{}, err
	}
	if s.tasks != nil {
		s.tasks.AllowAgentSessions(result.AgentID)
	}
	// Enrollment is response-replayable, so signing the canonical snapshot after
	// commit cannot strand a consumed token. An identical retry returns the same
	// agent token and certificate before retrying snapshot publication.
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKIRegistrationReply{}, err
	}
	snapshot, err := s.fullSecuritySnapshot(ctx, state)
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

func (s *InternalPKIService) ControlSync(
	ctx context.Context,
	agentID string,
	acknowledgement *storage.PKISecurityAcknowledgement,
	requests []PKIControlEnrollmentRequest,
) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	state, credentials, err := s.controlSyncCanonicalState(ctx, agentID, acknowledgement, requests)
	if err != nil {
		return storage.PKISecuritySnapshot{}, credentials, err
	}
	snapshot, err := s.fullSecuritySnapshot(ctx, state)
	return snapshot, credentials, err
}

func (s *InternalPKIService) controlSyncCanonicalState(
	ctx context.Context,
	agentID string,
	acknowledgement *storage.PKISecurityAcknowledgement,
	requests []PKIControlEnrollmentRequest,
) (storage.PKICanonicalState, []PKIControlCredential, error) {
	if _, err := s.recordSecurityAcknowledgement(ctx, agentID, acknowledgement); err != nil {
		return storage.PKICanonicalState{}, nil, err
	}
	credentials := make([]PKIControlCredential, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		requestID := strings.TrimSpace(request.RequestID)
		if requestID == "" {
			credentials = append(credentials, PKIControlCredential{Error: "request_id_required"})
			continue
		}
		if _, duplicate := seen[requestID]; duplicate {
			credentials = append(credentials, PKIControlCredential{RequestID: requestID, Error: "duplicate_request_id"})
			continue
		}
		seen[requestID] = struct{}{}
		result, err := s.enrollment.EnrollAuthenticated(ctx, agentID, request.controlToken, PKIEnrollRequest{
			RequestID: requestID,
			Kind:      request.Kind, ListenerID: request.ListenerID, Purpose: request.Purpose,
			CSRPEM: request.CSRPEM, DNSNames: request.DNSNames, IPAddresses: request.IPAddresses,
		})
		if err != nil {
			if !isPKIControlEnrollmentItemError(err) {
				return storage.PKICanonicalState{}, credentials, err
			}
			credentials = append(credentials, PKIControlCredential{RequestID: requestID, Error: pkiControlEnrollmentErrorCode(err)})
			continue
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
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, credentials, err
	}
	if err := validateControlCredentialsAgainstState(credentials, state); err != nil {
		return storage.PKICanonicalState{}, credentials, err
	}
	return state, credentials, nil
}

func validateControlCredentialsAgainstState(credentials []PKIControlCredential, state storage.PKICanonicalState) error {
	for _, issued := range credentials {
		credential := issued.Credential
		if issued.Error != "" || credential.CertificateID == "" {
			continue
		}
		authorityFound := false
		for _, authority := range state.Authorities {
			if authority.ID == credential.AuthorityID && authority.Generation == credential.CAGeneration &&
				(authority.Status == "active" || authority.Status == "prepared" || authority.Status == "retiring") {
				authorityFound = true
				break
			}
		}
		certificateFound := false
		for _, certificate := range state.Certificates {
			if certificate.ID == credential.CertificateID && certificate.IdentityID == credential.IdentityID &&
				certificate.AuthorityID == credential.AuthorityID && certificate.CAGeneration == credential.CAGeneration &&
				certificate.Status == storage.PKICertificateStatusActive {
				certificateFound = true
				break
			}
		}
		identityFound := false
		for _, identity := range state.Identities {
			if identity.ID == credential.IdentityID && identity.State == storage.PKIIdentityStateActive &&
				identity.CurrentCertificateID != nil && *identity.CurrentCertificateID == credential.CertificateID {
				identityFound = true
				break
			}
		}
		if !authorityFound || !certificateFound || !identityFound {
			return fmt.Errorf("%w: control credential %q is not represented by the final canonical PKI state", storage.ErrPKIInvariant, credential.CertificateID)
		}
	}
	return nil
}

// ControlSyncAndPrepare projects the security payload and relay listeners from
// one canonical PKI state read. This prevents a concurrent rotation from
// producing a security snapshot from one generation and relay credentials
// from another in the same heartbeat.
func (s *InternalPKIService) ControlSyncAndPrepare(
	ctx context.Context,
	agentID string,
	acknowledgement *storage.PKISecurityAcknowledgement,
	requests []PKIControlEnrollmentRequest,
	listeners []storage.RelayListener,
) (storage.PKISecuritySnapshot, []PKIControlCredential, []storage.RelayListener, error) {
	state, credentials, err := s.controlSyncCanonicalState(ctx, agentID, acknowledgement, requests)
	if err != nil {
		return storage.PKISecuritySnapshot{}, credentials, nil, err
	}
	snapshot, err := s.fullSecuritySnapshot(ctx, state)
	if err != nil {
		return storage.PKISecuritySnapshot{}, credentials, nil, err
	}
	prepared, err := prepareRelayListenersWithPKIState(state, agentID, listeners)
	if err != nil {
		return storage.PKISecuritySnapshot{}, credentials, nil, err
	}
	return snapshot, credentials, prepared, nil
}

func pkiControlEnrollmentErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrPKIEnrollmentOwnerMismatch):
		return "owner_mismatch"
	case errors.Is(err, ErrPKIEnrollmentPublicKeyReuse):
		return "public_key_reuse"
	case errors.Is(err, ErrPKIEnrollmentCSR):
		return "invalid_csr"
	default:
		return "invalid_request"
	}
}

func isPKIControlEnrollmentItemError(err error) bool {
	return errors.Is(err, errPKIEnrollmentClientRequest) || errors.Is(err, ErrPKIEnrollmentCSR) ||
		errors.Is(err, ErrPKIEnrollmentOwnerMismatch) || errors.Is(err, ErrPKIEnrollmentPublicKeyReuse)
}

// PrepareRelayListeners attaches canonical PKI references during migration but
// preserves the currently active relay authentication until activation commits
// the tunnel_mtls_only state. Only then are legacy certificate and pin fields
// removed from control snapshots.
func (s *InternalPKIService) PrepareRelayListeners(ctx context.Context, agentID string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	return prepareRelayListenersWithPKIState(state, agentID, listeners)
}

func prepareRelayListenersWithPKIState(state storage.PKICanonicalState, agentID string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	if state.Settings == nil {
		return nil, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	prepared := make([]storage.RelayListener, len(listeners))
	activated := state.Settings.UpgradeState == storage.PKIUpgradeStateTunnelMTLSOnly
	for index, listener := range listeners {
		if activated {
			listener.CertificateID = nil
			listener.TLSMode = "pki_mtls"
			listener.PinSet = nil
			listener.TrustedCACertificateIDs = nil
			listener.AllowSelfSigned = false
		}
		ownerAgentID := strings.TrimSpace(listener.AgentID)
		if ownerAgentID == "" {
			ownerAgentID = agentID
		}
		listener.AgentID = ownerAgentID
		listener.PKIIdentityState = storage.PKIIdentityStateEnrollmentRequired
		identity, found, err := storage.FindActivePKIIdentity(state, storage.PKIIdentityKindListener, ownerAgentID, strconv.Itoa(listener.ID))
		if err != nil {
			return nil, err
		}
		if found {
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
	state, err := s.recordSecurityAcknowledgement(ctx, agentID, acknowledgement)
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	return s.fullSecuritySnapshot(ctx, state)
}

func (s *InternalPKIService) recordSecurityAcknowledgement(ctx context.Context, agentID string, acknowledgement *storage.PKISecurityAcknowledgement) (storage.PKICanonicalState, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return storage.PKICanonicalState{}, err
	}
	if state.Settings == nil {
		return storage.PKICanonicalState{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	if acknowledgement != nil {
		if err := validatePKISecurityAcknowledgementForState(state, agentID, *acknowledgement); err != nil {
			return storage.PKICanonicalState{}, err
		}
		encoded, err := json.Marshal(acknowledgement)
		if err != nil {
			return storage.PKICanonicalState{}, err
		}
		if err := s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
			return tx.SavePKISecurityAcknowledgement(ctx, agentID, string(encoded), s.clock().UTC())
		}); err != nil {
			return storage.PKICanonicalState{}, err
		}
		state, err = s.store.LoadPKICanonicalState(ctx)
		if err != nil {
			return storage.PKICanonicalState{}, err
		}
	}
	return state, nil
}

func validatePKISecurityAcknowledgement(settings storage.PKISettingsRow, acknowledgement storage.PKISecurityAcknowledgement) error {
	if strings.TrimSpace(acknowledgement.PKIDomainID) != settings.PKIDomainID || acknowledgement.PKIEpoch < 0 || acknowledgement.SecurityRevision < 0 {
		return fmt.Errorf("%w: PKI security acknowledgement domain/version is invalid", ErrInvalidArgument)
	}
	if acknowledgement.CertificateID == "" || strings.TrimSpace(acknowledgement.CertificateID) != acknowledgement.CertificateID || len(acknowledgement.TrustGenerations) == 0 {
		return fmt.Errorf("%w: PKI security acknowledgement credential/trust binding is incomplete", ErrInvalidArgument)
	}
	previousGeneration := int64(0)
	for _, generation := range acknowledgement.TrustGenerations {
		if generation <= previousGeneration {
			return fmt.Errorf("%w: PKI security acknowledgement trust generations are not canonical", ErrInvalidArgument)
		}
		previousGeneration = generation
	}
	previousListenerKey := ""
	for _, listener := range acknowledgement.ListenerCredentials {
		listenerID, err := strconv.Atoi(listener.ListenerID)
		listenerKey := listener.ListenerID + "\x00" + listener.IdentityID
		if err != nil || listenerID <= 0 || strings.TrimSpace(listener.ListenerID) != listener.ListenerID ||
			listener.IdentityID == "" || strings.TrimSpace(listener.IdentityID) != listener.IdentityID ||
			listener.CertificateID == "" || strings.TrimSpace(listener.CertificateID) != listener.CertificateID ||
			listener.CAGeneration <= 0 || listenerKey <= previousListenerKey {
			return fmt.Errorf("%w: PKI listener credential acknowledgements are not canonical", ErrInvalidArgument)
		}
		previousListenerKey = listenerKey
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

func validatePKISecurityAcknowledgementForState(
	state storage.PKICanonicalState,
	agentID string,
	acknowledgement storage.PKISecurityAcknowledgement,
) error {
	if state.Settings == nil {
		return fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	settings := *state.Settings
	if err := validatePKISecurityAcknowledgement(settings, acknowledgement); err != nil {
		return err
	}
	identities := make(map[string]storage.PKIIdentityRow, len(state.Identities))
	for _, identity := range state.Identities {
		identities[identity.ID] = identity
	}
	certificates := make(map[string]storage.PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		certificates[certificate.ID] = certificate
	}
	for _, listener := range acknowledgement.ListenerCredentials {
		identity, found := identities[listener.IdentityID]
		if !found || identity.PKIDomainID != settings.PKIDomainID || identity.Kind != storage.PKIIdentityKindListener ||
			identity.AgentID != agentID || identity.ListenerID != listener.ListenerID {
			return fmt.Errorf("%w: PKI listener credential acknowledgement owner is invalid", ErrInvalidArgument)
		}
		certificate, found := certificates[listener.CertificateID]
		if !found || certificate.IdentityID != identity.ID || certificate.Purpose != storage.PKICertificatePurposeServer ||
			certificate.CAGeneration != listener.CAGeneration {
			return fmt.Errorf("%w: PKI listener credential acknowledgement certificate is invalid", ErrInvalidArgument)
		}
	}
	if acknowledgement.PKIEpoch != settings.PKIEpoch || acknowledgement.SecurityRevision != settings.SecurityRevision {
		return nil
	}
	snapshot, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil {
		return err
	}
	return validatePKISecurityAcknowledgementTrustBinding(settings, pkiSecurityTrustGenerations(snapshot), acknowledgement)
}

func validatePKISecurityAcknowledgementTrustBinding(
	settings storage.PKISettingsRow,
	expectedTrustGenerations []int64,
	acknowledgement storage.PKISecurityAcknowledgement,
) error {
	if err := validatePKISecurityAcknowledgement(settings, acknowledgement); err != nil {
		return err
	}
	if acknowledgement.PKIEpoch == settings.PKIEpoch && acknowledgement.SecurityRevision == settings.SecurityRevision &&
		!slices.Equal(acknowledgement.TrustGenerations, expectedTrustGenerations) {
		return fmt.Errorf("%w: PKI security acknowledgement trust generations do not match the current signed snapshot", ErrInvalidArgument)
	}
	return nil
}

func pkiSecurityTrustGenerations(snapshot storage.PKISecuritySnapshot) []int64 {
	generations := make([]int64, len(snapshot.TrustRoots))
	for index, root := range snapshot.TrustRoots {
		generations[index] = root.Generation
	}
	return generations
}

func pkiSecurityAcknowledgementSatisfiesTunnelMTLSActivation(
	settings storage.PKISettingsRow,
	certificateID string,
	expectedTrustGenerations []int64,
	acknowledgement storage.PKISecurityAcknowledgement,
) bool {
	return acknowledgement.PKIDomainID == settings.PKIDomainID && acknowledgement.PKIEpoch == settings.PKIEpoch &&
		acknowledgement.SecurityRevision == settings.SecurityRevision && acknowledgement.Full &&
		acknowledgement.CertificateID == certificateID && slices.Equal(acknowledgement.TrustGenerations, expectedTrustGenerations)
}

func (s *InternalPKIService) fullSecuritySnapshot(ctx context.Context, state storage.PKICanonicalState) (storage.PKISecuritySnapshot, error) {
	settings := state.Settings
	if settings == nil {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: PKI settings are unavailable", ErrPKILifecycleInvalid)
	}
	if state.SecuritySnapshot != nil {
		persisted, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
		if err != nil {
			return storage.PKISecuritySnapshot{}, err
		}
		return persisted, nil
	}
	if settings.SecurityRevision != 0 {
		return storage.PKISecuritySnapshot{}, fmt.Errorf("%w: non-initial security snapshot requires protected recovery", ErrPKILifecycleInvalid)
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	if grant.PKIDomainID != settings.PKIDomainID || grant.PKIEpoch != settings.PKIEpoch {
		return storage.PKISecuritySnapshot{}, ErrPKILeaseNotHeld
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
	snapshot, err := storagePKISecuritySnapshot(state, signed)
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return storage.PKISecuritySnapshot{}, err
	}
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIEnrollmentLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		current, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || current.PKIDomainID != settings.PKIDomainID || current.PKIEpoch != settings.PKIEpoch ||
			current.SecurityRevision != settings.SecurityRevision {
			return fmt.Errorf("%w: security state changed while signing snapshot", ErrPKIEpochStale)
		}
		if err := tx.SavePKISecuritySnapshot(ctx, storage.PKISecuritySnapshotRow{
			PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch,
			SecurityRevision: settings.SecurityRevision, SnapshotJSON: string(encoded), UpdatedAt: s.clock().UTC(),
		}); err != nil {
			return err
		}
		return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
	})
	if err == nil {
		return snapshot, nil
	}
	// A concurrent publisher may have won the same version. Return its canonical
	// bytes instead of turning a harmless signing race into a registration loss.
	currentState, loadErr := s.store.LoadPKICanonicalState(ctx)
	if loadErr == nil && currentState.Settings != nil {
		if persisted, ok := persistedPKISecuritySnapshot(currentState, *currentState.Settings); ok &&
			persisted.PKIDomainID == settings.PKIDomainID && persisted.PKIEpoch == settings.PKIEpoch &&
			persisted.SecurityRevision == settings.SecurityRevision {
			return persisted, nil
		}
	}
	return storage.PKISecuritySnapshot{}, err
}

func persistedPKISecuritySnapshot(state storage.PKICanonicalState, settings storage.PKISettingsRow) (storage.PKISecuritySnapshot, bool) {
	if state.SecuritySnapshot == nil || state.Settings == nil || state.Settings.PKIDomainID != settings.PKIDomainID ||
		state.Settings.PKIEpoch != settings.PKIEpoch || state.Settings.SecurityRevision != settings.SecurityRevision {
		return storage.PKISecuritySnapshot{}, false
	}
	snapshot, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil {
		return storage.PKISecuritySnapshot{}, false
	}
	return snapshot, true
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
		RuntimeStatus: "ready",
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
		details := make(map[string]any)
		if err := json.Unmarshal([]byte(row.DetailsJSON), &details); err != nil {
			return nil, fmt.Errorf("%w: stored PKI audit details are invalid", ErrPKILifecycleInvalid)
		}
		event := PKIAuditEvent{
			ID: row.ID, Type: row.Type, OccurredAt: row.OccurredAt, Source: row.Source, OperatorID: row.OperatorID,
			ObjectType: row.ObjectType, ObjectID: row.ObjectID, Result: row.Result, Reason: row.Reason,
			SecurityRevision: row.SecurityRevision, Details: details,
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
	if query.SerialHex != "" && !pkiAuditDetailsMatchSerial(row.DetailsJSON, query.SerialHex) {
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

func pkiAuditDetailsMatchSerial(detailsJSON, wanted string) bool {
	wanted = canonicalPKIAuditSerial(wanted)
	if wanted == "" {
		return false
	}
	var details any
	if json.Unmarshal([]byte(detailsJSON), &details) != nil {
		return false
	}
	return pkiAuditValueMatchesSerial(details, "", wanted)
}

func pkiAuditValueMatchesSerial(value any, field, wanted string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if pkiAuditValueMatchesSerial(nested, strings.ToLower(strings.TrimSpace(key)), wanted) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if pkiAuditValueMatchesSerial(nested, field, wanted) {
				return true
			}
		}
	case string:
		switch field {
		case "serial", "serial_hex", "revoked_serial", "revoked_serials", "all_revoked_serials":
			return canonicalPKIAuditSerial(typed) == wanted
		}
	}
	return false
}

func canonicalPKIAuditSerial(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "0x")
	if value == "" {
		return ""
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return ""
		}
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	return value
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
		if job.State != storage.PKILifecycleJobStateFailed && job.State != storage.PKILifecycleJobStateBlocked && job.Phase != "blocked" {
			continue
		}
		level := PKIAlertWarning
		if job.Phase == "blocked" || job.State == storage.PKILifecycleJobStateBlocked {
			level = PKIAlertCritical
		}
		facts = append(facts, PKIAlertFact{
			Kind: PKIAlertKindRotationBlocked, ObjectType: job.TargetType, ObjectID: job.TargetID,
			Level: level, FirstSeen: job.UpdatedAt, LastSeen: now, Reason: job.LastError,
		})
	}
	return DerivePKIAlerts(facts)
}

// ReconcilePendingConvergence retries durable data-plane safety work after a
// restart or a transient task/session failure. Only the live lease holder is
// allowed to drive the retry loop; ordinary control traffic is unaffected.
func (s *InternalPKIService) ReconcilePendingConvergence(ctx context.Context) error {
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return err
	}
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return err
	}
	if state.Settings != nil && state.Settings.SecurityRevision == 0 && state.SecuritySnapshot == nil {
		if _, err := s.fullSecuritySnapshot(ctx, state); err != nil {
			return err
		}
		state, err = s.store.LoadPKICanonicalState(ctx)
		if err != nil {
			return err
		}
	}
	revocationErr := s.revocation.ReconcilePendingConvergence(ctx, state)
	automaticActivationErr := s.reconcileAutomaticActivation(ctx, state, grant)
	activationErr := s.reconcileActivationOperations(ctx, state, grant)
	var authorityErr error
	if s.authority != nil {
		authorityErr = s.authority.ReconcilePending(ctx)
	}
	cleanupErr := s.pruneInvalidPKIData(ctx, grant, state, s.clock().UTC())
	return errors.Join(revocationErr, automaticActivationErr, activationErr, authorityErr, cleanupErr)
}

func (s *InternalPKIService) pruneInvalidPKIData(
	ctx context.Context,
	grant PKILeaseGrant,
	state storage.PKICanonicalState,
	now time.Time,
) error {
	if state.Settings == nil || state.Settings.AuditRetentionDays <= 0 {
		return nil
	}
	interval := s.invalidDataCleanupInterval
	if interval <= 0 {
		interval = defaultPKIInvalidDataCleanupInterval
	}
	s.invalidDataCleanupMu.Lock()
	if !s.lastInvalidDataCleanup.IsZero() && now.Before(s.lastInvalidDataCleanup.Add(interval)) {
		s.invalidDataCleanupMu.Unlock()
		return nil
	}
	s.lastInvalidDataCleanup = now
	s.invalidDataCleanupMu.Unlock()

	const maxAuditRetentionDays = int64((1<<63 - 1) / int64(24*time.Hour))
	if int64(state.Settings.AuditRetentionDays) > maxAuditRetentionDays {
		s.invalidDataCleanupMu.Lock()
		if s.lastInvalidDataCleanup.Equal(now) {
			s.lastInvalidDataCleanup = time.Time{}
		}
		s.invalidDataCleanupMu.Unlock()
		return fmt.Errorf("%w: audit retention is too large", ErrPKILifecycleInvalid)
	}
	retention := storage.PKIInvalidDataRetention{
		ConsumedNonce: defaultPKIConsumedNonceRetention,
		TerminalJob:   defaultPKITerminalJobRetention,
		AuditEvent:    time.Duration(state.Settings.AuditRetentionDays) * 24 * time.Hour,
	}
	err := s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIAuthorityLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		if _, err := tx.PrunePKIInvalidData(ctx, now, retention); err != nil {
			return err
		}
		return requirePKIAuthorityLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		s.invalidDataCleanupMu.Lock()
		if s.lastInvalidDataCleanup.Equal(now) {
			s.lastInvalidDataCleanup = time.Time{}
		}
		s.invalidDataCleanupMu.Unlock()
	}
	return err
}

func (s *InternalPKIService) reconcileActivationOperations(ctx context.Context, state storage.PKICanonicalState, grant PKILeaseGrant) error {
	operations, ok := s.store.(interface {
		GetOperation(context.Context, string) (storage.OperationRow, bool, error)
	})
	if !ok {
		return nil
	}
	var result error
	for _, job := range state.LifecycleJobs {
		if job.Kind != "activate" || job.State != storage.PKILifecycleJobStateRunning {
			continue
		}
		operation, found, err := operations.GetOperation(ctx, job.OperationID)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !found || (operation.Status != storage.OperationStatusApplied && operation.Status != storage.OperationStatusFailed && operation.Status != storage.OperationStatusSuperseded) {
			continue
		}
		err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
			if err := requirePKIEnrollmentLeaseFence(ctx, tx, grant); err != nil {
				return err
			}
			previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, job.ID)
			if err != nil || !found {
				return err
			}
			if previous.State != storage.PKILifecycleJobStateRunning {
				return nil
			}
			next := previous
			next.Attempt++
			next.UpdatedAt = s.clock().UTC()
			if operation.Status == storage.OperationStatusApplied {
				next.Phase = "completed"
				next.State = storage.PKILifecycleJobStateSucceeded
				next.LastError = ""
			} else {
				next.Phase = "blocked"
				next.State = storage.PKILifecycleJobStateFailed
				next.LastError = "activation revision did not converge"
			}
			if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
				return err
			}
			return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
		})
		result = errors.Join(result, err)
	}
	return result
}

func (s *InternalPKIService) CreateEnrollmentToken(ctx context.Context, request PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error) {
	if _, err := s.lease.RequirePKILease(ctx); err != nil {
		return PKIEnrollmentToken{}, err
	}
	return s.tokens.Create(ctx, request)
}

func (s *InternalPKIService) IssueConfirmationNonce(ctx context.Context, request PKIConfirmationRequest) (PKIConfirmation, error) {
	action, targetID, err := canonicalPKIConfirmationBinding(request.Action, request.TargetID)
	if err != nil {
		return PKIConfirmation{}, err
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIConfirmation{}, err
	}
	nonceBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.random, nonceBytes); err != nil {
		return PKIConfirmation{}, fmt.Errorf("generate PKI confirmation nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	digest := sha256.Sum256(nonceBytes)
	now := s.clock().UTC()
	expiresAt := now.Add(pkiConfirmationNonceTTL)
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIEnrollmentLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found || settings.PKIDomainID != grant.PKIDomainID || settings.PKIEpoch != grant.PKIEpoch {
			return ErrPKILeaseNotHeld
		}
		if err := tx.CreatePKIConfirmationNonce(ctx, storage.PKIConfirmationNonceRow{
			ID: "pki-confirmation-" + hex.EncodeToString(digest[:]), PKIDomainID: settings.PKIDomainID,
			DigestSHA256: hex.EncodeToString(digest[:]), OperatorID: "panel", Action: action,
			TargetID: targetID, ExpiresAt: expiresAt, CreatedAt: now,
		}); err != nil {
			return err
		}
		return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		return PKIConfirmation{}, err
	}
	return PKIConfirmation{Nonce: nonce, Action: action, TargetID: targetID, ExpiresAt: expiresAt}, nil
}

type pkiConfirmationConsumption struct {
	digest   string
	action   string
	targetID string
}

func preparePKIConfirmationConsumption(action, targetID, nonce string) (pkiConfirmationConsumption, error) {
	action, targetID, err := canonicalPKIConfirmationBinding(action, targetID)
	if err != nil {
		return pkiConfirmationConsumption{}, err
	}
	nonceBytes, err := hex.DecodeString(strings.TrimSpace(nonce))
	if err != nil || len(nonceBytes) != 32 {
		return pkiConfirmationConsumption{}, fmt.Errorf("%w: confirmation nonce is invalid", ErrInvalidArgument)
	}
	digest := sha256.Sum256(nonceBytes)
	return pkiConfirmationConsumption{digest: hex.EncodeToString(digest[:]), action: action, targetID: targetID}, nil
}

func canonicalPKIConfirmationBinding(action, targetID string) (string, string, error) {
	action = strings.TrimSpace(action)
	targetID = strings.TrimSpace(targetID)
	switch action {
	case "revoke":
		if targetID == "" {
			return "", "", fmt.Errorf("%w: revoke confirmation target is required", ErrInvalidArgument)
		}
	case "force_rotate":
		if targetID == "" {
			return "", "", fmt.Errorf("%w: force rotation confirmation target is required", ErrInvalidArgument)
		}
	case "ca_rotate", "emergency_ca_rotate", "activate":
		if targetID != "" && targetID != "domain" {
			return "", "", fmt.Errorf("%w: confirmation target is invalid", ErrInvalidArgument)
		}
		targetID = "domain"
	default:
		return "", "", fmt.Errorf("%w: confirmation action is unsupported", ErrInvalidArgument)
	}
	return action, targetID, nil
}

func consumePKIConfirmation(
	ctx context.Context,
	tx *storage.PKITransaction,
	domainID string,
	confirmation pkiConfirmationConsumption,
	now time.Time,
) error {
	consumed, err := tx.ConsumePKIConfirmationNonce(ctx, domainID, confirmation.digest, "panel", confirmation.action, confirmation.targetID, now)
	if err != nil {
		return err
	}
	if !consumed {
		return fmt.Errorf("%w: confirmation nonce is expired, reused, or bound to another action", ErrInvalidArgument)
	}
	return nil
}

func (s *InternalPKIService) Revoke(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, true); err != nil {
		return PKIOperation{}, err
	}
	confirmation, err := preparePKIConfirmationConsumption("revoke", request.TargetID, request.ConfirmationNonce)
	if err != nil {
		return PKIOperation{}, err
	}
	commit, err := s.revocation.Revoke(ctx, PKIRevocationRequest{
		IdentityID: request.TargetID, Reason: request.Reason, Source: "panel", OperatorID: "panel",
		ConfirmationDigest: confirmation.digest, ConfirmationAction: confirmation.action,
		ConfirmationTargetID: confirmation.targetID,
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return s.Operation(ctx, fmt.Sprintf("revoke-%s-r%d", commit.Facts.IdentityID, commit.Facts.SecurityRevision))
}

// RevokeListenerForDeletion is the relay configuration deletion safety hook.
// It revokes the canonical listener identity and publishes the new full
// security snapshot before the listener row can disappear. Agent control
// credentials and token-authenticated control routes are deliberately kept.
func (s *InternalPKIService) RevokeListenerForDeletion(
	ctx context.Context,
	transactionStore *storage.GormStore,
	agentID string,
	listenerID int,
) (func(), error) {
	agentID = strings.TrimSpace(agentID)
	if transactionStore == nil || agentID == "" || listenerID <= 0 {
		return nil, fmt.Errorf("%w: listener owner is invalid", ErrInvalidArgument)
	}
	state, err := transactionStore.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	canonicalListenerID := strconv.Itoa(listenerID)
	identity, found, err := storage.FindActivePKIIdentity(state, storage.PKIIdentityKindListener, agentID, canonicalListenerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{Store: transactionStore, Clock: s.clock})
	if err != nil {
		return nil, err
	}
	commit, err := s.revocation.CommitWithRepository(ctx, PKIRevocationRequest{
		IdentityID: identity.ID,
		Reason:     "relay listener deleted",
		Source:     "control_plane",
		OperatorID: "control_plane",
	}, repository)
	if err != nil {
		return nil, err
	}
	return func() { _ = s.revocation.CompleteCommittedRevocation(commit) }, nil
}

// RevokeAgentForDeletion revokes every active identity owned by an agent in
// the caller's deletion transaction. The resulting tombstones, audit events,
// and signed security snapshot survive the AgentRow hard delete.
func (s *InternalPKIService) RevokeAgentForDeletion(
	ctx context.Context,
	transactionStore *storage.GormStore,
	agentID string,
) (func(), error) {
	agentID = strings.TrimSpace(agentID)
	if transactionStore == nil || agentID == "" {
		return nil, fmt.Errorf("%w: agent owner is invalid", ErrInvalidArgument)
	}
	state, err := transactionStore.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	identities := make([]storage.PKIIdentityRow, 0)
	for _, identity := range state.Identities {
		if identity.AgentID == agentID && identity.State != storage.PKIIdentityStateRevoked {
			identities = append(identities, identity)
		}
	}
	if len(identities) == 0 {
		return nil, nil
	}
	slices.SortFunc(identities, func(left, right storage.PKIIdentityRow) int {
		return strings.Compare(left.ID, right.ID)
	})
	repository, err := NewGormPKIRevocationRepository(GormPKIRevocationRepositoryOptions{Store: transactionStore, Clock: s.clock})
	if err != nil {
		return nil, err
	}
	commits := make([]PKIRevocationCommit, 0, len(identities))
	for _, identity := range identities {
		commit, revokeErr := s.revocation.CommitWithRepository(ctx, PKIRevocationRequest{
			IdentityID: identity.ID,
			Reason:     "agent deleted",
			Source:     "control_plane",
			OperatorID: "control_plane",
		}, repository)
		if revokeErr != nil {
			return nil, revokeErr
		}
		commits = append(commits, commit)
	}
	return func() {
		for _, commit := range commits {
			_ = s.revocation.CompleteCommittedRevocation(commit)
		}
	}, nil
}

func (s *InternalPKIService) ForceRotate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, true); err != nil {
		return PKIOperation{}, err
	}
	confirmation, err := preparePKIConfirmationConsumption("force_rotate", request.TargetID, request.ConfirmationNonce)
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err := s.queueOperation(ctx, "force_rotate", "identity", request.TargetID, &confirmation, "")
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err = s.transitionOperation(ctx, operation.ID, "dispatching", storage.PKILifecycleJobStateRunning, "")
	if err != nil {
		return PKIOperation{}, err
	}
	state, executionErr := s.store.LoadPKICanonicalState(ctx)
	var identity *storage.PKIIdentityRow
	var previousCertificateID string
	if executionErr == nil {
		for index := range state.Identities {
			if state.Identities[index].ID == request.TargetID && state.Identities[index].State == storage.PKIIdentityStateActive {
				identity = &state.Identities[index]
				break
			}
		}
		if identity == nil {
			executionErr = fmt.Errorf("%w: active PKI identity not found", ErrInvalidArgument)
		} else if identity.CurrentCertificateID == nil || strings.TrimSpace(*identity.CurrentCertificateID) == "" {
			executionErr = fmt.Errorf("%w: active PKI identity has no current certificate", ErrPKILifecycleInvalid)
		} else {
			previousCertificateID = *identity.CurrentCertificateID
		}
	}
	if executionErr == nil {
		taskCtx, cancelTask := context.WithTimeout(context.Background(), PKIOnlineRevocationConvergence)
		record, dispatchErr := s.tasks.CreateAndDispatchContext(taskCtx, TaskCreateRequest{
			AgentID: identity.AgentID, Type: TaskTypePKIForceRotation,
			Payload: map[string]any{
				"operation_id": operation.ID, "identity_id": identity.ID,
				"identity_kind": identity.Kind, "listener_id": identity.ListenerID,
			},
			TTL: PKIOnlineRevocationConvergence,
		})
		if dispatchErr == nil {
			dispatchErr = s.tasks.waitForTaskTerminal(taskCtx, record.ID)
		}
		if dispatchErr == nil {
			record, dispatchErr = s.tasks.Get(taskCtx, identity.AgentID, record.ID)
		}
		cancelTask()

		acknowledgementStore, hasAcknowledgementStore := s.store.(pkiForceRotationAcknowledgementStore)
		if dispatchErr == nil && !hasAcknowledgementStore {
			dispatchErr = fmt.Errorf("%w: force rotation acknowledgement store is unavailable", ErrPKIRuntimeUnavailable)
		}
		requestID := ""
		if dispatchErr == nil {
			localAgent := strings.TrimSpace(acknowledgementStore.LocalAgentID()) == identity.AgentID
			requestID, dispatchErr = forceRotationTaskRequestID(record, identity.ID, localAgent)
		}
		if dispatchErr == nil {
			operation, dispatchErr = s.transitionOperation(context.Background(), operation.ID, "awaiting_activation", storage.PKILifecycleJobStateRunning, "")
		}
		if dispatchErr == nil {
			activationCtx, cancelActivation := context.WithTimeout(context.Background(), s.forceRotationConvergenceWindow())
			dispatchErr = s.waitForForceRotationActivation(
				activationCtx, acknowledgementStore, *identity, previousCertificateID, requestID,
			)
			cancelActivation()
		}
		executionErr = dispatchErr
	}
	if executionErr != nil {
		failed, recordErr := s.transitionOperation(context.Background(), operation.ID, "failed", storage.PKILifecycleJobStateFailed, executionErr.Error())
		return failed, recordErr
	}
	return s.transitionOperation(context.Background(), operation.ID, "completed", storage.PKILifecycleJobStateSucceeded, "")
}

func (s *InternalPKIService) forceRotationConvergenceWindow() time.Duration {
	if s.forceRotationConvergence > 0 {
		return s.forceRotationConvergence
	}
	return defaultPKIForceRotationConvergence
}

func forceRotationTaskRequestID(record TaskRecord, identityID string, localAgent bool) (string, error) {
	resultIdentityID, ok := record.Result["identity_id"].(string)
	if !ok || strings.TrimSpace(resultIdentityID) != identityID {
		return "", fmt.Errorf("%w: force rotation task returned an invalid identity", ErrPKILifecycleInvalid)
	}
	requestValue, hasRequestID := record.Result["request_id"]
	if !hasRequestID && localAgent {
		// The embedded task performs signing, durable activation, and ACK in the
		// task itself. Remote tasks only prepare a CSR and must return request_id.
		return "", nil
	}
	requestID, ok := requestValue.(string)
	requestID = strings.TrimSpace(requestID)
	if !ok || requestID == "" {
		return "", fmt.Errorf("%w: force rotation task returned no enrollment request ID", ErrPKILifecycleInvalid)
	}
	return requestID, nil
}

func (s *InternalPKIService) waitForForceRotationActivation(
	ctx context.Context,
	store pkiForceRotationAcknowledgementStore,
	requestedIdentity storage.PKIIdentityRow,
	previousCertificateID string,
	requestID string,
) error {
	ticker := time.NewTicker(pkiForceRotationPollInterval)
	defer ticker.Stop()
	for {
		converged, err := s.forceRotationActivationConverged(
			ctx, store, requestedIdentity, previousCertificateID, requestID,
		)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("forced PKI credential activation did not converge: %w", ctx.Err())
			}
			return err
		}
		if converged {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("forced PKI credential activation did not converge: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *InternalPKIService) forceRotationActivationConverged(
	ctx context.Context,
	store pkiForceRotationAcknowledgementStore,
	requestedIdentity storage.PKIIdentityRow,
	previousCertificateID string,
	requestID string,
) (bool, error) {
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return false, err
	}
	var identity *storage.PKIIdentityRow
	for index := range state.Identities {
		if state.Identities[index].ID == requestedIdentity.ID {
			identity = &state.Identities[index]
			break
		}
	}
	if identity == nil || identity.State != storage.PKIIdentityStateActive ||
		identity.AgentID != requestedIdentity.AgentID || identity.Kind != requestedIdentity.Kind ||
		identity.ListenerID != requestedIdentity.ListenerID {
		return false, fmt.Errorf("%w: force rotation identity is no longer active", ErrPKILifecycleConflict)
	}
	if identity.CurrentCertificateID == nil || *identity.CurrentCertificateID == previousCertificateID {
		return false, nil
	}
	certificateID := *identity.CurrentCertificateID
	var certificate *storage.PKICertificateRow
	for index := range state.Certificates {
		if state.Certificates[index].ID == certificateID {
			certificate = &state.Certificates[index]
			break
		}
	}
	if certificate == nil || certificate.IdentityID != identity.ID || certificate.Status != storage.PKICertificateStatusActive {
		return false, fmt.Errorf("%w: force rotation current certificate is invalid", ErrPKILifecycleInvalid)
	}

	if requestID != "" {
		requestKeyPrefix := "control:"
		if strings.TrimSpace(store.LocalAgentID()) == identity.AgentID {
			requestKeyPrefix = "local:"
		}
		requestKey := requestKeyPrefix + identity.AgentID + ":" + requestID
		var replay *storage.PKIEnrollmentReplayRow
		for index := range state.EnrollmentReplays {
			if state.EnrollmentReplays[index].RequestKey == requestKey {
				replay = &state.EnrollmentReplays[index]
				break
			}
		}
		if replay == nil {
			return false, nil
		}
		var result PKIEnrollmentResult
		if err := json.Unmarshal([]byte(replay.ResultJSON), &result); err != nil {
			return false, fmt.Errorf("%w: force rotation enrollment replay is invalid: %v", ErrPKILifecycleInvalid, err)
		}
		if result.AgentID != identity.AgentID || result.IdentityID != identity.ID ||
			result.CertificateID != certificateID || result.CertificateID == previousCertificateID ||
			result.CAGeneration != certificate.CAGeneration {
			return false, fmt.Errorf("%w: force rotation enrollment result does not match the active credential", ErrPKILifecycleConflict)
		}
	}

	acknowledgement, found, err := loadForceRotationAcknowledgement(ctx, store, identity.AgentID)
	if err != nil {
		return false, err
	}
	if !found || !validPKIRotationCredentialAcknowledgement(state, acknowledgement) ||
		!rotationCredentialAcknowledged(*identity, certificateID, certificate.CAGeneration, acknowledgement) {
		return false, nil
	}
	return true, nil
}

func loadForceRotationAcknowledgement(
	ctx context.Context,
	store pkiForceRotationAcknowledgementStore,
	agentID string,
) (storage.PKISecurityAcknowledgement, bool, error) {
	acknowledgementJSON := ""
	if strings.TrimSpace(store.LocalAgentID()) == agentID {
		state, err := store.LoadLocalAgentState(ctx)
		if err != nil {
			return storage.PKISecurityAcknowledgement{}, false, err
		}
		acknowledgementJSON = state.PKISecurityAckJSON
	} else {
		agents, err := store.ListAgents(ctx)
		if err != nil {
			return storage.PKISecurityAcknowledgement{}, false, err
		}
		for _, agent := range agents {
			if agent.ID == agentID {
				acknowledgementJSON = agent.PKISecurityAckJSON
				break
			}
		}
	}
	if strings.TrimSpace(acknowledgementJSON) == "" {
		return storage.PKISecurityAcknowledgement{}, false, nil
	}
	var acknowledgement storage.PKISecurityAcknowledgement
	if err := json.Unmarshal([]byte(acknowledgementJSON), &acknowledgement); err != nil {
		return storage.PKISecurityAcknowledgement{}, false, nil
	}
	return acknowledgement, true, nil
}

func (s *InternalPKIService) RotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, false); err != nil {
		return PKIOperation{}, err
	}
	confirmation, err := preparePKIConfirmationConsumption("ca_rotate", "domain", request.ConfirmationNonce)
	if err != nil {
		return PKIOperation{}, err
	}
	if s.authority == nil {
		return PKIOperation{}, fmt.Errorf("%w: normal CA rotation runtime is unavailable", ErrPKIRuntimeUnavailable)
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	runtimeJSON, err := marshalPKIAuthorityQueuedPayload(request.Reason, "panel")
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err := s.queueOperation(ctx, "ca_rotate", "pki_domain", grant.PKIDomainID, &confirmation, runtimeJSON)
	if err != nil {
		return PKIOperation{}, err
	}
	if err := s.authority.StartNormal(ctx, operation.ID, request.Reason); err != nil {
		return PKIOperation{}, err
	}
	return s.Operation(ctx, operation.ID)
}

func (s *InternalPKIService) EmergencyRotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, false); err != nil {
		return PKIOperation{}, err
	}
	confirmation, err := preparePKIConfirmationConsumption("emergency_ca_rotate", "domain", request.ConfirmationNonce)
	if err != nil {
		return PKIOperation{}, err
	}
	if s.authority == nil {
		return PKIOperation{}, fmt.Errorf("%w: emergency CA rotation runtime is unavailable", ErrPKIRuntimeUnavailable)
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	runtimeJSON, err := marshalPKIAuthorityQueuedPayload(request.Reason, "panel")
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err := s.queueOperation(ctx, "emergency_ca_rotate", "pki_domain", grant.PKIDomainID, &confirmation, runtimeJSON)
	if err != nil {
		return PKIOperation{}, err
	}
	if err := s.authority.StartEmergency(ctx, operation.ID, request.Reason, "panel"); err != nil {
		return PKIOperation{}, err
	}
	return s.Operation(ctx, operation.ID)
}

func (s *InternalPKIService) ExportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if strings.TrimSpace(request.Passphrase) == "" {
		return PKIOperation{}, fmt.Errorf("%w: backup passphrase is required", ErrInvalidArgument)
	}
	operation, err := s.queueOperation(ctx, "protected_export", "backup", "pki", nil, "")
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err = s.transitionOperation(ctx, operation.ID, "exporting", storage.PKILifecycleJobStateRunning, "")
	if err != nil {
		return PKIOperation{}, err
	}
	passphrase := []byte(request.Passphrase)
	defer clear(passphrase)
	exported, executionErr := s.backup.ExportProtected(ctx, passphrase)
	if executionErr != nil {
		failed, recordErr := s.transitionOperation(context.Background(), operation.ID, "failed", storage.PKILifecycleJobStateFailed, executionErr.Error())
		return failed, recordErr
	}
	completed, err := s.transitionOperation(context.Background(), operation.ID, "completed", storage.PKILifecycleJobStateSucceeded, "")
	if err != nil {
		clear(exported.Envelope)
		return PKIOperation{}, err
	}
	completed.Result = map[string]any{"archive": exported.Envelope, "manifest": exported.Manifest}
	return completed, nil
}

func (s *InternalPKIService) ImportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if strings.TrimSpace(request.Passphrase) == "" {
		return PKIOperation{}, fmt.Errorf("%w: backup passphrase is required", ErrInvalidArgument)
	}
	if len(request.Archive) == 0 {
		return PKIOperation{}, fmt.Errorf("%w: protected backup archive is required", ErrInvalidArgument)
	}
	operation, err := s.queueOperation(ctx, "protected_import", "backup", "pki", nil, "")
	if err != nil {
		return PKIOperation{}, err
	}
	operation, err = s.transitionOperation(ctx, operation.ID, "importing", storage.PKILifecycleJobStateRunning, "")
	if err != nil {
		return PKIOperation{}, err
	}
	passphrase := []byte(request.Passphrase)
	defer clear(passphrase)
	result, executionErr := s.backup.RestoreProtected(ctx, request.Archive, passphrase, PKIBackupRestoreOptions{
		Force: request.Force, OperationID: operation.ID,
	})
	if executionErr != nil {
		failed, recordErr := s.transitionOperation(context.Background(), operation.ID, "failed", storage.PKILifecycleJobStateFailed, executionErr.Error())
		return failed, recordErr
	}
	completed, err := s.Operation(ctx, operation.ID)
	if err != nil || completed.State != storage.PKILifecycleJobStateSucceeded {
		if err == nil {
			err = fmt.Errorf("%w: restored database did not record the completed import", ErrPKIBackupActivation)
		}
		return PKIOperation{}, err
	}
	completed.Result = map[string]any{
		"pki_domain_id": result.PKIDomainID, "pki_epoch": result.Version.PKIEpoch,
		"security_revision": result.Version.SecurityRevision, "forced": result.Forced,
		"cleanup_pending": result.CleanupPending,
	}
	return completed, nil
}

func (s *InternalPKIService) Activate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if err := requirePKIAction(request, true, false); err != nil {
		return PKIOperation{}, err
	}
	confirmation, err := preparePKIConfirmationConsumption("activate", "domain", request.ConfirmationNonce)
	if err != nil {
		return PKIOperation{}, err
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	return s.activateTunnelMTLS(ctx, grant, pkiActivationTrigger{
		Name: "manual", Source: "panel", OperatorID: "panel", Reason: strings.TrimSpace(request.Reason),
		Confirmation: &confirmation,
	})
}

type pkiActivationTrigger struct {
	Name         string
	Source       string
	OperatorID   string
	Reason       string
	Confirmation *pkiConfirmationConsumption
}

func (s *InternalPKIService) reconcileAutomaticActivation(
	ctx context.Context,
	state storage.PKICanonicalState,
	grant PKILeaseGrant,
) error {
	if state.Settings == nil || state.Settings.UpgradeState != PKIUpgradeStateMigrationRequired {
		return nil
	}
	_, err := s.activateTunnelMTLS(ctx, grant, pkiActivationTrigger{
		Name:       "automatic",
		Source:     "control_plane",
		OperatorID: "system",
		Reason:     "automatic activation after PKI readiness gate",
	})
	if errors.Is(err, ErrPKILifecycleConflict) {
		return nil
	}
	return err
}

func (s *InternalPKIService) activateTunnelMTLS(
	ctx context.Context,
	grant PKILeaseGrant,
	trigger pkiActivationTrigger,
) (PKIOperation, error) {
	trigger.Name = strings.TrimSpace(trigger.Name)
	trigger.Source = strings.TrimSpace(trigger.Source)
	trigger.OperatorID = strings.TrimSpace(trigger.OperatorID)
	trigger.Reason = strings.TrimSpace(trigger.Reason)
	if trigger.Name == "" || trigger.Source == "" || trigger.OperatorID == "" || trigger.Reason == "" {
		return PKIOperation{}, fmt.Errorf("%w: activation trigger is incomplete", ErrPKILifecycleInvalid)
	}
	idempotencyKey := fmt.Sprintf("activate:%s:%d", grant.PKIDomainID, grant.PKIEpoch)
	state, err := s.store.LoadPKICanonicalState(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	for _, existing := range state.LifecycleJobs {
		if existing.IdempotencyKey == idempotencyKey {
			return pkiOperationFromRow(existing), nil
		}
	}
	operationID := fmt.Sprintf("activate-%d", grant.PKIEpoch)
	var row storage.PKILifecycleJobRow
	err = s.activation.FinalizeTunnelMTLSUpgrade(ctx, operationID, func(activationCtx context.Context, activationStore *storage.GormStore) error {
		return activationStore.WithPKITransaction(activationCtx, func(tx *storage.PKITransaction) error {
			if err := requirePKIEnrollmentLeaseFence(activationCtx, tx, grant); err != nil {
				return err
			}
			if existing, found, err := tx.FindPKILifecycleJobByIdempotencyForUpdate(activationCtx, idempotencyKey); err != nil {
				return err
			} else if found {
				row = existing
				return nil
			}
			settings, found, err := tx.GetPKISettingsForUpdate(activationCtx)
			if err != nil {
				return err
			}
			if !found || settings.PKIDomainID != grant.PKIDomainID || settings.PKIEpoch != grant.PKIEpoch {
				return ErrPKILeaseNotHeld
			}
			now := s.clock().UTC()
			affectedAgents, err := validateTunnelMTLSActivationGate(activationCtx, activationStore, tx, settings, now)
			if err != nil {
				return err
			}
			if settings.UpgradeState != PKIUpgradeStateMigrationRequired {
				return fmt.Errorf("%w: tunnel mTLS activation requires migration state", ErrPKILifecycleConflict)
			}
			if trigger.Confirmation != nil {
				if err := consumePKIConfirmation(activationCtx, tx, settings.PKIDomainID, *trigger.Confirmation, now); err != nil {
					return err
				}
			}
			if err := tx.SetPKIUpgradeState(activationCtx, PKIUpgradeStateMigrationRequired, PKIUpgradeStateTunnelMTLSOnly, now); err != nil {
				return err
			}
			deadline := grant.LeaseDeadline
			phase := "awaiting_revision_apply"
			jobState := storage.PKILifecycleJobStateRunning
			if len(affectedAgents) == 0 {
				phase = "completed"
				jobState = storage.PKILifecycleJobStateSucceeded
			}
			row = storage.PKILifecycleJobRow{
				ID: operationID, PKIDomainID: grant.PKIDomainID, TargetType: "pki_domain", TargetID: grant.PKIDomainID,
				Kind: "activate", Phase: phase, State: jobState,
				OperationID: operationID, IdempotencyKey: idempotencyKey, LeaseOwner: grant.InstanceID,
				LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.CreatePKILifecycleJob(activationCtx, row); err != nil {
				return err
			}
			eventID, err := randomPKIIdentifier(s.random)
			if err != nil {
				return err
			}
			detailsJSON, err := json.Marshal(map[string]any{
				"trigger":                     trigger.Name,
				"affected_agent_count":        len(affectedAgents),
				"pki_epoch":                   settings.PKIEpoch,
				"security_revision":           settings.SecurityRevision,
				"legacy_relay_authentication": "disabled",
				"control_protocol":            "token",
			})
			if err != nil {
				return err
			}
			if err := tx.AppendPKIEvent(activationCtx, storage.PKIEventRow{
				ID: eventID, PKIDomainID: grant.PKIDomainID, Type: "pki.tunnel_mtls.activation_started", OccurredAt: now,
				Source: trigger.Source, OperatorID: trigger.OperatorID, ObjectType: "pki_domain", ObjectID: grant.PKIDomainID,
				Result: "success", Reason: trigger.Reason, SecurityRevision: settings.SecurityRevision,
				DetailsJSON: string(detailsJSON),
			}); err != nil {
				return err
			}
			return requirePKIEnrollmentLeaseFence(activationCtx, tx, grant)
		})
	}, func(finalCtx context.Context, finalStore *storage.GormStore) error {
		return finalStore.WithPKITransaction(finalCtx, func(tx *storage.PKITransaction) error {
			return requirePKIEnrollmentLeaseFence(finalCtx, tx, grant)
		})
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return pkiOperationFromRow(row), nil
}

func validateTunnelMTLSActivationGate(
	ctx context.Context,
	store *storage.GormStore,
	tx *storage.PKITransaction,
	settings storage.PKISettingsRow,
	now time.Time,
) ([]string, error) {
	state, err := tx.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	if state.SecuritySnapshot == nil || state.SecuritySnapshot.PKIEpoch != settings.PKIEpoch ||
		state.SecuritySnapshot.SecurityRevision != settings.SecurityRevision {
		return nil, fmt.Errorf("%w: activation requires the current signed security snapshot", ErrPKILifecycleConflict)
	}
	currentSnapshot, err := storage.ValidateCanonicalPKISecuritySnapshot(state)
	if err != nil {
		return nil, err
	}
	requiredTrustGenerations := pkiSecurityTrustGenerations(currentSnapshot)
	certificates := make(map[string]storage.PKICertificateRow, len(state.Certificates))
	for _, certificate := range state.Certificates {
		certificates[certificate.ID] = certificate
	}
	requireIdentity := func(kind, agentID, listenerID string) (storage.PKIIdentityRow, error) {
		identity, found, err := storage.FindActivePKIIdentity(state, kind, agentID, listenerID)
		if err != nil {
			return storage.PKIIdentityRow{}, err
		}
		if !found || identity.State != storage.PKIIdentityStateActive || identity.CurrentCertificateID == nil {
			return storage.PKIIdentityRow{}, fmt.Errorf("%w: %s identity for agent %s is not active", ErrPKILifecycleConflict, kind, agentID)
		}
		certificate, found := certificates[*identity.CurrentCertificateID]
		if !found || certificate.IdentityID != identity.ID || certificate.Status != storage.PKICertificateStatusActive ||
			!certificate.NotAfter.After(now) {
			return storage.PKIIdentityRow{}, fmt.Errorf("%w: %s identity for agent %s has no current certificate", ErrPKILifecycleConflict, kind, agentID)
		}
		return identity, nil
	}
	listeners, err := store.ListRelayListeners(ctx, "")
	if err != nil {
		return nil, err
	}
	owners := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		if !relayListenerRowSupported(listener) {
			continue
		}
		if _, _, err := canonicalPKIListenerSANs(listener); err != nil {
			return nil, fmt.Errorf("%w: relay listener %d has no concrete certificate endpoint", ErrPKILifecycleConflict, listener.ID)
		}
		owners = append(owners, listener.AgentID)
		if _, err := requireIdentity(storage.PKIIdentityKindListener, listener.AgentID, strconv.Itoa(listener.ID)); err != nil {
			return nil, err
		}
	}
	dependencyConfig := config.Config{EnableLocalAgent: true, LocalAgentID: store.LocalAgentID()}
	affectedAgents, err := expandConfigDependencyAgentIDs(ctx, dependencyConfig, store, uniqueAgentIDs(owners))
	if err != nil {
		return nil, err
	}
	agentRows, err := store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	agents := make(map[string]storage.AgentRow, len(agentRows))
	for _, agent := range agentRows {
		agents[agent.ID] = agent
	}
	for _, agentID := range affectedAgents {
		identity, err := requireIdentity(storage.PKIIdentityKindAgent, agentID, "")
		if err != nil {
			return nil, err
		}
		agent, found := agents[agentID]
		if !found {
			return nil, fmt.Errorf("%w: activation agent %s is missing", ErrPKILifecycleConflict, agentID)
		}
		if agent.IsLocal {
			continue
		}
		var acknowledgement storage.PKISecurityAcknowledgement
		if err := json.Unmarshal([]byte(agent.PKISecurityAckJSON), &acknowledgement); err != nil ||
			!pkiSecurityAcknowledgementSatisfiesTunnelMTLSActivation(settings, *identity.CurrentCertificateID, requiredTrustGenerations, acknowledgement) {
			return nil, fmt.Errorf("%w: agent %s has not acknowledged current PKI security", ErrPKILifecycleConflict, agentID)
		}
	}
	return affectedAgents, nil
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

func (s *InternalPKIService) queueOperation(
	ctx context.Context,
	kind, targetType, targetID string,
	confirmation *pkiConfirmationConsumption,
	runtimeJSON string,
) (PKIOperation, error) {
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
		now := s.clock().UTC()
		if confirmation != nil {
			if err := consumePKIConfirmation(ctx, tx, grant.PKIDomainID, *confirmation, now); err != nil {
				return err
			}
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
		deadline := grant.LeaseDeadline
		if strings.TrimSpace(runtimeJSON) == "" {
			runtimeJSON = "{}"
		}
		if !json.Valid([]byte(runtimeJSON)) {
			return fmt.Errorf("%w: operation runtime context is invalid", ErrPKILifecycleInvalid)
		}
		row = storage.PKILifecycleJobRow{
			ID: "pki-op-" + operationID, PKIDomainID: grant.PKIDomainID,
			TargetType: targetType, TargetID: targetID, Kind: kind, Phase: "queued",
			State: storage.PKILifecycleJobStatePending, OperationID: "pki-op-" + operationID,
			IdempotencyKey: kind + ":" + targetType + ":" + targetID + ":" + operationID,
			RuntimeJSON:    runtimeJSON,
			LeaseOwner:     grant.InstanceID, LeaseDeadline: &deadline, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.CreatePKILifecycleJob(ctx, row); err != nil {
			return err
		}
		return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return pkiOperationFromRow(row), nil
}

func (s *InternalPKIService) transitionOperation(
	ctx context.Context,
	operationID, phase, state, lastError string,
) (PKIOperation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	grant, err := s.lease.RequirePKILease(ctx)
	if err != nil {
		return PKIOperation{}, err
	}
	var next storage.PKILifecycleJobRow
	err = s.store.WithPKITransaction(ctx, func(tx *storage.PKITransaction) error {
		if err := requirePKIEnrollmentLeaseFence(ctx, tx, grant); err != nil {
			return err
		}
		previous, found, err := tx.GetPKILifecycleJobForUpdate(ctx, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPKIOperationNotFound
		}
		if pkiLifecycleTerminal(previous.State) {
			if previous.State == strings.TrimSpace(state) {
				next = previous
				return nil
			}
			return fmt.Errorf("%w: terminal PKI operation cannot transition from %s to %s", ErrPKILifecycleConflict, previous.State, state)
		}
		if !pkiLifecycleTransitionAllowed(previous.State, strings.TrimSpace(state)) {
			return fmt.Errorf("%w: PKI operation cannot transition from %s to %s", ErrPKILifecycleConflict, previous.State, state)
		}
		next = previous
		next.Phase = strings.TrimSpace(phase)
		next.State = strings.TrimSpace(state)
		next.LastError = strings.TrimSpace(lastError)
		next.UpdatedAt = s.clock().UTC()
		if previous.State == storage.PKILifecycleJobStatePending && next.State == storage.PKILifecycleJobStateRunning {
			next.Attempt++
		}
		if err := tx.UpdatePKILifecycleJob(ctx, previous, next); err != nil {
			return err
		}
		return requirePKIEnrollmentLeaseFence(ctx, tx, grant)
	})
	if err != nil {
		return PKIOperation{}, err
	}
	return pkiOperationFromRow(next), nil
}

func pkiLifecycleTerminal(state string) bool {
	switch state {
	case storage.PKILifecycleJobStateSucceeded, storage.PKILifecycleJobStateFailed, storage.PKILifecycleJobStateCancelled:
		return true
	default:
		return false
	}
}

func pkiLifecycleTransitionAllowed(previous, next string) bool {
	switch previous {
	case storage.PKILifecycleJobStatePending:
		return next == storage.PKILifecycleJobStateRunning || next == storage.PKILifecycleJobStateBlocked || next == storage.PKILifecycleJobStateFailed || next == storage.PKILifecycleJobStateCancelled
	case storage.PKILifecycleJobStateRunning:
		return next == storage.PKILifecycleJobStateRunning || next == storage.PKILifecycleJobStateBlocked || next == storage.PKILifecycleJobStateSucceeded || next == storage.PKILifecycleJobStateFailed || next == storage.PKILifecycleJobStateCancelled
	case storage.PKILifecycleJobStateBlocked:
		return next == storage.PKILifecycleJobStateRunning || next == storage.PKILifecycleJobStateBlocked || next == storage.PKILifecycleJobStateFailed || next == storage.PKILifecycleJobStateCancelled
	default:
		return false
	}
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
	return PKIOperation{}, ErrPKIOperationNotFound
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
