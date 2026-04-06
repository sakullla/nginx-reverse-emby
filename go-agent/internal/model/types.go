package model

type Snapshot struct {
	DesiredVersion string
	Revision       int64
}

type RuntimeState struct {
	NodeID          string            `json:"node_id,omitempty"`
	CurrentRevision int64             `json:"current_revision,omitempty"`
	Status          string            `json:"status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type L4Rule struct {
	Protocol   string
	RelayChain []int
}

type RelayListener struct {
	TLSMode                 string
	PinSet                  []string
	TrustedCACertificateIDs []int
}
