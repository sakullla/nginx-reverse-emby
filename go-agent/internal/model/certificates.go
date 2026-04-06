package model

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
