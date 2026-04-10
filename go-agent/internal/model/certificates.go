package model

import "encoding/json"

type ManagedCertificateBundle struct {
	ID       int    `json:"id"`
	Domain   string `json:"domain"`
	Revision int64  `json:"revision"`
	CertPEM  string `json:"cert_pem"`
	KeyPEM   string `json:"key_pem"`
}

type ManagedCertificateACMEInfo struct {
	MainDomain string `json:"Main_Domain"`
	KeyLength  string `json:"KeyLength"`
	SANDomains string `json:"SAN_Domains"`
	Profile    string `json:"Profile"`
	CA         string `json:"CA"`
	Created    string `json:"Created"`
	Renew      string `json:"Renew"`
}

type ManagedCertificatePolicy struct {
	ID              int                        `json:"id"`
	Domain          string                     `json:"domain"`
	Enabled         bool                       `json:"enabled"`
	Scope           string                     `json:"scope"`
	IssuerMode      string                     `json:"issuer_mode"`
	Status          string                     `json:"status"`
	LastIssueAt     string                     `json:"last_issue_at"`
	LastError       string                     `json:"last_error"`
	ACMEInfo        ManagedCertificateACMEInfo `json:"acme_info"`
	Tags            []string                   `json:"tags"`
	Revision        int64                      `json:"revision"`
	Usage           string                     `json:"usage"`
	CertificateType string                     `json:"certificate_type"`
	SelfSigned      bool                       `json:"self_signed"`
}

type ManagedCertificateACMEAccountState struct {
	KeyPEM       []byte          `json:"key_pem,omitempty"`
	Registration json.RawMessage `json:"registration,omitempty"`
}

type ManagedCertificateACMERenewalState struct {
	NotAfterUnix        int64  `json:"not_after_unix,omitempty"`
	RenewAtUnix         int64  `json:"renew_at_unix,omitempty"`
	LastRenewedAtUnix   int64  `json:"last_renewed_at_unix,omitempty"`
	LastAttemptAtUnix   int64  `json:"last_attempt_at_unix,omitempty"`
	LastAttemptError    string `json:"last_attempt_error,omitempty"`
	LastAttemptStatus   string `json:"last_attempt_status,omitempty"`
	LastAttemptNotAfter int64  `json:"last_attempt_not_after,omitempty"`
}

type ManagedCertificateACMEState struct {
	Account ManagedCertificateACMEAccountState `json:"account,omitempty"`
	Renewal ManagedCertificateACMERenewalState `json:"renewal,omitempty"`
}
