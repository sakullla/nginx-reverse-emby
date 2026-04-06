package model

type Snapshot struct {
	DesiredVersion      string                     `json:"desired_version"`
	Revision            int64                      `json:"desired_revision"`
	Certificates        []ManagedCertificateBundle `json:"certificates"`
	CertificatePolicies []ManagedCertificatePolicy `json:"certificate_policies"`
}

type RuntimeState struct {
	NodeID          string            `json:"node_id,omitempty"`
	CurrentRevision int64             `json:"current_revision,omitempty"`
	Status          string            `json:"status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}
