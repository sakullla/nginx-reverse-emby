package model

import "time"

const (
	PKIIdentityKindAgent    = "agent"
	PKIIdentityKindListener = "listener"

	PKICertificatePurposeClient = "client_auth"
	PKICertificatePurposeServer = "server_auth"
)

// PKISecurityAcknowledgement is sent over the existing authenticated control
// channel after the corresponding snapshot is durably active. It is not a
// control-plane credential.
type PKISecurityAcknowledgement struct {
	PKIDomainID      string  `json:"pki_domain_id"`
	PKIEpoch         int64   `json:"pki_epoch"`
	SecurityRevision int64   `json:"security_revision"`
	Full             bool    `json:"full"`
	CertificateID    string  `json:"certificate_id,omitempty"`
	TrustGenerations []int64 `json:"trust_generations,omitempty"`
}

// PKITrustRoot contains public trust material only.
type PKITrustRoot struct {
	AuthorityID       string    `json:"authority_id"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	CertificatePEM    string    `json:"certificate_pem"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}

// PKISecuritySnapshot mirrors the control-plane signed security envelope. The
// Signature field is base64 encoded by encoding/json.
type PKISecuritySnapshot struct {
	PKIDomainID        string         `json:"pki_domain_id"`
	PKIEpoch           int64          `json:"pki_epoch"`
	SecurityRevision   int64          `json:"security_revision"`
	Full               bool           `json:"full"`
	IssuedAt           time.Time      `json:"issued_at"`
	TrustRoots         []PKITrustRoot `json:"trust_roots"`
	RevokedIdentityIDs []string       `json:"revoked_identity_ids"`
	RevokedSerials     []string       `json:"revoked_serials"`
	SignerGeneration   int64          `json:"signer_generation"`
	Signature          []byte         `json:"signature"`
}

// PKITunnelCredential is the public half of an enrolled tunnel identity. The
// matching private key is generated and retained by the owning agent.
type PKITunnelCredential struct {
	IdentityID           string    `json:"identity_id"`
	CertificateID        string    `json:"certificate_id"`
	Purpose              string    `json:"purpose"`
	CertificatePEM       string    `json:"certificate_pem"`
	PublicKeyFingerprint string    `json:"public_key_fingerprint_sha256"`
	AuthorityID          string    `json:"authority_id"`
	CAGeneration         int64     `json:"ca_generation"`
	NotBefore            time.Time `json:"not_before"`
	NotAfter             time.Time `json:"not_after"`
}

// PKIEnrollmentRequest is carried either by registration or by the existing
// authenticated control sync. RegisterToken deliberately does not belong to
// this value so it cannot be persisted with a pending request.
type PKIEnrollmentRequest struct {
	RequestID   string   `json:"request_id"`
	Kind        string   `json:"kind"`
	ListenerID  string   `json:"listener_id,omitempty"`
	Purpose     string   `json:"purpose"`
	CSRPEM      string   `json:"csr_pem"`
	DNSNames    []string `json:"dns_names,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
}

// PKIControlCredential correlates an authenticated enrollment response with
// the durable pending request that produced it.
type PKIControlCredential struct {
	RequestID  string              `json:"request_id"`
	Credential PKITunnelCredential `json:"credential,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type PKIControlStatus struct {
	Status       string `json:"status"`
	Code         string `json:"code,omitempty"`
	RecoveryHint string `json:"recovery_hint,omitempty"`
}

// PKIRegistrationReply is the PKI portion of /agents/register. AgentToken is
// retained for the existing control protocol and must never be written to a
// credential store.
type PKIRegistrationReply struct {
	AgentID          string              `json:"agent_id"`
	AgentToken       string              `json:"agent_token"`
	TunnelCredential PKITunnelCredential `json:"tunnel_credential"`
	SecuritySnapshot PKISecuritySnapshot `json:"security_snapshot"`
}
