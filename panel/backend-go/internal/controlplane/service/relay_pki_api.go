package service

import (
	"context"
	"errors"
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
	blocker PKIRecoveryBlocker
}

func NewDegradedPKIService(cause error) *DegradedPKIService {
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
	return &DegradedPKIService{blocker: blocker}
}

func (s *DegradedPKIService) Overview(context.Context) (PKIOverview, error) {
	blocker := s.blocker
	return PKIOverview{RuntimeStatus: "degraded", RecoveryBlocker: &blocker}, nil
}

func (s *DegradedPKIService) unavailable() error { return ErrPKIRuntimeUnavailable }

func (s *DegradedPKIService) Authorities(context.Context) ([]PKIAuthorityView, error) {
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Identities(context.Context) ([]PKIIdentityView, error) {
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Certificates(context.Context) ([]PKICertificateView, error) {
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Events(context.Context, PKIEventQuery) ([]PKIAuditEvent, error) {
	return nil, s.unavailable()
}
func (s *DegradedPKIService) Alerts(context.Context) ([]PKIDerivedAlert, error) {
	return nil, s.unavailable()
}
func (s *DegradedPKIService) CreateEnrollmentToken(context.Context, PKIEnrollmentTokenRequest) (PKIEnrollmentToken, error) {
	return PKIEnrollmentToken{}, s.unavailable()
}
func (s *DegradedPKIService) IssueConfirmationNonce(context.Context, PKIConfirmationRequest) (PKIConfirmation, error) {
	return PKIConfirmation{}, s.unavailable()
}
func (s *DegradedPKIService) Revoke(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ForceRotate(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) RotateCA(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) EmergencyRotateCA(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ExportProtected(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) ImportProtected(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) Activate(context.Context, PKIActionRequest) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) Operation(context.Context, string) (PKIOperation, error) {
	return PKIOperation{}, s.unavailable()
}
func (s *DegradedPKIService) SecuritySnapshot(context.Context, string, *storage.PKISecurityAcknowledgement) (storage.PKISecuritySnapshot, error) {
	return storage.PKISecuritySnapshot{}, s.unavailable()
}

func (s *DegradedPKIService) RegisterAgent(context.Context, RegisterRequest, storage.AgentRow) (PKIRegistrationReply, error) {
	return PKIRegistrationReply{}, s.unavailable()
}
func (s *DegradedPKIService) ControlSync(context.Context, string, *storage.PKISecurityAcknowledgement, []PKIControlEnrollmentRequest) (storage.PKISecuritySnapshot, []PKIControlCredential, error) {
	return storage.PKISecuritySnapshot{}, nil, s.unavailable()
}
func (s *DegradedPKIService) PrepareRelayListeners(context.Context, string, []storage.RelayListener) ([]storage.RelayListener, error) {
	return nil, s.unavailable()
}
