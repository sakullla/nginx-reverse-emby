package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// PKIOverview is a sanitized summary of the internal relay PKI domain. It is
// deliberately separate from managed/public certificate models.
type PKIOverview struct {
	PKIDomainID      string              `json:"pki_domain_id"`
	PKIEpoch         int64               `json:"pki_epoch"`
	SecurityRevision int64               `json:"security_revision"`
	UpgradeState     string              `json:"upgrade_state"`
	AuthorityCount   int                 `json:"authority_count"`
	IdentityCount    int                 `json:"identity_count"`
	CertificateCount int                 `json:"certificate_count"`
	RuntimeStatus    string              `json:"runtime_status"`
	RecoveryBlocker  *PKIRecoveryBlocker `json:"recovery_blocker,omitempty"`
}

type PKIRecoveryBlocker struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	RecoveryHint string `json:"recovery_hint"`
}

var ErrPKIRuntimeUnavailable = errors.New("internal PKI runtime unavailable")

type PKIAuthorityView struct {
	ID                string     `json:"id"`
	Generation        int64      `json:"generation"`
	Status            string     `json:"status"`
	CertificatePEM    string     `json:"certificate_pem"`
	FingerprintSHA256 string     `json:"fingerprint_sha256"`
	NotBefore         time.Time  `json:"not_before"`
	NotAfter          time.Time  `json:"not_after"`
	RetireDeadline    *time.Time `json:"retire_deadline,omitempty"`
}

type PKIIdentityView struct {
	ID                   string     `json:"id"`
	Kind                 string     `json:"kind"`
	AgentID              string     `json:"agent_id"`
	ListenerID           string     `json:"listener_id,omitempty"`
	State                string     `json:"state"`
	CurrentCertificateID *string    `json:"current_certificate_id,omitempty"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	RevokedReason        string     `json:"revoked_reason,omitempty"`
}

type PKICertificateView struct {
	ID                   string     `json:"id"`
	SerialHex            string     `json:"serial_hex"`
	IdentityID           string     `json:"identity_id"`
	Purpose              string     `json:"purpose"`
	AuthorityID          string     `json:"authority_id"`
	CAGeneration         int64      `json:"ca_generation"`
	CertificatePEM       string     `json:"certificate_pem"`
	PublicKeyFingerprint string     `json:"public_key_fingerprint_sha256"`
	NotBefore            time.Time  `json:"not_before"`
	NotAfter             time.Time  `json:"not_after"`
	Status               string     `json:"status"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	RevokedReason        string     `json:"revoked_reason,omitempty"`
}

type PKIResourcePage struct {
	Overview     PKIOverview          `json:"overview"`
	Authorities  []PKIAuthorityView   `json:"authorities"`
	Identities   []PKIIdentityView    `json:"identities"`
	Certificates []PKICertificateView `json:"certificates"`
}

type PKIEventQuery struct {
	Type         string
	IdentityID   string
	SerialHex    string
	OperatorID   string
	Source       string
	Result       string
	CAGeneration *int64
	From         *time.Time
	To           *time.Time
}

type PKIActionRequest struct {
	TargetID          string `json:"target_id,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ConfirmationNonce string `json:"confirmation_nonce,omitempty"`
	Passphrase        string `json:"passphrase,omitempty"`
	Archive           []byte `json:"archive,omitempty"`
	Force             bool   `json:"force,omitempty"`
}

type PKIConfirmationRequest struct {
	Action   string `json:"action"`
	TargetID string `json:"target_id,omitempty"`
}

type PKIConfirmation struct {
	Nonce     string    `json:"nonce"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// PKIOperation references canonical PKI lifecycle work. Handlers return it in
// the same accepted-operation envelope used by other asynchronous mutations.
type PKIOperation struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	State      string         `json:"state"`
	Phase      string         `json:"phase,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	LastError  string         `json:"last_error,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
}

// PKIAPIService is the panel-facing internal PKI boundary. Implementations
// must persist mutations in canonical storage; an in-memory implementation is
// not a valid production adapter.
type PKIAPIService interface {
	Overview(context.Context) (PKIOverview, error)
	Authorities(context.Context) ([]PKIAuthorityView, error)
	Identities(context.Context) ([]PKIIdentityView, error)
	Certificates(context.Context) ([]PKICertificateView, error)
	Events(context.Context, PKIEventQuery) ([]PKIAuditEvent, error)
	Alerts(context.Context) ([]PKIDerivedAlert, error)
	CreateEnrollmentToken(context.Context, PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error)
	IssueConfirmationNonce(context.Context, PKIConfirmationRequest) (PKIConfirmation, error)
	Revoke(context.Context, PKIActionRequest) (PKIOperation, error)
	ForceRotate(context.Context, PKIActionRequest) (PKIOperation, error)
	RotateCA(context.Context, PKIActionRequest) (PKIOperation, error)
	EmergencyRotateCA(context.Context, PKIActionRequest) (PKIOperation, error)
	ExportProtected(context.Context, PKIActionRequest) (PKIOperation, error)
	ImportProtected(context.Context, PKIActionRequest) (PKIOperation, error)
	Activate(context.Context, PKIActionRequest) (PKIOperation, error)
	Operation(context.Context, string) (PKIOperation, error)
	SecuritySnapshot(context.Context, string, *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error)
}

// DegradedPKIService keeps the existing control listener and read-only PKI
// overview available while all tunnel credential and mutation capabilities
// remain failed closed. It never exposes the underlying error or filesystem
// paths through the API.
type DegradedPKIService struct {
	mu      sync.RWMutex
	blocker PKIRecoveryBlocker
	healthy *InternalPKIService
}

func NewDegradedPKIService(cause error) *DegradedPKIService {
	return &DegradedPKIService{blocker: pkiRecoveryBlocker(cause)}
}

func pkiRecoveryBlocker(cause error) PKIRecoveryBlocker {
	blocker := PKIRecoveryBlocker{
		Code:         "runtime_unavailable",
		Message:      "internal tunnel PKI runtime is unavailable",
		RecoveryHint: "inspect control-plane logs and retry after restoring the PKI runtime",
	}
	switch {
	case errors.Is(cause, ErrPKILeaseNotHeld):
		blocker = PKIRecoveryBlocker{Code: "lease_unavailable", Message: "internal tunnel PKI lease is unavailable", RecoveryHint: "verify the active control-plane instance and retry"}
	case errors.Is(cause, ErrPKIVaultInvalid):
		blocker = PKIRecoveryBlocker{Code: "vault_unavailable", Message: "internal tunnel PKI vault is unavailable", RecoveryHint: "verify the mounted master-key secret and PKI data permissions"}
	case errors.Is(cause, storage.ErrPKIInvariant), errors.Is(cause, ErrPKILifecycleInvalid):
		blocker = PKIRecoveryBlocker{Code: "canonical_state_invalid", Message: "internal tunnel PKI state requires recovery", RecoveryHint: "restore a validated protected PKI backup or repair canonical state"}
	}
	return blocker
}

// Promote switches all existing HTTP, registration, heartbeat and relay
// deletion references to one recovered runtime without replacing the control
// listener or rebuilding its router.
func (s *DegradedPKIService) Promote(healthy *InternalPKIService) {
	if s == nil || healthy == nil {
		return
	}
	s.mu.Lock()
	s.healthy = healthy
	s.mu.Unlock()
}

func (s *DegradedPKIService) SetUnavailable(cause error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.healthy = nil
	s.blocker = pkiRecoveryBlocker(cause)
	s.mu.Unlock()
}

func (s *DegradedPKIService) current() *InternalPKIService {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthy
}

func (s *DegradedPKIService) Overview(ctx context.Context) (PKIOverview, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Overview(ctx)
	}
	s.mu.RLock()
	blocker := s.blocker
	s.mu.RUnlock()
	return PKIOverview{RuntimeStatus: "degraded", RecoveryBlocker: &blocker}, nil
}

func (s *DegradedPKIService) unavailable() error { return ErrPKIRuntimeUnavailable }

func (s *DegradedPKIService) Authorities(ctx context.Context) ([]PKIAuthorityView, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Authorities(ctx)
	}
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Identities(ctx context.Context) ([]PKIIdentityView, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Identities(ctx)
	}
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Certificates(ctx context.Context) ([]PKICertificateView, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Certificates(ctx)
	}
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Events(ctx context.Context, query PKIEventQuery) ([]PKIAuditEvent, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Events(ctx, query)
	}
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Alerts(ctx context.Context) ([]PKIDerivedAlert, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Alerts(ctx)
	}
	return nil, s.unavailable()
}
func (s *DegradedPKIService) CreateEnrollmentToken(ctx context.Context, request PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.CreateEnrollmentToken(ctx, request)
	}
	return PKIEnrollmentToken{}, s.unavailable()
}
func (s *DegradedPKIService) IssueConfirmationNonce(ctx context.Context, request PKIConfirmationRequest) (PKIConfirmation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.IssueConfirmationNonce(ctx, request)
	}
	return PKIConfirmation{}, s.unavailable()
}
func (s *DegradedPKIService) Revoke(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Revoke(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ForceRotate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.ForceRotate(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) RotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.RotateCA(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) EmergencyRotateCA(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.EmergencyRotateCA(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ExportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.ExportProtected(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ImportProtected(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.ImportProtected(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) Activate(ctx context.Context, request PKIActionRequest) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Activate(ctx, request)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) Operation(ctx context.Context, operationID string) (PKIOperation, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.Operation(ctx, operationID)
	}
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) SecuritySnapshot(ctx context.Context, agentID string, acknowledgement *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.SecuritySnapshot(ctx, agentID, acknowledgement)
	}
	return storage.PKISecuritySnapshot{}, s.unavailable()
}

func (s *DegradedPKIService) EnrollLocal(ctx context.Context, request PKILocalEnrollRequest) (PKILocalEnrollmentReply, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.EnrollLocal(ctx, request)
	}
	return PKILocalEnrollmentReply{}, s.unavailable()
}

func (s *DegradedPKIService) RegisterAgent(ctx context.Context, request RegisterRequest, agent storage.AgentRow) (PKIRegistrationReply, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.RegisterAgent(ctx, request, agent)
	}
	return PKIRegistrationReply{}, s.unavailable()
}
func (s *DegradedPKIService) ControlSync(ctx context.Context, agentID string, acknowledgement *storage.PKISecurityAcknowledgement, requests []PKIControlEnrollmentRequest) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.ControlSync(ctx, agentID, acknowledgement, requests)
	}
	return storage.PKISecuritySnapshot{}, nil, s.unavailable()
}
func (s *DegradedPKIService) PrepareRelayListeners(ctx context.Context, agentID string, listeners []storage.RelayListener) ([]storage.RelayListener, error) {
	if healthy := s.current(); healthy != nil {
		return healthy.PrepareRelayListeners(ctx, agentID, listeners)
	}
	return nil, s.unavailable()
}

// RevokeListenerForDeletion is installed unconditionally. Legacy databases
// without canonical settings may delete normally; once canonical PKI exists,
// a degraded runtime fails closed until the supervisor promotes a healthy
// implementation in place.
func (s *DegradedPKIService) RevokeListenerForDeletion(ctx context.Context, transactionStore *storage.GormStore, agentID string, listenerID int) (func(), error) {
	if healthy := s.current(); healthy != nil {
		return healthy.RevokeListenerForDeletion(ctx, transactionStore, agentID, listenerID)
	}
	if transactionStore == nil || strings.TrimSpace(agentID) == "" || listenerID <= 0 {
		return nil, fmt.Errorf("%w: listener owner is invalid", ErrInvalidArgument)
	}
	state, err := transactionStore.LoadPKICanonicalState(ctx)
	if err != nil {
		return nil, err
	}
	if state.Settings == nil {
		return nil, nil
	}
	return nil, s.unavailable()
}
