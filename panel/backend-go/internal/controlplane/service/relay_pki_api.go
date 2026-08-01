package service

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

// PKIOverview is a sanitized summary of the internal relay PKI domain. It is
// deliberately separate from managed/public certificate models.
type PKIOverview struct {
	PKIDomainID      string `json:"pki_domain_id"`
	PKIEpoch         int64  `json:"pki_epoch"`
	SecurityRevision int64  `json:"security_revision"`
	UpgradeState     string `json:"upgrade_state"`
	AuthorityCount   int    `json:"authority_count"`
	IdentityCount    int    `json:"identity_count"`
	CertificateCount int    `json:"certificate_count"`
}

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
