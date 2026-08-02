// Package pki owns tunnel private keys, enrollment replay state, credential
// generations, and the last-known-good signed security snapshot on an agent.
package pki

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var (
	ErrInvalidIdentity   = errors.New("invalid PKI storage identity")
	ErrPendingConflict   = errors.New("pending PKI enrollment conflicts with the requested identity")
	ErrPendingNotFound   = errors.New("pending PKI enrollment not found")
	ErrCredentialInvalid = errors.New("tunnel credential is invalid")
	ErrSecurityInvalid   = errors.New("PKI security snapshot is invalid")
	ErrSecurityDowngrade = errors.New("PKI security snapshot would downgrade durable state")
	ErrActiveCredential  = errors.New("active tunnel credential is unavailable")
	ErrUnsupportedDelta  = errors.New("PKI security snapshot delta is incomplete")
)

// EnrollmentSpec describes one local key/CSR owner. StorageIdentity is a
// stable, non-secret local directory name (for example "agent" or a listener
// ID); canonical IdentityID remains server-owned and is recorded only after
// enrollment succeeds. An empty DomainID and AgentID produce the deliberately
// empty CSR required for first registration of a new agent.
type EnrollmentSpec struct {
	StorageIdentity string
	DomainID        string
	AgentID         string
	Kind            string
	ListenerID      string
	Purpose         string
	DNSNames        []string
	IPAddresses     []string
}

// PendingEnrollment contains only replay-safe public request state. The
// private key is kept in the same identity's restricted pending directory and
// is never returned by this API.
type PendingEnrollment struct {
	Version              int                        `json:"version"`
	StorageIdentity      string                     `json:"storage_identity"`
	Request              model.PKIEnrollmentRequest `json:"request"`
	DomainID             string                     `json:"pki_domain_id,omitempty"`
	AgentID              string                     `json:"agent_id,omitempty"`
	RequestFingerprint   string                     `json:"request_fingerprint_sha256"`
	PublicKeyFingerprint string                     `json:"public_key_fingerprint_sha256"`
	CreatedAt            time.Time                  `json:"created_at"`
}

// CredentialExpectation binds a public credential response to its intended
// local identity. It prevents a valid certificate for another agent/listener
// from being activated in this store.
type CredentialExpectation struct {
	DomainID    string    `json:"pki_domain_id"`
	AgentID     string    `json:"agent_id"`
	Kind        string    `json:"kind"`
	ListenerID  string    `json:"listener_id,omitempty"`
	Purpose     string    `json:"purpose"`
	DNSNames    []string  `json:"dns_names,omitempty"`
	IPAddresses []string  `json:"ip_addresses,omitempty"`
	Now         time.Time `json:"-"`
}

// ActivateRequest is the complete input for one atomic credential cutover.
// RequestID selects the already-durable private key and CSR.
type ActivateRequest struct {
	StorageIdentity string
	RequestID       string
	Credential      model.PKITunnelCredential
	Security        model.PKISecuritySnapshot
	Expectation     CredentialExpectation
}

// StagedRegistration is the sanitized public response written by the join
// helper beside a pending request. The control token is stored only in
// agent.env and is deliberately absent here.
type StagedRegistration struct {
	AgentID          string                    `json:"agent_id"`
	TunnelCredential model.PKITunnelCredential `json:"tunnel_credential"`
	SecuritySnapshot model.PKISecuritySnapshot `json:"security_snapshot"`
}

type CredentialManifest struct {
	Version              int                       `json:"version"`
	Generation           string                    `json:"generation"`
	RequestID            string                    `json:"request_id"`
	RequestFingerprint   string                    `json:"request_fingerprint_sha256"`
	Credential           model.PKITunnelCredential `json:"credential"`
	PKIDomainID          string                    `json:"pki_domain_id"`
	PKIEpoch             int64                     `json:"pki_epoch"`
	SecurityRevision     int64                     `json:"security_revision"`
	SecuritySnapshotHash string                    `json:"security_snapshot_sha256"`
	Expectation          CredentialExpectation     `json:"expectation"`
	ActivatedAt          time.Time                 `json:"activated_at"`
}

type ActivePointer struct {
	Version      int       `json:"version"`
	Generation   string    `json:"generation"`
	ManifestHash string    `json:"manifest_sha256"`
	ActivatedAt  time.Time `json:"activated_at"`
}

// ActiveCredential exposes parsed in-memory TLS material, never raw private
// key bytes. Manifest and Security contain public metadata only.
type ActiveCredential struct {
	TLSCertificate tls.Certificate
	Leaf           *x509.Certificate `json:"-"`
	Manifest       CredentialManifest
	Security       model.PKISecuritySnapshot
}

// SecurityState records the durably active signed snapshot. Hash is computed
// over the exact normalized wire value stored in Snapshot.
type SecurityState struct {
	Version     int                       `json:"version"`
	Hash        string                    `json:"sha256"`
	Snapshot    model.PKISecuritySnapshot `json:"snapshot"`
	ActivatedAt time.Time                 `json:"activated_at"`
}
